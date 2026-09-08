#!/usr/bin/env bash
set -euo pipefail

# scripts/lint-dist.sh
# 检查前端源码与 internal/api/dist/ 内嵌产物的一致性
# 约束：docs/agy/tasks/P1-25-前端产物一致性守卫.md

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

DIST_DIR="internal/api/dist"
FP_FILE="$DIST_DIR/.build-fingerprint"

compute_fingerprint() {
  find web/src web/index.html web/vite.config.ts web/tsconfig.json \
       web/package.json web/package-lock.json -type f 2>/dev/null \
    | sort | xargs sha256sum | sha256sum | cut -c1-16
}

if [[ "${1:-}" == "--write" || "${1:-}" == "-w" || "${1:-}" == "--update" ]]; then
  mkdir -p "$DIST_DIR"
  FP=$(compute_fingerprint)
  echo "$FP" > "$FP_FILE"
  echo "✅ Updated $FP_FILE to $FP"
  exit 0
fi

SRC_FP=$(compute_fingerprint)
RECORDED_FP=""
if [[ -f "$FP_FILE" ]]; then
  RECORDED_FP=$(tr -d '[:space:]' < "$FP_FILE")
fi

if [[ -z "$RECORDED_FP" || "$SRC_FP" != "$RECORDED_FP" ]]; then
  echo "❌ 前端产物与源码不一致" >&2
  echo "   web/src 指纹: ${SRC_FP}" >&2
  echo "   dist 记录的:  ${RECORDED_FP:-(缺失)}" >&2
  if command -v npm >/dev/null 2>&1; then
    echo "   请执行: make build-web" >&2
  else
    echo "   本机无 npm，请在有 Node 的机器上构建 (make build-web) 后提交产物" >&2
  fi
  exit 1
fi

echo "✅ 前端产物与源码指纹一致 ($SRC_FP)"
exit 0
