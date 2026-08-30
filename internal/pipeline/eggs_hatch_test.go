package pipeline

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/whoisnian/rocom-capture/internal/gcp"
	"github.com/whoisnian/rocom-capture/internal/pet"
	"github.com/whoisnian/rocom-capture/internal/store"
)


// —— 孵蛋器在孵标记:权威口径 egg_gid 的时序 ——
//
// 背景(见 internal/pet/egg.go 的 Hatching):蛋从孵蛋器取出后服务器**不清
// start_hatch_time**,只把进度清零。故判定必须走权威的 egg_gid 列表,而不能用蛋自己的字段。
//
// 权威列表有两个下发点:登录数据 0x0102(PetBackpackInfo.egg_gid)与开孵蛋器 0x0312。
// 但登录包**先于**背包分页到达 —— 那时库里还没有蛋,对账改不动任何行。
// 故 connState.hatchGids 把列表记住,等蛋从 0x1344 入库时逐颗判定。
// 本测试锁住这个时序:没有它,首次运行(空库)会全部判成"不在孵"。

// loginWithHatchBody 构造带孵蛋器占用列表的登录回包:body.field2 = LoginData,
// 其 player_info(2).pet_info(4).backpack_info(9) = PetBackpackInfo{egg_gid(1)…, boxes(3)}。
// boxes 需凑够 5 个非零 pet_gid,否则 bestBackpack 判为误解析。
func loginWithHatchBody(userID uint64, name string, eggGids []uint64) []byte {
	base := protowire.AppendTag(nil, 1, protowire.VarintType)
	base = protowire.AppendVarint(base, userID)
	base = protowire.AppendTag(base, 3, protowire.BytesType)
	base = protowire.AppendString(base, name)
	login := protowire.AppendTag(nil, 1, protowire.BytesType)
	login = protowire.AppendBytes(login, base)

	// PetBackpackInfo: egg_gid(1) 重复; boxes(3) 至少 5 只宠物
	box := protowire.AppendTag(nil, 1, protowire.VarintType)
	box = protowire.AppendVarint(box, 1) // box_id
	for _, g := range []uint64{6476, 12335, 11291, 18471, 266, 1503} {
		box = protowire.AppendTag(box, 3, protowire.VarintType)
		box = protowire.AppendVarint(box, g)
	}
	bp := protowire.AppendTag(nil, 3, protowire.BytesType)
	bp = protowire.AppendBytes(bp, box)
	for _, g := range eggGids {
		bp = protowire.AppendTag(bp, 1, protowire.VarintType)
		bp = protowire.AppendVarint(bp, g)
	}
	pi := protowire.AppendTag(nil, 9, protowire.BytesType)
	pi = protowire.AppendBytes(pi, bp)
	petInfo := protowire.AppendTag(nil, 4, protowire.BytesType)
	petInfo = protowire.AppendBytes(petInfo, pi)
	login = protowire.AppendTag(login, 2, protowire.BytesType)
	login = protowire.AppendBytes(login, petInfo)

	b := protowire.AppendTag(nil, 2, protowire.BytesType)
	return protowire.AppendBytes(b, login)
}

// TestHatchSlotsLoginBeforeBackpack 锁住「登录包先到、蛋后入库」的时序:
// 空库时收到登录包(带 egg_gid),随后背包分页送来蛋 —— 在孵标记应当立刻判对,
// 而不是全部为 0(那样页面会显示「孵蛋器 0/N」,与实际不符)。
func TestHatchSlotsLoginBeforeBackpack(t *testing.T) {
	p, _ := newTestPipeline(t)

	// 1) 登录先到:库里还没有蛋,但对账列表要记住
	p.handle(msg(gcp.S2C, pet.OpLoginRsp, loginWithHatchBody(1, "测试", []uint64{3259, 3262, 3264})))
	if got := p.conn(testSess).hatchGids; len(got) != 3 || !got[3259] || !got[3262] || !got[3264] {
		t.Fatalf("登录未记住孵蛋器列表: %v", got)
	}

	// 2) 背包分页随后到:三颗在孵的蛋 + 两颗不在孵的,全部首次入库
	p.handle(msg(gcp.S2C, pet.OpGetBagItemInfoByPageRsp,
		bagEggPageBody(1, 1, []uint32{3259, 3262, 3264, 4001, 4002})))

	eggs, err := p.st.For(testAcc).ListEggs(store.EggFilter{})
	if err != nil {
		t.Fatalf("读蛋: %v", err)
	}
	hatching := map[uint32]bool{}
	for _, e := range eggs {
		if e.Hatching {
			hatching[e.Gid] = true
		}
	}
	if len(eggs) != 5 {
		t.Fatalf("蛋数 = %d, want 5", len(eggs))
	}
	for _, g := range []uint32{3259, 3262, 3264} {
		if !hatching[g] {
			t.Errorf("gid %d 在登录的 egg_gid 里,应判为在孵", g)
		}
	}
	for _, g := range []uint32{4001, 4002} {
		if hatching[g] {
			t.Errorf("gid %d 不在 egg_gid 里,不该判为在孵", g)
		}
	}
}

// bagEggPageBody 构造 0x1344 背包分页回包:total_page(2)=page, req_page(3)=page,
// bag_info(4).item_list(3) 一组,type(1)=EggItemType, items(2) 为各蛋。
func bagEggPageBody(page uint32, total uint32, gids []uint32) []byte {
	var items []byte
	for _, g := range gids {
		item := protowire.AppendTag(nil, 1, protowire.VarintType)
		item = protowire.AppendVarint(item, uint64(g))
		item = protowire.AppendTag(item, 15, protowire.BytesType)
		item = protowire.AppendBytes(item, eggBriefBytes(g))
		items = protowire.AppendTag(items, 2, protowire.BytesType)
		items = protowire.AppendBytes(items, item)
	}
	list := protowire.AppendTag(nil, 1, protowire.VarintType)
	list = protowire.AppendVarint(list, pet.EggItemType)
	list = append(list, items...)

	bag := protowire.AppendTag(nil, 3, protowire.BytesType)
	bag = protowire.AppendBytes(bag, list)

	b := protowire.AppendTag(nil, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(total))
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(page))
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	return protowire.AppendBytes(b, bag)
}

// eggBriefBytes 拼一颗蛋的 PetEggBrief:conf_id(1)=3062001(小独角兽),max(6)=57600,
// 其余给非零值以通过 ParseBagEggs 的合法性检查。start_hatch(9) 故意都给 0 ——
// 真实场景里取出过的蛋反而带着非零的 start_hatch,那正是要靠 egg_gid 纠正的。
func eggBriefBytes(gid uint32) []byte {
	b := protowire.AppendTag(nil, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, 3062001)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, 42) // height
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, 14525) // weight
	b = protowire.AppendTag(b, 6, protowire.VarintType)
	b = protowire.AppendVarint(b, 57600) // max_secs
	return b
}

// —— 放进孵蛋器的蛋要立刻出现在孵蛋器栏 ——
//
// 用户操作:在**背包**里点一颗蛋选「孵化」。客户端发 0x0164(用道具),服务器回包
// 带回这颗蛋的 BagItem(start_hatch_time 已被置为非零)。
//
// 现状:0x0164 走「只当普通变化入库」那条分支,known 传 nil,hatching 列不动,
// 于是孵蛋器栏不新增;要等玩家打开孵蛋器面板、0x0312 的权威 egg_gid 列表到货才收敛。
// 但从背包入孵时孵蛋器面板并未打开,0x0311/0x0312 未必会来 —— 这一等就是永远。
//
// 为何这里可以信 start_hatch_time:不信任它只因「取出后服务器不清零」,那说的是
// **快照**(0x1344 / 登录包)里的残留值;而 0x0164 / 0x0300 是服务器对「刚放进去 /
// 刚取出来」这一动作的明确回包,是非即否都是当场证据,不存在残留问题。

// TestPutEggIntoIncubatorShowsIt 在背包入孵后,这颗蛋应立刻出现在孵蛋器栏。
func TestPutEggIntoIncubatorShowsIt(t *testing.T) {
	p, _ := newTestPipeline(t)

	// 登录时孵蛋器是空的
	p.handle(msg(gcp.S2C, pet.OpLoginRsp, loginWithHatchBody(1, "测试", nil)))
	p.handle(msg(gcp.S2C, pet.OpGetBagItemInfoByPageRsp, bagEggPageBody(1, 1, []uint32{4001})))
	if got := eggHatching(t, p, 4001); got {
		t.Fatalf("前置条件:入孵前 gid 4001 不该在孵")
	}

	// 在背包里点「孵化」:0x0164 回包带回这颗蛋,start_hatch_time 已置位
	p.handle(msg(gcp.S2C, pet.OpUseBagItemRsp, eggActionBody(4001)))

	if got := eggHatching(t, p, 4001); !got {
		t.Errorf("背包入孵后 gid 4001 应判为在孵(孵蛋器栏要立刻多出这颗蛋),实际 hatching=false")
	}
}

// TestHatchStatusNotClobberByStaleLoginList 权威列表到货后不能被登录那一刻的旧快照冲掉。
//
// hatchGids 是登录时记下的孵蛋器占用列表,本意是给「登录包先到、蛋后入库」的时序兜底。
// 但它**从不失效**:此后每一次背包分页(0x1344)都拿这份登录时的旧列表把 hatching 列
// 整体覆盖回去。于是入孵之后再开一次背包,标记就被打成登录时的样子 —— 新放进去的那颗
// (登录时还不在孵蛋器里)会被打回不在孵。
func TestHatchStatusNotClobberByStaleLoginList(t *testing.T) {
	p, _ := newTestPipeline(t)

	p.handle(msg(gcp.S2C, pet.OpLoginRsp, loginWithHatchBody(1, "测试", nil)))
	p.handle(msg(gcp.S2C, pet.OpGetBagItemInfoByPageRsp, bagEggPageBody(1, 1, []uint32{4001})))

	// 开孵蛋器:权威列表说 4001 在孵
	p.handle(msg(gcp.S2C, pet.OpGetAllHatchStatusRsp, hatchStatusBody([]uint32{4001}, nil)))
	if got := eggHatching(t, p, 4001); !got {
		t.Fatalf("前置条件:0x0312 权威列表到货后 4001 应在孵,实际 false")
	}

	// 再开一次背包(入孵后回到背包很常见):不该被登录时的空列表打回
	p.handle(msg(gcp.S2C, pet.OpGetBagItemInfoByPageRsp, bagEggPageBody(1, 1, []uint32{4001})))
	if got := eggHatching(t, p, 4001); !got {
		t.Errorf("开背包后 4001 被登录时的旧列表打回不在孵(hatchGids 未随权威列表更新)")
	}
}

// TestStopHatchRemovesFromIncubator 取出(0x0300)应立刻把这颗蛋从孵蛋器栏撤下。
func TestStopHatchRemovesFromIncubator(t *testing.T) {
	p, _ := newTestPipeline(t)

	p.handle(msg(gcp.S2C, pet.OpLoginRsp, loginWithHatchBody(1, "测试", []uint64{4001})))
	p.handle(msg(gcp.S2C, pet.OpGetBagItemInfoByPageRsp, bagEggPageBody(1, 1, []uint32{4001})))
	if got := eggHatching(t, p, 4001); !got {
		t.Fatalf("前置条件:4001 应在孵")
	}

	p.handle(msg(gcp.S2C, pet.OpStopHatchRsp, eggActionBody(4001)))
	if got := eggHatching(t, p, 4001); got {
		t.Errorf("取出后 4001 应立刻从孵蛋器撤下,实际仍在孵")
	}
}

// ---- 辅助 ----

// eggHatching 读某颗蛋当前是否在孵。
func eggHatching(t *testing.T, p *Pipeline, gid uint32) bool {
	t.Helper()
	eggs, err := p.st.For(testAcc).ListEggs(store.EggFilter{})
	if err != nil {
		t.Fatalf("读蛋: %v", err)
	}
	for _, e := range eggs {
		if e.Gid == gid {
			return e.Hatching
		}
	}
	t.Fatalf("库里没有 gid %d", gid)
	return false
}

// eggActionBody 构造 0x0164 / 0x0300 这类「动作回包」:
// ret_info(1).goods_change_info(4).changes(1).bag_item(4) = 一颗蛋。
//
// start_hatch_time(9) **两种动作下都给非零**,这是刻意按服务器真实行为建模的:
// 取出时服务器只把进度清零,**不清入孵时刻**(这正是「取出过的蛋会被误判在孵」的根源,
// 见 pet.Egg.Hatching 的注释)。故单看这个字段分不出放入还是取出 —— 只能靠 opcode。
// 测试若把它留成 0,就测不出「取出」分支是否真的把标记清掉了。
func eggActionBody(gid uint32) []byte {
	brief := eggBriefBytes(gid)
	brief = protowire.AppendTag(brief, 9, protowire.VarintType)
	brief = protowire.AppendVarint(brief, 1785000000) // start_hatch_time(两种动作下都非零)
	item := protowire.AppendTag(nil, 1, protowire.VarintType)
	item = protowire.AppendVarint(item, uint64(gid))
	item = protowire.AppendTag(item, 15, protowire.BytesType)
	item = protowire.AppendBytes(item, brief)

	chg := protowire.AppendTag(nil, 4, protowire.BytesType)
	chg = protowire.AppendBytes(chg, item)
	change := protowire.AppendTag(nil, 1, protowire.BytesType)
	change = protowire.AppendBytes(change, chg)

	// ret_info: field 4 = goods_change_info
	ret := protowire.AppendTag(nil, 4, protowire.BytesType)
	ret = protowire.AppendBytes(ret, change)

	// 回包顶层: field 1 = ret_info
	b := protowire.AppendTag(nil, 1, protowire.BytesType)
	return protowire.AppendBytes(b, ret)
}

// hatchStatusBody 构造 0x0312 孵化状态回包:顶层 egg_gid(2) 与 hatched_secs(3)。
func hatchStatusBody(gids []uint32, secs []int32) []byte {
	var b []byte
	for i, g := range gids {
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(g))
		if i < len(secs) {
			b = protowire.AppendTag(b, 3, protowire.VarintType)
			b = protowire.AppendVarint(b, uint64(secs[i]))
		}
	}
	return b
}
