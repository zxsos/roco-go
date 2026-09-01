// 标注模式(众包图鉴)的前端支撑:
//   - AnnotationsProvider:App 层挂载,拉取全服已审核标注(kind -> code -> {name,desc}),
//     供各页把协议里查不到名字的技能/特性 id 显示成名字;
//   - AnnotationModal:对某个未知 id 搜索候选词典、选中并提交(进入待审,管理员审核后全服可见);
//   - UnknownChip:未知 id 的统一展示块(id + 「标注」按钮,点击打开 AnnotationModal)。
//
// 数据流:玩家提交 → 管理员在 #/admin 审核 → 审核通过后 GET /api/annotations 全服下发,
// 刷新页面即见。提交的标注在通过前只有管理员能看到(展示侧不显示 pending)。
import React, { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { getAnnotations, getAnnotationCandidates, submitAnnotation } from '../api'
import { AnnotationsContext } from '../context'
import { toast } from './toast'

// AnnotationsProvider 拉取两类已审核标注并缓存。
export default function AnnotationsProvider({ children }) {
  const [byKind, setByKind] = useState({ skill: {}, feature: {} })

  const refresh = useCallback(() => {
    Promise.all(['skill', 'feature'].map(async (kind) => {
      const d = await getAnnotations(kind)
      const m = {}
      for (const it of (d.items || [])) m[it.code] = { name: it.name, desc: it.desc }
      setByKind((prev) => ({ ...prev, [kind]: m }))
    })).catch(() => { /* 拉取失败时维持旧缓存,页面照常展示 id */ })
  }, [])

  useEffect(() => { refresh() }, [refresh])

  const value = useMemo(() => ({
    // lookup 查已审核标注:命中返回 {name,desc},未命中返回 null。
    lookup: (kind, code) => (byKind[kind] || {})[code] || null,
    refresh,
  }), [byKind, refresh])

  return <AnnotationsContext.Provider value={value}>{children}</AnnotationsContext.Provider>
}

// useAnnotations 供页面消费标注缓存。
export function useAnnotations() {
  return useContext(AnnotationsContext)
}

// AnnotationModal 标注弹窗。
//   kind: 'skill' | 'feature'   code: 协议里的未知 id
// 交互:输入框过滤候选词典 → 点击候选填名(与描述)→ 提交。提交后进入待审,
// 提示「已提交,等待管理员审核」并关闭。候选词典来自后端
// GET /api/annotation-candidates(skill=全量技能目录,feature=wiki 特性词典)。
export function AnnotationModal({ kind, code, onClose }) {
  const { refresh } = useAnnotations()
  const [candidates, setCandidates] = useState(null) // null = 加载中
  const [q, setQ] = useState('')
  const [name, setName] = useState('')
  const [desc, setDesc] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [err, setErr] = useState('')
  const inputRef = useRef(null)

  useEffect(() => {
    let alive = true
    getAnnotationCandidates(kind).then((d) => { if (alive) setCandidates(d.items || []) })
      .catch(() => { if (alive) setCandidates([]) })
    return () => { alive = false }
  }, [kind])
  useEffect(() => { if (inputRef.current) inputRef.current.focus() }, [])

  // 过滤候选:名字/描述含搜索词(忽略大小写);特性候选还按描述搜。
  const filtered = useMemo(() => {
    const kw = q.trim().toLowerCase()
    if (!candidates) return []
    if (!kw) return candidates.slice(0, 50)
    return candidates.filter((c) =>
      c.name.toLowerCase().includes(kw) || (c.desc || '').toLowerCase().includes(kw),
    ).slice(0, 50)
  }, [candidates, q])

  const pick = (c) => {
    setName(c.name)
    setDesc((c.desc || '').slice(0, 500))
    setErr('')
  }

  const submit = async () => {
    if (!name.trim()) { setErr('请先从候选里选择一个名字'); return }
    setSubmitting(true)
    setErr('')
    try {
      await submitAnnotation(kind, code, name.trim(), desc.trim())
      toast('已提交,等待管理员审核')
      refresh() // 重新拉取(虽然本次 pending 不会出现,但保持缓存与后端一致)
      onClose()
    } catch (e) {
      setErr(e.message || '提交失败')
      setSubmitting(false)
    }
  }

  const kindLabel = kind === 'skill' ? '技能' : '特性'

  return (
    <div className="confirm-backdrop show" onClick={() => onClose()}>
      <div className="confirm-dialog anno-dialog" onClick={(e) => e.stopPropagation()}>
        <div className="anno-title">标注{kindLabel} id {code}</div>
        <div className="anno-hint">在候选里搜索并选择一个名字,提交后由管理员审核、全服共享。</div>
        <input
          ref={inputRef}
          className="anno-input"
          placeholder={`搜索${kindLabel}候选…`}
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <div className="anno-list">
          {!candidates
            ? <div className="anno-empty">候选加载中…</div>
            : filtered.length === 0
              ? <div className="anno-empty">没有匹配的候选,试试其它关键词</div>
              : filtered.map((c) => (
                // pets 是「wiki 上哪些精灵带这个特性」:玩家对着裸 id 没有线索,
                // 凭战斗表现猜答案时,这条归属往往是能直接印证的那一块拼图。
                <button
                  key={c.id || c.name}
                  type="button"
                  className="anno-item"
                  title={c.pets && c.pets.length > 0 ? `拥有该特性的精灵:${c.pets.join('、')}` : undefined}
                  onClick={() => pick(c)}
                >
                  <span className="anno-item-name">{c.name}</span>
                  {c.desc && <span className="anno-item-desc">{c.desc}</span>}
                  {c.id && <span className="anno-item-id">id {c.id}</span>}
                </button>
              ))}
        </div>
        <input
          className="anno-input"
          placeholder="名字(必填,一般与选中的候选一致)"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <textarea
          className="anno-input anno-desc"
          placeholder="描述(选填)"
          rows={2}
          value={desc}
          onChange={(e) => setDesc(e.target.value)}
        />
        {err && <div className="anno-err">{err}</div>}
        <div className="confirm-actions">
          <button type="button" className="btn" onClick={() => onClose()}>取消</button>
          <button type="button" className="btn primary" disabled={submitting} onClick={submit}>
            {submitting ? '提交中…' : '提交标注'}
          </button>
        </div>
      </div>
    </div>
  )
}

// UnknownChip 未知 id 的展示块:显示 id,提供「标注」入口。
// 游戏协议只给 id 时(试炼特性/技能),内置词典查不到就渲染这个;
// 玩家点「标注」打开 AnnotationModal。
export function UnknownChip({ kind, code }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <span className="anno-unknown" title={`未知${kind === 'skill' ? '技能' : '特性'} id ${code} — 点击标注`}>
        {code}
        <button type="button" className="anno-chip-btn" onClick={() => setOpen(true)}>标注</button>
      </span>
      {open && <AnnotationModal kind={kind} code={code} onClose={() => setOpen(false)} />}
    </>
  )
}
