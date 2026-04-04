#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PRODUCTION_ENV_FILE="${PRODUCTION_ENV_FILE:-$REPO_ROOT/ops/.env.production}"
LEGACY_PRODUCTION_ENV_FILE="$REPO_ROOT/ops/.env"
STAGING_ENV_FILE="${STAGING_ENV_FILE:-$REPO_ROOT/ops/.env.staging}"
if [ ! -f "$STAGING_ENV_FILE" ] && [ -f "$REPO_ROOT/ops/staging/.env" ]; then
  STAGING_ENV_FILE="$REPO_ROOT/ops/staging/.env"
fi
MODE="full"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

usage() {
  cat <<'EOF'
Usage: ops/verify_deploy.sh [--local-only | --public-only]

Verifies the split Worker + VM deployment topology:
  - local production/staging core health
  - public production/staging web and API health
  - R2 config presence for each configured environment
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --local-only)
      MODE="local"
      shift
      ;;
    --public-only)
      MODE="public"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

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

check_url() {
  local label="$1"
  local url="$2"
  log "Checking $label at $url"
  curl -fsS "$url" >/dev/null
}

verify_local_target() {
  local role="$1"
  local env_file="$2"
  local default_port="$3"
  local port

  port="$(read_env_value "$env_file" CORE_SERVICE_PORT "$default_port")"
  log "Checking local $role core on 127.0.0.1:$port"
  curl -fsS "http://127.0.0.1:${port}/health" >/dev/null
  nc -z 127.0.0.1 "$port"
}

verify_r2_target() {
  local role="$1"
  local env_file="$2"
  local object_store_type endpoint bucket prefix access_key secret_key

  if [ -z "$env_file" ] || [ ! -f "$env_file" ]; then
    log "Skipping $role R2 config verification because no env file is configured"
    return 0
  fi

  object_store_type="$(read_env_value "$env_file" OBJECT_STORE_TYPE "r2")"
  if [ "$object_store_type" != "r2" ]; then
    echo "$role OBJECT_STORE_TYPE must be r2, got $object_store_type" >&2
    exit 1
  fi

  endpoint="$(read_env_value "$env_file" R2_ENDPOINT "")"
  bucket="$(read_env_value "$env_file" R2_BUCKET "")"
  prefix="$(read_env_value "$env_file" R2_PREFIX "")"
  access_key="$(read_env_value "$env_file" R2_ACCESS_KEY_ID "")"
  secret_key="$(read_env_value "$env_file" R2_SECRET_ACCESS_KEY "")"

  if [ -z "$endpoint" ] || [ -z "$bucket" ] || [ -z "$prefix" ] || [ -z "$access_key" ] || [ -z "$secret_key" ]; then
    echo "$role R2 configuration is incomplete" >&2
    exit 1
  fi

  log "Verified $role R2 config: bucket=$bucket prefix=$prefix endpoint=$endpoint"
}

production_env_file="$(resolve_production_env_file || true)"
staging_env_file=""
if [ -f "$STAGING_ENV_FILE" ]; then
  staging_env_file="$STAGING_ENV_FILE"
fi

if [ "$MODE" != "public" ]; then
  verify_local_target "production" "$production_env_file" "50051"
  verify_r2_target "production" "$production_env_file"
  if [ -n "$staging_env_file" ]; then
    verify_local_target "staging" "$staging_env_file" "50052"
    verify_r2_target "staging" "$staging_env_file"
  fi
fi

if [ "$MODE" != "local" ]; then
  check_url "production web" "https://gitslice.io/"
  check_url "production API" "https://api.gitslice.io/v1/global/state"
  if [ -n "$staging_env_file" ]; then
    check_url "staging web" "https://agenttools.dev/"
    check_url "staging API" "https://api.agenttools.dev/v1/global/state"
  fi
fi

log "Deployment verification passed"
