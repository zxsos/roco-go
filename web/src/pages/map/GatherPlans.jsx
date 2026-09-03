import { useState } from 'react'
import { confirmDialog } from '../../components/confirm'

// —— 采集方案:用户自定义的一组品种快捷集合 ——
//
// 存在理由:品种有 41 个,「只挖矿」要手动勾十来个、「只采某几种花」每次重勾一遍
// 都不现实。方案就是给常用组合起个名字,点一下切换。
//
// 与 utils/rules.js 的 RULE_SCHEMES 同一套心智(「自由度高不等于每次都从零配」),
// 但那边是**内置只读**的方案,这边是**用户自己存**的(能新建/改名/删除)。
//
// 为什么不直接做成「官方大类」:大类得从解包数据取(MEGAMAP_CONF 分类字段待验证),
// 而方案是**每个人自己的分法** —— 你要的「矿」未必等于官方分类。两者将来可并存。

export const PLANS_LS_KEY = 'map.gatherPlans.v1'

const isName = (x) => typeof x === 'string' && x.length > 0

// sanitizePlans 从存储还原;字段不对的方案整条丢弃。
//
// 宁可少一个方案,也不要留个坏方案把筛选搞乱 —— 方案一旦激活就决定了**图上显示什么**,
// 而这类错误不报错,只表现为「地图空了」或「筛不掉」。
export function sanitizePlans(raw) {
  if (!raw || typeof raw !== 'object') return { plans: [], active: null }
  const plans = (Array.isArray(raw.plans) ? raw.plans : [])
    .map((p) => {
      if (!p || typeof p !== 'object') return null
      const id = typeof p.id === 'string' ? p.id : ''
      const name = typeof p.name === 'string' ? p.name : ''
      if (!id || !name) return null
      return { id, name, kinds: Array.isArray(p.kinds) ? p.kinds.filter(isName) : [] }
    })
    .filter(Boolean)
  // active 指向的方案不存在(被删了/存储损坏)就退回无方案,不留悬空引用。
  const active = plans.some((p) => p.id === raw.active) ? raw.active : null
  return { plans, active }
}

// newPlanId 生成方案 id:时间戳 + 随机尾缀,同一毫秒连建两个也不撞。
export const newPlanId = () =>
  'p' + Date.now().toString(36) + Math.random().toString(36).slice(2, 6)

// PlanNameInput 单行命名:回车确认、Esc 取消、失焦确认。
// 失焦取**确认**而非取消:手机上点别处时已敲的字不该白丢。
function PlanNameInput({ initial = '', placeholder, onDone, onCancel }) {
  const [v, setV] = useState(initial)
  return (
    <input className="map-plan-input" value={v} autoFocus maxLength={20}
      placeholder={placeholder} aria-label="方案名"
      onChange={(e) => setV(e.target.value)}
      onBlur={() => onDone(v.trim())}
      onKeyDown={(e) => {
        if (e.key === 'Enter') onDone(v.trim())
        if (e.key === 'Escape') onCancel()
      }} />
  )
}

// GatherPlans 方案条:chips 切换 + 当前方案的退出/改名/删除 + 新建。
//
// 改名删除只在**当前激活**的方案上展开,不在每个 chip 上挂按钮 ——
// 那样一行挤满图标,且「点 chip 是切换还是删除」要猜。
export default function GatherPlans({
  plans, activeId, onActivate, onDeactivate, onCreate, onRename, onDelete,
}) {
  const [mode, setMode] = useState(null) // null | 'new' | 'rename'

  return (
    <div className="map-gather-plans">
      {plans.map((p) => (
        <span key={p.id} className={'map-plan-item' + (p.id === activeId ? ' on' : '')}>
          {mode === 'rename' && p.id === activeId
            ? (
              <PlanNameInput initial={p.name}
                onDone={(name) => { setMode(null); if (name) onRename(p.id, name) }}
                onCancel={() => setMode(null)} />
            )
            : (
              <>
                <button className="map-plan-name" onClick={() => onActivate(p.id)}
                  title={`含 ${p.kinds.length} 个品种,点击切换`}
                  aria-pressed={p.id === activeId}>
                  {p.name}
                  <span className="muted">{p.kinds.length}</span>
                </button>
                {p.id === activeId && (
                  <>
                    <button className="map-plan-act" onClick={onDeactivate}
                      title="退出方案,回到手动勾选" aria-label="退出方案">⏏</button>
                    <button className="map-plan-act" onClick={() => setMode('rename')}
                      title="改名" aria-label="改名">✎</button>
                    <button className="map-plan-act" onClick={() => {
                      confirmDialog({ message: `删除方案「${p.name}」?`, okText: '删除', danger: true })
                        .then((ok) => ok && onDelete(p.id))
                    }} title="删除方案" aria-label="删除方案">🗑</button>
                  </>
                )}
              </>
            )}
        </span>
      ))}

      {mode === 'new'
        ? (
          <PlanNameInput placeholder="方案名"
            onDone={(name) => { setMode(null); if (name) onCreate(name) }}
            onCancel={() => setMode(null)} />
        )
        : (
          <button className="map-plan-add" onClick={() => setMode('new')}
            title="把当前勾选的品种存成一个方案">+ 存为方案</button>
        )}
    </div>
  )
}
