#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_LOG_DIR="${LOG_DIR:-$REPO_ROOT/logs}"
WEB_DIR="$REPO_ROOT/web"
SLICE_BIN="$REPO_ROOT/slice_service_server"
ADMIN_BIN="$REPO_ROOT/admin_service_server"
GATEWAY_BIN="$REPO_ROOT/gateway_service_server"
LOG_DIR="$(cd "$REPO_ROOT" && mkdir -p "$RAW_LOG_DIR" && cd "$RAW_LOG_DIR" && pwd)"
WEB_LOG="$LOG_DIR/web_preview.log"
SLICE_LOG="$LOG_DIR/slice_service.log"
ADMIN_LOG="$LOG_DIR/admin_service.log"
GATEWAY_LOG="$LOG_DIR/gateway_service.log"
GATEWAY_PORT="${GATEWAY_PORT:-8080}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

wait_for_health() {
  local service_name="$1"
  local health_url="$2"
  local max_attempts="${3:-30}"
  local log_file="${4:-$SLICE_LOG}"
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
  local log_file="${4:-$SLICE_LOG}"
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

start_slice_service() {
  log "Stopping existing slice service..."
  pkill -f "$SLICE_BIN" >/dev/null 2>&1 || true

  # Wait a moment for ports to be released
  sleep 2

  log "Building slice service (with proto generation)..."
  make build-slice

  log "Starting slice service (log: $SLICE_LOG)..."
  nohup "$SLICE_BIN" > "$SLICE_LOG" 2>&1 &
  local pid=$!
  log "Slice service started with PID $pid"

  if ! wait_for_port "Slice service" 50051 30 "$SLICE_LOG"; then
    log "ERROR: Failed to start slice service. Check $SLICE_LOG for details"
    exit 1
  fi
}

start_admin_service() {
  log "Stopping existing admin service..."
  pkill -f "$ADMIN_BIN" >/dev/null 2>&1 || true

  # Wait a moment for ports to be released
  sleep 2

  log "Building admin service (with proto generation)..."
  make build-admin

  log "Starting admin service (log: $ADMIN_LOG)..."
  nohup "$ADMIN_BIN" > "$ADMIN_LOG" 2>&1 &
  local pid=$!
  log "Admin service started with PID $pid"

  # Admin service is gRPC only - wait for port to be listening
  if ! wait_for_port "Admin service" 50052 30 "$ADMIN_LOG"; then
    log "ERROR: Failed to start admin service. Check $ADMIN_LOG for details"
    exit 1
  fi
}

start_gateway_service() {
  log "Stopping existing gateway service..."
  pkill -f "$GATEWAY_BIN" >/dev/null 2>&1 || true

  # Wait a moment for ports to be released
  sleep 2

  log "Building gateway service..."
  make build-gateway

  log "Starting gateway service (log: $GATEWAY_LOG)..."
  GATEWAY_PORT="$GATEWAY_PORT" nohup "$GATEWAY_BIN" > "$GATEWAY_LOG" 2>&1 &
  local pid=$!
  log "Gateway service started with PID $pid"

  if ! wait_for_health "Gateway service" "http://localhost:${GATEWAY_PORT}/health" 30 "$GATEWAY_LOG"; then
    log "ERROR: Failed to start gateway service. Check $GATEWAY_LOG for details"
    exit 1
  fi
}

start_web_preview() {
  cd "$WEB_DIR"

  if [ ! -d node_modules ]; then
    log "Installing web dependencies..."
    npm ci
  fi

  log "Building web preview..."
  npm run build

  log "Stopping existing web preview..."
  pkill -f "vite preview" >/dev/null 2>&1 || true

  log "Starting web preview (log: $WEB_LOG)..."
  nohup npm run preview -- --host 0.0.0.0 --port 4173 > "$WEB_LOG" 2>&1 &
  log "Web preview started with PID $!"
}

log "=== Starting all services ==="
start_slice_service
start_admin_service
start_gateway_service
start_web_preview
log "=== All services started ==="
