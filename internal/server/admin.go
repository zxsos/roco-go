// 管理员面板:首启引导设置密码 → 登录签发令牌 → 查看各玩家使用情况。
// 密码哈希用 PBKDF2-HMAC-SHA256(纯标准库实现,不为一次性登录引入外部依赖);
// 登录令牌只在服务端内存,服务重启后需重新登录(局域网工具够用)。
package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/whoisnian/rocom-capture/internal/store"
)

const (
	pbkdf2Iter   = 100_000 // 迭代次数:弱机登录也仅 ~50ms,足够拖慢暴力破解
	pbkdf2KeyLen = 32
)

// pbkdf2SHA256 实现 RFC 2898 的 PBKDF2(PRF=HMAC-SHA256)。
func pbkdf2SHA256(password, salt []byte, iterations, dkLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (dkLen + hashLen - 1) / hashLen
	var dk []byte
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		var b [4]byte
		b[0] = byte(block >> 24)
		b[1] = byte(block >> 16)
		b[2] = byte(block >> 8)
		b[3] = byte(block)
		prf.Write(b[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:dkLen]
}

// hashPassword 生成 "盐hex$哈希hex" 格式的密码哈希(随机 16 字节盐)。
func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2SHA256([]byte(pw), salt, pbkdf2Iter, pbkdf2KeyLen)
	return hex.EncodeToString(salt) + "$" + hex.EncodeToString(dk), nil
}

// verifyPassword 恒定时间比较校验密码。
func verifyPassword(pw, stored string) bool {
	i := strings.IndexByte(stored, '$')
	if i < 0 {
		return false
	}
	salt, err := hex.DecodeString(stored[:i])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(stored[i+1:])
	if err != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(pw), salt, pbkdf2Iter, pbkdf2KeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// newToken 生成登录令牌(随机 24 字节,URL 安全 base64)。
func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// setAdminToken 写入/清空管理员登录令牌(单令牌:任一浏览器登录后共用同一会话)。
func (s *Server) setAdminToken(t string) {
	s.adminMu.Lock()
	s.adminToken = t
	s.adminMu.Unlock()
}

// adminOK 校验请求携带的管理员令牌(Authorization: Bearer <token>)。
func (s *Server) adminOK(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.adminMu.Lock()
	cur := s.adminToken
	s.adminMu.Unlock()
	return cur != "" && subtle.ConstantTimeCompare([]byte(token), []byte(cur)) == 1
}

// handleAdminStatus 返回管理员密码是否已配置(首启引导设置)。
func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	configured, err := s.store.AdminConfigured()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]bool{"configured": configured})
}

// handleAdminSetup 首次设置管理员密码(仅未配置时可用),成功即签发令牌进入面板。
func (s *Server) handleAdminSetup(w http.ResponseWriter, r *http.Request) {
	if configured, _ := s.store.AdminConfigured(); configured {
		http.Error(w, "管理员密码已设置,请直接登录", http.StatusBadRequest)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Password) < 6 {
		http.Error(w, "密码至少 6 位", http.StatusBadRequest)
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.store.SetAdminPassword(hash); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	token, err := newToken()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.setAdminToken(token)
	writeJSON(w, map[string]string{"token": token})
}

// handleAdminLogin 校验密码并签发令牌。
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	stored := s.store.AdminPassHash()
	if stored == "" || !verifyPassword(req.Password, stored) {
		http.Error(w, "密码错误", http.StatusUnauthorized)
		return
	}
	token, err := newToken()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.setAdminToken(token)
	writeJSON(w, map[string]string{"token": token})
}

// handleAdminLogout 使当前令牌失效(前端「退出登录」用)。
func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.setAdminToken("")
	writeJSON(w, map[string]bool{"ok": true})
}

// handleAdminUsers 返回各玩家使用情况(需管理员令牌)。
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	users, err := s.store.ListAccountUsage()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if users == nil {
		users = []store.AccountUsage{}
	}
	for i := range users {
		users[i].Online = s.AccountOnline(users[i].Account)
	}
	writeJSON(w, users)
}

// handleDeleteAccount 删除某账号的「登录记录」(仅表面信息):
// accounts 表的一行被删,该账号从切换下拉消失;宠物/事件等抓包数据保留,
// 玩家下次登录抓包时自动重新登记出现。同时清掉内存里的在线与实时位置现场。
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	acc := r.PathValue("account")
	if acc == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.onlineMu.Lock()
	delete(s.lastSeen, acc)
	s.onlineMu.Unlock()
	s.posMu.Lock()
	delete(s.lastPos, acc)
	delete(s.lastWild, acc)
	delete(s.lastHome, acc)
	s.posMu.Unlock()
	if err := s.store.DeleteAccount(acc); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
