// 宠物陈列卡的图片渲染尺寸验收(需要 playwright + chromium)。
//
//   node scripts/verify-pet-card-size.mjs      (或 npm run verify:browser 带上它)
//
// 锁住的不变量:**256px 的全身像源(Pet256)不得被放大显示**。放大就会糊 ——
// 有损 webp(gen_images.py 的 QUALITY=90)的边缘振铃会被放大效应一并凸显。
//
// 为什么必须实测而不是看 CSS:列宽要经 auto-fill + minmax 两道换算,图片还要过
// aspect-ratio + padding + object-fit:contain。这里连环看错是**静默**的 ——
// 页面照常显示,只是图还是糊的。实测时踩到过两个坑,都记在这里:
//
//   1. aspect-ratio 4/3 在图片加载后**不生效** —— img 的 height:100% 与父的
//      aspect-ratio 循环依赖,浏览器回退到图片固有比例(Pet256 是正方形),
//      图区被撑成 ≈1:1。曾据此按 4:3 推算显示边长,结论是错的。
//   2. 测试图的固有尺寸会参与上面那条回退,故必须用 256×256 而非 1×1 占位图。
//
// 断言只卡 DPR=1(改动前实测最高 1.80× 放大,改动后全程 0.94×)。
// DPR=2 那一列**只报告不断言**:高倍屏下 256 的源必然要插值放大到 ~480 物理
// 像素,那是源分辨率的天花板,只有换更大的源(Pet1024)才解决 —— 不是本脚本
// 守得住的,算进断言会让它永远红。
//
// ⚠️ 已做变异测试:去掉 .pt-img 的 max-width/max-height: 256px → 失败(回到 1.80×,
//    报错直接指出是哪个容器宽度被放大)。
import { chromium } from 'playwright'
import { readFileSync } from 'node:fs'

// 256×256 纯色 PNG(内联,免依赖仓库资源):固有尺寸必须与被测源一致,见上。
const PX = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAQAAAAEACAIAAADTED8xAAAB/0lEQVR42u3TQREAMAjAsDH9iEEFunijgURC7xpd+eCqLwEGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAATAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAbAAGAAMAAYAAwABgADgAHAAGAAMAAYAAwABgADgAHAAGAAMAAYAAwABgADgAHAAGAAMAAYAAwABgADgAHAAGAAMAAYAAwABgADgAHAAGAAMAAYAAwABgADgAHAAGAAMAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAYAAwABgADAAGAAOAAcAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAGAAMAAYAA4ABwABgADAAbAOG6gQCnuJhgQAAAABJRU5ErkJggg=='

const base = readFileSync('src/styles/base.css', 'utf8')
const list = readFileSync('src/styles/list.css', 'utf8')

// stripMax=true → 去掉被测的 max-*(即改动前的样子),仅用于输出对比列。
const build = (stripMax) => {
  const css = stripMax ? list.replace(/max-width: 256px; max-height: 256px;/, '') : list
  return `<!doctype html><html><head><meta charset="utf-8">
<style>${base}\n${css}</style></head>
<body style="margin:0;padding:0">
<div id="host" style="width:1200px"><div class="pet-grid" id="g"></div></div>
</body></html>`
}

const WIDTHS = [480, 492, 600, 700, 800, 1000, 1200, 1500]
const browser = await chromium.launch()

async function measure(stripMax, dpr) {
  const page = await browser.newPage({ viewport: { width: 1600, height: 900 }, deviceScaleFactor: dpr })
  await page.setContent(build(stripMax))
  await page.evaluate((px) => {
    const g = document.getElementById('g')
    for (let i = 0; i < 8; i++) {
      const card = document.createElement('article')
      card.className = 'pet-card'
      card.innerHTML = '<div class="pt-media"><img class="pt-img" alt="">'
      g.appendChild(card)
    }
    for (const im of document.querySelectorAll('.pt-img')) im.src = px
  }, PX)
  const out = {}
  for (const w of WIDTHS) {
    await page.evaluate((w) => { document.getElementById('host').style.width = w + 'px' }, w)
    // clientWidth/Height 含 padding(不含 border),减掉 8×2 即 object-fit 的内容区;
    // contain 下实际绘制边长 = 内容区较短的一边。
    out[w] = await page.evaluate(() => {
      const im = document.querySelector('.pt-img')
      return {
        card: +document.querySelector('.pet-card').getBoundingClientRect().width.toFixed(0),
        css: +Math.min(im.clientWidth - 16, im.clientHeight - 16).toFixed(0),
      }
    })
  }
  await page.close()
  return out
}

const before1 = await measure(true, 1)
const after1 = await measure(false, 1)
const after2 = await measure(false, 2)

console.log('\n源图 256×256(Pet256)。放大倍数 = 显示边长 ÷ 256,>1 即糊。\n')
console.log('容器   卡片宽 │ 改前边长 放大 │ 改后边长 放大 │ DPR=2 物理边长 物理放大')
let worstBefore = 0, worstAfter = 0, worstPhys = 0
for (const w of WIDTHS) {
  const b = before1[w], a = after1[w]
  const rB = b.css / 256, rA = a.css / 256
  const phys = after2[w].css * 2, rP = phys / 256
  worstBefore = Math.max(worstBefore, rB)
  worstAfter = Math.max(worstAfter, rA)
  worstPhys = Math.max(worstPhys, rP)
  const m = (x) => x.toFixed(2) + (x > 1.001 ? '✗' : ' ')
  console.log(
    String(w).padStart(4) + '  ' + String(a.card).padStart(6) + ' │ '
    + String(b.css).padStart(7) + ' ' + m(rB) + ' │ '
    + String(a.css).padStart(7) + ' ' + m(rA) + ' │ '
    + String(phys).padStart(12) + ' ' + m(rP),
  )
}

console.log(`\n改前 DPR=1 最大放大 ${worstBefore.toFixed(2)}×`)
console.log(`改后 DPR=1 最大放大 ${worstAfter.toFixed(2)}×  ${worstAfter > 1.001 ? '✗ 仍有放大' : '✓ 全程不放大'}`)
console.log(`改后 DPR=2 最大放大 ${worstPhys.toFixed(2)}×  (仅报告:源分辨率天花板,需 Pet1024 才能解决)`)

await browser.close()
if (worstAfter > 1.001) {
  const bad = WIDTHS.filter((w) => after1[w].css / 256 > 1.001)
  console.error(`\n✗ 图片被放大(容器宽度 ${bad.join('/')}px),源是 256×256 —— 检查 .pt-img 的 max-width/max-height`)
  process.exit(1)
}
console.log('\n✓ 全部容器宽度下图片均未被放大')
