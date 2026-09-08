#!/usr/bin/env bash
# lint-region-literals.sh
# 检查 web/src 下是否出现云厂商区域字面量（如 cn-hangzhou, ap-southeast-1 等）。
# 区域是动态数据，禁止在前端代码中硬编码。
# 命中即 exit 1。

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WEB_SRC="${REPO_ROOT}/web/src"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

found=0
output=""

# 匹配主流云区域模式，例如 cn-hangzhou, ap-southeast-1, eu-central-1, us-east-1, me-east-1 等
PATTERN='(cn-|ap-|eu-|us-|me-)[a-z]+-[0-9]'

while IFS= read -r -d '' file; do
  matches=$(grep -nPE "${PATTERN}" "$file" 2>/dev/null || true)
  if [ -n "$matches" ]; then
    count=$(echo "$matches" | wc -l)
    found=$((found + count))
    relpath="${file#$REPO_ROOT/}"
    output+=$'\n'"${YELLOW}${relpath}${NC} (${count} 处)"
    while IFS= read -r line; do
      output+=$'\n'"  ${RED}${line}${NC}"
    done <<< "$matches"
  fi
done < <(find "${WEB_SRC}" -type f \( -name "*.ts" -o -name "*.tsx" -o -name "*.js" -o -name "*.jsx" -o -name "*.css" \) -print0)

if [ "$found" -gt 0 ]; then
  echo -e "${RED}❌ 发现 ${found} 处区域字面量硬编码（禁止在 web/src 中硬编码区域，区域是数据不是常量）：${NC}"
  echo -e "$output"
  exit 1
fi

echo -e "${GREEN}✅ 区域字面量检查通过（web/src 0 处命中）${NC}"
exit 0
