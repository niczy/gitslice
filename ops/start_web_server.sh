#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="${LOG_DIR:-"$REPO_ROOT/logs"}"
WEB_DIR="$REPO_ROOT/web"
SLICE_BIN="$REPO_ROOT/slice_service_server"
ADMIN_BIN="$REPO_ROOT/admin_service_server"
WEB_LOG="$LOG_DIR/web_preview.log"
SLICE_LOG="$LOG_DIR/slice_service.log"
ADMIN_LOG="$LOG_DIR/admin_service.log"

mkdir -p "$LOG_DIR"

cd "$REPO_ROOT"

start_slice_service() {
  pkill -f "$SLICE_BIN" >/dev/null 2>&1 || true
  go build -o "$SLICE_BIN" ./slice_service
  nohup "$SLICE_BIN" > "$SLICE_LOG" 2>&1 &
}

start_admin_service() {
  pkill -f "$ADMIN_BIN" >/dev/null 2>&1 || true
  go build -o "$ADMIN_BIN" ./admin_service
  nohup "$ADMIN_BIN" > "$ADMIN_LOG" 2>&1 &
}

start_web_preview() {
  cd "$WEB_DIR"

  if [ ! -d node_modules ]; then
    npm ci
  fi

  npm run build

  pkill -f "vite preview" >/dev/null 2>&1 || true

  nohup npm run preview -- --host 0.0.0.0 --port 4173 > "$WEB_LOG" 2>&1 &
}

start_slice_service
start_admin_service
start_web_preview
