#!/bin/sh
set -e

# ==============================================================================
# dashd 一键部署与管理脚本 (POSIX sh 兼容)
# 支持: install / upgrade / uninstall / status
# 架构: Nginx 双域名反向代理 (内网控制台 + 公网 Agent 接入) + dashd
# ==============================================================================

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)

usage() {
    cat <<EOF
Usage: $0 <command> [options]

Commands:
  install     Install and bootstrap dashd with Nginx dual-domain ingress
  upgrade     Upgrade dashd binary with automatic rollback
  uninstall   Uninstall dashd service, binary, and Nginx configuration
  status      Show current status of dashd, Nginx, and TLS certificates

Options for 'install':
  --console-domain <domain>   Domain for internal web console (e.g. console.dash.internal)
  --agent-domain <domain>     Domain for public agent ingress (e.g. agent.example.com)
  --domain <domain>           Fallback domain (if single domain specified)
  --internal-ip <ip>          Internal IP to bind console (default: auto-detect WireGuard/LAN IP)
  --acme-email <email>        Email for Let's Encrypt / ACME HTTP-01 certificate on agent domain
  --db-driver <driver>        Database driver: oracle (default) or mysql
  --db-user <user>            Database user (default: admin)
  --db-password <password>    Database password (or DASH_DB_PASSWORD env)
  --db-dsn <dsn>              Database connection DSN (or DASH_DB_DSN env)
  --server-listen <listen>    Local loopback listen address for dashd (default: 127.0.0.1:8080)
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

detect_internal_ip() {
    # 1. 尝试检测 WireGuard / VPN 网卡 (wg0, wg, tailscale0, tun0)
    for iface in wg0 wg-quick wg tailscale0 tun0; do
        if command -v ip >/dev/null 2>&1; then
            _ip=$(ip -4 addr show dev "$iface" 2>/dev/null | sed -n 's/.*inet[[:space:]]*\([0-9.]*\).*/\1/p' | head -n 1)
            if [ -n "$_ip" ]; then
                echo "$_ip"
                return 0
            fi
        fi
    done

    # 2. 尝试从 hostname -I 获取私有 IPv4
    if command -v hostname >/dev/null 2>&1; then
        for _ip in $(hostname -I 2>/dev/null); do
            case "$_ip" in
                10.*|172.1[6-9].*|172.2[0-9].*|172.3[0-1].*|192.168.*)
                    echo "$_ip"
                    return 0
                    ;;
            esac
        done
    fi

    # 3. 尝试从 ip -4 addr 获取非 127.* IP
    if command -v ip >/dev/null 2>&1; then
        for _ip in $(ip -4 addr show 2>/dev/null | sed -n 's/.*inet[[:space:]]*\([0-9.]*\).*/\1/p'); do
            case "$_ip" in
                127.*) ;;
                *)
                    echo "$_ip"
                    return 0
                    ;;
            esac
        done
    fi

    # 4. 回退到 127.0.0.1
    echo "127.0.0.1"
}

ensure_dependencies() {
    detect_distro
    echo "--> 正在检查并安装系统依赖 (nginx, openssl, libcap, curl, certbot)..."
    if [ "$DISTRO" = "alpine" ]; then
        apk add --no-cache nginx openssl libcap curl certbot 2>/dev/null || apk add --no-cache nginx openssl libcap curl
    elif command -v apt-get >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -qq && apt-get install -y -qq nginx openssl libcap2-bin curl certbot
    else
        echo "⚠️ 请确保系统已安装: nginx, openssl, setcap (libcap), curl, certbot" >&2
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

ensure_certificates() {
    _c_domain="$1"
    _a_domain="$2"
    _i_ip="$3"

    mkdir -p /etc/dash/certs
    chmod 0755 /etc/dash/certs

    # 控制台证书 (绑定内网 IP 与内网域名)
    if [ ! -f /etc/dash/certs/console.crt ] || [ ! -f /etc/dash/certs/console.key ]; then
        echo "--> 为管理控制台生成 TLS 证书: ${_c_domain} (内网 IP: ${_i_ip})..."
        openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout /etc/dash/certs/console.key \
            -out /etc/dash/certs/console.crt \
            -subj "/CN=${_c_domain}" \
            -addext "subjectAltName=DNS:${_c_domain},IP:${_i_ip}" 2>/dev/null || \
        openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout /etc/dash/certs/console.key \
            -out /etc/dash/certs/console.crt \
            -subj "/CN=${_c_domain}" 2>/dev/null
        chmod 0644 /etc/dash/certs/console.crt
        chmod 0600 /etc/dash/certs/console.key
    fi

    # Agent 接入证书
    if [ ! -f /etc/dash/certs/agent.crt ] || [ ! -f /etc/dash/certs/agent.key ]; then
        echo "--> 为 Agent 接入生成 TLS 证书: ${_a_domain}..."
        openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout /etc/dash/certs/agent.key \
            -out /etc/dash/certs/agent.crt \
            -subj "/CN=${_a_domain}" \
            -addext "subjectAltName=DNS:${_a_domain}" 2>/dev/null || \
        openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout /etc/dash/certs/agent.key \
            -out /etc/dash/certs/agent.crt \
            -subj "/CN=${_a_domain}" 2>/dev/null
        chmod 0644 /etc/dash/certs/agent.crt
        chmod 0600 /etc/dash/certs/agent.key
    fi
}

configure_nginx() {
    _c_domain="$1"
    _a_domain="$2"
    _i_ip="$3"
    _listen="$4"

    echo "--> 配置 Nginx 双域名反向代理与 TLS 终结..."
    NGINX_DIR="/etc/nginx/conf.d"
    if [ -d /etc/nginx/http.d ]; then
        NGINX_DIR="/etc/nginx/http.d"
    fi
    mkdir -p "$NGINX_DIR"
    mkdir -p /var/lib/dash/acme
    chmod 0755 /var/lib/dash/acme

    # 避免默认站点的 80/443 端口冲突
    rm -f /etc/nginx/sites-enabled/default 2>/dev/null || true
    if [ -f /etc/nginx/http.d/default.conf ]; then
        mv /etc/nginx/http.d/default.conf /etc/nginx/http.d/default.conf.bak 2>/dev/null || true
    fi

    cat > "$NGINX_DIR/dash.conf" <<NGINX_EOF
# dash 反向代理配置（自动生成）
map \$http_upgrade \$dash_connection_upgrade {
    default upgrade;
    ''      close;
}

# 1. 证书申请与 HTTP 跳转
server {
    listen 80;
    server_name ${_c_domain} ${_a_domain};

    location /.well-known/acme-challenge/ {
        root /var/lib/dash/acme;
    }

    location /healthz {
        proxy_pass http://${_listen};
    }

    location / {
        return 301 https://\$host\$request_uri;
    }
}

# 2. 管理控制台 (仅绑定内网 IP: ${_i_ip})
server {
    listen ${_i_ip}:443 ssl;
    server_name ${_c_domain};

    ssl_certificate /etc/dash/certs/console.crt;
    ssl_certificate_key /etc/dash/certs/console.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    location / {
        proxy_pass http://${_listen};
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$dash_connection_upgrade;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}

# 3. Agent 接入入口 (公网监听 443，只暴露 /api/agent/v1/* 与 /dl/*)
server {
    listen 443 ssl;
    server_name ${_a_domain};

    ssl_certificate /etc/dash/certs/agent.crt;
    ssl_certificate_key /etc/dash/certs/agent.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    location /healthz {
        proxy_pass http://${_listen};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }

    location /api/agent/v1/ {
        proxy_pass http://${_listen};
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$dash_connection_upgrade;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 300s;
        proxy_send_timeout 300s;
    }

    location /dl/ {
        proxy_pass http://${_listen};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }

    location = /install.sh {
        proxy_pass http://${_listen};
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }

    # 核心安全控制：公网不暴露控制台 API 与前端界面
    location / {
        return 404;
    }
}
NGINX_EOF

    # 测试并启动或重载 Nginx
    nginx -t
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        systemctl daemon-reload 2>/dev/null || true
        systemctl enable nginx 2>/dev/null || true
        systemctl restart nginx || systemctl start nginx
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        rc-update add nginx default 2>/dev/null || true
        rc-service nginx restart || rc-service nginx start
    else
        nginx -s reload 2>/dev/null || nginx || true
    fi
}

diagnose_acme_failure() {
    _target_domain="$1"
    _log_file="$2"

    echo "🔍 正在诊断 ACME 证书申请失败原因:" >&2

    # 1. 检查 80 端口占用与监听情况
    LISTEN_80=""
    if command -v ss >/dev/null 2>&1; then
        LISTEN_80=$(ss -tlnp 2>/dev/null | grep -E ':(80)[[:space:]]' || true)
    elif command -v netstat >/dev/null 2>&1; then
        LISTEN_80=$(netstat -tlnp 2>/dev/null | grep -E ':(80)[[:space:]]' || true)
    fi

    if [ -n "$LISTEN_80" ]; then
        if printf '%s' "$LISTEN_80" | grep -qi 'nginx'; then
            echo "  [80 端口]: 正常由 Nginx 监听。" >&2
        else
            echo "  ❌ [80 端口被占]: 80 端口被其他非 Nginx 进程占用:" >&2
            printf '%s\n' "$LISTEN_80" >&2
            echo "     解决建议: 请停止占用 80 端口的程序并释放端口后重试。" >&2
        fi
    else
        echo "  ❌ [80 端口未监听]: 80 端口未检测到监听服务，Nginx 可能未正常运行。" >&2
        echo "     解决建议: 请检查 nginx 运行状态及日志 (nginx -t 或 journalctl -u nginx)。" >&2
    fi

    # 2. 检查 DNS 解析与本机公网 IP 是否匹配
    PUBLIC_IP=""
    for ip_svc in "https://api.ipify.org" "https://ifconfig.me" "https://icanhazip.com"; do
        PUBLIC_IP=$(curl -s -m 3 "$ip_svc" 2>/dev/null || true)
        if [ -n "$PUBLIC_IP" ]; then
            break
        fi
    done

    RESOLVED_IP=""
    if command -v getent >/dev/null 2>&1; then
        RESOLVED_IP=$(getent hosts "$_target_domain" 2>/dev/null | awk '{print $1}' | head -n 1)
    elif command -v host >/dev/null 2>&1; then
        RESOLVED_IP=$(host "$_target_domain" 2>/dev/null | sed -n 's/.*has address \([0-9.]*\).*/\1/p' | head -n 1)
    elif command -v nslookup >/dev/null 2>&1; then
        RESOLVED_IP=$(nslookup "$_target_domain" 2>/dev/null | awk '/^Address: / { print $2 }' | tail -n 1)
    fi

    if [ -n "$RESOLVED_IP" ]; then
        if [ -n "$PUBLIC_IP" ] && [ "$RESOLVED_IP" != "$PUBLIC_IP" ]; then
            echo "  ❌ [域名未解析到本机]: 域名 ${_target_domain} 当前解析至 ${RESOLVED_IP}，但本机公网 IP 为 ${PUBLIC_IP}。" >&2
            echo "     解决建议: 请在 DNS 服务商处将 ${_target_domain} 的 A 记录修改为本机公网 IP (${PUBLIC_IP})，待生效后重试。" >&2
        else
            echo "  [DNS 解析]: 域名 ${_target_domain} 解析至 ${RESOLVED_IP}。" >&2
        fi
    else
        echo "  ❌ [域名未解析到本机]: 无法解析域名 ${_target_domain}。" >&2
        echo "     解决建议: 请在 DNS 服务商处添加 ${_target_domain} 的 A 记录并指向本机公网 IP (${PUBLIC_IP:-未获取到})。" >&2
    fi

    # 3. 检查常见 Let's Encrypt 错误特征
    if [ -f "$_log_file" ]; then
        if grep -qiE 'rate.*limit|too many requests' "$_log_file"; then
            echo "  ❌ [速率限制/限流]: Let's Encrypt 触发了请求速率限制 (Rate Limit)。" >&2
            echo "     解决建议: 短期内向 Let's Encrypt 请求证书次数过多，请稍候几小时或更换域名重试。" >&2
        elif grep -qiE 'connection refused|timeout|failed to connect' "$_log_file"; then
            echo "  ❌ [连接超时/防火墙拦截]: Let's Encrypt CA 验证服务器无法连通本机 80 端口。" >&2
            echo "     解决建议: 请检查云服务商安全组 (Security Group) 与主机防火墙规则，确保入方向 80 端口对公网放行。" >&2
        fi
    fi
}

setup_acme_renewal() {
    _target_domain="$1"

    echo "--> 配置 ACME 证书自动续期 (certbot timer / cron)..."

    # 1. 部署 hook：续期成功后自动拷贝证书并 reload nginx
    HOOK_DIR="/etc/letsencrypt/renewal-hooks/deploy"
    mkdir -p "$HOOK_DIR"
    cat > "$HOOK_DIR/dash-nginx.sh" <<'HOOK_EOF'
#!/bin/sh
for d in /etc/letsencrypt/live/*; do
    if [ -d "$d" ] && [ -f "$d/fullchain.pem" ]; then
        cp -L "$d/fullchain.pem" /etc/dash/certs/agent.crt
        cp -L "$d/privkey.pem" /etc/dash/certs/agent.key
        chmod 0644 /etc/dash/certs/agent.crt
        chmod 0600 /etc/dash/certs/agent.key
        nginx -s reload 2>/dev/null || true
    fi
done
HOOK_EOF
    chmod 0755 "$HOOK_DIR/dash-nginx.sh"

    # 2. 启用 systemd certbot.timer 或配置 cron
    if [ "$INIT_SYSTEM" = "systemd" ] && command -v systemctl >/dev/null 2>&1; then
        systemctl enable --now certbot.timer 2>/dev/null || true
    fi

    if [ -d /etc/cron.d ]; then
        cat > /etc/cron.d/certbot-dash <<'CRON_EOF'
0 3 * * * root certbot renew -q
CRON_EOF
        chmod 0644 /etc/cron.d/certbot-dash
    elif [ -d /etc/periodic/daily ]; then
        cat > /etc/periodic/daily/certbot-dash <<'CRON_EOF'
#!/bin/sh
certbot renew -q
CRON_EOF
        chmod 0755 /etc/periodic/daily/certbot-dash
    fi
}

request_agent_acme_cert() {
    _a_domain="$1"
    _email="$2"

    echo "--> 正在通过 ACME HTTP-01 为 Agent 接入域名 ${_a_domain} 申请 Let's Encrypt 证书..."

    if ! command -v certbot >/dev/null 2>&1 && ! command -v acme.sh >/dev/null 2>&1; then
        echo "❌ 错误: 未检测到 certbot 或 acme.sh 工具，无法执行 ACME 证书申请。" >&2
        echo "   请安装 certbot (例如 apt-get install -y certbot 或 apk add certbot) 后重试。" >&2
        exit 1
    fi

    mkdir -p /var/lib/dash/acme/.well-known/acme-challenge
    chmod -R 0755 /var/lib/dash/acme

    ACME_SUCCESS=0
    ACME_LOG=$(mktemp 2>/dev/null || echo "/tmp/acme_$$.log")

    if command -v certbot >/dev/null 2>&1; then
        echo "--> 使用 certbot (webroot 模式) 申请证书..."
        if certbot certonly --webroot -w /var/lib/dash/acme \
            --non-interactive --agree-tos --no-eff-email \
            --email "$_email" \
            -d "$_a_domain" \
            --keep-until-expiring >"$ACME_LOG" 2>&1; then

            CERT_PATH="/etc/letsencrypt/live/${_a_domain}/fullchain.pem"
            KEY_PATH="/etc/letsencrypt/live/${_a_domain}/privkey.pem"
            if [ -f "$CERT_PATH" ] && [ -f "$KEY_PATH" ]; then
                cp -L "$CERT_PATH" /etc/dash/certs/agent.crt
                cp -L "$KEY_PATH" /etc/dash/certs/agent.key
                chmod 0644 /etc/dash/certs/agent.crt
                chmod 0600 /etc/dash/certs/agent.key
                ACME_SUCCESS=1
                echo "✅ Agent 接入域名 Let's Encrypt ACME 证书签发成功。"
            fi
        fi
    elif command -v acme.sh >/dev/null 2>&1; then
        echo "--> 使用 acme.sh (webroot 模式) 申请证书..."
        if acme.sh --issue -d "$_a_domain" -w /var/lib/dash/acme >"$ACME_LOG" 2>&1; then
            if acme.sh --install-cert -d "$_a_domain" \
                --key-file /etc/dash/certs/agent.key \
                --fullchain-file /etc/dash/certs/agent.crt \
                --reloadcmd "nginx -s reload 2>/dev/null || true" >/dev/null 2>&1; then
                chmod 0644 /etc/dash/certs/agent.crt
                chmod 0600 /etc/dash/certs/agent.key
                ACME_SUCCESS=1
                echo "✅ Agent 接入域名 acme.sh 证书签发成功。"
            fi
        fi
    fi

    if [ "$ACME_SUCCESS" -ne 1 ]; then
        echo "❌ 错误: Agent 接入域名 (${_a_domain}) ACME 证书申请失败！" >&2
        echo "----------------------------------------" >&2
        cat "$ACME_LOG" >&2
        echo "----------------------------------------" >&2

        diagnose_acme_failure "$_a_domain" "$ACME_LOG"
        rm -f "$ACME_LOG"
        exit 1
    fi
    rm -f "$ACME_LOG"

    setup_acme_renewal "$_a_domain"

    # 重载 Nginx 应用新证书
    if command -v nginx >/dev/null 2>&1; then
        nginx -s reload 2>/dev/null || systemctl reload nginx 2>/dev/null || rc-service nginx reload 2>/dev/null || true
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

    CONSOLE_DOMAIN=""
    AGENT_DOMAIN=""
    INTERNAL_IP=""
    DOMAIN=""
    ACME_EMAIL="${DASH_ACME_EMAIL:-}"
    DB_DRIVER="${DASH_DB_DRIVER:-oracle}"
    DB_USER="${DASH_DB_USER:-admin}"
    DB_PASSWORD="${DASH_DB_PASSWORD:-}"
    DB_DSN="${DASH_DB_DSN:-}"
    SERVER_LISTEN="${DASH_SERVER_LISTEN:-127.0.0.1:8080}"
    DATA_DIR="${DASH_SERVER_DATA_DIR:-/var/lib/dash}"
    ADMIN_USER="admin"
    ADMIN_PASSWORD=""
    NON_INTERACTIVE=0

    while [ $# -gt 0 ]; do
        case "$1" in
            --console-domain)
                CONSOLE_DOMAIN="$2"
                shift 2
                ;;
            --agent-domain)
                AGENT_DOMAIN="$2"
                shift 2
                ;;
            --domain)
                DOMAIN="$2"
                shift 2
                ;;
            --internal-ip)
                INTERNAL_IP="$2"
                shift 2
                ;;
            --acme-email)
                ACME_EMAIL="$2"
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

    # 域名交互输入 (P1-19 需求变更：双域名要求输入两个域名)
    if [ -z "$CONSOLE_DOMAIN" ] || [ -z "$AGENT_DOMAIN" ]; then
        if [ "$NON_INTERACTIVE" -eq 1 ] || [ ! -t 0 ]; then
            if [ -n "$DOMAIN" ]; then
                [ -z "$AGENT_DOMAIN" ] && AGENT_DOMAIN="$DOMAIN"
                [ -z "$CONSOLE_DOMAIN" ] && CONSOLE_DOMAIN="console.$DOMAIN"
            fi
            if [ -z "$CONSOLE_DOMAIN" ] || [ -z "$AGENT_DOMAIN" ]; then
                echo "❌ 错误: 非交互模式下必须指定 --console-domain 与 --agent-domain（或通过 --domain 提供）。" >&2
                exit 1
            fi
        else
            if [ -z "$CONSOLE_DOMAIN" ]; then
                printf "请输入管理控制台内网域名 (如 console.dash.internal): "
                read -r CONSOLE_DOMAIN
                if [ -z "$CONSOLE_DOMAIN" ]; then
                    echo "❌ 错误: 控制台域名不能为空。" >&2
                    exit 1
                fi
            fi
            if [ -z "$AGENT_DOMAIN" ]; then
                printf "请输入 Agent 接入公网域名 (如 agent.example.com): "
                read -r AGENT_DOMAIN
                if [ -z "$AGENT_DOMAIN" ]; then
                    echo "❌ 错误: Agent 域名不能为空。" >&2
                    exit 1
                fi
            fi
        fi
    fi

    # 内网 IP 自动检测与确认
    AUTO_IP=$(detect_internal_ip)
    if [ -z "$INTERNAL_IP" ]; then
        if [ "$NON_INTERACTIVE" -eq 1 ] || [ ! -t 0 ]; then
            INTERNAL_IP="$AUTO_IP"
        else
            printf "请输入管理控制台绑定的内网 IP [默认 %s]: " "$AUTO_IP"
            read -r INPUT_IP
            INTERNAL_IP="${INPUT_IP:-$AUTO_IP}"
        fi
    fi

    # ACME 证书通知邮箱输入与警告提示
    if [ -z "$ACME_EMAIL" ]; then
        if [ "$NON_INTERACTIVE" -eq 0 ] && [ -t 0 ]; then
            printf "请输入 Agent 接入域名 ACME 证书通知邮箱 (用于 Let's Encrypt，留空使用自签测试证书): "
            read -r INPUT_EMAIL
            ACME_EMAIL="$INPUT_EMAIL"
        fi
    fi

    if [ -z "$ACME_EMAIL" ]; then
        echo "⚠️ 警告: 未提供 --acme-email，Agent 接入域名将使用自签测试证书。"
        echo "⚠️ 警告: agent 将无法校验证书，仅供测试！生产环境请指定 --acme-email 以签发受信任证书。"
    fi

    echo "==> [1/8] 检查系统环境与依赖 (Nginx, OpenSSL, setcap, curl)..."
    ensure_dependencies
    detect_init

    echo "==> [2/8] 确保系统用户与目录权限..."
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
data_dir = "$DATA_DIR"
master_key = "/etc/dash/master.key"
CFG_EOF
        chmod 0600 /etc/dash/config.toml
        chown dashd:dashd /etc/dash/config.toml
    fi

    echo "==> [3/8] 生成双域名 TLS 证书..."
    ensure_certificates "$CONSOLE_DOMAIN" "$AGENT_DOMAIN" "$INTERNAL_IP"

    echo "==> [4/8] 配置并启动 Nginx 双入口反向代理..."
    configure_nginx "$CONSOLE_DOMAIN" "$AGENT_DOMAIN" "$INTERNAL_IP" "$SERVER_LISTEN"

    # 若提供了 --acme-email，执行 ACME HTTP-01 证书申请并自动配置续期
    if [ -n "$ACME_EMAIL" ]; then
        request_agent_acme_cert "$AGENT_DOMAIN" "$ACME_EMAIL"
    fi

    echo "==> [5/8] 准备 dashd 可执行程序..."
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

    if command -v setcap >/dev/null 2>&1; then
        setcap cap_net_bind_service=+ep /usr/local/bin/dashd || true
    fi

    echo "==> [6/8] 配置系统服务 (dashd.service)..."
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
ExecStart=/usr/local/bin/dashd -config /etc/dash/config.toml serve
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
command_args="-config /etc/dash/config.toml serve"
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

    echo "==> [7/8] 执行数据库迁移与双域名初始化..."
    INIT_CMD="/usr/local/bin/dashd init-db --config /etc/dash/config.toml --domain $AGENT_DOMAIN --agent-domain $AGENT_DOMAIN --console-domain $CONSOLE_DOMAIN --admin-user $ADMIN_USER"
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

    echo "==> [8/8] 启动 dashd 服务并等待健康检查..."
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        systemctl restart dashd.service
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        rc-service dashd restart
    else
        echo "⚠️ 未识别到 systemd 或 openrc，尝试启动 dashd..."
        pkill -f /usr/local/bin/dashd 2>/dev/null || true
        su -s /bin/sh dashd -c "/usr/local/bin/dashd -config /etc/dash/config.toml serve" >/var/log/dashd.log 2>&1 &
    fi

    # 健康检查轮询 (30 秒)
    CHECK_URL="http://127.0.0.1/healthz"
    if [ -n "$SERVER_LISTEN" ]; then
        CHECK_URL="http://${SERVER_LISTEN}/healthz"
    fi

    MAX_WAIT=30
    WAITED=0
    HEALTH_OK=0

    while [ "$WAITED" -lt "$MAX_WAIT" ]; do
        if curl -s "$CHECK_URL" 2>/dev/null | grep -q '"status"' || curl -k -s "https://127.0.0.1/healthz" 2>/dev/null | grep -q '"status"'; then
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
    echo "🎉 dashd 双入口一键部署成功！"
    echo "============================================================"
    echo "管理控制台:   https://${CONSOLE_DOMAIN}"
    echo "控制台绑定:   ${INTERNAL_IP}:443 (仅内网/WireGuard 访问，不监听公网)"
    echo "Agent 接入:   https://${AGENT_DOMAIN} (公网接入，仅暴露 Agent 协议)"
    if [ -n "$ACME_EMAIL" ]; then
        echo "Agent 证书:   Let's Encrypt ACME 证书 (通知邮箱: ${ACME_EMAIL})"
    else
        echo "Agent 证书:   自签测试证书 (未提供 --acme-email)"
    fi
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
    echo "  3. /etc/dash/certs/       (TLS 证书与密钥目录)"
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
            # ★ 前端产物一致性守卫（P1-25）：自行编译时绝不能绕过，
            #   否则 web/src 改了但 internal/api/dist 是旧的，照样能编译部署上去 —— 这正是 P1-25 要防的坑。
            if [ -x "$SCRIPT_DIR/scripts/lint-dist.sh" ]; then
                if ! (cd "$SCRIPT_DIR" && ./scripts/lint-dist.sh); then
                    echo "❌ 前端内嵌产物与源码不一致，已中止升级。" >&2
                    echo "   请先在有 Node 的机器上执行 make build-web 并提交产物，再重新升级。" >&2
                    exit 1
                fi
            fi
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
        setcap cap_net_bind_service=+ep /usr/local/bin/dashd || true
    fi

    echo "==> [4/6] 执行数据库迁移..."
    MIGRATE_OUT=$(/usr/local/bin/dashd migrate --config /etc/dash/config.toml 2>&1) || {
        echo "❌ 数据库迁移失败，正在回滚到旧版本..." >&2
        printf '%s\n' "$MIGRATE_OUT" >&2
        cp -f /usr/local/bin/dashd.bak /usr/local/bin/dashd
        if command -v setcap >/dev/null 2>&1; then
            setcap cap_net_bind_service=+ep /usr/local/bin/dashd || true
        fi
        if [ "$INIT_SYSTEM" = "systemd" ]; then
            systemctl start dashd.service
        elif [ "$INIT_SYSTEM" = "openrc" ]; then
            rc-service dashd start
        fi
        exit 1
    }

    echo "==> [5/6] 启动新版本服务并重载 Nginx..."
    if [ "$INIT_SYSTEM" = "systemd" ]; then
        systemctl start dashd.service
        systemctl reload nginx 2>/dev/null || systemctl restart nginx 2>/dev/null || true
    elif [ "$INIT_SYSTEM" = "openrc" ]; then
        rc-service dashd start
        rc-service nginx reload 2>/dev/null || true
    else
        su -s /bin/sh dashd -c "/usr/local/bin/dashd -config /etc/dash/config.toml serve" >/var/log/dashd.log 2>&1 &
        nginx -s reload 2>/dev/null || true
    fi

    echo "==> [6/6] 验证新版本健康状态..."
    CHECK_URL="http://127.0.0.1:8080/healthz"
    if [ -f /etc/dash/config.toml ]; then
        LISTEN_CFG=$(grep '^[[:space:]]*listen[[:space:]]*=' /etc/dash/config.toml | head -n 1 | sed 's/.*=[[:space:]]*"\([^"]*\)".*/\1/')
        if [ -n "$LISTEN_CFG" ]; then
            CHECK_URL="http://${LISTEN_CFG}/healthz"
        fi
    fi

    MAX_WAIT=15
    WAITED=0
    HEALTH_OK=0

    while [ "$WAITED" -lt "$MAX_WAIT" ]; do
        if curl -s "$CHECK_URL" 2>/dev/null | grep -q '"status"' || curl -k -s "https://127.0.0.1/healthz" 2>/dev/null | grep -q '"status"'; then
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
            setcap cap_net_bind_service=+ep /usr/local/bin/dashd || true
        fi

        if [ "$INIT_SYSTEM" = "systemd" ]; then
            systemctl start dashd.service
        elif [ "$INIT_SYSTEM" = "openrc" ]; then
            rc-service dashd start
        else
            su -s /bin/sh dashd -c "/usr/local/bin/dashd -config /etc/dash/config.toml serve" >/var/log/dashd.log 2>&1 &
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

    echo "--> 清除 Nginx 反向代理配置与证书续期任务..."
    rm -f /etc/nginx/conf.d/dash.conf /etc/nginx/http.d/dash.conf /etc/nginx/sites-enabled/dash.conf
    rm -f /etc/letsencrypt/renewal-hooks/deploy/dash-nginx.sh
    rm -f /etc/cron.d/certbot-dash /etc/periodic/daily/certbot-dash
    nginx -s reload 2>/dev/null || true

    echo "--> 删除 dashd 二进制文件..."
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

    # 1. dashd 服务运行状态
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

    # 2. Nginx 服务运行状态
    NGINX_STATUS="stopped (未运行)"
    if pidof nginx >/dev/null 2>&1 || pgrep -x nginx >/dev/null 2>&1; then
        NGINX_STATUS="active (running)"
    elif [ "$INIT_SYSTEM" = "systemd" ]; then
        if systemctl is-active nginx >/dev/null 2>&1; then
            NGINX_STATUS="active (running)"
        fi
    fi

    # 3. 查询 /healthz
    HEALTH_JSON=""
    for url in "http://127.0.0.1:8080/healthz" "http://127.0.0.1/healthz" "https://127.0.0.1/healthz"; do
        HEALTH_JSON=$(curl -k -s -m 2 "$url" 2>/dev/null || true)
        if [ -n "$HEALTH_JSON" ] && printf '%s' "$HEALTH_JSON" | grep -q '"status"'; then
            break
        fi
    done

    VER=""
    DB_STATUS="unknown"
    AGENTS_ONLINE="0"
    DEV_NO_AUTH="false"

    if [ -n "$HEALTH_JSON" ]; then
        VER=$(extract_json_val "$HEALTH_JSON" "version")
        DB_STATUS=$(extract_json_val "$HEALTH_JSON" "db")
        AGENTS_ONLINE=$(extract_json_int "$HEALTH_JSON" "agents_online")
        if printf '%s' "$HEALTH_JSON" | grep -q '"dev_no_auth"[[:space:]]*:[[:space:]]*true'; then
            DEV_NO_AUTH="true"
        fi
    fi

    if [ -z "$VER" ]; then
        if [ -f /usr/local/bin/dashd ]; then
            VER=$(/usr/local/bin/dashd -v 2>/dev/null || echo "installed")
        else
            VER="not installed"
        fi
    fi

    # 4. 证书到期时间
    CONSOLE_CERT_EXPIRY="尚未生成"
    AGENT_CERT_EXPIRY="尚未生成"
    if [ -f /etc/dash/certs/console.crt ] && command -v openssl >/dev/null 2>&1; then
        EXP_DATE=$(openssl x509 -enddate -noout -in /etc/dash/certs/console.crt 2>/dev/null | sed 's/notAfter=//')
        [ -n "$EXP_DATE" ] && CONSOLE_CERT_EXPIRY="$EXP_DATE (自签/内部)"
    fi
    if [ -f /etc/dash/certs/agent.crt ] && command -v openssl >/dev/null 2>&1; then
        EXP_DATE=$(openssl x509 -enddate -noout -in /etc/dash/certs/agent.crt 2>/dev/null | sed 's/notAfter=//')
        ISSUER=$(openssl x509 -issuer -noout -in /etc/dash/certs/agent.crt 2>/dev/null | sed 's/issuer=//')
        if printf '%s' "$ISSUER" | grep -qi "Let's Encrypt"; then
            AGENT_CERT_EXPIRY="$EXP_DATE (Let's Encrypt / ACME)"
        elif printf '%s' "$ISSUER" | grep -qi "acme"; then
            AGENT_CERT_EXPIRY="$EXP_DATE (ACME)"
        else
            AGENT_CERT_EXPIRY="$EXP_DATE (自签/未校验)"
        fi
    fi

    # 5. ACME 自动续期状态
    ACME_RENEWAL_STATUS="未配置"
    if [ -d /etc/letsencrypt/live ] && [ -n "$(ls -A /etc/letsencrypt/live 2>/dev/null)" ]; then
        if [ "$INIT_SYSTEM" = "systemd" ] && systemctl is-enabled certbot.timer >/dev/null 2>&1; then
            ACME_RENEWAL_STATUS="已启用 (systemd certbot.timer)"
        elif [ -f /etc/cron.d/certbot-dash ] || [ -f /etc/periodic/daily/certbot-dash ]; then
            ACME_RENEWAL_STATUS="已启用 (cron 定时任务)"
        else
            ACME_RENEWAL_STATUS="已签发 (独立证书目录)"
        fi
    fi

    # 6. 读取配置域名与绑定信息
    CONF_FILE="/etc/nginx/conf.d/dash.conf"
    [ -f /etc/nginx/http.d/dash.conf ] && CONF_FILE="/etc/nginx/http.d/dash.conf"

    CONSOLE_INFO="未知"
    AGENT_INFO="未知"
    if [ -f "$CONF_FILE" ]; then
        C_BIND=$(grep 'listen .*ssl' "$CONF_FILE" | head -n 1 | awk '{print $2}')
        C_NAME=$(grep 'server_name' "$CONF_FILE" | sed -n '2p' | awk '{print $2}' | tr -d ';')
        A_NAME=$(grep 'server_name' "$CONF_FILE" | sed -n '3p' | awk '{print $2}' | tr -d ';')
        [ -n "$C_NAME" ] && CONSOLE_INFO="https://${C_NAME} (绑定: ${C_BIND})"
        [ -n "$A_NAME" ] && AGENT_INFO="https://${A_NAME} (公网 443)"
    fi

    echo "----------------------------------------"
    echo "dashd 服务状态"
    echo "----------------------------------------"
    echo "dashd 状态:     $SERVICE_STATUS"
    if [ "$DEV_NO_AUTH" = "true" ]; then
        echo "⚠️ 鉴权模式:    开发免登录模式已开启 (dev_no_auth=true)"
    fi
    echo "Nginx 状态:     $NGINX_STATUS"
    echo "程序版本:       $VER"
    echo "数据库连通性:   $DB_STATUS"
    echo "控制台入口:     $CONSOLE_INFO"
    echo "控制台证书到期: $CONSOLE_CERT_EXPIRY"
    echo "Agent 接入入口: $AGENT_INFO"
    echo "Agent 证书到期: $AGENT_CERT_EXPIRY"
    echo "ACME 续期状态:  $ACME_RENEWAL_STATUS"
    echo "在线 agent 数:  $AGENTS_ONLINE"
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
