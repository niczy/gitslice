#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="$REPO_ROOT/logs"

cd "$REPO_ROOT"

git pull --ff-only

LOG_DIR="$LOG_DIR" "$REPO_ROOT/ops/start_web_server.sh"

sudo mkdir -p "$LOG_DIR"
export LOG_DIR
envsubst '$LOG_DIR' < "$REPO_ROOT/ops/nginx.conf" | sudo tee /etc/nginx/nginx.conf > /dev/null
sudo systemctl reload nginx

