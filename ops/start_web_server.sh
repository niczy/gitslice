#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_LOG_DIR="${LOG_DIR:-$REPO_ROOT/logs}"
WEB_DIR="$REPO_ROOT/web"
CORE_BIN="$REPO_ROOT/core_server"
LOG_DIR="$(cd "$REPO_ROOT" && mkdir -p "$RAW_LOG_DIR" && cd "$RAW_LOG_DIR" && pwd)"
WEB_LOG="$LOG_DIR/web_preview.log"
CORE_LOG="$LOG_DIR/core_server.log"
CORE_SERVICE_PORT="${CORE_SERVICE_PORT:-50051}"
GATEWAY_PORT="${GATEWAY_PORT:-8080}"
# In production we don't want to auto-scan the local git repo and populate genesis.
# Leave overrideable for one-off maintenance runs.
SKIP_GIT_POPULATION="${SKIP_GIT_POPULATION:-1}"
# If using Postgres metadata in production, avoid requiring GCS ADC creds by default.
STORAGE_TYPE="${STORAGE_TYPE:-postgres}"
POSTGRES_DSN="${POSTGRES_DSN:-}"
OBJECT_STORE_TYPE="${OBJECT_STORE_TYPE:-filesystem}"
OBJECT_STORE_DIR="${OBJECT_STORE_DIR:-$REPO_ROOT/.objectstore}"
MIN_NODE_MAJOR="${MIN_NODE_MAJOR:-18}"
PM2_STOP_TIMEOUT_SECONDS="${PM2_STOP_TIMEOUT_SECONDS:-10}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
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
  if ! command -v pm2 >/dev/null 2>&1; then
    return 0
  fi

  # Prevent PM2 autorestart from racing the manual restart flow.
  if command -v timeout >/dev/null 2>&1; then
    timeout "${PM2_STOP_TIMEOUT_SECONDS}s" pm2 stop gitslice-core >/dev/null 2>&1 || true
    timeout "${PM2_STOP_TIMEOUT_SECONDS}s" pm2 stop gitslice-web >/dev/null 2>&1 || true
  else
    pm2 stop gitslice-core >/dev/null 2>&1 || true
    pm2 stop gitslice-web >/dev/null 2>&1 || true
  fi
}

start_core_server() {
  cd "$REPO_ROOT"
  stop_pm2_gitslice_apps

  log "Stopping existing core server..."
  pkill -f "$CORE_BIN" >/dev/null 2>&1 || true

  # Wait a moment for ports to be released
  sleep 2

  log "Building core server (with proto generation)..."
  make build-core

  log "Starting core server (log: $CORE_LOG)..."
  CORE_SERVICE_PORT="$CORE_SERVICE_PORT" \
    GATEWAY_PORT="$GATEWAY_PORT" \
    STORAGE_TYPE="$STORAGE_TYPE" \
    POSTGRES_DSN="$POSTGRES_DSN" \
    SKIP_GIT_POPULATION="$SKIP_GIT_POPULATION" \
    OBJECT_STORE_TYPE="$OBJECT_STORE_TYPE" \
    OBJECT_STORE_DIR="$OBJECT_STORE_DIR" \
    nohup "$CORE_BIN" > "$CORE_LOG" 2>&1 &
  local pid=$!
  log "Core server started with PID $pid (STORAGE_TYPE=$STORAGE_TYPE, SKIP_GIT_POPULATION=$SKIP_GIT_POPULATION, OBJECT_STORE_TYPE=$OBJECT_STORE_TYPE)"

  if ! wait_for_port "Core gRPC" "$CORE_SERVICE_PORT" 30 "$CORE_LOG"; then
    log "ERROR: Failed to start core gRPC. Check $CORE_LOG for details"
    exit 1
  fi

  if ! wait_for_health "Gateway" "http://localhost:${GATEWAY_PORT}/health" 30 "$CORE_LOG"; then
    log "ERROR: Failed to start gateway. Check $CORE_LOG for details"
    exit 1
  fi
}

build_web_preview() {
  cd "$WEB_DIR"

  if [ ! -d node_modules ]; then
    log "Installing web dependencies..."
    npm ci
  fi

  log "Building web preview..."
  npm run build
}

start_web_preview() {
  cd "$WEB_DIR"

  log "Stopping existing web preview..."
  pkill -f "vite preview" >/dev/null 2>&1 || true

  log "Starting web preview (log: $WEB_LOG)..."
  nohup npm run preview -- --host 0.0.0.0 --port 4173 > "$WEB_LOG" 2>&1 &
  log "Web preview started with PID $!"
}

log "=== Starting all services ==="
ensure_node_runtime
build_web_preview
start_core_server
start_web_preview
log "=== All services started ==="
