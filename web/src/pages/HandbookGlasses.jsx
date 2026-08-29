import React, { useEffect, useState, useContext, useMemo, useRef } from 'react'
import { toPng } from 'html-to-image'
import { getHandbookGlasses } from '../api'
import { IconsContext } from '../context'
import { imgURL } from '../components/icons'
import { GlassChip, glassMask } from '../components/badges'
import { GLASS_COLORS, GLASS_PARTICLES, GLASS_HIDDEN } from '../data/glassConf'

// 主色(ui_color_1)→ 显示名。11 种主色按 GLASS_COLORS 键序(见 web/src/data/glassConf.js)。
const MAJOR_NAMES = {
  '#3c32cf': '深蓝紫', '#83d256': '草绿', '#3ed7d7': '青',
  '#b869ed': '紫', '#e5576f': '绯红', '#bac8fb': '淡蓝',
  '#aedfae': '淡绿', '#a8e5e5': '淡青', '#dbbceb': '淡紫',
  '#fdcdd3': '淡粉', '#f1d397': '淡黄',
}

// 分享图 8 个颜色分类(图例/列):6 个彩色系 + 黑白 + 赛季彩色。
// 彩色系的代表色即列底色与图例圆点色;赛季彩色用多彩渐变。
const SHARE_COLS = [
  { key: 'blue', name: '蓝', color: '#4f6ff0' },
  { key: 'cyan', name: '青', color: '#35c4dd' },
  { key: 'green', name: '绿', color: '#67c94f' },
  { key: 'yellow', name: '黄', color: '#f2c531' },
  { key: 'pink', name: '粉', color: '#f2799e' },
  { key: 'purple', name: '紫', color: '#a86df2' },
  { key: 'mono', name: '黑白', color: '#7d8694' },
  { key: 'season', name: '赛季', color: 'linear-gradient(135deg,#f2c531,#f2799e,#a86df2,#35c4dd)' },
]
// 11 种主色(ui_color_1)→ 分享图分类:按色相归入蓝/青/绿/黄/粉/紫。
const MAJOR_TO_SHARE = {
  '#3c32cf': 'blue', '#bac8fb': 'blue',
  '#3ed7d7': 'cyan', '#a8e5e5': 'cyan',
  '#83d256': 'green', '#aedfae': 'green',
  '#f1d397': 'yellow',
  '#e5576f': 'pink', '#fdcdd3': 'pink',
  '#b869ed': 'purple', '#dbbceb': 'purple',
}
// 每列底部留白,让 8 列底部高度参差不齐(数量本就不同 + 留白差异,错落不齐)。
const SHARE_COL_GAP = [8, 24, 14, 30, 10, 26, 16, 32]

// 炫彩图鉴:按品种聚合展示本账号收集到的普通/隐藏炫彩色卡。
// 数据来自登录包 pet_handbook(每次登录时快照更新),点击色卡可放大预览。
export default function HandbookGlasses() {
  const icons = useContext(IconsContext)
  const [data, setData] = useState(null) // null=加载中,其余为 glasses 数组
  const [err, setErr] = useState('')
  const [exporting, setExporting] = useState(false)
  const shareRef = useRef(null) // 分享导出用的隐藏 DOM

  // 分享图片:把「主色分组」排版(横向色卡、纵向分组竖排)导出为一张 PNG。
  // 分享节点平时 visibility:hidden + left:-10000px 不占位,html-to-image 会原样
  // 复制该样式导致导出空白,故导出前临时改为可见并移到视口左上角(z-index -1,
  // 被页面背景盖住,用户看不到闪动),导出完成后恢复。
  const exportShare = async () => {
    const node = shareRef.current
    if (!node || exporting) return
    setExporting(true)
    node.style.visibility = 'visible'
    node.style.left = '0'
    node.style.top = '0'
    try {
      const url = await toPng(node, { pixelRatio: 2, backgroundColor: '#ffffff', cacheBust: true })
      const a = document.createElement('a')
      a.href = url
      const d = new Date()
      const ymd = `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`
      a.download = `炫彩图鉴_${ymd}.png`
      a.click()
    } catch (e) {
      alert('导出失败,请重试')
    } finally {
      node.style.visibility = ''
      node.style.left = ''
      node.style.top = ''
      setExporting(false)
    }
  }

  useEffect(() => {
    let dead = false
    setErr('')
    getHandbookGlasses().then((d) => {
      if (!dead) setData((d && d.glasses) || [])
    }).catch((e) => {
      if (!dead) { setData([]); setErr(e.message) }
    })
    return () => { dead = true }
  }, [])

  const stats = useMemo(() => {
    if (!data) return null
    let common = 0
    let hidden = 0
    for (const it of data) {
      common += (it.common || []).length
      hidden += (it.hidden || []).length
    }
    return { species: data.length, common, hidden }
  }, [data])

  // 按主色(ui_color_1)分组:普通炫彩 value = (粒子id<<20)|配色id,主色 = GLASS_COLORS[配色id][0]。
  // 每组:cards = 该主色下已收集的全部色卡(value 粒度,横排);pets = 拥有该主色色卡的精灵(头像+其色卡)。
  const majorGroups = useMemo(() => {
    if (!data) return null
    const groups = new Map() // 主色 hex -> { cards: Map<value,value>, pets: Map<base, {base,head,name,book,values:Set> } }
    for (const it of data) {
      for (const v of it.common || []) {
        const colors = GLASS_COLORS[v & 0xFFFFF]
        if (!colors) continue
        const major = colors[0]
        let g = groups.get(major)
        if (!g) {
          g = { major, cards: new Map(), pets: new Map() }
          groups.set(major, g)
        }
        g.cards.set(v, v)
        let pet = g.pets.get(it.base)
        if (!pet) {
          pet = { base: it.base, head: it.head, name: it.name, book: it.book, values: new Set() }
          g.pets.set(it.base, pet)
        }
        pet.values.add(v)
      }
    }
    return [...groups.entries()]
      .map(([major, g]) => ({
        major,
        name: MAJOR_NAMES[major] || major,
        cards: [...g.cards.values()].sort((a, b) => a - b),
        pets: [...g.pets.values()]
          .map((p) => ({ ...p, values: [...p.values].sort((a, b) => a - b) }))
          .sort((a, b) => a.base - b.base),
      }))
      // 组序按组内最小配色 id(与 GLASS_COLORS 键序一致)
      .sort((a, b) => (a.cards[0] & 0xFFFFF) - (b.cards[0] & 0xFFFFF))
  }, [data])

  // 分享图 8 列数据:每列一个颜色分类,列内垂直堆叠该分类下的宠物卡片。
  // 卡片粒度 = (宠物 × 分类):同一宠物有多个同分类炫彩只取第一张,跨分类
  // (不同色系的炫彩)可出现在多列;普通炫彩按主色归入 6 个彩色系,隐藏炫彩
  // 1000 归黑白、1/2/3 赛季归赛季彩色。
  const shareCols = useMemo(() => {
    if (!data) return null
    const cols = SHARE_COLS.map((c) => ({ key: c.key, name: c.name, color: c.color, cards: [] }))
    const seen = cols.map(() => new Set()) // 每列按 base 去重
    for (const it of data) {
      for (const v of it.common || []) {
        const colors = GLASS_COLORS[v & 0xFFFFF]
        if (!colors) continue
        const key = MAJOR_TO_SHARE[colors[0]]
        if (!key) continue
        const ci = SHARE_COLS.findIndex((c) => c.key === key)
        const s = seen[ci]
        if (s.has(it.base)) continue
        s.add(it.base)
        cols[ci].cards.push({
          base: it.base, book: it.book, name: it.name, head: it.head,
          particle: GLASS_PARTICLES[v >> 20], // 卡片装饰粒子
        })
      }
      for (const v of it.hidden || []) {
        const key = v === 1000 ? 'mono' : 'season'
        const ci = SHARE_COLS.findIndex((c) => c.key === key)
        const s = seen[ci]
        if (s.has(it.base)) continue
        s.add(it.base)
        cols[ci].cards.push({
          base: it.base, book: it.book, name: it.name, head: it.head,
          hidden: GLASS_HIDDEN[v], // 隐藏炫彩整图作卡片装饰
        })
      }
    }
    for (const c of cols) c.cards.sort((a, b) => a.base - b.base)
    return cols
  }, [data])

  return (
    <div className="hb-page">
      <div className="hb-head">
        <h2 className="hb-title">✨ 炫彩图鉴</h2>
        <span className="hb-note">数据来自登录包快照(每次登录更新),非实时</span>
        {stats && stats.species > 0 && (
          <button className="btn hb-share-btn" onClick={exportShare} disabled={exporting}>
            {exporting ? '生成中…' : '分享图片'}
          </button>
        )}
      </div>

      {data === null ? (
        <div className="muted">加载中…</div>
      ) : (
        <>
          {stats.species > 0 && (
            <div className="hb-stats">
              <span className="hb-stat">收集品种 <b>{stats.species}</b></span>
              <span className="hb-stat">普通炫彩 <b>{stats.common}</b></span>
              <span className="hb-stat">隐藏炫彩 <b>{stats.hidden}</b></span>
            </div>
          )}

          {majorGroups && majorGroups.length > 0 && (
            <div className="hb-major">
              <div className="hb-major-title">按主色分组</div>
              {majorGroups.map((g) => (
                <div className="hb-major-group" key={g.major}>
                  <div className="hb-major-head">
                    <span className="hb-major-dot" style={{ background: g.major }} />
                    <span className="hb-major-name">{g.name}<em>{g.major}</em></span>
                    <span className="hb-major-count">{g.cards.length} 色卡 · {g.pets.length} 品种</span>
                  </div>
                  <div className="hb-major-cards">
                    {g.cards.map((v) => <GlassChip key={v} p={{ glassType: 1, glassValue: v }} />)}
                  </div>
                  <div className="hb-major-bar">
                    {g.pets.map((p) => (
                      <span className="hb-major-pet" key={p.base} title={`${p.name} · 图鉴 #${p.book}`}>
                        <img className="hb-major-head-ic" src={imgURL(p.head)} alt={p.name} loading="lazy" />
                        <span className="hb-major-pet-cards">
                          {p.values.map((v) => <GlassChip key={v} p={{ glassType: 1, glassValue: v }} />)}
                        </span>
                        <span className="hb-major-pet-name">{p.name}</span>
                      </span>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}

          {stats.species === 0 && !err ? (
            <div className="hb-empty">
              <span className="hb-empty-ic">{icons.colorful ? <img src={imgURL(icons.colorful)} alt="" /> : '彩'}</span>
              <p>还没有收集到炫彩。</p>
              <p className="hb-empty-sub">登录一次游戏后自动同步图鉴记录;同一品种的多个炫彩变体都会按图鉴号汇总在这里。</p>
            </div>
          ) : err ? (
            <div className="hb-empty">
              <p className="hb-empty-sub">加载失败:{err}</p>
            </div>
          ) : (
            <div className="hb-list">
              {data.map((it) => (
                <div className="hb-card" key={it.base}>
                  <img
                    className="hb-avatar"
                    src={imgURL(it.head)}
                    alt={it.name || `图鉴 #${it.book}`}
                    loading="lazy"
                  />
                  <div className="hb-meta">
                    <div className="hb-name">
                      {it.name || '未知品种'}
                      <span className="hb-book">图鉴 #{it.book}</span>
                    </div>
                    <div className="hb-cards">
                      {(it.common || []).length > 0 && (
                        <span className="hb-group">
                          <span className="hb-group-label">普通</span>
                          {it.common.map((v) => (
                            <GlassChip key={v} p={{ glassType: 1, glassValue: v }} />
                          ))}
                        </span>
                      )}
                      {(it.hidden || []).length > 0 && (
                        <span className="hb-group">
                          <span className="hb-group-label">隐藏</span>
                          {it.hidden.map((v) => (
                            <GlassChip key={v} p={{ glassType: 2, glassValue: v }} />
                          ))}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </>
      )}

      {/* 分享导出 DOM:平时隐藏不占位,点击分享时临时移到视口内导出。
          白色干净背景:顶部图例(8 个颜色分类圆点),下方 8 列垂直卡片列表
          (每列一个分类,列内垂直堆叠彩色圆角卡片:粒子装饰 + 内嵌宠物头像),
          每列数量不等 + 底部留白差异形成参差,页面底部灰色小字水印。 */}
      <div className="hb-share" ref={shareRef}>
        <div className="share-head">
          <div className="share-title">✨ 炫彩图鉴</div>
          <div className="share-legend">
            {shareCols && shareCols.map((c) => (
              <span className="share-legend-item" key={c.key}>
                <i className="share-legend-dot" style={{ background: c.color }} />
                {c.name}
              </span>
            ))}
          </div>
        </div>
        <div className="share-cols">
          {shareCols && shareCols.map((c, i) => (
            <div className="share-col" key={c.key} style={{ paddingBottom: SHARE_COL_GAP[i] }}>
              {c.cards.map((card) => (
                <div className="share-card" key={card.base} style={{ background: c.color }}>
                  {card.hidden ? (
                    <img className="share-card-bg" src={imgURL('dazzling/' + card.hidden)} alt="" loading="lazy" />
                  ) : card.particle ? (
                    <i className="share-card-bg" style={glassMask(card.particle, 'rgba(255,255,255,.55)')} />
                  ) : null}
                  <img className="share-card-head" src={imgURL(card.head)} alt={card.name} loading="lazy" />
                  <span className="share-card-name">{card.name}</span>
                </div>
              ))}
            </div>
          ))}
        </div>
        <div className="share-watermark">洛克王国·世界 · 炫彩图鉴 · 数据来自登录包快照</div>
      </div>
    </div>
  )
}
