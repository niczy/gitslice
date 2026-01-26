#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_LOG_DIR="${LOG_DIR:-./logs}"
NGINX_LOG_DIR="${NGINX_LOG_DIR:-/var/log/nginx}"
GATEWAY_PORT="${GATEWAY_PORT:-8080}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

cd "$REPO_ROOT"

log "Pulling latest changes..."
# Only pull if the current branch has an upstream tracking branch
if git rev-parse --abbrev-ref --symbolic-full-name @{u} >/dev/null 2>&1; then
  git pull --ff-only
else
  log "No upstream tracking branch configured, skipping git pull"
fi

log "Starting services..."
LOG_DIR="$SERVICE_LOG_DIR" "$REPO_ROOT/ops/start_web_server.sh"

log "Verifying all services are healthy..."
# Final verification that all services are up before starting nginx
if ! curl -sf "http://localhost:${GATEWAY_PORT}/health" >/dev/null 2>&1; then
  log "ERROR: Gateway service is not healthy on port ${GATEWAY_PORT}"
  exit 1
fi
if ! nc -z localhost 50052 2>/dev/null; then
  log "ERROR: Admin service is not listening on port 50052"
  exit 1
fi
log "All services verified healthy"

log "Configuring and restarting nginx..."
LOG_DIR="$NGINX_LOG_DIR" envsubst '$LOG_DIR' <"$REPO_ROOT/ops/nginx.conf" | sudo tee /etc/nginx/nginx.conf >/dev/null
sudo systemctl restart nginx

log "Deployment complete!"
log "Services:"
log "  - Slice service (gRPC):  localhost:50051"
log "  - HTTP Gateway:           localhost:${GATEWAY_PORT}"
log "  - Admin service (gRPC):   localhost:50052"
log "  - Web preview:            localhost:4173"
log ""
log "Nginx proxying:"
log "  - https://agenttools.dev -> Web UI"
log "  - https://api.agenttools.dev -> Services"
