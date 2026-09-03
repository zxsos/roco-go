package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// 随机蛋「猜猜孵出谁」:优先用**本地解包数据**反推,第三方图鉴 API 降级为可选复核。
//
// 两条路径共用一份响应契约(eggMatchOut),前端不必分支:
//   - src=local(默认):按 PET_EGG_CONF 反推(见 gamedata.MatchRandomEgg 与
//     docs/data.md「随机蛋的区间藏在哪」)。零外部依赖、无限流、离线可用,
//     但没有系别(系别只在协议 GetSkillDamType 里,配置表没有)。
//   - src=api:代理第三方图鉴。需要 -egg-api-key,限流 10 次/分钟。
//     比本地多给系别,代价是要钱、要令牌、会限流。
//
// 本地之所以够用:第三方返回的 hatch_data / height_range / weight_range 正是
// PET_EGG_CONF 的列,它读的是同一张表。唯一一例随机蛋破壳实测(2026-08-15)
// 本地算法给出的候选与破壳结果一致(见 gamedata 的 TestMatchRandomEggKnownHatch)。

// var 而非 const:测试要把它换成 httptest 假服务(真请求会烧第三方额度,
// 且断言取决于对方当时的货单)。
var eggMatchAPI = "https://apii.xianyuw.cn/api/v1/rocom-incubate"

// eggMatchOut 是两条路径共用的响应契约。
type eggMatchOut struct {
	Source       string          `json:"source"`       // "local" 或 "api"
	Total        int             `json:"total"`        // 候选条数(= len(matches))
	Matches      []eggMatchEntry `json:"matches"`      // 按匹配度降序
	APIAvailable bool            `json:"apiAvailable"` // 是否已配 -egg-api-key(= 复核按钮要不要显示)
}

// eggMatchEntry 是统一后的单条候选。
//
// img 是**可直接赋给 <img src> 的完整值**,不是相对路径:本地给 /img/ 开头的站内路径,
// 第三方给外链 URL。候选列表是本契约唯一的消费方,统一成"拿来即用"省掉前端分支。
type eggMatchEntry struct {
	Name      string  `json:"name"`                // 物种名
	Img       string  `json:"img,omitempty"`       // 头像;无图时为空
	HatchSecs int32   `json:"hatchSecs"`           // 孵化时长(秒)
	Score     float64 `json:"score"`               // 匹配度 0-100(仅用于排序,不是概率)
	HeightPct float64 `json:"heightPct,omitempty"` // 蛋身高在该物种区间内的百分位(第三方不提供)
	WeightPct float64 `json:"weightPct,omitempty"` // 同上,体重
	ConfID    uint32  `json:"confId,omitempty"`    // 物种 conf_id(第三方的 pet_id 口径未必相同,勿跨源比较)
	Note      string  `json:"note,omitempty"`      // 补充说明:本地给孵化时长文案,第三方给 hatch_label
}

// handleEggQuery 查随机蛋可能孵出的物种。
//
// 参数:
//
//	height=  蛋身高(米)      weight=  蛋体重(千克)
//	maxSecs= 孵满所需秒数    src=     local(默认) | api
//
// 三者都来自前端 EggView,可省略(缺 maxSecs 时本地退化成纯尺寸匹配,会宽得多)。
// src=api 且未配令牌时返回 503;本地路径永不因缺令牌失败。
func (s *Server) handleEggQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	height := parseFloat(q.Get("height"))
	weight := parseFloat(q.Get("weight"))
	maxSecs := parseInt32(q.Get("maxSecs"))

	if q.Get("src") == "api" {
		s.handleEggQueryAPI(w, r, q.Get("height"), q.Get("weight"))
		return
	}

	cands := s.db.MatchRandomEgg(height, weight, maxSecs)
	matches := make([]eggMatchEntry, 0, len(cands))
	for _, c := range cands {
		img := ""
		if c.Img != "" {
			img = "/img/" + c.Img
		}
		matches = append(matches, eggMatchEntry{
			Name:      c.Name,
			Img:       img,
			HatchSecs: c.HatchSecs,
			Score:     c.Score,
			HeightPct: c.HeightPct,
			WeightPct: c.WeightPct,
			ConfID:    c.ConfID,
			Note:      hatchNote(c.HatchSecs),
		})
	}
	writeJSON(w, eggMatchOut{
		Source:       "local",
		Total:        len(matches),
		Matches:      matches,
		APIAvailable: s.eggAPIKey != "",
	})
}

// hatchNote 把孵化秒数说成人话;0 或异常值不给文案(宁可不写,也别写错)。
func hatchNote(secs int32) string {
	if secs <= 0 {
		return ""
	}
	if secs < 3600 {
		return fmt.Sprintf("孵化 %d 分钟", secs/60)
	}
	h, m := secs/3600, (secs%3600)/60
	if m == 0 {
		return fmt.Sprintf("孵化 %d 小时", h)
	}
	return fmt.Sprintf("孵化 %d 小时 %d 分", h, m)
}

// handleEggQueryAPI 代理第三方图鉴(可选复核路径),把对方的结构翻成统一契约。
// 统计:只要「发起了第三方请求」就记一条(未配 key 或请求没发出去不记),
// 见 store.LogEggQuery 与管理面板「查蛋 API 统计」。
func (s *Server) handleEggQueryAPI(w http.ResponseWriter, r *http.Request, height, weight string) {
	// 统一埋点:ok 表示拿到第三方正常 JSON,matches 仅在成功时有效。
	start := time.Now()
	sent := false
	ok := false
	matches := 0
	defer func() {
		if !sent {
			return
		}
		s.store.LogEggQuery(s.acct(r), ok, int(time.Since(start).Milliseconds()), matches, height, weight)
	}()

	if s.eggAPIKey == "" {
		http.Error(w, "服务端未配置查询令牌(启动时加 -egg-api-key);可用 src=local 走本地数据",
			http.StatusServiceUnavailable)
		return
	}
	params := url.Values{}
	params.Add("key", s.eggAPIKey)
	params.Add("format", "json")
	if height != "" {
		params.Add("height", height)
	}
	if weight != "" {
		params.Add("weight", weight)
	}
	params.Add("implemented", "true")

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eggMatchAPI+"?"+params.Encode(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "查询第三方失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	sent = true                                               // 已发起第三方请求,之后的成败都要计入统计
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 上限 1MB,防第三方异常大响应
	if err != nil {
		http.Error(w, "读取第三方响应失败", http.StatusBadGateway)
		return
	}
	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("第三方返回 %d: %s", resp.StatusCode, body), resp.StatusCode)
		return
	}
	// 校验 JSON 结构,别把错误页透传给前端。
	var out eggMatchResp
	if err := json.Unmarshal(body, &out); err != nil {
		http.Error(w, "第三方响应解析失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	ok = true
	matches = out.Data.Total

	entries := make([]eggMatchEntry, 0, len(out.Data.Matches))
	for _, m := range out.Data.Matches {
		e := eggMatchEntry{
			Name:      m.PetName,
			Img:       m.ImgName,
			HatchSecs: int32(m.HatchData),
			Score:     m.Score,
			Note:      m.HatchLabel,
		}
		if m.PetID > 0 && m.PetID <= 1<<32-1 {
			e.ConfID = uint32(m.PetID)
		}
		entries = append(entries, e)
	}
	writeJSON(w, eggMatchOut{
		Source:       "api",
		Total:        len(entries),
		Matches:      entries,
		APIAvailable: true,
	})
}

// eggMatchResp 第三方响应的外层结构(仅用于解析,不透传给前端)。
type eggMatchResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Matches []eggMatchPet `json:"matches"`
		Total   int           `json:"total"`
		Source  string        `json:"source"`
	} `json:"data"`
}

// eggMatchPet 第三方单条匹配结果(字段与第三方约定一致)。
type eggMatchPet struct {
	PetID              int64   `json:"pet_id"`
	PetName            string  `json:"pet_name"`
	ImgName            string  `json:"img_name"`
	SourcePetName      string  `json:"source_pet_name"`
	ChainRootName      string  `json:"chain_root_name"`
	HeightRange        string  `json:"height_range"`
	WeightRange        string  `json:"weight_range"`
	HatchData          int64   `json:"hatch_data"`
	HatchLabel         string  `json:"hatch_label"`
	MainType           string  `json:"main_type"`
	SubType            *string `json:"sub_type"`
	Score              float64 `json:"score"`
	MatchedMetricCount int     `json:"matched_metric_count"`
	IsImplemented      bool    `json:"is_implemented"`
}

// parseFloat 解析可选的浮点查询参数;空或非法都当 0(MatchRandomEgg 对 0 有定义)。
func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseInt32 解析可选的整数查询参数;空或非法都当 0。
func parseInt32(s string) int32 {
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0
	}
	return int32(v)
}
