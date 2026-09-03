import { useState, useEffect, useMemo, useCallback } from 'react'
import { getGathers, subscribe } from '../../api'
import { useAsyncData } from '../../hooks/useAsyncData'

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
const NO_GATHERS = { gathers: [] }
const GATHER_LS_KEY = 'map.gatherLayer'

// 默认**开启**:这层只画此刻真有的那几个(个位数到二十来个),不像候选点那样糊屏;
// 而「现在能采什么」正是跑图时最想知道的。用 null 区分「用户没选过」。
const loadOn = () => {
  try {
    const v = localStorage.getItem(GATHER_LS_KEY)
    return v === null ? true : v === '1'
  } catch { return true }
}

// useGathers 管理实时采集物图层:订阅后端推送、按开关筛出可绘制的标记。
export function useGathers(account) {
  const { data, setData, refresh } = useAsyncData(
    useCallback(() => getGathers(), []),
    { fallback: NO_GATHERS, reloadKey: account },
  )
  const gathers = (data || NO_GATHERS).gathers || []
  const [on, setOn] = useState(loadOn)

  useEffect(() => {
    try { localStorage.setItem(GATHER_LS_KEY, on ? '1' : '0') } catch { /* 隐私模式下忽略 */ }
  }, [on])

  // 后端每次都推全量列表(实体进出已按窗口合并),整体替换即可;
  // 断线重连期间的增量丢失了,故重连成功时补拉一次全量快照。
  useEffect(() => subscribe('gathers', (d) => setData(d), { onOpen: refresh }), [refresh, setData])

  // 按品种归并计数,供图层面板显示「此刻有多少个 · 几个品种」。
  // 只依赖 gathers,位置推送(每秒 8 次)不触发重算。
  const byKind = useMemo(() => {
    const m = new Map()
    for (const g of gathers) m.set(g.n, (m.get(g.n) || 0) + 1)
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [gathers])

  const toggle = () => setOn((v) => !v)

  // marks 直接透传:这层没有收集状态、没有区间规则,开关是唯一的筛选项。
  // 位置推送不碰它,引用在 gathers 不变时保持稳定(PoiLayer 级别的 memo 才吃得满)。
  const marks = useMemo(() => (on ? gathers : []), [gathers, on])

  return { marks, on, toggle, total: gathers.length, byKind, sceneResId: (data || NO_GATHERS).sceneResId }
}
