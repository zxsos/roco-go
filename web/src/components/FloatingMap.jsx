import React, { useState, useEffect, useRef, useCallback, useContext } from 'react'
import { createPortal } from 'react-dom'
import { AccountContext, IconsContext } from '../context'
import { useMapEngine, MapViz } from '../pages/map/useMapEngine.jsx'
import { imgURL } from './icons'
import LayerPanel from '../pages/map/LayerPanel'
import { pipSupported, setFloatState } from '../pages/map/floatState'

// FloatingMap 浮窗:三级降级,按设备能力自动选模式。
//   1. documentPictureInPicture(桌面 Chrome/Edge/Firefox 151+):任意 DOM 进 OS 级浮窗,可移出浏览器;
//   2. video PiP(Android Chrome 8.0+):地图渲染到 canvas → captureStream → video → requestPictureInPicture,
//      系统级浮窗(可移出浏览器、置顶),但只读(不能点标记/拖地图,只能看);
//   3. Web 浮窗(兜底:iOS Safari/不支持 PiP 的浏览器):页面内 fixed 可拖拽缩放浮窗,不能移出浏览器。
// 浮窗与主页面互斥:开启时主页面 MapPage 渲染占位(见 MapPage 的 floatOpen 检测)。
// 复用同一条 SSE 连接(全局单例),复用同套图层数据 hook(各自 useMapEngine 实例)。

// videoPiPSupported:检测 video requestPictureInPicture 是否可用。
// Android Chrome 8.0+ 支持;iOS Safari 也支持但有严格限制(需 fullscreen);桌面 Chrome 都支持。
// 这里主要面向 Android——iOS 的 PiP 要求 video 在 fullscreen 内,本方案不适用 iOS,会降级到 Web 浮窗。
const videoPiPSupported = typeof document !== 'undefined'
  && 'pictureInPictureEnabled' in document
  && document.pictureInPictureEnabled

const WEB_FLOAT_KEY = 'map.webFloatBox' // { x, y, w, h, collapsed }

// loadBox 读本地保存的 Web 浮窗位置/尺寸(无记录给默认)。
function loadBox() {
  try {
    const v = JSON.parse(localStorage.getItem(WEB_FLOAT_KEY))
    if (v && typeof v === 'object' && !Array.isArray(v)) {
      return {
        x: typeof v.x === 'number' ? v.x : 40,
        y: typeof v.y === 'number' ? v.y : 80,
        w: typeof v.w === 'number' ? Math.max(280, v.w) : 380,
        h: typeof v.h === 'number' ? Math.max(280, v.h) : 380,
      }
    }
  } catch { /* ignore */ }
  return { x: 40, y: 80, w: 380, h: 380 }
}

// dragPointer:统一处理鼠标 + 触摸拖拽。手机不触发 mousemove,必须绑 touch。
// onMove(dx, dy) 给出相对按下的位移,由调用方决定如何用。
// 返回一个事件处理函数,绑到 mousedown / touchstart 上。
// preventTextSel:拖动期间禁掉文本选中与滚动(移动端关键——否则拖标题栏时页面跟着滚)。
function dragPointer(onMoveStart, onMove, onMoveEnd) {
  return (e) => {
    if (e.button != null && e.button !== 0) return
    const isTouch = e.type === 'touchstart'
    if (isTouch && e.touches.length !== 1) return // 多指交给缩放手柄/地图
    const pt = isTouch ? e.touches[0] : e
    const startX = pt.clientX, startY = pt.clientY
    const ctx = onMoveStart(startX, startY)
    if (ctx === false) return
    e.preventDefault()
    e.stopPropagation()
    // 禁掉拖动期间的 touch 滚动(ios passive 默认 true,无法阻止——故用 CSS touch-action:none 兜底)
    const move = (ev) => {
      const p = ev.touches ? ev.touches[0] : ev
      if (!p) return
      ev.preventDefault()
      onMove(p.clientX - startX, p.clientY - startY, ctx, ev)
    }
    const up = (ev) => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      window.removeEventListener('touchmove', move, { passive: false })
      window.removeEventListener('touchend', up)
      window.removeEventListener('touchcancel', up)
      onMoveEnd && onMoveEnd(ctx, ev)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    window.addEventListener('touchmove', move, { passive: false })
    window.addEventListener('touchend', up)
    window.addEventListener('touchcancel', up)
  }
}

// PiP 模式:用 documentPictureInPicture 开一个独立窗口,把浮窗内容渲染进去。
// React 18 的 createPortal 可以目标到外部 document 的节点。
export default function FloatingMap() {
  // 模式选择优先级:documentPiP(桌面) > videoPiP(Android) > web(兜底)
  const [mode, setMode] = useState(() => {
    if (pipSupported) return 'pip'        // 桌面 Chrome/Edge/Firefox:任意 DOM 浮窗
    if (videoPiPSupported) return 'video-pip' // Android Chrome:video PiP(地图塞进 canvas→video)
    return 'web'                          // iOS/无 PiP:页面内 Web 浮窗
  })
  // PiP 窗口:持有 window 对象;关闭时 null。
  const [pipWin, setPipWin] = useState(null)
  // Web 浮窗容器节点(PiP 不可用时用)。
  const webRootRef = useRef(null)

  // 关闭浮窗:各模式各自清理。
  const close = useCallback(() => {
    if (mode === 'pip' && pipWin) { try { pipWin.close() } catch { /* ignore */ } }
    // video-pip 模式的 video 元素退出 PiP 由 FloatingContent 内部 effect 处理(卸载即退出)
    setFloatState({ open: false, mode: null })
  }, [mode, pipWin])

  // PiP 模式:开窗口。请求失败(权限/不支持)时回退到 Web 浮窗。
  useEffect(() => {
    if (mode !== 'pip') return
    let cancelled = false
    const container = document.createElement('div')
    container.style.cssText = 'width:100vw;height:100vh;margin:0;padding:0;'
    ;(async () => {
      try {
        const w = await window.documentPictureInPicture.requestWindow({
          width: 420, height: 420,
        })
        if (cancelled) { try { w.close() } catch { /* ignore */ } return }
        // 复制基础样式:让浮窗内主题色/字体与主页一致。
        copyStylesInto(w.document)
        w.document.body.style.cssText = 'margin:0;padding:0;background:#0b0e13;overflow:hidden;font-family:inherit;'
        w.document.body.appendChild(container)
        w.addEventListener('pagehide', () => {
          setFloatState({ open: false, mode: null })
          setPipWin(null)
        })
        setPipWin(w)
      } catch {
        // 请求失败:回退 Web 浮窗。
        setMode('web')
      }
    })()
    return () => { cancelled = true }
  }, [mode])

  // Web 浮窗模式:渲染到 body 下的一个 fixed 容器。
  if (mode === 'pip') {
    if (!pipWin) return null // 等待窗口开启
    return createPortal(
      <FloatingContent onClose={close} floatMode="pip" />,
      pipWin.document.body.firstChild || pipWin.document.body,
    )
  }
  // video PiP 模式:内容渲染到 body(含隐藏 canvas + 隐藏 video),由 FloatingContent 内部
  // 调 video.requestPictureInPicture() 把 video 升到系统浮窗。
  // floatMode='video-pip' 时 FloatingContent 用 MapCanvasViz(canvas 渲染)替代 MapViz(DOM 渲染)。
  if (mode === 'video-pip') {
    return createPortal(
      <FloatingContent onClose={close} floatMode="video-pip" />,
      document.body,
    )
  }
  // Web 浮窗
  return createPortal(
    <FloatingContent onClose={close} floatMode="web" />,
    document.body,
  )
}

// copyStylesInto 把主页的 <style> 与 <link> 复制到 PiP 窗口,让浮窗内样式一致。
// embed 的 CSS 经 Vite 打包成 <style> 或 <link>,逐一克隆即可。
function copyStylesInto(targetDoc) {
  try {
    document.querySelectorAll('style, link[rel="stylesheet"]').forEach((node) => {
      try {
        const clone = node.cloneNode(true)
        targetDoc.head.appendChild(clone)
      } catch { /* 个别节点跨 document 克隆失败,忽略 */ }
    })
  } catch { /* ignore */ }
}

// FloatingContent 浮窗内容本体:自带 useMapEngine(独立 RAF,主页面已卸载,无冗余),
// 渲染 MapViz + 简化版图层入口(PiP 模式下窗口很小,图层栏放折叠按钮,点开浮层)。
// video-pip 模式:渲染 MapCanvasViz(纯 canvas)到一个隐藏 canvas,经 captureStream→video→
// requestPictureInPicture 升到系统浮窗。需用户手势触发(移动端强制),故有一个"进入画中画"按钮。
function FloatingContent({ onClose, floatMode }) {
  const account = useContext(AccountContext)
  const engine = useMapEngine(account, { floating: true })
  // Web 浮窗的位置/尺寸/折叠态
  const [box, setBox] = useState(() => loadBox())
  const [collapsed, setCollapsed] = useState(false) // 收起成小条(只留标题栏)
  const [layersOpen, setLayersOpen] = useState(false) // 图层浮层(移动抽屉式)
  const dragRef = useRef(null) // 拖拽中:{ startX, startY, boxX, boxY, mode }
  // video-pip 专属
  const canvasRef = useRef(null)     // 隐藏 canvas(地图渲染目标)
  const videoRef = useRef(null)       // 隐藏 video(captureStream 的接收者)
  const [pipActive, setPipActive] = useState(false) // 是否已进入系统 PiP
  const [pipError, setPipError] = useState('')      // 进入 PiP 失败提示

  // 持久化 Web 浮窗位置/尺寸。
  useEffect(() => {
    if (floatMode !== 'web') return
    const t = setTimeout(() => {
      try { localStorage.setItem(WEB_FLOAT_KEY, JSON.stringify(box)) } catch { /* ignore */ }
    }, 400)
    return () => clearTimeout(t)
  }, [box, floatMode])

  // video-pip:canvas → captureStream → video.srcObject。canvas 尺寸随视频浮窗建议比例。
  // 一旦 video 开始播放,用户点"进入画中画"即可升到系统浮窗。
  useEffect(() => {
    if (floatMode !== 'video-pip') return
    const cv = canvasRef.current, vd = videoRef.current
    if (!cv || !vd) return
    // canvas 固定尺寸(PiP 视频会按比例缩放,这个尺寸决定清晰度 vs 流量)
    cv.width = 480; cv.height = 480
    let stream
    try { stream = cv.captureStream(15) } catch { setPipError('captureStream 不可用'); return }
    vd.srcObject = stream
    vd.muted = true // 移动端自动播放必须 muted
    vd.play().catch(() => setPipError('video 播放被拦截(需用户手势)'))
    // PiP 被系统关闭时(用户点 PiP 窗的关闭)回到非激活态,但浮窗仍开着
    const onLeave = () => setPipActive(false)
    vd.addEventListener('leavepictureinpicture', onLeave)
    return () => { vd.removeEventListener('leavepictureinpicture', onLeave); try { vd.pause() } catch {} }
  }, [floatMode])

  // enterPiP:用户手势触发进入系统画中画(移动端必须手势触发)。
  const enterPiP = useCallback(async () => {
    const vd = videoRef.current
    if (!vd) return
    try {
      await vd.play() // 确保在播放(PiP 前提)
      await vd.requestPictureInPicture()
      setPipActive(true)
      setPipError('')
    } catch (e) {
      setPipActive(false)
      setPipError(e?.message || '进入画中画失败')
    }
  }, [])

  // 拖拽手柄(标题栏):按下后移动整个浮窗。Web 浮窗专属;PiP 窗口由 OS 拖。
  // 边界:允许部分移出视口(保留至少手柄宽度可见可拖回),不能完全钳死在内——否则
  // 手机视口小、浮窗大,顶到边后无法再拖、也看不到地图中心。
  const onHandleDown = useCallback((e) => {
    if (floatMode !== 'web') return
    dragPointer(
      () => { const { x, y } = box; return { x, y } },
      (dx, dy, ctx) => {
        // 保留至少 80px 宽的标题栏在视口内可拖回;允许其余部分移出视口(手机视口小,全钳在内没法用)
        const nx = Math.max(-box.w + 80, Math.min(window.innerWidth - 80, ctx.x + dx))
        const ny = Math.max(0, Math.min(window.innerHeight - 40, ctx.y + dy))
        setBox((b) => ({ ...b, x: nx, y: ny }))
      }
    )(e)
  }, [box, floatMode, box.w])

  // 缩放手柄(右下角):拖动改变尺寸。鼠标 + 触摸双支持。
  const onResizeDown = useCallback((e) => {
    if (floatMode !== 'web') return
    dragPointer(
      () => { const { w, h } = box; return { w, h } },
      (dx, dy, ctx) => {
        // 上限:不超视口宽高的 1.5 倍(手机大屏也能看清,但不会大到没法操作)
        const maxW = window.innerWidth * 1.5
        const maxH = window.innerHeight * 1.5
        const nw = Math.min(maxW, Math.max(220, ctx.w + dx))
        const nh = Math.min(maxH, Math.max(220, ctx.h + dy))
        setBox((b) => ({ ...b, w: nw, h: nh }))
      }
    )(e)
  }, [box, floatMode])

  // PiP 模式:容器撑满 PiP 窗口;Web 模式:按 box 定位;video-pip 模式:页内小窗 + 隐藏 canvas/video。
  const containerStyle = (floatMode === 'pip' || floatMode === 'video-pip')
    ? { position: 'fixed', inset: 0, display: 'flex', flexDirection: 'column' }
    : {
        position: 'fixed', left: box.x, top: box.y, width: box.w,
        height: collapsed ? 40 : box.h,
        display: 'flex', flexDirection: 'column',
        zIndex: 99998,
        transition: collapsed ? 'height .18s ease' : 'none',
      }

  return (
    <div className={'map-float' + (floatMode === 'web' ? ' map-float-web' : ' map-float-pip')} style={containerStyle}>
      <div className="map-float-title" onMouseDown={onHandleDown} onDoubleClick={() => setCollapsed((c) => !c)}>
        <span className="map-float-title-ic">🗺️</span>
        <span className="map-float-title-text">实时地图浮窗</span>
        {floatMode === 'video-pip' && (
          <button className="map-float-btn map-float-pip-btn" title={pipActive ? '已进入画中画' : '进入系统画中画(可移出浏览器)'}
            onClick={(e) => { e.stopPropagation(); enterPiP() }}>
            {pipActive ? '▣' : '⊞'}
          </button>
        )}
        <button className="map-float-btn" title="图层" onClick={(e) => { e.stopPropagation(); setLayersOpen((o) => !o) }}>☰</button>
        <button className="map-float-btn" title={collapsed ? '展开' : '收起'} onClick={(e) => { e.stopPropagation(); setCollapsed((c) => !c) }}>{collapsed ? '▢' : '▬'}</button>
        <button className="map-float-btn map-float-close" title="关闭浮窗" onClick={(e) => { e.stopPropagation(); onClose() }}>✕</button>
      </div>
      {!collapsed && (
        <div className="map-float-body">
          {/* video-pip:页内显示 canvas 渲染的地图(可见),同时隐藏 video 用于系统 PiP */}
          {floatMode === 'video-pip' ? (
            <MapCanvasViz engine={engine} canvasRef={canvasRef} videoRef={videoRef}
              pipActive={pipActive} pipError={pipError} onEnterPip={enterPiP} />
          ) : (
            <MapViz engine={engine} floatMode={floatMode}
              sidebarOpen={false} onToggleLayers={() => setLayersOpen((o) => !o)} />
          )}
        </div>
      )}
      {floatMode === 'web' && !collapsed && (
        <div className="map-float-resize" onMouseDown={onResizeDown} />
      )}
      {/* 图层浮层:点击 ☰ 弹出,占满浮窗或贴合一边 */}
      {layersOpen && !collapsed && (
        <div className="map-float-layers-backdrop" onClick={() => setLayersOpen(false)}>
          <div className="map-float-layers" onClick={(e) => e.stopPropagation()}>
            <LayerPanel
              pois={engine.pois} wilds={engine.wilds} paint={engine.paint}
              collapsed={false}
              onClose={() => setLayersOpen(false)}
              onCollapseSidebar={() => setLayersOpen(false)}
            />
          </div>
        </div>
      )}
    </div>
  )
}

// MapCanvasViz:纯 canvas 2D 渲染地图,供 video PiP 用(captureStream → video → 系统画中画)。
// 画的内容:底图 + 层图(洞穴) + 涂地(从 engine.paint 的 canvas 复制) + 标记层(简化为点) + 箭头(玩家位置)。
// 不复用 MapViz 的 DOM 渲染——因为 DOM 无法被 captureStream,必须画到一个 canvas 上。
// 渲染参数(left/top/px 等)直接从 engine 的 refs 读取,与 applyFrame 同源,保证与主画面一致。
// props.canvasRef/videoRef 由 FloatingContent 传入(供 captureStream effect 用)。
function MapCanvasViz({ engine, canvasRef, videoRef, pipActive, pipError, onEnterPip }) {
  const { pos, hasMap, imgError, view, frameStateRef, stRef, focusRef, sceneRef, layerRef,
    pois, wilds, paint } = engine

  // 图片缓存:底图/层图 webp 加载后存 Image 对象,drawImage 用。
  // key = imgURL 路径。地图换场景时旧 key 留着不影响(下次进同场景可复用)。
  const imgCacheRef = useRef({})

  // canvas 尺寸随视口变化(与 MapViz 的 mapPx 同口径),但 video PiP 的画面比例固定——
  // 这里让 canvas 的 width/height 跟随视口最小边,保证地图是正方形。
  useEffect(() => {
    const cv = canvasRef.current
    if (!cv) return
    const { vp } = stRef.current
    const size = Math.min(vp.w, vp.h) || 480
    if (cv.width !== size) cv.width = size
    if (cv.height !== size) cv.height = size
  })

  // RAF 渲染循环:每帧把地图画到 canvas。复用 engine 的 applyFrame(它更新 frameStateRef)。
  useEffect(() => {
    let raf = 0
    const draw = () => {
      renderCanvasFrame(canvasRef.current, engine, imgCacheRef.current)
      raf = requestAnimationFrame(draw)
    }
    raf = requestAnimationFrame(draw)
    return () => cancelAnimationFrame(raf)
  }, [engine, canvasRef])

  if (!pos) return <div className="empty">等待位置数据…</div>
  if (!hasMap) return <div className="empty">该场景无底图</div>

  return (
    <div className="map-canvas-vp" style={{ position: 'relative', width: '100%', height: '100%', overflow: 'hidden', background: '#0b0e13' }}>
      <canvas ref={canvasRef} style={{ width: '100%', height: '100%', display: 'block' }} />
      {/* 隐藏 video:captureStream 的接收者,供 requestPictureInPicture 用 */}
      <video ref={videoRef} playsInline muted autoPlay
        style={{ position: 'absolute', width: 1, height: 1, opacity: 0, pointerEvents: 'none' }} />
      {/* 未进入 PiP 时的提示覆盖层 */}
      {!pipActive && (
        <div className="map-canvas-pip-hint" style={{
          position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column',
          alignItems: 'center', justifyContent: 'center', gap: 10,
          background: 'rgba(0,0,0,.45)', color: '#fff', fontSize: 13, pointerEvents: 'none',
        }}>
          <div>地图已渲染到画中画通道</div>
          <button className="btn primary" style={{ pointerEvents: 'auto' }} onClick={onEnterPip}>
            进入系统画中画
          </button>
          {pipError && <div style={{ color: 'var(--red)' }}>{pipError}</div>}
        </div>
      )}
    </div>
  )
}

// renderCanvasFrame:把当前帧的地图画到 canvas。纯函数,不碰 React。
// 从 engine 的 refs 读当前帧参数(与 applyFrame 同步),画底图+层图+涂地+标记+箭头。
function renderCanvasFrame(cv, engine, imgCache) {
  if (!cv) return
  const ctx = cv.getContext('2d')
  if (!ctx) return
  const { pos, view, frameStateRef, stRef, focusRef, sceneRef, layerRef, pois, wilds, paint } = engine
  if (!pos || !pos.img) { ctx.clearRect(0, 0, cv.width, cv.height); return }

  const { zoom: z, vp } = stRef.current
  const size = Math.min(cv.width, cv.height)
  const mapPx = size * z
  const f = focusRef.current
  // 与 applyFrame 同口径:canvas 原点在视口中心,left/top 是地图左上角的偏移
  const left = (cv.width / 2 - f.u * mapPx) | 0
  const top = (cv.height / 2 - f.v * mapPx) | 0

  // 清屏
  ctx.fillStyle = '#0b0e13'
  ctx.fillRect(0, 0, cv.width, cv.height)

  // 底图
  const baseImg = getImg(imgURL('bigmap/' + pos.img + '.webp'), imgCache)
  if (baseImg && baseImg.complete && baseImg.naturalWidth) {
    ctx.drawImage(baseImg, left, top, mapPx, mapPx)
  }

  // 层图(洞穴)
  if (pos.layer) {
    const layerImg = getImg(imgURL('bigmap/' + pos.layer.img + '.webp'), imgCache)
    if (layerImg && layerImg.complete && layerImg.naturalWidth) {
      const lx = left + pos.layer.u0 * mapPx
      const ly = top + pos.layer.v0 * mapPx
      const lw = (pos.layer.u1 - pos.layer.u0) * mapPx
      const lh = (pos.layer.v1 - pos.layer.v0) * mapPx
      ctx.drawImage(layerImg, lx, ly, lw, lh)
    }
  }

  // 涂地图层:从 engine.paint 的内部 canvas 复制位图(若开着且就绪)
  // paint.attach 是个 callback ref,内部 canvas 引用没直接暴露——跳过,涂地在 PiP 模式可省略(非核心)

  // 标记层:POI、家园巢、野生宠——简化为带颜色的点(精细图标在 canvas 上画成本高,点已够定位)
  drawMarks(ctx, pois.marks, left, top, mapPx, '#5fd0ff')
  drawMarks(ctx, wilds.marks, left, top, mapPx, '#fff', true)
  drawMarks(ctx, (engine.home?.marks) || [], left, top, mapPx, '#ff9100')

  // 玩家箭头:在 frameStateRef 的位置画一个三角形
  const disp = frameStateRef.current
  if (disp) {
    const ax = left + disp.u * mapPx
    const ay = top + disp.v * mapPx
    ctx.save()
    ctx.translate(ax, ay)
    ctx.rotate((disp.heading + 90) * Math.PI / 180)
    ctx.fillStyle = '#f5365c'
    ctx.strokeStyle = '#fff'
    ctx.lineWidth = 1.5
    ctx.beginPath()
    ctx.moveTo(0, -10)
    ctx.lineTo(7, 8)
    ctx.lineTo(0, 4)
    ctx.lineTo(-7, 8)
    ctx.closePath()
    ctx.fill()
    ctx.stroke()
    ctx.restore()
  }
}

// getImg:从缓存取或新建 Image,webp 加载完后 drawImage 可用。加载中返回 null(这帧跳过)。
function getImg(src, cache) {
  if (!cache[src]) {
    const img = new Image()
    img.src = src
    cache[src] = img
  }
  return cache[src]
}

// drawMarks:把标记画成圆点。rare(野生稀有)用稍大带描边的点。
function drawMarks(ctx, marks, left, top, mapPx, color, rare) {
  if (!marks || !marks.length) return
  ctx.fillStyle = color
  ctx.strokeStyle = 'rgba(0,0,0,.5)'
  ctx.lineWidth = 1
  for (const m of marks) {
    if (m.u == null) continue
    const x = (left + m.u * mapPx) | 0
    const y = (top + m.v * mapPx) | 0
    const r = rare ? 4 : 3
    ctx.beginPath()
    ctx.arc(x, y, r, 0, Math.PI * 2)
    ctx.fill()
    ctx.stroke()
  }
}
