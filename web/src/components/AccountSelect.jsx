import React, { useRef } from 'react'
import { useDropdown } from '../hooks/useDropdown'
import { useMediaQuery } from '../hooks/useMediaQuery'
import { uidOf } from '../data/nav'
import RankTitle from './RankTitle'
import AccountAvatar from './AccountAvatar'
import AccountItem from './AccountItem'
import AccountSheet from './AccountSheet'
import { IconLock } from './svg'

// 断点必须与 CSS 同步:styles/shell.css 的 @media (max-width: 760px) 与
// styles/base.css 的移动端触控基线都用它。改一处要同步另一处。
// 它决定**渲染哪套 DOM**(锚定浮层 / 底部 sheet),CSS 只决定各自的样式。
const MOBILE_Q = '(max-width: 760px)'

// AccountSelect 账号切换器:桌面端锚定浮层,手机端底部 sheet。
//
// 为什么不是一套 DOM 靠 CSS 变形态 —— sheet 必须 createPortal 到 document.body
// (.topbar 的 backdrop-filter 会成为 fixed 后代的包含块,详见 AccountSheet.jsx),
// 而 portal 与否是 DOM 结构问题,CSS 改不了。
//
// 但**行为只有一份**(useDropdown)、**行渲染只有一份**(AccountItem):
// 两套 DOM 只是同一套内容的两种呈现,不会出现改了桌面忘了手机。
//
// 键鼠/触屏均可操作:点击展开/选条;键盘 ↑↓ 切换、Enter 选择、Esc 关闭、Tab 收起,
// 点外部自动收起(见 hooks/useDropdown.js,与通用 Dropdown 共用)。
// 手机端 sheet 走 extraRef 纳入「点内部」判断,否则点它自己也会被当成点外部。
export default function AccountSelect({ accounts, current, onChange, onManagePin, onDeleteAccount }) {
  const selIdx = current ? accounts.findIndex((a) => a.account === current.account) : -1
  // sheet 根节点的 ref:传给 useDropdown 让它不被判成「点外部」。桌面端从不挂载,
  // .current 恒为 null,useOutsideClick 里的 extraRef 判据自然短路。
  const sheetRef = useRef(null)
  const { open, setOpen, hi, setHi, rootRef, ulRef, onKeyDown, pickAt } = useDropdown({
    count: accounts.length,
    selectedIndex: selIdx,
    extraRef: sheetRef,
    onPick: (i) => { if (accounts[i].account !== current?.account) onChange(accounts[i].account) },
  })
  const mobile = useMediaQuery(MOBILE_Q)

  const rows = accounts.map((a, i) => (
    <AccountItem
      key={a.account}
      account={a}
      cur={current && a.account === current.account}
      hi={i === hi}
      touch={mobile}
      onChoose={() => pickAt(i)}
      onHover={() => setHi(i)}
    />
  ))

  // 当前账号的 PIN 管理 + 删除入口。浮层与 sheet 共用(只有外层 CSS 不同)。
  const actions = current && (
    <li className="account-actions" onMouseEnter={() => setHi(-1)}>
      <button className="btn small account-action-btn" onClick={() => {
        setOpen(false)
        onManagePin?.(current)
      }}>管理 PIN</button>
      <button className="btn small account-action-btn account-del-btn" onClick={() => {
        setOpen(false)
        onDeleteAccount?.(current)
      }}>删除账号</button>
    </li>
  )

  return (
    <div className="account-wrap" ref={rootRef} onKeyDown={onKeyDown}>
      <button
        type="button"
        className={'select account-select account-trigger' + (open ? ' open' : '')}
        onClick={() => setOpen((o) => !o)}
        title="切换账号(玩家)"
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        {/* 顺序:头像 → 称号 → 锁 → 昵称 → UID。
            徽章在最左:它是唯一「一眼可辨」的元素,靠颜色和首字就能区分账号,
            不用逐条读名字;也把整条的视觉重心钉在左边,昵称再长也不影响定位。
            徽标(称号、锁)跟在头像后、昵称**之前**:它们是不随昵称长短变化的固定标识,
            挤在昵称和 UID 中间会被伸缩的文本推来推去。
            UID 只在桌面端显示(手机端顶栏空间紧张,且昵称更需要这点宽度),
            两种端都能在展开后的列表里查到。
            昵称与 UID 都带 .privacy,与全局截图防泄一致(见 shell.css 的 html[data-privacy])。 */}
        {current && <AccountAvatar account={current.account} name={current.name} online={current.online} avatar={current.avatar} />}
        {current && <RankTitle title={current.title} />}
        {current?.hasPin && <span className="account-pin-mark" title="已设 PIN 保护"><IconLock size={11} /></span>}
        <span className="privacy account-trigger-name">
          {current ? <span className="acct-name">{current.name}</span> : '选择账号…'}
        </span>
        {current && !mobile && <span className="privacy acct-uid">UID:{uidOf(current.account)}</span>}
        <span className="account-caret">▾</span>
      </button>

      {mobile ? (
        <AccountSheet open={open} onClose={() => setOpen(false)} sheetRef={sheetRef} title="切换账号">
          <ul className="account-list" ref={ulRef} role="listbox">
            {rows}
            {actions}
          </ul>
        </AccountSheet>
      ) : open && (
        <ul className="account-list account-dropdown" ref={ulRef} role="listbox">
          {rows}
          {actions}
        </ul>
      )}
    </div>
  )
}
