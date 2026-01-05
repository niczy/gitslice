#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DIR="$REPO_ROOT/web"
LOG_FILE="$WEB_DIR/preview.log"

cd "$WEB_DIR"

if [ ! -d node_modules ]; then
  npm ci
fi

npm run build

pkill -f "vite preview" >/dev/null 2>&1 || true

nohup npm run preview -- --host 0.0.0.0 --port 4173 > "$LOG_FILE" 2>&1 &

