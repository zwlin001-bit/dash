#!/bin/sh
set -e

# ==============================================================================
# dashd 一键部署与管理脚本 (POSIX sh 兼容)
# 支持: install / upgrade / uninstall / status
# ==============================================================================

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)

usage() {
    cat <<EOF
Usage: $0 <command> [options]

Commands:
  install     Install and bootstrap dashd
  upgrade     Upgrade dashd binary with automatic rollback
  uninstall   Uninstall dashd service and binary
  status      Show current status of dashd

Options for 'install':
  --domain <domain>           Domain for HTTPS/ACME (required in non-interactive mode)
  --db-driver <driver>        Database driver: oracle (default) or mysql
  --db-user <user>            Database user (default: admin)
  --db-password <password>    Database password (or DASH_DB_PASSWORD env)
  --db-dsn <dsn>              Database connection DSN (or DASH_DB_DSN env)
  --server-listen <listen>    Listen address for HTTPS (default: :443)
  --server-listen-acme <addr> Listen address for ACME HTTP-01 (default: :80)
  --data-dir <path>           Data directory (default: /var/lib/dash)
  --admin-user <name>         Initial admin username (default: admin)
  --admin-password <pass>     Initial admin password (optional)
  --yes, -y                   Non-interactive mode (use defaults)

Options for 'upgrade':
  --binary <path>             Path to new dashd binary (default: ./bin/dashd)

Options for 'uninstall':
  --purge                     Also remove /etc/dash and /var/lib/dash (database is NEVER touched)
EOF
    exit 1
}

check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        echo "❌ 错误: 请使用 root 用户或 sudo 运行此脚本。" >&2
        exit 1
    fi
}

detect_init() {
    if [ -d /run/systemd/system ] || command -v systemctl >/dev/null 2>&1; then
        INIT_SYSTEM="systemd"
    elif [ -f /sbin/openrc-run ] || command -v rc-service >/dev/null 2>&1; then
        INIT_SYSTEM="openrc"
    else
        INIT_SYSTEM="unknown"
    fi
}

detect_distro() {
    if [ -f /etc/alpine-release ]; then
        DISTRO="alpine"
    elif [ -f /etc/debian_version ]; then
        DISTRO="debian"
    else
        DISTRO="linux"
    fi
}

ensure_dependencies() {
    detect_distro
    if ! command -v setcap >/dev/null 2>&1; then
        echo "--> 正在安装 setcap 工具..."
        if [ "$DISTRO" = "alpine" ]; then
            apk add --no-cache libcap
        elif command -v apt-get >/dev/null 2>&1; then
            apt-get update -qq && apt-get install -y -qq libcap2-bin
        else
            echo "❌ 警告: 未找到 setcap，请先手动安装 libcap 工具。" >&2
        fi
    fi

    if ! command -v curl >/dev/null 2>&1; then
        echo "--> 正在安装 curl 工具..."
        if [ "$DISTRO" = "alpine" ]; then
            apk add --no-cache curl
        elif command -v apt-get >/dev/null 2>&1; then
            apt-get update -qq && apt-get install -y -qq curl
        fi
    fi
}

ensure_user() {
    if ! id dashd >/dev/null 2>&1; then
        echo "--> 创建系统用户 dashd..."
        detect_distro
        if [ "$DISTRO" = "alpine" ]; then
            adduser -S -D -H -h /var/lib/dash -s /sbin/nologin dashd 2>/dev/null || adduser -S -D dashd
        else
            useradd -r -s /usr/sbin/nologin -d /var/lib/dash -M dashd 2>/dev/null || \
            useradd -r -s /bin/false -d /var/lib/dash dashd 2>/dev/null || \
            adduser -S -D dashd 2>/dev/null || \
            adduser --system --no-create-home dashd 2>/dev/null || true
        fi
    fi
}

extract_json_val() {
    printf '%s' "$1" | sed -n 's/.*"'"$2"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

extract_json_int() {
    printf '%s' "$1" | sed -n 's/.*"'"$2"'"[[:space:]]*:[[:space:]]*\([0-9]*\).*/\1/p'
}

cmd_install() {
    check_root

    DOMAIN=""
    DB_DRIVER="${DASH_DB_DRIVER:-oracle}"
    DB_USER="${DASH_DB_USER:-admin}"
    DB_PASSWORD="${DASH_DB_PASSWORD:-}"
    DB_DSN="${DASH_DB_DSN:-}"
    SERVER_LISTEN="${DASH_SERVER_LISTEN:-:443}"
    SERVER_LISTEN_ACME="${DASH_SERVER_LISTEN_ACME:-:80}"
    DATA_DIR="${DASH_SERVER_DATA_DIR:-/var/lib/dash}"
    ADMIN_USER="admin"
    ADMIN_PASSWORD=""
    NON_INTERACTIVE=0

    while [ $# -gt 0 ]; do
        case "$1" in
            --domain)
                DOMAIN="$2"
                shift 2
                ;;
            --db-driver)
                DB_DRIVER="$2"
                shift 2
                ;;
            --db-user)
                DB_USER="$2"
                shift 2
                ;;
            --db-password)
                DB_PASSWORD="$2"
                shift 2
                ;;
            --db-dsn)
                DB_DSN="$2"
                shift 2
                ;;
            --server-listen)
                SERVER_LISTEN="$2"
                shift 2
                ;;
            --server-listen-acme)
                SERVER_LISTEN_ACME="$2"
                shift 2
                ;;
            --data-dir)
                DATA_DIR="$2"
                shift 2
                ;;
            --admin-user)
                ADMIN_USER="$2"
                shift 2
                ;;
            --admin-password)
                ADMIN_PASSWORD="$2"
                shift 2
                ;;
            --yes|-y)
                NON_INTERACTIVE=1
                shift
                ;;
            *)
                echo "未知参数: $1" >&2
                usage
                ;;
        esac
    done

    # 域名交互输入 (P1-19 约束：交互只有一个必填项：域名。其余全部有合理默认值)
    if [ -z "$DOMAIN" ]; then
        if [ "$NON_INTERACTIVE" -eq 1 ] || [ ! -t 0 ]; then
            echo "❌ 错误: 非交互模式下必须通过 --domain 指定访问域名。" >&2
            exit 1
        fi
        printf "请输入访问域名 (如 dash.example.com): "
        read -r DOMAIN
        if [ -z "$DOMAIN" ]; then
            echo "❌ 错误: 域名不能为空。" >&2
            exit 1
        fi
    fi

    echo "==> [1/7] 检查系统环境与依赖..."
    ensure_dependencies
    detect_init

    echo "==> [2/7] 确保系统用户与目录权限..."
    ensure_user
    mkdir -p /etc/dash
    chmod 0750 /etc/dash
    chown root:dashd /etc/dash

    mkdir -p "$DATA_DIR"
    chmod 0750 "$DATA_DIR"
    chown dashd:dashd "$DATA_DIR"

    mkdir -p "$DATA_DIR/certs"
    chmod 0700 "$DATA_DIR/certs"
    chown dashd:dashd "$DATA_DIR/certs"

    mkdir -p "$DATA_DIR/providers"
    chmod 0750 "$DATA_DIR/providers"
    chown dashd:dashd "$DATA_DIR/providers"

    # 生成主密钥 master.key (0400 dashd:dashd，若已存在则保持不动)
    if [ ! -f /etc/dash/master.key ]; then
        echo "--> 生成凭据加密主密钥 /etc/dash/master.key..."
        head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > /etc/dash/master.key
        chmod 0400 /etc/dash/master.key
        chown dashd:dashd /etc/dash/master.key
    fi

    # 生成自举配置文件 /etc/dash/config.toml (0600 dashd:dashd)
    if [ ! -f /etc/dash/config.toml ]; then
        echo "--> 生成自举配置文件 /etc/dash/config.toml..."
        CFG_PASSWORD="$DB_PASSWORD"
        if [ -z "$CFG_PASSWORD" ]; then
            CFG_PASSWORD="\${DASH_DB_PASSWORD}"
        fi
        cat > /etc/dash/config.toml <<CFG_EOF
[db]
driver = "$DB_DRIVER"
user = "$DB_USER"
password = "$CFG_PASSWORD"
dsn = "$DB_DSN"
wallet_path = ""
max_open_conns = 20
max_idle_conns = 10
conn_max_lifetime_s = 1800

[server]
listen = "$SERVER_LISTEN"
listen_acme = "$SERVER_LISTEN_ACME"
data_dir = "$DATA_DIR"
master_key = "/etc/dash/master.key"
CFG_EOF
        chmod 0600 /etc/dash/config.toml
        chown dashd:dashd /etc/dash/config.toml
    fi

    echo "==> [3/7] 准备可执行程序..."
    BIN_SRC=""
    if [ -f "$SCRIPT_DIR/bin/dashd" ]; then
        BIN_SRC="$SCRIPT_DIR/bin/dashd"
    elif [ -f "./bin/dashd" ]; then
        BIN_SRC="./bin/dashd"
    elif command -v go >/dev/null 2>&1 && [ -d "$SCRIPT_DIR/cmd/dashd" ]; then
        echo "--> 正在编译 dashd 二进制..."
        (cd "$SCRIPT_DIR" && CGO_ENABLED=0 go build -trimpath -o bin/dashd ./cmd/dashd)
        BIN_SRC="$SCRIPT_DIR/bin/dashd"
    elif [ -f /usr/local/bin/dashd ]; then
        BIN_SRC="/usr/local/bin/dashd"
    fi

    if [ -z "$BIN_SRC" ] || [ ! -f "$BIN_SRC" ]; then
        echo "❌ 错误: 未找到可执行文件 bin/dashd 且无法自动编译。" >&2
        exit 1
    fi

    if [ "$BIN_SRC" != "/usr/local/bin/dashd" ]; then
        cp -f "$BIN_SRC" /usr/local/bin/dashd
    fi
    chmod 0755 /usr/local/bin/dashd
    chown root:root /usr/local/bin/dashd

    echo "==> [4/7] 赋予低端口绑定能力 (setcap)..."
    if command -v setcap >/dev/null 2>&1; then
        setcap cap_net_bind_service=+ep /usr/local/bin/dashd
    fi

    echo "==> [5/7] 配置系统服务..."
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        cat > /etc/systemd/system/dashd.service <<'UNIT_EOF'
[Unit]
Description=dashd - VPS Management and Monitoring Server
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=dashd
Group=dashd
ExecStart=/usr/local/bin/dashd -config /etc/dash/config.toml
Restart=always
RestartSec=5s
LimitNOFILE=65535
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

ProtectSystem=full
ProtectHome=true
PrivateTmp=true
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT_EOF
        systemctl daemon-reload
        systemctl enable dashd.service >/dev/null 2>&1 || true
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        cat > /etc/init.d/dashd <<'OPENRC_EOF'
#!/sbin/openrc-run

name="dashd"
description="dashd - VPS Management and Monitoring Server"
command="/usr/local/bin/dashd"
command_args="-config /etc/dash/config.toml"
command_user="dashd:dashd"
command_background="true"
pidfile="/run/dashd.pid"

depend() {
	need net
	after firewall
}
OPENRC_EOF
        chmod 0755 /etc/init.d/dashd
        rc-update add dashd default >/dev/null 2>&1 || true
    fi

    echo "==> [6/7] 执行数据库迁移与初始化..."
    INIT_CMD="/usr/local/bin/dashd init-db --config /etc/dash/config.toml --domain $DOMAIN --admin-user $ADMIN_USER"
    if [ -n "$ADMIN_PASSWORD" ]; then
        INIT_CMD="$INIT_CMD --admin-password $ADMIN_PASSWORD"
    fi

    INIT_OUT=$($INIT_CMD 2>&1) || {
        echo "❌ 数据库初始化失败:" >&2
        printf '%s\n' "$INIT_OUT" >&2
        exit 1
    }

    # 提取管理员密码
    ADMIN_PW=$(printf '%s\n' "$INIT_OUT" | sed -n 's/^ADMIN_PASSWORD:[[:space:]]*//p' | head -n 1)

    echo "==> [7/7] 启动服务并等待健康检查..."
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        systemctl restart dashd.service
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        rc-service dashd restart
    else
        echo "⚠️ 未识别到 systemd 或 openrc，尝试启动 dashd..."
        pkill -f /usr/local/bin/dashd 2>/dev/null || true
        su -s /bin/sh dashd -c "/usr/local/bin/dashd -config /etc/dash/config.toml" >/var/log/dashd.log 2>&1 &
    fi

    # 健康检查轮询 (30 秒)
    CHECK_URL="http://127.0.0.1/healthz"
    if [ "$SERVER_LISTEN" != ":443" ]; then
        CHECK_URL="http://127.0.0.1${SERVER_LISTEN}/healthz"
    fi

    MAX_WAIT=30
    WAITED=0
    HEALTH_OK=0

    while [ "$WAITED" -lt "$MAX_WAIT" ]; do
        if curl -s -f "$CHECK_URL" >/dev/null 2>&1 || curl -k -s -f "https://127.0.0.1/healthz" >/dev/null 2>&1; then
            HEALTH_OK=1
            break
        fi
        sleep 1
        WAITED=$((WAITED + 1))
    done

    if [ "$HEALTH_OK" -ne 1 ]; then
        echo "❌ 健康检查失败: dashd 未能在 ${MAX_WAIT} 秒内通过 /healthz 检查。" >&2
        echo "--> 最近日志输出:" >&2
        if [ "$INIT_SYSTEM" = "systemd" ]; then
            journalctl -u dashd.service -n 50 --no-pager >&2 || true
        elif [ -f /var/log/dashd.log ]; then
            tail -n 50 /var/log/dashd.log >&2 || true
        fi
        exit 1
    fi

    echo ""
    echo "============================================================"
    echo "🎉 dashd 安装成功！"
    echo "============================================================"
    echo "访问地址:     https://${DOMAIN}"
    echo "初始管理员:   ${ADMIN_USER}"
    if [ -n "$ADMIN_PW" ]; then
        echo "初始密码:     ${ADMIN_PW}"
        echo "（密码仅显示一次，请妥善保存）"
    else
        echo "初始密码:     (管理员账号已存在，保留原有密码)"
    fi
    echo ""
    echo "★ 关键文件备份提醒（重要）："
    echo "  1. /etc/dash/master.key   (凭据主密钥，丢失则已加密凭据无法恢复)"
    echo "  2. /etc/dash/config.toml  (系统自举配置文件)"
    echo "============================================================"
}

cmd_upgrade() {
    check_root
    detect_init

    NEW_BIN=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --binary)
                NEW_BIN="$2"
                shift 2
                ;;
            *)
                NEW_BIN="$1"
                shift
                ;;
        esac
    done

    if [ -z "$NEW_BIN" ]; then
        if [ -f "$SCRIPT_DIR/bin/dashd" ]; then
            NEW_BIN="$SCRIPT_DIR/bin/dashd"
        elif [ -f "./bin/dashd" ]; then
            NEW_BIN="./bin/dashd"
        elif command -v go >/dev/null 2>&1 && [ -d "$SCRIPT_DIR/cmd/dashd" ]; then
            echo "--> 正在编译新版 dashd 二进制..."
            (cd "$SCRIPT_DIR" && CGO_ENABLED=0 go build -trimpath -o bin/dashd ./cmd/dashd)
            NEW_BIN="$SCRIPT_DIR/bin/dashd"
        fi
    fi

    if [ -z "$NEW_BIN" ] || [ ! -f "$NEW_BIN" ]; then
        echo "❌ 错误: 未指定新版二进制文件或文件不存在。" >&2
        exit 1
    fi

    if [ ! -f /usr/local/bin/dashd ]; then
        echo "❌ 错误: /usr/local/bin/dashd 不存在，请先执行 install。" >&2
        exit 1
    fi

    echo "==> [1/6] 备份当前旧版本二进制..."
    cp -p /usr/local/bin/dashd /usr/local/bin/dashd.bak

    echo "==> [2/6] 停止运行中的服务..."
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        systemctl stop dashd.service || true
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        rc-service dashd stop || true
    else
        pkill -f /usr/local/bin/dashd || true
    fi

    echo "==> [3/6] 替换新版本二进制..."
    cp -f "$NEW_BIN" /usr/local/bin/dashd
    chmod 0755 /usr/local/bin/dashd
    chown root:root /usr/local/bin/dashd
    if command -v setcap >/dev/null 2>&1; then
        setcap cap_net_bind_service=+ep /usr/local/bin/dashd
    fi

    echo "==> [4/6] 执行数据库迁移..."
    MIGRATE_OUT=$(/usr/local/bin/dashd migrate --config /etc/dash/config.toml 2>&1) || {
        echo "❌ 数据库迁移失败，正在回滚到旧版本..." >&2
        printf '%s\n' "$MIGRATE_OUT" >&2
        cp -f /usr/local/bin/dashd.bak /usr/local/bin/dashd
        if command -v setcap >/dev/null 2>&1; then
            setcap cap_net_bind_service=+ep /usr/local/bin/dashd
        fi
        if [ "$INIT_SYSTEM" = "systemd" ]; then
            systemctl start dashd.service
        elif [ "$INIT_SYSTEM" = "openrc" ]; then
            rc-service dashd start
        fi
        exit 1
    }

    echo "==> [5/6] 启动新版本服务..."
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        systemctl start dashd.service
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        rc-service dashd start
    else
        su -s /bin/sh dashd -c "/usr/local/bin/dashd -config /etc/dash/config.toml" >/var/log/dashd.log 2>&1 &
    fi

    echo "==> [6/6] 验证新版本健康状态..."
    CHECK_URL="http://127.0.0.1/healthz"
    if [ -f /etc/dash/config.toml ]; then
        LISTEN_CFG=$(grep '^[[:space:]]*listen[[:space:]]*=' /etc/dash/config.toml | head -n 1 | sed 's/.*=[[:space:]]*"\([^"]*\)".*/\1/')
        if [ -n "$LISTEN_CFG" ] && [ "$LISTEN_CFG" != ":443" ]; then
            CHECK_URL="http://127.0.0.1${LISTEN_CFG}/healthz"
        fi
    fi

    MAX_WAIT=15
    WAITED=0
    HEALTH_OK=0

    while [ "$WAITED" -lt "$MAX_WAIT" ]; do
        if curl -s -f "$CHECK_URL" >/dev/null 2>&1 || curl -k -s -f "https://127.0.0.1/healthz" >/dev/null 2>&1; then
            HEALTH_OK=1
            break
        fi
        sleep 1
        WAITED=$((WAITED + 1))
    done

    if [ "$HEALTH_OK" -ne 1 ]; then
        echo "❌ 新版本健康检查失败，正在自动回滚到旧版本..." >&2
        if [ "$INIT_SYSTEM" = "systemd" ]; then
            journalctl -u dashd.service -n 30 --no-pager >&2 || true
            systemctl stop dashd.service || true
        elif [ "$INIT_SYSTEM" = "openrc" ]; then
            rc-service dashd stop || true
        else
            pkill -f /usr/local/bin/dashd || true
        fi

        cp -f /usr/local/bin/dashd.bak /usr/local/bin/dashd
        if command -v setcap >/dev/null 2>&1; then
            setcap cap_net_bind_service=+ep /usr/local/bin/dashd
        fi

        if [ "$INIT_SYSTEM" = "systemd" ]; then
            systemctl start dashd.service
        elif [ "$INIT_SYSTEM" = "openrc" ]; then
            rc-service dashd start
        else
            su -s /bin/sh dashd -c "/usr/local/bin/dashd -config /etc/dash/config.toml" >/var/log/dashd.log 2>&1 &
        fi
        echo "✅ 回滚完成，旧版本服务已恢复运行。"
        exit 1
    fi

    rm -f /usr/local/bin/dashd.bak
    echo "🎉 dashd 升级成功！"
}

cmd_uninstall() {
    check_root
    detect_init

    PURGE=0
    for arg in "$@"; do
        if [ "$arg" = "--purge" ]; then
            PURGE=1
        fi
    done

    echo "--> 停止并注销 dashd 服务..."
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        systemctl stop dashd.service 2>/dev/null || true
        systemctl disable dashd.service 2>/dev/null || true
        rm -f /etc/systemd/system/dashd.service
        systemctl daemon-reload 2>/dev/null || true
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        rc-service dashd stop 2>/dev/null || true
        rc-update del dashd default 2>/dev/null || true
        rm -f /etc/init.d/dashd
    else
        pkill -f /usr/local/bin/dashd 2>/dev/null || true
    fi

    echo "--> 删除二进制文件..."
    rm -f /usr/local/bin/dashd /usr/local/bin/dashd.bak

    if [ "$PURGE" -eq 1 ]; then
        echo "--> 清除配置与本地数据目录..."
        rm -rf /etc/dash
        rm -rf /var/lib/dash
        echo "已清除 /etc/dash 与 /var/lib/dash。"
    else
        echo "已保留 /etc/dash 与 /var/lib/dash (使用 --purge 可清除)。"
    fi

    echo "★ 提示: 数据库数据保持原样，未作任何删除。"
    echo "dashd 卸载完成。"
}

cmd_status() {
    detect_init

    # 1. 服务运行状态
    PID=$(pidof dashd 2>/dev/null || pgrep -x dashd 2>/dev/null || true)
    PID=$(printf '%s' "$PID" | awk '{print $1}')

    SERVICE_STATUS="stopped (未运行)"
    RUN_USER="none"

    if [ -n "$PID" ]; then
        RUN_USER=$(ps -o user= -p "$PID" 2>/dev/null | tr -d ' ' || echo "unknown")
        SERVICE_STATUS="active (running) [PID: ${PID}, 运行用户: ${RUN_USER}]"
    elif [ "$INIT_SYSTEM" = "systemd" ]; then
        if systemctl is-active dashd.service >/dev/null 2>&1; then
            SERVICE_STATUS="active (running)"
        fi
    fi

    # 2. 查询 /healthz
    HEALTH_JSON=""
    for url in "http://127.0.0.1/healthz" "http://127.0.0.1:8080/healthz" "https://127.0.0.1/healthz"; do
        HEALTH_JSON=$(curl -k -s -m 2 "$url" 2>/dev/null || true)
        if [ -n "$HEALTH_JSON" ] && printf '%s' "$HEALTH_JSON" | grep -q '"status"'; then
            break
        fi
    done

    VER=""
    DB_STATUS="unknown"
    AGENTS_ONLINE="0"

    if [ -n "$HEALTH_JSON" ]; then
        VER=$(extract_json_val "$HEALTH_JSON" "version")
        DB_STATUS=$(extract_json_val "$HEALTH_JSON" "db")
        AGENTS_ONLINE=$(extract_json_int "$HEALTH_JSON" "agents_online")
    fi

    if [ -z "$VER" ]; then
        if [ -f /usr/local/bin/dashd ]; then
            VER=$(/usr/local/bin/dashd -v 2>/dev/null || echo "installed")
        else
            VER="not installed"
        fi
    fi

    # 3. 证书到期时间
    CERT_EXPIRY="尚未生成 (首次通过域名访问时自动申请)"
    CERT_DIR="/var/lib/dash/certs"
    if [ -d "$CERT_DIR" ]; then
        CERT_FILE=$(find "$CERT_DIR" -type f 2>/dev/null | head -n 1)
        if [ -n "$CERT_FILE" ] && command -v openssl >/dev/null 2>&1; then
            EXP_DATE=$(openssl x509 -enddate -noout -in "$CERT_FILE" 2>/dev/null | sed 's/notAfter=//')
            if [ -n "$EXP_DATE" ]; then
                CERT_EXPIRY="$EXP_DATE"
            fi
        fi
    fi

    echo "----------------------------------------"
    echo "dashd 服务状态"
    echo "----------------------------------------"
    echo "服务状态:     $SERVICE_STATUS"
    echo "程序版本:     $VER"
    echo "数据库连通性: $DB_STATUS"
    echo "证书到期时间: $CERT_EXPIRY"
    echo "在线 agent 数: $AGENTS_ONLINE"
    echo "----------------------------------------"
}

# 命令分发
COMMAND="${1:-}"
if [ -z "$COMMAND" ]; then
    usage
fi
shift

case "$COMMAND" in
    install)
        cmd_install "$@"
        ;;
    upgrade)
        cmd_upgrade "$@"
        ;;
    uninstall)
        cmd_uninstall "$@"
        ;;
    status)
        cmd_status "$@"
        ;;
    *)
        usage
        ;;
esac
