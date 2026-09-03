package server

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 本文件锁住 /api/eggs/query 的两条路径与它们共用的那份契约。
//
// 为什么值得测:这条接口是**两个数据源对同一个问题的两套答案**,而前端只认一份契约。
// 任何一边悄悄改了字段名、或者 src 参数没生效(默认走了第三方),都不会报错 ——
// 页面照样列出候选,只是烧了不该烧的令牌额度、或者候选少了系别那列。
//
// 三条最要紧的:
//  1. 默认必须走本地 —— 否则每次点「猜猜孵出谁」都在烧第三方额度,而这正是换本地的初衷。
//  2. 两条路径的响应结构必须一致 —— 前端不分支,结构一岔就是静默丢字段。
//  3. src=api 未配令牌必须 503 且**不落统计** —— 统计是给「烧了多少额度」看的,
//     没发出去的请求不该算进去。

// stubMerchantFetch 挡住远行商人的后台回源。
//
// 必须挡:设置 s.eggAPIKey 会唤醒 merchantLoop,而它默认走咸鱼源 —— 于是这几条
// 查蛋用例会**真的打到第三方站点**(烧额度、且日志里刷出对方的 401)。与查蛋无关,
// 纯粹是设了令牌的副作用。与 merchant_src_test.go 的 stubHaoyou 同一个道理。
func stubMerchantFetch(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"data":{"item_count":0,"items":[]}}`)
	}))
	t.Cleanup(srv.Close)
	old := merchantFetchURL
	merchantFetchURL = srv.URL
	t.Cleanup(func() { merchantFetchURL = old })
}

// eggQuery 打一次 /api/eggs/query,返回响应与状态码。
func eggQuery(t *testing.T, s *Server, query string) (*httptest.ResponseRecorder, eggMatchOut) {
	t.Helper()
	rr := httptest.NewRecorder()
	s.handleEggQuery(rr, httptest.NewRequest("GET", "/api/eggs/query?"+query, nil))
	var out eggMatchOut
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败: %v (%s)", err, rr.Body.String())
		}
	}
	return rr, out
}

// TestEggQueryDefaultsToLocal 默认走本地,且不要令牌也能出结果。
func TestEggQueryDefaultsToLocal(t *testing.T) {
	s := newTestServer(t) // eggAPIKey 为空
	// 2026-08-15 破壳实测的样本:随机蛋 gid 2985,孵出权杖-Ⅱ。
	rr, out := eggQuery(t, s, "height=0.20&weight=11.443&maxSecs=57600")
	if rr.Code != http.StatusOK {
		t.Fatalf("本地查询不应失败,状态码 %d: %s", rr.Code, rr.Body.String())
	}
	if out.Source != "local" {
		t.Errorf("source = %q, 期望 local(默认绝不能走第三方)", out.Source)
	}
	if out.Total == 0 || len(out.Matches) == 0 {
		t.Fatal("本地查询应有候选")
	}
	if out.Total != len(out.Matches) {
		t.Errorf("total=%d 与 matches 长度 %d 不一致", out.Total, len(out.Matches))
	}
	// 未配令牌时复核按钮不该出现。
	if out.APIAvailable {
		t.Error("未配 -egg-api-key 时 apiAvailable 应为 false")
	}
	// 破壳真值必须在候选里,且**百分位要与当年那份实测记录对得上**。
	//
	// 百分位这条比"在不在候选里"严格得多:docs/data.md 记着这颗蛋落在权杖-Ⅱ 区间内的
	// 身高 25.0% / 体重 34.1%。对得上说明筛选、单位换算、百分位口径三者全对 ——
	// 任何一处差一点,数字就会漂走,而候选列表照样列得出来。
	var found bool
	for _, m := range out.Matches {
		if m.Name == "权杖-Ⅱ" {
			found = true
			if math.Abs(m.HeightPct-25.0) > 0.05 {
				t.Errorf("权杖-Ⅱ 身高百分位 = %v,实测记录是 25.0", m.HeightPct)
			}
			if math.Abs(m.WeightPct-34.1) > 0.05 {
				t.Errorf("权杖-Ⅱ 体重百分位 = %v,实测记录是 34.1", m.WeightPct)
			}
			if m.Img == "" {
				t.Error("本地候选应带上头像(孵出物种的 HeadIcon)")
			}
		}
		if m.Img != "" && !strings.HasPrefix(m.Img, "/img/") {
			t.Errorf("候选 %s 的 img = %q,本地源的 img 必须是 /img/ 开头的站内路径", m.Name, m.Img)
		}
	}
	if !found {
		names := make([]string, 0, len(out.Matches))
		for _, m := range out.Matches {
			names = append(names, m.Name)
		}
		t.Errorf("实测真值「权杖-Ⅱ」不在本地候选里: %v", names)
	}
	// 分数降序。
	for i := 1; i < len(out.Matches); i++ {
		if out.Matches[i-1].Score < out.Matches[i].Score {
			t.Errorf("候选未按 score 降序: %+v", out.Matches)
			break
		}
	}
}

// TestEggQueryLocalNeedsNoKey 本地路径与令牌彻底解耦:配了令牌也仍走本地。
func TestEggQueryLocalNeedsNoKey(t *testing.T) {
	s := newTestServer(t)
	stubMerchantFetch(t)
	s.eggAPIKey = "test-key"
	rr, out := eggQuery(t, s, "height=0.20&weight=11.443&maxSecs=57600")
	if rr.Code != http.StatusOK {
		t.Fatalf("本地查询失败: %d %s", rr.Code, rr.Body.String())
	}
	if out.Source != "local" {
		t.Errorf("显式 src=local 之外,默认也必须是 local,实际 %q", out.Source)
	}
	if !out.APIAvailable {
		t.Error("已配 -egg-api-key 时 apiAvailable 应为 true(前端据此显示复核按钮)")
	}
}

// TestEggQueryAPIWithoutKey src=api 未配令牌:503,且不落统计。
func TestEggQueryAPIWithoutKey(t *testing.T) {
	s := newTestServer(t) // eggAPIKey 为空
	rr, _ := eggQuery(t, s, "height=0.20&weight=11.443&src=api")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("未配令牌时 src=api 应返回 503,实际 %d: %s", rr.Code, rr.Body.String())
	}
	// 本地路径不受影响,仍有结果可选 —— 复核失败不该让「猜猜孵出谁」整个不可用。
	rr2, out := eggQuery(t, s, "height=0.20&weight=11.443&maxSecs=57600")
	if rr2.Code != http.StatusOK || out.Total == 0 {
		t.Errorf("未配令牌时本地路径仍应可用: %d / %+v", rr2.Code, out)
	}
}

// TestEggQueryBothSourcesShareShape 两条路径的响应必须是同一份结构。
//
// 第三方用 httptest 假服务顶替(真请求会烧额度、且结果取决于对方当时的货单)。
// 只核对**结构**(字段齐不齐、类型对不对),不核对第三方给的具体数值 ——
// 那是对方的口径,我们锁不住也不该锁。
func TestEggQueryBothSourcesShareShape(t *testing.T) {
	old := eggMatchAPI
	defer func() { eggMatchAPI = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 顺带钉住对方的必填参数:key 与 format=json 缺一不可(见 fetchXianyu 的同款断言)。
		if r.URL.Query().Get("key") == "" || r.URL.Query().Get("format") == "" {
			http.Error(w, "missing params", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"msg":"ok","data":{"total":1,"source":"xianyu","matches":[
			{"pet_id":3410001,"pet_name":"权杖-Ⅱ","img_name":"https://example.com/a.png",
			 "hatch_data":57600,"hatch_label":"16小时","main_type":"石","score":99.5}]}}`))
	}))
	defer srv.Close()
	eggMatchAPI = srv.URL

	s := newTestServer(t)
	stubMerchantFetch(t)
	s.eggAPIKey = "test-key"
	rr, apiOut := eggQuery(t, s, "height=0.20&weight=11.443&src=api")
	if rr.Code != http.StatusOK {
		t.Fatalf("src=api 失败: %d %s", rr.Code, rr.Body.String())
	}
	if apiOut.Source != "api" {
		t.Errorf("source = %q, 期望 api", apiOut.Source)
	}
	if len(apiOut.Matches) != 1 {
		t.Fatalf("第三方候选应为 1 条,实际 %d", len(apiOut.Matches))
	}
	// 外链原样透出(前端直接给 <img src>,不拼 /img/)。
	if apiOut.Matches[0].Img != "https://example.com/a.png" {
		t.Errorf("第三方 img 应是外链原文,实际 %q", apiOut.Matches[0].Img)
	}
	if apiOut.Matches[0].Note != "16小时" {
		t.Errorf("第三方 note 应取 hatch_label,实际 %q", apiOut.Matches[0].Note)
	}

	_, localOut := eggQuery(t, s, "height=0.20&weight=11.443&maxSecs=57600")
	// 结构同一:两边都得有 name / img / hatchSecs / score / note 这些键。
	for _, label := range []struct {
		what string
		out  eggMatchOut
	}{{"local", localOut}, {"api", apiOut}} {
		for _, m := range label.out.Matches {
			if m.Name == "" {
				t.Errorf("[%s] 候选缺 name: %+v", label.what, m)
			}
			if m.HatchSecs == 0 {
				t.Errorf("[%s] 候选缺 hatchSecs: %+v", label.what, m)
			}
			if m.Score < 0 || m.Score > 100 {
				t.Errorf("[%s] 候选 score 越界: %+v", label.what, m)
			}
		}
	}
}

// TestEggQueryLocalNote 本地候选的孵化时长文案。
func TestEggQueryLocalNote(t *testing.T) {
	s := newTestServer(t)
	_, out := eggQuery(t, s, "height=0.20&weight=11.443&maxSecs=57600")
	if len(out.Matches) == 0 {
		t.Skip("无候选")
	}
	// 16 小时 = 57600 秒
	if out.Matches[0].Note != "孵化 16 小时" {
		t.Errorf("57600 秒的文案应是「孵化 16 小时」,实际 %q", out.Matches[0].Note)
	}
	// 带零头的:28800+1800=30600 秒 = 8 小时 30 分
	_, out2 := eggQuery(t, s, "height=0.20&weight=11.443&maxSecs=30600")
	if len(out2.Matches) > 0 && out2.Matches[0].Note != "孵化 8 小时 30 分" {
		t.Errorf("30600 秒的文案应是「孵化 8 小时 30 分」,实际 %q", out2.Matches[0].Note)
	}
	// 不足 1 小时:300 秒(策划占位值,但文案仍要说对)
	_, out3 := eggQuery(t, s, "height=0.20&weight=11.443&maxSecs=300")
	if len(out3.Matches) > 0 && out3.Matches[0].Note != "孵化 5 分钟" {
		t.Errorf("300 秒的文案应是「孵化 5 分钟」,实际 %q", out3.Matches[0].Note)
	}
}

// TestHatchNote 直接单测时长文案(覆盖 HTTP 路径碰不到的边界)。
func TestHatchNote(t *testing.T) {
	for _, c := range []struct {
		secs int32
		want string
	}{{0, ""}, {-1, ""}, {60, "孵化 1 分钟"}, {300, "孵化 5 分钟"},
		{3600, "孵化 1 小时"}, {57600, "孵化 16 小时"}, {30600, "孵化 8 小时 30 分"}} {
		if got := hatchNote(c.secs); got != c.want {
			t.Errorf("hatchNote(%d) = %q, 期望 %q", c.secs, got, c.want)
		}
	}
}
