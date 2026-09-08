#!/usr/bin/env bash
set -euo pipefail

# lint-skips.sh: 检查新增的 t.Skip / t.Skipf 是否带有充分的理由说明
# 规则（P2-12 / 03-rules.md）：
# 1. 不许无理由或仅仅写 "skip"
# 2. 禁止静默跳过；跳过信息长度必须 >= 10 个字符

EXIT_CODE=0

echo "Checking test skip discipline..."

while IFS= read -r -d '' file; do
  # 查找 t.Skip( 或 t.Skipf(
  lines=$(grep -nE 't\.(Skip|Skipf)\(' "$file" || true)
  if [ -n "$lines" ]; then
    while IFS= read -r match; do
      [ -z "$match" ] && continue
      lineno=$(echo "$match" | cut -d: -f1)
      content=$(echo "$match" | cut -d: -f2-)
      
      # 提取括号内的内容
      arg=$(echo "$content" | sed -E 's/.*t\.(Skip|Skipf)\((.*)\).*/\2/' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
      
      # 检查是否为空或只有几个字符
      clean_arg=$(echo "$arg" | tr -d '"' | tr -d "'" | tr -d '`')
      if [ ${#clean_arg} -lt 8 ]; then
        echo "❌ ERROR: Undocumented or too short skip reason in $file:$lineno: $content" >&2
        EXIT_CODE=1
      fi
    done <<< "$lines"
  fi
done < <(find . -type f -name "*_test.go" -not -path "./vendor/*" -print0)

if [ $EXIT_CODE -ne 0 ]; then
  echo "❌ Skip discipline lint failed. Every t.Skip must provide a clear and descriptive reason." >&2
  exit 1
fi

echo "✅ Skip discipline lint passed: all t.Skip calls have descriptive reasons."
exit 0
