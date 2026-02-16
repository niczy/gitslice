---
name: start-dev-servers
description: Start and verify gitslice local development services using `ops/start_web_server.sh`, then front them with a local Nginx reverse proxy derived from `ops/nginx.conf`. Use when asked to boot/restart local dev servers, recover missing listeners (`50051`, `8080`, `4173`), or run the local stack behind Nginx for realistic routing.
---

# Start Dev Servers

## Startup Workflow

1. Change to repo root and read current local notes:
```bash
cd /Users/nic/workspace/gitslice
sed -n '1,200p' local_dev.md
```

2. Ensure web dependencies exist:
```bash
if [ ! -d web/node_modules ]; then
  (cd web && npm ci)
fi
```

3. Start local services via `ops/start_web_server.sh` with local-safe storage settings:
```bash
STORAGE_TYPE=memory SKIP_GIT_POPULATION=1 OBJECT_STORE_TYPE=filesystem \
  bash ops/start_web_server.sh
```

4. Verify local listeners and health:
```bash
for p in 4173 8080 50051; do
  nc -z 127.0.0.1 "$p" && echo "port $p: listening" || echo "port $p: down"
done

curl -sf http://127.0.0.1:8080/health
curl -sf http://127.0.0.1:4173/
```

5. Re-check stability after a short delay:
```bash
sleep 3
for p in 4173 8080 50051; do
  nc -z 127.0.0.1 "$p" && echo "port $p: listening" || echo "port $p: down"
done
curl -sf http://127.0.0.1:8080/health
```

## Local Nginx Workflow

1. Ensure Nginx is installed:
```bash
command -v nginx || echo "Install nginx first (macOS: brew install nginx)"
```

2. Generate a local Nginx config from `ops/nginx.conf`:
```bash
cd /Users/nic/workspace/gitslice
mkdir -p .tmp/nginx-local logs/nginx
awk -v repo="$PWD" '
{
  if ($0 ~ /access_log \/var\/log\/nginx\/gitslice\.access\.log main;/) {
    print "    access_log " repo "/logs/nginx/gitslice.access.log main;"; next
  }
  if ($0 ~ /error_log \/var\/log\/nginx\/gitslice\.error\.log;/) {
    print "    error_log " repo "/logs/nginx/gitslice.error.log;"; next
  }
  if ($0 ~ /listen 80 http2;/) { print "        listen 4181 http2;"; next }
  if ($0 ~ /server_name api\.agenttools\.dev;/) { print "        server_name _;"; next }
  if ($0 ~ /listen 80;/) { print "        listen 4180;"; next }
  if ($0 ~ /server_name agenttools\.dev;/) { print "        server_name _;"; next }
  print
}
' ops/nginx.conf > .tmp/nginx-local/nginx.local.conf
```

3. Start local Nginx with that generated config:
```bash
nginx -p "$PWD/.tmp/nginx-local" -c "$PWD/.tmp/nginx-local/nginx.local.conf"
```

4. Verify proxy listeners and routing:
```bash
nc -z 127.0.0.1 4180 && echo "nginx web/rest proxy up"
nc -z 127.0.0.1 4181 && echo "nginx grpc proxy up"
curl -sf http://127.0.0.1:4180/
curl -sf http://127.0.0.1:4180/v1/global/state
```

5. Stop local Nginx:
```bash
nginx -p "$PWD/.tmp/nginx-local" -c "$PWD/.tmp/nginx-local/nginx.local.conf" -s stop
```

## Troubleshoot Common Failures

- Fix missing PostgreSQL DSN errors from prod defaults:
```bash
STORAGE_TYPE=memory SKIP_GIT_POPULATION=1 OBJECT_STORE_TYPE=filesystem \
  bash ops/start_web_server.sh
```

- Fix missing web packages (for example, `Cannot find package '@auth/core'`):
```bash
cd /Users/nic/workspace/gitslice/web && npm ci
```

- Fix duplicate core servers (`address already in use` on `:50051`):
```bash
pkill -f '[/]core_server' || true
bash /Users/nic/workspace/gitslice/ops/start_web_server.sh
```

- Fix missing Nginx binary:
```bash
brew install nginx
```

- Use `ops/start_web_server.sh` for local startup; avoid `ops/restart_all.sh` for day-to-day local dev since it performs `git pull --ff-only` and installs a cron entry.
