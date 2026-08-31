package gamedata

import (
	_ "embed" // go:embed 需要(即使本文件不直接用 embed.FS)
	"encoding/json"
	"sort"
	"strconv"
)

// 技能表(由 scripts/gen_skills.py 生成),含两块互补的数据:
//
//  1. **skill_id → 技能中文名**(names):协议里的 base_skill_id(7020500 这类)。
//     草系试炼等「只给 id」的场景靠它显示中文名。
//  2. **形态 → 天生技能**(pets):某形态天生会什么、几级学会、威力/能耗/效果。
//     宠物详情页用。注意这是**可换的配置**,不是某只宠物当前携带的技能 ——
//     后者见 git 0762eb6 移除 Pet.SkillIDs 的理由(技能可经技能石等途径更换)。
//
// 数据来源与 names.json 不同:names.json 全部出自游戏解包 Bin 配置,而这份来自两个
// 第三方资料站(aismile.dev + arkmeng.cn),是**过渡方案** —— 解包出 SKILL_CONF
// (id→中文名,与协议同源)后应改走解包数据。独立成文件正是为了让「这份数据从哪来」说得清。
//
//go:embed data/skills.json
var skillsJSON []byte

// InnateSkill 是一个天生技能(某形态天生会、在某等级学会)。
//
// json tag 是**必须的**:本结构直接进 /api/pets/{gid} 的响应(见 server 的 petDetailView),
// 而全站 API 一律 camelCase;不加 tag 会输出 Name/Cost 这类大写键,与其余字段
// (weightKg/glassType/…)不一致,前端取值也容易写错。
// Power/Cost 用字符串而非数值:无威力的变化类技能是 "—"(如「防御」),整数表达不了。
type InnateSkill struct {
	Name    string `json:"name"`
	Level   uint32 `json:"level"`
	Elem    string `json:"elem"`
	Power   string `json:"power"`
	Cost    string `json:"cost"`
	Effect  string `json:"effect"`
	SkillID uint32 `json:"skillId,omitempty"` // 协议里的 base_skill_id;重名技能为 0(见 gen_skills.py)
}

// innateSkillRaw 是 JSON 里的紧凑数组形式:
// [名, 等级, 属性, 威力, 能耗, 效果下标, skill_id]。
// 用数组而非对象是为压体积(5213 条省下重复键名,见 gen_skills.py 的说明)。
type innateSkillRaw [7]any

// skillsDB 是解析后的技能表。
type skillsDB struct {
	effects []string                    // 共享效果描述池
	names   map[uint32]string           // skill_id -> 技能名
	pets    map[uint32][]innateSkillRaw // petbase_id -> 紧凑条目
	byID    map[uint32][]InnateSkill    // petbase_id -> 展开后的技能(懒解析)
}

// loadSkills 解析内嵌的技能表;文件缺失或格式不符时返回空表(不影响其余功能)。
func loadSkills() *skillsDB {
	db := &skillsDB{
		names: map[uint32]string{},
		pets:  map[uint32][]innateSkillRaw{},
	}
	var raw struct {
		Effects []string                    `json:"effects"`
		Names   map[string]string           `json:"names"`
		Pets    map[string][]innateSkillRaw `json:"pets"`
	}
	if err := json.Unmarshal(skillsJSON, &raw); err != nil {
		return db
	}
	db.effects = raw.Effects
	for k, v := range raw.Names {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			db.names[uint32(id)] = v
		}
	}
	for k, v := range raw.Pets {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			db.pets[uint32(id)] = v
		}
	}
	return db
}

// SkillName 把协议里的技能 id(base_skill_id,如 7020500)翻成中文名;
// 查不到返回空串(资料站只覆盖 605 条,未收录的 id 仍可能出现在协议里)。
//
// 关于「试炼专属 id」:试炼会给部分技能换一个 id(如魔能爆的 7020550 在试炼里
// 变成 7880058),这些 id 资料站没有,已在 gen_skills.py 里按抓包实证登记
// (EXTRA_SKILL_IDS)。再遇到查不到的 id,通常是资料站尚未收录的新技能,
// 调用方回退显示原始 id 即可,不是错误。
//
// 注意**融合不会改变 base_skill_id**:融合只改 fused_power 与 fusion_count
// (实测 7880058 融合 2 次:威力 20 -> 150, id 不变)。故不需要为融合态
// 单独维护映射 —— 这点早先判断错了,特此留注避免再犯。
func (db *DB) SkillName(skillID uint32) string {
	if db.skills == nil {
		return ""
	}
	return db.skills.names[skillID]
}

// InnateSkills 返回某形态天生会的技能,按学会等级降序(同级按名);
// 无记录返回 nil —— 第三方资料只覆盖部分形态,缺数据不是错误。
func (db *DB) InnateSkills(petbaseID uint32) []InnateSkill {
	sdb := db.skills
	if sdb == nil {
		return nil
	}
	if got, ok := sdb.byID[petbaseID]; ok {
		return got
	}
	raws, ok := sdb.pets[petbaseID]
	if !ok {
		return nil
	}
	out := make([]InnateSkill, 0, len(raws))
	for _, r := range raws {
		s := InnateSkill{}
		if v, ok := r[0].(string); ok {
			s.Name = v
		}
		if v, ok := r[1].(float64); ok {
			s.Level = uint32(v)
		}
		if v, ok := r[2].(string); ok {
			s.Elem = v
		}
		if v, ok := r[3].(string); ok {
			s.Power = v
		}
		if v, ok := r[4].(string); ok {
			s.Cost = v
		}
		if v, ok := r[5].(float64); ok {
			if i := int(v); i >= 0 && i < len(sdb.effects) {
				s.Effect = sdb.effects[i]
			}
		}
		if v, ok := r[6].(float64); ok {
			s.SkillID = uint32(v)
		}
		out = append(out, s)
	}
	// 生成脚本已排好序,这里再排一次只为防御(展开顺序与紧凑条目一致,行为可预期)。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level
		}
		return out[i].Name < out[j].Name
	})
	if sdb.byID == nil {
		sdb.byID = map[uint32][]InnateSkill{}
	}
	sdb.byID[petbaseID] = out
	return out
}

// HasInnateSkills 判断某形态是否有天生技能数据(前端据此决定要不要显示技能区块)。
func (db *DB) HasInnateSkills(petbaseID uint32) bool {
	return db.skills != nil && len(db.skills.pets[petbaseID]) > 0
}
