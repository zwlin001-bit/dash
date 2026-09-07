#!/usr/bin/env bash
set -euo pipefail

# scripts/verify-agent-matrix.sh
# 验证 Agent 静态构建与 Alpine / Debian / Ubuntu 三系统兼容性
# 依据：docs/agy/tasks/P1-09-agent构建.md

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"

echo "========================================================================"
echo "         dash-agent Static Build & Multi-Distro Verification"
echo "========================================================================"

# 1. 静态构建所有架构
echo "==> [1/5] Building all agent binaries (amd64, arm64, armv7)..."
make build-agent-all

# 2. 验证 ldd 静态链接性 (CGO_ENABLED=0)
echo "==> [2/5] Checking static binary linkage (ldd)..."
LDD_OUTPUT=$(LC_ALL=C ldd "$PROJECT_ROOT/bin/dash-agent-linux-amd64" 2>&1 || true)
echo "    ldd output: $LDD_OUTPUT"
if echo "$LDD_OUTPUT" | grep -q "not a dynamic executable"; then
  echo "    Linkage check: [PASS] (Fully static, no dynamic libc dependencies)"
else
  echo "❌ ERROR: Binary is not statically linked!" >&2
  exit 1
fi

# 3. 验证二进制体积预算 (<= 6 MB)
echo "==> [3/5] Checking binary size limits (<= 6 MB)..."
MAX_BYTES=6291456 # 6 MB = 6 * 1024 * 1024

for arch in amd64 arm64 armv7; do
  bin_file="$PROJECT_ROOT/bin/dash-agent-linux-$arch"
  if [ ! -f "$bin_file" ]; then
    echo "❌ ERROR: File not found: $bin_file" >&2
    exit 1
  fi
  size_bytes=$(stat -c%s "$bin_file")
  size_mb=$(awk "BEGIN {printf \"%.2f\", $size_bytes / 1048576}")
  if [ "$size_bytes" -le "$MAX_BYTES" ]; then
    echo "    - dash-agent-linux-$arch: ${size_mb} MB (${size_bytes} bytes) -> [PASS]"
  else
    echo "❌ ERROR: dash-agent-linux-$arch exceeds 6 MB: ${size_mb} MB" >&2
    exit 1
  fi
done

# 4. 验证 sha256 校验和
echo "==> [4/5] Verifying sha256sums.txt..."
(cd "$PROJECT_ROOT/bin" && sha256sum -c sha256sums.txt)
echo "    sha256sums.txt: [PASS]"

# 5. 三系统 Docker 实测验证
echo "==> [5/5] Testing execution in Alpine, Debian 12, and Ubuntu 24.04..."

# Alpine (musl libc)
echo "    [1/3] Alpine (musl libc):"
ALPINE_OUT=$(docker run --rm -v "$PROJECT_ROOT/bin":/x alpine /x/dash-agent-linux-amd64 --version)
echo "          $ALPINE_OUT"

# Debian 12 (glibc)
echo "    [2/3] Debian 12 (glibc):"
DEBIAN_OUT=$(docker run --rm -v "$PROJECT_ROOT/bin":/x debian:12 /x/dash-agent-linux-amd64 --version)
echo "          $DEBIAN_OUT"

# Ubuntu 24.04 (glibc)
echo "    [3/3] Ubuntu 24.04 (glibc):"
UBUNTU_OUT=$(docker run --rm -v "$PROJECT_ROOT/bin":/x ubuntu:24.04 /x/dash-agent-linux-amd64 --version)
echo "          $UBUNTU_OUT"

echo "========================================================================"
echo "🎉 ALL CHECKS PASSED: P1-09 Agent static build & multi-distro verified!"
echo "========================================================================"
