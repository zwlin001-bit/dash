#!/usr/bin/env bash
set -euo pipefail

# scripts/lint-dist-test.sh
# 验证 lint-dist.sh 的指纹计算具备 locale 不变性（C / POSIX / en_US / zh_CN 等）

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "==> Testing lint-dist.sh locale and environment invariance..."

LOCALES=("C" "POSIX" "C.UTF-8" "en_US.UTF-8" "zh_CN.UTF-8")
BASELINE=""

for loc in "${LOCALES[@]}"; do
    FP=$(LC_ALL="$loc" LANG="$loc" LC_COLLATE="$loc" ./scripts/lint-dist.sh --print 2>/dev/null)
    if [ -z "$BASELINE" ]; then
        BASELINE="$FP"
        echo "  [baseline] locale=$loc -> $BASELINE"
    else
        echo "  [check]    locale=$loc -> $FP"
        if [ "$FP" != "$BASELINE" ]; then
            echo "❌ Fingerprint mismatch under locale $loc: expected $BASELINE, got $FP" >&2
            exit 1
        fi
    fi
done

echo "✅ lint-dist.sh locale invariance verified successfully ($BASELINE)"
