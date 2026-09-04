import React from 'react'
import { imgURL, useImgFallback, useImgReady, InlineIcon } from '../../components/icons'
import { Types, Marks, Gender, Form, Blood, PetMark } from '../../components/badges'
import { SixGrid, Measure } from './metrics'
import { locTag, voiceHot, fmtShortTime } from '../../utils/format'

// PetCard 陈列视图里的一张宠物卡。与旧版移动卡片(PetCards.jsx)的区别不是"换个皮":
//
//   - **宠物图当主角**:旧版用 34px 头像,而 portraitSmall(全身像)早就随包下发、
//     此前只在详情弹窗里露过面。找宠物时认的是形象,不是那行小字。
//   - **数字改成读数**:六维从「三列纯文字」变成定标条(见 metrics.jsx 的 SixGrid),
//     体重/身高从「值 + 百分位两行」变成带游标的标尺 —— 扫视时读的是位置不是数字。
//   - **不再堆「标签：值」**:性格/特长/奖牌/血脉收成一行胶囊,体型与声音各占一行。
//
// 卡片是**正方形图 + 数据区**的纵向结构,在 240~300px 宽的自适应网格里排开:
// 一屏能看到十几只,异色/炫彩的角标在整片卡片里是能"扫"出来的。

// portrait 取全身像;没有则退大头像、再退小头像(与 components/avatar.jsx 同序)。
// 三者都缺(未上线形态/缺源)时组件回退 emoji,不留空洞。
const portrait = (p) => (p.image && (p.image.portraitSmall || p.image.bigHead || p.image.head)) || ''

export default function PetCard({ p, selected, itemProps }) {
  const src = portrait(p)
  const [bad, onError] = useImgFallback(src)
  const [ready, onLoad, onRef] = useImgReady(src)

  return (
    <article className={'pet-card' + (selected ? ' on' : '')} {...itemProps(p)}>
      <div className="pt-media">
        {src && !bad
          ? (
            <img
              ref={onRef}
              className={'pt-img' + (ready ? '' : ' img-ph')}
              src={imgURL(src)} alt={p.species} loading="lazy"
              onLoad={onLoad} onError={onError}
            />
          )
          : <span className="pt-fallback">{p.shiny ? '✨' : '🐾'}</span>}
        {/* 搭档标记压在图上左上角(与列表头像一致的位置),异色/炫彩压右上角:
            两者语义不同 —— 前者是玩家自己打的标,后者是稀有度,分开放才不会混。 */}
        <span className="pt-partner"><PetMark p={p} /></span>
        <span className="pt-flags"><Marks p={p} /></span>
        <span className="pt-lv">Lv.{p.level}</span>
      </div>

      <div className="pt-body">
        <div className="pt-name">{p.name || p.species}<Gender g={p.gender} /><Form form={p.form} /></div>
        <div className="pt-sub">{p.species}{p.book ? ` · #${p.book}` : ''}</div>
        <div className="pt-types"><Types types={p.types} icons={p.typeIcons} /></div>

        <SixGrid p={p} />

        <div className="pt-tags">
          {p.nature && <span className="pt-tag" title="性格">{p.nature}</span>}
          {p.speciality && p.speciality !== '无' && <span className="pt-tag" title="特长">{p.speciality}</span>}
          {p.medal && (
            <span className="pt-tag" title={p.medalDesc || '佩戴奖牌'}>
              <InlineIcon src={p.medalIcon} className="pt-medal-ic" alt="" />{p.medal}
            </span>
          )}
          {p.blood && <span className="pt-tag" title={'血脉 ' + p.blood}><Blood p={p} iconOnly />{p.blood}</span>}
          {p.eggGroups?.length > 0 && (
            <span className="pt-tag" title={p.eggGroups.map((g) => g.name).join(' / ')}>
              蛋组 {p.eggGroups.length}
            </span>
          )}
        </div>

        <Measure label="体重" value={p.weightKg} unit="kg" min={p.weightMin} max={p.weightMax} pct={p.weightPct} />
        <Measure label="身高" value={p.heightM} unit="m" min={p.heightMin} max={p.heightMax} pct={p.heightPct} />

        <div className="pt-foot">
          <span className="pt-voice">声 <b className={voiceHot(p.voice)}>{p.voice}</b></span>
          <span className="pt-loc" title={locTag(p)}>{locTag(p)}</span>
          <span className="pt-time muted">{fmtShortTime(p.catchTime)}</span>
        </div>
      </div>
    </article>
  )
}
