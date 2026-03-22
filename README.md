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
# Run core server (gRPC on :50051, gateway on :8080)
CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 ./core_server

# Optional: override the browser URL returned by OAuth device login
PUBLIC_WEB_BASE_URL=http://localhost:4173 CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 ./core_server

# Run core server with PostgreSQL + GCS storage
STORAGE_TYPE=postgres \
POSTGRES_DSN='postgres://user:pass@localhost:5432/gitslice?sslmode=disable' \
GCS_BUCKET=gitslice-objects \
GCS_CREDENTIALS_FILE=/path/to/service-account.json \
CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 ./core_server

# Run core server with PostgreSQL + filesystem object store (no GCS required)
STORAGE_TYPE=postgres \
POSTGRES_DSN='postgres://user:pass@localhost:5432/gitslice?sslmode=disable' \
OBJECT_STORE_TYPE=filesystem \
OBJECT_STORE_DIR="$PWD/.objectstore" \
CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 ./core_server

# Run CLI (override addresses if needed)
./gs_cli --help
```

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

`gs slice create` keeps a free-form display name and also returns a stable slug. `gs slice checkout` accepts either the slice ID or that slug.
Plain `gs slice checkout` is the fast default and skips local git metadata. Add `--git` when you want local git status/diff and a git-native local workflow.

For the normal local workflow, list your slices, check one out, sync it in place, and publish through the tracked changeset:

```bash
gs slice list
gs slice checkout <slice-id-or-slug> --git
gs slice status
gs slice status --remote
gs slice sync
gs slice publish --message "refresh settings page" --files src/routes/settings.tsx
```

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
CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 ./core_server
```

If `E2B_API_KEY` and `E2B_ACCESS_TOKEN` are both unset, agent sessions use the simulated runtime provider.

Enable Cloudflare Containers runtime lifecycle in `core_server`:

```bash
CFC_CONTROL_BASE_URL=https://<worker-subdomain>.workers.dev \
CFC_SERVICE_TOKEN_ID=<service-token-id> \
CFC_SERVICE_TOKEN_SECRET=<service-token-secret> \
AGENT_RUNTIME_PROVIDER_DEFAULT=cloudflare_containers \
CORE_SERVICE_PORT=50051 GATEWAY_PORT=8080 ./core_server
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
  -H "Authorization: User <admin-username>" \
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

- `GET /debug/vars` (expvar metrics including agent session lifecycle/runtime/ws counters)
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
- `CFC_START_UNAUTHORIZED` or `CFC_STOP_UNAUTHORIZED`: service token rejected by Worker/Access.
- `CFC_RUNTIME_UNAVAILABLE`: Worker/control plane is unreachable or returned 5xx.
- `CFC_STREAM_DECODE_FAILED`: stream endpoint returned malformed SSE payload.
- `RUNTIME_BRIDGE_SYNC_FAILED`: core could not sync stream events; inspect control-plane `/stream` response and core logs.

## Accounts / Organizations

This repo uses lightweight account sign-in: web supports OAuth via Auth.js (Google/GitHub) and CLI supports username login. Requests include the signed-in username in metadata.

- The root slice (`root_slice`) is publicly viewable.
- Non-root slices are only visible/accessible to their owners.
- Organizations are user-created groups shown on the profile page (no invites yet).

Web OAuth environment variables (see `web/.env.example`):

```bash
VITE_WEB_AGENT_REAL_RUNTIME=1
AUTH_SECRET=replace-with-long-random-string
AUTH_GOOGLE_ID=your-google-oauth-client-id
AUTH_GOOGLE_SECRET=your-google-oauth-client-secret
AUTH_GITHUB_ID=your-github-oauth-client-id
AUTH_GITHUB_SECRET=your-github-oauth-client-secret
```

For local setup, copy the template and fill in values:

```bash
cp web/.env.example web/.env
```

For production deploys managed by `ops/restart_all.sh` or PM2, put the same `AUTH_*`
values in `ops/.env`. The restart script sources `ops/.env`, and the PM2 ecosystem
reads it directly so hourly restarts keep the web auth middleware configured.


CLI usage:

```bash
# Start OAuth device login and store refreshable credentials in ~/.gitslice/credentials.json
gs login

# Check the current stored login
gs login status

# Dev-only fallback: persist a username to ~/.gitslice/user
gs login your_name

# Or pass a dev username per-command
gs --user your_name slice create my-slice ./some/folder

# Remote filesystem commands
gs --user your_name fs write /your_name/README.md -f ./README.md
gs --user your_name fs cat /your_name/README.md
gs --user your_name fs snapshot -m "save point"
gs --user your_name fs shell
gs --user your_name fs upload ./project /your_name/project
gs --user your_name fs download /your_name/project ./project-copy
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
- Rebuilds/restarts core + gateway + web preview via `ops/start_web_server.sh`
- Verifies service health before exiting
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

The PM2 ecosystem reads `ops/.env` for both core and web settings, including
Auth.js credentials such as `AUTH_SECRET`, `AUTH_GOOGLE_*`, and `AUTH_GITHUB_*`.
The web app now runs a React Router SSR server on `127.0.0.1:4173` instead of `vite preview`.

To restore PM2 apps on reboot (user crontab approach):

```bash
@reboot PATH=/home/<user>/.nvm/versions/node/<node-version>/bin:/home/<user>/.local/go/bin:/home/<user>/.local/protoc/bin:/home/<user>/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin /home/<user>/.nvm/versions/node/<node-version>/bin/pm2 resurrect >> /home/<user>/workspace/gitslice/logs/pm2_reboot.log 2>&1
```

### NGINX (Cloudflare in Front, Origin HTTPS)

`ops/nginx.conf` terminates TLS on the origin for both `agenttools.dev` and `api.agenttools.dev`, redirects port `80` to HTTPS, and serves HTTP/2 on port `443`.

`api.agenttools.dev` routes the public gRPC service paths to the core server:
- `/slice.v1.SliceService/`
- `/admin.v1.AdminService/`
- `/account.v1.AccountService/`
- `/file.v1.FileService/`
- `/filesystem.v1.FilesystemService/`
- `/agent.v1.AgentService/`

`agenttools.dev` continues to serve the web app and `/v1/` REST gateway paths.

For CLI connectivity, target `api.agenttools.dev:443` with TLS enabled.

The origin Nginx config keeps long-lived gRPC calls open for up to `30m`, which
is required for large repo imports and similarly heavy CLI operations to survive
the public edge without a `504 Gateway Timeout`.

Cloudflare must proxy `api.agenttools.dev` in gRPC mode and use an HTTPS origin mode such as `Full (strict)`. Plain HTTP origin mode and h2c on port `80` are not compatible with Cloudflare gRPC proxying.

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
