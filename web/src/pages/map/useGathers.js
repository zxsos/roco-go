import { useState, useEffect, useMemo, useCallback } from 'react'
import { getGathers, subscribe } from '../../api'
import { useAsyncData } from '../../hooks/useAsyncData'
import { PLANS_LS_KEY, sanitizePlans, newPlanId } from './GatherPlans'
import { GATHER_KIND } from './usePois'

// —— 实时采集物图层(花/草/菌/矿/果树)——
//
// 这一层与 POI 图层的「采集物」是**同一批东西的两种画法**,并存且互补:
//   - POI 图层(usePois.js):3552 个**候选刷新点**,官方配置里有登记就画,
//     回答「这儿会有」;默认关闭,因为三千多个点糊在图上没法看。
//   - 本图层:服务器**当下真下发**的实体,回答「这会儿有」。
//
// 两者差得远:实测两份 pcap,玩家 87m 圈内平均 13~19 个候选点里只有 4~6 个真有实体
// (刷出率三到四成)。所以本层的价值恰在「替玩家滤掉那七成空的候选点」—— 开着它跑图,
// 看到的就是此刻真能采的。
//
// 后端见 internal/pipeline/gathers.go。要点:
//   - 采完就消失:实体离开(被采走或走出 AOI)后端当场撤标记,**不留灰点**
//     (与野生宠相反 —— 采集物采完会按刷新规则再刷,留灰点会让人白跑一趟)。
//   - 换场景/传送整份作废:旧实体必然已不在视野,留着就是一屏假标记。
//   - 每次推送都是**全量**(实体进出按 150ms 合并),直接替换即可。

// 空数据的兜底常量:引用稳定,免得每次渲染都造新对象、打穿下游的 useMemo。
// 清单有 41 项,每次渲染重算一遍不值当,故空数组也用同一个常量。
const NO_GATHERS = { gathers: [] }
const NO_POIS = []
const GATHER_LS_KEY = 'map.gatherLayer'
const KINDS_LS_KEY = 'map.gatherKinds.v1'

// 默认**开启**:这层只画此刻真有的那几个(个位数到二十来个),不像候选点那样糊屏;
// 而「现在能采什么」正是跑图时最想知道的。用 null 区分「用户没选过」。
const loadOn = () => {
  try {
    const v = localStorage.getItem(GATHER_LS_KEY)
    return v === null ? true : v === '1'
  } catch { return true }
}

// 品种筛选存**关闭的集合**而非开启的:品种会随版本增删(官方加新采集物),
// 存「开启清单」会让新出现的品种默认隐藏,用户还得再配一次;
// 存「关闭清单」则是新品种自动可见,只有明确关掉的才不显示。
const loadOffKinds = () => {
  try {
    const v = JSON.parse(localStorage.getItem(KINDS_LS_KEY))
    return new Set(Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [])
  } catch { return new Set() }
}

// 采集方案。解析走 sanitizePlans 逐条校验:方案一旦激活就决定图上显示什么,
// 坏数据不报错、只表现为「地图空了」,故宁可丢掉坏条目也不能原样吃进去。
const loadPlans = () => {
  try {
    return sanitizePlans(JSON.parse(localStorage.getItem(PLANS_LS_KEY)))
  } catch { return { plans: [], active: null } }
}

// useGathers 管理实时采集物图层:订阅后端推送、按「主开关 + 品种筛选」筛出可绘制的标记。
//
// pois 参数(usePois 的返回值)只用来取**品种全集**:候选点表登记了 41 个品种,
// 而此刻视野里往往只有几个,清单只能从静态数据来,否则用户没法预先勾选。
export function useGathers(account, pois) {
  const { data, setData, refresh } = useAsyncData(
    useCallback(() => getGathers(), []),
    { fallback: NO_GATHERS, reloadKey: account },
  )
  // 收紧成 useMemo:`(data || {}).gathers || []` 每次渲染都造一个新数组,
  // 会让下面三个 useMemo 的依赖**每次都变**,等于缓存全废 —— 实时层每秒推好几次,
  // 而品种清单(41 项)与 marks 的重算本可跳过。
  const gathers = useMemo(() => (data || NO_GATHERS).gathers || [], [data])
  const [on, setOn] = useState(loadOn)
  const [offKinds, setOffKinds] = useState(loadOffKinds)
  const [kindsOpen, setKindsOpen] = useState(false)
  // 本场景的全部 POI(未经图层开关过滤),品种清单的静态来源。
  // 用模块级常量兜底:`pois.allPois || []` 每次渲染都会造新数组,
  // 会让下面 kinds 的 useMemo 依赖失效、每次重算 41 项。
  const allPois = pois?.allPois || NO_POIS
  // 采集方案:用户自定义的一组品种,激活时替代手动勾选成为筛选依据。
  //
  // 没用 useStoredJSON:它内部对 storage.getItem/setItem 不做容错,而隐私模式下
  // 访问 localStorage 会抛 SecurityError(Safari 无痕最直接)—— 那会让整个地图页
  // 白屏,比丢一份配置严重得多。故与其它几个键一样自己包 try/catch。
  const [plans, setPlans] = useState(loadPlans)
  const activeId = plans.active

  useEffect(() => {
    try { localStorage.setItem(PLANS_LS_KEY, JSON.stringify(plans)) } catch { /* 隐私模式下忽略 */ }
  }, [plans])

  useEffect(() => {
    try { localStorage.setItem(GATHER_LS_KEY, on ? '1' : '0') } catch { /* 隐私模式下忽略 */ }
  }, [on])
  useEffect(() => {
    try { localStorage.setItem(KINDS_LS_KEY, JSON.stringify([...offKinds])) } catch { /* 同上 */ }
  }, [offKinds])

  // 后端每次都推全量列表(实体进出已按窗口合并),整体替换即可;
  // 断线重连期间的增量丢失了,故重连成功时补拉一次全量快照。
  useEffect(() => subscribe('gathers', (d) => setData(d), { onOpen: refresh }), [refresh, setData])

  // 此刻视野内各品种的数量。只依赖 gathers,位置推送(每秒 8 次)不触发重算。
  const liveCount = useMemo(() => {
    const m = new Map()
    for (const g of gathers) if (g.n) m.set(g.n, (m.get(g.n) || 0) + 1)
    return m
  }, [gathers])

  // 品种清单 = 候选点表登记的全集 ∪ 实时标记里出现过的。
  //
  // 取并集而非只用候选点表:点位表对某些品种登记不全(实测 npc_cfg_id 50047 在刷,
  // 点位表一条都没有,见 docs/data.md 3.3),只用静态清单会让这些品种**筛不掉** ——
  // 它们照样显示在图上,而面板里根本没有对应的开关。
  //
  // 静态那一半必须取 **allPois**(未过滤),不能取 marks:
  // marks 按图层开关筛过,而采集物图层默认是**关**的,marks 里一个采集物都没有 ——
  // 那样清单就只剩此刻视野内的几个,用户勾不到视野外的品种。
  const kinds = useMemo(() => {
    const info = new Map() // 品种名 -> { icon, cand }
    for (const p of allPois) {
      if (p.k !== GATHER_KIND || !p.n) continue
      const e = info.get(p.n) || { icon: '', cand: 0 }
      e.cand++
      if (p.i) e.icon = p.i // 图标是品种级的,任取一点即可
      info.set(p.n, e)
    }
    for (const g of gathers) {
      if (!g.n) continue
      const e = info.get(g.n) || { icon: '', cand: 0 }
      if (g.icon) e.icon = g.icon
      info.set(g.n, e)
    }
    // 排序**只按名称**,不按 live 数量降序。
    //
    // 试过「此刻有的排前面」,实测站不住:视野内通常只有 4 个而清单有 41 个,
    // 于是 37 个 live=0 的按名称排、4 个 live>0 的浮在顶部;玩家一走动,
    // 某品种 live 从 0 变 1 就整行从底部窜到顶部 —— 若此刻正展开列表伸手去勾,
    // 点到的就是另一个品种了(表现为「我明明点的是这个」)。
    //
    // 这是**交互控件**而非信息看板,且用法是「配一次长期生效」,故稳定性优先:
    // 固定名称序让用户能形成位置记忆。「此刻有没有」改由 live 计数与高亮传达,
    // 不需要靠行位置 —— 那本来也是实时图层自己在做的事。
    return [...info.entries()]
      .map(([name, e]) => ({ name, icon: e.icon, cand: e.cand, live: liveCount.get(name) || 0 }))
      .sort((a, b) => a.name.localeCompare(b.name, 'zh'))
  }, [allPois, gathers, liveCount])

  const toggle = () => setOn((v) => !v)

  // 品种勾选:方案激活时**直接改方案内容**,而非脱离方案。
  //
  // 取舍:方案是用户自己的东西,「我现在按这个方案采集,顺便加上火焰石」的直觉结果
  // 就是方案里多了火焰石。若改成「一动就脱离方案」,用户想补一个品种反而丢掉当前
  // 方案,还得重新点回来;若做成「已修改,是否保存」的弹窗,为了勾一个品种要
  // 多点两下 —— 功能没错,但每次操作都要先回答一个问题,太重。
  //
  // 想临时看而不改方案:先「退出方案」(⏏)再勾,或直接用主开关关掉这一层。
  const toggleKind = (name) => {
    if (activeId) {
      setPlans((s) => ({
        ...s,
        plans: s.plans.map((p) => {
          if (p.id !== activeId) return p
          const has = p.kinds.includes(name)
          return { ...p, kinds: has ? p.kinds.filter((x) => x !== name) : [...p.kinds, name] }
        }),
      }))
      return
    }
    setOffKinds((prev) => {
      const next = new Set(prev)
      next.has(name) ? next.delete(name) : next.add(name)
      return next
    })
  }

  // 全开/全关:方案激活时同样作用于方案内容;否则作用于手动的关闭集合。
  const setAllKinds = (show) => {
    const names = kinds.map((k) => k.name)
    if (activeId) {
      setPlans((s) => ({
        ...s,
        plans: s.plans.map((p) => (p.id === activeId ? { ...p, kinds: show ? names : [] } : p)),
      }))
      return
    }
    setOffKinds(show ? new Set() : new Set(names))
  }

  const toggleKindsOpen = () => setKindsOpen((v) => !v)

  // 当前生效的品种集合:方案激活时以方案为准,否则是「全集 - 手动关闭的」。
  //
  // 用「是否含于 allowed」而非「是否被禁用」:方案里的品种列表是**封闭**的,
  // 官方新加的品种不在方案里就不会显示(手动模式下新品种默认可见)。这是有意的 ——
  // 方案是用户明确圈定的一组,悄悄混进新东西反而会打扰。
  const allowed = useMemo(() => {
    if (activeId) {
      const p = plans.plans.find((x) => x.id === activeId)
      return p ? new Set(p.kinds) : null
    }
    return null
  }, [plans, activeId])

  // 供 POI 候选点图层复用的判定:两个图层展示的是同一批采集物,
  // 品种筛选必须**同时**对两层生效 —— 只筛实时层的话,打开候选点时
  // 图上还是画满 3552 个点,用户会以为筛选坏了。
  const allowName = useCallback(
    (name) => (allowed ? allowed.has(name) : !offKinds.has(name)),
    [allowed, offKinds])

  // marks:主开关 + 品种筛选。无名的标记(品种查不到)一律保留 ——
  // 位置是真的,只是说不出它叫什么,藏掉等于在图上开个洞。
  const marks = useMemo(
    () => (on ? gathers.filter((g) => allowName(g.n)) : []),
    [gathers, on, allowName])

  const shownKinds = kinds.filter((k) => allowName(k.name)).length

  // —— 方案操作 ——
  const activatePlan = (id) => setPlans((s) => ({ ...s, active: id }))
  // 退出方案:把当前方案内容**落地成手动的关闭集合**再清空 active。
  // 不这么做的话,退出瞬间图上会突然多出一批刚被方案挡住的品种,像是筛选跳变了。
  const deactivatePlan = () => setPlans((s) => {
    const p = s.plans.find((x) => x.id === s.active)
    if (!p) return { ...s, active: null }
    const keep = new Set(p.kinds)
    setOffKinds(new Set(kinds.map((k) => k.name).filter((n) => !keep.has(n))))
    return { ...s, active: null }
  })
  const createPlan = (name) => {
    const id = newPlanId()
    // 存的是**当前实际显示**的品种(方案激活时即方案内容),所见即所得。
    const picked = kinds.filter((k) => allowName(k.name)).map((k) => k.name)
    setPlans((s) => ({ plans: [...s.plans, { id, name, kinds: picked }], active: id }))
  }
  const renamePlan = (id, name) => setPlans((s) => ({
    ...s, plans: s.plans.map((p) => (p.id === id ? { ...p, name } : p)),
  }))
  const deletePlan = (id) => {
    // 删前先把方案内容落地到手动集合,理由同 deactivatePlan。
    deactivatePlan()
    setPlans((s) => ({ plans: s.plans.filter((p) => p.id !== id), active: null }))
  }

  return {
    marks, on, toggle,
    total: gathers.length,
    allowName, kindsOff: offKinds,
    kinds, kindsOpen, toggleKindsOpen, toggleKind, setAllKinds,
    shownKinds,
    plans: plans.plans, activeId,
    activatePlan, deactivatePlan, createPlan, renamePlan, deletePlan,
    sceneResId: (data || NO_GATHERS).sceneResId,
  }
}
