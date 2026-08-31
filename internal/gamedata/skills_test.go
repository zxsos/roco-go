package gamedata

import "testing"

// 本文件验证天生技能表(scripts/gen_skills.py 产出)的加载与访问。
// 数据来自第三方资料站,取值随资料更新而变,故只断言**结构**与确定的行为,
// 不断言具体技能名(那会让资料一更新测试就红)。

func TestInnateSkillsLoaded(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if db.skills == nil {
		t.Fatal("skills 未加载")
	}
	if len(db.skills.pets) == 0 {
		t.Fatal("技能表为空:data/skills.json 没解析出形态")
	}
	// 效果池必须非空,否则所有技能的 effect 都会是空串
	if len(db.skills.effects) == 0 {
		t.Error("effects 池为空")
	}
	t.Logf("形态数 %d, 效果池 %d", len(db.skills.pets), len(db.skills.effects))
}

// TestInnateSkillsSortedDesc 断言按学会等级降序(生成脚本排好,这里再排一次防御)。
func TestInnateSkillsSortedDesc(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	checked := 0
	for id := range db.skills.pets {
		sk := db.InnateSkills(id)
		if len(sk) == 0 {
			t.Errorf("形态 %d 有紧凑条目却展开为空", id)
			continue
		}
		for i := 1; i < len(sk); i++ {
			if sk[i-1].Level < sk[i].Level {
				t.Errorf("形态 %d 未按等级降序: %v", id, sk)
				break
			}
		}
		// 每条都应有名字(名字是这张表的全部意义)
		for _, s := range sk {
			if s.Name == "" {
				t.Errorf("形态 %d 有技能缺名字: %+v", id, s)
			}
		}
		checked++
		if checked >= 50 { // 全量 347 个形态逐个断言太慢,抽样足够
			break
		}
	}
}

// TestSkillName 断言 skill_id → 中文名的映射。
//
// 取值来自第三方资料站,故只断言确定的几条(用协议实测过的 id),覆盖三类情况:
// 普通技能、同名变体、试炼专属 id。
func TestSkillName(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(db.skills.names) == 0 {
		t.Fatal("names 映射为空:data/skills.json 没解析出 skill_id")
	}
	// 普通技能:试炼里实测出现过的 id
	for id, want := range map[uint32]string{
		7020500: "乱打", // 黑猫巫师初始技能(融合前威力25/能耗4)
		7020550: "魔能爆",
		7170210: "午夜噪音",
		7120100: "毒液渗透",
		7020840: "借用", // 变化类(威力0),按数值反查不了,只能按 id 查
		7140190: "听桥",
		7120300: "毒肽",
	} {
		if got := db.SkillName(id); got != want {
			t.Errorf("SkillName(%d) = %q, 期望 %q", id, got, want)
		}
	}
	// 试炼专属 id:魔能爆在试炼里被换成 7880058(资料站 788 段整段缺失,
	// 靠抓包实证逐个登记,见 gen_skills.py 的 EXTRA_SKILL_IDS)
	if got := db.SkillName(7880058); got != "魔能爆" {
		t.Errorf("试炼态 7880058 = %q, 期望 魔能爆", got)
	}
	// 不存在的 id
	if got := db.SkillName(1); got != "" {
		t.Errorf("不存在的 id 应返回空串, 得到 %q", got)
	}
}

// TestSkillNameFusionKeepsID 守护一条反直觉的事实:**融合不改变 base_skill_id**。
//
// 早先误以为「融合会生成新 skill_id、查不到名是正常的」,据此放过了一个本可查到的
// 名字(7880058)。实测它开局零融合时就存在,融合 2 次(威力 20 → 150、
// fusion_count 0 → 2)后 id 始终不变 —— 故融合态技能同样该查到名。
// 本测试防止该误解回归。
func TestSkillNameFusionKeepsID(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 试炼实测:这三个 id 无论融合几次都不变(只增长威力与 fusion_count)
	for _, id := range []uint32{7020500, 7880058, 7170210} {
		if got := db.SkillName(id); got == "" {
			t.Errorf("融合态技能 %d 应能查到名(融合不改 base_skill_id), 得到空串", id)
		}
	}
}

// TestInnateSkillsHaveSkillID 断言天生技能带上了 skill_id(重名技能除外)。
func TestInnateSkillsHaveSkillID(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	withID, withoutID := 0, 0
	for id := range db.skills.pets {
		for _, s := range db.InnateSkills(id) {
			if s.SkillID != 0 {
				withID++
			} else {
				withoutID++ // 重名技能(借用/取念/复写/愿力冲击/疾风涡轮)填 0
			}
		}
	}
	if withID == 0 {
		t.Fatal("没有任何天生技能带上 skill_id —— 合并回填没生效")
	}
	// 名字能查到 id 的应占绝大多数(资料站 492 个技能里 488 个唯一命中)
	if r := float64(withID) / float64(withID+withoutID); r < 0.9 {
		t.Errorf("带 skill_id 的比例过低: %.1f%% (%d/%d)", r*100, withID, withID+withoutID)
	}
	t.Logf("带 skill_id %d 条, 重名无 id %d 条", withID, withoutID)
}

// TestInnateSkillsUnknown 断言未知形态返回 nil 且不 panic。
func TestInnateSkillsUnknown(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := db.InnateSkills(99999999); got != nil {
		t.Errorf("未知形态应返回 nil, 得到 %v", got)
	}
	if db.HasInnateSkills(99999999) {
		t.Error("未知形态 HasInnateSkills 应为 false")
	}
}

// TestInnateSkillsCached 断言二次调用命中缓存、结果一致(懒解析不能每次都新建)。
func TestInnateSkillsCached(t *testing.T) {
	db, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for id := range db.skills.pets {
		a := db.InnateSkills(id)
		b := db.InnateSkills(id)
		if len(a) != len(b) {
			t.Fatalf("形态 %d 两次调用长度不一: %d vs %d", id, len(a), len(b))
		}
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("形态 %d 第 %d 条两次不一致: %+v vs %+v", id, i, a[i], b[i])
			}
		}
		break // 只需验证一个
	}
}
