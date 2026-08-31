package gamedata

import (
	_ "embed" // go:embed 需要(即使本文件不直接用 embed.FS)
	"encoding/json"
	"sort"
	"strconv"
)

// 精灵的「天生技能」表(由 scripts/gen_skills.py 生成)。
//
// 数据来源与 names.json 不同:names.json 全部出自游戏解包 Bin 配置,而这份来自第三方
// 资料站(arkmeng.cn 的技能数据),是**过渡方案** —— 解包出 SKILL_CONF 后应改走解包数据。
// 独立成文件(而非并入 names.json)正是为了让「这份数据从哪来」说得清。
//
// 组织方式:按 petbase 形态 id 索引,值为该形态天生会的技能(含学会等级)。
// 注意这是**可换的配置**,不是某只宠物当前携带的技能 —— 后者见 git 0762eb6 移除
// Pet.SkillIDs 的理由(技能可从技能石等途径更换,且体积开销大)。
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
	Name   string `json:"name"`
	Level  uint32 `json:"level"`
	Elem   string `json:"elem"`
	Power  string `json:"power"`
	Cost   string `json:"cost"`
	Effect string `json:"effect"`
}

// innateSkillRaw 是 JSON 里的紧凑数组形式:[名, 等级, 属性, 威力, 能耗, 效果下标]。
// 用数组而非对象是为压体积(5213 条省下重复键名,见 gen_skills.py 的说明)。
type innateSkillRaw [6]any

// skillsDB 是解析后的技能表。
type skillsDB struct {
	effects []string                    // 共享效果描述池
	pets    map[uint32][]innateSkillRaw // petbase_id -> 紧凑条目
	byID    map[uint32][]InnateSkill    // petbase_id -> 展开后的技能(懒解析)
}

// loadSkills 解析内嵌的技能表;文件缺失或格式不符时返回空表(不影响其余功能)。
func loadSkills() *skillsDB {
	db := &skillsDB{pets: map[uint32][]innateSkillRaw{}}
	var raw struct {
		Effects []string                    `json:"effects"`
		Pets    map[string][]innateSkillRaw `json:"pets"`
	}
	if err := json.Unmarshal(skillsJSON, &raw); err != nil {
		return db
	}
	db.effects = raw.Effects
	for k, v := range raw.Pets {
		if id, err := strconv.ParseUint(k, 10, 32); err == nil {
			db.pets[uint32(id)] = v
		}
	}
	return db
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

// 保证 embed 变量被引用(避免只用于解析时被误判未使用)。
var _ = skillsJSON
