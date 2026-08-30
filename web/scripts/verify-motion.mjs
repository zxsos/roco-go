// 实时地图「箭头平滑移动」的回归度量。
//
//   node scripts/verify-motion.mjs
//
// 存在理由:motion.js 里那几个常数(EXTRAP_TAU / GLIDE_RATIO / TAU_RATIO …)此前全靠
// "跑一把看看手感"来调,改坏了没人知道 —— 每帧最大位移从 0.8px 劣化到 20px 也不会有任何
// 测试报警。本脚本用**真实抓包**驱动真实的 motion.js,把"平不平"变成可比较的数字。
//
// 做法:
//  1. fixture 是 137 个真实移动包(经 pipeline 的 ParseMoveReq 解析 + gamedata 同款投影,
//     即 buildPos 推给前端的形状),见 fixtures/motion-packets.json 的来源说明;
//  2. 真值 = 包位置 + move_seg_list 补报的轨迹点(带服务器时间戳),按时间线性插值;
//  3. 以 60fps 跑 useMapEngine.applyFrame 的同款公式,逐帧比对真值。
//
// 三个指标各有分工,缺一个就会被"拆东墙补西墙"蒙混过去:
//   - 偏差(米):箭头离玩家真实位置多远 —— 总体准不准;
//   - 每帧位移最大值(米):单帧跳多远 —— 抽搐/瞬移;
//   - 抽搐帧数:超过真实最大速度对应步长的帧数 —— 抖动频率。
//
// 门槛略放宽于当前实测值(约 15%),避免机器抖动造成假红;但**劣化超过门槛就会失败**。
// 若某次改动有意让数字变差(换取别的收益),请连同理由一起更新门槛,不要删断言。
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const raw = JSON.parse(fs.readFileSync(path.join(here, 'fixtures/motion-packets.json'), 'utf8'))
const M = await import(path.join(here, '../src/pages/map/motion.js'))

const SIDE = raw.side      // 场景边长(厘米)
const PK = raw.pkts
const FPS = 60
const toM = (d) => (d * SIDE) / 100 // 归一化坐标差 → 米

// —— 真值时序:补报轨迹点(服务器时间戳)+ 包位置,按时间插值 ——
// to_pos 比末个 seg 略新(实测差 0.2-0.6 个采样步长),故强制它不早于末 seg。
function buildTruth() {
  const pts = []
  for (const p of PK) {
    if (p.path && p.path.length >= 2) {
      let lastT = -Infinity
      for (const q of p.path) {
        const t = Math.max(q.t, lastT + 1e-3)
        pts.push({ t, u: q.u, v: q.v })
        lastT = t
      }
      pts.push({ t: Math.max(p.t, lastT + 0.05), u: p.u, v: p.v })
    } else {
      pts.push({ t: p.t, u: p.u, v: p.v })
    }
  }
  pts.sort((a, b) => a.t - b.t)
  for (let i = 1; i < pts.length; i++) if (pts[i].t <= pts[i - 1].t) pts[i].t = pts[i - 1].t + 1e-3
  return pts
}
const TRUTH = buildTruth()
let ti = 0
function truthAt(t) {
  if (t <= TRUTH[0].t) return { u: TRUTH[0].u, v: TRUTH[0].v }
  if (t >= TRUTH[TRUTH.length - 1].t) { const l = TRUTH[TRUTH.length - 1]; return { u: l.u, v: l.v } }
  while (ti < TRUTH.length - 2 && TRUTH[ti + 1].t < t) ti++
  while (ti > 0 && TRUTH[ti].t > t) ti--
  const a = TRUTH[ti], b = TRUTH[ti + 1], f = (t - a.t) / (b.t - a.t)
  return { u: a.u + (b.u - a.u) * f, v: a.v + (b.v - a.v) * f }
}

// motion.js 读 performance.now() 当锚点时刻,这里接管成可控时钟。
let NOW = 0
Object.defineProperty(globalThis, 'performance', { value: { now: () => NOW }, writable: true })

// 与 useMapEngine 的 applyPos + applyFrame 同构:建锚点 → 逐帧外推 + 指数回拉。
function simulate() {
  const t0 = PK[0].t, tEnd = PK[PK.length - 1].t
  let pi = 0, a = null, disp = null
  const errs = [], jumps = []
  for (let t = t0; t <= tEnd; t += 1 / FPS) {
    while (pi < PK.length && PK[pi].t <= t) {
      NOW = t * 1000
      a = M.makeAnchor(PK[pi], disp, false)
      pi++
    }
    if (!a) continue
    NOW = t * 1000
    const dt = (NOW - a.t0) / 1000
    const tau = a.tau || M.SMOOTH_TAU
    const decay = dt >= tau * M.TAU_CUTOFF ? 0 : Math.exp(-dt / tau)
    const q = M.posAt(a, dt)
    disp = { u: q.u + a.cu * decay, v: q.v + a.cv * decay }
    const T = truthAt(t)
    errs.push(toM(Math.hypot(disp.u - T.u, disp.v - T.v)))
    if (errs.length > 1) {
      const p = prevDisp
      jumps.push(toM(Math.hypot(disp.u - p.u, disp.v - p.v)))
    }
    var prevDisp = { u: disp.u, v: disp.v }
  }
  return { errs, jumps }
}

const { errs, jumps } = simulate()
const q = (xs, p) => { const s = [...xs].sort((x, y) => x - y); return s[Math.min(s.length - 1, Math.floor(p * s.length))] }
const mean = (xs) => xs.reduce((a, b) => a + b, 0) / xs.length

const got = {
  errMean: mean(errs),
  errP95: q(errs, .95),
  errMax: Math.max(...errs),
  jumpP99: q(jumps, .99),
  jumpMax: Math.max(...jumps),
  jitter: jumps.filter((x) => x > 1).length, // >1m/帧 = 瞬时 60m/s,远超本场景真实上限 29m/s
}
// 门槛 = 实测值 × 1.15(见文件头)。改动若让数字变差就会红,有意放宽请带理由更新。
const limit = {
  errMean: 8.7,   // 实测 7.5
  errP95: 16.4,   // 实测 14.2
  errMax: 23.5,   // 实测 20.4
  jumpP99: 1.5,   // 实测 1.28
  jumpMax: 3.0,   // 实测 2.6
  jitter: 117,    // 实测 101
}

const fails = []
for (const k of Object.keys(limit)) {
  const ok = got[k] <= limit[k]
  console.log(`${ok ? '  ok  ' : ' FAIL '} ${k.padEnd(9)} ${got[k].toFixed(2).padStart(7)}  (门槛 ${limit[k]})`)
  if (!ok) fails.push(`${k}: ${got[k].toFixed(2)} > ${limit[k]}`)
}
console.log(`\n  偏差 均值 ${got.errMean.toFixed(1)}m / p95 ${got.errP95.toFixed(1)}m / 最大 ${got.errMax.toFixed(1)}m` +
  ` · 每帧位移 p99 ${got.jumpP99.toFixed(2)}m / 最大 ${got.jumpMax.toFixed(2)}m · 抽搐帧 ${got.jitter}/${jumps.length}`)

if (fails.length) {
  console.error('\n✗ 平滑指标劣化:' + fails.map((f) => '\n  - ' + f).join(''))
  process.exit(1)
}
console.log('\n✓ 平滑指标在门槛内')
