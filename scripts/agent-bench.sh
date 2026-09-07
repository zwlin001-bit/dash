#!/usr/bin/env bash
set -euo pipefail

# scripts/agent-bench.sh
# dash-agent 资源预算自动化验收基准测试
# 依据：docs/03-agent.md §2 与 docs/agy/tasks/P1-08-agent主循环.md §资源预算
#
# 指标与预算：
# 1. 常驻 RSS   : 上限 20 MB，目标 12 MB
# 2. 稳态 CPU   : 上限 0.5%（单核，5s 采集间隔），目标 0.2%
# 3. 磁盘写入   : 上限 <= 1 次/分钟，目标 0
# 4. 二进制体积 : 上限 <= 6 MB，目标 5 MB

DURATION="${1:-60}"
if ! [[ "$DURATION" =~ ^[0-9]+$ ]] || [ "$DURATION" -le 0 ]; then
  echo "Usage: $0 [duration_in_seconds] (default: 60)" >&2
  exit 1
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "========================================================================"
echo "          dash-agent Resource Benchmark (Duration: ${DURATION}s)"
echo "========================================================================"

# 1. 编译二进制产物
echo "==> [1/4] Building bin/dash-agent..."
make build-agent > /dev/null

BIN_PATH="$PROJECT_ROOT/bin/dash-agent"
if [ ! -f "$BIN_PATH" ]; then
  echo "❌ ERROR: Binary not found at $BIN_PATH" >&2
  exit 1
fi

BIN_SIZE_BYTES=$(stat -c%s "$BIN_PATH")
BIN_SIZE_MB=$(awk "BEGIN {printf \"%.2f\", $BIN_SIZE_BYTES / 1048576}")
# 6 MB = 6291456 bytes
if [ "$BIN_SIZE_BYTES" -le 6291456 ]; then
  BIN_STATUS="PASS"
else
  BIN_STATUS="FAIL"
fi
echo "    Binary size: ${BIN_SIZE_MB} MB (${BIN_SIZE_BYTES} bytes) -> [${BIN_STATUS}]"

# 2. 启动测试 Mock 服务端
echo "==> [2/4] Starting mock WebSocket server..."
MOCK_LOG="/tmp/bench-mock-$$.log"
go run ./scripts/mock-server.go > "$MOCK_LOG" 2>&1 &
MOCK_PID=$!

PORT=""
for _ in {1..50}; do
  if grep -q "MOCK_SERVER_PORT=" "$MOCK_LOG" 2>/dev/null; then
    PORT=$(grep "MOCK_SERVER_PORT=" "$MOCK_LOG" | tail -n1 | cut -d= -f2)
    break
  fi
  sleep 0.1
done

if [ -z "$PORT" ]; then
  echo "❌ ERROR: Failed to start mock server. Log:" >&2
  cat "$MOCK_LOG" >&2
  kill -9 "$MOCK_PID" 2>/dev/null || true
  exit 1
fi
echo "    Mock server listening on port $PORT"

# 3. 启动 dash-agent 并建立长连接
echo "==> [3/4] Launching dash-agent..."
STATE_FILE="/tmp/dash-agent-bench-state-$$.json"
AGENT_LOG="/tmp/dash-agent-bench-$$.log"
rm -f "$STATE_FILE"

"$BIN_PATH" \
  --endpoint="http://127.0.0.1:$PORT" \
  --token="bench-secret-token" \
  --state-file="$STATE_FILE" \
  --interval-fast=5 \
  --interval-slow=60 \
  --facts-max-interval=1800 \
  > "$AGENT_LOG" 2>&1 &
AGENT_PID=$!

cleanup() {
  kill -15 "$AGENT_PID" 2>/dev/null || true
  kill -15 "$MOCK_PID" 2>/dev/null || true
  wait "$AGENT_PID" 2>/dev/null || true
  wait "$MOCK_PID" 2>/dev/null || true
  rm -f "$STATE_FILE" "$MOCK_LOG" "$AGENT_LOG" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# 等待 2 秒让 agent 完成握手与首次采集，进入稳态
sleep 2

if ! kill -0 "$AGENT_PID" 2>/dev/null; then
  echo "❌ ERROR: Agent crashed on startup. Logs:" >&2
  cat "$AGENT_LOG" >&2
  exit 1
fi

# 读取初始 CPU 与磁盘指标
CLK_TCK=$(getconf CLK_TCK)

get_cpu_ticks() {
  local stat_content
  stat_content=$(cat "/proc/$1/stat" 2>/dev/null || echo "")
  if [ -z "$stat_content" ]; then
    echo "0"
    return
  fi
  local rest="${stat_content##*) }"
  local utime stime
  read -r _ _ _ _ _ _ _ _ _ _ _ utime stime _ <<< "$rest"
  echo "$((utime + stime))"
}

get_write_bytes() {
  local wb
  wb=$(grep "^write_bytes:" "/proc/$1/io" 2>/dev/null | awk '{print $2}' || echo "0")
  [ -z "$wb" ] && wb="0"
  echo "$wb"
}

INIT_TICKS=$(get_cpu_ticks "$AGENT_PID")
INIT_WRITE_BYTES=$(get_write_bytes "$AGENT_PID")

echo "==> [4/4] Sampling resource metrics for ${DURATION} seconds..."

START_TIME=$(date +%s)
ELAPSED=0

while [ "$ELAPSED" -lt "$DURATION" ]; do
  sleep 5
  NOW=$(date +%s)
  ELAPSED=$((NOW - START_TIME))
  if [ "$ELAPSED" -gt "$DURATION" ]; then
    ELAPSED="$DURATION"
  fi
  CURRENT_RSS_KB=$(ps -o rss= -p "$AGENT_PID" 2>/dev/null | tr -d ' ' || echo "0")
  CURRENT_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $CURRENT_RSS_KB / 1024}")
  printf "\r    Progress: %3ds / %3ds | Current RSS: %s MB" "$ELAPSED" "$DURATION" "$CURRENT_RSS_MB"
done
echo ""

# 读取最终统计指标
FINAL_RSS_KB=$(ps -o rss= -p "$AGENT_PID" 2>/dev/null | tr -d ' ' || echo "0")
FINAL_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $FINAL_RSS_KB / 1024}")

FINAL_TICKS=$(get_cpu_ticks "$AGENT_PID")
DELTA_TICKS=$((FINAL_TICKS - INIT_TICKS))
CPU_PCT=$(awk "BEGIN {printf \"%.3f\", (100 * $DELTA_TICKS) / ($CLK_TCK * $DURATION)}")

FINAL_WRITE_BYTES=$(get_write_bytes "$AGENT_PID")
DELTA_WRITE_BYTES=$((FINAL_WRITE_BYTES - INIT_WRITE_BYTES))
WRITES_PER_MIN=$(awk "BEGIN {printf \"%.2f\", ($DELTA_WRITE_BYTES > 0 ? 1 : 0) * 60 / $DURATION}")

# 判定各指标达标情况
# 1. RSS: <= 20 MB (20480 KB)
if [ "$FINAL_RSS_KB" -le 20480 ]; then
  RSS_STATUS="PASS"
else
  RSS_STATUS="FAIL"
fi

# 2. CPU: <= 0.50%
CPU_PASS=$(awk "BEGIN {if ($CPU_PCT <= 0.500) print 1; else print 0}")
if [ "$CPU_PASS" -eq 1 ]; then
  CPU_STATUS="PASS"
else
  CPU_STATUS="FAIL"
fi

# 3. Disk Writes: <= 1.00 / min
DISK_PASS=$(awk "BEGIN {if ($WRITES_PER_MIN <= 1.00) print 1; else print 0}")
if [ "$DISK_PASS" -eq 1 ]; then
  DISK_STATUS="PASS"
else
  DISK_STATUS="FAIL"
fi

# 格式化打印输出
echo ""
echo "========================================================================"
echo "                 dash-agent Resource Benchmark Report"
echo "========================================================================"
echo "Duration Measured : ${DURATION}s"
echo "Target Process PID: ${AGENT_PID}"
echo "System Clock Tick : ${CLK_TCK} Hz"
echo "------------------------------------------------------------------------"
printf "%-16s | %-12s | %-8s | %-12s | %-6s\n" "Metric" "Upper Limit" "Target" "Measured" "Result"
echo "-----------------+--------------+----------+--------------+-------"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Resident RSS" "<= 20 MB" "12 MB" "${FINAL_RSS_MB} MB" "$RSS_STATUS"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Steady CPU" "<= 0.5%" "0.2%" "${CPU_PCT}%" "$CPU_STATUS"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Disk Writes" "<= 1/min" "0" "${WRITES_PER_MIN}/min" "$DISK_STATUS"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Binary Size" "<= 6 MB" "5 MB" "${BIN_SIZE_MB} MB" "$BIN_STATUS"
echo "========================================================================"

if [ "$RSS_STATUS" = "PASS" ] && [ "$CPU_STATUS" = "PASS" ] && [ "$DISK_STATUS" = "PASS" ] && [ "$BIN_STATUS" = "PASS" ]; then
  echo "🎉 OVERALL RESULT: ALL PASS (4/4)"
  exit 0
else
  echo "❌ OVERALL RESULT: FAILED"
  exit 1
fi
