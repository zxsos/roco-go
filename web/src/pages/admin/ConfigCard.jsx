import React, { useCallback, useState } from 'react'
import { adminConfig, adminConfigSave } from '../../api'
import { useAdminFetch } from './useAdminFetch'

// ConfigCard 运行期配置:邮箱 SMTP、第三方图鉴令牌、内置 SOCKS5 代理。
//
// 三块配置的生效代价不同,界面上要分别说清楚,不能笼统一句「保存后重启」:
//   - 邮箱 / 令牌   改完**立即生效**(纯内存热更)
//   - SOCKS5 代理   改完**立即热重启**(它是独立 goroutine,不影响抓包与 Web 服务)
//   - 监听地址/TLS  **本轮不在此处提供** —— 改它等于让正在处理你请求的服务器当场消失,
//                   一旦失败(端口被占)就是远端失联,须另配防变砖保护再做
//
// 落盘位置是 /etc/rocom.env(systemd 的 EnvironmentFile),由后端写入;前端不关心,
// 只在配置不可写时把后端的说明原样显示出来。

export default function ConfigCard({ onUnauthed }) {
  const { data, error, refresh } = useAdminFetch(useCallback(() => adminConfig(), []), onUnauthed)
  const [form, setForm] = useState(null)      // null = 尚未编辑,显示后端值
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')

  // 表单值:未编辑时回落到后端下发的当前值
  const f = form ?? {
    smtpUser: data?.smtpUser ?? '',
    smtpPass: '',
    eggKey: '',
    socks5: {
      addr: data?.socks5?.addr ?? '',
      allow: data?.socks5?.allow ?? '',
      block: data?.socks5?.block ?? '',
      maxConns: data?.socks5?.maxConns ?? 0,
      user: data?.socks5?.user ?? '',
      pass: '',
    },
  }
  const set = (patch) => { setForm({ ...f, ...patch }); setMsg(''); setErr('') }
  const setS5 = (patch) => set({ socks5: { ...f.socks5, ...patch } })

  const save = async () => {
    setBusy(true); setMsg(''); setErr('')
    try {
      // 敏感字段留空 = 不修改(后端约定)。只提交用户真的填了的东西,
      // 故代理那块总是提交(要能关掉它),但密码留空即沿用。
      await adminConfigSave({
        smtpUser: f.smtpUser,
        smtpPass: f.smtpPass,
        eggKey: f.eggKey,
        socks5: {
          addr: f.socks5.addr,
          allow: f.socks5.allow,
          block: f.socks5.block,
          maxConns: Number(f.socks5.maxConns) || 0,
          user: f.socks5.user,
          pass: f.socks5.pass,
        },
      })
      setForm(null)          // 清空草稿,重新显示后端值(脱敏后的)
      setMsg('已保存并生效。')
      refresh()
    } catch (e) {
      setErr(e.message || '保存失败')
    } finally {
      setBusy(false)
    }
  }

  if (error) return <div className="admin-card admin-wide"><h3>运行配置</h3><p className="admin-error">{error.message}</p></div>
  if (!data) return <div className="admin-card admin-wide"><h3>运行配置</h3><p className="admin-hint">加载中…</p></div>

  // 配置不可写(手动运行的进程通常没有 /etc/rocom.env):整块只读,
  // 与其让人改了存不下,不如一开始就说清该去哪儿改。
  if (!data.writable) {
    return (
      <div className="admin-card admin-wide">
        <h3>运行配置</h3>
        <p className="admin-hint">
          当前进程的配置不可写({data.path || '/etc/rocom.env'}),故此处为只读。
          通常由 systemd 部署后才会生成该文件;手动运行二进制时请在服务器上直接编辑它,
          或用 <code>sudo ./scripts/deploy.sh</code> 部署后再来改。
        </p>
      </div>
    )
  }

  const s5 = data.socks5 ?? {}

  return (
    <div className="admin-card admin-wide">
      <h3>运行配置</h3>
      <p className="admin-hint">
        改动会写入 <code>{data.path}</code> 并立即生效,邮箱与令牌无需重启;
        代理会热重启(不影响抓包)。监听地址、HTTPS、抓包网卡属启动项,改它们需要
        编辑该文件后执行 <code>systemctl restart rocom</code>。
      </p>

      <div className="admin-config-group">
        <h4>远行商人订阅邮件</h4>
        <label className="admin-field">
          <span>发件邮箱</span>
          <input
            type="text" value={f.smtpUser} placeholder="例如 123456@qq.com"
            onChange={(e) => set({ smtpUser: e.target.value })}
          />
        </label>
        <label className="admin-field">
          <span>SMTP 授权码</span>
          <input
            type="password" value={f.smtpPass} autoComplete="new-password"
            placeholder={data.smtpPassSet ? '已设置,留空表示不修改' : '未设置'}
            onChange={(e) => set({ smtpPass: e.target.value })}
          />
        </label>
        {data.smtpPassSet
          ? <p className="admin-hint">已设置授权码。留空则不改动它。</p>
          : <p className="admin-hint">未设置,订阅提醒不可用(商家数据本身不受影响)。</p>}
      </div>

      <div className="admin-config-group">
        <h4>第三方图鉴令牌</h4>
        <label className="admin-field">
          <span>API 令牌</span>
          <input
            type="password" value={f.eggKey} autoComplete="new-password"
            placeholder={data.eggKeySet ? '已设置,留空表示不修改' : '未设置'}
            onChange={(e) => set({ eggKey: e.target.value })}
          />
        </label>
        <p className="admin-hint">
          查「神奇的蛋」可能物种用。只存服务端、不下发前端,故此处不显示原文。
          {data.eggKeySet ? ' 已设置,留空则不改动。' : ' 未设置,该项查询不可用。'}
        </p>
      </div>

      <div className="admin-config-group">
        <h4>内置 SOCKS5 代理</h4>
        <p className="admin-hint">
          {s5.running
            ? <>当前运行中:{s5.realAddr}。改端口会热重启,改其它项不中断连接。</>
            : '当前未启用。填监听地址即可开启(如 :1080);留空 = 不启用。'}
        </p>
        <label className="admin-field">
          <span>监听地址</span>
          <input
            type="text" value={f.socks5.addr} placeholder="留空 = 不启用,如 :1080"
            onChange={(e) => setS5({ addr: e.target.value })}
          />
        </label>
        <label className="admin-field">
          <span>客户端白名单</span>
          <input
            type="text" value={f.socks5.allow} placeholder="逗号分隔,支持 IP 或 CIDR;留空 = 不限制"
            onChange={(e) => setS5({ allow: e.target.value })}
          />
        </label>
        <label className="admin-field">
          <span>屏蔽域名</span>
          <input
            type="text" value={f.socks5.block} placeholder="逗号分隔;留空 = 不屏蔽"
            onChange={(e) => setS5({ block: e.target.value })}
          />
        </label>
        <label className="admin-field">
          <span>并发上限</span>
          <input
            type="number" min="0" value={f.socks5.maxConns}
            onChange={(e) => setS5({ maxConns: e.target.value })}
          />
        </label>
        <label className="admin-field">
          <span>认证用户名</span>
          <input
            type="text" value={f.socks5.user} placeholder="留空 = 无认证"
            onChange={(e) => setS5({ user: e.target.value })}
          />
        </label>
        <label className="admin-field">
          <span>认证密码</span>
          <input
            type="password" value={f.socks5.pass} autoComplete="new-password"
            placeholder={s5.passSet ? '已设置,留空表示不修改' : '未设置'}
            onChange={(e) => setS5({ pass: e.target.value })}
          />
        </label>
        <p className="admin-hint">
          ⚠ 公网暴露时务必填白名单:RFC 1929 的密码是明文传输的,挡得住扫描器,
          挡不住任何能碰到这段流量的人。
        </p>
      </div>

      <div className="admin-config-actions">
        <button className={'btn' + (busy ? ' is-loading' : '')} type="button" disabled={busy} onClick={save}>
          保存并生效
        </button>
        {form && <button className="btn" type="button" disabled={busy} onClick={() => { setForm(null); setErr('') }}>放弃修改</button>}
      </div>

      {err && <p className="admin-error">{err}</p>}
      {msg && <p className="admin-hint" style={{ color: 'var(--green, #4caf50)' }}>{msg}</p>}
    </div>
  )
}
