#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ENV_NAME=""
APP_NAME=""
ENV_FILE=""
PM2_BIN="${PM2_BIN:-}"
PM2_NODE_BIN="${PM2_NODE_BIN:-}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

usage() {
  cat <<'EOF'
Usage: ops/deploy.sh --env <staging|production> --app <web|api> [--env-file <path>]

Deploys a specific application using the environment file for that deployment
target.

Examples:
  ./ops/deploy.sh --env staging --app web
  ./ops/deploy.sh --env production --app web
  ./ops/deploy.sh --env staging --app api
  ./ops/deploy.sh --env staging --app web --env-file ops/staging/.env

Default env files:
  staging:    ops/staging/.env
  production: ops/.env.production (fallback: ops/.env)
EOF
}

default_core_bind_addr() {
  printf '%s\n' "127.0.0.1"
}

default_core_service_port() {
  case "$ENV_NAME" in
    staging)
      printf '%s\n' "50052"
      ;;
    production)
      printf '%s\n' "50051"
      ;;
    *)
      return 1
      ;;
  esac
}

resolve_env_file() {
  if [ -n "$ENV_FILE" ]; then
    printf '%s\n' "$ENV_FILE"
    return 0
  fi

  case "$ENV_NAME" in
    staging)
      if [ -f "$REPO_ROOT/ops/staging/.env" ]; then
        printf '%s\n' "$REPO_ROOT/ops/staging/.env"
        return 0
      fi
      if [ -f "$REPO_ROOT/ops/.env.staging" ]; then
        printf '%s\n' "$REPO_ROOT/ops/.env.staging"
        return 0
      fi
      ;;
    production)
      if [ -f "$REPO_ROOT/ops/.env.production" ]; then
        printf '%s\n' "$REPO_ROOT/ops/.env.production"
        return 0
      fi
      if [ -f "$REPO_ROOT/ops/.env" ]; then
        printf '%s\n' "$REPO_ROOT/ops/.env"
        return 0
      fi
      ;;
  esac

  return 1
}

require_command() {
  local command_name="$1"
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Required command not found: $command_name" >&2
    exit 1
  fi
}

ensure_path() {
  local default_path="$HOME/.local/go/bin:$HOME/.local/protoc/bin:$HOME/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  export PATH="${PATH:-$default_path}"
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
  if [ -d "$HOME/.nvm/versions/node" ]; then
    local nvm_node_bin
    nvm_node_bin="$(find "$HOME/.nvm/versions/node" -mindepth 2 -maxdepth 2 -type d -name bin 2>/dev/null | sort -V | tail -n 1 || true)"
    if [ -n "$nvm_node_bin" ] && [ -d "$nvm_node_bin" ]; then
      case ":$PATH:" in
        *":$nvm_node_bin:"*) ;;
        *) PATH="$nvm_node_bin:$PATH" ;;
      esac
    fi
  fi
  export PATH
}

discover_pm2_bin() {
  if [ -n "$PM2_BIN" ]; then
    if [ -n "$PM2_NODE_BIN" ] && [ -x "$PM2_NODE_BIN" ] && [ -f "$PM2_BIN" ]; then
      printf '%s\n' "$PM2_BIN"
      return 0
    fi
    if [ -x "$PM2_BIN" ]; then
      printf '%s\n' "$PM2_BIN"
      return 0
    fi
  fi

  if command -v pm2 >/dev/null 2>&1; then
    PM2_BIN="$(command -v pm2)"
    printf '%s\n' "$PM2_BIN"
    return 0
  fi

  local candidate=""
  candidate="$(find "$HOME/.nvm/versions/node" -mindepth 3 -maxdepth 3 -type f -path '*/bin/pm2' 2>/dev/null | sort -V | tail -n 1 || true)"
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    PM2_BIN="$candidate"
    PM2_NODE_BIN=""
    printf '%s\n' "$PM2_BIN"
    return 0
  fi

  candidate="$(find "$HOME/.nvm/versions/node" -type f -path '*/lib/node_modules/pm2/lib/binaries/CLI.js' 2>/dev/null | sort -V | tail -n 1 || true)"
  if [ -n "$candidate" ] && [ -f "$candidate" ]; then
    local node_bin=""
    node_bin="${candidate%/lib/node_modules/pm2/lib/binaries/CLI.js}/bin/node"
    if [ -x "$node_bin" ]; then
      PM2_BIN="$candidate"
      PM2_NODE_BIN="$node_bin"
      printf '%s\n' "$PM2_BIN"
      return 0
    fi
  fi

  return 1
}

run_pm2() {
  if ! discover_pm2_bin >/dev/null 2>&1; then
    echo "Required command not found: pm2" >&2
    exit 1
  fi

  if [ -n "$PM2_NODE_BIN" ]; then
    "$PM2_NODE_BIN" "$PM2_BIN" "$@"
    return $?
  fi

  "$PM2_BIN" "$@"
}

wait_for_local_health() {
  local bind_addr="$1"
  local port="$2"
  local attempts=30
  local attempt=1
  while [ "$attempt" -le "$attempts" ]; do
    if curl -fsS "http://${bind_addr}:${port}/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    attempt=$((attempt + 1))
  done
  return 1
}

sync_worker_secret() {
  local secret_name="$1"
  local secret_value="$2"
  if [ -z "$secret_value" ]; then
    return 0
  fi

  log "Syncing Worker secret ${secret_name} for ${ENV_NAME}"
  (
    cd "$REPO_ROOT/web"
    printf '%s' "$secret_value" | npx wrangler secret put "$secret_name" --config wrangler.jsonc --env "$ENV_NAME" >/dev/null
  )
}

build_worker_config() {
  local base_config="$REPO_ROOT/web/wrangler.jsonc"
  local output_config=""

  output_config="$(mktemp "${TMPDIR:-/tmp}/gitslice-wrangler-${ENV_NAME}-XXXXXX.jsonc")"
  node - "$base_config" "$output_config" "$ENV_NAME" <<'NODE'
const fs = require('node:fs');
const path = require('node:path');

const [baseConfigPath, outputConfigPath, envName] = process.argv.slice(2);
const config = JSON.parse(fs.readFileSync(baseConfigPath, 'utf8'));
const configDir = path.dirname(baseConfigPath);
const envConfig = { ...(config.env?.[envName] || {}) };
const vars = { ...(envConfig.vars || {}) };

const workerVars = {
  AUTH_PROVIDER: process.env.AUTH_PROVIDER || '',
  ALLOW_DEV_LOGIN: process.env.ALLOW_DEV_LOGIN || '',
  CLERK_PUBLISHABLE_KEY: process.env.CLERK_PUBLISHABLE_KEY || process.env.VITE_CLERK_PUBLISHABLE_KEY || '',
  VITE_CLERK_PUBLISHABLE_KEY: process.env.VITE_CLERK_PUBLISHABLE_KEY || process.env.CLERK_PUBLISHABLE_KEY || '',
  WORKOS_CLIENT_ID: process.env.WORKOS_CLIENT_ID || '',
  WORKOS_REDIRECT_URI: process.env.WORKOS_REDIRECT_URI || '',
  WORKOS_JWKS_URL: process.env.WORKOS_JWKS_URL || '',
  WORKOS_AUTHKIT_DOMAIN: process.env.WORKOS_AUTHKIT_DOMAIN || '',
};

for (const [key, value] of Object.entries(workerVars)) {
  if (String(value || '').trim()) {
    vars[key] = value;
  } else {
    delete vars[key];
  }
}

envConfig.vars = vars;
config.env = { ...(config.env || {}), [envName]: envConfig };
if (config.main) {
  config.main = path.resolve(configDir, config.main);
}
if (config.assets?.directory) {
  config.assets = {
    ...config.assets,
    directory: path.resolve(configDir, config.assets.directory),
  };
}
fs.writeFileSync(outputConfigPath, `${JSON.stringify(config, null, 2)}\n`);
NODE

  printf '%s\n' "$output_config"
}

sync_worker_secrets() {
  local auth_provider=""
  auth_provider="$(printf '%s' "${AUTH_PROVIDER:-local}" | tr '[:upper:]' '[:lower:]')"

  if [ -z "${AUTH_SECRET:-}" ]; then
    echo "AUTH_SECRET must be set in the resolved env file for web deploys" >&2
    exit 1
  fi

  if [ "$auth_provider" = "workos" ]; then
    if [ -z "${WORKOS_CLIENT_ID:-}" ] || [ -z "${WORKOS_API_KEY:-}" ] || [ -z "${WORKOS_COOKIE_PASSWORD:-}" ]; then
      echo "WORKOS_CLIENT_ID, WORKOS_API_KEY, and WORKOS_COOKIE_PASSWORD must be set when AUTH_PROVIDER=workos" >&2
      exit 1
    fi
  fi
  if [ "$auth_provider" = "clerk" ]; then
    if [ -z "${CLERK_PUBLISHABLE_KEY:-}" ] || [ -z "${CLERK_SECRET_KEY:-}" ]; then
      echo "CLERK_PUBLISHABLE_KEY and CLERK_SECRET_KEY must be set when AUTH_PROVIDER=clerk" >&2
      exit 1
    fi
  fi

  sync_worker_secret "AUTH_SECRET" "${AUTH_SECRET:-}"
  sync_worker_secret "CLERK_SECRET_KEY" "${CLERK_SECRET_KEY:-}"
  sync_worker_secret "CLERK_JWT_KEY" "${CLERK_JWT_KEY:-}"
  sync_worker_secret "WORKOS_API_KEY" "${WORKOS_API_KEY:-}"
  sync_worker_secret "WORKOS_COOKIE_PASSWORD" "${WORKOS_COOKIE_PASSWORD:-}"
}

validate_api_env() {
  local auth_provider=""
  local object_store_type=""
  local storage_type=""
  local missing=()
  auth_provider="$(printf '%s' "${AUTH_PROVIDER:-local}" | tr '[:upper:]' '[:lower:]')"
  storage_type="$(printf '%s' "${STORAGE_TYPE:-postgres}" | tr '[:upper:]' '[:lower:]')"
  object_store_type="$(printf '%s' "${OBJECT_STORE_TYPE:-r2}" | tr '[:upper:]' '[:lower:]')"

  case "$storage_type" in
    postgres)
      if [ -z "${POSTGRES_DSN:-}" ] && [ -z "${NEON_DB:-}" ]; then
        missing+=("POSTGRES_DSN-or-NEON_DB")
      fi
      ;;
    memory)
      :
      ;;
    *)
      echo "Unsupported STORAGE_TYPE for api deploy: ${STORAGE_TYPE:-}" >&2
      exit 1
      ;;
  esac

  case "$object_store_type" in
    r2)
      [ -n "${R2_BUCKET:-}" ] || missing+=("R2_BUCKET")
      [ -n "${R2_ENDPOINT:-}" ] || missing+=("R2_ENDPOINT")
      [ -n "${R2_ACCESS_KEY_ID:-}" ] || missing+=("R2_ACCESS_KEY_ID")
      [ -n "${R2_SECRET_ACCESS_KEY:-}" ] || missing+=("R2_SECRET_ACCESS_KEY")
      ;;
    filesystem|fs|file)
      : "${OBJECT_STORE_DIR:=$REPO_ROOT/.objectstore}"
      ;;
    gcs|"")
      :
      ;;
    *)
      echo "Unsupported OBJECT_STORE_TYPE for api deploy: ${OBJECT_STORE_TYPE:-}" >&2
      exit 1
      ;;
  esac

  if [ "$auth_provider" = "workos" ]; then
    [ -n "${WORKOS_CLIENT_ID:-}" ] || missing+=("WORKOS_CLIENT_ID")
  fi
  if [ "$auth_provider" = "clerk" ]; then
    [ -n "${AUTH_SECRET:-}" ] || missing+=("AUTH_SECRET")
    [ -n "${CLERK_SECRET_KEY:-}" ] || missing+=("CLERK_SECRET_KEY")
    [ -n "${CLERK_PUBLISHABLE_KEY:-}" ] || missing+=("CLERK_PUBLISHABLE_KEY")
  fi

  if [ "${#missing[@]}" -gt 0 ]; then
    echo "Missing required api env values in $resolved_env_file: ${missing[*]}" >&2
    echo "Populate them in the env file or override with --env-file before deploying ${ENV_NAME} api." >&2
    exit 1
  fi
}

deploy_web() {
  local worker_config=""
  case "$ENV_NAME" in
    staging|production) ;;
    *)
      echo "Unsupported deploy environment for web: $ENV_NAME" >&2
      exit 1
      ;;
  esac

  if [ -z "${CLOUDFLARE_API_TOKEN:-}" ]; then
    echo "CLOUDFLARE_API_TOKEN must be set in the resolved env file for web deploys" >&2
    exit 1
  fi

  require_command npm

  sync_worker_secrets
  worker_config="$(build_worker_config)"
  trap 'rm -f "$worker_config"' RETURN

  log "Deploying web app for $ENV_NAME"
  (
    cd "$REPO_ROOT/web"
    npm run build
    npx wrangler deploy --config "$worker_config" --env "$ENV_NAME"
  )
  log "Web deploy finished for $ENV_NAME"
}

deploy_api() {
  local app_name=""
  local env_var_name=""
  local bind_addr=""
  local port=""
  case "$ENV_NAME" in
    staging)
      app_name="gitslice-core-staging"
      env_var_name="STAGING_ENV_FILE"
      ;;
    production)
      app_name="gitslice-core-production"
      env_var_name="PRODUCTION_ENV_FILE"
      ;;
    *)
      echo "Unsupported deploy environment for api: $ENV_NAME" >&2
      exit 1
      ;;
  esac

  bind_addr="${CORE_BIND_ADDR:-$(default_core_bind_addr)}"
  port="${CORE_SERVICE_PORT:-$(default_core_service_port)}"
  validate_api_env

  require_command make
  require_command curl

  log "Building core server for $ENV_NAME"
  (
    cd "$REPO_ROOT"
    make build-core
  )

  log "Restarting ${app_name} via PM2"
  (
    export "$env_var_name=$resolved_env_file"
    cd "$REPO_ROOT"
    run_pm2 startOrRestart ops/ecosystem.config.cjs --only "$app_name" --update-env >/dev/null
  )

  if ! wait_for_local_health "$bind_addr" "$port"; then
    echo "API deploy did not become healthy on ${bind_addr}:${port}" >&2
    exit 1
  fi

  log "API deploy finished for $ENV_NAME on ${bind_addr}:${port}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --env)
      if [ $# -lt 2 ]; then
        echo "--env requires a value" >&2
        usage >&2
        exit 1
      fi
      ENV_NAME="$2"
      shift 2
      ;;
    --app)
      if [ $# -lt 2 ]; then
        echo "--app requires a value" >&2
        usage >&2
        exit 1
      fi
      APP_NAME="$2"
      shift 2
      ;;
    --env-file)
      if [ $# -lt 2 ]; then
        echo "--env-file requires a value" >&2
        usage >&2
        exit 1
      fi
      ENV_FILE="$2"
      shift 2
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

if [ -z "$ENV_NAME" ] || [ -z "$APP_NAME" ]; then
  usage >&2
  exit 1
fi

case "$ENV_NAME" in
  staging|production) ;;
  *)
    echo "Unsupported --env value: $ENV_NAME" >&2
    usage >&2
    exit 1
    ;;
esac

case "$APP_NAME" in
  web|api) ;;
  *)
    echo "Unsupported --app value: $APP_NAME (supported: web, api)" >&2
    usage >&2
    exit 1
    ;;
esac

resolved_env_file="$(resolve_env_file || true)"
if [ -z "$resolved_env_file" ] || [ ! -f "$resolved_env_file" ]; then
  echo "Unable to resolve env file for $ENV_NAME deployment" >&2
  exit 1
fi

ensure_path
log "Using env file: $resolved_env_file"
set -a
# shellcheck source=/dev/null
. "$resolved_env_file"
set +a

case "$APP_NAME" in
  web)
    deploy_web
    ;;
  api)
    deploy_api
    ;;
esac
