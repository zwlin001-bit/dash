#!/usr/bin/env bash
set -euo pipefail

CONTAINER_NAME="dash-test-mysql"
PORT="${DASH_TEST_DB_PORT:-33306}"
IMAGE="${DASH_TEST_DB_IMAGE:-mysql:8.0}"
MYSQL_ROOT_PASSWORD="${DASH_TEST_DB_PASSWORD:-root}"
MYSQL_DATABASE="${DASH_TEST_DB_NAME:-dash_test}"

cmd="${1:-up}"

# ★ 容器运行时探测：docker 优先，其次 podman。
#   两个都没有时给出可操作的提示，而不是抛一句 "docker: 未找到命令" 就退 127。
if command -v docker >/dev/null 2>&1; then
  RUNTIME=docker
elif command -v podman >/dev/null 2>&1; then
  RUNTIME=podman
else
  cat >&2 <<'NOEOF'
❌ 本机没有 docker 也没有 podman，起不了测试数据库。

可以这样做：
  1. 装一个容器运行时（apt install docker.io / podman），然后重跑 make test
  2. 已经有现成的 MySQL 8 的话，指定端口跳过本脚本：
       DASH_TEST_DB_PORT=<端口> ./scripts/test-runner.sh
  3. 只想先跑不依赖数据库的那部分：
       ./scripts/test-runner.sh
     它会跑完并在末尾醒目列出因缺库而跳过的数量（不会误判为全绿）。
NOEOF
  exit 1
fi
docker() { "$RUNTIME" "$@"; }

check_ready() {
  local max_tries=60
  local count=0
  echo "Waiting for test database on 127.0.0.1:${PORT} to become ready..."
  while ! (echo > /dev/tcp/127.0.0.1/"${PORT}") 2>/dev/null; do
    sleep 0.5
    count=$((count + 1))
    if [ "$count" -ge "$max_tries" ]; then
      echo "❌ Timeout waiting for port ${PORT} to open" >&2
      return 1
    fi
  done

  # Now verify mysql actually accepts queries
  count=0
  while true; do
    if docker exec "$CONTAINER_NAME" mysqladmin ping -h localhost -uroot -p"${MYSQL_ROOT_PASSWORD}" --silent 2>/dev/null; then
      break
    fi
    sleep 0.5
    count=$((count + 1))
    if [ "$count" -ge "$max_tries" ]; then
      echo "❌ Timeout waiting for MySQL to respond to ping" >&2
      return 1
    fi
  done
  echo "✅ Test database on 127.0.0.1:${PORT} is ready."
}

case "$cmd" in
  up)
    if docker ps --filter "name=^/${CONTAINER_NAME}$" --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
      echo "Container ${CONTAINER_NAME} is already running."
      check_ready
      exit 0
    fi

    if docker ps -a --filter "name=^/${CONTAINER_NAME}$" --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
      echo "Starting stopped container ${CONTAINER_NAME}..."
      docker start "${CONTAINER_NAME}" >/dev/null
      check_ready
      exit 0
    fi

    echo "Launching container ${CONTAINER_NAME} (${IMAGE}) on port ${PORT}..."
    docker run -d \
      --name "${CONTAINER_NAME}" \
      -p "${PORT}:3306" \
      -e MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD}" \
      -e MYSQL_DATABASE="${MYSQL_DATABASE}" \
      --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=512m \
      "${IMAGE}" \
      --default-authentication-plugin=mysql_native_password \
      --character-set-server=utf8mb4 \
      --collation-server=utf8mb4_unicode_ci \
      >/dev/null

    check_ready
    ;;

  down)
    echo "Stopping and removing container ${CONTAINER_NAME}..."
    docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true
    echo "✅ Test database stopped and cleaned up."
    ;;

  status)
    if docker ps --filter "name=^/${CONTAINER_NAME}$" --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
      echo "running"
    else
      echo "stopped"
    fi
    ;;

  *)
    echo "Usage: $0 {up|down|status}" >&2
    exit 1
    ;;
esac
