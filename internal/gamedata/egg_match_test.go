package gamedata

import (
	"math"
	"testing"
)

// 本文件验证随机蛋候选反推(MatchRandomEgg,见 egg_match.go 与 docs/data.md)。
//
// 为什么值得测:这条链路的**唯一实测样本只有一例**(2026-08-15 那份 pcap 里随机蛋
// gid 2985 破壳),而筛选靠的是三条件联立(孵化时长 + 身高区间 + 体重区间)——
// 少一个条件、或单位换算差一位(米/厘米、千克/克),候选集就整个变了。
// 这种错误**没有任何报错**:页面照样列出一堆候选,玩家看不出对错。

// TestMatchRandomEggKnownHatch 锁定那唯一一例破壳实测。
//
// 样本:随机蛋 gid 2985,height=20(÷100 米)、weight=11443(÷1000 千克)、
// max_hatched_secs=57600,孵出**权杖-Ⅱ**(conf_id 3410001,hatch_data 57600)。
// 期望:候选里真值在、且不是靠"放过所有行"混进来的(候选数要少而准)。
func TestMatchRandomEggKnownHatch(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 入参用展示单位,与前端 EggView 的 heightM/weightKg/maxSecs 完全一致
	got := db.MatchRandomEgg(0.20, 11.443, 57600)
	if len(got) == 0 {
		t.Fatal("一个候选都没给:筛选条件多半写错了")
	}
	const wantConf, wantName = uint32(3410001), "权杖-Ⅱ"
	var rank int
	for i, c := range got {
		if c.ConfID == wantConf {
			rank = i + 1
			break
		}
	}
	if rank == 0 {
		names := make([]string, 0, len(got))
		for _, c := range got {
			names = append(names, c.Name)
		}
		t.Fatalf("真值 %s(%d) 不在候选里: %v", wantName, wantConf, names)
	}
	// 候选集要收得住:若条件放行太多,猜就没有意义了。
	if len(got) > 12 {
		t.Errorf("候选 %d 条,太多(该样本实测 3 条),筛选条件可能漏了一维", len(got))
	}
	// 时长维是唯一有实测支撑的:候选必须清一色等于入参时长。
	for _, c := range got {
		if c.HatchSecs != 57600 {
			t.Errorf("候选 %s(%d) 的 hatchSecs=%d, 应恒等于入参 57600", c.Name, c.ConfID, c.HatchSecs)
		}
	}
	// 分数只用于排序,必须是降序且落在 0-100。
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Errorf("候选未按 score 降序: %v", got)
			break
		}
	}
}

// TestMatchRandomEggFilters 逐个验证三个筛选维度,以及退化情形。
func TestMatchRandomEggFilters(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	base := db.MatchRandomEgg(0.20, 11.443, 57600)
	if len(base) == 0 {
		t.Fatal("基准样本无候选,后续断言无从谈起")
	}

	// 时长改一档(16h):候选集必须整体换掉 —— 说明时长维确实在筛。
	other := db.MatchRandomEgg(0.20, 11.443, 57600+14400)
	for _, c := range other {
		if c.HatchSecs == 57600 {
			t.Errorf("入参 72000s 时仍给出 57600s 的候选 %s:时长维没生效", c.Name)
		}
	}

	// 尺寸甩到人体不可能的范围:应当一个都不剩。
	if got := db.MatchRandomEgg(999, 999999, 57600); len(got) != 0 {
		t.Errorf("离谱尺寸应无候选,实际 %d 条", len(got))
	}

	// maxSecs=0(从未入孵的随机蛋):不做时长筛选,退化成纯尺寸匹配,
	// 结果必须是「带时长筛选」的超集 —— 少一维只能更宽,不能更窄。
	wide := db.MatchRandomEgg(0.20, 11.443, 0)
	if len(wide) < len(base) {
		t.Errorf("maxSecs=0 的候选(%d) 少于带时长的(%d):退化方向反了", len(wide), len(base))
	}
}

// TestToEggScaleRoundTrip 验证「整数 → 展示单位 → 整数」这条往返不丢值。
//
// 为什么单独测:前端传来的 heightM/weightKg 是 float64(int/100) 与 float64(int/1000),
// 后端要还原成整数才能跟配置区间比。若还原时截断而非四舍五入,0.29*100=28.999…
// 会变成 28,边界上的蛋就被筛出自己的区间 —— **候选列表照样列得出来,只是真值不见了**。
// 这类错误只在特定数值上出现,人工点几下页面根本碰不到。
func TestToEggScaleRoundTrip(t *testing.T) {
	bad := 0
	for h := int32(0); h <= 300; h++ { // 蛋身高实测全在 1~100 厘米量级,放宽到 300
		if got := toEggScale(float64(h)/100, 100); got != h && bad < 5 {
			t.Errorf("身高 %d -> %v -> %d, 往返丢值", h, float64(h)/100, got)
			bad++
		}
	}
	for w := int32(0); w <= 200000; w += 7 { // 步长取样,覆盖到 200 千克
		if got := toEggScale(float64(w)/1000, 1000); got != w && bad < 10 {
			t.Errorf("体重 %d -> %v -> %d, 往返丢值", w, float64(w)/1000, got)
			bad++
		}
	}
	// 点名几个实测会踩坑的值(截断即错),免得将来有人"顺手优化"掉 Round。
	for _, c := range []struct {
		v     float64
		scale int32
		want  int32
	}{{0.29, 100, 29}, {11.443, 1000, 11443}, {0.07, 100, 7}, {3.645, 1000, 3645}} {
		if got := toEggScale(c.v, c.scale); got != c.want {
			t.Errorf("toEggScale(%v, %d) = %d, 期望 %d", c.v, c.scale, got, c.want)
		}
	}
}

// TestMatchScoreAndSpan 单测打分与偏离这两个**纯函数**。
// 不从 db.eggConf 里挑样本:map 遍历次序随机,挑到哪个不确定,断言就成了碰运气。
func TestMatchScoreAndSpan(t *testing.T) {
	// 区间 [10,20] 上:正中 15 偏离 0,两端 10/20 偏离 0.5。
	if got := spanDev(10, 20, 15); got != 0 {
		t.Errorf("正中 spanDev = %v, 期望 0", got)
	}
	if got := spanDev(10, 20, 20); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("上沿 spanDev = %v, 期望 0.5", got)
	}
	if got := spanDev(10, 20, 10); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("下沿 spanDev = %v, 期望 0.5", got)
	}
	// 退化区间(lo==hi):只有唯一取值,落进来就是它 —— 偏离 0、百分位 100,不许除零。
	if got := spanDev(5, 5, 5); got != 0 {
		t.Errorf("退化区间 spanDev = %v, 期望 0(不许除零)", got)
	}
	if got := spanPct(5, 5, 5); got != 100 {
		t.Errorf("退化区间 spanPct = %v, 期望 100", got)
	}

	mid := EggConf{HeightLow: 10, HeightHigh: 20, WeightLow: 1000, WeightHigh: 2000}
	if got := matchScore(mid, 15, 1500); math.Abs(got-100) > 1e-9 {
		t.Errorf("两维都在正中应得 100,实际 %v", got)
	}
	if got := matchScore(mid, 20, 2000); math.Abs(got) > 1e-9 {
		t.Errorf("两维都贴边应得 0,实际 %v", got)
	}
	// 一维正中一维贴边 → 50:两维等权,别悄悄加权。
	if got := matchScore(mid, 15, 2000); math.Abs(got-50) > 1e-9 {
		t.Errorf("一维正中一维贴边应得 50,实际 %v", got)
	}
}

// TestMatchRandomEggScoreRange 扫全表验证输出的数值健全性:分数落在 0-100、
// 百分位落在 0-100、无 NaN。
//
// NaN 尤其要挡:sort.Slice 的比较里出现 NaN 会得到"随机但稳定"的顺序,
// 页面每次刷新候选顺序都在跳,而没有任何报错。
func TestMatchRandomEggScoreRange(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	n := 0
	for conf, c := range db.eggConf {
		// 每个配置行都拿它自己的区间中点去查,保证一定有候选返回。
		got := db.MatchRandomEgg(
			float64(c.HeightLow+c.HeightHigh)/2/100,
			float64(c.WeightLow+c.WeightHigh)/2/1000,
			c.HatchSecs)
		for _, cand := range got {
			n++
			if math.IsNaN(cand.Score) || cand.Score < 0 || cand.Score > 100 {
				t.Errorf("conf %d: 分数越界 %v", conf, cand.Score)
			}
			if math.IsNaN(cand.HeightPct) || cand.HeightPct < 0 || cand.HeightPct > 100 {
				t.Errorf("conf %d: 身高百分位越界 %v", conf, cand.HeightPct)
			}
			if math.IsNaN(cand.WeightPct) || cand.WeightPct < 0 || cand.WeightPct > 100 {
				t.Errorf("conf %d: 体重百分位越界 %v", conf, cand.WeightPct)
			}
			if cand.HatchSecs != c.HatchSecs {
				t.Errorf("conf %d: 候选时长 %d != 入参 %d", conf, cand.HatchSecs, c.HatchSecs)
			}
		}
	}
	if n == 0 {
		t.Fatal("全表扫下来一个候选都没有:筛选条件多半写错了")
	}
}
