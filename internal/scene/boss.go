package scene

import (
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/whoisnian/rocom-capture/internal/wire"
)

// 花种(花灵)活动 BOSS 信息(见 docs/data.md 花种页)。
//
// 花种界面依赖 s2c 0x0375(883) ZONE_SCENE_QUERY_BOSS_NPC_INFO_RSP:客户端打开花种面板时
// 服务器一次性下发全部 BOSS 分组,其中 flower_npcs 是花种(花灵)活动 BOSS——每颗花种对应一只
// 守护宠物(battle_petbase_id),带星级/血量/活动结束时间;另有 spec_flower_seed_id 的非零的
// 3 只 7 星特殊花种。world_leader_npcs(世界 BOSS)与 legendary_npcs(传说 NPC)与花种无关,不解析。
const (
	OpQueryBossNpcInfoRsp = 0x0375 // ZONE_SCENE_QUERY_BOSS_NPC_INFO_RSP(883), s2c,花种等 BOSS 分组信息
)

// BossNpcInfo 是一只花种(BOSS)的展示字段。字段号据 all.pb 描述符(见 scripts/pbdesc.py):
// BossNpcInfos.boss_npcs(1,repeated BossNpcInfo) 内:
//
//	npc_cfg_id(1) / star(2) / blood(3) / battle_petbase_id(5) / end_timestamp(10) /
//	spec_flower_seed_id(11,非零=特殊花种) / activity_id(12) / select_flower_owner_id(24)
type BossNpcInfo struct {
	NpcCfgID    uint32 // 花种 NPC 配置 id(20129-20144 普通 / 700002-700004 特殊)
	Star        uint32 // 星级(普通 5 / 特殊 7)
	Blood       uint32 // 血量序号(普通 1-17;游戏内按此区分花种)
	PetBaseID   uint32 // 守护宠物 petbase id(取名称/头像)
	EndTs       uint64 // 活动结束时间戳(Unix 秒)
	SpecSeedID  uint32 // 特殊花种种子 id(0=普通花种)
	ActivityID  uint32 // 活动 id(仅特殊花种带)
	OwnerUserID uint64 // 已选花种的玩家 user_id(0=未被选)
}

// ParseBossNpcInfoRsp 从 s2c ZoneSceneQueryBossNpcInfoRsp(0x0375)取 flower_npcs(2) 分组:
// flower_npcs(2,BossNpcInfos) → boss_npcs(1,repeated BossNpcInfo)。
// 只取花种所需的字段;world_leader_npcs/legendary_npcs 与花种无关,跳过。
func ParseBossNpcInfoRsp(body []byte) []BossNpcInfo {
	flowers := wire.SubMsg(body, 2) // flower_npcs
	if flowers == nil {
		return nil
	}
	var out []BossNpcInfo
	for _, b := range wire.Subs(flowers, 1) { // boss_npcs
		var n BossNpcInfo
		wire.ScanFields(b, func(num protowire.Number, typ protowire.Type, _ []byte, v uint64) {
			if typ != protowire.VarintType {
				return
			}
			switch num {
			case 1:
				n.NpcCfgID = uint32(v)
			case 2:
				n.Star = uint32(v)
			case 3:
				n.Blood = uint32(v)
			case 5:
				n.PetBaseID = uint32(v)
			case 10:
				n.EndTs = v
			case 11:
				n.SpecSeedID = uint32(v)
			case 12:
				n.ActivityID = uint32(v)
			case 24:
				n.OwnerUserID = v
			}
		})
		if n.NpcCfgID != 0 {
			out = append(out, n)
		}
	}
	return out
}
