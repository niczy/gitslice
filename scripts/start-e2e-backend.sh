#!/usr/bin/env bash
# Starts the core backend server (gRPC + gateway) for e2e testing.
# Blocks until SIGINT/SIGTERM, then cleans up child processes.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE_BIN="$REPO_ROOT/core_server"

E2E_CORE_PORT="${E2E_CORE_PORT:-50151}"

log() { echo "[e2e-backend] $*"; }

if [ ! -f "$CORE_BIN" ]; then
  log "ERROR: Binary not found: $CORE_BIN — run 'make build' first."
  exit 1
fi

PIDS=""
cleanup() {
log "Stopping e2e backend services..."
  for pid in $PIDS; do
    kill "$pid" 2>/dev/null || true
  done
  for pid in $PIDS; do
    wait "$pid" 2>/dev/null || true
  done
  log "Stopped."
}
trap cleanup EXIT INT TERM

cd "$REPO_ROOT"

# Start core server (gRPC + gateway)
CORE_SERVICE_PORT="$E2E_CORE_PORT" "$CORE_BIN" &
PIDS="$PIDS $!"
log "Core server started (PID $!, gRPC + gateway $E2E_CORE_PORT)"

# Wait for gateway health endpoint
for i in $(seq 1 60); do
  if curl -sf "http://localhost:$E2E_CORE_PORT/health" >/dev/null 2>&1; then
    log "Gateway healthy — core server ready on port $E2E_CORE_PORT"
    break
  fi
  if [ "$i" -eq 60 ]; then
    log "ERROR: Gateway failed to become healthy after 60s"
    exit 1
  fi
  sleep 1
done

# Block until signalled
wait
