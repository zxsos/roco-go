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

	// 逐次喂入(每次 +10 秒、进度 +50 → 差分恒为 5),并在攒够最小样本前断言「未知」。
	// 攒不够就返回 0 是**刻意的**:样本太少时中位数会被单发跳变主导,宁可让前端退回
	// 保守的 1 倍(理由见 hatchMinSamples 与 TestHatchRateNeedsMinSamples)。
	for i := int64(0); i < hatchMinSamples+1; i++ {
		if err := sc.UpsertEggs([]*pet.EggView{
			{Gid: 80001, ItemID: 107028, Hatching: true,
				HatchedSecs: int32(100 + 50*i), MaxSecs: 28800},
		}, base+10*i, nil); err != nil {
			t.Fatalf("UpsertEggs#%d: %v", i, err)
		}
		// i=0 是首次采样(手里只有新值,没有旧值可比)→ 0 个样本
		if n := int64(i); n < hatchMinSamples-1 {
			if got := sc.HatchRate(); got != 0 {
				t.Errorf("只攒了 %d 个样本时应返回 0(未知),实得 %v", n, got)
			}
		}
	}
	if got := sc.HatchRate(); math.Abs(got-5) > 1e-9 {
		t.Errorf("攒够样本后应估出 5 倍,实得 %v(后端估不出,前端就只能退回 1 倍)", got)
	}
}

// TestHatchRateNeedsMinSamples 守「攒不够样本不许给倍率」。
//
// 2026-09-05 那份 pcap 的差分里混着 16.9 / 25.8 / 130.0 三个跳变。若赶在攒够样本
// 前就把某一次跳变当真,预计完成时间会虚报成 20 倍(钳制后)那么离谱 —— 玩家会看到
// 「马上可破壳」而实际还早。攒不够时返回 0,前端退回保守的 1 倍。
func TestHatchRateNeedsMinSamples(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAccount(testAcc, "测试账号"); err != nil {
		t.Fatalf("建账号: %v", err)
	}
	sc := st.For(testAcc)
	const base = int64(1700000000)

	// 首次采样(建立旧值)+ 一次 130 倍的跳变 = 只攒到 1 个样本
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80041, ItemID: 107028, Hatching: true, HatchedSecs: 100, MaxSecs: 28800},
	}, base, nil); err != nil {
		t.Fatalf("UpsertEggs#1: %v", err)
	}
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80041, ItemID: 107028, Hatching: true, HatchedSecs: 230, MaxSecs: 28800},
	}, base+1, nil); err != nil { // 130/1 = 130 倍 → 钳到 20
		t.Fatalf("UpsertEggs#2: %v", err)
	}
	// 只有 1 个样本时若照给,就是 20 倍 —— 会把 ETA 虚报成 20 倍快
	if got := sc.HatchRate(); got != 0 {
		t.Errorf("样本不足时应返回 0 而不是拿跳变当真,实得 %v(会虚报成 20 倍)", got)
	}

	// 补 1 个正常样本 → 共 2 个:[20, 5] 的中位数是 12.5,**仍被那次跳变主导**。
	// 这一档必须继续返回 0 —— 它正是「最少要 3 个」的全部理由:2 个样本不足以
	// 滤掉单发异常,照给就会把 ETA 虚报成 2.5 倍快。
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80041, ItemID: 107028, Hatching: true, HatchedSecs: 280, MaxSecs: 28800},
	}, base+11, nil); err != nil { // (280-230)/10 = 5 倍
		t.Fatalf("UpsertEggs#3: %v", err)
	}
	if got := sc.HatchRate(); got != 0 {
		t.Errorf("2 个样本时中位数仍被跳变主导([20,5]→12.5),应继续返回 0,实得 %v", got)
	}

	// 再补 1 个 → 共 3 个:[20, 5, 5] 的中位数是 5,跳变被滤掉
	if err := sc.UpsertEggs([]*pet.EggView{
		{Gid: 80041, ItemID: 107028, Hatching: true, HatchedSecs: 330, MaxSecs: 28800},
	}, base+21, nil); err != nil {
		t.Fatalf("UpsertEggs#4: %v", err)
	}
	if got := sc.HatchRate(); math.Abs(got-5) > 1e-9 {
		t.Errorf("凑够 3 个后中位数应回到 5(滤掉 20 的跳变),实得 %v", got)
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

	// 先攒够最小样本数(每次 +10 秒、进度 +50 → 差分恒为 5)
	for i := int64(0); i <= hatchMinSamples; i++ {
		if err := sc.UpsertEggs([]*pet.EggView{
			{Gid: 80011, ItemID: 107028, Hatching: true,
				HatchedSecs: int32(100 + 50*i), MaxSecs: 28800},
		}, base+10*i, nil); err != nil {
			t.Fatalf("UpsertEggs#%d: %v", i, err)
		}
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

	// 慢得离谱:每次 100000 秒只推进 900 秒 → 0.009 倍,不钳就会把进度条拖到几乎不动
	for i := int64(0); i <= hatchMinSamples; i++ {
		if err := sc.UpsertEggs([]*pet.EggView{
			{Gid: 80031, ItemID: 107028, Hatching: true,
				HatchedSecs: int32(100 + 900*i), MaxSecs: 28800},
		}, base+100000*i, nil); err != nil {
			t.Fatalf("UpsertEggs 慢#%d: %v", i, err)
		}
	}
	if got := sc.HatchRate(); got != hatchRateMin {
		t.Errorf("倍率应被钳到下限 %v,实得 %v", hatchRateMin, got)
	}

	// 快得离谱:1 秒推进 10000 秒 → 不钳会外推出离谱的完成时间
	st2 := newTestStore(t)
	if err := st2.UpsertAccount(testAcc, "测试账号"); err != nil {
		t.Fatalf("建账号: %v", err)
	}
	sc2 := st2.For(testAcc)
	// 步长取 1000 而非更大:进度得留在 maxSecs 以内,否则会被「已孵满」规则排除、
	// 攒不够样本(那正是 TestHatchRateIgnoresNonAdvancing 守的那条)。
	for i := int64(0); i <= hatchMinSamples; i++ {
		if err := sc2.UpsertEggs([]*pet.EggView{
			{Gid: 80032, ItemID: 107028, Hatching: true,
				HatchedSecs: int32(100 + 1000*i), MaxSecs: 28800},
		}, base+i, nil); err != nil {
			t.Fatalf("UpsertEggs 快#%d: %v", i, err)
		}
	}
	if got := sc2.HatchRate(); got != hatchRateMax {
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
