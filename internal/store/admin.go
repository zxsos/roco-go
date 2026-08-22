package store

import "time"

// AdminConfigured 返回管理员密码是否已设置(首启未设置时,前端引导设置)。
func (s *Store) AdminConfigured() (bool, error) {
	var n int
	err := s.rdb.QueryRow(`SELECT COUNT(*) FROM admin`).Scan(&n)
	return n > 0, err
}

// SetAdminPassword 写入管理员密码哈希(首启设置;id=1 冲突时覆盖)。
func (s *Store) SetAdminPassword(hash string) error {
	_, err := s.db.Exec(`
INSERT INTO admin(id, pass_hash, created_at) VALUES(1, ?, ?)
ON CONFLICT(id) DO UPDATE SET pass_hash=excluded.pass_hash`,
		hash, time.Now().Unix())
	return err
}

// AdminPassHash 返回管理员密码哈希;未设置时返回空串。
func (s *Store) AdminPassHash() string {
	var h string
	_ = s.rdb.QueryRow(`SELECT pass_hash FROM admin WHERE id=1`).Scan(&h)
	return h
}

// DeleteAccount 删除某账号的「登录记录」(accounts 表一行,即表面信息):
// 宠物/事件等抓包数据保留,该账号从切换下拉消失,玩家下次登录抓包时重新出现。
func (s *Store) DeleteAccount(account string) error {
	_, err := s.db.Exec(`DELETE FROM accounts WHERE account=?`, account)
	return err
}

// AccountUsage 是管理员面板的单个玩家使用情况。
// Online 由 server 层判定后合并(见 server.AccountOnline),store 不持久化。
type AccountUsage struct {
	Account      string `json:"account"`
	Name         string `json:"name"`
	PetCount     int    `json:"petCount"`
	EventCount   int    `json:"eventCount"`
	SessionCount int    `json:"sessionCount"`
	UpdatedAt    int64  `json:"updatedAt"` // 最近活跃(unix 秒)
	FirstSeen    int64  `json:"firstSeen"` // 首次出现(最早捕获事件;无事件取登记时间)
	Online       bool   `json:"online"`
}

// ListAccountUsage 返回各账号的使用情况(管理员面板用),按最近活跃倒序。
// 宠物/事件/会话三列各自 DISTINCT 计数,互不影响。
func (s *Store) ListAccountUsage() ([]AccountUsage, error) {
	rows, err := s.rdb.Query(`
SELECT a.account, a.name, a.updated_at,
       COUNT(DISTINCT p.gid),
       COUNT(DISTINCT e.id),
       COUNT(DISTINCT ss.conn_id),
       COALESCE(MIN(e.time), a.updated_at)
FROM accounts a
LEFT JOIN pets p ON p.account = a.account
LEFT JOIN events e ON e.account = a.account
LEFT JOIN sessions ss ON ss.account = a.account
GROUP BY a.account, a.name, a.updated_at
ORDER BY a.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccountUsage
	for rows.Next() {
		var u AccountUsage
		if err := rows.Scan(&u.Account, &u.Name, &u.UpdatedAt, &u.PetCount, &u.EventCount, &u.SessionCount, &u.FirstSeen); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
