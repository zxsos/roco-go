#!/usr/bin/env bash
#
# deploy.sh — 在 Linux 网关上部署/更新 rocom-capture,数据与程序分离,更新不丢历史。
#
# 数据(rocom.db / TLS 证书)放在独立的 /var/lib/rocom/ 下,程序放在 /opt/rocom/ 下。
# 更新时只替换二进制并重启服务,数据库与证书不受影响——会话密钥/归属/场景全部预热恢复,
# 宠物/事件/涂地等历史统计原样保留。
#
# 用法:
#   sudo ./deploy.sh                  # 首次安装或更新二进制(自动判断)
#   sudo ./deploy.sh --archive x.tar  # 从 tar 包安装(内含 rocom-capture 单文件)
#   sudo ./deploy.sh --stop           # 仅停止服务(不删除数据)
#   sudo ./deploy.sh --backup         # 备份数据库到 /var/lib/rocom/backup/
#   sudo ./deploy.sh --uninstall      # 卸载程序(保留数据;加 --purge 同时删数据)
#
# 环境变量(在调用前 export,或写入 /etc/rocom.env):
#   ROCOM_IFACE       抓包网卡(必填,如 eth0 / br-lan)
#   ROCOM_PORT        游戏端口(默认 8195)
#   ROCOM_ADDR        Web 监听地址(默认 :4939)
#   ROCOM_TLS         启用 HTTPS(设 1 启用)
#   ROCOM_SOCKS5_ADDR SOCKS5 监听地址(如 :1080;空=不启用)
#   ROCOM_SOCKS5_ALLOW SOCKS5 客户端白名单(逗号分隔 IP/CIDR)
#   ROCOM_SOCKS5_USER / ROCOM_SOCKS5_PASS  SOCKS5 认证
#   ROCOM_SKIP_SELF_IP  socks5 模式下设 false(默认 true)
#   ROCOM_EXTRA       其他要透传的参数(如 -ignore-ip)
#
set -euo pipefail

# ---- 目录约定 ----
INSTALL_DIR="/opt/rocom"
DATA_DIR="/var/lib/rocom"
BACKUP_DIR="$DATA_DIR/backup"
SERVICE_NAME="rocom"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
ENV_FILE="/etc/rocom.env"

BIN_NAME="rocom-capture"
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *) echo "不支持的架构: $ARCH" >&2; exit 1 ;;
esac

# ---- 参数 ----
ACTION="install"
ARCHIVE=""
PURGE=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --archive)  ARCHIVE="$2"; ACTION="install"; shift 2 ;;
        --stop)     ACTION="stop"; shift ;;
        --backup)   ACTION="backup"; shift ;;
        --uninstall) ACTION="uninstall"; shift ;;
        --purge)    PURGE=1; shift ;;
        -h|--help)  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *) echo "未知参数: $1" >&2; exit 1 ;;
    esac
done

# ---- 必须 root ----
if [[ "$(id -u)" -ne 0 ]]; then
    echo "错误: 需要 root 权限,请用 sudo 运行。" >&2
    exit 1
fi

# ---- 确定二进制来源 ----
find_binary() {
    # 1) --archive 指定的 tar 包
    if [[ -n "$ARCHIVE" ]]; then
        local tmp; tmp="$(mktemp -d)"
        tar -xf "$ARCHIVE" -C "$tmp"
        local found
        found="$(find "$tmp" -name "${BIN_NAME}-linux-${ARCH}" -o -name "$BIN_NAME" | head -1 || true)"
        if [[ -z "$found" ]]; then
            echo "错误: tar 包内未找到 $BIN_NAME (linux-$ARCH)" >&2
            rm -rf "$tmp"
            exit 1
        fi
        echo "$found"
        return
    fi
    # 2) dist/ 构建产物
    local dist="./dist/${BIN_NAME}-linux-${ARCH}"
    if [[ -f "$dist" ]]; then
        echo "$dist"
        return
    fi
    # 3) 当前目录
    if [[ -f "./$BIN_NAME" ]]; then
        echo "./$BIN_NAME"
        return
    fi
    echo "错误: 未找到二进制。请先 make release,或用 --archive 指定 tar 包。" >&2
    exit 1
}

# ---- 生成 systemd service 文件 ----
write_service() {
    cat > "$SERVICE_FILE" <<'EOF'
[Unit]
Description=rocom-capture (游戏流量抓包与统计)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
# 环境变量从 /etc/rocom.env 读取(IFACE / SOCKS5 等,见 deploy.sh 注释)
EnvironmentFile=-/etc/rocom.env
# 数据库与证书放在 /var/lib/rocom 下,更新二进制不动数据
ExecStart=/opt/rocom/rocom-capture \
    -db /var/lib/rocom/rocom.db \
    -cert /var/lib/rocom/rocom-cert.pem \
    -key /var/lib/rocom/rocom-key.pem \
    -iface ${ROCOM_IFACE} \
    -port ${ROCOM_PORT:-8195} \
    -addr ${ROCOM_ADDR:-:4939} \
    ${ROCOM_TLS:+-tls} \
    ${ROCOM_SOCKS5_ADDR:+-socks5-addr ${ROCOM_SOCKS5_ADDR} -skip-self-ip=${ROCOM_SKIP_SELF_IP:-false}} \
    ${ROCOM_SOCKS5_ALLOW:+-socks5-allow ${ROCOM_SOCKS5_ALLOW}} \
    ${ROCOM_SOCKS5_USER:+-socks5-user ${ROCOM_SOCKS5_USER} -socks5-pass ${ROCOM_SOCKS5_PASS}} \
    ${ROCOM_EXTRA}
# 抓包需要 root(afpacket);如用 pcap 模式可改为专用用户
User=root
# 崩溃自动重启
Restart=on-failure
RestartSec=2
# 数据目录由 systemd 创建
StateDirectory=rocom
# 日志走 journald
StandardOutput=journal
StandardError=journal
SyslogIdentifier=rocom

[Install]
WantedBy=multi-user.target
EOF
    echo "已写入 $SERVICE_FILE"
}

# ---- 生成环境变量文件 ----
write_env() {
    if [[ -f "$ENV_FILE" ]]; then
        echo "$ENV_FILE 已存在,保留现有配置(如需更新请手动编辑)"
        return
    fi
    cat > "$ENV_FILE" <<EOF
# rocom-capture 运行参数(改后执行: systemctl restart rocom)
# 抓包网卡(必填)
ROCOM_IFACE=
# 游戏端口(默认 8195)
ROCOM_PORT=8195
# Web 监听地址(默认 :4939)
ROCOM_ADDR=:4939
# 启用 HTTPS(设 1 启用,首次自动生成自签证书)
ROCOM_TLS=
# SOCKS5 代理(云端部署时用;留空=不启用)
ROCOM_SOCKS5_ADDR=
ROCOM_SOCKS5_ALLOW=
ROCOM_SOCKS5_USER=
ROCOM_SOCKS5_PASS=
ROCOM_SKIP_SELF_IP=false
# 其他透传参数(如 -ignore-ip 10.0.0.1)
ROCOM_EXTRA=
EOF
    chmod 600 "$ENV_FILE"
    echo "已写入 $ENV_FILE —— 请编辑填入 ROCOM_IFACE 等参数后执行: systemctl start rocom"
}

# ---- 主流程 ----
case "$ACTION" in
    install)
        SRC_BIN="$(find_binary)"
        mkdir -p "$INSTALL_DIR" "$DATA_DIR" "$BACKUP_DIR"

        # 停旧服务(若在运行)
        if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
            echo "停止旧服务..."
            systemctl stop "$SERVICE_NAME"
        fi

        # 替换二进制
        echo "安装二进制: $SRC_BIN → $INSTALL_DIR/$BIN_NAME"
        cp -f "$SRC_BIN" "$INSTALL_DIR/$BIN_NAME"
        chmod +x "$INSTALL_DIR/$BIN_NAME"

        # 写 service 与 env
        write_service
        write_env

        systemctl daemon-reload
        systemctl enable "$SERVICE_NAME" 2>/dev/null || true

        # 如果 env 已配置了 IFACE,直接启动;否则提示用户编辑
        if grep -qP '^\s*ROCOM_IFACE=\S' "$ENV_FILE"; then
            echo "启动服务..."
            systemctl restart "$SERVICE_NAME"
            sleep 1
            systemctl status "$SERVICE_NAME" --no-pager || true
            echo "==> 部署完成。日志: journalctl -u rocom -f"
        else
            echo "==> 二进制已就位。请编辑 $ENV_FILE 填入 ROCOM_IFACE 后执行:"
            echo "      systemctl start rocom"
        fi
        ;;

    stop)
        systemctl stop "$SERVICE_NAME" 2>/dev/null || true
        echo "服务已停止(数据保留在 $DATA_DIR)"
        ;;

    backup)
        if [[ ! -f "$DATA_DIR/rocom.db" ]]; then
            echo "数据库不存在: $DATA_DIR/rocom.db" >&2
            exit 1
        fi
        TS="$(date +%Y%m%d-%H%M%S)"
        OUT="$BACKUP_DIR/rocom.db.$TS"
        # 用 sqlite3 的 backup API 做热备份(不锁库);无 sqlite3 命令则直接 cp
        if command -v sqlite3 >/dev/null 2>&1; then
            sqlite3 "$DATA_DIR/rocom.db" ".backup '$OUT'"
        else
            cp "$DATA_DIR/rocom.db" "$OUT"
        fi
        echo "已备份: $OUT ($(du -h "$OUT" | cut -f1))"
        # 保留最近 10 份
        ls -t "$BACKUP_DIR"/rocom.db.* 2>/dev/null | tail -n +11 | xargs -r rm --
        ;;

    uninstall)
        systemctl stop "$SERVICE_NAME" 2>/dev/null || true
        systemctl disable "$SERVICE_NAME" 2>/dev/null || true
        rm -f "$SERVICE_FILE" "$ENV_FILE"
        rm -rf "$INSTALL_DIR"
        systemctl daemon-reload
        if [[ "$PURGE" -eq 1 ]]; then
            rm -rf "$DATA_DIR"
            echo "已卸载程序并清除数据($DATA_DIR)"
        else
            echo "已卸载程序,数据保留在 $DATA_DIR(加 --purge 可同时删除)"
        fi
        ;;
esac
