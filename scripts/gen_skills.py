"""提取精灵「天生技能」表，输出到 internal/gamedata/data/skills.json。

与其余 gen_*.py 不同,本脚本的数据源**不是游戏解包**,而是第三方资料站的技能数据
(洛克万事屋 arkmeng.cn 的 skillGuideData.json,含 492 个技能的中文名/属性/威力/能耗/
效果,以及每个技能「哪些精灵天生会、几级学会」的 innatePets 清单)。

为什么要它:技能名本地化在 docs/data.md 里一直是「待校准」项 —— 游戏解包的 SKILL_CONF
(id→中文名)才是权威来源,但本机暂无解包数据,故先用第三方资料顶上。
**这是过渡方案**:等 scripts/unpack.sh 解出 SKILL_CONF 后应改走解包数据并删掉本脚本
(那时能直接给出 skill_id→name 的权威映射,无需按「形态+等级」间接组织)。

为什么不进 names.json:names.json 全部来自解包 Bin 配置,两者数据源与更新节奏都不同,
混在一起会让「这份名字是从哪来的」说不清。故独立成 skills.json。

数据组织:按 **petbase 形态 id** 索引(而非按 skill_id)——
  1. 第三方资料没有协议里的 base_skill_id(7020500 这类),只有技能中文名;
  2. 其 innatePets 给的是「形态名 + 学会等级」,正好能与我们的形态名对齐。
故反查成:  petbase_id -> [{n:技能名, lv:学会等级, e:属性, p:威力, c:能耗, d:效果}]
宠物详情页据此展示该形态的天生技能,不涉及 PetData 里「当前携带的技能」
(那些是可换配置,见 git 0762eb6 移除 Pet.SkillIDs 的理由)。

运行:  uv run python scripts/gen_skills.py
数据源: ROCOM_SKILL_GUIDE 环境变量,默认 ~/Downloads/rocom/skillGuideData.json
        获取: curl -o ~/Downloads/rocom/skillGuideData.json \
              https://arkmeng.cn/storage/files/skillGuideData.json
"""
import json
import os
import sys

SKILL_JSON = os.environ.get(
    "ROCOM_SKILL_GUIDE", os.path.expanduser("~/Downloads/rocom/skillGuideData.json")
)
NAMES = "internal/gamedata/data/names.json"
OUT_DIR = "internal/gamedata/data"
OUT = os.path.join(OUT_DIR, "skills.json")

if not os.path.exists(SKILL_JSON):
    sys.exit(
        f"缺技能数据: {SKILL_JSON}\n"
        "先获取: curl -o ~/Downloads/rocom/skillGuideData.json \\\n"
        "        https://arkmeng.cn/storage/files/skillGuideData.json\n"
        "(或设 ROCOM_SKILL_GUIDE 指向已有文件)"
    )
if not os.path.exists(NAMES):
    sys.exit(f"缺 {NAMES} —— 请先跑 scripts/gen_gamedata.py")

with open(SKILL_JSON, encoding="utf-8") as f:
    skills = json.load(f)
with open(NAMES, encoding="utf-8") as f:
    names = json.load(f)

# 形态全名(名 + 地区/季节形态) -> petbase_id 列表
name2ids = {}
for pid, v in names.get("petbase", {}).items():
    full = v["n"] + (("_" + v["f"]) if v.get("f") else "")
    name2ids.setdefault(full, []).append(int(pid))


def main_id(ids):
    """取主形态 petbase_id:优先常规区间(<1e7)里最小的那个。

    与 gen_gamedata.py 的 _real 判据同口径 —— 同名还有一批 petbase_id 落在
    1.3e7~1.9e7 的复制形态(剧情/测试/NPC 用,如「迪莫」16000004),它们不是真实
    图鉴形态,技能表不该挂在它们身上。
    """
    real = [i for i in ids if i < 10_000_000]
    return min(real or ids)


# 效果描述去重:5213 条技能里只有 364 种不同文本(「对敌方精灵造成物理伤害。」出现 473 次),
# 故抽成共享字符串池,条目里只存下标 —— 省 81 KiB。
effects, eff_idx = [], {}

# 反查: petbase_id -> 天生技能列表(按学会等级降序,同级按技能名)
by_pet = {}
skipped = set()
for s in skills:
    for p in s.get("innatePets", []):
        ids = name2ids.get(p["petName"])
        if not ids:
            skipped.add(p["petName"])
            continue
        eff = s.get("effect") or ""
        if eff not in eff_idx:
            eff_idx[eff] = len(effects)
            effects.append(eff)
        # 条目用**数组**而非对象:每条省下 6 个重复键名("n"/"lv"/…),
        # 5213 条累计省 400+ KiB —— 生成物不求可读,体积优先。
        entry = [
            s["name"],
            p.get("level") or 0,
            s.get("element") or "",
            s.get("power") or "",
            s.get("cost") or "",
            eff_idx[eff],
        ]
        by_pet.setdefault(str(main_id(ids)), []).append(entry)

for lst in by_pet.values():
    lst.sort(key=lambda x: (-x[1], x[0]))

out = {
    "_source": "arkmeng.cn skillGuideData.json(第三方资料站,过渡方案;解出 SKILL_CONF 后应替换)",
    "_note": "按 petbase 形态 id 索引的天生技能(可换配置,非个体携带)。"
    "条目是数组:[名, 学会等级, 属性, 威力, 能耗, 效果下标];效果下标查 effects 池。"
    "威力/能耗是字符串,无威力者为 —",
    "effects": effects,
    "pets": by_pet,
}
os.makedirs(OUT_DIR, exist_ok=True)
with open(OUT, "w", encoding="utf-8") as f:
    json.dump(out, f, ensure_ascii=False, separators=(",", ":"))

n_skills = sum(len(v) for v in by_pet.values())
print(
    f"已生成 {OUT}: {len(by_pet)} 个形态 / {n_skills} 条技能 "
    f"({len(skills)} 个技能源, {len(skipped)} 个形态名未匹配到 petbase)"
)
if skipped:
    print("  未匹配(第三方资料有、我方 petbase 无同名):", "、".join(sorted(skipped)[:12]))
