package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

// 远行商人:第三方 API(https://apii.xianyuw.cn/api/v1/rocom-merchant)的本地缓存代理。
//
// 业务模型(按游戏活动节奏):
//   - 每天 8:00 开张、12:00 收摊,12:00 及之后到次日 8:00 前没有在售(页面显示当天/昨天回顾);
//   - 查询槽按 4h 对齐 8 点:8/12/16/20/0/4 六个槽,其中只有 8 点(营业首轮)与 12 点
//     (收摊前最后一轮,可能补货)回源第三方,16 点起一律视为休市不再查询;
//   - 结果按槽缓存进 SQLite(store 的 merchant_slots),命中缓存不再回源,防止反复烧第三方 token;
//     缓存保留 2 天,写入时顺手清理更早记录;
//   - 触发回源两条路径:merchantLoop 每 15 分钟检查当前槽(覆盖「早上 8 点自动查第一次」),
//     以及玩家打开页面时 handleMerchant 按当前时间补查缺失的槽。
const (
	merchantFetchURL  = "https://apii.xianyuw.cn/api/v1/rocom-merchant"
	merchantOpenHour  = 8               // 每天 8 点开张
	merchantCloseHour = 12              // 12 点收摊,之后到次日无货
	merchantSlotStep  = 4 * time.Hour   // 查询槽跨度
	merchantCheck     = 15 * time.Minute // 定时器检查间隔
)

// merchantSlotJSON 单个 4h 槽:empty 表示该槽查过但无货/休市,merchant 是该槽第三方原始 JSON。
type merchantSlotJSON struct {
	Start    int64           `json:"start"`
	End      int64           `json:"end"`
	Label    string          `json:"label"`
	Empty    bool            `json:"empty"`
	Merchant json.RawMessage `json:"merchant,omitempty"`
}

// merchantRespJSON handleMerchant 的响应:status 供前端选择「营业中 / 全天回顾 / 昨日回顾」。
type merchantRespJSON struct {
	Now    int64              `json:"now"`
	Day    string             `json:"day"` // 营业日(YYYY-MM-DD)
	Status string             `json:"status"`
	Today  []merchantSlotJSON `json:"today"` // 当天 6 个槽(升序,empty 标注休市)
	Prev   []merchantSlotJSON `json:"prev"`  // 仅 status=idle 时填充:昨天的 6 个槽(回顾用)
}

// merchantDayStart 返回 t 所在营业日的 0 点。
func merchantDayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// merchantDaySlots 返回营业日 6 个槽的开始时刻(对齐 8 点:8/12/16/20/0/4)。
func merchantDaySlots(day time.Time) []time.Time {
	return []time.Time{
		day.Add(8 * time.Hour),
		day.Add(12 * time.Hour),
		day.Add(16 * time.Hour),
		day.Add(20 * time.Hour),
		day.Add(24 * time.Hour),
		day.Add(28 * time.Hour), // 次日 4 点
	}
}

// merchantDayStatus 返回当前时刻的营业状态:
// open=营业中(8-12 点),closed=已收摊(12 点后,显示当天全天回顾),idle=未开张(8 点前,显示昨日回顾)。
func merchantDayStatus(now time.Time) string {
	switch h := now.Hour(); {
	case h < merchantOpenHour:
		return "idle"
	case h < merchantCloseHour:
		return "open"
	default:
		return "closed"
	}
}

// merchantLoop 定时补查:每 15 分钟检查一次当前槽,未缓存则回源(8 点开张后自动完成首次查询)。
func (s *Server) merchantLoop() {
	s.merchantEnsure(time.Now())
	t := time.NewTicker(merchantCheck)
	defer t.Stop()
	for range t.C {
		s.merchantEnsure(time.Now())
	}
}

// merchantEnsure 按当前时间补齐缓存:营业中保证 8 点槽;已收摊补 8 点槽,查到货再补 12 点槽;
// 未开张(凌晨)不查(昨天的数据看库,第三方查不到历史轮次)。force=true 时跳过缓存直接回源。
func (s *Server) merchantEnsure(now time.Time, force ...bool) {
	if s.eggAPIKey == "" {
		return
	}
	state := merchantDayStatus(now)
	if state == "idle" {
		return
	}
	slots := merchantDaySlots(merchantDayStart(now))

	s.merchantMu.Lock()
	defer s.merchantMu.Unlock()

	// 先保证 8 点槽(营业首轮):已有缓存(含空标记)则跳过。
	if !force && s.merchantCached(slots[0].Unix()) {
		// 已收摊且 12 点槽还没查过 → 补最后一轮(可能补货)。
		if state == "closed" && !s.merchantCached(slots[1].Unix()) {
			s.merchantFetch(slots[1])
		}
		return
	}
	ok := s.merchantFetch(slots[0])
	// 8 点轮确实有货才值得查 12 点轮;已收摊场景下补查。
	if ok && state == "closed" && !s.merchantCached(slots[1].Unix()) {
		s.merchantFetch(slots[1])
	}
}

// merchantCached 判断某槽是否已有缓存记录(empty 也算,避免反复查空)。
func (s *Server) merchantCached(slot int64) bool {
	_, _, ok := s.store.GetMerchantSlot(slot)
	return ok
}

// merchantFetch 回源第三方并写入槽缓存,顺带清理 2 天前的过期记录。
// 返回是否拿到「第三方正常响应」(有货无货都算,仅网络/HTTP 层失败返回 false,不写库)。
func (s *Server) merchantFetch(slotStart time.Time) bool {
	params := url.Values{}
	params.Add("key", s.eggAPIKey)
	params.Add("format", "json")
	params.Add("refresh", "false")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, merchantFetchURL+"?"+params.Encode(), nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 上限 1MB
	if err != nil || resp.StatusCode != http.StatusOK {
		return false
	}
	// 校验并判定有货/无货:code==0 且 data.items 非空视为有货,其余(无货/业务错误)记空。
	var out struct {
		Code int `json:"code"`
		Data struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false
	}
	empty := out.Code != 0 || len(out.Data.Items) == 0
	if err := s.store.PutMerchantSlot(slotStart.Unix(), empty, string(body)); err != nil {
		return false
	}
	return true
}

// handleMerchant 返回当前营业日的槽缓存与状态,玩家打开页面时按当前时间补查缺失槽。
// 参数:force=1 强制回源当前可查槽(烧第三方 token,前端「强制刷新」用)。
// 响应结构见 merchantRespJSON。
func (s *Server) handleMerchant(w http.ResponseWriter, r *http.Request) {
	if s.eggAPIKey == "" {
		http.Error(w, "服务端未配置查询令牌(启动时加 -egg-api-key)", http.StatusServiceUnavailable)
		return
	}
	now := time.Now()
	s.merchantEnsure(now, r.URL.Query().Get("force") == "1")

	day := merchantDayStart(now)
	out := merchantRespJSON{
		Now:    now.Unix(),
		Day:    day.Format("2006-01-02"),
		Status: merchantDayStatus(now),
		Today:  s.merchantSlotsOfDay(day),
	}
	if out.Status == "idle" {
		out.Prev = s.merchantSlotsOfDay(day.AddDate(0, 0, -1))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(out)
}

// merchantSlotsOfDay 组装某营业日的 6 个槽:8/12 槽读缓存(有货则带 merchant),16 点后的槽休市。
func (s *Server) merchantSlotsOfDay(day time.Time) []merchantSlotJSON {
	starts := merchantDaySlots(day)
	out := make([]merchantSlotJSON, 0, len(starts))
	for i, st := range starts {
		js := merchantSlotJSON{
			Start: st.Unix(),
			End:   st.Add(merchantSlotStep).Unix(),
			Label: st.Format("15:04") + "~" + st.Add(merchantSlotStep).Format("15:04"),
			Empty: true,
		}
		if i < 2 { // 8 点、12 点两个可查槽读缓存
			if empty, data, ok := s.store.GetMerchantSlot(st.Unix()); ok {
				js.Empty = empty
				if !empty {
					js.Merchant = json.RawMessage(data)
				}
			}
		}
		out = append(out, js)
	}
	return out
}
