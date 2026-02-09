#!/usr/bin/env bash
# Starts the core backend server (gRPC + gateway) for e2e testing.
# Blocks until SIGINT/SIGTERM, then cleans up child processes.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE_BIN="$REPO_ROOT/core_server"

E2E_CORE_PORT="${E2E_CORE_PORT:-50151}"
E2E_GATEWAY_PORT="${E2E_GATEWAY_PORT:-18080}"
E2E_STORAGE_TYPE="${E2E_STORAGE_TYPE:-postgres}"
E2E_POSTGRES_DSN="${E2E_POSTGRES_DSN:-}"
E2E_LOCAL_POSTGRES_DB="${E2E_LOCAL_POSTGRES_DB:-gitslice_e2e}"
E2E_OBJECT_STORE_TYPE="${E2E_OBJECT_STORE_TYPE:-filesystem}"
E2E_OBJECT_STORE_DIR="${E2E_OBJECT_STORE_DIR:-$REPO_ROOT/.objectstore-e2e}"
GO_BIN="${GO_BIN:-}"

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

if [ -z "$GO_BIN" ]; then
  GO_BIN="$(command -v go || true)"
fi
if [ -z "$GO_BIN" ] && [ -x "$HOME/.local/go/bin/go" ]; then
  GO_BIN="$HOME/.local/go/bin/go"
fi

if [ "$E2E_STORAGE_TYPE" = "postgres" ] && [ -z "$E2E_POSTGRES_DSN" ]; then
  E2E_POSTGRES_DSN="$(LOCAL_POSTGRES_DB="$E2E_LOCAL_POSTGRES_DB" "$REPO_ROOT/ops/ensure_local_postgres.sh")"
fi
mkdir -p "$E2E_OBJECT_STORE_DIR"

# Start core server (gRPC + gateway)
CORE_SERVICE_PORT="$E2E_CORE_PORT" \
GATEWAY_PORT="$E2E_GATEWAY_PORT" \
STORAGE_TYPE="$E2E_STORAGE_TYPE" \
POSTGRES_DSN="$E2E_POSTGRES_DSN" \
OBJECT_STORE_TYPE="$E2E_OBJECT_STORE_TYPE" \
OBJECT_STORE_DIR="$E2E_OBJECT_STORE_DIR" \
"$CORE_BIN" &
PIDS="$PIDS $!"
log "Core server started (PID $!, grpc $E2E_CORE_PORT, gateway $E2E_GATEWAY_PORT)"

# Wait for gateway health endpoint
for i in $(seq 1 60); do
  if curl -sf "http://localhost:$E2E_GATEWAY_PORT/health" >/dev/null 2>&1; then
    log "Gateway healthy — core server ready on port $E2E_GATEWAY_PORT"
    break
  fi
  if [ "$i" -eq 60 ]; then
    log "ERROR: Gateway failed to become healthy after 60s"
    exit 1
  fi
  sleep 1
done

if [ "${E2E_RUN_GENESIS_INIT:-1}" = "1" ]; then
  if [ "$E2E_STORAGE_TYPE" != "postgres" ]; then
    log "ERROR: manual genesis init requires shared storage; set E2E_STORAGE_TYPE=postgres"
    exit 1
  fi
  if [ -z "$GO_BIN" ] || [ ! -x "$GO_BIN" ]; then
    log "ERROR: go binary not found; set GO_BIN or add go to PATH"
    exit 1
  fi
  STORAGE_TYPE="$E2E_STORAGE_TYPE" \
  POSTGRES_DSN="$E2E_POSTGRES_DSN" \
  OBJECT_STORE_TYPE="$E2E_OBJECT_STORE_TYPE" \
  OBJECT_STORE_DIR="$E2E_OBJECT_STORE_DIR" \
  "$GO_BIN" run ./scripts/genesis_init
  log "Genesis initialized for e2e backend"
fi

# Block until signalled
wait
