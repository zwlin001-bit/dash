#!/usr/bin/env bash
set -euo pipefail

# scripts/dashd-bench.sh
# dashd 服务端资源预算自动化基准测试
# 依据：docs/agy/tasks/P1-21-dashd主程序装配.md §服务端资源预算 与 P1-19
#
# 指标与预算：
# 1. 稳态 RSS : 上限 1024 MB (1 GB)，目标 512 MB
# 2. 峰值 RSS : 上限 1536 MB (1.5 GB)，目标 768 MB

DURATION="${1:-10}"
if ! [[ "$DURATION" =~ ^[0-9]+$ ]] || [ "$DURATION" -le 0 ]; then
  echo "Usage: $0 [duration_in_seconds] (default: 10)" >&2
  exit 1
fi

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "========================================================================"
echo "          dashd Server Resource Benchmark (Duration: ${DURATION}s)"
echo "========================================================================"

# 1. 编译服务端二进制
echo "==> [1/3] Building bin/dashd..."
make build-dashd > /dev/null

BIN_PATH="$PROJECT_ROOT/bin/dashd"
if [ ! -f "$BIN_PATH" ]; then
  echo "❌ ERROR: Binary not found at $BIN_PATH" >&2
  exit 1
fi

# 2. 启动基准测试模式下的 dashd
echo "==> [2/3] Launching dashd in bench mode..."
BENCH_DIR="/tmp/dashd-bench-$$"
mkdir -p "$BENCH_DIR"
CFG_FILE="$BENCH_DIR/config.toml"
LOG_FILE="$BENCH_DIR/dashd.log"
PORT=28080

cat > "$CFG_FILE" <<CFG_EOF
[db]
driver = "oracle"
user = ""
password = ""
dsn = ""

[server]
listen = "127.0.0.1:${PORT}"
data_dir = "$BENCH_DIR/data"
master_key = "$BENCH_DIR/master.key"
CFG_EOF

head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$BENCH_DIR/master.key"

"$BIN_PATH" -config "$CFG_FILE" serve > "$LOG_FILE" 2>&1 &
DASHD_PID=$!

cleanup() {
  kill -15 "$DASHD_PID" 2>/dev/null || true
  wait "$DASHD_PID" 2>/dev/null || true
  rm -rf "$BENCH_DIR" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# 等待 /healthz 就绪
HEALTH_OK=0
for _ in {1..30}; do
  if curl -s "http://127.0.0.1:${PORT}/healthz" | grep -q '"status"'; then
    HEALTH_OK=1
    break
  fi
  sleep 0.2
done

if [ "$HEALTH_OK" -ne 1 ]; then
  echo "❌ ERROR: dashd failed to start or pass /healthz. Log:" >&2
  cat "$LOG_FILE" >&2
  exit 1
fi

echo "    dashd listening on port $PORT (PID: $DASHD_PID)"

# 3. 采样资源指标
echo "==> [3/3] Sampling resource metrics for ${DURATION} seconds..."
START_TIME=$(date +%s)
ELAPSED=0
PEAK_RSS_KB=0

while [ "$ELAPSED" -lt "$DURATION" ]; do
  sleep 1
  NOW=$(date +%s)
  ELAPSED=$((NOW - START_TIME))
  if [ "$ELAPSED" -gt "$DURATION" ]; then
    ELAPSED="$DURATION"
  fi
  CURRENT_RSS_KB=$(ps -o rss= -p "$DASHD_PID" 2>/dev/null | tr -d ' ' || echo "0")
  if [ -n "$CURRENT_RSS_KB" ] && [ "$CURRENT_RSS_KB" -gt "$PEAK_RSS_KB" ]; then
    PEAK_RSS_KB="$CURRENT_RSS_KB"
  fi
  CURRENT_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $CURRENT_RSS_KB / 1024}")
  PEAK_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $PEAK_RSS_KB / 1024}")
  printf "\r    Progress: %3ds / %3ds | Current RSS: %s MB | Peak RSS: %s MB" "$ELAPSED" "$DURATION" "$CURRENT_RSS_MB" "$PEAK_RSS_MB"
done
echo ""

FINAL_RSS_KB=$(ps -o rss= -p "$DASHD_PID" 2>/dev/null | tr -d ' ' || echo "0")
FINAL_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $FINAL_RSS_KB / 1024}")
PEAK_RSS_MB=$(awk "BEGIN {printf \"%.2f\", $PEAK_RSS_KB / 1024}")

# 判定: 稳态 <= 1024 MB (1048576 KB), 峰值 <= 1536 MB (1572864 KB)
if [ "$FINAL_RSS_KB" -le 1048576 ]; then
  RSS_STATUS="PASS"
else
  RSS_STATUS="FAIL"
fi

if [ "$PEAK_RSS_KB" -le 1572864 ]; then
  PEAK_STATUS="PASS"
else
  PEAK_STATUS="FAIL"
fi

echo ""
echo "========================================================================"
echo "                 dashd Resource Benchmark Report"
echo "========================================================================"
echo "Duration Measured : ${DURATION}s"
echo "Target Process PID: ${DASHD_PID}"
echo "------------------------------------------------------------------------"
printf "%-16s | %-12s | %-8s | %-12s | %-6s\n" "Metric" "Upper Limit" "Target" "Measured" "Result"
echo "-----------------+--------------+----------+--------------+-------"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Resident RSS" "<= 1024 MB" "512 MB" "${FINAL_RSS_MB} MB" "$RSS_STATUS"
printf "%-16s | %-12s | %-8s | %-12s | [%s]\n" "Peak RSS" "<= 1536 MB" "768 MB" "${PEAK_RSS_MB} MB" "$PEAK_STATUS"
echo "========================================================================"

if [ "$RSS_STATUS" = "PASS" ] && [ "$PEAK_STATUS" = "PASS" ]; then
  echo "🎉 OVERALL RESULT: ALL PASS (2/2)"
  exit 0
else
  echo "❌ OVERALL RESULT: FAILED"
  exit 1
fi
