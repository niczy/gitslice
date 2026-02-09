#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$REPO_ROOT/ops/prod.env}"

if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

export STORAGE_TYPE="${STORAGE_TYPE:-postgres}"
if [ "$STORAGE_TYPE" = "postgres" ] && [ -z "${POSTGRES_DSN:-}" ]; then
  export POSTGRES_DSN="$("$REPO_ROOT/ops/ensure_local_postgres.sh")"
fi

export OBJECT_STORE_TYPE="${OBJECT_STORE_TYPE:-filesystem}"
export OBJECT_STORE_DIR="${OBJECT_STORE_DIR:-$REPO_ROOT/.objectstore}"

cd "$REPO_ROOT"
go run ./scripts/genesis_init
