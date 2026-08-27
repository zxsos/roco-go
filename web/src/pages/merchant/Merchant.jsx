import React, { useCallback, useEffect, useState } from 'react'
import { getMerchant } from '../../api'
import { fmtTime } from '../../utils/format'

// 远行商人页:展示后端缓存的 4h 轮次数据(令牌在服务端,缓存 2 天,见
// internal/server/api_merchant.go)。业务节奏:每天 8 点开张、0 点收摊,8/12/16/20 四轮上架;
// 营业中显示今日已上架轮次,打烊后(0 点到次日 8 点前)显示昨日全天回顾。
// 刷新按钮只是重拉后端缓存(不烧 token);强制刷新才让后端回源第三方。
// 图片字段兼容两种形式:http(s) 外链直接用;否则按本地 /img/ 相对路径解析。
const imgSrc = (it) => {
  const v = it && it.image
  if (!v) return ''
  return /^https?:\/\//i.test(v) ? v : '/img/' + v
}

// msTime 第三方的时间戳是毫秒,fmtTime 要秒,这里除 1000 再交给它。
const msTime = (ms) => (ms ? fmtTime(ms / 1000) : '')

// 营业状态 → 徽标文案与样式
const STATUS = {
  open: { cls: 'ok', text: '营业中' },
  idle: { cls: 'off', text: '已打烊' },
}

// count 统计某轮的第三方 item 数(优先 item_count,兜底数 items 数组)。
const count = (m) => (m ? (m.item_count ?? ((m.items && m.items.length) || 0)) : 0)

export default function Merchant() {
  const [d, setD] = useState(null) // {now,day,status,today,prev}
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(false)

  const load = useCallback(async (force) => {
    setLoading(true)
    setErr('')
    try {
      setD(await getMerchant(force))
    } catch (e) {
      setErr(e.message || '拉取失败')
      setD(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load(false) }, [load])

  if (err) {
    return <div className="merchant-page"><div className="merchant-err">{err}</div></div>
  }
  if (!d) {
    return <div className="merchant-page"><div className="empty">{loading ? '加载中…' : ''}</div></div>
  }

  // 回顾视图取哪一天的槽:idle(次日 8 点前)看昨天,其余看今天。
  const rounds = d.status === 'idle' ? d.prev : d.today
  const active = (rounds || []).filter((r) => !r.empty) // 有货的轮次
  const head = active.find((r) => r.merchant) // 第一个有货轮,取商人名/副标题
  const m = head && head.merchant
  const total = active.reduce((n, r) => n + count(r.merchant), 0)
  const st = STATUS[d.status] || STATUS.idle
  const viewTitle = d.status === 'open' ? '今日营业' : '昨日回顾'

  return (
    <div className="merchant-page">
      <div className="panel merchant-head">
        <div className="merchant-head-main">
          <div className="merchant-name">{m ? m.merchant_name : '远行商人'}</div>
          {m && m.subtitle && <div className="merchant-sub">{m.subtitle}</div>}
        </div>
        <div className="merchant-round">
          <span className={`merchant-status merchant-status-${st.cls}`}>{st.text}</span>
          <span className="merchant-day">{d.day}</span>
          {m && m.round && m.round.current != null && (
            <span className="merchant-round-idx">{m.round.current}/{m.round.total}</span>
          )}
          {m && m.round && m.round.countdown && (
            <span className="merchant-round-countdown" title="距本轮结束">⏳ {m.round.countdown}</span>
          )}
        </div>
        <div className="muted merchant-fetched">{m ? `更新于 ${m.fetched_at || '-'}` : ''}</div>
      </div>

      <div className="merchant-bar">
        <span className="merchant-count">{viewTitle} · 在售 {total} 件</span>
        <span className="merchant-btns">
          <button type="button" className="btn" onClick={() => load(false)} disabled={loading}>
            {loading ? '加载中…' : '刷新'}
          </button>
          <button type="button" className="btn" onClick={() => load(true)} disabled={loading}
            title="强制后端重新向第三方抓取(烧对方额度,非必要别点)">
            强制刷新
          </button>
        </span>
      </div>

      {active.length ? (
        <div className="merchant-rounds">
          {active.map((r) => <MerchantSection key={r.start} r={r} />)}
        </div>
      ) : (
        <div className="empty">该时段暂无数据,后端会按 4h 自动补查,稍后再试</div>
      )}

      {rounds && rounds.some((r) => r.empty) && (
        <div className="merchant-off">
          {rounds.filter((r) => r.empty).map((r) => (
            <span key={r.start} className="merchant-off-chip">休市 {r.label}</span>
          ))}
        </div>
      )}
    </div>
  )
}

// MerchantSection 单个有货轮次:轮次时段标题 + 该轮商品网格。
function MerchantSection({ r }) {
  const m = r.merchant
  const items = (m && m.items) || []
  return (
    <div className="merchant-round-block">
      <div className="merchant-round-title">
        <span className="merchant-round-dot" />
        <span>{r.label}</span>
        <span className="merchant-round-sub">第 {count(m)} 件</span>
        {m && m.round && m.round.label && <span className="muted">· {m.round.label}</span>}
      </div>
      {items.length ? (
        <div className="merchant-grid">
          {items.map((it, i) => <MerchantItem key={it.name + i} it={it} />)}
        </div>
      ) : (
        <div className="empty">该轮次无商品</div>
      )}
    </div>
  )
}

// MerchantItem 单个商品卡片:图(外链/本地兼容)+ 名称/类别 + 价格/限购/销售时间。
function MerchantItem({ it }) {
  const [imgBad, setImgBad] = useState(false)
  const src = imgSrc(it)
  const range = it.start_time && it.end_time ? `${msTime(it.start_time)} ~ ${msTime(it.end_time)}` : ''
  const tLabel = it.time_label || range
  return (
    <div className="merchant-item">
      <div className="merchant-item-img-wrap">
        {src && !imgBad ? (
          <img className="merchant-item-img" src={src} alt="" loading="lazy" draggable={false}
            onError={() => setImgBad(true)} />
        ) : (
          <span className="merchant-item-img-fallback">🛍️</span>
        )}
      </div>
      <div className="merchant-item-body">
        <div className="merchant-item-name" title={it.name}>{it.name}</div>
        {it.kind && <div className="muted merchant-item-kind">{it.kind}</div>}
        <div className="merchant-item-rows">
          <div className="merchant-item-row">
            <span className="muted">价格</span>
            <span className="merchant-price">{it.price} <span className="muted">金币</span></span>
          </div>
          <div className="merchant-item-row">
            <span className="muted">限购</span>
            <span>{it.limit > 0 ? `${it.limit} 个` : '不限'}</span>
          </div>
          {tLabel && (
            <div className="merchant-item-row merchant-item-time" title={range || tLabel}>
              <span className="muted">时间</span>
              <span>{tLabel}</span>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
