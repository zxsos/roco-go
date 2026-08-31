"""合并两份第三方技能资料,输出到 internal/gamedata/data/skills.json。

与其余 gen_*.py 不同,本脚本的数据源**不是游戏解包**,而是两个第三方资料站:

  1. skillIds.json(由 scripts/fetch_skill_ids.py 抓取,aismile.dev 技能图鉴)
     → **skill_id → 技能中文名**,604 条。这是协议里 base_skill_id(7020500 这类)
       的唯一可用映射:有了它,草系试炼里那些裸技能 id 才能显示中文名。
  2. skillGuideData.json(arkmeng.cn 的开放数据,含 492 个技能的威力/能耗/效果,
     以及每个技能「哪些精灵天生会、几级学会」的 innatePets 清单)
     → **形态 → 天生技能**(带学会等级)。

两份互补,缺一不可:
  - 只有 1:试炼技能能显示名,但宠物详情拿不到「这只精灵几级学会什么」;
  - 只有 2:宠物详情有数据,但试炼的裸 id 反查不了 —— 试炼技能会**融合**,
    威力被改写(实测 7020500 融合1次:威力 25→30),按数值反推会命中错误技能
    (30/4 反查得「埋伏」,真身是「乱打」),只能按 id 精确查。

**这是过渡方案**:权威来源是游戏解包的 SKILL_CONF(id→中文名,与协议同源)。
等 scripts/unpack.sh 解出后应改走解包数据,并删掉本脚本与 fetch_skill_ids.py。

为什么不进 names.json:names.json 全部来自解包 Bin 配置,数据源与更新节奏都不同,
混在一起会让「这份名字是从哪来的」说不清。故独立成 skills.json。

输出结构(紧凑数组,体积优先):
  names: {skill_id: 技能名}                      —— 供按 id 查(试炼页)
  pets:  {petbase_id: [[名, 等级, 属性, 威力, 能耗, 效果下标, skill_id]]}
  effects: [...]}                                —— 共享效果描述池

运行:
  uv run python scripts/fetch_skill_ids.py   # 抓 skill_id 映射(1)
  uv run python scripts/gen_skills.py        # 合并生成

数据源(均可用环境变量覆盖):
  ROCOM_SKILL_IDS   skill_id→名,    默认 ~/Downloads/rocom/skillIds.json
  ROCOM_SKILL_GUIDE 技能详情+天生,  默认 ~/Downloads/rocom/skillGuideData.json
"""
import json
import os
import sys

SKILL_IDS = os.environ.get(
    "ROCOM_SKILL_IDS", os.path.expanduser("~/Downloads/rocom/skillIds.json")
)
SKILL_JSON = os.environ.get(
    "ROCOM_SKILL_GUIDE", os.path.expanduser("~/Downloads/rocom/skillGuideData.json")
)
NAMES = "internal/gamedata/data/names.json"
OUT_DIR = "internal/gamedata/data"
OUT = os.path.join(OUT_DIR, "skills.json")

for path, how in (
    (SKILL_IDS, "先跑: uv run python scripts/fetch_skill_ids.py"),
    (SKILL_JSON, "获取: curl -o ~/Downloads/rocom/skillGuideData.json \\\n"
                 "        https://arkmeng.cn/storage/files/skillGuideData.json"),
):
    if not os.path.exists(path):
        sys.exit(f"缺技能数据: {path}\n{how}\n(或设 ROCOM_SKILL_IDS / ROCOM_SKILL_GUIDE 指向已有文件)")
if not os.path.exists(NAMES):
    sys.exit(f"缺 {NAMES} —— 请先跑 scripts/gen_gamedata.py")

with open(SKILL_IDS, encoding="utf-8") as f:
    id_raw = json.load(f)
with open(SKILL_JSON, encoding="utf-8") as f:
    skills = json.load(f)
with open(NAMES, encoding="utf-8") as f:
    names = json.load(f)

# ---- 1. skill_id -> 技能名(主映射,来自 fetch_skill_ids.py)----
id2name = {}
for s in id_raw.get("skills", []):
    id2name[int(s["skillId"])] = s["name"]

# 反查 名 -> [skill_id],用于把 id 回填进天生技能表。
# 有**重名**的技能(同名多个 id)填 0 表示「不确定是哪个」:
#   - 借用/取念/复写:各 4 个连续 id(7020840~7020843),是同技能的不同变体;
#   - 愿力冲击:15 个 id,是各精灵的专属版本。
# 这类按名字无从判断该形态会的是哪一个,填错比不填更糟。
name2ids = {}
for sid, nm in id2name.items():
    name2ids.setdefault(nm, []).append(sid)
ambiguous = {n for n, ids in name2ids.items() if len(ids) > 1}

# ---- 2. 形态全名 -> petbase_id ----
name2pets = {}
for pid, v in names.get("petbase", {}).items():
    full = v["n"] + (("_" + v["f"]) if v.get("f") else "")
    name2pets.setdefault(full, []).append(int(pid))


def main_id(ids):
    """取主形态 petbase_id:优先常规区间(<1e7)里最小的那个。

    与 gen_gamedata.py 的 _real 判据同口径 —— 同名还有一批 petbase_id 落在
    1.3e7~1.9e7 的复制形态(剧情/测试/NPC 用,如「迪莫」16000004),它们不是真实
    图鉴形态,技能表不该挂在它们身上。
    """
    real = [i for i in ids if i < 10_000_000]
    return min(real or ids)


# ---- 3. 天生技能表 + 回填 skill_id ----
# 效果描述去重:5213 条技能里只有 364 种不同文本,抽成共享池省 81 KiB。
effects, eff_idx = [], {}
by_pet, skipped, no_id = {}, set(), 0

for s in skills:
    nm = s["name"]
    # 重名技能填 0(见上 ambiguous 的说明)
    sid = 0 if nm in ambiguous else (name2ids.get(nm) or [0])[0]
    if sid == 0 and nm not in ambiguous:
        no_id += 1
    for p in s.get("innatePets", []):
        ids = name2pets.get(p["petName"])
        if not ids:
            skipped.add(p["petName"])
            continue
        eff = s.get("effect") or ""
        if eff not in eff_idx:
            eff_idx[eff] = len(effects)
            effects.append(eff)
        # 条目用**数组**而非对象:每条省下 7 个重复键名,5213 条累计省 400+ KiB。
        by_pet.setdefault(str(main_id(ids)), []).append([
            nm,
            p.get("level") or 0,
            s.get("element") or "",
            s.get("power") or "",
            s.get("cost") or "",
            eff_idx[eff],
            sid,
        ])

for lst in by_pet.values():
    lst.sort(key=lambda x: (-x[1], x[0]))

out = {
    "_source": "skillIds.json(aismile.dev)+skillGuideData.json(arkmeng.cn),"
               "均为第三方资料站的过渡方案;解出 SKILL_CONF 后应替换",
    "_note": "names: 技能 id -> 名(试炼等只给 id 的场景直接查)。"
             "pets: 按 petbase 形态 id 索引的天生技能(可换配置,非个体携带),"
             "条目是数组 [名, 学会等级, 属性, 威力, 能耗, 效果下标, 技能id];"
             "技能id=0 表示该技能在资料站是重名的(如借用有 4 个变体),无从判断是哪一个。"
             "威力/能耗是字符串,无威力者为 —",
    "effects": effects,
    "names": {str(k): v for k, v in sorted(id2name.items())},
    "pets": by_pet,
}
os.makedirs(OUT_DIR, exist_ok=True)
with open(OUT, "w", encoding="utf-8") as f:
    json.dump(out, f, ensure_ascii=False, separators=(",", ":"))

n_skills = sum(len(v) for v in by_pet.values())
print(
    f"已生成 {OUT}\n"
    f"  技能 id 映射 {len(id2name)} 条(其中重名 {len(ambiguous)} 个: "
    f"{'、'.join(sorted(ambiguous)[:5])})\n"
    f"  天生技能 {len(by_pet)} 个形态 / {n_skills} 条"
    f"({len(skills)} 个技能源, {len(skipped)} 个形态名未匹配, {no_id} 个技能无 id)"
)
if skipped:
    print("  未匹配(资料站有、我方 petbase 无同名):", "、".join(sorted(skipped)[:12]))
