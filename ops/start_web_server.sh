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
CORE_SERVICE_PORT="${CORE_SERVICE_PORT:-50051}"
# In production we don't want to auto-scan the local git repo and populate genesis.
# Leave overrideable for one-off maintenance runs.
SKIP_GIT_POPULATION="${SKIP_GIT_POPULATION:-1}"
# If using Postgres metadata in production, avoid requiring GCS ADC creds by default.
STORAGE_TYPE="${STORAGE_TYPE:-postgres}"
POSTGRES_DSN="${POSTGRES_DSN:-}"
OBJECT_STORE_TYPE="${OBJECT_STORE_TYPE:-filesystem}"
OBJECT_STORE_DIR="${OBJECT_STORE_DIR:-$REPO_ROOT/.objectstore}"
PUBLIC_WEB_BASE_URL="${PUBLIC_WEB_BASE_URL:-https://agenttools.dev}"
WEB_HOST="${WEB_HOST:-0.0.0.0}"
WEB_PORT="${WEB_PORT:-4173}"
MIN_NODE_MAJOR="${MIN_NODE_MAJOR:-18}"
PM2_STOP_TIMEOUT_SECONDS="${PM2_STOP_TIMEOUT_SECONDS:-10}"
PM2_BIN="${PM2_BIN:-}"
PM2_NODE_BIN="${PM2_NODE_BIN:-}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
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

  # Prevent PM2 autorestart from racing the manual restart flow.
  if command -v timeout >/dev/null 2>&1; then
    if [ -n "$PM2_NODE_BIN" ]; then
      timeout "${PM2_STOP_TIMEOUT_SECONDS}s" "$PM2_NODE_BIN" "$PM2_BIN" stop gitslice-core >/dev/null 2>&1 || true
      timeout "${PM2_STOP_TIMEOUT_SECONDS}s" "$PM2_NODE_BIN" "$PM2_BIN" stop gitslice-web >/dev/null 2>&1 || true
    else
      timeout "${PM2_STOP_TIMEOUT_SECONDS}s" "$PM2_BIN" stop gitslice-core >/dev/null 2>&1 || true
      timeout "${PM2_STOP_TIMEOUT_SECONDS}s" "$PM2_BIN" stop gitslice-web >/dev/null 2>&1 || true
    fi
  else
    run_pm2 stop gitslice-core >/dev/null 2>&1 || true
    run_pm2 stop gitslice-web >/dev/null 2>&1 || true
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
  npm run build
}

start_core_server_nohup() {
  cd "$REPO_ROOT"

  log "Starting core server (log: $CORE_LOG)..."
  CORE_SERVICE_PORT="$CORE_SERVICE_PORT" \
    STORAGE_TYPE="$STORAGE_TYPE" \
    POSTGRES_DSN="$POSTGRES_DSN" \
    SKIP_GIT_POPULATION="$SKIP_GIT_POPULATION" \
    OBJECT_STORE_TYPE="$OBJECT_STORE_TYPE" \
    OBJECT_STORE_DIR="$OBJECT_STORE_DIR" \
    PUBLIC_WEB_BASE_URL="$PUBLIC_WEB_BASE_URL" \
    nohup "$CORE_BIN" > "$CORE_LOG" 2>&1 &
  local pid=$!
  log "Core server started with PID $pid (STORAGE_TYPE=$STORAGE_TYPE, SKIP_GIT_POPULATION=$SKIP_GIT_POPULATION, OBJECT_STORE_TYPE=$OBJECT_STORE_TYPE, PUBLIC_WEB_BASE_URL=$PUBLIC_WEB_BASE_URL)"

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
  HOST="$WEB_HOST" PORT="$WEB_PORT" nohup npm run start > "$WEB_LOG" 2>&1 &
  log "Web SSR server started with PID $!"
  wait_for_web_server
}

start_services_with_pm2() {
  discover_pm2_bin >/dev/null 2>&1 || return 1

  log "Starting services via PM2 ecosystem ($PM2_BIN)..."
  pkill -f "$CORE_BIN" >/dev/null 2>&1 || true
  stop_web_server_processes

  run_pm2 startOrRestart "$PM2_ECOSYSTEM_FILE" --update-env >/dev/null

  if ! wait_for_port "Core gRPC" "$CORE_SERVICE_PORT" 30 "/home/nic/workspace/gitslice/logs/pm2-core.err.log"; then
    log "ERROR: Failed to start core gRPC via PM2"
    exit 1
  fi

  if ! wait_for_health "Core HTTP health" "http://localhost:${CORE_SERVICE_PORT}/health" 30 "/home/nic/workspace/gitslice/logs/pm2-core.err.log"; then
    log "ERROR: Failed to start core HTTP health endpoint via PM2"
    exit 1
  fi

  wait_for_web_server
}

start_services() {
  build_core_server

  if discover_pm2_bin >/dev/null 2>&1; then
    start_services_with_pm2
    return 0
  fi

  start_core_server_nohup
  start_web_server_nohup
}

log "=== Starting all services ==="
ensure_node_runtime
build_web_server
start_services
log "=== All services started ==="
