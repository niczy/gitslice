# Production Deployment Todo

This is the remaining operational work for the final hosted topology:

- web: `gitslice.io` on a Cloudflare Worker
- production API: `api.gitslice.io` on the current VM
- staging web: `agenttools.dev` on a Cloudflare Worker
- staging API: `api.agenttools.dev` on the current VM
- metadata DB: Neon
- object storage: R2

## 1. Provision Cloudflare Worker Environments

- Create or confirm the Worker environments in [web/wrangler.jsonc](/home/nic/workspace/gitslice/web/wrangler.jsonc):
  - `staging` -> `agenttools.dev`
  - `production` -> `gitslice.io`
- Set Worker secrets for both environments:
  - `AUTH_SECRET`
  - `AUTH_GITHUB_ID`
  - `AUTH_GITHUB_SECRET`
  - `AUTH_GOOGLE_ID`
  - `AUTH_GOOGLE_SECRET`
- Use a fresh production `AUTH_SECRET`; invalidating old browser sessions is acceptable.
- Deploy both Worker environments:
  - `cd web && npm run deploy:worker:staging`
  - `cd web && npm run deploy:worker:production`

## 2. Provision Neon

- Create separate Neon namespaces for:
  - production
  - staging
- Generate separate DSNs and keep TLS enabled.
- Decide the exact resource split:
  - separate projects, or
  - separate databases/branches with separate credentials
- Confirm production DSN points to production data only.
- Confirm staging DSN points to staging data only.

## 3. Provision R2

- Create separate R2 namespaces for:
  - production
  - staging
- Prefer separate buckets; separate prefixes are acceptable if needed.
- Record for each environment:
  - `R2_ENDPOINT`
  - `R2_BUCKET`
  - `R2_PREFIX`
  - `R2_ACCESS_KEY_ID`
  - `R2_SECRET_ACCESS_KEY`
- Confirm `OBJECT_STORE_TYPE=r2` for both production and staging.

## 4. Prepare VM Environment Files

- Create production env file:
  - `cp ops/.env.example ops/.env.production`
- Create staging env file:
  - `cp ops/.env.example ops/.env.staging`
- Set production values in `ops/.env.production`:
  - `DEPLOY_ENV=production`
  - `CORE_BIND_ADDR=127.0.0.1`
  - `CORE_SERVICE_PORT=50051`
  - `PUBLIC_WEB_BASE_URL=https://gitslice.io`
  - `PUBLIC_API_BASE_URL=https://api.gitslice.io`
  - production `POSTGRES_DSN`
  - production R2 settings
  - `WEB_DEPLOY_TARGET=cloudflare_worker`
  - `WEB_COMPAT_RUNTIME=worker`
  - `RUN_WEB_SSR=0`
- Set staging values in `ops/.env.staging`:
  - `DEPLOY_ENV=staging`
  - `CORE_BIND_ADDR=127.0.0.1`
  - `CORE_SERVICE_PORT=50052`
  - `PUBLIC_WEB_BASE_URL=https://agenttools.dev`
  - `PUBLIC_API_BASE_URL=https://api.agenttools.dev`
  - staging `POSTGRES_DSN`
  - staging R2 settings
  - `WEB_DEPLOY_TARGET=cloudflare_worker`
  - `WEB_COMPAT_RUNTIME=worker`
  - `RUN_WEB_SSR=0`

## 5. Copy Object Store Data Into R2

- For production, copy referenced objects from the current store into the production R2 namespace:
  ```bash
  OBJECT_STORE_TYPE=filesystem \
  OBJECT_STORE_DIR=/srv/gitslice/objectstore \
  TARGET_OBJECT_STORE_TYPE=r2 \
  TARGET_ENV=production \
  TARGET_R2_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com \
  TARGET_R2_BUCKET=gitslice-production \
  TARGET_R2_PREFIX=production \
  TARGET_R2_ACCESS_KEY_ID=... \
  TARGET_R2_SECRET_ACCESS_KEY=... \
  ./storage_migrate copy-object-store --dsn "$POSTGRES_DSN" --namespace core
  ```
- Verify production copy:
  ```bash
  OBJECT_STORE_TYPE=filesystem \
  OBJECT_STORE_DIR=/srv/gitslice/objectstore \
  TARGET_OBJECT_STORE_TYPE=r2 \
  TARGET_ENV=production \
  TARGET_R2_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com \
  TARGET_R2_BUCKET=gitslice-production \
  TARGET_R2_PREFIX=production \
  TARGET_R2_ACCESS_KEY_ID=... \
  TARGET_R2_SECRET_ACCESS_KEY=... \
  ./storage_migrate verify-object-store --dsn "$POSTGRES_DSN" --namespace core
  ```
- Repeat the same process for staging if staging already needs real data.
- Search artifacts do not need to be copied; they can be rebuilt.

## 6. Apply API-Origin Nginx Config

- Install the updated API-only Nginx config:
  ```bash
  sudo cp ops/nginx.conf /etc/nginx/nginx.conf
  sudo nginx -t
  sudo systemctl reload nginx
  ```
- Confirm certificates exist for:
  - `api.gitslice.io`
  - `api.agenttools.dev`
- Confirm Cloudflare is set to proxy both API hosts in gRPC mode with HTTPS origin mode such as `Full (strict)`.

## 7. Start or Refresh VM Services

- Start PM2 using the new shared-VM ecosystem:
  ```bash
  pm2 start ops/ecosystem.config.cjs
  pm2 save
  ```
- Or run the normal restart flow:
  ```bash
  bash ops/restart_all.sh
  ```
- Confirm PM2 shows:
  - `gitslice-core-production`
  - `gitslice-core-staging`
- Confirm no public web VM process is required in the steady-state Worker deployment.

## 8. Verify Local VM State

- Run:
  ```bash
  ./ops/verify_deploy.sh --local-only
  ```
- Confirm:
  - production core health on `127.0.0.1:50051`
  - staging core health on `127.0.0.1:50052`
  - production R2 config is present
  - staging R2 config is present

## 9. Verify Public Staging

- Run:
  ```bash
  curl -sf https://agenttools.dev/ >/dev/null
  curl -sf https://api.agenttools.dev/v1/global/state >/dev/null
  ```
- Confirm browser auth still works on staging.
- Confirm CLI access still works against `api.agenttools.dev:443`.
- Confirm staging writes and reads round-trip through staging R2.

## 10. Cut Over Production

- Deploy the production Worker to `gitslice.io`.
- Ensure the production VM core is running with:
  - production Neon DSN
  - production R2 config
  - `CORE_SERVICE_PORT=50051`
- Confirm `api.gitslice.io` is serving the VM API origin.

## 11. Verify Public Production

- Run:
  ```bash
  curl -sf https://gitslice.io/ >/dev/null
  curl -sf https://api.gitslice.io/v1/global/state >/dev/null
  ```
- After exporting `GS_API_KEY=...` or logging in locally, verify CLI connectivity:
  ```bash
  gs context --json \
    --slice-addr api.gitslice.io:443 \
    --account-addr api.gitslice.io:443 \
    --admin-addr api.gitslice.io:443 \
    --file-addr api.gitslice.io:443 \
    --fs-addr api.gitslice.io:443
  ```
- Confirm production auth flow works in the browser.
- Confirm a real production read/write path round-trips through production R2.
- Confirm staging still works after production cutover:
  - `https://agenttools.dev/`
  - `https://api.agenttools.dev/v1/global/state`

## 12. Final Steady-State Checks

- Run the full deploy verifier:
  ```bash
  ./ops/verify_deploy.sh
  ```
- Confirm hourly restart remains safe:
  - lock file behavior works
  - `git pull --ff-only` flow still works
  - no PM2 flapping
- Confirm the VM is treated as:
  - core/API origin only
  - not the public web origin
  - not the durable blob store
