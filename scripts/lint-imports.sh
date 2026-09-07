#!/usr/bin/env bash
set -euo pipefail

# 检查 agent/** 和 cmd/dash-agent 是否违规引入了服务端 internal 包
# 约束（P1-01 / PRINCIPLES P3）：
# agent 侧只允许 import internal/protocol，绝对禁止直接或间接依赖 internal/db, internal/api,
# internal/control, internal/ingest, internal/inventory 等服务端包。

EXIT_CODE=0
TARGET_DIRS=("agent" "cmd/dash-agent")

echo "Running agent import discipline lint..."

# 1. 第一道：源码级直接 import 检查（给出具体文件行号，定位更友好）
for dir in "${TARGET_DIRS[@]}"; do
  if [ ! -d "$dir" ]; then
    continue
  fi

  while IFS= read -r -d '' file; do
    # 查找任何包含 internal/ 但不是 internal/protocol 的 import 行（排除纯注释行）
    matches=$(grep -nE '["`][^"`]*internal/[^"`]*["`]' "$file" 2>/dev/null | grep -Ev '^[0-9]+:[[:space:]]*//' | grep -v 'internal/protocol' || true)
    if [ -n "$matches" ]; then
      echo "❌ ERROR: Prohibited direct server internal import found in $file:" >&2
      echo "$matches" >&2
      EXIT_CODE=1
    fi
  done < <(find "$dir" -type f -name "*.go" -print0)
done

# 2. 第二道：传递依赖检查（go list -deps 展开全部依赖树，防止间接依赖泄漏）
FORBIDDEN=$(go list -deps ./agent/... ./cmd/dash-agent 2>/dev/null \
  | grep -E '^dash/internal/' \
  | grep -v '^dash/internal/protocol$' || true)
if [ -n "$FORBIDDEN" ]; then
  echo "❌ ERROR: Transitive server internal dependencies detected in agent packages:" >&2
  echo "$FORBIDDEN" >&2
  EXIT_CODE=1
fi

if [ $EXIT_CODE -ne 0 ]; then
  echo "" >&2
  echo "❌ Lint failed: agent/** and cmd/dash-agent must not depend on server internal packages (only internal/protocol is permitted)." >&2
  exit 1
fi

echo "✅ Import discipline lint passed: no forbidden internal imports in agent/** or cmd/dash-agent."
exit 0
