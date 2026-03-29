#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_LOG_DIR="${LOG_DIR:-./logs}"
PRODUCTION_ENV_FILE="${PRODUCTION_ENV_FILE:-$REPO_ROOT/ops/.env.production}"
LEGACY_PRODUCTION_ENV_FILE="$REPO_ROOT/ops/.env"
STAGING_ENV_FILE="${STAGING_ENV_FILE:-$REPO_ROOT/ops/.env.staging}"
CORE_SERVICE_PORT="${CORE_SERVICE_PORT:-50051}"
WEB_DEPLOY_TARGET="${WEB_DEPLOY_TARGET:-node}"
RUN_WEB_SSR="${RUN_WEB_SSR:-auto}"
LOCK_FILE="${REPO_ROOT}/.restart_all.lock"
DEFAULT_PATH="$HOME/.local/go/bin:$HOME/.local/protoc/bin:$HOME/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
NVM_NODE_BIN="${NVM_NODE_BIN:-}"

if [ -z "$NVM_NODE_BIN" ] && [ -d "$HOME/.nvm/versions/node" ]; then
  NVM_NODE_BIN="$(find "$HOME/.nvm/versions/node" -mindepth 2 -maxdepth 2 -type d -name bin 2>/dev/null | sort -V | tail -n 1 || true)"
fi

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

resolve_production_env_file() {
  if [ -f "$PRODUCTION_ENV_FILE" ]; then
    printf '%s\n' "$PRODUCTION_ENV_FILE"
    return 0
  fi
  if [ -f "$LEGACY_PRODUCTION_ENV_FILE" ]; then
    printf '%s\n' "$LEGACY_PRODUCTION_ENV_FILE"
    return 0
  fi
  return 1
}

read_env_value() {
  local file_path="$1"
  local key="$2"
  local default_value="$3"

  if [ -z "$file_path" ] || [ ! -f "$file_path" ]; then
    printf '%s\n' "$default_value"
    return 0
  fi

  local line
  line="$(grep -E "^(export[[:space:]]+)?${key}=" "$file_path" | tail -n 1 || true)"
  if [ -z "$line" ]; then
    printf '%s\n' "$default_value"
    return 0
  fi

  line="${line#export }"
  line="${line#${key}=}"
  line="${line%\"}"
  line="${line#\"}"
  line="${line%\'}"
  line="${line#\'}"
  printf '%s\n' "$line"
}

should_run_web_ssr() {
  case "$(printf '%s' "$RUN_WEB_SSR" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on)
      return 0
      ;;
    0|false|no|off)
      return 1
      ;;
    auto|"")
      [ "$WEB_DEPLOY_TARGET" = "node" ]
      return $?
      ;;
    *)
      return 1
      ;;
  esac
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

production_env_file="$(resolve_production_env_file || true)"

# Source production environment file if it exists. This lets the hourly cronjob
# pick up STORAGE_TYPE, POSTGRES_DSN, and the shared-VM production listener
# settings that are not available in the minimal cron environment.
if [ -n "$production_env_file" ] && [ -f "$production_env_file" ]; then
  set -a
  # shellcheck source=/dev/null
  . "$production_env_file"
  set +a
fi

acquire_lock

update_repo

log "Starting services..."
LOG_DIR="$SERVICE_LOG_DIR" "$REPO_ROOT/ops/start_web_server.sh"

log "Verifying all services are healthy..."
"$REPO_ROOT/ops/verify_deploy.sh" --local-only
log "All local services verified healthy"

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
if [ -f "$STAGING_ENV_FILE" ]; then
  log "  - Staging core:           localhost:$(read_env_value "$STAGING_ENV_FILE" CORE_SERVICE_PORT 50052)"
fi
if should_run_web_ssr; then
  log "  - Web SSR server:         localhost:4173"
else
  log "  - Public web:             Cloudflare Worker (${PUBLIC_WEB_BASE_URL:-unset})"
fi
