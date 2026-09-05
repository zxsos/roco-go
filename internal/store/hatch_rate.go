package store

import (
	"encoding/json"
	"math"
	"sort"
)

// 孵化倍率的后端估算(见 docs/data.md 3.6 与 web/src/pages/eggs/hatch.js)。
//
// 倍率 = 每过 1 真实秒推进多少「孵化秒」。平时 1 倍,「孵蛋加速日」活动期间是 2 或 5 倍,
// 玩家跑动、用孵化宝典还会再加。它**没有任何可读的配置字段**(活动倍率只存在于文案里,
// 且不随协议下发),只能从相邻两次 hatched_secs 采样的差分反推。
//
// —— 为什么要挪到后端 ——
//
// 前端也能做差分,但它手里**通常只有一次采样**:蛋的进度只在开孵蛋器(0x0312)、
// 开背包(0x1344)时由服务器下发,没有被动推送。玩家打开页面时,后端库里躺着的就只有
// 最后一次快照 —— 只有一次采样就没有差分可言,前端只能退回保守的 1 倍,于是加速日
// (实测 5 倍)把预计完成时间报成 5 倍远。这正是用户报的「时间不对」。
//
// 后端则**全程看得见**每一次下发:实时抓包时逐包累积,离线回放时整份 pcap 一次跑完。
// 差分在这里随手可得,算好的倍率随 /api/eggs 下发给前端即可 —— 前端一次采样都不用等。
//
// —— 为什么存样本数组取中位数,而不是单个值 ——
//
// 服务器不是平滑推进的:2026-09-05 那份 pcap 的 22 个差分里 19 个精确 5.00,
// 却混着 16.9 / 25.8 / 130.0 三个异常(服务器偶尔批量补齐一大段)。单个值会被
// 这些跳变带跑;取最近若干个样本的中位数则把它们滤掉,得到稳定的 5.0。

// hatchSamples 是保留的样本数。取 9:够让中位数把偶发跳变滤掉(异常占比约 1/7,
// 9 个里混进 1~2 个也不改中位数),又不至于让倍率切换(活动开始/结束)后迟迟收敛。
const hatchSamples = 9

// hatchMinSamples 是最少要攒够几个样本才肯给出倍率。
//
// 3 是能滤掉**单发**异常的最小值:[130, 5, 5] 的中位数是 5,而只有 1~2 个时中位
// 数仍被那一个异常主导(2 个时取平均,[130, 5] 得 67.5)。这件事有实测必要 ——
// 2026-09-05 那份 pcap 的差分里确实混着 16.9 / 25.8 / 130.0 三个跳变,若赶在攒够
// 样本前就把某一次跳变当真,预计完成时间会虚报成 20 倍(钳制后)那么离谱。
// 攒不够时返回 0(未知),前端退回保守的 1 倍:宁可慢,也不要虚报「可破壳」。
const hatchMinSamples = 3

// hatchRateMin/Max 倍率的合理取值域,与前端 clampRate 同一把尺子。
//
// 下限 1:除「加速」没见过别的方向(宝典/跑动都是加),<1 只可能是异常采样。
// 上限 20:实测见过的最大等效是跑动期间 14.5,给到 20 留余量;再高就是异常
// (时钟跳变、采样错配),钳住免得外推出离谱的完成时间。
const (
	hatchRateMin = 1.0
	hatchRateMax = 20.0
)

// HatchRate 返回当前倍率估计;样本不足(攒不够 hatchMinSamples 个)时返回 0,
// 由调用方按「未知」处理 —— 宁可让前端退回保守的 1 倍,也不要拿一两次可能失真的
// 采样当真(理由见 hatchMinSamples)。
func (sc *Scoped) HatchRate() float64 {
	var blob string
	if err := sc.rdb.QueryRow(`SELECT samples FROM hatch_rate WHERE account=?`, sc.account).Scan(&blob); err != nil {
		return 0
	}
	var xs []float64
	if json.Unmarshal([]byte(blob), &xs) != nil || len(xs) < hatchMinSamples {
		return 0
	}
	return median(xs)
}

// AddHatchSample 收一次差分样本:钳进合理区间、追加到队尾、截掉最老的,再写回。
//
// 调用方负责判断这次采样是否有效(v 严格递增、未孵满),这里只管记。
func (sc *Scoped) AddHatchSample(rate float64) error {
	if math.IsNaN(rate) {
		return nil
	}
	rate = math.Min(hatchRateMax, math.Max(hatchRateMin, rate))
	var blob string
	_ = sc.rdb.QueryRow(`SELECT samples FROM hatch_rate WHERE account=?`, sc.account).Scan(&blob)
	var xs []float64
	_ = json.Unmarshal([]byte(blob), &xs)
	xs = append(xs, rate)
	if len(xs) > hatchSamples {
		xs = xs[len(xs)-hatchSamples:]
	}
	out, err := json.Marshal(xs)
	if err != nil {
		return err
	}
	_, err = sc.db.Exec(`INSERT INTO hatch_rate(account, samples, updated_at) VALUES(?,?,unixepoch())
		ON CONFLICT(account) DO UPDATE SET samples=excluded.samples, updated_at=excluded.updated_at`,
		sc.account, string(out))
	return err
}

// median 取中位数(调用方保证非空)。
func median(xs []float64) float64 {
	ys := append([]float64(nil), xs...)
	sort.Float64s(ys)
	n := len(ys)
	if n%2 == 1 {
		return ys[n/2]
	}
	return (ys[n/2-1] + ys[n/2]) / 2
}
