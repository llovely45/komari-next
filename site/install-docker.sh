#!/usr/bin/env bash

set -Eeuo pipefail

readonly APP_NAME="komari-next"
readonly INSTALL_DIR="${KOMARI_INSTALL_DIR:-/opt/komari-next}"
readonly COMPOSE_FILE="$INSTALL_DIR/compose.yaml"
readonly ENV_FILE="$INSTALL_DIR/.env"
readonly DEFAULT_IMAGE="ghcr.io/llovely45/komari-next:1.0.2"
readonly DEFAULT_PORT="25775"

log() {
    printf '[%s] %s\n' "$APP_NAME" "$*"
}

die() {
    printf '[%s] 错误：%s\n' "$APP_NAME" "$*" >&2
    exit 1
}

generate_password() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 24
        return
    fi

    od -An -N24 -tx1 /dev/urandom | tr -d '[:space:]'
}

if [[ "$(id -u)" -ne 0 ]]; then
    die '请使用 root 运行：sudo bash install-docker.sh'
fi

command -v docker >/dev/null 2>&1 || die '未找到 Docker，请先安装并启动 Docker。'
docker info >/dev/null 2>&1 || die 'Docker daemon 不可用，请先启动 Docker。'

if docker compose version >/dev/null 2>&1; then
    COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE=(docker-compose)
else
    die '未找到 Docker Compose，请安装 Compose plugin 后重试。'
fi

mkdir -p "$INSTALL_DIR/data"
chmod 0750 "$INSTALL_DIR/data"

# Re-running the installer must preserve the existing PostgreSQL password.
KOMARI_IMAGE="${KOMARI_IMAGE:-$DEFAULT_IMAGE}"
KOMARI_PORT="${KOMARI_PORT:-$DEFAULT_PORT}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-}"

if [[ -f "$ENV_FILE" ]]; then
    # The file is created with mode 0600 below and is intended to be managed
    # only by root. It contains only shell-compatible KEY=value assignments.
    # shellcheck disable=SC1090
    source "$ENV_FILE"
fi

if [[ -n "${KOMARI_VERSION:-}" ]]; then
    KOMARI_IMAGE="ghcr.io/llovely45/komari-next:${KOMARI_VERSION}"
fi

if [[ -z "$POSTGRES_PASSWORD" ]]; then
    POSTGRES_PASSWORD="$(generate_password)"
fi

if [[ ! "$KOMARI_PORT" =~ ^[0-9]+$ ]] || ((KOMARI_PORT < 1 || KOMARI_PORT > 65535)); then
    die "KOMARI_PORT 无效：$KOMARI_PORT"
fi

# The generated password is URL-safe, so it can be used in the PostgreSQL DSN
# without a second URL-encoding step.
if [[ ! "$POSTGRES_PASSWORD" =~ ^[A-Za-z0-9]+$ ]]; then
    die 'POSTGRES_PASSWORD 只能包含字母和数字，避免破坏 PostgreSQL DSN。'
fi

umask 077
{
    printf 'KOMARI_IMAGE=%s\n' "$KOMARI_IMAGE"
    printf 'KOMARI_PORT=%s\n' "$KOMARI_PORT"
    printf 'POSTGRES_PASSWORD=%s\n' "$POSTGRES_PASSWORD"
} > "$ENV_FILE"
chmod 0600 "$ENV_FILE"

cat > "$COMPOSE_FILE" <<'YAML'
services:
  postgres:
    image: postgres:16-alpine
    container_name: komari-next-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: komari
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: komari
    volumes:
      - komari-next-pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U komari -d komari"]
      interval: 5s
      timeout: 5s
      retries: 30
    networks:
      - komari-next

  komari:
    image: ${KOMARI_IMAGE}
    container_name: komari-next
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      KOMARI_LISTEN: 0.0.0.0:25775
      KOMARI_DB_DSN: "postgres://komari:${POSTGRES_PASSWORD}@postgres:5432/komari?sslmode=disable"
    ports:
      - "${KOMARI_PORT}:25775"
    volumes:
      - ./data:/app/data
    networks:
      - komari-next

networks:
  komari-next:
    name: komari-next-net

volumes:
  komari-next-pgdata:
    name: komari-next-pgdata
YAML

chmod 0644 "$COMPOSE_FILE"

compose() {
    (
        cd "$INSTALL_DIR"
        "${COMPOSE[@]}" -f "$COMPOSE_FILE" "$@"
    )
}

log "安装目录：$INSTALL_DIR"
log "镜像：$KOMARI_IMAGE"
log '校验 Docker Compose 配置……'
compose config >/dev/null

log '拉取镜像……'
compose pull

log '启动 PostgreSQL 和 Komari……'
compose up -d

log '安装完成。'
log "访问地址：http://服务器IP:${KOMARI_PORT}/install"
log "查看状态：cd $INSTALL_DIR && ${COMPOSE[*]} ps"
log "查看日志：cd $INSTALL_DIR && ${COMPOSE[*]} logs -f komari"
log 'Redis 已由 Komari 容器内置启动，不会额外暴露 6379 端口。'
