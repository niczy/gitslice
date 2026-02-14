---
name: restart-server
description: Safely restart gitslice production services and verify end-to-end health. Use when asked to restart prod servers, recover from `502` on `agenttools.dev`, diagnose missing listeners (`4173`, `8080`, `50051`), or stabilize PM2/core process supervision after deploys.
---

# Restart Server

## Restart Workflow

1. Change to repo root:
```bash
cd /home/nic/workspace/gitslice
```
2. Read current operational notes before acting:
```bash
sed -n '1,200p' local_dev.md
```
3. Run the supervised restart path:
```bash
./ops/restart_all.sh
```
4. Verify listeners after restart:
```bash
ss -ltnp 2>/dev/null | rg '(:4173|:8080|:50051)'
```
5. Verify local health:
```bash
curl -sf http://127.0.0.1:8080/health
```
6. Verify public health:
```bash
curl -sf https://agenttools.dev/
curl -sf https://agenttools.dev/v1/global/state
```
7. Verify supervisor state:
```bash
pm2 ls --no-color
```

## Troubleshoot Failures

- Check upstream listeners before blaming Nginx when `agenttools.dev` returns `502`.
- Avoid running `ops/start_web_server.sh` directly when `PATH` may be incomplete; missing `protoc` can break core startup and leave upstreams down.
- Resolve duplicate core processes if logs show `listen tcp :50051: bind: address already in use`:
```bash
ps -ef | rg core_server
pkill -f '/home/nic/workspace/gitslice/core_server'
./ops/restart_all.sh
```
- Use PM2-managed restart if processes exit right after shell completion:
```bash
pm2 start /home/nic/workspace/gitslice/ops/ecosystem.config.cjs --update-env
pm2 restart gitslice-core --update-env
pm2 restart gitslice-web --update-env
```
- Treat `globalCommitHash=global-init` with empty history from `/v1/global/state` as missing genesis population/import for the active server instance.

## Preserve Production Assumptions

- Keep `ops/restart_all.sh` safe for unattended hourly runs.
- Preserve the `git pull --ff-only` update flow.
- Keep lock and health-check behavior so cron executions do not overlap.
- Keep prod defaults on core startup: `SKIP_GIT_POPULATION=1`.
- Ensure object store configuration exists when `STORAGE_TYPE=postgres`.
