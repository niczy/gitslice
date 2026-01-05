#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$REPO_ROOT"

git pull --ff-only

"$REPO_ROOT/ops/start_web_server.sh"

sudo cp "$REPO_ROOT/ops/nginx.conf" /etc/nginx/nginx.conf
sudo systemctl reload nginx

