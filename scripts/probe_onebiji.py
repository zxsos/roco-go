"""探测 onebiji(快爆工具箱)远行商人页面的档期语义。

验证目的:判断这个源能不能替代/补充我们现用的 xianyuw API。

现行方案(internal/server/merchant.go)的痛点是**第三方缓存滞后** ——
轮次整点(8/12/16/20)切换后,货单要滞后 6~56 分钟才从上一轮切成新轮
(实测见 AI_merchant_probe.md),故整点后得每 10 秒回源狂砸。

onebiji 这个源的结构不同(参考 kanzakine/Roco-API 的解析):
页面上每档商品都带 `data-time` = **该档期的结束时间(Unix 秒)**,
且一次给出全天四档。若它在营业时段就能给出「当前进行中的这一档」,
则按时间戳直接取值即可,滞后问题从根上消失。

## 两种模式

**--watch(推荐,可提前跑)**:连续采样,监视页面**何时切换到新的一天**。
不必等到 8 点开市 —— 关键要区分的是两种截然不同的行为:

  A 按天前瞻:8:00 之前页面就换成了「今天全天四档」
             → 开市即有数据,**滞后问题可绕开**
  B 回顾完整营业日:页面始终显示「最近一个已结束的营业日」
             → 8:00 时还显示昨天,要到今天结束才切,**无用**

两者的区别在**日切时刻**就会暴露,而现在(凌晨)正好能观察到:
页面此时显示 09-01,盯着它何时变成 09-02 即可判定。

**默认(单次)**:抓一次并按「当前时间是否落在某一档内」判定。
⚠️ 休市时段(0:00-8:00)抓到的恒为「昨天全天」,判定必然是 ❌ ——
那不是失败,是抓早了。此模式要在营业时段(8:00 后)跑才有意义。

用法:
  uv run python scripts/probe_onebiji.py --watch                 # 每 5 分钟采样,一直跑
  uv run python scripts/probe_onebiji.py --watch --interval 10   # 10 分钟一次
  uv run python scripts/probe_onebiji.py --watch --max-runs 3    # 只采 3 次(试跑)
  uv run python scripts/probe_onebiji.py                         # 单次判定(8 点后跑)

日志同时写入 ~/probe_onebiji.log,便于事后回看。

结论出来后本脚本与日志一并删除,或转正为正式的抓取脚本。

📄 **完整背景与判定标准见 `docs/merchant-onebiji-probe.md`** ——
那份文档是给下一个接手的人/AI 的交接说明:为什么要做、现状如何、
怎么解读结果、结论出来后怎么接。改本脚本前请先读它。
"""
import argparse
import datetime
import os
import re
import sys
import time
import urllib.request

URL = "https://www.onebiji.com/hykb_tools/comm/lkwgmerchant/preview.php?id=1&immgj=0"
SLOT = datetime.timedelta(hours=4)      # 每档 4 小时
TZ = datetime.timezone(datetime.timedelta(hours=8))  # 游戏内档期按北京时间
UA = {"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120"}
LOG = os.path.expanduser("~/probe_onebiji.log")

RE_BLOCK = re.compile(r'<[^>]*class="all_show[^"]*"[^>]*data-time="(\d+)"[^>]*>(.*?)</li>', re.S)
RE_NAME = re.compile(r'class="shop_name[^"]*"[^>]*>([^<]+)<')
RE_PRICE = re.compile(r'class="shop_price[^"]*"[^>]*>([^<]+)<')
RE_LIMIT = re.compile(r'<em>([^<]*)</em>')


def log(msg=""):
    """同时打印到终端与日志(日志用于事后回看,终端用于当场观察)。"""
    print(msg, flush=True)
    try:
        os.makedirs(os.path.dirname(LOG), exist_ok=True)
        with open(LOG, "a", encoding="utf-8") as f:
            f.write(msg + "\n")
    except OSError:
        pass


def fetch():
    req = urllib.request.Request(URL, headers=UA)
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode("utf-8", "replace")


def parse(html):
    """返回 [(end_dt, name, price, limit)],按档期结束时间排序。"""
    out = []
    for ts, body in RE_BLOCK.findall(html):
        nm = RE_NAME.search(body)
        if not nm:
            continue
        pr = RE_PRICE.search(body)
        lm = RE_LIMIT.search(body)
        end = datetime.datetime.fromtimestamp(int(ts), TZ)
        out.append((
            end,
            nm.group(1).strip(),
            pr.group(1).replace("价格：", "").strip() if pr else "",
            lm.group(1).strip() if lm else "",
        ))
    return sorted(out)


def group(items):
    """按档期分组:{end_dt: [(name, price, limit), ...]}"""
    g = {}
    for end, nm, pr, lm in items:
        g.setdefault(end, []).append((nm, pr, lm))
    return g


def signature(items):
    """档期集合的可比较签名 —— 只看「有哪些档」,不看档里有什么货。

    用结束时间戳而非商品内容:要监视的是**页面换没换天**,
    商品微调(如价格改一两个)不该触发报警,否则日志全是噪声。
    """
    return tuple(sorted({end for end, _, _, _ in items}))


def fmt_slots(g):
    return ", ".join(f"{(e - SLOT):%m-%d %H:%M}~{e:%H:%M}" for e in sorted(g))


def report(now, items):
    """打印一份完整快照 + 当前档判定。"""
    g = group(items)
    log(f"\n===== 抓取时刻 {now:%Y-%m-%d %H:%M:%S} (UTC+8) =====")
    cur = None
    for end in sorted(g):
        start = end - SLOT
        if start <= now < end:
            state, cur = ">>> 进行中 <<<", end
        elif end <= now:
            state = "已结束"
        else:
            state = "即将开始"
        log(f"\n[{start:%H:%M} - {end:%H:%M}]  {state}  {len(g[end])} 件")
        for nm, pr, lm in g[end]:
            log(f"    {nm:<14} {pr:<12} {lm}")

    log("\n----- 判定 -----")
    if cur:
        log(f"OK  页面**包含当前进行中的档期** ({cur - SLOT:%H:%M}-{cur:%H:%M}),"
            f"共 {len(g[cur])} 件")
        log("    → 可按 data-time 直接取值,不必等第三方缓存切换;滞后问题可绕开。")
    else:
        log("NO  页面**不含当前进行中的档期**")
        log("    → 单次抓无法定论:休市时段(0:00-8:00)本就恒为「昨天全天」。")
        log("      结论要看 --watch 监视到的**换天时刻**。")
    return cur


def watch(interval_min, max_runs):
    """连续采样,监视页面何时切换到新的一天。"""
    log(f"\n### 进入监视模式:每 {interval_min} 分钟采样一次"
        + (f",共 {max_runs} 次" if max_runs else ",Ctrl+C 退出"))
    log("### 只看「档期集合」变化(换天);商品微调不报警。\n")

    prev_sig, prev_g = None, {}
    runs = 0
    try:
        while max_runs is None or runs < max_runs:
            now = datetime.datetime.now(TZ)
            try:
                items = parse(fetch())
            except Exception as e:
                log(f"[{now:%H:%M:%S}] 抓取失败: {e}")
                prev_sig = None  # 失败后下次强制打印快照,免得漏掉期间的变化
            else:
                if not items:
                    log(f"[{now:%H:%M:%S}] 解析到 0 件商品 —— 站点结构变了或该时段无数据")
                    prev_sig = None
                else:
                    g, sig = group(items), signature(items)
                    if sig != prev_sig:
                        if prev_sig is None:
                            log(f"[{now:%H:%M:%S}] 初始快照  档期: {fmt_slots(g)}")
                        else:
                            # —— 这就是要等的那一刻 ——
                            log(f"[{now:%H:%M:%S}]  *** 档期集合变化 ***")
                            log(f"      旧: {fmt_slots(prev_g)}")
                            log(f"      新: {fmt_slots(g)}")
                            newest = max(g)
                            log(f"      最新档期结束于 {newest:%m-%d %H:%M},"
                                f"距现在 {(newest - now).total_seconds() / 3600:+.1f} 小时")
                            log(f"      → {'页面已切到新的一天(A 按天前瞻,可绕开滞后)'
                                       if newest > max(prev_g) else '档期回退了,请人工看一眼'}")
                        for end in sorted(g):
                            names = ", ".join(n for n, _, _ in g[end])
                            log(f"          {fmt_slots({end: 1})}  {names}")
                        prev_sig, prev_g = sig, g
                    else:
                        log(f"[{now:%H:%M:%S}] 无变化  ({fmt_slots(g)})")
            runs += 1
            if max_runs is None or runs < max_runs:
                time.sleep(interval_min * 60)
    except KeyboardInterrupt:
        log("\n### 已停止(用户中断)")
    log(f"### 共采样 {runs} 次")


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--watch", action="store_true", help="连续采样监视换天(可提前跑)")
    ap.add_argument("--interval", type=float, default=5, help="采样间隔(分钟),默认 5")
    ap.add_argument("--max-runs", type=int, default=None, help="最多采样几次(不填=一直跑)")
    a = ap.parse_args()

    now = datetime.datetime.now(TZ)
    if a.watch:
        watch(a.interval, a.max_runs)
        return 0
    try:
        items = parse(fetch())
    except Exception as e:
        log(f"抓取失败: {e}")
        return 1
    if not items:
        log("!! 没有解析到任何商品 —— 站点结构变了(选择器失效)或该时段无数据")
        return 1
    report(now, items)
    return 0


if __name__ == "__main__":
    sys.exit(main())
