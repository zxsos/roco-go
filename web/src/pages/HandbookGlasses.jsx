import React, { useEffect, useState, useContext, useMemo } from 'react'
import { getHandbookGlasses } from '../api'
import { IconsContext } from '../context'
import { imgURL } from '../components/icons'
import { GlassChip } from '../components/badges'

// 炫彩图鉴:按品种聚合展示本账号收集到的普通/隐藏炫彩色卡。
// 数据来自登录包 pet_handbook(每次登录时快照更新),点击色卡可放大预览。
export default function HandbookGlasses() {
  const icons = useContext(IconsContext)
  const [data, setData] = useState(null) // null=加载中,其余为 glasses 数组
  const [err, setErr] = useState('')

  useEffect(() => {
    let dead = false
    setErr('')
    getHandbookGlasses().then((d) => {
      if (!dead) setData((d && d.glasses) || [])
    }).catch((e) => {
      if (!dead) { setData([]); setErr(e.message) }
    })
    return () => { dead = true }
  }, [])

  const stats = useMemo(() => {
    if (!data) return null
    let common = 0
    let hidden = 0
    for (const it of data) {
      common += (it.common || []).length
      hidden += (it.hidden || []).length
    }
    return { species: data.length, common, hidden }
  }, [data])

  return (
    <div className="hb-page">
      <div className="hb-head">
        <h2 className="hb-title">✨ 炫彩图鉴</h2>
        <span className="hb-note">数据来自登录包快照(每次登录更新),非实时</span>
      </div>

      {data === null ? (
        <div className="muted">加载中…</div>
      ) : (
        <>
          {stats.species > 0 && (
            <div className="hb-stats">
              <span className="hb-stat">收集品种 <b>{stats.species}</b></span>
              <span className="hb-stat">普通炫彩 <b>{stats.common}</b></span>
              <span className="hb-stat">隐藏炫彩 <b>{stats.hidden}</b></span>
            </div>
          )}

          {stats.species === 0 && !err ? (
            <div className="hb-empty">
              <span className="hb-empty-ic">{icons.colorful ? <img src={imgURL(icons.colorful)} alt="" /> : '彩'}</span>
              <p>还没有收集到炫彩。</p>
              <p className="hb-empty-sub">登录一次游戏后自动同步图鉴记录;同一品种的多个炫彩变体都会按图鉴号汇总在这里。</p>
            </div>
          ) : err ? (
            <div className="hb-empty">
              <p className="hb-empty-sub">加载失败:{err}</p>
            </div>
          ) : (
            <div className="hb-list">
              {data.map((it) => (
                <div className="hb-card" key={it.base}>
                  <img
                    className="hb-avatar"
                    src={imgURL(it.head)}
                    alt={it.name || `图鉴 #${it.book}`}
                    loading="lazy"
                  />
                  <div className="hb-meta">
                    <div className="hb-name">
                      {it.name || '未知品种'}
                      <span className="hb-book">图鉴 #{it.book}</span>
                    </div>
                    <div className="hb-cards">
                      {it.common.length > 0 && (
                        <span className="hb-group">
                          <span className="hb-group-label">普通</span>
                          {it.common.map((v) => (
                            <GlassChip key={v} p={{ glassType: 1, glassValue: v }} />
                          ))}
                        </span>
                      )}
                      {it.hidden.length > 0 && (
                        <span className="hb-group">
                          <span className="hb-group-label">隐藏</span>
                          {it.hidden.map((v) => (
                            <GlassChip key={v} p={{ glassType: 2, glassValue: v }} />
                          ))}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  )
}
