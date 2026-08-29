import { useState, useCallback, useContext } from 'react'
import { AccountContext } from '../../context'
import { useMapEngine, MapViz } from './useMapEngine.jsx'
import LayerPanel from './LayerPanel'

// 实时地图页:外壳 = 图层抽屉 + MapViz(地图本体,引擎在 useMapEngine)。
// 图层栏所有宽度统一为侧滑抽屉(对齐手机端):右上角 ☰ / 遮罩 / ✕ 控制开合。
export default function MapPage() {
  const account = useContext(AccountContext)
  const engine = useMapEngine(account)
  const [collapsed, setCollapsed] = useState(true) // 图层抽屉(开合),默认收起=地图全屏

  const toggleLayers = () => setCollapsed((c) => !c)

  // 收集度面板点某分区:把地图移到该区点位中心,并收起抽屉露出地图。
  // 直接改 focusRef 而非走 state——视口中心是每帧消费的 ref(跟随模式下每帧跟着玩家变),
  // 进 state 会让每次平移都重渲染整页;pokeFrame 唤醒可能已停的 RAF 画这一帧
  // (玩家静止时 RAF 会停,不唤醒就看不到移动)。
  const focusZone = useCallback((z) => {
    if (z.u == null) return
    engine.view.setFollow(false)
    engine.focusRef.current = { u: z.u, v: z.v }
    engine.pokeFrame()
    setCollapsed(true)
  }, [engine])

  return (
    <div className="map-page">
      <div className="map-layout">
        <LayerPanel pois={engine.pois} wilds={engine.wilds} paint={engine.paint} routes={engine.routes}
          collapsed={collapsed} onClose={() => setCollapsed(true)} onFocusZone={engine.hasMap ? focusZone : null} />

        <MapViz engine={engine}
          layersActive={!collapsed}
          onToggleLayers={toggleLayers} />
      </div>
    </div>
  )
}
