#!/usr/bin/env bash
set -euo pipefail

# scripts/lint-sql.sh —— 方言检查脚本（P1-03 / PRINCIPLES P2）
#
# 扫描 internal/（排除 internal/db/dialect）与 agent/ 下的 .go 文件。
# 任何业务代码出现 Oracle 或 MySQL 方言关键字，立即报错退出非 0。

EXIT_CODE=0

echo "Running SQL dialect portability lint..."

# 整体扫描 internal/（排除 internal/db/dialect）和 agent/ 下的所有 .go 文件
FILES=$(find internal agent -type f -name '*.go' -not -path 'internal/db/dialect/*' 2>/dev/null || true)

if [ -z "$FILES" ]; then
  echo "✅ SQL dialect lint passed: No files to check."
  exit 0
fi

# 1. 绝大多数方言关键字（大小写不敏感）：
# ROWNUM, SYSDATE, NVL(, FROM dual, MERGE INTO, ON DUPLICATE, FETCH NEXT, CONNECT BY
if grep -nE -i '(ROWNUM|SYSDATE|\bNVL\(|FROM[[:space:]]+dual|MERGE[[:space:]]+INTO|ON[[:space:]]+DUPLICATE|FETCH[[:space:]]+NEXT|CONNECT[[:space:]]+BY)' $FILES; then
  EXIT_CODE=1
fi

# 2. DECODE( 函数（排除 Go 方法调用如 .Decode(）
if grep -nE '(^|[^.a-zA-Z0-9_])DECODE\(' $FILES; then
  EXIT_CODE=1
fi

# 3. RETURNING / LIMIT 只匹配大写形式（SQL 关键字惯例大写，避免误报 "returning to caller" / "rate limit exceeded"）
if grep -nE '\b(LIMIT|RETURNING)\b' $FILES; then
  EXIT_CODE=1
fi

if [ $EXIT_CODE -ne 0 ]; then
  echo "" >&2
  echo "❌ SQL dialect lint failed: Non-portable SQL dialect detected outside internal/db/dialect." >&2
  echo "   All business SQL must be portable between Oracle ADB and MySQL 8." >&2
  exit 1
fi

echo "✅ SQL dialect lint passed: No non-portable SQL dialect found."
exit 0
