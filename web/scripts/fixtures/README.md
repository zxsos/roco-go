# 运动平滑回归 fixture 的来源与重制方法

`motion-packets.json` 是**真实抓包**的派生数据，不是手搓的。它存在的唯一目的：让
`verify-motion.mjs` 能在没有 pcap、没有后端的环境下复现"箭头平不平"的度量。

## 内容

137 个 `ZONE_SCENE_MOVE_REQ`(0x0133) 移动包，按 **pipeline 推给前端的形状**导出：

| 字段 | 含义 |
| --- | --- |
| `side` | 场景边长（厘米），卡洛西亚大陆 `scene_res_cfg_id=10003` 为 408000 |
| `pkts[].t` | 包时刻（秒，相对首包） |
| `u` / `v` | `to_pos` 经 `gamedata.Project` 投影的归一化底图坐标 |
| `vu` / `vv` | `speed`（UE 厘米/秒）按同一投影换算的「归一化坐标/秒」；stop 包为 0 |
| `stop` | `stop_move`；**仅在为 true 时出现**（与 `PositionPayload` 的 omitempty 语义一致） |
| `path` | `move_seg_list` 补报的真实轨迹，仅当 `SegSpan() >= 0.6` 时才带（同 `buildPos` 的 `minSegSpan` 判据） |

`path[].t` 是各轨迹点的**服务器时间戳**（秒）——后端目前不下发它（前端按弧长回放），
但它是求"真值"的关键，故由导出脚本补上。若哪天后端开始下发，度量会更准。

## 重制方法

1. 抓一份含多种移动方式的 pcap（地面 / 骑乘 / 飞行、直线 / 转弯、频繁启停）。
2. 用 `scene.ParseMoveReq` 解析，按 `pipeline.buildPos` 的同款逻辑投影成上表形状，
   JSON 落盘（`t` 保留 3 位小数、`u/v` 保留 6 位，1px 底图 ≈ 1.02m，精度足够）。

导出逻辑约 60 行 Go，此前是临时工具 `cmd/_tmp_movean`（已删，未随提交保留）。
重制时照 `buildPos` 重写即可，判据必须与后端一致，否则度量的是"假数据"。

## 更换 fixture 的注意事项

- **门槛要跟着重算**。数字依赖具体抓包的移动特征（本份是"点触式走走停停"：
  45/137 是 stop 包，骑乘峰值 29m/s）。换一份巡航为主的包，绝对值会变。
- 别为了让测试变绿而下调门槛而不写理由 —— 见 `verify-motion.mjs` 文件头。
