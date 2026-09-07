#!/usr/bin/env bash
set -euo pipefail

# scripts/dashd-bench.sh
# dashd 服务端资源预算自动化验收基准测试
# 依据：docs/agy/tasks/P1-21-dashd主程序装配.md §服务端资源预算
#
# 指标与预算：
# 1. 稳态 RSS   : 上限 <= 1 GB (1024 MB)，30 台 agent 在线正常上报
# 2. 峰值 RSS   : 上限 <= 1.5 GB (1536 MB)
# 3. 健康检查   : 200 OK

DURATION="${1:-60}"
if ! [[ "$DURATION" =~ ^[0-9]+$ ]] || [ "$DURATION" -le 0 ]; then
  echo "Usage: $0 [duration_in_seconds] (default: 60)" >&2
  exit 1
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "========================================================================"
echo "          dashd Server Resource Benchmark (Duration: ${DURATION}s)"
echo "========================================================================"

# 1. 编译 dashd 二进制产物
echo "==> [1/4] Building bin/dashd..."
make build-dashd > /dev/null

BIN_PATH="$PROJECT_ROOT/bin/dashd"
if [ ! -f "$BIN_PATH" ]; then
  echo "❌ ERROR: Binary not found at $BIN_PATH" >&2
  exit 1
fi

# 2. 准备自举配置文件
echo "==> [2/4] Setting up test configuration and database connectivity..."
BENCH_PORT=18088
CONFIG_FILE="/tmp/dashd-bench-cfg-$$.toml"
DATA_DIR="/tmp/dashd-bench-data-$$"
MASTER_KEY="/tmp/dashd-bench-master-$$.key"
LOG_FILE="/tmp/dashd-bench-$$.log"

mkdir -p "$DATA_DIR"
rm -f "$MASTER_KEY" "$CONFIG_FILE" "$LOG_FILE"

# 默认优先探测本地 MySQL (端口 3306 或 33306)，亦允许通过 DASH_DB_DSN 覆盖
DB_DRIVER="${DASH_DB_DRIVER:-mysql}"
DB_USER="${DASH_DB_USER:-root}"
DB_PASSWORD="${DASH_DB_PASSWORD:-root}"
if [ -n "${DASH_DB_DSN:-}" ]; then
  DB_DSN="$DASH_DB_DSN"
elif (echo > /dev/tcp/127.0.0.1/3306) 2>/dev/null; then
  DB_DSN="root:root@tcp(127.0.0.1:3306)/dash_test?parseTime=true"
else
  DB_DSN="root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true"
fi

cat << EOF > "$CONFIG_FILE"
[db]
driver = "${DB_DRIVER}"
user = "${DB_USER}"
password = "${DB_PASSWORD}"
dsn = "${DB_DSN}"
max_open_conns = 20
max_idle_conns = 10
conn_max_lifetime_s = 1800

[server]
listen = "127.0.0.1:${BENCH_PORT}"
data_dir = "${DATA_DIR}"
master_key = "${MASTER_KEY}"
EOF

# 3. 启动 dashd 服务端
echo "==> [3/4] Starting dashd server on port ${BENCH_PORT}..."
"$BIN_PATH" -config "$CONFIG_FILE" > "$LOG_FILE" 2>&1 &
DASHD_PID=$!

MOCK_PID=""
cleanup() {
  if [ -n "$MOCK_PID" ]; then
    kill -15 "$MOCK_PID" 2>/dev/null || true
    wait "$MOCK_PID" 2>/dev/null || true
  fi
  if [ -n "$DASHD_PID" ]; then
    kill -15 "$DASHD_PID" 2>/dev/null || true
    wait "$DASHD_PID" 2>/dev/null || true
  fi
  rm -f "$CONFIG_FILE" "$MASTER_KEY" "$LOG_FILE" 2>/dev/null || true
  rm -rf "$DATA_DIR" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# 等待 /healthz 就绪
READY=0
for _ in {1..30}; do
  if curl -s -f "http://127.0.0.1:${BENCH_PORT}/healthz" >/dev/null 2>&1; then
    READY=1
    break
  fi
  sleep 0.2
done

if [ "$READY" -ne 1 ]; then
  echo "❌ ERROR: dashd failed to start. Logs:" >&2
  cat "$LOG_FILE" >&2
  exit 1
fi
echo "    dashd ready at http://127.0.0.1:${BENCH_PORT}"

# 启动 30 个模拟 Agent 并维持长连接上报
NUM_AGENTS=30
echo "    Spawning ${NUM_AGENTS} mock agents..."
MOCK_BIN="$PROJECT_ROOT/bin/mock-agents"
if [ ! -f "$MOCK_BIN" ]; then
  go build -o "$MOCK_BIN" ./scripts/mock-agents
fi

MOCK_LOG="/tmp/mock-agents-$$.log"
"$MOCK_BIN" \
  -endpoint="http://127.0.0.1:${BENCH_PORT}" \
  -dsn="${DB_DSN}" \
  -nodes="$NUM_AGENTS" \
  -interval=5s \
  -duration="$((DURATION + 30))s" \
  > "$MOCK_LOG" 2>&1 &
MOCK_PID=$!

# 等待 Agent 上线就绪
AGENTS_READY=0
for _ in {1..50}; do
  ONLINE_AGENTS=$(curl -s "http://127.0.0.1:${BENCH_PORT}/healthz" 2>/dev/null | grep -o '"agents_online":[0-9]*' | cut -d: -f2 || true)
  ONLINE_AGENTS="${ONLINE_AGENTS:-0}"
  if [ "$ONLINE_AGENTS" -ge "$NUM_AGENTS" ] 2>/dev/null; then
    AGENTS_READY=1
    echo "    All ${NUM_AGENTS} agents connected and reporting (online: ${ONLINE_AGENTS})"
    break
  fi
  sleep 0.5
done

if [ "$AGENTS_READY" -ne 1 ]; then
  echo "⚠️ Warning: expected ${NUM_AGENTS} agents, currently online: ${ONLINE_AGENTS:-0}"
fi

# 4. 采样 dashd 资源指标
echo "==> [4/4] Sampling dashd memory metrics for ${DURATION} seconds..."

START_TIME=$(date +%s)
ELAPSED=0
PEAK_RSS_KB=0
SUM_RSS_KB=0
SAMPLES=0

while [ "$ELAPSED" -lt "$DURATION" ]; do
  sleep 5
  NOW=$(date +%s)
  ELAPSED=$((NOW - START_TIME))
  if [ "$ELAPSED" -gt "$DURATION" ]; then
    ELAPSED="$DURATION"
  fi

  CURRENT_RSS_KB=$(ps -o rss= -p "$DASHD_PID" 2>/dev/null | tr -d ' ' || echo "0")
  [ -z "$CURRENT_RSS_KB" ] && CURRENT_RSS_KB=0

  if [ "$CURRENT_RSS_KB" -gt "$PEAK_RSS_KB" ]; then
    PEAK_RSS_KB="$CURRENT_RSS_KB"
  fi

  SUM_RSS_KB=$((SUM_RSS_KB + CURRENT_RSS_KB))
  SAMPLES=$((SAMPLES + 1))

  CURRENT_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $CURRENT_RSS_KB / 1024}")
  PEAK_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $PEAK_RSS_KB / 1024}")
  printf "\r    Progress: %3ds / %3ds | Current RSS: %s MB | Peak RSS: %s MB" "$ELAPSED" "$DURATION" "$CURRENT_RSS_MB" "$PEAK_RSS_MB"
done
echo ""

# 判定指标达标情况
FINAL_RSS_KB=$(ps -o rss= -p "$DASHD_PID" 2>/dev/null | tr -d ' ' || echo "0")
[ -z "$FINAL_RSS_KB" ] && FINAL_RSS_KB=0

AVG_RSS_KB=$((SUM_RSS_KB / (SAMPLES > 0 ? SAMPLES : 1)))
STEADY_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $AVG_RSS_KB / 1024}")
PEAK_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $PEAK_RSS_KB / 1024}")

# 1. 稳态 RSS 上限 1 GB = 1048576 KB
if [ "$AVG_RSS_KB" -le 1048576 ]; then
  STEADY_STATUS="PASS"
else
  STEADY_STATUS="FAIL"
fi

# 2. 峰值 RSS 上限 1.5 GB = 1572864 KB
if [ "$PEAK_RSS_KB" -le 1572864 ]; then
  PEAK_STATUS="PASS"
else
  PEAK_STATUS="FAIL"
fi

# 3. 健康检查状态
HEALTH_HTTP=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${BENCH_PORT}/healthz" 2>/dev/null || echo "000")
if [ "$HEALTH_HTTP" = "200" ]; then
  HEALTH_STATUS="PASS"
else
  HEALTH_STATUS="FAIL"
fi

# 格式化报告
echo ""
echo "========================================================================"
echo "                 dashd Server Resource Benchmark Report"
echo "========================================================================"
echo "Duration Measured : ${DURATION}s"
echo "Target Process PID: ${DASHD_PID}"
echo "Simulated Agents  : ${NUM_AGENTS}"
echo "------------------------------------------------------------------------"
printf "%-16s | %-12s | %-8s | %-12s | %-6s\n" "Metric" "Upper Limit" "Target" "Measured" "Result"
echo "-----------------+--------------+----------+--------------+-------"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Steady RSS" "<= 1024 MB" "500 MB" "${STEADY_RSS_MB} MB" "$STEADY_STATUS"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Peak RSS" "<= 1536 MB" "800 MB" "${PEAK_RSS_MB} MB" "$PEAK_STATUS"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Health Check" "200 OK" "200 OK" "${HEALTH_HTTP} OK" "$HEALTH_STATUS"
echo "========================================================================"

rm -f "$MOCK_LOG" 2>/dev/null || true

if [ "$STEADY_STATUS" = "PASS" ] && [ "$PEAK_STATUS" = "PASS" ] && [ "$HEALTH_STATUS" = "PASS" ]; then
  echo "🎉 OVERALL RESULT: ALL PASS (3/3)"
  exit 0
else
  echo "❌ OVERALL RESULT: FAILED"
  exit 1
fi
