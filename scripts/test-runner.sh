#!/usr/bin/env bash
set -eo pipefail

# test-runner.sh:
# 1. 运行测试
# 2. 如果跳过了 DB 测试，打印醒目汇总
# 3. 如果设置了 DASH_REQUIRE_TEST_DB=1，当存在由于缺少 DB 而跳过的测试时直接退出非 0

OUTPUT_FILE="$(mktemp)"
trap 'rm -f "$OUTPUT_FILE"' EXIT

set +e
go test -p=1 -v ./... 2>&1 | tee "$OUTPUT_FILE"
TEST_EXIT_CODE=$?
set -e

# 统计跳过情况
SKIPS=$(grep "^--- SKIP:" "$OUTPUT_FILE" || true)
TOTAL_SKIPS=$(grep -c "^--- SKIP:" "$OUTPUT_FILE" || true)

# 区分哪些跳过是因为缺少测试数据库（MySQL 33306 / DB）
# 排除掉已知的合理跳过（例如 Oracle ADB 测试跳过，因为 Oracle 本地无法运行）
DB_SKIPS=$(grep -E "(cannot connect to.*33306|MySQL.*not (available|accessible)|cannot connect to test MySQL|cannot connect to MySQL|skipping test requiring database)" "$OUTPUT_FILE" || true)
DB_SKIP_COUNT=0
if [ -n "$DB_SKIPS" ]; then
  DB_SKIP_COUNT=$(echo "$DB_SKIPS" | grep -c . || true)
fi

echo ""
if [ "$DB_SKIP_COUNT" -gt 0 ]; then
  echo "================================================================================"
  echo "⚠️  本次有 ${DB_SKIP_COUNT} 个测试因缺少测试数据库被跳过！"
  echo "   请跑 'make test-db-up' 启动测试数据库后再试。"
  echo "================================================================================"
  
  if [ "${DASH_REQUIRE_TEST_DB:-0}" = "1" ]; then
    echo "❌ 错误: DASH_REQUIRE_TEST_DB=1 已设置，禁止因缺少数据库而跳过测试！" >&2
    exit 1
  fi
fi

if [ $TEST_EXIT_CODE -ne 0 ]; then
  exit $TEST_EXIT_CODE
fi

exit 0
