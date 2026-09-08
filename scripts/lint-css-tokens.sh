#!/usr/bin/env bash
# lint-css-tokens.sh
# 扫描两类字面量颜色违规：
#   1. web/src/**/*.module.css 里的字面量颜色值（tokens.css 本身豁免）
#   2. web/src/**/*.tsx 里内联 style={{ }} 中的字面量颜色值
#
# 命中即 exit 1。
#
# 不匹配：transparent / currentColor / inherit / none
# 不许行内注释豁免（不支持 lint-ignore）
#
# 用法：
#   ./scripts/lint-css-tokens.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WEB_SRC="${REPO_ROOT}/web/src"
TOKENS_FILE="${WEB_SRC}/styles/tokens.css"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

found=0
output=""

# ─── ① 扫 *.module.css（排除 tokens.css 本身）───────────────────────────────
while IFS= read -r -d '' file; do
  if [ "$file" = "$TOKENS_FILE" ]; then
    continue
  fi

  matches=$(grep -nP '(^|[\s:,\(])#[0-9a-fA-F]{3,8}\b|rgba?\s*\(|hsla?\s*\(' "$file" 2>/dev/null || true)
  if [ -n "$matches" ]; then
    count=$(echo "$matches" | wc -l)
    found=$((found + count))
    relpath="${file#$REPO_ROOT/}"
    output+=$'\n'"${YELLOW}${relpath}${NC} (${count} 处)"
    while IFS= read -r line; do
      output+=$'\n'"  ${RED}${line}${NC}"
    done <<< "$matches"
  fi
done < <(find "${WEB_SRC}" -name "*.module.css" -print0)

# ─── ② 扫 *.tsx 里 style={{ }} 中的字面量颜色 ────────────────────────────────
# 策略：找出含有 style={{ ... }} 的行，且该行包含字面量颜色值（不含 var(）
# 使用 python3 做多行 AST 级别的搜索太重，这里做保守的逐行扫描：
#   - 行内有 style= 且有字面量颜色
#   - 或者行内有字面量颜色但不在注释里（// 开头的行排除）
#
# 实际上，TSX 里唯一合理出现字面量颜色的地方是：
#   a) 图表颜色数组（这些是用户可见的数据，读自 CSS 变量后用，fallback 除外）
#   b) color picker 的 preset 颜色数据（不是 style=，是 data）
#
# 所以只扫含有 style= 关键字的行：
while IFS= read -r -d '' file; do
  # 只看含 style={ 的行里是否有字面量颜色（而不是 var(--...)）
  matches=$(grep -nP 'style\s*=\s*\{' "$file" 2>/dev/null | \
    grep -P '#[0-9a-fA-F]{3,8}|rgba?\s*\(|hsla?\s*\(' || true)
  # 排除 style= 行里只用 var(--...) 的（已安全的写法）
  # 进一步过滤：排除行内没有任何字面量颜色、只有 var() 的行
  if [ -n "$matches" ]; then
    # 再过滤掉只包含 var(--...) 而无裸字面量的行
    safe_matches=""
    while IFS= read -r line; do
      # 去掉所有 var(...) 调用后，检查剩余是否还有 # 或 rgb(
      stripped=$(echo "$line" | sed 's/var([^)]*)/VAR/g')
      if echo "$stripped" | grep -qP '#[0-9a-fA-F]{3,8}|rgba?\s*\(|hsla?\s*\('; then
        safe_matches+="${line}"$'\n'
      fi
    done <<< "$matches"

    if [ -n "$safe_matches" ]; then
      count=$(echo "$safe_matches" | grep -c . || true)
      found=$((found + count))
      relpath="${file#$REPO_ROOT/}"
      output+=$'\n'"${YELLOW}${relpath}${NC} (${count} 处 inline style)"
      while IFS= read -r line; do
        [ -n "$line" ] && output+=$'\n'"  ${RED}${line}${NC}"
      done <<< "$safe_matches"
    fi
  fi
done < <(find "${WEB_SRC}" -name "*.tsx" -print0)

# ─── 结果 ────────────────────────────────────────────────────────────────────
if [ "$found" -gt 0 ]; then
  echo -e "${RED}✖ lint-css-tokens: 发现 ${found} 处字面量颜色值${NC}"
  echo -e "  所有颜色必须走 var(--token)。字面量只允许出现在 tokens.css 本身。"
  echo -e "${output}"
  exit 1
fi

echo -e "${GREEN}✔ lint-css-tokens: 0 处字面量颜色值，全部走 token。${NC}"
exit 0
