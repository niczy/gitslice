---
name: start-dev-servers
description: Start and verify gitslice local development services (core gRPC `:50051`, gateway `:8080`, web preview `:4173`). Use when asked to start local servers, boot the local dev stack, or recover missing local listeners and health checks during development.
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

3. Start local services with memory storage defaults:
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

- Use a persistent TTY process if the shell runner reaps background jobs after command exit:
```bash
cd /Users/nic/workspace/gitslice
CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 STORAGE_TYPE=memory \
  SKIP_GIT_POPULATION=1 OBJECT_STORE_TYPE=filesystem \
  ./core_server > logs/core_server.log 2>&1 &
cd web && npm run preview -- --host 127.0.0.1 --port 4173
```
