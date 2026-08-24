package pipeline

import (
	"sort"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/scene"
)

// flowerItem 是一只花种(花灵)BOSS 的展示信息:花种页卡片按此渲染。
// 面板 0x0375 整组下发基础字段;玩家点击地图花种后的 0x0338 详情(等级/炫彩/绑定宠物/奖牌)合并进来。
type flowerItem struct {
	ID          uint32 `json:"id"`          // 花种 NPC 配置 id
	Name        string `json:"name"`        // 守护宠物名(petbase,未知时为空)
	Img         string `json:"img"`         // 守护宠物头像 /img/<此路径>(未知时为空,前端回退)
	Star        uint32 `json:"star"`        // 星级(普通花灵 5,特殊花灵 7)
	Blood       uint32 `json:"blood"`       // 血量序号(游戏内按此区分同种花种)
	EndTs       uint64 `json:"endTs"`       // 活动结束 Unix 秒(0=未设置)
	SpecSeedID  uint32 `json:"specSeedId"`  // 特殊花种种子 id(0=普通花种)
	ActivityID  uint32 `json:"activityId"`  // 活动 id
	OwnerUserID uint64 `json:"ownerUserId"` // 已选花种的玩家 user_id(0=无人选择)
	Detail     bool   `json:"detail"`       // 是否已收到 0x0338 详情(玩家点过地图花种;未点过=false)
	// 以下为 0x0338 详情(点击地图花种后更新;0/空=尚未获取,普通花种绑定/奖牌恒为 0/空):
	Lv        uint32 `json:"lv"`        // 等级
	GlassType int32  `json:"glassType"` // 炫彩类型(0=无炫彩 / 1=普通 / 2=隐藏;仅在 detail=true 时有效)
	Glass     string `json:"glass"`     // 炫彩中文描述(GlassDesc;空=无炫彩或未获取)
	BindName  string `json:"bindName"`  // 绑定守护宠物名(空=无绑定)
	BindImg   string `json:"bindImg"`   // 绑定守护宠物头像 /img/<此路径>
	BindEvo   uint32 `json:"bindEvo"`   // 绑定宠物进化阶段 id(0=无)
	MedalName string `json:"medalName"` // 绑定宠物佩戴的奖牌名(空=无)
	MedalIcon string `json:"medalIcon"` // 奖牌小图 /img/<此路径>
}

// flowerKey 定位一只花种:同种花种可同时存在多只,游戏内按血量序号区分。
type flowerKey struct {
	id    uint32
	blood uint32
}

// onBossNpcInfo 处理 s2c 0x0375 花种 BOSS 分组:只把 flower_npcs(花灵,普通+特殊)渲染到花种页。
// world_leader_npcs(世界 BOSS)与 legendary_npcs(传说 NPC)与花种无关,解析时不取。
// 游戏内每次打开面板都会整组重发:基础字段以新下发为准,但已点过的 0x0338 详情
// 按 (id,blood) 从旧分组里保留,避免整组刷新把查看状态冲掉。
func (p *Pipeline) onBossNpcInfo(m capture.Message, acc string) {
	// 先读旧分组,收集已点过(有 0x0338 详情)的项。
	prev := make(map[flowerKey]flowerItem)
	if raw := p.srv.GetLastFlowers(acc); raw != nil {
		if payload, ok := raw.(map[string]any); ok {
			if items, ok := payload["flowers"].([]flowerItem); ok {
				for _, it := range items {
					if it.Detail {
						prev[flowerKey{it.ID, it.Blood}] = it
					}
				}
			}
		}
	}
	items := make([]flowerItem, 0, 23)
	for _, b := range scene.ParseBossNpcInfoRsp(m.AppBody) {
		it := flowerItem{
			ID:          b.NpcCfgID,
			Star:        b.Star,
			Blood:       b.Blood,
			EndTs:       b.EndTs,
			SpecSeedID:  b.SpecSeedID,
			ActivityID:  b.ActivityID,
			OwnerUserID: b.OwnerUserID,
		}
		if base, ok := p.db.PetBase(b.PetBaseID); ok {
			it.Name = base.Name
		}
		it.Img = p.db.PetImageByBase(b.PetBaseID, false).Head
		// 合并旧详情:面板整组重发不丢已点过的 0x0338 查看状态。
		if old, ok := prev[flowerKey{it.ID, it.Blood}]; ok {
			it.Detail = old.Detail
			it.Lv = old.Lv
			it.GlassType = old.GlassType
			it.Glass = old.Glass
			it.BindName = old.BindName
			it.BindImg = old.BindImg
			it.BindEvo = old.BindEvo
			it.MedalName = old.MedalName
			it.MedalIcon = old.MedalIcon
		}
		items = append(items, it)
	}
	// 顺序稳定:特殊花种(7 星)在前,普通花种按血量序号升序。
	sort.Slice(items, func(i, j int) bool {
		if items[i].Star != items[j].Star {
			return items[i].Star > items[j].Star
		}
		return items[i].Blood < items[j].Blood
	})
	payload := map[string]any{"account": acc, "flowers": items}
	p.srv.SetLastFlowers(acc, payload)
	p.srv.Hub().Broadcast("flowers", acc, payload)
}

// onTeamBattleInfo 处理 s2c 0x0338(点击地图花种的详情回包):
// 按 npc_cfg_id+blood 匹配到已有卡片,合并等级/炫彩/绑定宠物/奖牌等详情后重新广播。
// 面板 0x0375 整组重发时 onBossNpcInfo 会从旧分组保留详情,玩家无需再次点击。
func (p *Pipeline) onTeamBattleInfo(m capture.Message, acc string) {
	d, ok := scene.ParseTeamBattleInfoQueryRsp(m.AppBody)
	if !ok {
		return
	}
	raw := p.srv.GetLastFlowers(acc)
	if raw == nil {
		return // 还没收到过面板分组,无从合并
	}
	payload, ok := raw.(map[string]any)
	if !ok {
		return
	}
	items, ok := payload["flowers"].([]flowerItem)
	if !ok {
		return
	}
	// 复制一份再改,避免直接动 server 缓存里的共享切片。
	items = append([]flowerItem(nil), items...)
	updated := false
	for i := range items {
		f := &items[i]
		if f.ID != d.NpcCfgID || f.Blood != d.Blood {
			continue
		}
		updated = true
		f.Detail = true
		f.Lv = d.Lv
		f.GlassType = d.GlassType
		if d.EndTs != 0 {
			f.EndTs = d.EndTs
		}
		if d.SpecSeedID != 0 {
			f.SpecSeedID = d.SpecSeedID
		}
		if d.ActivityID != 0 {
			f.ActivityID = d.ActivityID
		}
		if d.OwnerUserID != 0 {
			f.OwnerUserID = d.OwnerUserID
		}
		f.Glass = p.db.GlassDesc(d.GlassType, d.GlassValue)
		if d.BindBaseID != 0 {
			if base, ok := p.db.PetBase(d.BindBaseID); ok {
				f.BindName = base.Name
			}
			f.BindImg = p.db.PetImageByBase(d.BindBaseID, false).Head
			f.BindEvo = d.BindEvoID
		}
		if d.MedalID != 0 {
			if md, ok := p.db.Medal(d.MedalID); ok {
				f.MedalName = md.Name
			}
			f.MedalIcon = p.db.MedalIcon(d.MedalID)
		}
		break
	}
	if !updated {
		return
	}
	payload = map[string]any{"account": acc, "flowers": items}
	p.srv.SetLastFlowers(acc, payload)
	p.srv.Hub().Broadcast("flowers", acc, payload)
}
