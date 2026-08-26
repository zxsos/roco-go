package pipeline

import (
	"time"

	"github.com/whoisnian/rocom-capture/internal/capture"
	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/store"
)

// ---- 精灵蛋(见 docs/data.md 3.6)----
//
// 蛋从四处露面,处理方式各异:
//   - 0x1344 背包分页全量:入库 + 末页对账(不在背包的删掉,与宠物列表同一套路)
//   - 0x0243 奖励通知:新得的蛋;flow_reason=223 即家园小窝下的蛋,顺手记双亲
//   - 0x0262 商店购买:远行商人的「神奇的蛋」等,新蛋只在这条回包里下发(不另发奖励通知)
//   - 0x0164 用道具 / 0x0312 孵化状态:同一颗蛋的进度更新(入孵时刻、已孵秒数)
//   - 0x02ff/0x0300 取出孵蛋器:服务器不清 start_hatch_time(实测只清进度),回包后按
//     动作清零在孵标记(见 stopHatch);背包全量(0x1344/登录)可能把残留标记回灌,靠
//     pet.Egg.PruneTakenOut 时间推断兜底、0x0312 对账作最终权威(见 applyHatchStatus)
//   - 0x030b/0x030c 破壳:请求带 egg_gid,回包一到就把这颗蛋从库里删掉(它已不在背包里)
//
// 库里存的就是**背包现状**:页面只看背包,破壳/送人的蛋没人回看,故不留历史行。
// 双亲只在**收蛋那一刻**能确定:蛋 NPC 趴在母本的窝上,配对由服务器下发(见 home.go)。
// 快照落库后与宠物表脱钩,亲本日后被放生/赠送也不影响这颗蛋上已记录的双亲。

// eggSweep 累积一轮分页背包全量,末页对账(与 petSweep 同构,见 pets.go)。
type eggSweep struct {
	gids     map[uint32]bool
	order    []uint32 // 服务器下发顺序(背包里的原始次序,见 store.SetEggOrder)
	nextPage uint32
	valid    bool
	start    time.Time
}

// handleEgg 分发精灵蛋相关消息;返回是否已消费(消费了也仍会继续走宠物那一路,
// 因为破壳/奖励通知同时带着新宠物)。
func (p *Pipeline) handleEgg(m capture.Message, acc string) {
	sc := p.st.For(acc)
	switch {
	case m.Direction == gcp.C2S && m.Opcode == pet.OpCrackEggReq:
		if gid := pet.ParseCrackEggReq(m.AppBody); gid != 0 {
			p.conn(m.Session).crackEgg = gid // 等回包确认破壳成功再删
		}

	case m.Direction == gcp.S2C && m.Opcode == pet.OpGetBagItemInfoByPageRsp:
		p.applyEggPage(m, sc, acc)

	case m.Direction == gcp.S2C && m.Opcode == pet.OpCrackEggRsp:
		// 破壳成功(回包带孵出的宠物 gid):这颗蛋没了,当场删行,不必等下次开背包对账。
		if egg := p.conn(m.Session).crackEgg; egg != 0 && pet.ParseCrackEggRsp(m.AppBody) != 0 {
			sc.DeleteEgg(egg)
			p.conn(m.Session).crackEgg = 0
			p.srv.Hub().Broadcast("eggs", acc, map[string]any{"account": acc})
		}

	case m.Direction == gcp.S2C && (m.Opcode == pet.OpGoodsRewardNotify ||
		m.Opcode == pet.OpShopBuyItemRsp || m.Opcode == pet.OpUseBagItemRsp):
		eggs := pet.ParseChangedEggs(m.AppBody)
		if len(eggs) == 0 {
			return
		}
		p.upsertEggs(sc, acc, eggs, m.Time)
		if m.Opcode == pet.OpGoodsRewardNotify && pet.ParseFlowReason(m.AppBody) == pet.FlowReasonHomeLay {
			p.recordEggParents(m.Session, sc, eggs, m.Time)
		}

	case m.Direction == gcp.C2S && m.Opcode == pet.OpStopHatchReq:
		if gid := pet.ParseStopHatchReq(m.AppBody); gid != 0 {
			p.conn(m.Session).takeEgg = gid // 等回包确认取出成功再清
		}

	case m.Direction == gcp.S2C && m.Opcode == pet.OpStopHatchRsp:
		p.stopHatch(m, sc, acc)

	case m.Direction == gcp.S2C && m.Opcode == pet.OpGetAllHatchStatusRsp:
		p.applyHatchStatus(m, sc, acc)
	}
}

// stopHatch 处理取出孵蛋器回包(0x0300)。服务器不清 start_hatch_time,蛋字段不可信,
// 按「动作已确认」语义清零:回包 ret_info.changes 里的蛋仍常规入库(刷新背包侧 update_time,
// PruneTakenOut 可能已顺带清掉残留),再按 c2s 请求(0x02ff)记下的 gid 动作清零,保证
// 放入后立刻取出(时间差 < 孵满时长,推断判不出)也能实时清掉。
func (p *Pipeline) stopHatch(m capture.Message, sc *store.Scoped, acc string) {
	gid := p.conn(m.Session).takeEgg
	p.conn(m.Session).takeEgg = 0
	if eggs := pet.ParseChangedEggs(m.AppBody); len(eggs) > 0 {
		p.upsertEggs(sc, acc, eggs, m.Time)
	}
	if gid != 0 {
		if changed, err := sc.ClearHatching(gid, m.Time.Unix()); err == nil && changed {
			p.srv.Hub().Broadcast("eggs", acc, map[string]any{"account": acc})
		}
	}
}

// applyHatchStatus 处理孵化状态回包(0x0312):ret_info.changes 里的蛋先按常规入库
// (带精确的 last_hatch_update_sec),再用顶层 egg_gid[] 权威列表对账在孵标记。
// 取出孵蛋器的蛋不另发清零报文,start_hatch_time 会残留旧值,只有这里的对账能兜底
// 把标记清掉(见 docs/data.md 3.6)。
func (p *Pipeline) applyHatchStatus(m capture.Message, sc *store.Scoped, acc string) {
	eggs := pet.ParseChangedEggs(m.AppBody)
	skip := make(map[uint32]bool, len(eggs))
	for _, e := range eggs {
		skip[e.Gid] = true
	}
	if len(eggs) > 0 {
		p.upsertEggs(sc, acc, eggs, m.Time)
	}
	gids, secs := pet.ParseHatchStatus(m.AppBody)
	if len(gids) == 0 {
		return
	}
	if changed, err := sc.ReconcileHatching(gids, secs, skip, m.Time.Unix()); err == nil && changed {
		p.srv.Hub().Broadcast("eggs", acc, map[string]any{"account": acc})
	}
}

// applyEggPage 处理背包分页全量:本页的蛋入库,收齐 1..total 后对账。
func (p *Pipeline) applyEggPage(m capture.Message, sc *store.Scoped, acc string) {
	eggs, page, total := pet.ParseBagEggs(m.AppBody)
	p.upsertEggs(sc, acc, eggs, m.Time)

	as := p.acct(acc)
	sw := as.eggSweep
	if page <= 1 || sw == nil { // 新一轮:从第 1 页起累积
		sw = &eggSweep{gids: map[uint32]bool{}, nextPage: 1, valid: page <= 1, start: m.Time}
		as.eggSweep = sw
	}
	if page != sw.nextPage { // 乱序/漏页:本轮不对账,避免误标
		sw.valid = false
	}
	sw.nextPage = page + 1
	for _, e := range eggs {
		if !sw.gids[e.Gid] {
			sw.order = append(sw.order, e.Gid)
		}
		sw.gids[e.Gid] = true
	}
	if total == 0 || page < total {
		return
	}
	if sw.valid {
		sc.PruneMissingEggs(sw.gids, sw.start.Unix())
		sc.SetEggOrder(sw.order) // 收齐了才落次序:半轮的顺序是错的
	}
	as.eggSweep = nil
	p.srv.Hub().Broadcast("eggs", acc, map[string]any{"account": acc})
}

// upsertEggs 把解析出的蛋转成展示模型入库,并通知前端刷新。
// now 是消息时刻(离线回放同样按包走,见 store.UpsertEggs)。
func (p *Pipeline) upsertEggs(sc *store.Scoped, acc string, eggs []pet.Egg, now time.Time) {
	if len(eggs) == 0 {
		return
	}
	nowUnix := now.Unix()
	views := make([]*pet.EggView, 0, len(eggs))
	for i := range eggs {
		// 背包快照(0x1344/登录)里的 start_hatch_time 可能是取出后残留的旧值,时间推断
		// 兜底清掉,避免重新登录把已取出的蛋又标成在孵(见 pet.Egg.PruneTakenOut)。
		eggs[i].PruneTakenOut(nowUnix)
		views = append(views, pet.ToEggView(eggs[i], p.db))
	}
	if err := sc.UpsertEggs(views, nowUnix); err == nil {
		p.srv.Hub().Broadcast("eggs", acc, map[string]any{"account": acc})
	}
}

// recordEggParents 给刚从小窝收上来的蛋记双亲快照。
//
// 认领靠「刚点的那颗蛋 NPC」(c2s 0x0137 记下的 pendingEgg):它挂在母本的窝上,母本由
// furniture_guid 定位,父本取服务器下发的配对候选。窝里有好几颗同种蛋、或没抓到那次交互时,
// 退一步按**蛋物品 id** 在当前家园里找唯一匹配的窝;仍不唯一就不记(宁缺毋错)。
func (p *Pipeline) recordEggParents(conn string, sc *store.Scoped, eggs []pet.Egg, now time.Time) {
	cs := p.conns[conn]
	if cs == nil || cs.home == nil {
		return
	}
	h := cs.home
	for _, e := range eggs {
		src := h.pendingEgg
		if src != nil && (src.itemID != e.ItemID || now.Sub(h.pendingSince) > pendingEggTTL) {
			src = nil // 点的那颗与发下来的对不上(或隔太久):不认
		}
		if src == nil {
			src = h.uniqueEggByItem(e.ItemID)
		}
		if src == nil {
			continue
		}
		if ps := p.parentsOf(sc, h, src.furniture, now); ps != nil {
			sc.SetEggParents(e.Gid, ps)
		}
		if h.pendingEgg == src {
			h.pendingEgg = nil
		}
	}
}

// uniqueEggByItem 在当前家园里按蛋物品 id 找唯一的那颗窝上蛋;不唯一返回 nil。
func (h *homeState) uniqueEggByItem(item uint32) *homeEgg {
	var found *homeEgg
	for _, e := range h.eggs {
		if e.itemID != item {
			continue
		}
		if found != nil {
			return nil // 多个候选:无从区分
		}
		found = e
	}
	return found
}

// parentsOf 组一颗蛋的双亲快照:母本是窝里那只,父本取配对候选(多于一个即串窝,标 Ambiguous)。
func (p *Pipeline) parentsOf(sc *store.Scoped, h *homeState, furniture uint64, now time.Time) *pet.EggParents {
	actor, mother := h.petAt(furniture)
	if mother == nil {
		return nil
	}
	out := &pet.EggParents{Mother: p.parentSnap(sc, mother.PetGid, mother.Name), RecordedAt: now.Unix()}
	for _, mate := range h.matesOf(actor) {
		mp := h.pets[mate]
		if mp == nil {
			continue
		}
		if s := p.parentSnap(sc, mp.PetGid, mp.Name); s != nil {
			out.Fathers = append(out.Fathers, *s)
		}
	}
	out.Ambiguous = len(out.Fathers) > 1
	return out
}

// parentSnap 取一只亲本在此刻的快照(个体属性来自宠物库;宠物尚未入库时至少留下 gid 与名字)。
func (p *Pipeline) parentSnap(sc *store.Scoped, gid uint32, name string) *pet.EggParent {
	s := &pet.EggParent{Gid: gid, Name: name}
	pp, err := sc.GetPet(gid)
	if err != nil || pp == nil {
		return s
	}
	pet.FillSizePercentile(p.db, pp)
	if s.Name == "" {
		s.Name = pp.Name
	}
	s.Species, s.ConfID, s.Img = pp.Species, pp.ConfID, pp.Image.Head
	s.Gender, s.HeightM, s.WeightKg = pp.Gender, pp.HeightM, pp.WeightKg
	s.HeightPct, s.WeightPct = pp.HeightPct, pp.WeightPct
	s.Voice, s.Nature, s.Talent = pp.Voice, pp.Nature, pp.TalentRank
	return s
}
