package store

import (
	"math"
	"testing"

	"github.com/whoisnian/rocom-capture/internal/pet"
)

// TestHatchRateFromSamples 守「后端能从相邻两次下发的差分里估出孵化倍率」。
//
// 这是加速日预计完成时间不再报成 5 倍远的关键:前端通常只拿到最后一次快照、
// 只有一次采样,凑不出差分,只能退回 1 倍(实测回放页 title 长期挂着「倍率尚未测出」)。
// 后端则看得见每一次下发,差分随手可得 —— 本用例钉住这条链路:两次 UpsertEggs
// 之间进度涨了 50 秒、时钟走了 10 秒,倍率就该是 5。
func TestHatchRateFromSamples(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAccount(testAcc, "测试账号"); err != nil {
		t.Fatalf("建账号: %v", err)
	}
	sc := st.For(testAcc)
	const base = int64(1700000000)

	// 第一次下发:进度 100 秒,时刻 base
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80001, ItemID: 107028, Hatching: true, HatchedSecs: 100, MaxSecs: 28800},
	}, base, nil); err != nil {
		t.Fatalf("UpsertEggs#1: %v", err)
	}
	// 只有一次采样 → 估不出来(不能凭空给个 1 充当已知值)
	if got := sc.HatchRate(); got != 0 {
		t.Errorf("只有一次采样时应返回 0(未知),实得 %v", got)
	}

	// 第二次下发:10 秒后进度涨到 150 → 差分 50/10 = 5 倍
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80001, ItemID: 107028, Hatching: true, HatchedSecs: 150, MaxSecs: 28800},
	}, base+10, nil); err != nil {
		t.Fatalf("UpsertEggs#2: %v", err)
	}
	if got := sc.HatchRate(); math.Abs(got-5) > 1e-9 {
		t.Errorf("两次采样应估出 5 倍,实得 %v(后端估不出,前端就只能退回 1 倍)", got)
	}
}

// TestHatchRateIgnoresNonAdvancing 守两条会让倍率失真的采样:
//   - 进度没推进(服务器没重算,「新 t 配旧 v」是常态)→ 不算,否则得 0 倍再被钳到下限 1;
//   - 已孵满(进度被 maxSecs 截断)→ 不算,差分必然偏小,会把中位数拉低。
func TestHatchRateIgnoresNonAdvancing(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAccount(testAcc, "测试账号"); err != nil {
		t.Fatalf("建账号: %v", err)
	}
	sc := st.For(testAcc)
	const base = int64(1700000000)

	// 先建立一次采样:5 倍
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80011, ItemID: 107028, Hatching: true, HatchedSecs: 100, MaxSecs: 28800},
	}, base, nil); err != nil {
		t.Fatalf("UpsertEggs#1: %v", err)
	}
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80011, ItemID: 107028, Hatching: true, HatchedSecs: 150, MaxSecs: 28800},
	}, base+10, nil); err != nil {
		t.Fatalf("UpsertEggs#2: %v", err)
	}
	want := sc.HatchRate()
	if math.Abs(want-5) > 1e-9 {
		t.Fatalf("前置:应先估出 5 倍,实得 %v", want)
	}

	// 进度没推进(v 相同、t 前进):不得把倍率打成 1
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80011, ItemID: 107028, Hatching: true, HatchedSecs: 150, MaxSecs: 28800},
	}, base+30, nil); err != nil {
		t.Fatalf("UpsertEggs#3: %v", err)
	}
	if got := sc.HatchRate(); math.Abs(got-want) > 1e-9 {
		t.Errorf("进度没推进时倍率不该变,期望仍 %v,实得 %v", want, got)
	}

	// 已孵满的蛋:进度被截断,不得参与(否则把中位数从 5 拉低)
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80012, ItemID: 107028, Hatching: true, HatchedSecs: 28795, MaxSecs: 28800},
	}, base, nil); err != nil {
		t.Fatalf("UpsertEggs#4: %v", err)
	}
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80012, ItemID: 107028, Hatching: true, HatchedSecs: 28800, MaxSecs: 28800},
	}, base+60, nil); err != nil {
		t.Fatalf("UpsertEggs#5: %v", err)
	}
	if got := sc.HatchRate(); math.Abs(got-5) > 1e-9 {
		t.Errorf("已孵满的蛋不得进样本(差分被截断),期望仍 5,实得 %v", got)
	}
}

// TestHatchRateClamped 守倍率被钳进 [1, 20]。
//
// 没有这条,差分一旦异常(时钟跳变、采样错配、服务器回退进度),倍率能把进度条
// 拖死(<1)或吹飞(几十倍)。实测见过的最大等效是跑动期间 14.5,故上限给到 20;
// 下限是 1 —— 除了「加速」没见过别的方向。
func TestHatchRateClamped(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAccount(testAcc, "测试账号"); err != nil {
		t.Fatalf("建账号: %v", err)
	}
	sc := st.For(testAcc)
	const base = int64(1700000000)

	// 慢得离谱:1 秒只推进 0 秒以上一点点 → 不钳就会把进度条拖到几乎不动
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80031, ItemID: 107028, Hatching: true, HatchedSecs: 100, MaxSecs: 28800},
	}, base, nil); err != nil {
		t.Fatalf("UpsertEggs#1: %v", err)
	}
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80031, ItemID: 107028, Hatching: true, HatchedSecs: 1000, MaxSecs: 28800},
	}, base+100000, nil); err != nil { // 900/100000 = 0.009 倍
		t.Fatalf("UpsertEggs#2: %v", err)
	}
	if got := sc.HatchRate(); got < hatchRateMin {
		t.Errorf("倍率应被钳到下限 %v,实得 %v", hatchRateMin, got)
	}

	// 快得离谱:1 秒推进 10000 秒 → 不钳会外推出离谱的完成时间
	st2 := newTestStore(t)
	if err := st2.UpsertAccount(testAcc, "测试账号"); err != nil {
		t.Fatalf("建账号: %v", err)
	}
	sc2 := st2.For(testAcc)
	if err := sc2.UpsertEggs([]*pet.EggView{
		{Gid: 80032, ItemID: 107028, Hatching: true, HatchedSecs: 100, MaxSecs: 28800},
	}, base, nil); err != nil {
		t.Fatalf("UpsertEggs#3: %v", err)
	}
	if err := sc2.UpsertEggs([]*pet.EggView{
		{Gid: 80032, ItemID: 107028, Hatching: true, HatchedSecs: 10100, MaxSecs: 28800},
	}, base+1, nil); err != nil { // 10000/1 = 10000 倍
		t.Fatalf("UpsertEggs#4: %v", err)
	}
	if got := sc2.HatchRate(); got > hatchRateMax {
		t.Errorf("倍率应被钳到上限 %v,实得 %v", hatchRateMax, got)
	}
}

// TestHatchRateMedianFiltersJumps 守中位数抗跳变。
// 2026-09-05 那份 pcap 的 22 个差分里 19 个精确 5.00,却混着 16.9 / 25.8 / 130.0
// 三个异常(服务器偶尔批量补齐一大段)。取最近样本的中位数应把它们滤掉,回到 5。
func TestHatchRateMedianFiltersJumps(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAccount(testAcc, "测试账号"); err != nil {
		t.Fatalf("建账号: %v", err)
	}
	sc := st.For(testAcc)
	const base = int64(1700000000)

	// 逐次喂入实测序列(5,5,5,5,5,5,130):填满样本窗口后中位数应仍是 5
	seq := []struct{ v int32; dt int64 }{
		{100, 0}, {105, 1}, {110, 2}, {115, 3}, {120, 4}, {125, 5}, {255, 6},
	}
	for _, s := range seq {
		if err := sc.UpsertEggs([]*pet.EggView{
			{Gid: 80021, ItemID: 107028, Hatching: true, HatchedSecs: s.v, MaxSecs: 28800},
		}, base+s.dt, nil); err != nil {
			t.Fatalf("UpsertEggs v=%d: %v", s.v, err)
		}
	}
	// 样本里混进了 130(125→255 只隔 1 秒),但多数是 5 → 中位数必须是 5
	if got := sc.HatchRate(); math.Abs(got-5) > 1e-9 {
		t.Errorf("中位数应滤掉 130 的跳变回到 5,实得 %v", got)
	}
}
