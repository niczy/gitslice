#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_LOG_DIR="${LOG_DIR:-./logs}"
CORE_SERVICE_PORT="${CORE_SERVICE_PORT:-50051}"
LOCK_FILE="${REPO_ROOT}/.restart_all.lock"
DEFAULT_PATH="$HOME/.local/go/bin:$HOME/.local/protoc/bin:$HOME/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
NVM_NODE_BIN="${NVM_NODE_BIN:-}"

if [ -z "$NVM_NODE_BIN" ] && [ -d "$HOME/.nvm/versions/node" ]; then
  NVM_NODE_BIN="$(find "$HOME/.nvm/versions/node" -mindepth 2 -maxdepth 2 -type d -name bin 2>/dev/null | sort -V | tail -n 1 || true)"
fi

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

ensure_path() {
  export PATH="${PATH:-$DEFAULT_PATH}"
  case ":$PATH:" in
    *":$HOME/.local/go/bin:"*) ;;
    *) PATH="$HOME/.local/go/bin:$PATH" ;;
  esac
  case ":$PATH:" in
    *":$HOME/.local/protoc/bin:"*) ;;
    *) PATH="$HOME/.local/protoc/bin:$PATH" ;;
  esac
  case ":$PATH:" in
    *":$HOME/go/bin:"*) ;;
    *) PATH="$HOME/go/bin:$PATH" ;;
  esac
  if [ -n "$NVM_NODE_BIN" ] && [ -d "$NVM_NODE_BIN" ]; then
    case ":$PATH:" in
      *":$NVM_NODE_BIN:"*) ;;
      *) PATH="$NVM_NODE_BIN:$PATH" ;;
    esac
  fi
  export PATH
}

acquire_lock() {
  exec 9>"$LOCK_FILE"
  if ! flock -n 9; then
    log "Another restart_all.sh run is in progress; skipping."
    exit 0
  fi
}

update_repo() {
  log "Pulling latest changes..."
  # Only update if the current branch has an upstream tracking branch.
  if git rev-parse --abbrev-ref --symbolic-full-name @{u} >/dev/null 2>&1; then
    git fetch --prune
    git pull --ff-only
  else
    log "No upstream tracking branch configured, skipping git pull."
  fi
}

cd "$REPO_ROOT"
ensure_path

# Source production environment file if it exists.  This lets the hourly
# cronjob pick up STORAGE_TYPE, POSTGRES_DSN, and any other production
# settings that are not available in the minimal cron environment.
if [ -f "$REPO_ROOT/ops/.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "$REPO_ROOT/ops/.env"
  set +a
fi

acquire_lock

update_repo

log "Starting services..."
LOG_DIR="$SERVICE_LOG_DIR" "$REPO_ROOT/ops/start_web_server.sh"

log "Verifying all services are healthy..."
# Final verification that all services are up before starting nginx
if ! curl -sf "http://localhost:${CORE_SERVICE_PORT}/health" >/dev/null 2>&1; then
  log "ERROR: Core HTTP health endpoint is not healthy on port ${CORE_SERVICE_PORT}"
  exit 1
fi
if ! nc -z localhost "${CORE_SERVICE_PORT}" 2>/dev/null; then
  log "ERROR: Core gRPC server is not listening on port ${CORE_SERVICE_PORT}"
  exit 1
fi
log "All services verified healthy"

setup_cronjob() {
  local cron_path="$DEFAULT_PATH"
  if [ -n "$NVM_NODE_BIN" ] && [ -d "$NVM_NODE_BIN" ]; then
    cron_path="$NVM_NODE_BIN:$cron_path"
  fi
  local cron_line="0 * * * * PATH=$cron_path bash $REPO_ROOT/ops/restart_all.sh >> $REPO_ROOT/logs/cron.log 2>&1"

  # Replace only this job and preserve other user cron entries.
  (
    crontab -l 2>/dev/null | grep -vF "$REPO_ROOT/ops/restart_all.sh" || true
    echo "$cron_line"
  ) | crontab -
  log "Installed hourly cronjob in user crontab"
}

setup_cronjob

log "Deployment complete!"
log "Services:"
log "  - Core (gRPC + HTTP):     localhost:${CORE_SERVICE_PORT}"
log "  - Web SSR server:         localhost:4173"
