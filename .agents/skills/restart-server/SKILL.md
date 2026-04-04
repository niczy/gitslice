---
name: restart-server
description: Safely restart gitslice VM-hosted core services and verify end-to-end health. Use when asked to restart prod or staging API services, recover from API `502`/`522` failures, diagnose missing listeners (`50051`, `50052`), or stabilize PM2 core supervision after deploys.
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
3. Run the unattended-safe restart path:
```bash
./ops/restart_all.sh
```
4. Verify core listeners after restart:
```bash
ss -ltnp 2>/dev/null | rg '(:50051|:50052)'
```
5. Verify local health:
```bash
curl -sf http://127.0.0.1:50051/health
curl -sf http://127.0.0.1:50052/health
```
6. Verify public health:
```bash
curl -sf https://gitslice.io/
curl -sf https://api.gitslice.io/v1/global/state
curl -sf https://agenttools.dev/
curl -sf https://api.agenttools.dev/v1/global/state
```
7. Verify supervisor state:
```bash
pm2 ls --no-color
```
8. Re-check stability after a short delay (catch post-start exits):
```bash
sleep 5
ss -ltnp 2>/dev/null | rg '(:50051|:50052)'
curl -sf http://127.0.0.1:50051/health
curl -sf http://127.0.0.1:50052/health
curl -sf https://api.gitslice.io/v1/global/state
curl -sf https://api.agenttools.dev/v1/global/state
pm2 ls --no-color
```

## Troubleshoot Failures

- Use the shared verification helper when restart output looks healthy but public routes still fail:
```bash
./ops/verify_deploy.sh --local-only
./ops/verify_deploy.sh --public-only
```
- Check VM listeners before blaming Nginx when API hosts fail. The VM only runs core services now; the public web app is served by Cloudflare Workers.
- If `gitslice.io` or `agenttools.dev` fails while the matching API host is healthy, the problem is probably the Worker deployment or Cloudflare route/secrets, not PM2.
- Avoid running `ops/start_web_server.sh` directly when `PATH` may be incomplete; missing `protoc` can break core startup and leave API upstreams down.
- Resolve duplicate core processes if logs show `listen tcp :50051: bind: address already in use` or the same for `:50052`:
```bash
ps -ef | rg core_server
pkill -f '/home/nic/workspace/gitslice/core_server'
./ops/restart_all.sh
```
- Use PM2-managed restart if processes exit right after shell completion:
```bash
pm2 start /home/nic/workspace/gitslice/ops/ecosystem.config.cjs --update-env
pm2 restart gitslice-core-production --update-env
pm2 restart gitslice-core-staging --update-env
```
- If `restart_all.sh` reports success but listeners disappear (`50051`/`50052` refused, public API `502`/`522`, or PM2 shows `stopped`), immediately run the PM2-managed restart sequence above, then repeat the full listener + health verification.
- Treat `globalCommitHash=global-init` with empty history from `/v1/global/state` as missing genesis population/import for the active server instance.
- For staging auth/login issues, verify `https://api.agenttools.dev/v1/global/state` first. The Worker auth routes proxy through the staging API host, so a broken staging API DNS/proxy path will break sign-in even if the Worker itself is healthy.

## Preserve Production Assumptions

- Keep `ops/restart_all.sh` safe for unattended hourly runs.
- Preserve the `git pull --ff-only` update flow.
- Keep lock and health-check behavior so cron executions do not overlap.
- Keep prod defaults on core startup: `SKIP_GIT_POPULATION=1`.
- Ensure object store configuration exists when `STORAGE_TYPE=postgres`.
- Remember the current target topology:
  - production core: `127.0.0.1:50051`
  - staging core: `127.0.0.1:50052`
  - production web: Cloudflare Worker on `gitslice.io`
  - staging web: Cloudflare Worker on `agenttools.dev`
  - PM2 manages only core services, not web SSR, on the VM
