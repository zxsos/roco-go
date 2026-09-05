// 孵化进度的本地外推。
//
// 服务器只在下发那一刻给出 hatchedSecs(以及它的计算时刻 hatchUpdate),之后不再推送
// (进度只在开孵蛋器/开背包时才下发,没有被动推送)。要让页面上的进度条动起来,只能本地按
// 「当前值 + 倍率 × 已过秒数」外推。
//
// —— 倍率从哪来:后端给,前端不估 ——
//
// 倍率由两部分构成,**性质不同、来源不同**:
//
//	活动倍率  = 每周「孵蛋加速日」(北京时间周五 04:00 ~ 周一 04:00)→ 5 倍,否则 1 倍
//	在线加成  = 玩家移动 / 挂风场 / 孵化宝典,只在**在线**时叠加
//
// 活动倍率**不靠测量**:它是固定的每周时间表,服务器在窗口内照此推进,**玩家离线时
// 也一样**。故后端按时刻直接算出来下发(见 internal/pet.HatchActivityRate),离线
// 外推因此天然准确 —— 不需要攒样本,也没有「刚打开页面还没校准」的冷启动。
//
// 在线加成**不进 ETA**:它随速度与移动方式而变(实测同一份 pcap:静止时差分精确
// 5.00,移动时 16.9~25.8),样本不足以定出可信的数,拿它外推会虚报「可破壳」。故后端
// 只下发一个布尔(玩家此刻是否在移动),页面作**定性提示**:进度会比标注的更快。
//
// 这条划分是踩过坑才明确的。早先把两者混成一个数去「测」(对相邻两次进度下发做差分、
// 取中位数),结果是:样本被移动时段的加速污染(16.9/130 混在里面)、需要攒够 3 个
// 样本才敢给值(冷启动慢)、离线那段完全算不了 —— 都不是噪声问题,是把两个不同性质
// 的量当成了一个。详见 docs/data.md 3.6。

// RATE_MIN 是倍率的下限,只用于防御后端数据异常(如字段缺失被解成 0)。
// 除了「加速」没见过别的方向,倍率 <1 只可能是数据出错。
const RATE_MIN = 1

// clampRate 把倍率收进合理区间(仅作防御,正常路径下后端给的就是 1 或 5)。
export function clampRate(r) {
  // 先处理 NaN(它不参与任何大小比较,单独挡掉);±Infinity 走下面的大小比较
  // 自然落到下限或保持不变,不必单列 —— 用 Number.isFinite 一并判非法会把 +∞
  // 也塞到下限,而它明明不该是 1(测试因此抓到过一次)。
  if (Number.isNaN(r)) return RATE_MIN
  if (r < RATE_MIN) return RATE_MIN
  return r
}

// hatchProgress 返回 {pct, secs, rate} —— 外推到 now(毫秒)的孵化进度;
// 不在孵蛋器里返回 null。
//
// rate 是**后端**给的孵化倍率(每过 1 真实秒推进多少孵化秒)。它按加速日时间表算出,
// 离线也准;玩家此刻若在移动,实际会更快(见文件头),但那部分不进本函数的外推 ——
// 宁可慢一点,也不要虚报「可破壳」。
//
// ⚠️ **secs 是「孵化秒」,不是真实秒**:它是进度条用的量纲(与 maxSecs 同一把尺子),
// 加速期间 1 真实秒推进 rate 个孵化秒。要算「还要多久」的**真实**时间,必须除以 rate
// (见 EggList 的 etaText)—— 直接拿 maxSecs − secs 当秒数,5 倍加速下就会把
// 完成时刻报成 5 倍远,而 1 倍时两者恰好相等、看不出来。
//
// **没有采样就返回 null,绝不外推**:hatchUpdate 为 0 表示这颗蛋从未有过进度采样
// (登录包 0x0102 只给「哪些蛋在孵」、不带逐蛋进度,进度要等开孵蛋器 0x0312 或开
// 背包 0x1344)。此时若按 elapsed = now - 0 外推,得到的是十几亿秒,进度直接顶满 ——
// 而 EggList 把「在孵且进度满」的蛋两栏都不显示,蛋就这样凭空消失了。
// 返回 null 时页面按「在孵、进度未知」处理(见 EggList 的分栏与 EggCard 的展示)。
export function hatchProgress(egg, now, rate) {
  if (!egg || !egg.hatching || !egg.maxSecs) return null
  if (!egg.hatchUpdate) return null // 无采样:不外推,免得算成 100% 把蛋弄丢
  const r = clampRate(rate || 1)
  const elapsed = Math.max(0, Math.floor(now / 1000) - egg.hatchUpdate)
  const secs = Math.min(egg.maxSecs, (egg.hatchedSecs || 0) + elapsed * r)
  const pct = Math.floor(Math.min(100, (secs / egg.maxSecs) * 100))
  return { pct, secs, rate: r }
}

// remainRealSecs 把「还剩多少孵化秒」折算成**真实**秒数。
//
// 它是 etaText/etaTitle 唯一该用的换算:进度条看孵化秒,倒计时看真实秒,两者
// 差一个 rate。抽出来是为了让这个换算只有一处 —— 分写两遍必有一处漏掉。
export function remainRealSecs(egg, p) {
  if (!egg || !p) return 0
  return Math.max(0, (egg.maxSecs - p.secs) / (p.rate || 1))
}
