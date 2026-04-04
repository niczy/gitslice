#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_LOG_DIR="${LOG_DIR:-$REPO_ROOT/logs}"
WEB_DIR="$REPO_ROOT/web"
CORE_BIN="$REPO_ROOT/core_server"
LOG_DIR="$(cd "$REPO_ROOT" && mkdir -p "$RAW_LOG_DIR" && cd "$RAW_LOG_DIR" && pwd)"
WEB_LOG="$LOG_DIR/web_preview.log"
CORE_LOG="$LOG_DIR/core_server.log"
PM2_ECOSYSTEM_FILE="$REPO_ROOT/ops/ecosystem.config.cjs"
PRODUCTION_ENV_FILE="${PRODUCTION_ENV_FILE:-$REPO_ROOT/ops/.env.production}"
LEGACY_PRODUCTION_ENV_FILE="$REPO_ROOT/ops/.env"
STAGING_ENV_FILE="${STAGING_ENV_FILE:-$REPO_ROOT/ops/.env.staging}"
if [ ! -f "$STAGING_ENV_FILE" ] && [ -f "$REPO_ROOT/ops/staging/.env" ]; then
  STAGING_ENV_FILE="$REPO_ROOT/ops/staging/.env"
fi
DEPLOY_ENV="${DEPLOY_ENV:-production}"
CORE_BIND_ADDR="${CORE_BIND_ADDR:-127.0.0.1}"
CORE_SERVICE_PORT="${CORE_SERVICE_PORT:-50051}"
# In production we don't want to auto-scan the local git repo and populate genesis.
# Leave overrideable for one-off maintenance runs.
SKIP_GIT_POPULATION="${SKIP_GIT_POPULATION:-1}"
# If using Postgres metadata in production, avoid requiring GCS ADC creds by default.
STORAGE_TYPE="${STORAGE_TYPE:-postgres}"
POSTGRES_DSN="${POSTGRES_DSN:-}"
POSTGRES_DSN="${POSTGRES_DSN:-${NEON_DB:-}}"
OBJECT_STORE_TYPE="${OBJECT_STORE_TYPE:-filesystem}"
OBJECT_STORE_DIR="${OBJECT_STORE_DIR:-$REPO_ROOT/.objectstore}"
PUBLIC_WEB_BASE_URL="${PUBLIC_WEB_BASE_URL:-https://gitslice.io}"
PUBLIC_API_BASE_URL="${PUBLIC_API_BASE_URL:-http://127.0.0.1:${CORE_SERVICE_PORT}}"
VITE_FILE_API_BASE_URL="${VITE_FILE_API_BASE_URL:-$PUBLIC_API_BASE_URL}"
WEB_DEPLOY_TARGET="${WEB_DEPLOY_TARGET:-node}"
WEB_COMPAT_RUNTIME="${WEB_COMPAT_RUNTIME:-node}"
RUN_WEB_SSR="${RUN_WEB_SSR:-auto}"
WEB_HOST="${WEB_HOST:-0.0.0.0}"
WEB_PORT="${WEB_PORT:-4173}"
MIN_NODE_MAJOR="${MIN_NODE_MAJOR:-18}"
PM2_STOP_TIMEOUT_SECONDS="${PM2_STOP_TIMEOUT_SECONDS:-10}"
PM2_BIN="${PM2_BIN:-}"
PM2_NODE_BIN="${PM2_NODE_BIN:-}"
PM2_PRODUCTION_CORE_APP="${PM2_PRODUCTION_CORE_APP:-gitslice-core-production}"
PM2_STAGING_CORE_APP="${PM2_STAGING_CORE_APP:-gitslice-core-staging}"
PM2_PRODUCTION_WEB_APP="${PM2_PRODUCTION_WEB_APP:-gitslice-web-production}"
PM2_STAGING_WEB_APP="${PM2_STAGING_WEB_APP:-gitslice-web-staging}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

resolve_production_env_file() {
  if [ -f "$PRODUCTION_ENV_FILE" ]; then
    printf '%s\n' "$PRODUCTION_ENV_FILE"
    return 0
  fi
  if [ -f "$LEGACY_PRODUCTION_ENV_FILE" ]; then
    printf '%s\n' "$LEGACY_PRODUCTION_ENV_FILE"
    return 0
  fi
  return 1
}

read_env_value() {
  local file_path="$1"
  local key="$2"
  local default_value="$3"

  if [ -z "$file_path" ] || [ ! -f "$file_path" ]; then
    printf '%s\n' "$default_value"
    return 0
  fi

  local line
  line="$(grep -E "^(export[[:space:]]+)?${key}=" "$file_path" | tail -n 1 || true)"
  if [ -z "$line" ]; then
    printf '%s\n' "$default_value"
    return 0
  fi

  line="${line#export }"
  line="${line#${key}=}"
  line="${line%\"}"
  line="${line#\"}"
  line="${line%\'}"
  line="${line#\'}"
  printf '%s\n' "$line"
}

should_run_web_ssr_mode() {
  local run_web_value="${1:-auto}"
  local deploy_target="${2:-node}"

  case "$(printf '%s' "$run_web_value" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on)
      return 0
      ;;
    0|false|no|off)
      return 1
      ;;
    auto|"")
      [ "$deploy_target" = "node" ]
      return $?
      ;;
    *)
      return 1
      ;;
  esac
}

should_run_web_ssr() {
  if should_run_web_ssr_mode "$RUN_WEB_SSR" "$WEB_DEPLOY_TARGET"; then
    return 0
  fi
  case "$(printf '%s' "$RUN_WEB_SSR" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on|0|false|no|off|auto|"")
      return 1
      ;;
    *)
      log "ERROR: RUN_WEB_SSR must be one of auto,true,false,1,0,yes,no,on,off"
      exit 1
      ;;
  esac
}

verify_pm2_core_target() {
  local role="$1"
  local env_file="$2"
  local default_port="$3"
  local log_file="$REPO_ROOT/logs/pm2-core-${role}.err.log"
  local port

  port="$(read_env_value "$env_file" CORE_SERVICE_PORT "$default_port")"
  if ! wait_for_port "${role^} core gRPC" "$port" 30 "$log_file"; then
    log "ERROR: Failed to start ${role} core gRPC via PM2"
    exit 1
  fi

  if ! wait_for_health "${role^} core HTTP health" "http://127.0.0.1:${port}/health" 30 "$log_file"; then
    log "ERROR: Failed to start ${role} core HTTP health endpoint via PM2"
    exit 1
  fi
}

verify_pm2_web_target() {
  local role="$1"
  local env_file="$2"
  local default_port="$3"
  local deploy_target run_web_ssr web_port

  deploy_target="$(read_env_value "$env_file" WEB_DEPLOY_TARGET "cloudflare_worker")"
  run_web_ssr="$(read_env_value "$env_file" RUN_WEB_SSR "auto")"
  if ! should_run_web_ssr_mode "$run_web_ssr" "$deploy_target"; then
    return 0
  fi

  web_port="$(read_env_value "$env_file" WEB_PORT "$default_port")"
  if ! wait_for_port "${role^} web SSR" "$web_port" 30 "$REPO_ROOT/logs/pm2-web-${role}.err.log"; then
    log "ERROR: Failed to start ${role} web SSR via PM2"
    exit 1
  fi
}

pm2_needs_web_build() {
  local production_env_file staging_run_web_ssr staging_deploy_target
  local production_run_web_ssr production_deploy_target

  production_env_file="$(resolve_production_env_file || true)"
  production_run_web_ssr="$(read_env_value "$production_env_file" RUN_WEB_SSR "auto")"
  production_deploy_target="$(read_env_value "$production_env_file" WEB_DEPLOY_TARGET "cloudflare_worker")"
  if should_run_web_ssr_mode "$production_run_web_ssr" "$production_deploy_target"; then
    return 0
  fi

  if [ -f "$STAGING_ENV_FILE" ]; then
    staging_run_web_ssr="$(read_env_value "$STAGING_ENV_FILE" RUN_WEB_SSR "auto")"
    staging_deploy_target="$(read_env_value "$STAGING_ENV_FILE" WEB_DEPLOY_TARGET "cloudflare_worker")"
    if should_run_web_ssr_mode "$staging_run_web_ssr" "$staging_deploy_target"; then
      return 0
    fi
  fi

  return 1
}

discover_pm2_bin() {
  if [ -n "$PM2_BIN" ]; then
    if [ -n "$PM2_NODE_BIN" ] && [ -x "$PM2_NODE_BIN" ] && [ -f "$PM2_BIN" ]; then
      printf '%s\n' "$PM2_BIN"
      return 0
    fi
    if [ -x "$PM2_BIN" ]; then
      printf '%s\n' "$PM2_BIN"
      return 0
    fi
  fi

  if command -v pm2 >/dev/null 2>&1; then
    PM2_BIN="$(command -v pm2)"
    printf '%s\n' "$PM2_BIN"
    return 0
  fi

  local candidate
  candidate="$(find "$HOME/.nvm/versions/node" -mindepth 3 -maxdepth 3 -type f -path '*/bin/pm2' 2>/dev/null | sort -V | tail -n 1 || true)"
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    PM2_BIN="$candidate"
    PM2_NODE_BIN=""
    printf '%s\n' "$PM2_BIN"
    return 0
  fi

  candidate="$(find "$HOME/.nvm/versions/node" -type f -path '*/lib/node_modules/pm2/lib/binaries/CLI.js' 2>/dev/null | sort -V | tail -n 1 || true)"
  if [ -n "$candidate" ] && [ -f "$candidate" ]; then
    local node_bin
    node_bin="${candidate%/lib/node_modules/pm2/lib/binaries/CLI.js}/bin/node"
    if [ -x "$node_bin" ]; then
      PM2_BIN="$candidate"
      PM2_NODE_BIN="$node_bin"
      printf '%s\n' "$PM2_BIN"
      return 0
    fi
  fi

  return 1
}

run_pm2() {
  if ! discover_pm2_bin >/dev/null 2>&1; then
    return 1
  fi

  if [ -n "$PM2_NODE_BIN" ]; then
    "$PM2_NODE_BIN" "$PM2_BIN" "$@"
    return $?
  fi

  "$PM2_BIN" "$@"
}

wait_for_health() {
  local service_name="$1"
  local health_url="$2"
  local max_attempts="${3:-30}"
  local log_file="${4:-$CORE_LOG}"
  local attempt=0

  log "Waiting for $service_name to be healthy at $health_url..."

  while [ $attempt -lt $max_attempts ]; do
    if curl -sf "$health_url" >/dev/null 2>&1; then
      log "$service_name is healthy"
      return 0
    fi
    attempt=$((attempt + 1))
    sleep 1
  done

  log "ERROR: $service_name failed to become healthy after $max_attempts seconds"
  log "Last 20 lines from service log:"
  tail -20 "$log_file" 2>/dev/null || echo "No log available"
  return 1
}

wait_for_port() {
  local service_name="$1"
  local port="$2"
  local max_attempts="${3:-30}"
  local log_file="${4:-$CORE_LOG}"
  local attempt=0

  log "Waiting for $service_name to listen on port $port..."

  while [ $attempt -lt $max_attempts ]; do
    if nc -z localhost "$port" 2>/dev/null; then
      log "$service_name is listening on port $port"
      return 0
    fi
    attempt=$((attempt + 1))
    sleep 1
  done

  log "ERROR: $service_name failed to listen on port $port after $max_attempts seconds"
  log "Last 20 lines from service log:"
  tail -20 "$log_file" 2>/dev/null || echo "No log available"
  return 1
}

cd "$REPO_ROOT"

# Avoid inheriting restart_all.sh's flock FD into long-lived daemons.
exec 9>&- 2>/dev/null || true

ensure_node_runtime() {
  if ! command -v node >/dev/null 2>&1; then
    log "ERROR: node is not in PATH. Install Node.js >= ${MIN_NODE_MAJOR}."
    exit 1
  fi
  if ! command -v npm >/dev/null 2>&1; then
    log "ERROR: npm is not in PATH. Install Node.js >= ${MIN_NODE_MAJOR}."
    exit 1
  fi

  local node_version node_major
  node_version="$(node -v 2>/dev/null || true)"
  node_major="$(echo "$node_version" | sed -E 's/^v([0-9]+).*/\1/')"
  if [ -z "$node_major" ] || [ "$node_major" -lt "$MIN_NODE_MAJOR" ]; then
    log "ERROR: Node.js $node_version is unsupported. Need Node.js >= ${MIN_NODE_MAJOR}."
    exit 1
  fi

  log "Using Node.js $node_version ($(command -v node))"
}

stop_pm2_gitslice_apps() {
  discover_pm2_bin >/dev/null 2>&1 || return 0
  local app_names=(
    "$PM2_PRODUCTION_CORE_APP"
    "$PM2_STAGING_CORE_APP"
    "$PM2_PRODUCTION_WEB_APP"
    "$PM2_STAGING_WEB_APP"
    "gitslice-core"
    "gitslice-web"
  )
  local app_name

  # Prevent PM2 autorestart from racing the manual restart flow.
  if command -v timeout >/dev/null 2>&1; then
    for app_name in "${app_names[@]}"; do
      if [ -n "$PM2_NODE_BIN" ]; then
        timeout "${PM2_STOP_TIMEOUT_SECONDS}s" "$PM2_NODE_BIN" "$PM2_BIN" stop "$app_name" >/dev/null 2>&1 || true
      else
        timeout "${PM2_STOP_TIMEOUT_SECONDS}s" "$PM2_BIN" stop "$app_name" >/dev/null 2>&1 || true
      fi
    done
  else
    for app_name in "${app_names[@]}"; do
      run_pm2 stop "$app_name" >/dev/null 2>&1 || true
    done
  fi
}

wait_for_web_server() {
  if ! wait_for_port "Web SSR server" "$WEB_PORT" 30 "$WEB_LOG"; then
    log "ERROR: Failed to start web SSR server. Check $WEB_LOG for details"
    exit 1
  fi
}

build_core_server() {
  cd "$REPO_ROOT"
  stop_pm2_gitslice_apps

  log "Stopping existing core server..."
  pkill -f "$CORE_BIN" >/dev/null 2>&1 || true

  # Wait a moment for ports to be released
  sleep 2

  log "Building core server (with proto generation)..."
  make build-core
}

build_web_server() {
  cd "$WEB_DIR"

  if [ ! -d node_modules ]; then
    log "Installing web dependencies..."
    npm ci
  fi

  log "Building web SSR bundle..."
  log "Web deploy target: $WEB_DEPLOY_TARGET (runtime compatibility: $WEB_COMPAT_RUNTIME, deploy env: $DEPLOY_ENV)"
  DEPLOY_ENV="$DEPLOY_ENV" \
    PUBLIC_WEB_BASE_URL="$PUBLIC_WEB_BASE_URL" \
    PUBLIC_API_BASE_URL="$PUBLIC_API_BASE_URL" \
    VITE_FILE_API_BASE_URL="$VITE_FILE_API_BASE_URL" \
    WEB_DEPLOY_TARGET="$WEB_DEPLOY_TARGET" \
    WEB_COMPAT_RUNTIME="$WEB_COMPAT_RUNTIME" \
    npm run build
}

start_core_server_nohup() {
  cd "$REPO_ROOT"

  log "Starting core server (log: $CORE_LOG)..."
  CORE_BIND_ADDR="$CORE_BIND_ADDR" \
  CORE_SERVICE_PORT="$CORE_SERVICE_PORT" \
    STORAGE_TYPE="$STORAGE_TYPE" \
    POSTGRES_DSN="$POSTGRES_DSN" \
    SKIP_GIT_POPULATION="$SKIP_GIT_POPULATION" \
    OBJECT_STORE_TYPE="$OBJECT_STORE_TYPE" \
    OBJECT_STORE_DIR="$OBJECT_STORE_DIR" \
    PUBLIC_WEB_BASE_URL="$PUBLIC_WEB_BASE_URL" \
    PUBLIC_API_BASE_URL="$PUBLIC_API_BASE_URL" \
    DEPLOY_ENV="$DEPLOY_ENV" \
    WEB_DEPLOY_TARGET="$WEB_DEPLOY_TARGET" \
    WEB_COMPAT_RUNTIME="$WEB_COMPAT_RUNTIME" \
    nohup "$CORE_BIN" > "$CORE_LOG" 2>&1 &
  local pid=$!
  log "Core server started with PID $pid (DEPLOY_ENV=$DEPLOY_ENV, CORE_BIND_ADDR=$CORE_BIND_ADDR, STORAGE_TYPE=$STORAGE_TYPE, SKIP_GIT_POPULATION=$SKIP_GIT_POPULATION, OBJECT_STORE_TYPE=$OBJECT_STORE_TYPE, PUBLIC_WEB_BASE_URL=$PUBLIC_WEB_BASE_URL, PUBLIC_API_BASE_URL=$PUBLIC_API_BASE_URL)"

  if ! wait_for_port "Core gRPC" "$CORE_SERVICE_PORT" 30 "$CORE_LOG"; then
    log "ERROR: Failed to start core gRPC. Check $CORE_LOG for details"
    exit 1
  fi

  if ! wait_for_health "Core HTTP health" "http://localhost:${CORE_SERVICE_PORT}/health" 30 "$CORE_LOG"; then
    log "ERROR: Failed to start core HTTP health endpoint. Check $CORE_LOG for details"
    exit 1
  fi
}

stop_web_server_processes() {
  pkill -f "vite preview" >/dev/null 2>&1 || true
  pkill -f "react-router-serve ./build/server/index.js" >/dev/null 2>&1 || true
  pkill -f "$WEB_DIR/build/server/index.js" >/dev/null 2>&1 || true
}

start_web_server_nohup() {
  cd "$WEB_DIR"

  log "Stopping existing web server..."
  stop_web_server_processes

  log "Starting web SSR server (log: $WEB_LOG)..."
  HOST="$WEB_HOST" \
    PORT="$WEB_PORT" \
    DEPLOY_ENV="$DEPLOY_ENV" \
    PUBLIC_WEB_BASE_URL="$PUBLIC_WEB_BASE_URL" \
    PUBLIC_API_BASE_URL="$PUBLIC_API_BASE_URL" \
    VITE_FILE_API_BASE_URL="$VITE_FILE_API_BASE_URL" \
    WEB_DEPLOY_TARGET="$WEB_DEPLOY_TARGET" \
    WEB_COMPAT_RUNTIME="$WEB_COMPAT_RUNTIME" \
    nohup npm run start > "$WEB_LOG" 2>&1 &
  log "Web SSR server started with PID $!"
  wait_for_web_server
}

start_services_with_pm2() {
  discover_pm2_bin >/dev/null 2>&1 || return 1
  local production_env_file staging_env_file
  production_env_file="$(resolve_production_env_file || true)"
  staging_env_file=""
  if [ -f "$STAGING_ENV_FILE" ]; then
    staging_env_file="$STAGING_ENV_FILE"
  fi

  log "Starting services via PM2 ecosystem ($PM2_BIN)..."
  pkill -f "$CORE_BIN" >/dev/null 2>&1 || true
  stop_web_server_processes

  run_pm2 startOrRestart "$PM2_ECOSYSTEM_FILE" --update-env >/dev/null

  verify_pm2_core_target "production" "$production_env_file" "50051"
  verify_pm2_web_target "production" "$production_env_file" "4173"

  if [ -n "$staging_env_file" ]; then
    verify_pm2_core_target "staging" "$staging_env_file" "50052"
    verify_pm2_web_target "staging" "$staging_env_file" "4174"
  fi
}

start_services() {
  build_core_server

  if discover_pm2_bin >/dev/null 2>&1; then
    start_services_with_pm2
    return 0
  fi

  start_core_server_nohup
  if should_run_web_ssr; then
    start_web_server_nohup
  fi
}

log "=== Starting all services ==="
ensure_node_runtime
if should_run_web_ssr || pm2_needs_web_build; then
  build_web_server
else
  log "Skipping local web SSR build/start because WEB_DEPLOY_TARGET=$WEB_DEPLOY_TARGET and RUN_WEB_SSR=$RUN_WEB_SSR"
fi
start_services
log "=== All services started ==="
