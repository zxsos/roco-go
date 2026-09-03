package gamedata

import (
	"math"
	"sort"
)

// 随机蛋(神奇的蛋)的候选物种反推(算法见 docs/data.md「随机蛋的区间藏在哪」)。
//
// 随机蛋 conf_id = 0,查不到 PET_EGG_CONF 行,客户端自己也看不到区间。但**物种在下蛋时
// 就已确定,只是对客户端隐藏**,两个线索还在包里:
//   - max_hatched_secs:只能来自 PET_EGG_CONF[隐藏 conf_id].hatch_data(普通蛋客户端自己
//     查表,随机蛋只能由服务器下发),这是**有实测支撑的一维**;
//   - height/weight:按那个隐藏物种的蛋区间滚出来的。
//
// 故候选 = hatch_data == max_hatched_secs 且身高体重落在区间内的全部蛋配置行。
//
// 2026-08-15 的实测(唯一一例随机蛋破壳):h=0.20m / w=11.443kg / 57600s 的蛋孵出
// 权杖-Ⅱ —— 正是本函数给出的 3 个候选之一(另两个是布是石、首领布是石),
// 见 TestMatchRandomEggKnownHatch。

// EggCandidate 是随机蛋的一个候选物种。
type EggCandidate struct {
	ConfID    uint32  `json:"confId"`        // 物种 conf_id(即隐藏的 PET_EGG_CONF id)
	Name      string  `json:"name"`          // 物种名(孵出来是谁)
	Img       string  `json:"img,omitempty"` // 头像相对路径(前端拼 /img/);无图时为空
	HatchSecs int32   `json:"hatchSecs"`     // 该物种的孵化时长(= 入参 maxSecs)
	Score     float64 `json:"score"`         // 匹配度 0-100,越大越可能是它
	HeightPct float64 `json:"heightPct"`     // 蛋的身高落在该物种区间内的百分位
	WeightPct float64 `json:"weightPct"`     // 同上,体重
}

// MatchRandomEgg 反推随机蛋可能孵出的物种,按匹配度降序返回。
//
// 入参用的是**展示单位**(米 / 千克 / 秒):前端 EggView 里的 heightM/weightKg/maxSecs
// 正是这三个,无需换算。内部按 PET_EGG_CONF 的原始刻度(身高 ×100、体重 ×1000)比对,
// 浮点转整数一律四舍五入 —— 前端那两个数是 float64 除法来的,直接截断会因 0.1 的表示
// 误差把边界值挤出区间。
//
// maxSecs 为 0(蛋上没有孵化时长,只有从未入孵的随机蛋会这样)时不做时长筛选,
// 退化为纯尺寸匹配 —— 会宽得多,但仍比不猜好。
func (db *DB) MatchRandomEgg(heightM, weightKg float64, maxSecs int32) []EggCandidate {
	h := toEggScale(heightM, 100)
	w := toEggScale(weightKg, 1000)
	var out []EggCandidate
	for conf, c := range db.eggConf {
		if maxSecs != 0 && c.HatchSecs != maxSecs {
			continue
		}
		if c.HeightLow > h || h > c.HeightHigh {
			continue
		}
		if c.WeightLow > w || w > c.WeightHigh {
			continue
		}
		out = append(out, EggCandidate{
			ConfID:    conf,
			Name:      c.Name,
			Img:       db.eggCandidateImg(conf),
			HatchSecs: c.HatchSecs,
			Score:     matchScore(c, h, w),
			HeightPct: spanPct(c.HeightLow, c.HeightHigh, h),
			WeightPct: spanPct(c.WeightLow, c.WeightHigh, w),
		})
	}
	// 同分时按 conf_id 升序:map 遍历次序随机,不定序则每次刷新候选顺序都在跳。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ConfID < out[j].ConfID
	})
	return out
}

// eggCandidateImg 取候选物种的头像;异色/炫彩蛋孵的是异色个体,但随机蛋无从知道品类,
// 故一律取普通图(随机蛋品类为 0,与 fillType 的推断一致)。
func (db *DB) eggCandidateImg(conf uint32) string {
	base, _, ok := db.PetBaseOf(conf)
	if !ok || base == 0 {
		return ""
	}
	return db.PetImageByBase(base, false).Head
}

// matchScore 算匹配度 0-100:**离区间中心越近越高**。
//
// 尺寸是在区间内均匀滚的,所以真值落在哪都可能 —— 打分只是把「最像的那个」排前面,
// 不是概率。合成测试(每维独立均匀、全表做池)下真值进前 3 只有约一半,
// 故这个分数**只能用来排序,不能用来断言「就是它」**。
func matchScore(c EggConf, h, w int32) float64 {
	return 100 * (1 - (spanDev(c.HeightLow, c.HeightHigh, h) + spanDev(c.WeightLow, c.WeightHigh, w)))
}

// toEggScale 把展示单位(米/千克)换算回 PET_EGG_CONF 的整数刻度(×100 / ×1000)。
//
// **必须四舍五入,不能截断**:前端那两个数是 float64 除法来的(heightM = int/100),
// 而 0.29*100 在 IEEE754 下是 28.999999999999996 —— 直接截断得 28,于是身高 29 的蛋
// 会被筛出「下沿正好是 29」的那个区间,候选里凭空少掉真值。这类错误只在边界值上出现,
// 页面照样列得出候选,谁也不会想到是换算少了个 Round(见 TestToEggScaleRoundTrip)。
func toEggScale(v float64, scale int32) int32 {
	return int32(math.Round(v * float64(scale)))
}

// spanDev 返回 x 落在 [lo,hi] 内的位置到区间中心的偏离(0=正中,0.5=贴边)。
// 退化区间(lo==hi)视为正中 —— 这种行只有唯一取值,落进来就是它。
func spanDev(lo, hi, x int32) float64 {
	if hi <= lo {
		return 0
	}
	return math.Abs(float64(x-lo)/float64(hi-lo) - 0.5)
}

// spanPct 返回 x 在 [lo,hi] 内的百分位(0-100);退化区间给 100。
func spanPct(lo, hi, x int32) float64 {
	if hi <= lo {
		return 100
	}
	return float64(x-lo) / float64(hi-lo) * 100
}
