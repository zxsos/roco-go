package pipeline

import (
	"sort"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/scene"
)

// flowerItem 是一只花种(花灵)BOSS 的展示信息:花种页卡片按此渲染。
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
}

// onBossNpcInfo 处理 s2c 0x0375 花种 BOSS 分组:只把 flower_npcs(花灵,普通+特殊)渲染到花种页。
// world_leader_npcs(世界 BOSS)与 legendary_npcs(传说 NPC)与花种无关,解析时不取。
func (p *Pipeline) onBossNpcInfo(m capture.Message, acc string) {
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
