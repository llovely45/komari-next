#!/bin/sh
set -eu

# Redis is intentionally private to this container. komari-next always connects to
# redis://127.0.0.1:6379/0, so no host port or external Redis URL is needed.
redis-server \
  --bind 127.0.0.1 \
  --port 6379 \
  --protected-mode yes \
  --save "" \
  --appendonly no \
  --daemonize no &
redis_pid=$!

/app/komari server &
komari_pid=$!

cleanup() {
  kill "$komari_pid" "$redis_pid" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

set +e
wait "$komari_pid"
status=$?
set -e

kill "$redis_pid" 2>/dev/null || true
wait "$redis_pid" 2>/dev/null || true
exit "$status"
