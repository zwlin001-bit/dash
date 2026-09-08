#!/usr/bin/env bash
set -euo pipefail

# 检查契约 fixtures 是否与服务端当前真实响应漂移
go run ./cmd/gen-fixtures --check
