#!/usr/bin/env bash
# Starts the backend services (slice, admin, gateway) for e2e testing.
# Blocks until SIGINT/SIGTERM, then cleans up child processes.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BAZEL_BIN="$REPO_ROOT/bazel-bin"
SLICE_BIN="$BAZEL_BIN/slice_service/slice_service_server_/slice_service_server"
ADMIN_BIN="$BAZEL_BIN/admin_service/admin_service_server_/admin_service_server"
GATEWAY_BIN="$BAZEL_BIN/gateway_service/gateway_service_server_/gateway_service_server"

E2E_SLICE_PORT="${E2E_SLICE_PORT:-50151}"
E2E_ADMIN_PORT="${E2E_ADMIN_PORT:-50152}"
E2E_GATEWAY_PORT="${E2E_GATEWAY_PORT:-18080}"

log() { echo "[e2e-backend] $*"; }

log "Building backend services with Bazel..."
(cd "$REPO_ROOT" && bazel build //slice_service:slice_service_server //admin_service:admin_service_server //gateway_service:gateway_service_server)

for bin in "$SLICE_BIN" "$ADMIN_BIN" "$GATEWAY_BIN"; do
  if [ ! -f "$bin" ]; then
    log "ERROR: Binary not found: $bin — run 'bazel build //...' first."
    exit 1
  fi
done

PIDS=()
cleanup() {
  log "Stopping e2e backend services..."
  for pid in "${PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  for pid in "${PIDS[@]}"; do
    wait "$pid" 2>/dev/null || true
  done
  log "Stopped."
}
trap cleanup EXIT INT TERM

cd "$REPO_ROOT"

# Start slice service
SLICE_SERVICE_PORT="$E2E_SLICE_PORT" "$SLICE_BIN" &
PIDS+=("$!")
log "Slice service started (PID $!, port $E2E_SLICE_PORT)"

# Start admin service
ADMIN_SERVICE_PORT="$E2E_ADMIN_PORT" "$ADMIN_BIN" &
PIDS+=("$!")
log "Admin service started (PID $!, port $E2E_ADMIN_PORT)"

# Wait for gRPC services to bind
sleep 2

# Start gateway (connects to slice + admin, exposes HTTP)
SLICE_SERVICE_PORT="$E2E_SLICE_PORT" \
ADMIN_SERVICE_PORT="$E2E_ADMIN_PORT" \
GATEWAY_PORT="$E2E_GATEWAY_PORT" \
"$GATEWAY_BIN" &
PIDS+=("$!")
log "Gateway service started (PID $!, port $E2E_GATEWAY_PORT)"

# Wait for gateway health endpoint
for i in $(seq 1 60); do
  if curl -sf "http://localhost:$E2E_GATEWAY_PORT/health" >/dev/null 2>&1; then
    log "Gateway healthy — all backend services ready on port $E2E_GATEWAY_PORT"
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
