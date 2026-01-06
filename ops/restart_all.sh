#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_LOG_DIR="${LOG_DIR:-./logs}"
NGINX_LOG_DIR="${NGINX_LOG_DIR:-/var/log/nginx}"

cd "$REPO_ROOT"

git pull --ff-only

LOG_DIR="$SERVICE_LOG_DIR" "$REPO_ROOT/ops/start_web_server.sh"

LOG_DIR="$NGINX_LOG_DIR" envsubst <"$REPO_ROOT/ops/nginx.conf" | sudo tee /etc/nginx/nginx.conf >/dev/null
sudo systemctl restart nginx

