#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RAW_LOG_DIR="${LOG_DIR:-$REPO_ROOT/logs}"
WEB_DIR="$REPO_ROOT/web"
SLICE_BIN="$REPO_ROOT/slice_service_server"
ADMIN_BIN="$REPO_ROOT/admin_service_server"
LOG_DIR="$(cd "$REPO_ROOT" && mkdir -p "$RAW_LOG_DIR" && cd "$RAW_LOG_DIR" && pwd)"
WEB_LOG="$LOG_DIR/web_preview.log"
SLICE_LOG="$LOG_DIR/slice_service.log"
ADMIN_LOG="$LOG_DIR/admin_service.log"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

cd "$REPO_ROOT"

start_slice_service() {
  log "Stopping existing slice service..."
  pkill -f "$SLICE_BIN" >/dev/null 2>&1 || true

  log "Building slice service..."
  go build -o "$SLICE_BIN" ./slice_service

  log "Starting slice service (log: $SLICE_LOG)..."
  nohup "$SLICE_BIN" > "$SLICE_LOG" 2>&1 &
  log "Slice service started with PID $!"
}

start_admin_service() {
  log "Stopping existing admin service..."
  pkill -f "$ADMIN_BIN" >/dev/null 2>&1 || true

  log "Building admin service..."
  go build -o "$ADMIN_BIN" ./admin_service

  log "Starting admin service (log: $ADMIN_LOG)..."
  nohup "$ADMIN_BIN" > "$ADMIN_LOG" 2>&1 &
  log "Admin service started with PID $!"
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
start_web_preview
log "=== All services started ==="
