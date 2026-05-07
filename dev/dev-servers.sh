#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="${DEV_LOG_DIR:-$REPO_ROOT/logs/dev}"
STATE_DIR="${DEV_STATE_DIR:-$LOG_DIR}"
CORE_PID_FILE="$STATE_DIR/core.pid"
WEB_PID_FILE="$STATE_DIR/web.pid"
CORE_LOG="$LOG_DIR/core.log"
WEB_LOG="$LOG_DIR/web.log"
WEB_DEV_VARS_FILE="$REPO_ROOT/web/.dev.vars"

COMMAND="${1:-start}"
if [ "$#" -gt 0 ]; then
  shift
fi

if [ -f "$WEB_DEV_VARS_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$WEB_DEV_VARS_FILE"
  set +a
fi

DEFAULT_LOCAL_POSTGRES_DSN="${DEFAULT_LOCAL_POSTGRES_DSN:-postgres://$(id -un)@127.0.0.1:5432/gitslice_dev?sslmode=disable}"
STORAGE_TYPE="${STORAGE_TYPE:-postgres}"
OBJECT_STORE_TYPE="${OBJECT_STORE_TYPE:-filesystem}"
POSTGRES_DSN="${POSTGRES_DSN:-$DEFAULT_LOCAL_POSTGRES_DSN}"
OBJECT_STORE_DIR="${OBJECT_STORE_DIR:-$REPO_ROOT/.objectstore}"
CORE_BIND_ADDR="${CORE_BIND_ADDR:-127.0.0.1}"
CORE_SERVICE_PORT="${CORE_SERVICE_PORT:-50051}"
WEB_HOST="${WEB_HOST:-127.0.0.1}"
WEB_PORT="${WEB_PORT:-5173}"
PUBLIC_WEB_BASE_URL_WAS_SET="${PUBLIC_WEB_BASE_URL+x}"
PUBLIC_API_BASE_URL_WAS_SET="${PUBLIC_API_BASE_URL+x}"
VITE_FILE_API_PROXY_TARGET_WAS_SET="${VITE_FILE_API_PROXY_TARGET+x}"
PUBLIC_WEB_BASE_URL="${PUBLIC_WEB_BASE_URL:-http://localhost:${WEB_PORT}}"
PUBLIC_API_BASE_URL="${PUBLIC_API_BASE_URL:-http://localhost:${CORE_SERVICE_PORT}}"
VITE_FILE_API_PROXY_TARGET="${VITE_FILE_API_PROXY_TARGET:-$PUBLIC_API_BASE_URL}"
SKIP_GIT_POPULATION="${SKIP_GIT_POPULATION:-0}"
AUTH_PROVIDER="${AUTH_PROVIDER:-clerk}"
ALLOW_DEV_LOGIN="${ALLOW_DEV_LOGIN:-1}"
BUILD="${BUILD:-1}"
DEV_CREATE_POSTGRES_DB="${DEV_CREATE_POSTGRES_DB:-1}"

usage() {
  cat <<EOF
Usage: dev/dev-servers.sh <start|stop|restart|status> [options]

Options:
  --storage <memory|postgres>       Metadata storage backend. Default: postgres.
  --postgres-dsn <dsn>              Postgres DSN. Default: $DEFAULT_LOCAL_POSTGRES_DSN.
  --object-store <file|filesystem|r2>
                                    Blob object storage for Postgres. Default: filesystem.
  --object-store-dir <path>         Filesystem object store dir. Default: .objectstore.
  --core-port <port>                Core gRPC + HTTP gateway port. Default: 50051.
  --web-port <port>                 Vite dev server port. Default: 5173.
  --skip-build                      Reuse existing core_server binary.
  -h, --help                        Show this help.

R2 mode uses the existing R2_* environment variables:
  R2_ENDPOINT, R2_BUCKET, R2_PREFIX, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY
EOF
}

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

normalize_storage_type() {
  case "$(printf '%s' "$STORAGE_TYPE" | tr '[:upper:]' '[:lower:]')" in
    memory|mem|"")
      STORAGE_TYPE="memory"
      ;;
    postgres|pg)
      STORAGE_TYPE="postgres"
      ;;
    *)
      echo "Unsupported storage type: $STORAGE_TYPE" >&2
      exit 1
      ;;
  esac
}

normalize_object_store_type() {
  case "$(printf '%s' "$OBJECT_STORE_TYPE" | tr '[:upper:]' '[:lower:]')" in
    file|fs|filesystem|"")
      OBJECT_STORE_TYPE="filesystem"
      ;;
    r2)
      OBJECT_STORE_TYPE="r2"
      ;;
    *)
      echo "Unsupported object store type: $OBJECT_STORE_TYPE" >&2
      exit 1
      ;;
  esac
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --storage)
      STORAGE_TYPE="${2:?--storage requires a value}"
      shift 2
      ;;
    --postgres-dsn)
      POSTGRES_DSN="${2:?--postgres-dsn requires a value}"
      shift 2
      ;;
    --object-store)
      OBJECT_STORE_TYPE="${2:?--object-store requires a value}"
      shift 2
      ;;
    --object-store-dir)
      OBJECT_STORE_DIR="${2:?--object-store-dir requires a value}"
      shift 2
      ;;
    --core-port)
      CORE_SERVICE_PORT="${2:?--core-port requires a value}"
      if [ -z "$PUBLIC_API_BASE_URL_WAS_SET" ]; then
        PUBLIC_API_BASE_URL="http://localhost:${CORE_SERVICE_PORT}"
      fi
      if [ -z "$VITE_FILE_API_PROXY_TARGET_WAS_SET" ]; then
        VITE_FILE_API_PROXY_TARGET="$PUBLIC_API_BASE_URL"
      fi
      shift 2
      ;;
    --web-port)
      WEB_PORT="${2:?--web-port requires a value}"
      if [ -z "$PUBLIC_WEB_BASE_URL_WAS_SET" ]; then
        PUBLIC_WEB_BASE_URL="http://localhost:${WEB_PORT}"
      fi
      shift 2
      ;;
    --skip-build)
      BUILD=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

normalize_storage_type
normalize_object_store_type

pid_is_running() {
  local pid_file="$1"
  [ -f "$pid_file" ] || return 1
  local pid
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  [ -n "$pid" ] || return 1
  kill -0 "$pid" >/dev/null 2>&1
}

stop_pid_file() {
  local label="$1"
  local pid_file="$2"
  if ! [ -f "$pid_file" ]; then
    return 0
  fi

  local pid
  pid="$(cat "$pid_file" 2>/dev/null || true)"
  if [ -z "$pid" ]; then
    rm -f "$pid_file"
    return 0
  fi

  if kill -0 "$pid" >/dev/null 2>&1; then
    log "Stopping $label pid $pid"
    kill "$pid" >/dev/null 2>&1 || true
    local attempt=0
    while kill -0 "$pid" >/dev/null 2>&1 && [ "$attempt" -lt 20 ]; do
      sleep 0.25
      attempt=$((attempt + 1))
    done
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill -9 "$pid" >/dev/null 2>&1 || true
    fi
  fi
  rm -f "$pid_file"
}

port_pids() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true
    return 0
  fi
  if command -v fuser >/dev/null 2>&1; then
    fuser -n tcp "$port" 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$' || true
    return 0
  fi
}

stop_port() {
  local port="$1"
  local pids
  pids="$(port_pids "$port" | sort -u | tr '\n' ' ')"
  if [ -n "$pids" ]; then
    log "Stopping listener(s) on port $port: $pids"
    kill $pids >/dev/null 2>&1 || true
  fi
}

stop_servers() {
  stop_pid_file "web dev server" "$WEB_PID_FILE"
  stop_pid_file "core server" "$CORE_PID_FILE"
  stop_port "$WEB_PORT"
  stop_port "$CORE_SERVICE_PORT"
}

wait_for_url() {
  local label="$1"
  local url="$2"
  local log_file="$3"
  local attempt=0
  while [ "$attempt" -lt 30 ]; do
    if curl -sf "$url" >/dev/null 2>&1; then
      log "$label is healthy at $url"
      return 0
    fi
    sleep 1
    attempt=$((attempt + 1))
  done
  log "ERROR: $label did not become healthy at $url"
  tail -40 "$log_file" 2>/dev/null || true
  return 1
}

wait_for_port() {
  local label="$1"
  local host="$2"
  local port="$3"
  local log_file="$4"
  local attempt=0
  while [ "$attempt" -lt 30 ]; do
    if nc -z "$host" "$port" >/dev/null 2>&1; then
      log "$label is listening on port $port"
      return 0
    fi
    sleep 1
    attempt=$((attempt + 1))
  done
  log "ERROR: $label did not listen on port $port"
  tail -40 "$log_file" 2>/dev/null || true
  return 1
}

validate_start_config() {
  if [ "$STORAGE_TYPE" = "postgres" ] && [ -z "$POSTGRES_DSN" ]; then
    echo "POSTGRES_DSN is required for postgres storage." >&2
    echo "Example: POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/gitslice?sslmode=disable' make start-servers-postgres-file" >&2
    exit 1
  fi

  if [ "$STORAGE_TYPE" = "postgres" ] && [ "$OBJECT_STORE_TYPE" = "filesystem" ]; then
    mkdir -p "$OBJECT_STORE_DIR"
  fi

  if [ "$STORAGE_TYPE" = "postgres" ] && [ "$OBJECT_STORE_TYPE" = "r2" ]; then
    local missing=()
    for key in R2_ENDPOINT R2_BUCKET R2_PREFIX R2_ACCESS_KEY_ID R2_SECRET_ACCESS_KEY; do
      if [ -z "${!key:-}" ]; then
        missing+=("$key")
      fi
    done
    if [ "${#missing[@]}" -gt 0 ]; then
      echo "Missing required R2 env vars: ${missing[*]}" >&2
      exit 1
    fi
  fi
}

postgres_dsn_database() {
  local dsn_without_query="${POSTGRES_DSN%%\?*}"
  local after_scheme="${dsn_without_query#*://}"
  local db_name="${after_scheme#*/}"
  if [ "$db_name" = "$after_scheme" ] || [ -z "$db_name" ]; then
    return 1
  fi
  printf '%s\n' "$db_name"
}

postgres_maintenance_dsn() {
  local query=""
  local dsn_without_query="$POSTGRES_DSN"
  if [ "$POSTGRES_DSN" != "${POSTGRES_DSN%%\?*}" ]; then
    query="?${POSTGRES_DSN#*\?}"
    dsn_without_query="${POSTGRES_DSN%%\?*}"
  fi
  printf '%s/postgres%s\n' "${dsn_without_query%/*}" "$query"
}

postgres_dsn_is_local() {
  case "$POSTGRES_DSN" in
    *127.0.0.1*|*localhost*|*%2F*|*%2f*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

ensure_local_postgres_database() {
  if [ "$STORAGE_TYPE" != "postgres" ] || [ "$DEV_CREATE_POSTGRES_DB" = "0" ]; then
    return 0
  fi
  if ! postgres_dsn_is_local; then
    return 0
  fi
  if ! command -v psql >/dev/null 2>&1; then
    log "psql not found; skipping local database creation"
    return 0
  fi

  local db_name maintenance_dsn
  db_name="$(postgres_dsn_database || true)"
  if [ -z "$db_name" ]; then
    log "Could not parse database name from POSTGRES_DSN; skipping local database creation"
    return 0
  fi
  maintenance_dsn="$(postgres_maintenance_dsn)"

  if psql "$POSTGRES_DSN" -v ON_ERROR_STOP=1 -c 'select 1' >/dev/null 2>&1; then
    return 0
  fi

  log "Ensuring local Postgres database '$db_name' exists"
  if ! psql "$maintenance_dsn" -v ON_ERROR_STOP=1 -c "create database \"$db_name\"" >/dev/null 2>&1; then
    if ! psql "$POSTGRES_DSN" -v ON_ERROR_STOP=1 -c 'select 1' >/dev/null 2>&1; then
      echo "Failed to create or connect to local Postgres database '$db_name'." >&2
      echo "Create it manually with: createdb $db_name" >&2
      exit 1
    fi
  fi
}

start_servers() {
  validate_start_config
  mkdir -p "$STATE_DIR" "$LOG_DIR"

  stop_servers
  ensure_local_postgres_database

  cd "$REPO_ROOT"
  if [ "$BUILD" = "1" ]; then
    log "Building core server"
    make build-core
  fi

  if [ ! -x "$REPO_ROOT/web/node_modules/.bin/react-router" ]; then
    log "Installing web dependencies"
    (cd "$REPO_ROOT/web" && npm ci)
  fi

  log "Starting core server (storage=$STORAGE_TYPE, object_store=$OBJECT_STORE_TYPE, log=$CORE_LOG)"
  CORE_BIND_ADDR="$CORE_BIND_ADDR" \
    CORE_SERVICE_PORT="$CORE_SERVICE_PORT" \
    STORAGE_TYPE="$STORAGE_TYPE" \
    POSTGRES_DSN="$POSTGRES_DSN" \
    SKIP_GIT_POPULATION="$SKIP_GIT_POPULATION" \
    OBJECT_STORE_TYPE="$OBJECT_STORE_TYPE" \
    OBJECT_STORE_DIR="$OBJECT_STORE_DIR" \
    PUBLIC_WEB_BASE_URL="$PUBLIC_WEB_BASE_URL" \
    PUBLIC_API_BASE_URL="$PUBLIC_API_BASE_URL" \
    AUTH_PROVIDER="$AUTH_PROVIDER" \
    AUTH_SECRET="${AUTH_SECRET:-}" \
    ALLOW_DEV_LOGIN="$ALLOW_DEV_LOGIN" \
    CLERK_WEBHOOK_SECRET="${CLERK_WEBHOOK_SECRET:-}" \
    R2_ENDPOINT="${R2_ENDPOINT:-}" \
    R2_REGION="${R2_REGION:-auto}" \
    R2_BUCKET="${R2_BUCKET:-}" \
    R2_PREFIX="${R2_PREFIX:-}" \
    R2_ACCESS_KEY_ID="${R2_ACCESS_KEY_ID:-}" \
    R2_SECRET_ACCESS_KEY="${R2_SECRET_ACCESS_KEY:-}" \
    R2_USE_PATH_STYLE="${R2_USE_PATH_STYLE:-false}" \
    "$REPO_ROOT/core_server" > "$CORE_LOG" 2>&1 &
  echo "$!" > "$CORE_PID_FILE"

  wait_for_port "Core server" "$CORE_BIND_ADDR" "$CORE_SERVICE_PORT" "$CORE_LOG"
  wait_for_url "Core HTTP gateway" "http://localhost:${CORE_SERVICE_PORT}/health" "$CORE_LOG"

  log "Starting Vite dev server (log=$WEB_LOG)"
  (
    cd "$REPO_ROOT/web"
    HOST="$WEB_HOST" \
      PORT="$WEB_PORT" \
      PUBLIC_WEB_BASE_URL="$PUBLIC_WEB_BASE_URL" \
      PUBLIC_API_BASE_URL="$PUBLIC_API_BASE_URL" \
      VITE_FILE_API_PROXY_TARGET="$VITE_FILE_API_PROXY_TARGET" \
      AUTH_PROVIDER="$AUTH_PROVIDER" \
      AUTH_SECRET="${AUTH_SECRET:-}" \
      ALLOW_DEV_LOGIN="$ALLOW_DEV_LOGIN" \
      CLERK_SECRET_KEY="${CLERK_SECRET_KEY:-}" \
      CLERK_PUBLISHABLE_KEY="${CLERK_PUBLISHABLE_KEY:-}" \
      VITE_CLERK_PUBLISHABLE_KEY="${VITE_CLERK_PUBLISHABLE_KEY:-}" \
      CLERK_JWT_KEY="${CLERK_JWT_KEY:-}" \
      WORKOS_CLIENT_ID="${WORKOS_CLIENT_ID:-}" \
      WORKOS_API_KEY="${WORKOS_API_KEY:-}" \
      WORKOS_REDIRECT_URI="${WORKOS_REDIRECT_URI:-}" \
      WORKOS_JWKS_URL="${WORKOS_JWKS_URL:-}" \
      WORKOS_COOKIE_PASSWORD="${WORKOS_COOKIE_PASSWORD:-}" \
      WORKOS_AUTHKIT_DOMAIN="${WORKOS_AUTHKIT_DOMAIN:-}" \
      npm run dev -- --host "$WEB_HOST" --port "$WEB_PORT"
  ) > "$WEB_LOG" 2>&1 &
  echo "$!" > "$WEB_PID_FILE"

  wait_for_port "Web dev server" "$WEB_HOST" "$WEB_PORT" "$WEB_LOG"
  log "Local dev servers started"
  log "  Web:  http://localhost:${WEB_PORT}"
  log "  Core: http://localhost:${CORE_SERVICE_PORT}"
  log "  Logs: $LOG_DIR"
}

status_servers() {
  if pid_is_running "$CORE_PID_FILE"; then
    echo "core: running pid $(cat "$CORE_PID_FILE") on port $CORE_SERVICE_PORT"
  else
    echo "core: stopped"
  fi
  if pid_is_running "$WEB_PID_FILE"; then
    echo "web: running pid $(cat "$WEB_PID_FILE") on port $WEB_PORT"
  else
    echo "web: stopped"
  fi
}

case "$COMMAND" in
  start)
    start_servers
    ;;
  stop)
    stop_servers
    ;;
  restart)
    stop_servers
    start_servers
    ;;
  status)
    status_servers
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    echo "Unknown command: $COMMAND" >&2
    usage >&2
    exit 1
    ;;
esac
