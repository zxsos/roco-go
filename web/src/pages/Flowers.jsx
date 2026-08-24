import React, { useState, useEffect, useContext, useMemo } from 'react'
import { getFlowers, subscribe } from '../api'
import { AccountContext } from '../context'
import { fmtTime } from '../utils/format'
import { ImgAvatar } from '../components/icons'

// 花种页面:渲染 s2c 0x0375 下发的 flower_npcs(花灵)活动 BOSS 分组。
// 只显示花种;world_leader_npcs(世界 BOSS)与 legendary_npcs(传说 NPC)在解析层就丢弃了。
// 数据源:进页面先 GET /api/flowers 回显最近一次分组,之后订阅 SSE flowers 实时覆盖
// (游戏内每打开一次花种面板,服务器就会整组重发 0x0375)。
export default function Flowers() {
  const account = useContext(AccountContext)
  const [data, setData] = useState(null) // null = 尚未收到任何 0x0375

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

  const flowers = useMemo(() => (data && data.flowers) || [], [data])
  const specials = flowers.filter((f) => f.specSeedId > 0)
  const normals = flowers.filter((f) => !(f.specSeedId > 0))

  return (
    <div className="flowers-page">
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>花种</h3>
        <span className="muted toolbar-hint">游戏中打开花种面板后自动更新</span>
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
                {specials.map((f) => <FlowerCard key={f.id} f={f} />)}
              </div>
            </section>
          )}
          <section className="flowers-group">
            <h4 className="flowers-group-t">普通花种</h4>
            <div className="flower-grid">
              {normals.map((f) => <FlowerCard key={f.id} f={f} />)}
            </div>
          </section>
        </>
      )}
    </div>
  )
}

function FlowerCard({ f }) {
  const stars = (f.star || 0) > 0 ? '★'.repeat(f.star) : ''
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
          <span className="muted">结束 {fmtTime(f.endTs)}</span>
        </div>
        {f.ownerUserId > 0 && (
          <div className="flower-meta">
            <span className="privacy">已选 UID:{f.ownerUserId}</span>
          </div>
        )}
      </div>
    </div>
  )
}
