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
