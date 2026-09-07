#!/usr/bin/env bash
set -euo pipefail

# scripts/lint-sql.sh —— 方言检查脚本（P1-03 / PRINCIPLES P2）
#
# 扫描 internal/（排除 internal/db/dialect）与 agent/ 下的 .go 文件。
# 任何业务代码出现 Oracle 或 MySQL 方言关键字，立即报错退出非 0。

EXIT_CODE=0
TARGET_DIRS=("internal" "agent")

echo "Running SQL dialect portability lint..."

# 方言关键字正则：
# ROWNUM | SYSDATE | NVL\( | DECODE\( | FROM dual | MERGE INTO
# ON DUPLICATE | \bLIMIT\b | FETCH NEXT | RETURNING | CONNECT BY

for dir in "${TARGET_DIRS[@]}"; do
  if [ ! -d "$dir" ]; then
    continue
  fi

  while IFS= read -r -d '' file; do
    # 排除 internal/db/dialect 目录
    if [[ "$file" == *"internal/db/dialect/"* ]]; then
      continue
    fi

    # 逐行检查，过滤掉纯注释行
    line_num=0
    while IFS= read -r line || [ -n "$line" ]; do
      line_num=$((line_num + 1))
      
      # 忽略纯注释行
      trimmed=$(echo "$line" | sed -e 's/^[[:space:]]*//')
      if [[ "$trimmed" =~ ^// ]] || [[ "$trimmed" =~ ^\* ]]; then
        continue
      fi

      # 1. 检查绝大多数方言关键字（大小写不敏感）
      # ROWNUM, SYSDATE, NVL(, FROM dual, MERGE INTO, ON DUPLICATE, FETCH NEXT, CONNECT BY
      if echo "$line" | grep -i -E '(ROWNUM|SYSDATE|\bNVL\(|FROM[[:space:]]+dual|MERGE[[:space:]]+INTO|ON[[:space:]]+DUPLICATE|FETCH[[:space:]]+NEXT|CONNECT[[:space:]]+BY)' >/dev/null 2>&1; then
        echo "❌ ERROR: Prohibited SQL dialect found in $file:$line_num:" >&2
        echo "   $line" >&2
        EXIT_CODE=1
        continue
      fi

      # 2. 检查 DECODE(（排除 Go 方法调用如 .Decode(）
      if echo "$line" | grep -i -E '(^|[^.a-zA-Z0-9_])DECODE\(' >/dev/null 2>&1; then
        echo "❌ ERROR: Prohibited SQL dialect (DECODE) found in $file:$line_num:" >&2
        echo "   $line" >&2
        EXIT_CODE=1
        continue
      fi

      # 3. 检查 RETURNING（SQL RETURNING 子句，排除 Go 函数返回语句 return / returning）
      if echo "$line" | grep -i -E '\bRETURNING\b' >/dev/null 2>&1; then
        echo "❌ ERROR: Prohibited SQL dialect (RETURNING) found in $file:$line_num:" >&2
        echo "   $line" >&2
        EXIT_CODE=1
        continue
      fi

      # 4. 检查 LIMIT
      # 大写 LIMIT 直接报错；在引号字符串内的 limit/LIMIT 报错；后面跟数字/?/: 的 limit 报错
      if echo "$line" | grep -E '\bLIMIT\b' >/dev/null 2>&1 || \
         echo "$line" | grep -i -E '["`].*\blimit\b.*["`]' >/dev/null 2>&1 || \
         echo "$line" | grep -i -E '\blimit[[:space:]]+([0-9]+|\?|:)' >/dev/null 2>&1; then
        # 排除 Go 变量声明参数列表如 limit, offset int
        if [[ "$line" =~ limit,[[:space:]]*offset[[:space:]]+int ]]; then
          continue
        fi
        echo "❌ ERROR: Prohibited SQL dialect (LIMIT) found in $file:$line_num:" >&2
        echo "   $line" >&2
        EXIT_CODE=1
        continue
      fi

    done < "$file"
  done < <(find "$dir" -type f -name "*.go" -print0)
done

if [ $EXIT_CODE -ne 0 ]; then
  echo "" >&2
  echo "❌ SQL dialect lint failed: Non-portable SQL dialect detected outside internal/db/dialect." >&2
  echo "   All business SQL must be portable between Oracle ADB and MySQL 8." >&2
  exit 1
fi

echo "✅ SQL dialect lint passed: No non-portable SQL dialect found."
exit 0
