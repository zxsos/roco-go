import React, { useState, useEffect, useContext, useMemo } from 'react'
import { getFlowers, subscribe } from '../api'
import { AccountContext } from '../context'
import { fmtTime } from '../utils/format'
import { ImgAvatar } from '../components/icons'

// 花种页面:渲染 s2c 0x0375 下发的 flower_npcs(花灵)活动 BOSS 分组。
// 只显示花种;world_leader_npcs(世界 BOSS)与 legendary_npcs(传说 NPC)在解析层就丢弃了。
// 数据源:进页面先 GET /api/flowers 回显最近一次分组,之后订阅 SSE flowers 实时覆盖
// (游戏内每打开一次花种面板,服务器就会整组重发 0x0375)。
// 游戏内点击地图上的花种时,服务器会额外下发 0x0338 单只详情(等级/炫彩/绑定宠物/奖牌),
// 由后端合并进对应卡片后经同一 SSE 刷新;未点过的花种这些字段为空。
export default function Flowers() {
  const account = useContext(AccountContext)
  const [data, setData] = useState(null) // null = 尚未收到任何 0x0375
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    getFlowers().then((v) => { if (v) setData(v) }).catch(() => {})
  }, [account])

  useEffect(() => {
    return subscribe((m) => {
      if (m.type !== 'flowers') return
      if (m.account && m.account !== account) return
      setData(m.data)
    })
  }, [account])

  // 活动结束倒计时随时间走:秒级刷新(卡片量少,重渲染开销可忽略)。
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  // 当前账号 user_id:判断「我选的」花种(account 形如 "UID:<user_id>")。
  const myUid = useMemo(() => Number((account || '').replace(/^UID:/, '') || 0), [account])

  const flowers = useMemo(() => (data && data.flowers) || [], [data])
  const specials = flowers.filter((f) => f.specSeedId > 0)
  const normals = flowers.filter((f) => !(f.specSeedId > 0))

  return (
    <div className="flowers-page">
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>花种</h3>
        <span className="muted toolbar-hint">打开面板自动更新,点地图花种看详情</span>
        <div className="spacer" />
        {data && <span className="muted">共 {flowers.length} 只花灵</span>}
      </div>
      {!data ? (
        <div className="empty">尚未收到花种数据:游戏内打开一次花种面板后自动显示…</div>
      ) : (
        <>
          {specials.length > 0 && (
            <section className="flowers-group">
              <h4 className="flowers-group-t">特殊花种(7 星)</h4>
              <div className="flower-grid">
                {specials.map((f) => <FlowerCard key={f.id} f={f} myUid={myUid} now={now} />)}
              </div>
            </section>
          )}
          <section className="flowers-group">
            <h4 className="flowers-group-t">普通花种</h4>
            <div className="flower-grid">
              {normals.map((f) => <FlowerCard key={f.id} f={f} myUid={myUid} now={now} />)}
            </div>
          </section>
        </>
      )}
    </div>
  )
}

// fmtLeft 把活动结束时间渲染为剩余倒计时;未设置返回 null,已结束返回 ended 标记。
function fmtLeft(endTs, nowMs) {
  if (!endTs) return null
  const s = Math.floor(endTs - nowMs / 1000)
  if (s <= 0) return { ended: true, text: '已结束' }
  const d = Math.floor(s / 86400)
  const hh = String(Math.floor((s % 86400) / 3600)).padStart(2, '0')
  const mm = String(Math.floor((s % 3600) / 60)).padStart(2, '0')
  const ss = String(s % 60).padStart(2, '0')
  return { ended: false, text: d > 0 ? `剩 ${d} 天 ${hh}:${mm}:${ss}` : `剩 ${hh}:${mm}:${ss}` }
}

function FlowerCard({ f, myUid, now }) {
  const stars = (f.star || 0) > 0 ? '★'.repeat(f.star) : ''
  // 已选状态:0=未选;等于当前账号=我选的;其他=被他人选走(不直接露他人 UID,只给 title 备查)。
  const mine = f.ownerUserId > 0 && f.ownerUserId === myUid
  const taken = f.ownerUserId > 0 && !mine
  const left = fmtLeft(f.endTs, now)
  // 详情字段:点过地图花种后由 0x0338 合并进来;未点过全空(普通花种绑定/奖牌恒为空)。
  const hasDetail = f.lv > 0 || f.glass || f.bindName || f.medalName
  return (
    <div className={'flower-card' + (f.specSeedId > 0 ? ' flower-special' : '')}>
      <ImgAvatar src={f.img} alt={f.name} className="flower-img" />
      <div className="flower-info">
        <div className="flower-name" title={f.name}>{f.name || '未知花灵'}</div>
        <div className="flower-meta">
          {stars && <span className="flower-star" title={`${f.star} 星`}>{stars}</span>}
          <span className="muted">血量 {f.blood || '-'}</span>
        </div>
        <div className="flower-meta">
          {left ? (
            <span className={'flower-left' + (left.ended ? ' ended' : '')} title={`结束 ${fmtTime(f.endTs)}`}>
              {left.text}
            </span>
          ) : (
            <span className="muted">结束 {fmtTime(f.endTs)}</span>
          )}
        </div>
        {hasDetail && (
          <div className="flower-detail">
            {f.lv > 0 && <span className="flower-chip" title="等级">Lv {f.lv}</span>}
            {f.glass && <span className="flower-chip flower-glass" title={`炫彩:${f.glass}`}>炫彩</span>}
            {f.bindName && (
              <span
                className="flower-chip flower-bind"
                title={f.bindEvo > 0 ? `绑定守护宠物,进化阶段 ${f.bindEvo}` : '绑定守护宠物'}
              >
                <ImgAvatar src={f.bindImg} alt={f.bindName} className="flower-chip-img" />
                绑定 {f.bindName}
              </span>
            )}
            {f.medalName && (
              <span className="flower-chip flower-medal" title="绑定宠物佩戴的奖牌">
                {f.medalIcon && <ImgAvatar src={f.medalIcon} alt={f.medalName} className="flower-chip-img" />}
                {f.medalName}
              </span>
            )}
          </div>
        )}
        <div className="flower-meta">
          {mine ? (
            <span className="flower-tag flower-mine" title="我已选择这朵花种">你已选</span>
          ) : taken ? (
            <span className="flower-tag flower-taken" title="已被其他玩家选择">已被选</span>
          ) : (
            <span className="flower-tag flower-free">未选</span>
          )}
        </div>
      </div>
    </div>
  )
}
