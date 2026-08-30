// 画中画(PiP)canvas 绘制的校验。
//
//   node scripts/verify-pip-canvas.mjs
//
// 与 scripts/verify-pip.mjs 的分工:那个校验**纯几何**(pipGeom.js 的换算与常量),
// 这个校验**实际画出来的像素**(pipDraw.js 的绘制指令本身)。PiP 是系统悬浮窗,
// 没有 DevTools 可 inspect —— 绘制错了只能靠把帧 dump 出来逐像素判,故固化成脚本。
//
// 做法:在真浏览器里 import 真实的 pipDraw.js,喂一份**合成快照**,画到一张离屏画布上,
// 再读指定位置的像素。判据靠两条:
//   1. 底图用一张故意不存在的图 → 加载不出来 → 整块画布保持 palette.bg;
//   2. palette.bg / palette.bg1 给成辨识度极高的洋红 / 亮绿。
// 于是「标记内部那块区域是洋红还是亮绿」就能直接回答
// **头像背后有没有垫实心底色** —— 垫了就是绿色(不透明),没垫就是洋红(透出底图)。
//
// 另有一组**玩家箭头居中**的断言(见下方 ARROW 段):PiP 的焦点恒为玩家自身位置,
// 故箭头必须画在画布正中。判据取箭头的**像素重心**而非某个顶点 —— 箭头随 heading
// 旋转,顶点位置跟着转,重心不转;断言具体像素会随朝向误报。

import { chromium } from 'playwright'
import { createServer } from 'vite'

const results = []
const check = (name, ok, detail) => {
  results.push({ name, ok })
  console.log(`${ok ? '  ok  ' : ' FAIL '} ${name}${detail ? '  — ' + detail : ''}`)
}

// 业务模块有 JSX 与无扩展名导入,Node 直接 import 解析不了,故走 vite dev server。
const server = await createServer({ root: process.cwd(), logLevel: 'error', server: { port: 5197, strictPort: true } })
await server.listen()

const browser = await chromium.launch({ args: ['--no-sandbox', '--disable-dev-shm-usage'] })
const page = await browser.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(String(e).split('\n')[0]))

try {
  // 挂到 vite dev server 的根:只为拿到模块图,随后用动态 import 取 pipDraw
  await page.goto('http://localhost:5197/', { waitUntil: 'domcontentloaded' })

  const got = await page.evaluate(async () => {
    const m = await import('/src/pages/map/pipDraw.js')
    const SIZE = 512
    const cv = document.createElement('canvas')
    cv.width = cv.height = SIZE
    const ctx = cv.getContext('2d')
    // bg = 洋红(底图),bg1 = 亮绿(实心底色):两者必须完全不同,否则判不出
    const palette = {
      bg: '#ff00ff', bg1: '#00ff00', line: '#0000ff',
      fg: '#ffffff', fgDim: '#808080', gold: '#ffd54a', star: '#ffd54a',
      portal: '#c9b6ff', shiny: '#ffd54a', flowerD: '#ff5fa2', flower: '#ffb6e0',
    }
    // 小窝放在野生宠右侧 100px:mapPx = 512 × zoom(2) = 1024,故 Δu = 100/1024
    const du = 100 / 1024
    const snap = {
      w: SIZE, h: SIZE, zoom: 2, scale: 1,
      focus: { u: 0.5, v: 0.5 },
      palette,
      sceneImg: '__no_such_scene__', // 加载不出 → 底图不画,整块保持 palette.bg
      wilds: [{ u: 0.5, v: 0.5, kinds: [], style: {}, img: null }],
      nests: [{ u: 0.5 + du, v: 0.5, pet: { img: null } }],
    }
    m.renderToCanvas(ctx, snap)

    const px = (x, y) => {
      const d = ctx.getImageData(Math.round(x), Math.round(y), 1, 1).data
      return `rgb(${d[0]}, ${d[1]}, ${d[2]})`
    }
    const cx = SIZE / 2, cy = SIZE / 2
    return {
      // 标记内部、但避开中央的 🐾 字形(字号约 0.53×size,纵向占 ±8px)
      wild: px(cx, cy - 11),   // .map-wild 30px → r=15
      nest: px(cx + 100, cy - 13), // .map-nest 34px → r=17
      bg: px(20, 20),          // 画布角落:必然是底图色
    }
  })

  check('画布底色按 palette.bg 铺满', got.bg === 'rgb(255, 0, 255)', `角落 ${got.bg}`)
  // 透出底图 = 洋红;垫了实心底色 = 亮绿
  check('野生宠头像无实心底色(透出底图)', got.wild === 'rgb(255, 0, 255)', `标记内 ${got.wild}`)
  check('小窝住户头像无实心底色(透出底图)', got.nest === 'rgb(255, 0, 255)', `标记内 ${got.nest}`)

  // —— 玩家箭头必须画在画布正中 ——
  // 背景:PiP 的 focus 恒取玩家位置(见 usePip.buildSnap),故玩家屏幕坐标 = 画布中心。
  // 曾漏了 DOM 版 translate(-50%,-50%) 的对应补偿,箭头整体偏移 (12,12)×k
  // (scale=1 时约 22px),且偏移随 heading 一起旋转 —— 表现为「箭头绕着中心打转」。
  // 多个朝向量一遍:只测一个朝向,恰好朝向与偏移同向时也可能蒙混过关。
  const arrows = await page.evaluate(async () => {
    const m = await import('/src/pages/map/pipDraw.js')
    const SIZE = 512
    const out = []
    for (const heading of [0, 90, 180, 270]) {
      const cv = document.createElement('canvas')
      cv.width = cv.height = SIZE
      const ctx = cv.getContext('2d')
      // 底图白、箭头纯红:只统计「红得发黑」的像素即可把箭头从背景里挑出来。
      // 光晕是半透明红叠在白底上(G 被抬到 ~120),不会被误计入。
      const palette = {
        bg: '#ffffff', bg1: '#ffffff', line: '#000000', fg: '#000000',
        fgDim: '#808080', gold: '#ffff00', star: '#ffff00', portal: '#c9b6ff',
        shiny: '#ffff00', flowerD: '#ff5fa2', flower: '#ffb6e0', red: '#ff0000',
      }
      const u = 0.5, v = 0.5
      m.renderToCanvas(ctx, {
        w: SIZE, h: SIZE, zoom: 2, scale: 1,
        focus: { u, v }, palette,
        sceneImg: '__no_such_scene__',
        player: { u, v, heading },
      })
      const d = ctx.getImageData(0, 0, SIZE, SIZE).data
      let n = 0, sx = 0, sy = 0
      for (let y = 0; y < SIZE; y++) {
        for (let x = 0; x < SIZE; x++) {
          const i = (y * SIZE + x) * 4
          if (d[i] > 200 && d[i + 1] < 40 && d[i + 2] < 40) { n++; sx += x; sy += y }
        }
      }
      out.push({ heading, n, dist: n ? Math.hypot(sx / n - SIZE / 2, sy / n - SIZE / 2) : -1 })
    }
    return out
  })
  // 阈值 4px:形状自身不对称(三角形重心在 viewBox 的 (12,12.7) 而非 (12,12))会留下
  // 约 1px 残余,与 DOM 版行为一致;留 4px 余量既不误报,也足以抓住当年的 22px 偏移。
  const off = arrows.filter((a) => a.n === 0 || a.dist > 4)
  check('玩家箭头居中(4 个朝向重心均贴近画布中心)', off.length === 0,
    off.length ? off.map((a) => `${a.heading}° 偏 ${a.dist.toFixed(1)}px`).join(', ')
      : arrows.map((a) => `${a.heading}° ${a.dist.toFixed(1)}px`).join(' '))

  check('绘制过程无 JS 错误', errors.length === 0, errors.slice(0, 2).join(' | '))
} catch (e) {
  check('执行完成', false, String(e).split('\n')[0])
} finally {
  await browser.close()
  await server.close()
}

const bad = results.filter((r) => !r.ok)
console.log(`\n${results.length - bad.length}/${results.length} 项通过`)
process.exit(bad.length ? 1 : 0)
