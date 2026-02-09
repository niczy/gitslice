#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PG_BIN_DIR="${PG_BIN_DIR:-/usr/lib/postgresql/14/bin}"
INITDB_BIN="$PG_BIN_DIR/initdb"
PG_CTL_BIN="$PG_BIN_DIR/pg_ctl"
PSQL_BIN="$PG_BIN_DIR/psql"

LOCAL_POSTGRES_DATA_DIR="${LOCAL_POSTGRES_DATA_DIR:-$REPO_ROOT/.postgres}"
LOCAL_POSTGRES_PORT="${LOCAL_POSTGRES_PORT:-55432}"
LOCAL_POSTGRES_DB="${LOCAL_POSTGRES_DB:-gitslice}"
LOCAL_POSTGRES_USER="${LOCAL_POSTGRES_USER:-$(id -un)}"
LOCAL_POSTGRES_LOG="${LOCAL_POSTGRES_LOG:-$REPO_ROOT/logs/local_postgres.log}"

for bin in "$INITDB_BIN" "$PG_CTL_BIN" "$PSQL_BIN"; do
  if [ ! -x "$bin" ]; then
    echo "required postgres binary missing: $bin" >&2
    exit 1
  fi
done

mkdir -p "$(dirname "$LOCAL_POSTGRES_LOG")"

if [ ! -f "$LOCAL_POSTGRES_DATA_DIR/PG_VERSION" ]; then
  "$INITDB_BIN" -D "$LOCAL_POSTGRES_DATA_DIR" -A trust -U "$LOCAL_POSTGRES_USER" >/dev/null
fi

set_conf() {
  local key="$1"
  local value="$2"
  local conf="$LOCAL_POSTGRES_DATA_DIR/postgresql.conf"
  if grep -qE "^[#[:space:]]*${key}[[:space:]]*=" "$conf"; then
    sed -i -E "s|^[#[:space:]]*${key}[[:space:]]*=.*|${key} = ${value}|" "$conf"
  else
    echo "${key} = ${value}" >>"$conf"
  fi
}

set_conf "listen_addresses" "'127.0.0.1'"
set_conf "port" "$LOCAL_POSTGRES_PORT"
set_conf "unix_socket_directories" "'$LOCAL_POSTGRES_DATA_DIR'"

if ! "$PG_CTL_BIN" -D "$LOCAL_POSTGRES_DATA_DIR" status >/dev/null 2>&1; then
  "$PG_CTL_BIN" -D "$LOCAL_POSTGRES_DATA_DIR" -l "$LOCAL_POSTGRES_LOG" -w start >/dev/null
fi

ADMIN_DSN="postgresql://$LOCAL_POSTGRES_USER@127.0.0.1:$LOCAL_POSTGRES_PORT/postgres?sslmode=disable"
if ! [[ "$LOCAL_POSTGRES_DB" =~ ^[a-zA-Z0-9_]+$ ]]; then
  echo "invalid LOCAL_POSTGRES_DB: $LOCAL_POSTGRES_DB" >&2
  exit 1
fi
if ! "$PSQL_BIN" "$ADMIN_DSN" -tAc "SELECT 1 FROM pg_database WHERE datname = '$LOCAL_POSTGRES_DB'" | grep -q 1; then
  "$PSQL_BIN" "$ADMIN_DSN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$LOCAL_POSTGRES_DB\"" >/dev/null
fi

echo "postgresql://$LOCAL_POSTGRES_USER@127.0.0.1:$LOCAL_POSTGRES_PORT/$LOCAL_POSTGRES_DB?sslmode=disable"
