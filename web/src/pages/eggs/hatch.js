// 孵化进度的本地外推。
//
// 服务器只在下发那一刻给出 hatchedSecs(以及它的计算时刻 hatchUpdate),之后不再推送
// (进度只在开孵蛋器/开背包时才下发,没有被动推送)。要让页面上的进度条动起来,只能本地按
// 「当前值 + 倍率 × 已过秒数」外推。
//
// **倍率不是常数**:平时 1 倍,「孵蛋加速日」活动期间是 2 或 5 倍,另外玩家跑动、
// 用孵化宝典都会再加。配置里没有可读的倍率字段(活动倍率只存在于文案里,且不随
// 协议下发),所以只能靠采样反推。详见 docs/data.md 3.6。
//
// —— 为什么用「差分」而不是「累计 ÷ 已孵时长」——
//
// 2026-09 三份 pcap、15 个采样点实测:
//   - 相邻两次采样的差分 Δv/Δt:**6 个采样点对全部精确 5.00**;
//   - 单点累计 v/(hatchUpdate−start_hatch_time):刚放入时是 5.00,但玩家中途跑动过
//     之后变成 **8.10 ~ 9.03**(同一时刻的差分仍是 5.00)。
//
// 原因:单点算的是**入孵以来的历史平均**,跑动那段的加速被平摊进去了;
// 差分算的是**采样当时的瞬时速率**。预测未来该用后者 —— 玩家此刻站着,
// 未来大概率还是站着,拿历史平均外推会虚报「可破壳」。
//
// 故:差分是唯一的主口径,单点**只作兜底且要保守**(见 hatchRate 内注释)。

// (页面上不再写这段说明:进度条本身就是估的,一句话说不清,写在这里给读代码的人。
//  游戏内打开一次孵蛋器,后端就能收到新的 hatchedSecs,这里随即对齐。)

// RATE_MIN/MAX 倍率的合理取值域,差分结果一律钳进这个区间。
//
// 下限 1:除了「加速」没见过别的方向(宝典/跑动都是加),倍率 <1 只可能是异常采样
// (如 hatchedSecs 被服务器回退、或两次采样跨了取出再放入)。原实现允许任意小的正数,
// 会把进度条拖到几乎不动。
// 上限 20:实测见过的最大等效是跑动期间 14.5(文档 3.6);给到 20 留余量,
// 再高就是异常(时钟跳变、采样错配),钳住免得外推出离谱的完成时间。
const RATE_MIN = 1
const RATE_MAX = 20

// 每颗蛋记住上一次见到的 (hatchUpdate, hatchedSecs, rate),据此估倍率。
// 只在本模块内存里留一份:刷新页面即回到保守的 1 倍,不会把错误估计持久化。
const seen = new Map()

// clampRate 把估出的倍率收进合理区间。
export function clampRate(r) {
  // 先处理 NaN(它不参与任何大小比较,单独挡掉);±Infinity 走下面的大小比较
  // 自然落到两端(+∞ → 上限,−∞ → 下限),不必单列 —— 用 Number.isFinite 一并
  // 判非法会把它俩都塞到下限,而 +∞ 明明该是上限(测试因此抓到过一次)。
  if (Number.isNaN(r)) return RATE_MIN
  if (r < RATE_MIN) return RATE_MIN
  if (r > RATE_MAX) return RATE_MAX
  return r
}

// gatherRates 收集本次刷新里**所有**蛋的瞬时倍率,返回其**中位数**。
//
// 倍率是全局的(实测:同一时刻三颗蛋——含一颗 16h 的、两颗 8h 的——在 2 秒内
// 全部 +10s,即统一 5.00),故可跨蛋聚合:单颗蛋的采样抖动(秒级取整、恰在
// 服务器刷新边界上取到)会在中位数里被消掉,比盯着一颗蛋稳。
// 只有 1~2 颗蛋时中位数退化为均值/较小值,同样可用。
//
// ⚠️ 必须在渲染各张卡片**之前**调:它读的是上一次采样留下的 seen,而
// hatchProgress 一调用就把 seen 推进到本次了,顺序反了凑不出任何差分。
export function gatherRates(eggs) {
  const rates = []
  for (const e of eggs || []) {
    if (!e || !e.hatching || !e.hatchUpdate || !e.maxSecs) continue
    const prev = seen.get(e.gid)
    const cur = { t: e.hatchUpdate, v: e.hatchedSecs }
    // 必须**严格**递增才算一次有效采样,理由同 hatchRate:进度没变时照算会得 0,
    // 混进中位数会把整体拉到下限(服务器没重算进度是常态,不是异常)。
    if (!prev || cur.t <= prev.t || cur.v <= prev.v) continue
    // 已孵满的蛋进度被 maxSecs 截断,差分必然偏小 —— 混进来会把中位数整体拉低,
    // 而这颗蛋恰恰是最不需要估倍率的:它已经孵完了。
    if (prev.v >= e.maxSecs || cur.v >= e.maxSecs) continue
    rates.push(clampRate((cur.v - prev.v) / (cur.t - prev.t)))
  }
  if (rates.length === 0) return null
  rates.sort((a, b) => a - b)
  const mid = rates.length >> 1
  return rates.length % 2 ? rates[mid] : (rates[mid - 1] + rates[mid]) / 2
}

// hatchRate 收下一次采样,返回 {rate, known}:rate 是估出的倍率(秒/秒),
// known 表示它是**实测出来的**还是退回来的一倍(后者不能拿去预测未来)。
//
// 主口径是**相邻两次采样的差分**;只有一次采样时(刚刷新页面、或这颗蛋刚入孵)
// 退回 1 倍 —— 不用「累计 ÷ 已孵时长」兜底:实测它会因玩家跑动高估到 9 倍,
// 宁可显示得慢些,也不要虚报可破壳(这一条与改动前一致,现由实测支撑)。
function hatchRate(egg) {
  const prev = seen.get(egg.gid)
  const cur = { t: egg.hatchUpdate, v: egg.hatchedSecs }
  if (prev && prev.t === cur.t) return prev
  // 必须**严格**递增才是一次有效采样。原判定是 `cur.v >= prev.v`,于是「服务器
  // 还没重算进度」会被算成 Δv=0 → 0 倍 → 钳到下限 1:把 5 倍加速生生打成 1 倍。
  // 而这是常态不是异常 —— 进度只在开孵蛋器/开背包时下发,连着刷两次页面很常见,
  // 后端的 HatchUpdate 又统一成了观测时刻,每次刷新都是新的 t 配同一个 v。
  if (prev && cur.t > prev.t && cur.v > prev.v) {
    // 原实现 `r > 0 ? r : prev.rate` 会放行任意小的正数,且无上限。
    // 这里钳进 [1, 20]:异常采样(进度回退/时钟跳变)不至于把进度条拖死或吹飞。
    cur.rate = clampRate((cur.v - prev.v) / (cur.t - prev.t))
    cur.known = true
  } else if (prev) {
    // 采样没推进(进度倒退,或进度没变):沿用上次估的,不重新算。
    cur.rate = prev.rate
    cur.known = prev.known
  }
  seen.set(egg.gid, cur)
  return cur
}

// hatchProgress 返回 {pct, secs, rate, rateKnown} —— 外推到 now(毫秒)的孵化进度;
// 不在孵蛋器里返回 null。
//
// sharedRate 是跨蛋中位数(见 gatherRates),给「自己还没测出倍率」的蛋兜底:
// 刚刷新页面、或这颗蛋刚入孵时,单看它只有一次采样,退回 1 倍会把加速日的预计
// 时间报成几倍远。它只兜底、**不覆盖**这颗蛋自己测出的值 —— 自己的差分是对这颗
// 蛋最直接的证据,而宝典之类是否逐蛋生效尚未证实(见 docs/data.md 3.6)。
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
export function hatchProgress(egg, now, sharedRate) {
  if (!egg || !egg.hatching || !egg.maxSecs) return null
  if (!egg.hatchUpdate) return null // 无采样:不外推,免得算成 100% 把蛋弄丢
  const own = hatchRate(egg)
  // rateKnown 决定页面能不能把预计时间当真:没测出倍率时的 1 倍只是占位,
  // 加速日会偏慢好几倍,与其给个看似精确的数,不如让人知道还没校准。
  const rateKnown = own.known || !!sharedRate
  const rate = own.known ? own.rate : (sharedRate || 1)
  const elapsed = Math.max(0, Math.floor(now / 1000) - egg.hatchUpdate)
  const secs = Math.min(egg.maxSecs, (egg.hatchedSecs || 0) + elapsed * rate)
  const pct = Math.floor(Math.min(100, (secs / egg.maxSecs) * 100))
  return { pct, secs, rate, rateKnown }
}

// remainRealSecs 把「还剩多少孵化秒」折算成**真实**秒数。
//
// 它是 etaText/etaTitle 唯一该用的换算:进度条看孵化秒,倒计时看真实秒,两者
// 差一个 rate。抽出来是为了让这个换算只有一处 —— 分写两遍必有一处漏掉。
export function remainRealSecs(egg, p) {
  if (!egg || !p) return 0
  return Math.max(0, (egg.maxSecs - p.secs) / (p.rate || 1))
}
