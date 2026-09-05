#!/bin/sh
# 容器入口:必要的启动参数缺失时给一句人话,再 exec 真正的二进制。
#
# 为什么需要它:rocom-capture 在既不给 -iface 也不给 -pcap 时只会打印一行用法后退出,
# 而容器里 `docker run` 完立刻退出、日志里孤零零一行,很容易被当成「镜像坏了」。
# 这里把「必须二选一」这条约束显式说出来,并给出可直接复制的命令。
#
# 只做参数校验,不搬运任何 flag —— 参数仍是 rocom-capture 原样,
# 免得容器里多一层与二进制不同步的封装(新增启动项时不必改这里)。

set -e

have_mode=0
for a in "$@"; do
    # 覆盖 -iface x / -iface=x / --iface x / --iface=x 四种写法
    case "$a" in
        -iface | --iface | -iface=* | --iface=* | \
        -pcap | --pcap | -pcap=* | --pcap=*)
            have_mode=1
            ;;
    esac
done

if [ "$have_mode" -eq 0 ]; then
    echo "rocom-capture: 必须指定 -iface 或 -pcap 之一" >&2
    echo "" >&2
    echo "  实时抓包: -iface <网卡名>       (需 --cap-add=NET_ADMIN --cap-add=NET_RAW)" >&2
    echo "  离线回放: -pcap <pcap文件>" >&2
    echo "" >&2
    echo "例:" >&2
    echo "  docker run --cap-add=NET_ADMIN --cap-add=NET_RAW \\" >&2
    echo "    -v rocom-data:/data -p 4939:4939 \\" >&2
    echo "    rocom-capture -iface eth0 -db /data/rocom.db" >&2
    exit 1
fi

exec rocom-capture "$@"
