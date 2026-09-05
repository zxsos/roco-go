package store

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/whoisnian/rocom-capture/internal/pet"
)

// 精灵蛋的持久化。与宠物同样按 account 隔离,主键 (account, egg gid)。
//
// 这张表就是**当前背包里的蛋**:破壳/送人/用掉的直接删行(页面只看背包,留着也没人看)。
// 唯一要当心的是 **双亲快照单列存**:亲本可能被放生/赠送(pets 行随之删除),而蛋上记下的
// 双亲要留存,故 parents 存的是收蛋那一刻的 JSON 快照,不引用 pets 表;常规 upsert 不碰它。
//
// **hatching 列是「在孵蛋器里」的权威状态,只由 0x0312 对账(ReconcileHatching)维护**:
// 服务器取出蛋时不清 start_hatch_time,放入/取出/破壳动作的回包与背包快照(0x1344/登录)
// 的入孵时刻都不可信,故 UpsertEggs 只把新行初始为 0、更新时不碰该列;放入(0x0164)、
// 取出(0x0300)回包一律只当普通变化入库,标记在玩家下次打开孵蛋器(0x0312)时全量收敛。
// 读取时以列为准覆盖 data 里的推断值(见 ListEggs)。
//
// **data 里的 HatchUpdate 一律是「抓包主机的观测时刻」,不是服务器下发的
// last_hatch_update_sec**(理由见 UpsertEggs 内的注释)。前端拿相邻两次采样做差分
// 估倍率,两个时刻不同源就没有差分可言 —— 这里是那个约定的唯一落地点。

// UpsertEggs 批量写入/更新蛋(不动 parents 与 first_seen)。
// now 取**消息时刻**而非 time.Now():离线回放的包时间是几小时前的,与挂钟混用会让
// PruneMissingEggs 的 first_seen<=before 永远不成立,过期的蛋就永远删不掉。
//
// knownHatch 是可选的权威「在孵」判定(nil = 不知道):
//   - nil:hatching 恒写 0(新行)且不覆盖旧值 —— 背包快照与放入/取出回包的
//     start_hatch_time 都不可信,权威状态只由 egg_gid 对账(ReconcileHatching)维护
//   - 非 nil:逐颗按列表写(1/0)。用于登录数据先到、蛋后入库的时序 —— 那时对账
//     改不动任何行,只能由入库时直接判定(见 pipeline/eggs.go 的 hatchGids)
func (sc *Scoped) UpsertEggs(eggs []*pet.EggView, now int64, knownHatch map[uint32]bool) error {
	if len(eggs) == 0 {
		return nil
	}
	// 倍率只能从相邻两次采样反推:新值在入参里,旧值得在覆盖之前从库里取出来。
	// 这一步必须在下面改写 HatchUpdate 之前做(见本函数内「HatchUpdate 改记作观测时刻」)。
	prev := sc.prevHatchPoints(eggs)
	var samples []float64
	rows := make([][]any, 0, len(eggs))
	for _, e := range eggs {
		// 权威列表要连 data 里的推断值一起改:读取时以 hatching 列为准覆盖 data,
		// 但 data 是整块 JSON,留着不一致的推断值会在别处(如导出)露出来
		if knownHatch != nil {
			e.Hatching = knownHatch[e.Gid]
		}
		// HatchUpdate 改记作**抓包主机的观测时刻** now,不采信服务器下发的
		// last_hatch_update_sec(那是服务器的钟)。
		//
		// 前端估倍率靠相邻两次采样的差分 Δv/Δt(见 web/src/pages/eggs/hatch.js),
		// 两个 t 一旦不同源,钟差就直接落进分母:网关时钟若与游戏服务器差 60 秒,
		// 相隔 10 秒的两次采样会被算成 70 秒 → 5 倍速算出 0.7,再被钳到下限 1。
		// 而 0x0312 顶层那份 hatched_secs[] 根本不带时刻,本来也只能配 now ——
		// 不统一成它,就永远存在「这次是这个钟、下次是那个钟」的组合。
		// now 是唯一处处可得的钟,统一到它上面差分才成立。
		//
		// 只在确有进度时盖:不在孵蛋器里的蛋 hatched_secs 恒为 0,若也给盖上 now,
		// 它入孵后第一次采样的差分就退化成「从 0 到现在」—— 那正是被实测否掉的
		// 单点法(玩家跑动过后会虚报成 8~9 倍)。留 0,前端按「进度未知」处理。
		// 记一次倍率样本:此刻新值(e.HatchedSecs, now)与库里的旧值(p.v, p.t)都在手上,
		// 差分就是倍率本身(详见 hatch_rate.go)。要求 v 严格递增 —— 「新 t 配旧 v」
		// 是常态(背包快照 0x1344 常下发没重算的旧进度),照算会得到 0 倍再被钳到下限。
		// 已孵满的蛋进度被 maxSecs 截断、差分必然偏小,一并排除。
		if p, ok := prev[e.Gid]; ok && p.t > 0 && p.v > 0 && e.HatchedSecs > p.v && now > p.t {
			if p.max <= 0 || (p.v < p.max && e.HatchedSecs < p.max) {
				samples = append(samples, float64(e.HatchedSecs-p.v)/float64(now-p.t))
			}
		}
		if e.HatchedSecs > 0 {
			e.HatchUpdate = now
		} else {
			e.HatchUpdate = 0
		}
		data, err := json.Marshal(e)
		if err != nil {
			continue
		}
		var hpct, wpct any
		if e.HeightPct != nil {
			hpct = *e.HeightPct
		}
		if e.WeightPct != nil {
			wpct = *e.WeightPct
		}
		var hatch int
		if knownHatch != nil && knownHatch[e.Gid] {
			hatch = 1
		}
		rows = append(rows, []any{
			sc.account, e.Gid, e.ItemID, e.ConfID, e.Name, e.Species,
			e.HeightM, e.WeightKg, hpct, wpct, e.Src, hatch, e.ObtainedAt,
			now, now, string(data),
		})
	}
	// knownHatch 非 nil 时,冲突更新也要覆盖 hatching 列(权威列表到货即订正);
	// 为 nil 则保持原样,免得背包快照把对账结果冲掉。
	hatchClause := ""
	if knownHatch != nil {
		hatchClause = ", hatching=excluded.hatching"
	}
	// 一批下发的几颗蛋共享同一次服务器结算(实测三颗蛋同秒各 +10s),若这次是批量
	// 补齐的跳变,几颗会一起跳 —— 逐个记进去等于把同一次跳变放大成好几票,故先取
	// 中位数,整批只贡献一个样本。记失败不影响主流程:倍率只是外推的输入,估不出来
	// 时前端仍有保守的 1 倍可用。
	if len(samples) > 0 {
		_ = sc.AddHatchSample(median(samples))
	}
	return execBatch(sc.db, `
INSERT INTO eggs(account, gid, item_id, conf_id, name, species,
                 height, weight, height_pct, weight_pct, src, hatching, obtained_at,
                 first_seen, updated_at, data)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(account, gid) DO UPDATE SET
  item_id=excluded.item_id, conf_id=excluded.conf_id, name=excluded.name, species=excluded.species,
  height=excluded.height, weight=excluded.weight,
  height_pct=excluded.height_pct, weight_pct=excluded.weight_pct,
  src=excluded.src, obtained_at=excluded.obtained_at`+hatchClause+`,
  updated_at=excluded.updated_at, data=excluded.data`, rows)
}

// hatchPoint 是一颗蛋在某个时刻的孵化进度采样(算差分用的旧值)。
type hatchPoint struct {
	t   int64 // HatchUpdate:上次采样的观测时刻
	v   int32 // HatchedSecs:那时的已孵秒数
	max int32 // MaxSecs:孵满所需秒数(判是否已孵满,截断的差分不能用)
}

// prevHatchPoints 批量取这些蛋**当前库里**的孵化进度,key 是 gid。
// 只查要算差分的那几颗(一条消息最多几颗),拿不到(新蛋)就不出现在结果里。
func (sc *Scoped) prevHatchPoints(eggs []*pet.EggView) map[uint32]hatchPoint {
	out := make(map[uint32]hatchPoint, len(eggs))
	if len(eggs) == 0 {
		return out
	}
	qs := make([]string, len(eggs))
	args := make([]any, 0, len(eggs)+1)
	args = append(args, sc.account)
	for i, e := range eggs {
		qs[i] = "?"
		args = append(args, e.Gid)
	}
	rows, err := sc.rdb.Query(`SELECT gid, data FROM eggs WHERE account=? AND gid IN (`+
		strings.Join(qs, ",")+`)`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var gid uint32
		var data string
		if err := rows.Scan(&gid, &data); err != nil {
			continue
		}
		var v pet.EggView
		if json.Unmarshal([]byte(data), &v) != nil {
			continue
		}
		out[gid] = hatchPoint{t: v.HatchUpdate, v: v.HatchedSecs, max: v.MaxSecs}
	}
	return out
}

// SetEggParents 记下某颗蛋的双亲快照(收蛋那一刻推断出来的);已有记录不覆盖,
// 免得后来的背包全量或再次进家园把当时的快照冲掉。
func (sc *Scoped) SetEggParents(gid uint32, p *pet.EggParents) error {
	blob, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = sc.db.Exec(
		`UPDATE eggs SET parents=? WHERE account=? AND gid=? AND (parents IS NULL OR parents='')`,
		string(blob), sc.account, gid)
	return err
}

// SetEggOrder 记下这些蛋在背包里的次序(下标即服务器下发顺序)。
// 页面的两种排序都可能出现「所有键都相等」的两颗蛋(同一时刻入包的同种蛋),游戏内此时保持
// 背包原始次序(客户端 table.sort 的输入就是这个顺序),故这里把它存下来当基准。
func (sc *Scoped) SetEggOrder(order []uint32) error {
	rows := make([][]any, 0, len(order))
	for i, gid := range order {
		rows = append(rows, []any{i, sc.account, gid})
	}
	return execBatch(sc.db, `UPDATE eggs SET seq=? WHERE account=? AND gid=?`, rows)
}

// DeleteEgg 删掉一颗蛋(破壳即用完了,背包里没有这一件了)。
func (sc *Scoped) DeleteEgg(gid uint32) error {
	_, err := sc.db.Exec(`DELETE FROM eggs WHERE account=? AND gid=?`, sc.account, gid)
	return err
}

// PruneMissingEggs 据一轮完整的背包全量对账:不在背包里的直接删掉。
// before 之后才首次见到的行放过,避免与同一时刻的新蛋抢跑。
func (sc *Scoped) PruneMissingEggs(keep map[uint32]bool, before int64) error {
	rows, err := sc.rdb.Query(`SELECT gid FROM eggs WHERE account=? AND first_seen<=?`, sc.account, before)
	if err != nil {
		return err
	}
	var gone []uint32
	for rows.Next() {
		var gid uint32
		if err := rows.Scan(&gid); err == nil && !keep[gid] {
			gone = append(gone, gid)
		}
	}
	rows.Close()
	for _, gid := range gone {
		sc.db.Exec(`DELETE FROM eggs WHERE account=? AND gid=?`, sc.account, gid)
	}
	return nil
}

// ReconcileHatching 用 0x0312 的权威列表对账「在孵蛋器」标记(见 docs/data.md 3.6)。
// hatching 列**只由这里维护**:放入/取出/破壳都不再写列,全靠它全量收敛——
//   - gids 为空(孵蛋器空) → 本账号所有标着在孵的蛋全部清列与进度
//   - gids 里没有、但列标着在孵的蛋 → 清列与进度(取出/破壳残留的收敛点)
//   - gids 里的蛋 → 确保列为在孵;skip 里的蛋刚由 ret_info.changes 精确刷新过
//     (含 last_hatch_update_sec),不再动它们的进度
//   - secs 与 gids 长度一致时才顺带刷新进度;配的时刻同样取消息时刻(与 UpsertEggs
//     同一个钟,否则前端的差分就跨钟了);proto3 会省掉 hatched_secs=0 的项,
//     数组对不上时只对账标记
//
// 返回是否有行被改动。
func (sc *Scoped) ReconcileHatching(gids []uint32, secs []int32, skip map[uint32]bool, now int64) (bool, error) {
	in := make(map[uint32]bool, len(gids))
	for _, g := range gids {
		in[g] = true
	}
	secBy := map[uint32]int32{}
	if paired := len(secs) == len(gids); paired {
		for i, g := range gids {
			secBy[g] = secs[i]
		}
	}
	rows, err := sc.rdb.Query(`SELECT gid, hatching, data FROM eggs WHERE account=?`, sc.account)
	if err != nil {
		return false, err
	}
	var updates [][]any
	// samples 是本次对账里各颗蛋的瞬时倍率。一次下发的几颗蛋**共享同一次服务器结算**
	// (实测三颗蛋同秒各 +10s),若某次是批量补齐的跳变,几颗蛋会一起跳 —— 逐个记进去
	// 等于把同一次跳变放大成好几票。故这批先取中位数,整批只贡献一个样本。
	var samples []float64
	for rows.Next() {
		var gid uint32
		var hatching int
		var data string
		if err := rows.Scan(&gid, &hatching, &data); err != nil {
			continue
		}
		var v pet.EggView
		if json.Unmarshal([]byte(data), &v) != nil {
			continue
		}
		if in[gid] {
			if hatching == 1 {
				if skip[gid] {
					continue // changes 已精确刷新过,不动
				}
				if sec, ok := secBy[gid]; !ok || (v.HatchedSecs == sec && v.HatchUpdate == now) {
					continue // 无可刷新进度
				}
			} else {
				hatching = 1
				v.Hatching = true
			}
			if sec, ok := secBy[gid]; ok {
				// 顺手记一次倍率样本:这里同时握着**旧的** (HatchUpdate, HatchedSecs)
				// 和即将写入的新 (now, sec),差分正是倍率本身。后端能做到的原因是它
				// 看得见每一次下发,而前端通常只有最后一次快照(详见 hatch_rate.go)。
				//
				// v 必须严格递增(「新 t 配旧 v」是常态:进度只在开孵蛋器/开背包时
				// 才由服务器重算),且两端都要有值 —— 否则算出来的是 0 倍或分母为 0。
				if prev := v.HatchedSecs; v.HatchUpdate > 0 && prev > 0 && sec > prev && now > v.HatchUpdate {
					// 已孵满的蛋进度被 maxSecs 截断,差分必然偏小,混进来会把中位数拉低
					if v.MaxSecs <= 0 || (prev < v.MaxSecs && sec < v.MaxSecs) {
						samples = append(samples, float64(sec-prev)/float64(now-v.HatchUpdate))
					}
				}
				v.HatchedSecs, v.HatchUpdate = sec, now
				if sec == 0 {
					// 进度为 0 时连时刻一起清零(与 UpsertEggs 同一条规矩):留着旧时刻
					// 配 0 进度,前端会拿 elapsed 去外推,退化成被实测否掉的单点法。
					v.HatchUpdate = 0
				}
			}
		} else if hatching == 1 {
			hatching = 0
			v.Hatching = false
			v.HatchedSecs, v.HatchUpdate, v.StartHatch = 0, 0, 0
		} else {
			continue
		}
		blob, err := json.Marshal(&v)
		if err != nil {
			continue
		}
		updates = append(updates, []any{hatching, string(blob), now, sc.account, gid})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	// 倍率样本与进度更新解耦:哪怕本次一颗蛋都没改动(进度未变),差分样本也要记 ——
	// 否则「服务器重算了进度但库里数值恰好相同」这类情况会漏采。记失败不影响主流程,
	// 倍率只是外推的输入,回到「未知」前端仍有保守的 1 倍可用。
	if len(samples) > 0 {
		_ = sc.AddHatchSample(median(samples))
	}
	if len(updates) == 0 {
		return false, nil
	}
	return true, execBatch(sc.db,
		`UPDATE eggs SET hatching=?, data=?, updated_at=? WHERE account=? AND gid=?`, updates)
}

// EggFilter 是精灵蛋列表的筛选条件(空值即不限)。
// 排序不在这里:游戏内的「品质排序」是品类/品质/物品排序号的复合键,这些键取自名称库、
// 读取时才重算(见 pet.RefreshEggView),故排序由调用方在重算之后用 pet.SortEggs 做。
type EggFilter struct {
	Search string // 按蛋名/物种名模糊
}

// ListEggs 按筛选返回蛋列表(已合并 parents 快照)。
func (sc *Scoped) ListEggs(f EggFilter) ([]*pet.EggView, error) {
	where := `account=?`
	args := []any{sc.account}
	if f.Search != "" {
		where += ` AND (name LIKE ? OR species LIKE ?)`
		args = append(args, "%"+f.Search+"%", "%"+f.Search+"%")
	}
	// 基准顺序 = 背包里的原始次序(见 SetEggOrder);还没对过账的新蛋没有 seq,排在最后。
	// pet.SortEggs 用的是稳定排序,故所有键都相等的蛋会保持这个次序,与游戏内一致。
	rows, err := sc.rdb.Query(
		`SELECT data, parents, hatching FROM eggs WHERE `+where+
			` ORDER BY seq IS NULL, seq, gid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*pet.EggView
	for rows.Next() {
		var data string
		var parents sql.NullString
		var hatching int
		if err := rows.Scan(&data, &parents, &hatching); err != nil {
			continue
		}
		var e pet.EggView
		if json.Unmarshal([]byte(data), &e) != nil {
			continue
		}
		e.Hatching = hatching == 1 // 以权威列覆盖 data 里的推断值
		if parents.Valid && parents.String != "" {
			var p pet.EggParents
			if json.Unmarshal([]byte(parents.String), &p) == nil {
				e.Parents = &p
			}
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
