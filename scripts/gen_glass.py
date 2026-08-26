#!/usr/bin/env python3
"""生成炫彩外观色卡图片(scripts/gen_glass.py)。

输入(均在仓库内,无需解包数据):
  - internal/gamedata/data/COLOR_RANDOM_CONF.ts    39 个配色(ui_color_1/2)
  - internal/gamedata/data/PARTICLE_RANDOM_CONF.ts  4 个粒子(particle_big_icon)
  - internal/gamedata/data/img/dazzling/*.png       底图 Bg/Bg2、粒子大图、隐藏炫彩整图

输出:
  - img/dazzling/glass_<type>_<value>.webp(280x154)
      普通炫彩 type=1: value = (粒子id << 20) | 配色id(见 internal/gamedata/pet.go glassParticleShift)
      隐藏炫彩 type=2: value = HIDDEN_GLASS_CONF.id(1/2/3 赛季,1000 黑白)
  - names.json 追加 glass_chips 索引: "type_value" -> "dazzling/glass_<type>_<value>.webp"

合成算法(与客户端 UMG 一致,见 docs/data.md 3.5):
  层1 底图 Bg(280x154) 白色区填 ui_color_2
  层2 中层 Bg2(280x108) 白色区填 ui_color_1,顶部对齐(同宽)
  层3 顶层 粒子大图(280x154) 非透明区染白
  层1+层2 简单相加(RGB 逐通道加,clamp 255),再叠加层3。

用法:
  uv run python scripts/gen_glass.py
"""

import json
import re
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
DATA = ROOT / "internal" / "gamedata" / "data"
DAZZ = DATA / "img" / "dazzling"
NAMES = DATA / "names.json"

SIZE = (280, 154)

# 隐藏炫彩:HIDDEN_GLASS_CONF.id -> 整图资产名(图片按语义重命名过,按前缀匹配)。
# 1/2/3 = 第 1/2/3 赛季炫彩,1000 = 黑白。
HIDDEN_GLASS = {
    1: "img_dazzling_Bg7_png",    # 暗夜拾光(s1赛季炫彩)
    2: "img_dazzling_Bg9_png",    # 狂欢怪谈(s2赛季炫彩)
    3: "img_dazzling_Bg11_png",   # 铅字幻梦(s3赛季炫彩)
    1000: "img_dazzling_Bg8_png",  # 黑白
}

# 普通炫彩底图: Bg 为底(280x154),Bg2 为中层(280x108,顶部对齐,宽度相同)。
BG = "img_dazzling_Bg_png"
BG2 = "img_dazzling_Bg2_png"


def find_png(asset: str) -> Path:
    """按资产名前缀在 dazzling 目录找 png(用户重命名加过语义后缀)。"""
    for f in DAZZ.glob(asset + "*.png"):
        return f
    raise FileNotFoundError(f"dazzling 目录缺 {asset}*.png")


def fill_color(im: Image.Image, color: tuple) -> Image.Image:
    """白色形状图填指定颜色:RGB 通道整体替换,保留 alpha。
    素材是「纯白+透明」(黑像素仅抗锯齿边),黑色过渡像素一并填成低 alpha 颜色,无视觉影响。"""
    im = im.convert("RGBA")
    r, g, b, a = im.split()
    r = r.point(lambda v: color[0])
    g = g.point(lambda v: color[1])
    b = b.point(lambda v: color[2])
    return Image.merge("RGBA", (r, g, b, a))


def render_common(particle_file: Path, c1: tuple, c2: tuple) -> Image.Image:
    """合成普通炫彩:Bg 填 c2 打底,Bg2 填 c1 直接覆盖其上(上边缘对齐),粒子染白叠加最上层。"""
    bg = fill_color(Image.open(find_png(BG)), c2)   # 280x154 底图
    bg2 = fill_color(Image.open(find_png(BG2)), c1)  # 280x108 中层
    base = bg.copy()
    base.alpha_composite(bg2, (0, 0))  # 直接覆盖,上边缘对齐(宽相同,水平无偏移)
    top = fill_color(Image.open(particle_file), (255, 255, 255))
    return Image.alpha_composite(base, top)


def hex_rgb(s: str) -> tuple:
    return (int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16))


def parse_colors() -> dict:
    """解析 COLOR_RANDOM_CONF.ts:id -> (ui_color_1, ui_color_2)。"""
    text = (DATA / "COLOR_RANDOM_CONF.ts").read_text()
    colors = {}
    for m in re.finditer(
        r'"id":\s*(\d+).*?"ui_color_1":\s*"#([0-9a-fA-F]{6})".*?"ui_color_2":\s*"#([0-9a-fA-F]{6})"',
        text,
        re.S,
    ):
        cid = int(m.group(1))
        colors[cid] = (hex_rgb(m.group(2)), hex_rgb(m.group(3)))
    return colors


def parse_particles() -> dict:
    """解析 PARTICLE_RANDOM_CONF.ts:id -> particle_big_icon 资产名(如 img_dazzling_Bg3_png)。"""
    text = (DATA / "PARTICLE_RANDOM_CONF.ts").read_text()
    particles = {}
    for m in re.finditer(
        r'"id":\s*(\d+).*?"particle_big_icon":\s*"[^"]*(img_dazzling_[A-Za-z0-9_]+)[^"]*"',
        text,
        re.S,
    ):
        particles[int(m.group(1))] = m.group(2)
    return particles


def update_names(chips: dict) -> None:
    """往 names.json 追加 glass_chips 索引(保留其他字段与紧凑格式)。"""
    names = json.loads(NAMES.read_text())
    names["glass_chips"] = chips
    NAMES.write_text(json.dumps(names, ensure_ascii=False, separators=(",", ":")) + "\n")


def main() -> None:
    colors = parse_colors()
    particles = parse_particles()
    if not colors or not particles:
        raise SystemExit(f"解析 ts 失败:colors={len(colors)} particles={len(particles)}")

    chips = {}
    # 普通炫彩:4 粒子 x 39 配色
    for pid, asset in particles.items():
        particle = find_png(asset)
        for cid, (c1, c2) in colors.items():
            value = (pid << 20) | cid
            out = render_common(particle, c1, c2)
            out.save(DAZZ / f"glass_1_{value}.webp", "WEBP", quality=92, method=6)
            chips[f"1_{value}"] = f"dazzling/glass_1_{value}.webp"
    # 隐藏炫彩:整图直接转码(统一缩放到 280x154)
    for gid, asset in HIDDEN_GLASS.items():
        im = Image.open(find_png(asset)).convert("RGBA").resize(SIZE, Image.LANCZOS)
        im.save(DAZZ / f"glass_2_{gid}.webp", "WEBP", quality=92, method=6)
        chips[f"2_{gid}"] = f"dazzling/glass_2_{gid}.webp"

    update_names(chips)
    print(f"已生成 {len(chips)} 张色卡(普通 {len(colors) * len(particles)} + 隐藏 {len(HIDDEN_GLASS)}),"
          f"glass_chips 索引 -> names.json")


if __name__ == "__main__":
    main()
