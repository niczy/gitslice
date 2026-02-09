#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_BIN="${CLI_BIN:-$REPO_ROOT/gs_cli/gs_cli}"
GRPC_ADDR="${GRPC_ADDR:-api.agenttools.dev:80}"
FALLBACK_GRPC_ADDR="${FALLBACK_GRPC_ADDR:-}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

build_cli() {
  log "Building CLI binary..."
  make -C "$REPO_ROOT" build-cli >/dev/null
}

run_gs() {
  "$CLI_BIN" --addr "$1" "${@:2}"
}


proxy_hint_if_needed() {
  local err_out="$1"
  if [[ -n "${HTTP_PROXY:-}${http_proxy:-}${HTTPS_PROXY:-}${https_proxy:-}" ]] && [[ "$err_out" == *"error reading server preface"* || "$err_out" == *"connection reset by peer"* ]]; then
    log "Detected proxy environment; gRPC h2c calls may be reset by HTTP proxies."
    log "If running behind a proxy, add the gRPC host to NO_PROXY/no_proxy (for example: api.agenttools.dev)."
  fi
}

if [[ ! -x "$CLI_BIN" ]]; then
  build_cli
fi

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

ACTIVE_ADDR="$GRPC_ADDR"
if ! root_output="$(run_gs "$ACTIVE_ADDR" root 2>&1)"; then
  log "Primary endpoint failed ($ACTIVE_ADDR): $root_output"
  proxy_hint_if_needed "$root_output"
  if [[ -n "$FALLBACK_GRPC_ADDR" ]]; then
    ACTIVE_ADDR="$FALLBACK_GRPC_ADDR"
    log "Retrying with fallback endpoint: $ACTIVE_ADDR"
    root_output="$(run_gs "$ACTIVE_ADDR" root 2>&1)"
  else
    exit 1
  fi
fi

printf '%s\n' "$root_output"
ROOT_SLICE_ID="$(printf '%s\n' "$root_output" | sed -n 's/^Root Slice ID: //p' | head -n1)"
if [[ -z "$ROOT_SLICE_ID" ]]; then
  echo "Failed to determine root slice ID" >&2
  exit 1
fi

CHECKOUT_DIR="$WORKDIR/checkout"
mkdir -p "$CHECKOUT_DIR"

log "Checking out root slice $ROOT_SLICE_ID"
(
  cd "$CHECKOUT_DIR"
  run_gs "$ACTIVE_ADDR" slice checkout "$ROOT_SLICE_ID"
  run_gs "$ACTIVE_ADDR" status
)

UNIQUE_FILE="smoke-tests/prod-$(date +%s)-$$.txt"
CHANGESET_MSG="prod smoke $(date -u +%Y-%m-%dT%H:%M:%SZ)"

create_output="$(
  cd "$CHECKOUT_DIR"
  run_gs "$ACTIVE_ADDR" changeset create --message "$CHANGESET_MSG" --files "$UNIQUE_FILE" --author "prod-smoke"
)"
printf '%s\n' "$create_output"

CHANGESET_ID="$(printf '%s\n' "$create_output" | sed -n 's/^Created changeset \([^ ]*\).*/\1/p' | head -n1)"
if [[ -z "$CHANGESET_ID" ]]; then
  echo "Failed to parse changeset ID" >&2
  exit 1
fi

log "Reviewing, listing, rebasing, and merging changeset $CHANGESET_ID"
(
  cd "$CHECKOUT_DIR"
  run_gs "$ACTIVE_ADDR" changeset review "$CHANGESET_ID"
  run_gs "$ACTIVE_ADDR" changeset list --limit 5
  run_gs "$ACTIVE_ADDR" changeset rebase "$CHANGESET_ID"
  run_gs "$ACTIVE_ADDR" changeset merge "$CHANGESET_ID"
  run_gs "$ACTIVE_ADDR" log
)

log "Smoke workflow passed against $ACTIVE_ADDR"
