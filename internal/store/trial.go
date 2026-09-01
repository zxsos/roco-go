package store

import "time"

// 草系徽章试炼的遇见记录(试炼页「遇见记录」三张图,见 internal/trial/battle.go)。
//
// 存的是「某账号在第 N 章遇到过某只精灵」,每章独立 —— 与 wiki 的口径一致
// (页面注明「3 章首领按章节独立计算」):同一只精灵在第 1 章遇到过,第 2 章
// 的图里仍算未遇见。这样三张图的进度才是各自真实的探索度。
//
// 只入库**试炼内**的战斗:解析时以「带 grass_trial_battle_info」为准,
// 野外与 PVP 战斗不会进来(见 trial.ParseBattleEnter)。

// TrialEncounter 是一条遇见记录。
type TrialEncounter struct {
	PetBase   uint32 `json:"petbase"`
	Chapter   uint32 `json:"chapter"` // 1/2/3
	Kind      uint32 `json:"kind"`    // 见 trial.BattleType
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
	Times     uint32 `json:"times"`
}

// AddTrialEncounters 记录一次战斗里遇到的精灵。chapter 为 0 时跳过 ——
// 不知道是哪一章就没法归到某张图上,含糊入库比不记更糟。
func (s *Store) AddTrialEncounters(account string, chapter uint32, kind uint32, petBases []uint32) error {
	if len(petBases) == 0 || chapter == 0 {
		return nil
	}
	now := time.Now().Unix()
	rows := make([][]any, 0, len(petBases))
	for _, p := range petBases {
		if p == 0 {
			continue
		}
		rows = append(rows, []any{account, p, chapter, kind, now, now, 1})
	}
	if len(rows) == 0 {
		return nil
	}
	return execBatch(s.db, `INSERT INTO trial_encounter
		(account, petbase, chapter, kind, first_seen, last_seen, times) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(account, petbase, chapter) DO UPDATE SET
			last_seen=excluded.last_seen,
			times=times+1,
			kind=CASE WHEN excluded.kind > kind THEN excluded.kind ELSE kind END`, rows)
}

// TrialEncounters 返回某账号在某章(0=全部章)的遇见记录,按 petbase 索引。
func (s *Store) TrialEncounters(account string, chapter uint32) map[uint32]TrialEncounter {
	out := map[uint32]TrialEncounter{}
	var rows, err = s.rdb.Query(
		`SELECT petbase, chapter, kind, first_seen, last_seen, times
		 FROM trial_encounter WHERE account=? AND (?=0 OR chapter=?)`,
		account, chapter, chapter)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e TrialEncounter
		if rows.Scan(&e.PetBase, &e.Chapter, &e.Kind, &e.FirstSeen, &e.LastSeen, &e.Times) == nil {
			out[e.PetBase] = e
		}
	}
	return out
}

// ClearTrialEncounters 清空某账号(或仅某一章)的遇见记录。
// chapter 传 0 表示全清 —— 试炼模式随版本重置精灵池时,旧记录就没意义了。
func (s *Store) ClearTrialEncounters(account string, chapter uint32) error {
	_, err := s.db.Exec(
		`DELETE FROM trial_encounter WHERE account=? AND (?=0 OR chapter=?)`,
		account, chapter, chapter)
	return err
}
