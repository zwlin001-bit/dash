#!/bin/sh
set -e

# ==============================================================================
# dash-agent 安装与配置脚本 (POSIX sh 兼容，支持 Alpine / Debian / Ubuntu)
# ==============================================================================

DEFAULT_ENDPOINT="__DEFAULT_ENDPOINT__"
DEFAULT_TOKEN="__DEFAULT_TOKEN__"

# 若模板占位符未被替换，重置为空
case "$DEFAULT_ENDPOINT" in
    __DEFAULT_*|"") DEFAULT_ENDPOINT="" ;;
esac
case "$DEFAULT_TOKEN" in
    __DEFAULT_*|"") DEFAULT_TOKEN="" ;;
esac

ENROLL_TOKEN="$DEFAULT_TOKEN"
ENDPOINT="$DEFAULT_ENDPOINT"
INSECURE=0

show_help() {
    cat <<EOF
Usage: $0 [options]

Options:
  --enroll, -e <token>   Enrollment token issued by dash server
  --endpoint, -s <url>   Dash server endpoint URL (e.g. https://dash.example.com)
  --insecure, -k         Allow insecure TLS connections (skip verification)
  --help, -h             Show this help message
EOF
}

# 命令行参数解析
while [ $# -gt 0 ]; do
    case "$1" in
        --enroll|-e|--token)
            if [ -z "${2:-}" ]; then
                echo "❌ 错误: $1 参数需要一个值" >&2
                exit 1
            fi
            ENROLL_TOKEN="$2"
            shift 2
            ;;
        --endpoint|--server|-s)
            if [ -z "${2:-}" ]; then
                echo "❌ 错误: $1 参数需要一个值" >&2
                exit 1
            fi
            ENDPOINT="$2"
            shift 2
            ;;
        --insecure|-k)
            INSECURE=1
            shift
            ;;
        --help|-h)
            show_help
            exit 0
            ;;
        *)
            echo "❌ 未知参数: $1" >&2
            show_help
            exit 1
            ;;
    esac
done

# 权限校验
if [ "$(id -u)" -ne 0 ]; then
    echo "❌ 错误: 请使用 root 用户或 sudo 运行此安装脚本。" >&2
    exit 1
fi

# 确定 Endpoint
if [ -z "$ENDPOINT" ]; then
    if [ -n "${DASH_ENDPOINT:-}" ]; then
        ENDPOINT="$DASH_ENDPOINT"
    else
        echo "❌ 错误: 未指定服务端 endpoint 地址。" >&2
        echo "请通过 --endpoint <url> 指定或确认 /install.sh 服务端已正确生成。" >&2
        exit 1
    fi
fi
# 去除 URL 末尾斜杠
ENDPOINT=$(printf '%s' "$ENDPOINT" | sed 's:/*$::')

# 探测 CPU 架构
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64)
        TARGET_ARCH="amd64"
        ;;
    aarch64|arm64)
        TARGET_ARCH="arm64"
        ;;
    armv7*|armhf|arm)
        TARGET_ARCH="armv7"
        ;;
    *)
        echo "❌ 不受支持的 CPU 架构: $ARCH (仅支持 amd64, arm64, armv7)" >&2
        exit 1
        ;;
esac

# 探测 init 系统
# 依据规范：/run/systemd/system 存在 → systemd，否则 → OpenRC
if [ -d /run/systemd/system ]; then
    INIT_SYSTEM="systemd"
elif [ -f /sbin/openrc-run ] || [ -f /etc/alpine-release ]; then
    INIT_SYSTEM="openrc"
elif command -v systemctl >/dev/null 2>&1; then
    INIT_SYSTEM="systemd"
else
    INIT_SYSTEM="openrc"
fi

# 探测发行版
if [ -f /etc/alpine-release ]; then
    DISTRO="alpine"
elif [ -f /etc/debian_version ]; then
    DISTRO="debian"
else
    DISTRO="linux"
fi

echo "==> 检测到系统架构: $TARGET_ARCH, 发行版: $DISTRO, init系统: $INIT_SYSTEM"

# 确保基础依赖存在
if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    echo "--> 安装 HTTP 下载工具..."
    if [ "$DISTRO" = "alpine" ]; then
        apk add --no-cache curl
    elif command -v apt-get >/dev/null 2>&1; then
        apt-get update -qq && apt-get install -y -qq curl
    fi
fi

if [ "$INIT_SYSTEM" = "openrc" ] && ! command -v openrc >/dev/null 2>&1 && [ ! -f /sbin/openrc-run ]; then
    if [ "$DISTRO" = "alpine" ]; then
        echo "--> 在 Alpine 上安装 OpenRC..."
        apk add --no-cache openrc
    fi
fi

# 通用下载函数
download_file() {
    _url="$1"
    _out="$2"
    _extra_k=""
    if [ "$INSECURE" = "1" ] || [ "${ENDPOINT#http://}" != "$ENDPOINT" ]; then
        _extra_k="-k"
    fi

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL $_extra_k "$_url" -o "$_out"
    elif command -v wget >/dev/null 2>&1; then
        _wget_k=""
        if [ "$INSECURE" = "1" ]; then
            _wget_k="--no-check-certificate"
        fi
        wget -q $_wget_k -O "$_out" "$_url"
    else
        echo "❌ 错误: 未找到 curl 或 wget 工具" >&2
        return 1
    fi
}

# 1. 下载 agent 二进制与校验 sha256
BIN_URL="$ENDPOINT/dl/dash-agent-linux-$TARGET_ARCH"
SHA_URL="$ENDPOINT/dl/sha256sums.txt"

echo "--> [1/5] 从 $BIN_URL 下载 dash-agent..."
TMP_BIN=$(mktemp /tmp/dash-agent.XXXXXX)
download_file "$BIN_URL" "$TMP_BIN"

TMP_SHA=$(mktemp /tmp/dash-agent-sha.XXXXXX)
if download_file "$SHA_URL" "$TMP_SHA" 2>/dev/null; then
    EXPECTED_SHA=$(grep "dash-agent-linux-$TARGET_ARCH" "$TMP_SHA" | awk '{print $1}' | head -n 1)
    if [ -n "$EXPECTED_SHA" ]; then
        ACTUAL_SHA=$(sha256sum "$TMP_BIN" | awk '{print $1}')
        if [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
            echo "❌ sha256 校验失败!" >&2
            echo "预期: $EXPECTED_SHA" >&2
            echo "实际: $ACTUAL_SHA" >&2
            rm -f "$TMP_BIN" "$TMP_SHA"
            exit 1
        fi
        echo "✅ sha256 校验通过 ($ACTUAL_SHA)"
    fi
    rm -f "$TMP_SHA"
else
    rm -f "$TMP_SHA"
    echo "⚠️  未能获取 sha256sums.txt，尝试独立校验文件..."
    TMP_SINGLE_SHA=$(mktemp /tmp/dash-agent-sha.XXXXXX)
    if download_file "${BIN_URL}.sha256" "$TMP_SINGLE_SHA" 2>/dev/null; then
        EXPECTED_SHA=$(awk '{print $1}' "$TMP_SINGLE_SHA" | head -n 1)
        ACTUAL_SHA=$(sha256sum "$TMP_BIN" | awk '{print $1}')
        if [ -n "$EXPECTED_SHA" ] && [ "$EXPECTED_SHA" != "$ACTUAL_SHA" ]; then
            echo "❌ sha256 校验失败!" >&2
            echo "预期: $EXPECTED_SHA" >&2
            echo "实际: $ACTUAL_SHA" >&2
            rm -f "$TMP_BIN" "$TMP_SINGLE_SHA"
            exit 1
        fi
        echo "✅ sha256 校验通过 ($ACTUAL_SHA)"
    fi
    rm -f "$TMP_SINGLE_SHA"
fi

chmod 0755 "$TMP_BIN"
mv -f "$TMP_BIN" /usr/local/bin/dash-agent
chmod 0755 /usr/local/bin/dash-agent

# 2. 创建非特权系统用户 dashagent
echo "--> [2/5] 配置系统用户 dashagent..."
if ! id dashagent >/dev/null 2>&1; then
    if [ "$DISTRO" = "alpine" ]; then
        addgroup -S dashagent 2>/dev/null || true
        adduser -S -D -H -G dashagent dashagent 2>/dev/null || adduser -S -D -H dashagent 2>/dev/null || adduser -S -D dashagent
    else
        useradd -r -s /usr/sbin/nologin -M dashagent 2>/dev/null || \
        useradd -r -s /usr/sbin/nologin dashagent 2>/dev/null || \
        useradd -r dashagent
    fi
fi
if [ "$DISTRO" = "alpine" ] && ! getent group dashagent >/dev/null 2>&1; then
    addgroup -S dashagent 2>/dev/null || true
    addgroup dashagent dashagent 2>/dev/null || true
fi

# 3. 准备数据目录
mkdir -p /var/lib/dash-agent
chown -R dashagent:dashagent /var/lib/dash-agent 2>/dev/null || chown -R dashagent /var/lib/dash-agent
chmod 0700 /var/lib/dash-agent

mkdir -p /etc/dash-agent

# 4. 获取长期令牌并生成配置文件
echo "--> [3/5] 校验注册状态与长期令牌..."
EXISTING_TOKEN=""
if [ -f /etc/dash-agent/config.json ]; then
    EXISTING_TOKEN=$(sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' /etc/dash-agent/config.json)
fi

AGENT_TOKEN=""
if [ -n "$ENROLL_TOKEN" ]; then
    echo "--> 正在通过注册令牌向服务端申请长期令牌..."
    ENROLL_URL="$ENDPOINT/api/agent/v1/enroll"
    ENROLL_BODY="{\"enroll_token\":\"$ENROLL_TOKEN\"}"

    CURL_K=""
    if [ "$INSECURE" = "1" ] || [ "${ENDPOINT#http://}" != "$ENDPOINT" ]; then
        CURL_K="-k"
    fi

    HTTP_OUT=""
    if command -v curl >/dev/null 2>&1; then
        HTTP_OUT=$(curl -s $CURL_K -w "\nHTTP_STATUS:%{http_code}" \
            -H "Content-Type: application/json" \
            -d "$ENROLL_BODY" \
            "$ENROLL_URL" 2>/dev/null || true)
    elif command -v wget >/dev/null 2>&1; then
        WGET_K=""
        if [ "$INSECURE" = "1" ]; then
            WGET_K="--no-check-certificate"
        fi
        HTTP_OUT=$(wget -q -S -O - $WGET_K \
            --header="Content-Type: application/json" \
            --post-data="$ENROLL_BODY" \
            "$ENROLL_URL" 2>&1 || true)
    fi

    STATUS_CODE=$(printf '%s\n' "$HTTP_OUT" | sed -n 's/^HTTP_STATUS://p')
    RESP_BODY=$(printf '%s\n' "$HTTP_OUT" | sed '/^HTTP_STATUS:/d')

    if [ "$STATUS_CODE" = "200" ]; then
        AGENT_TOKEN=$(printf '%s\n' "$RESP_BODY" | sed -n 's/.*"agent_token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
        echo "✅ 注册成功！长期令牌已签发。"
    elif [ -n "$EXISTING_TOKEN" ]; then
        echo "⚠️  注册令牌已被使用或已过期 (HTTP $STATUS_CODE: $RESP_BODY)。"
        echo "ℹ️  检测到已存在 /etc/dash-agent/config.json，保留现有长期令牌进行升级。"
        AGENT_TOKEN="$EXISTING_TOKEN"
    else
        echo "❌ 注册失败 (HTTP $STATUS_CODE): $RESP_BODY" >&2
        exit 1
    fi
elif [ -n "$EXISTING_TOKEN" ]; then
    echo "ℹ️  未提供 --enroll 注册令牌，检测到已有配置文件，保留现有长期令牌。"
    AGENT_TOKEN="$EXISTING_TOKEN"
else
    echo "❌ 错误: 初次安装必须提供注册令牌: --enroll <token>" >&2
    echo "用法: $0 --enroll <token> [--endpoint <url>]" >&2
    exit 1
fi

INSECURE_FIELD=""
if [ "$INSECURE" = "1" ]; then
    INSECURE_FIELD=",
  \"insecure_skip_verify\": true"
fi

cat > /etc/dash-agent/config.json <<EOF
{
  "endpoint": "$ENDPOINT",
  "token": "$AGENT_TOKEN",
  "interval_fast_s": 5,
  "interval_slow_s": 60,
  "facts_max_interval_s": 1800,
  "collect_conns": true,
  "exec_mode": "off"$INSECURE_FIELD
}
EOF

chown dashagent:dashagent /etc/dash-agent/config.json 2>/dev/null || chown dashagent /etc/dash-agent/config.json
chmod 0600 /etc/dash-agent/config.json

# 5. 配置并启动系统服务
echo "--> [4/5] 安装服务并启动..."
if [ "$INIT_SYSTEM" = "systemd" ]; then
    cat > /etc/systemd/system/dash-agent.service <<'EOF'
[Unit]
Description=dash agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=dashagent
Group=dashagent
ExecStart=/usr/local/bin/dash-agent --config /etc/dash-agent/config.json
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/dash-agent
MemoryMax=64M

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable dash-agent.service
    systemctl restart dash-agent.service
else
    cat > /etc/init.d/dash-agent <<'EOF'
#!/sbin/openrc-run
name="dash-agent"
description="dash agent"
command="/usr/local/bin/dash-agent"
command_args="--config /etc/dash-agent/config.json"
command_user="dashagent:dashagent"
supervisor="supervise-daemon"
respawn_delay=5
respawn_max=0
output_log="/dev/null"
error_log="/dev/null"
depend() { need net; after firewall; }
EOF

    chmod 0755 /etc/init.d/dash-agent
    if [ ! -d /run/openrc ]; then
        mkdir -p /run/openrc
    fi
    if [ ! -f /run/openrc/softlevel ]; then
        touch /run/openrc/softlevel
    fi
    rc-update add dash-agent default 2>/dev/null || true
    rc-service dash-agent restart 2>/dev/null || rc-service dash-agent start 2>/dev/null || true
fi

# 6. 验证服务状态
echo "--> [5/5] 验证 dash-agent 服务运行状态..."
sleep 1
if [ "$INIT_SYSTEM" = "systemd" ]; then
    systemctl is-active dash-agent.service >/dev/null 2>&1 && echo "✅ dash-agent 服务已成功启动并在后台运行 (systemd)。" || {
        echo "⚠️  dash-agent 服务启动检查未就绪，详情如下:" >&2
        systemctl status dash-agent.service --no-pager || true
    }
else
    rc-service dash-agent status 2>/dev/null && echo "✅ dash-agent 服务已成功启动并在后台运行 (OpenRC)。" || {
        echo "⚠️  dash-agent 服务启动检查未就绪。" >&2
    }
fi

echo "🎉 dash-agent 安装完成！"
