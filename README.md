# Gitslice

**High-level summary:** Gitslice is a prototype slice-based version control system with gRPC services, a CLI, and a lightweight web landing page. Storage backends include in-memory and PostgreSQL (with GCS object storage for payloads).

## Project Structure

```
.
├── gs_cli/                # CLI client implementation
│   └── main.go
├── internal/              # Storage and shared implementations
│   ├── gateway/
│   └── storage/
├── ops/                   # Ops assets (NGINX config, etc.)
├── proto/                  # Protocol Buffer definitions (generated stubs are local, not committed)
│   ├── slice/             # Slice service proto files
│   │   ├── slice_service.proto
│   │   ├── slice_service.pb.go
│   │   └── slice_service_grpc.pb.go
│   ├── file/              # File service proto files
│   │   ├── file_service.proto
│   │   ├── file_service.pb.go
│   │   ├── file_service_grpc.pb.go
│   │   └── file_service.pb.gw.go
│   ├── filesystem/        # Filesystem service proto files
│   │   ├── filesystem_service.proto
│   │   ├── filesystem_service.pb.go
│   │   ├── filesystem_service_grpc.pb.go
│   │   └── filesystem_service.pb.gw.go
│   ├── admin/             # Admin service proto files
│   │   ├── admin_service.proto
│   │   ├── admin_service.pb.go
│   │   └── admin_service_grpc.pb.go
│   ├── account/           # Account system proto files
│   │   ├── account_service.proto
│   │   ├── account_service.pb.go
│   │   └── account_service_grpc.pb.go
│   └── agent/             # Agent session proto files
│       ├── agent_service.proto
│       ├── agent_service.pb.go
│       ├── agent_service_grpc.pb.go
│       └── agent_service.pb.gw.go
├── services/              # RPC service implementations
│   ├── account/
│   ├── admin/
│   ├── agent/
│   ├── file/
│   ├── filesystem/
│   └── slice/
├── servers/               # Binary servers
│   └── core/              # Core server (gRPC + gateway)
├── sdk/                   # Client SDKs
│   ├── python/            # Python filesystem SDK
│   ├── typescript/        # TypeScript filesystem SDK
│   └── mcp/               # MCP stdio server for filesystem tools
├── spec/                 # Design specifications
│   ├── PRODUCT_VISION.md
│   ├── DATA_MODEL.md
│   ├── ALGORITHMS.md
│   ├── CLI_DESIGN.md
│   ├── API_DESIGN.md
│   └── ARCHITECTURE.md
├── web/                  # Vite + React landing page
│   └── README.md
├── workflow_test/        # End-to-end integration tests
│   └── integration_test.go
└── .github/workflows/    # CI/CD workflows
    └── build.yml
```

## Getting Started

### Prerequisites

- Go 1.24 or higher
- Protocol Buffers compiler (protoc)
- protoc-gen-go
- protoc-gen-go-grpc
- protoc-gen-grpc-gateway

### Go Workspace

This repository uses a Go workspace (`go.work`) to wire together the service and server modules (each service/server has its own `go.mod`). Run Go commands from the repo root to pick up the workspace configuration.

### Install Dependencies

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0
go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
```

### Generate Proto Code

```bash
cd proto/common
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative visibility.proto

cd proto/slice
protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative slice_service.proto

cd ../file
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative file_service.proto

cd ../filesystem
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative filesystem_service.proto

cd ../admin
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative admin_service.proto

cd ../account
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative account_service.proto

cd ../agent
protoc -I . -I .. --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative agent_service.proto
```

### Build

```bash
# Build core server (gRPC + gateway)
go build -o core_server ./servers/core/

# Build CLI
go build -o gs_cli/gs_cli ./gs_cli/
```

### Run

```bash
# Start local dev servers with in-memory storage
make start-servers

# Start local dev servers with Postgres metadata + filesystem object storage
POSTGRES_DSN='postgres://user:pass@127.0.0.1:5432/gitslice?sslmode=disable' \
  make start-servers-postgres-file

# For local Postgres DSNs, the dev script creates the database if missing.
# Core then runs the embedded schema migrations during storage startup.

# Start local dev servers with Postgres metadata + R2 object storage
POSTGRES_DSN='postgres://...' \
R2_ENDPOINT='https://<account-id>.r2.cloudflarestorage.com' \
R2_BUCKET='gitslice-dev' \
R2_PREFIX='dev' \
R2_ACCESS_KEY_ID='...' \
R2_SECRET_ACCESS_KEY='...' \
  make start-servers-postgres-r2

# Restart or stop local dev servers
make restart-servers
make stop-servers

# Run core server (gRPC + gateway on :50051)
CORE_SERVICE_PORT=50051 ./core_server

# Optional: override the browser URL returned by browser-approved device login
PUBLIC_WEB_BASE_URL=http://localhost:4173 CORE_SERVICE_PORT=50051 ./core_server

# Optional: override the public API origin used by split-host web deployments
PUBLIC_API_BASE_URL=http://localhost:50051 CORE_SERVICE_PORT=50051 ./core_server

# Run core server with PostgreSQL + GCS storage
STORAGE_TYPE=postgres \
POSTGRES_DSN='postgres://user:pass@localhost:5432/gitslice?sslmode=disable' \
GCS_BUCKET=gitslice-objects \
GCS_CREDENTIALS_FILE=/path/to/service-account.json \
CORE_SERVICE_PORT=50051 ./core_server

# Run core server with PostgreSQL + filesystem object store (no GCS required)
STORAGE_TYPE=postgres \
POSTGRES_DSN='postgres://user:pass@localhost:5432/gitslice?sslmode=disable' \
OBJECT_STORE_TYPE=filesystem \
OBJECT_STORE_DIR="$PWD/.objectstore" \
CORE_SERVICE_PORT=50051 ./core_server

# Run CLI (override addresses if needed)
./gs_cli --help
```

### Deployment Topology

The target hosted topology is:

1. `gitslice.io`
   - Cloudflare Worker for the public web app
2. `api.gitslice.io`
   - VM-hosted core service behind Nginx
   - public gRPC and `/v1/*` gateway traffic
3. `agenttools.dev`
   - staging Worker web environment
4. `api.agenttools.dev`
   - staging VM-hosted core API

On the shared VM, keep the internal core listeners distinct:

1. production core: `127.0.0.1:50051`
2. staging core: `127.0.0.1:50052`

The shared deployment env vocabulary is:

```bash
DEPLOY_ENV=production|staging
CORE_BIND_ADDR=127.0.0.1
PUBLIC_WEB_BASE_URL=https://gitslice.io
PUBLIC_API_BASE_URL=https://api.gitslice.io
VITE_FILE_API_BASE_URL=
WEB_DEPLOY_TARGET=cloudflare_worker
WEB_COMPAT_RUNTIME=worker
POSTGRES_DSN=postgres://...
POSTGRES_MAX_CONNS=20
POSTGRES_MIN_CONNS=2
POSTGRES_MAX_CONN_LIFETIME=30m
R2_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
R2_REGION=auto
R2_BUCKET=...
R2_PREFIX=production
R2_ACCESS_KEY_ID=...
R2_SECRET_ACCESS_KEY=...
```

On the shared VM, keep separate env files:

```bash
cp ops/.env.example ops/.env.production
cp ops/.env.example ops/.env.staging
```

Then adjust staging-specific values in `ops/.env.staging`:
- `DEPLOY_ENV=staging`
- `CORE_SERVICE_PORT=50052`
- `PUBLIC_WEB_BASE_URL=https://agenttools.dev`
- `PUBLIC_API_BASE_URL=https://api.agenttools.dev`
- staging Neon DSN and staging R2 namespace

For the Neon-backed core deployment:

1. staging and production must use separate Neon databases, branches, or credentials
2. `POSTGRES_DSN` is the runtime connection string and is also used for startup migrations today
3. remote PostgreSQL targets are expected to use TLS; `sslmode=disable` is only valid for local development targets
4. staging and production must use separate R2 namespaces, either by bucket or by `R2_PREFIX`
5. `OBJECT_STORE_TYPE=r2` is the production target; VM-local filesystem object storage is only a local-development fallback
6. for the Worker web target, browser `/v1/*` calls stay same-origin and the Worker proxies them to `PUBLIC_API_BASE_URL`

### Worker Web Deploy

The React Router web app now has a first-class Cloudflare Worker target under [wrangler.jsonc](/home/nic/workspace/gitslice/web/wrangler.jsonc). The Worker serves the built assets from `build/client`, runs SSR through [worker.js](/home/nic/workspace/gitslice/web/worker.js), and proxies browser-side `/v1/*` traffic to `PUBLIC_API_BASE_URL` while keeping auth cookies on the web origin.

Useful commands:

```bash
cd web
npm run dev:worker
npm run preview:worker
npm run deploy:worker:staging
npm run deploy:worker:production
```

You can also deploy the Worker through the ops wrapper, which resolves the
environment file and exports `CLOUDFLARE_API_TOKEN` before running Wrangler:

```bash
./ops/deploy.sh --env staging --app web
./ops/deploy.sh --env production --app web
./ops/deploy.sh --env staging --app api
./ops/deploy.sh --env production --app api
```

By default the wrapper reads:
- staging: `ops/.env.staging`
- production: `ops/.env.production`

`--app api` rebuilds `core_server`, restarts the matching PM2 app
(`gitslice-core-staging` or `gitslice-core-production`), and waits for the
local health check on the configured `CORE_SERVICE_PORT`. The selected env file
must contain the core runtime settings for that target. In particular, when
`OBJECT_STORE_TYPE=r2`, the deploy wrapper expects `R2_BUCKET`,
`R2_ENDPOINT`, `R2_ACCESS_KEY_ID`, and `R2_SECRET_ACCESS_KEY` to be set before
it will restart the API process.

For local Worker auth flows, copy [`.dev.vars.example`](/home/nic/workspace/gitslice/web/.dev.vars.example) to `.dev.vars` and set:
- `AUTH_PROVIDER=clerk` for the default Clerk path, or `AUTH_PROVIDER=workos` when validating WorkOS
- `ALLOW_DEV_LOGIN=1` only if you want username/dev login available as an explicit local fallback
- `AUTH_SECRET`
- `CLERK_SECRET_KEY` and `CLERK_PUBLISHABLE_KEY` when testing Clerk
- `WORKOS_*` when testing WorkOS

The legacy `Authorization: User <username>` shortcut is disabled automatically when `DEPLOY_ENV=production`; set `ALLOW_LEGACY_USER_AUTH=1` only for explicit debugging or controlled dev/staging compatibility.

When using WorkOS webhooks, point WorkOS at `/v1/auth/workos/webhook` on the API host and set `WORKOS_WEBHOOK_SECRET` in the API env file. Gitslice currently handles `user.updated` by syncing linked profile fields and `user.deleted` by revoking local sessions and unlinking the WorkOS ID.

For staging and production deploys, `ops/deploy.sh --app web` uses the env file to inject non-secret Worker auth vars (`AUTH_PROVIDER`, `ALLOW_DEV_LOGIN`, `WORKOS_CLIENT_ID`, `WORKOS_REDIRECT_URI`, `WORKOS_JWKS_URL`, `WORKOS_AUTHKIT_DOMAIN`) into a temporary Wrangler config. Set the actual secrets with Wrangler at deploy time:

```bash
cd web
wrangler secret put AUTH_SECRET --env staging
wrangler secret put WORKOS_API_KEY --env staging
wrangler secret put WORKOS_COOKIE_PASSWORD --env staging
wrangler secret put AUTH_SECRET --env production
wrangler secret put WORKOS_API_KEY --env production
wrangler secret put WORKOS_COOKIE_PASSWORD --env production
```

The production cutover does not need to preserve old browser sessions. Rotating `AUTH_SECRET`
as part of the first `gitslice.io` Worker deploy is acceptable and will invalidate any stale
cookies minted by the old origin.

### Remote filesystem workflow

`gs fs` operates on your home slice using absolute paths like `/$USER/project/README.md`.

```bash
gs login <username>
printf 'hello from cloud fs\n' | gs fs write /<username>/project/README.md
gs fs cat /<username>/project/README.md
gs fs snapshot -m "checkpoint"
```

Each `gs fs` mutation creates a home-slice commit and publishes it through the same slice changeset merge flow used by `gs changeset merge`. If you want the local workflow, check out the same home slice and inspect the merged publish history there:

```bash
mkdir my-home-slice && cd my-home-slice
gs slice checkout home.<username>
gs changeset list --status merged
```

`gs slice create` keeps a free-form display name and also returns a stable slice ref. Slice slugs are only unique within the owning user namespace, so external refs use `owner/slug`. `gs slice checkout` accepts either the slice ID or that ref.
Plain `gs slice checkout` is the primary local path. It is fast, skips local git metadata entirely, and supports local status, diff, restore, sync, and publish directly from the recorded `.gs/index`.

For the normal local workflow, list your slices, check one out, inspect local changes, and publish through the tracked changeset:

```bash
gs slice list
gs slice checkout <slice-id-or-ref>
gs slice status
gs slice status --remote
gs slice diff --summary
gs slice diff
gs slice restore --dry-run
gs slice restore
gs slice sync
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx
```

If the working tree is already clean but a tracked changeset exists, `gs slice publish` reuses that tracked changeset for review or merge instead of failing.

Useful day-to-day helpers:

```bash
gs slice tree
gs slice diff --name-only
gs changeset show
gs doctor
gs repo import https://github.com/org/repo.git /$USER/vendor/repo --push-enabled
gs repo pull /$USER/vendor/repo
gs repo push /$USER/vendor/repo --message "sync upstream fixes"
gs fs sync --direction push ./site /$USER/site
gs fs sync --direction pull /$USER/site ./site-copy
```

### Local checkout registry and cache

Git Slice tracks local slice checkouts globally under `~/.gitslice`, along with the shared local object cache used by fast repeated checkouts.

```bash
# Show globally tracked local checkouts and where they live
gs slice checkouts

# Show cache size, tracked checkout counts, and stale checkout records
gs cache stats --checkouts

# Remove dead checkout records after deleting worktrees manually
gs cache prune

# Reclaim disk by deleting cached objects
gs cache clear --objects
```

Enable E2B-backed agent session runtime lifecycle in `core_server`:

```bash
E2B_API_KEY=your-e2b-api-key \
E2B_DOMAIN=e2b.app \
E2B_RUNTIME_WS_PORT=9000 \
E2B_RUNTIME_WS_PATH=/ws \
CORE_SERVICE_PORT=50051 ./core_server
```

If `E2B_API_KEY` and `E2B_ACCESS_TOKEN` are both unset, agent sessions use the simulated runtime provider.

Enable Cloudflare Containers runtime lifecycle in `core_server`:

```bash
CFC_CONTROL_BASE_URL=https://<worker-subdomain>.workers.dev \
CFC_SERVICE_TOKEN_ID=<service-token-id> \
CFC_SERVICE_TOKEN_SECRET=<service-token-secret> \
AGENT_RUNTIME_PROVIDER_DEFAULT=cloudflare_containers \
CORE_SERVICE_PORT=50051 ./core_server
```

For rollout safety, keep `AGENT_RUNTIME_PROVIDER_DEFAULT=e2b` initially and opt slices into Cloudflare via environment registry (`provider=cloudflare_containers`).

Cloudflare control-plane worker source is in `servers/cloudflare_control_plane`:

```bash
cd servers/cloudflare_control_plane
npm install
npm test
npx wrangler dev
```

Register a Cloudflare-backed environment profile:

```bash
curl -X POST "$GATEWAY_BASE_URL/v1/environments" \
  -H "Authorization: Bearer <access-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name":"cfc-canary",
    "displayName":"Cloudflare Canary",
    "provider":"cloudflare_containers",
    "providerId":"cfc-profile",
    "providerConfig":{
      "worker_base_url":"https://<worker-subdomain>.workers.dev",
      "container_class":"sandbox",
      "instance_type":"basic"
    },
    "region":"us-east-1"
  }'
```

For Codex/Claude sandbox sessions, configure model credentials and optional egress policy:

```bash
OPENAI_API_KEY=your-openai-key
ANTHROPIC_API_KEY=your-anthropic-key

# Optional: enforce deny-by-default egress from sandbox runtime shim.
AGENT_EGRESS_DENY_BY_DEFAULT=true
AGENT_EGRESS_ALLOWLIST=api.openai.com,api.anthropic.com,github.com
```

Agent runtime observability endpoints:

- `GET /debug/vars` (expvar metrics including agent session lifecycle/runtime/ws counters plus search-index build/load and indexed filesystem search metrics)
- `GET /health/agent-runtime` (runtime provider readiness and policy validation)

Suggested baseline alerts:

- `agent_session_runtime_fail_total` increasing rapidly by `failureCode`
- high `agent_ws_backpressure_close_total` rate
- unhealthy `GET /health/agent-runtime` for more than 5 minutes

Cloudflare rollout checklist:

1. Deploy `servers/cloudflare_control_plane` and validate `GET /internal/runtime/health` through your service token path.
2. Configure `CFC_CONTROL_BASE_URL`, `CFC_SERVICE_TOKEN_ID`, and `CFC_SERVICE_TOKEN_SECRET` on core.
3. Create one canary environment (`provider=cloudflare_containers`) and assign only non-critical slices first.
4. Watch `/health/agent-runtime` and event flow (`/v1/agent-sessions/{id}/events`) during canary sessions.
5. Expand environment usage gradually; keep E2B environments available for rollback.
6. Roll back by switching slice environment back to E2B profile or removing Cloudflare config from core.

Cloudflare runtime troubleshooting:

- `CFC_CONTROL_URL_MISSING`: `CFC_CONTROL_BASE_URL` is empty or invalid.
- `CFC_AUTH_MISSING`: service token id/secret is missing in core config.

Search index maintenance:

- `storage_migrate backfill-search-index --dsn <dsn> --namespace core --commits 20`
- `storage_migrate repair-search-index --dsn <dsn> --namespace core --slice <slice-id> --commit <commit-hash>`
- `storage_migrate repair-search-index --dsn <dsn> --namespace core --workspace <slice-id>`
- `CFC_START_UNAUTHORIZED` or `CFC_STOP_UNAUTHORIZED`: service token rejected by Worker/Access.
- `CFC_RUNTIME_UNAVAILABLE`: Worker/control plane is unreachable or returned 5xx.
- `CFC_STREAM_DECODE_FAILED`: stream endpoint returned malformed SSE payload.
- `RUNTIME_BRIDGE_SYNC_FAILED`: core could not sync stream events; inspect control-plane `/stream` response and core logs.

## Accounts / Organizations

This repo uses WorkOS-backed human web sign-in, browser-approved CLI device login for humans, and enrolled `ed25519` keys for non-interactive agent auth. Requests include the signed-in user in metadata.

- The root slice (`root_slice`) is publicly viewable.
- Non-root slices are only visible/accessible to their owners.
- Organizations are user-created groups shown on the profile page (no invites yet).

Web auth environment variables (see `web/.env.example`):

```bash
VITE_WEB_AGENT_REAL_RUNTIME=1
AUTH_PROVIDER=workos
AUTH_SECRET=replace-with-long-random-string
ALLOW_LEGACY_USER_AUTH=
WORKOS_CLIENT_ID=client_...
WORKOS_API_KEY=sk_...
WORKOS_COOKIE_PASSWORD=replace-with-long-random-string
WORKOS_WEBHOOK_SECRET=whsec_...
```

For local setup, copy the template and fill in values:

```bash
cp web/.env.example web/.env
```

For local Node SSR fallback outside the normal VM deploy path, put the same
`AUTH_*` values in the environment file for that target (`ops/.env.production`
or `ops/.env.staging`). For the normal Worker deploy path, set auth secrets with Wrangler
instead of storing them in the VM env files.


CLI usage:

```bash
# Start browser-approved device login and store refreshable credentials in ~/.gitslice/credentials.json
gs login

# Agent-first signup/login with an ed25519 keypair
gs auth keygen --out ~/.config/gitslice/agent_ed25519
gs auth signup --username your_name --email you@example.com --name "Your Name" --key ~/.config/gitslice/agent_ed25519
gs auth login --key ~/.config/gitslice/agent_ed25519
gs auth claim-token --json

# Check the current stored login and auth metadata
gs auth status --json
gs doctor --json
gs context --json

# Manage enrolled agent keys from the CLI or Settings page
gs auth keys list --json
gs auth keys revoke <key-id>

# Remote filesystem commands
gs fs write /your_name/README.md -f ./README.md
gs fs cat /your_name/README.md
gs fs snapshot -m "save point"
gs fs shell
gs fs upload ./project /your_name/project
gs fs download /your_name/project ./project-copy
```

## Development

### Adding New Proto Definitions

1. Add or modify `.proto` files in `proto/slice/`, `proto/admin/`, `proto/account/`, `proto/file/`, `proto/filesystem/`, or `proto/agent/`
2. Regenerate the Go code using protoc (`make proto` works)
3. Do not commit generated `*.pb.go` / `*.pb.gw.go` files
4. Update the service implementations as needed
5. Run tests and ensure builds pass

### Running Tests

```bash
# Run all tests (installs dependencies first)
make test

# Run Python SDK tests
PYTHONPATH=sdk/python python3 -m unittest discover -s sdk/python/tests

# Run TypeScript SDK tests
npm ci --prefix sdk/typescript
npm test --prefix sdk/typescript

# Run MCP server tests
npm ci --prefix sdk/mcp
npm test --prefix sdk/mcp

# Run integration tests
RUN_INTEGRATION_TESTS=1 make test
```

## CI/CD

GitHub Actions workflow is configured to:
- Install Go and dependencies
- Generate proto code
- Build all services
- Test server startup
- Test CLI help command

See `.github/workflows/build.yml` for details.

## Operations

### Hourly Auto-Update and Restart

`ops/restart_all.sh` is the canonical deploy script. It:
- Acquires a lock to avoid overlapping cron runs
- Pulls latest changes (`git fetch --prune` + `git pull --ff-only`) when upstream is configured
- Rebuilds/restarts the VM-hosted core services via `ops/start_web_server.sh`
- Verifies local production/staging core health via `ops/verify_deploy.sh --local-only`
- Ensures an hourly user crontab entry exists
- Starts `core_server` with `SKIP_GIT_POPULATION=1` by default (disable genesis auto-population from the local git checkout)

Install or refresh the hourly cron entry:

```bash
bash ops/restart_all.sh
```

Cron target installed by the script:

```bash
0 * * * * PATH=/home/<user>/.nvm/versions/node/<node-version>/bin:/home/<user>/.local/go/bin:/home/<user>/.local/protoc/bin:/home/<user>/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin bash /home/<user>/workspace/gitslice/ops/restart_all.sh >> /home/<user>/workspace/gitslice/logs/cron.log 2>&1
```

### PM2 Process Supervision

For long-running service supervision, use the included PM2 ecosystem file:

```bash
npm install -g pm2
pm2 start ops/ecosystem.config.cjs
pm2 save
```

The PM2 ecosystem now reads:
- `ops/.env.production` for `gitslice-core-production`
- `ops/.env.staging` for `gitslice-core-staging`

The PM2 ecosystem is now VM-core-only. It supervises `gitslice-core-production`
and `gitslice-core-staging` only. The public web app runs on Cloudflare Worker,
so the VM no longer starts or supervises `gitslice-web-*` processes through PM2.

For the target hosted split, keep `CORE_BIND_ADDR`, `PUBLIC_WEB_BASE_URL`,
`PUBLIC_API_BASE_URL`, `VITE_FILE_API_BASE_URL`, `DEPLOY_ENV`, `WEB_DEPLOY_TARGET`,
and the Postgres/R2 settings in each env file. Set `OBJECT_STORE_TYPE=r2` with
`R2_ENDPOINT`, `R2_BUCKET`, `R2_PREFIX`, `R2_ACCESS_KEY_ID`, and
`R2_SECRET_ACCESS_KEY` for staging and production.

To restore PM2 apps on reboot (user crontab approach):

```bash
@reboot PATH=/home/<user>/.nvm/versions/node/<node-version>/bin:/home/<user>/.local/go/bin:/home/<user>/.local/protoc/bin:/home/<user>/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin /home/<user>/.nvm/versions/node/<node-version>/bin/pm2 resurrect >> /home/<user>/workspace/gitslice/logs/pm2_reboot.log 2>&1
```

### NGINX (Cloudflare in Front, Origin HTTPS)

`ops/nginx.conf` is now API-origin-only: the Worker owns `gitslice.io` and
`agenttools.dev`, while Nginx on the VM terminates TLS for:

1. `api.gitslice.io`
2. `api.agenttools.dev`

and routes them to separate local core listeners:

1. production -> `127.0.0.1:50051`
2. staging -> `127.0.0.1:50052`

Both API origins route the public gRPC service paths to the core server:
- `/slice.v1.SliceService/`
- `/admin.v1.AdminService/`
- `/account.v1.AccountService/`
- `/file.v1.FileService/`
- `/filesystem.v1.FilesystemService/`
- `/agent.v1.AgentService/`

Both API origins also proxy HTTP gateway traffic under `/v1/` and Git smart
HTTP traffic under `/git/`. The `/git/` locations disable request buffering and
allow large request bodies so `git push` can stream packfiles through Cloudflare
and Nginx to the core service.

### Git Smart HTTP

Git access is slice-oriented: one Git repository maps to one slice.

Use:

```text
https://api.<domain>/git/<owner>/<slice>.git
```

`<slice>` is the local slice slug inside the owner's namespace. Use `owner/slug` for Git and other external refs.

Examples:

```bash
# Clone a slice from staging with a bearer token.
git -c http.extraHeader="Authorization: Bearer $GS_API_KEY" \
  clone https://api.agenttools.dev/git/alice/my-slice.git

# Push back to the same slice.
cd my-slice
git add -A
git commit -m "update slice"
git -c http.extraHeader="Authorization: Bearer $GS_API_KEY" \
  push origin HEAD:main
```

Notes:
- The Git URL uses `slice`, not `workspace`.
- Private slices require authentication for clone/fetch.
- Push requires write access to the target slice.
- Staging still accepts `Authorization: User <username>` when legacy user auth is enabled, but bearer tokens are the intended path.

To copy referenced objects from the current store into a target R2 namespace before cutover:

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

To verify the copied objects against the authoritative source store:

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

The copy and verify commands only operate on metadata-referenced objects:
- current slice manifests
- versioned manifests
- referenced block payloads

Search artifacts can be regenerated after cutover and do not need to be copied.

For the Worker/VM split, browser-origin API traffic should use
same-origin `/v1/*` routes on the web host, and the Worker should proxy them to
`PUBLIC_API_BASE_URL`. CLI connectivity continues to target `api.gitslice.io:443`.

For staging CLI connectivity, target `api.agenttools.dev:443` with TLS enabled.
For the target production layout, CLI connectivity moves to `api.gitslice.io:443`.

Suggested production cutover smoke checks:

```bash
curl -sf https://gitslice.io/ >/dev/null
curl -sf https://api.gitslice.io/v1/global/state >/dev/null
curl -sf https://api.agenttools.dev/v1/global/state >/dev/null
# After exporting GS_API_KEY=... (or logging in locally):
gs context --json --slice-addr api.gitslice.io:443 --account-addr api.gitslice.io:443 --admin-addr api.gitslice.io:443 --file-addr api.gitslice.io:443 --fs-addr api.gitslice.io:443
```

For object storage, verify a real production write/read path after cutover by
creating a small file through the CLI or browser and confirming the bytes round-trip
through the production R2 namespace.

For repeatable shared-VM verification, use the deploy helper:

```bash
./ops/verify_deploy.sh --local-only
./ops/verify_deploy.sh
```

It checks:
- local production core health on `127.0.0.1:50051`
- local staging core health on `127.0.0.1:50052` when `ops/.env.staging` exists
- public `gitslice.io` and `api.gitslice.io`
- public `agenttools.dev` and `api.agenttools.dev` when staging is configured
- presence of required R2 config for each configured environment

The origin Nginx config keeps long-lived gRPC calls open for up to `30m`, which
is required for large repo imports and similarly heavy CLI operations to survive
the public edge without a `504 Gateway Timeout`.

Cloudflare must proxy both `api.agenttools.dev` and `api.gitslice.io` in gRPC mode
and use an HTTPS origin mode such as `Full (strict)`. Plain HTTP origin mode and
h2c on port `80` are not compatible with Cloudflare gRPC proxying.

Apply config:

```bash
sudo cp ops/nginx.conf /etc/nginx/nginx.conf
sudo nginx -t
sudo systemctl restart nginx
```

Cloudflare SSL/TLS mode should match your origin setup (HTTP origin commonly uses `Flexible`).

## Documentation

See the `spec/` directory for detailed design specifications:
- [Product Vision](spec/PRODUCT_VISION.md)
- [Data Model](spec/DATA_MODEL.md)
- [Algorithms](spec/ALGORITHMS.md)
- [CLI Design](spec/CLI_DESIGN.md)
- [API Design](spec/API_DESIGN.md)
- [Architecture](spec/ARCHITECTURE.md)
- [Scalability Review](spec/SCALABILITY_REVIEW.md)
- [Storage DB Design](spec/STORAGE_DB_DESIGN.md)

For the web landing page, see [web/README.md](web/README.md).

## License

[Add your license here]
