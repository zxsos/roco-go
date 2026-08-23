import { useRef, useState, useCallback } from 'react'

// useReveal 截图防泄的「触屏长按揭示」部分。
//
// 配套 CSS:敏感文字默认 filter:blur,桌面靠 :hover 揭示(纯 CSS,无需 JS);
// 触屏没有 hover,改用长按(≥400ms)揭示、松手恢复。这里只管触屏那一半。
//
// 用法:const { revealed, press, release } = useReveal()
//   <span className={'privacy' + (revealed ? ' reveal' : '')}
//     onTouchStart={press} onTouchEnd={release} onTouchCancel={release}>...</span>
//
// 设计要点:
// - 只在触屏生效(pointer: coarse),桌面 hover 走 CSS,press 不做任何事;
// - 长按达阈值才置 revealed=true,短按(点按钮/选条目)不误触发;
// - 松手/取消立刻恢复,避免截图时恰好处于揭示态;
// - 组件卸载时清掉计时器,防止 setState on unmounted。
export function useReveal(delay = 400) {
  const coarse = typeof window !== 'undefined'
    && window.matchMedia && window.matchMedia('(pointer: coarse)').matches
  const [revealed, setRevealed] = useState(false)
  const timer = useRef(null)

  const press = useCallback((e) => {
    // 仅触屏;桌面按 :hover,这里 noop。e 可能是 TouchEvent 或合成事件。
    if (!coarse) return
    if (timer.current) { clearTimeout(timer.current); timer.current = null }
    // 阻止长按触发系统的文字选择菜单/呼出菜单,避免干扰
    if (e && e.preventDefault) e.preventDefault()
    timer.current = setTimeout(() => setRevealed(true), delay)
  }, [coarse, delay])

  const release = useCallback(() => {
    if (timer.current) { clearTimeout(timer.current); timer.current = null }
    setRevealed(false)
  }, [])

  return { revealed, press, release }
}
