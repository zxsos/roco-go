import React, { useEffect, useState } from 'react'
import {
  getAdminStatus, adminSetup, adminLogin, adminLogout, getAdminUsers,
  getAdminToken, setAdminToken,
} from '../api'
import { fmtTime } from '../utils/format'

// Admin 管理员面板:首次进入引导设置密码 → 登录 → 查看各玩家使用情况。
// 密码经加盐哈希存于服务端库,登录令牌存浏览器 localStorage;服务重启后令牌失效需重新登录。
export default function Admin() {
  const [configured, setConfigured] = useState(null) // null=加载中,false=未设置(引导设密码),true=已设置
  const [authed, setAuthed] = useState(!!getAdminToken())
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [users, setUsers] = useState([])
  const [loadingUsers, setLoadingUsers] = useState(false)

  const refreshUsers = () => {
    setLoadingUsers(true)
    getAdminUsers()
      .then(setUsers)
      .catch(() => {
        // 令牌失效(如服务端重启):清掉本地令牌,回登录态
        setAdminToken('')
        setAuthed(false)
      })
      .finally(() => setLoadingUsers(false))
  }

  useEffect(() => {
    getAdminStatus()
      .then((d) => {
        setConfigured(d.configured)
        if (d.configured && getAdminToken()) refreshUsers()
      })
      .catch(() => setConfigured(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const submit = async (e) => {
    e.preventDefault()
    if (!configured) {
      if (pw.length < 6) { setErr('密码至少 6 位'); return }
      if (pw !== pw2) { setErr('两次输入的密码不一致'); return }
    }
    if (!pw) return
    setBusy(true); setErr('')
    try {
      const res = configured ? await adminLogin(pw) : await adminSetup(pw)
      setAdminToken(res.token)
      setAuthed(true)
      setPw(''); setPw2('')
      refreshUsers()
    } catch (e2) { setErr(e2.message) } finally { setBusy(false) }
  }

  const logout = async () => {
    await adminLogout()
    setAdminToken('')
    setAuthed(false)
    setUsers([])
  }

  if (configured === null) return <div className="empty">加载中…</div>

  if (!authed) {
    return (
      <div className="admin-login">
        <div className="admin-card">
          <h3>{configured ? '管理员登录' : '首次使用 · 设置管理员密码'}</h3>
          <p className="muted">
            {configured
              ? '输入管理员密码后,可查看各玩家的使用情况(宠物/事件/会话/活跃时间)。'
              : '这是本服务第一次访问管理面板,请先设置管理员密码(至少 6 位)。密码加盐哈希后存储,登录令牌仅保存在本浏览器。'}
          </p>
          <form onSubmit={submit} className="admin-form">
            <input className="input" type="password" placeholder="管理员密码" autoFocus
              value={pw} onChange={(e) => setPw(e.target.value)} />
            {!configured && (
              <input className="input" type="password" placeholder="确认密码"
                value={pw2} onChange={(e) => setPw2(e.target.value)} />
            )}
            {err && <div className="admin-err">{err}</div>}
            <button className="btn primary" disabled={busy || !pw}>
              {busy ? '处理中…' : (configured ? '登录' : '设置密码并进入')}
            </button>
          </form>
        </div>
      </div>
    )
  }

  return (
    <div>
      <div className="toolbar">
        <h3 style={{ margin: 0 }}>管理员面板</h3>
        <span className="muted toolbar-hint">各玩家使用情况 · 宠物 / 事件 / 会话 / 活跃时间</span>
        <div className="spacer" style={{ flex: 1 }} />
        <button className="btn" onClick={refreshUsers} disabled={loadingUsers}>刷新</button>
        <button className="btn" onClick={logout}>退出登录</button>
      </div>
      <div className="table-wrap">
        <table className="admin-table">
          <thead>
            <tr>
              <th>玩家</th><th>UID</th><th>状态</th>
              <th>宠物</th><th>事件</th><th>会话</th>
              <th>首次出现</th><th>最近活跃</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.account}>
                <td>{u.name || '—'}</td>
                <td className="muted">{u.account.replace(/^UID:/, '') || u.account}</td>
                <td>
                  <span className={'online ' + (u.online ? 'on' : 'off')}>
                    {u.online ? '● 在线' : '○ 离线'}
                  </span>
                </td>
                <td>{u.petCount}</td>
                <td>{u.eventCount}</td>
                <td>{u.sessionCount}</td>
                <td className="muted">{fmtTime(u.firstSeen)}</td>
                <td className="muted">{fmtTime(u.updatedAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {users.length === 0 && <div className="empty">暂无玩家记录</div>}
      </div>
    </div>
  )
}
