# Cloudflare Worker Web + Neon DB + VM Core Deployment Plan

## Implementation Status

- Current status: `not started`
- Last updated: `2026-03-29`

---

## Executive Summary

The target production topology is:

1. `agenttools.dev`
   - served by a Cloudflare Worker running the web app
2. `api.agenttools.dev`
   - served from the current VM
   - Cloudflare-proxied to the VM origin
   - terminates public gRPC and grpc-gateway HTTP traffic
3. `Neon`
   - authoritative PostgreSQL metadata database for the Go core
4. object storage
   - stays on the current backend for now (`filesystem` or `gcs`)
   - no R2 migration in this plan

This plan deliberately avoids the larger Cloudflare migration path in [cludflare_migration.md](/home/nic/workspace/gitslice/spec/cludflare_migration.md). That older plan assumes D1 and R2 migration. This plan does not.

This is the lowest-risk architecture change from the current repo state:

1. keep the Go core on a normal long-running VM
2. keep native gRPC on the VM path
3. move only the web app to Cloudflare Workers
4. move only the metadata database to Neon

---

## Why This Plan

The current repo is already close to this topology:

1. the core now serves gRPC and the HTTP gateway on the same port in [main.go](/home/nic/workspace/gitslice/servers/core/main.go)
2. public gRPC already expects Cloudflare in front of an HTTPS origin, per the current Nginx setup in [nginx.conf](/home/nic/workspace/gitslice/ops/nginx.conf)
3. the web app is already React Router SSR in [package.json](/home/nic/workspace/gitslice/web/package.json), not the older client-only SPA assumed by the broader Cloudflare migration plan
4. the storage backend is already PostgreSQL-native in [postgres_native.go](/home/nic/workspace/gitslice/internal/storage/postgres_native.go), so Neon is an infrastructure change, not a database-model rewrite

This means:

1. moving the core to Workers is unnecessary
2. moving metadata to D1 is unnecessary
3. moving blobs to R2 is unnecessary for the initial production cleanup

The real deployment simplification is:

1. Worker for web
2. VM for core
3. Neon for DB

---

## Current Repo Reality

Relevant facts from the current codebase:

1. the core listener is unified on `:50051` for both gRPC and grpc-gateway HTTP in [main.go](/home/nic/workspace/gitslice/servers/core/main.go)
2. production still assumes a VM with PM2 + Nginx in:
   - [restart_all.sh](/home/nic/workspace/gitslice/ops/restart_all.sh)
   - [start_web_server.sh](/home/nic/workspace/gitslice/ops/start_web_server.sh)
   - [ecosystem.config.cjs](/home/nic/workspace/gitslice/ops/ecosystem.config.cjs)
   - [nginx.conf](/home/nic/workspace/gitslice/ops/nginx.conf)
3. the web app is SSR React Router, currently served in Node via `react-router-serve` in [package.json](/home/nic/workspace/gitslice/web/package.json)
4. the web runtime still contains Node-specific code paths:
   - [auth.js](/home/nic/workspace/gitslice/web/server/auth.js)
   - [auth-middleware.js](/home/nic/workspace/gitslice/web/auth-middleware.js)
   - [proxy.js](/home/nic/workspace/gitslice/web/server/proxy.js)
5. the core currently uses a standard PostgreSQL DSN in [config.go](/home/nic/workspace/gitslice/internal/config/config.go)
6. the public web and public API are currently coupled in origin docs and runbooks

---

## Locked Decisions

These decisions keep the rollout tractable:

1. keep the Go core on the current VM
2. keep public native gRPC on `api.agenttools.dev`
3. keep Nginx on the VM as the TLS and Cloudflare-facing origin for the core
4. move only the web app to a Cloudflare Worker
5. use Neon as PostgreSQL, not D1
6. keep current object storage unchanged in this plan
7. do not rewrite service APIs or move gRPC handling into Workers

---

## Non-Goals

This plan does not do the following:

1. move the Go core off the VM
2. move metadata from PostgreSQL to D1
3. move object storage from the current backend to R2
4. replace protobuf or grpc-gateway contracts
5. replace Nginx for the core origin path
6. collapse public gRPC and public web onto the same Cloudflare Worker

---

## Target Topology

### Public topology

1. `agenttools.dev`
   - Cloudflare Worker
   - serves web HTML, assets, auth routes, and web SSR/data loader traffic
   - may proxy compatibility paths like `/v1/*` to `api.agenttools.dev` if needed
2. `api.agenttools.dev`
   - Cloudflare-proxied hostname
   - routes public gRPC to the VM origin
   - routes grpc-gateway `/v1/*` to the VM origin
3. current VM origin
   - Nginx on `443`
   - proxies gRPC and `/v1/*` to local core on `127.0.0.1:50051`
   - no longer serves the public web app once cutover is complete

### Data topology

1. `Neon`
   - authoritative metadata DB for the Go core
   - accessed directly by the core with PostgreSQL wire protocol
2. object store
   - unchanged from current production
   - `filesystem` or `gcs`, whichever remains configured

### Operational topology

1. VM responsibilities after cutover:
   - `core_server`
   - Nginx for `api.agenttools.dev`
   - no public web SSR process required
2. Worker responsibilities after cutover:
   - public web app at `agenttools.dev`
   - web-side auth/session routes
   - optional compatibility proxying for browser HTTP calls

---

## Key Constraints

### Cloudflare Worker constraints

1. Workers are appropriate for HTTP and asset serving, not for public native gRPC termination.
2. The current web runtime must be made Worker-compatible because it still uses Node-specific crypto and Buffer APIs.
3. Worker deployment should own `agenttools.dev`, not `api.agenttools.dev`.

### Core / gRPC constraints

1. Public gRPC should continue to go through the VM origin path with TLS and HTTP/2.
2. The current single-port core layout is already the correct shape for this.
3. The Worker should not be inserted into the public gRPC path.

### Neon constraints

1. Neon is PostgreSQL-compatible, but the core should treat it as a networked managed database, not a local low-latency host.
2. Connection pooling and timeouts must be explicitly configured.
3. Cutover should be reversible to the previous PostgreSQL deployment until confidence is high.

---

## Config Surface

The plan should standardize the following deployment config:

### Core / VM config

- `CORE_SERVICE_PORT`
- `POSTGRES_DSN`
- `POSTGRES_MIGRATION_DSN`
- `POSTGRES_MAX_CONNS`
- `POSTGRES_MIN_CONNS`
- `POSTGRES_MAX_CONN_LIFETIME`
- `PUBLIC_WEB_BASE_URL`
- `PUBLIC_API_BASE_URL`
- `OBJECT_STORE_TYPE`
- `OBJECT_STORE_DIR`
- `GCS_*` if still using GCS

### Worker config

- `PUBLIC_API_BASE_URL=https://api.agenttools.dev`
- `AUTH_SECRET`
- `AUTH_GOOGLE_*`
- `AUTH_GITHUB_*`
- `WORKER_ENV`
- `CF_ACCOUNT_ID`
- `CF_ZONE_ID`
- `CLOUDFLARE_WORKER_NAME`

### Deployment mode flags

- `WEB_DEPLOY_TARGET=node|cloudflare_worker`
- `WEB_COMPAT_RUNTIME=node|worker`

The exact env names can change in implementation, but the split itself should not.

---

## Required Product Behavior

After the migration:

1. `gs` CLI still targets `api.agenttools.dev:443` for gRPC and grpc-gateway HTTP
2. browser users load `https://agenttools.dev/` from the Worker
3. browser auth flows still work:
   - sign in
   - device approval
   - session inspection
4. browser-origin API calls still work
5. VM restarts for core do not require restarting the public web
6. web deploys do not require touching the VM core

---

## Migration Strategy

Low-risk order:

1. make the web runtime portable before changing hosting
2. make the DB target portable before changing production DSN
3. cut over Neon first or in parallel canary
4. cut over the Worker-hosted web only after web/API boundary cleanup
5. keep the current VM web path available until the Worker path is proven

This is intentionally not a big-bang cutover.

---

## PR-by-PR Plan

### PR1 - Target topology, env surface, and rollout guardrails

Goal: make the target deployment explicit in code and docs without changing production behavior.

Changes:

1. add this spec
2. update deployment docs to describe the target split:
   - Worker web
   - VM core
   - Neon DB
3. add stable env naming for:
   - public API base URL
   - public web base URL
   - web deploy target
   - worker deploy settings
   - Neon pool tuning
4. document that object storage is unchanged in this plan
5. add rollout and rollback checklist sections to `README.md`

Acceptance:

1. no runtime behavior changes
2. docs and env names are stable enough for follow-up PRs
3. existing local and prod docs no longer imply that public web must live on the VM

Rollback:

1. revert docs/config-only changes

---

### PR2 - Neon readiness for the core service

Goal: make the Go core production-safe on Neon without changing web hosting yet.

Changes:

1. add explicit Postgres/Neon pool configuration to [config.go](/home/nic/workspace/gitslice/internal/config/config.go)
2. thread pool tuning through the Postgres-native storage setup in [postgres_native.go](/home/nic/workspace/gitslice/internal/storage/postgres_native.go)
3. add startup validation and logging for remote Postgres configuration
4. add a core health check path that distinguishes:
   - process healthy
   - database reachable
5. document direct DSN vs migration DSN expectations
6. add a Neon canary runbook:
   - schema migration
   - backup/export
   - cutover
   - rollback

Acceptance:

1. core runs cleanly against a Neon database
2. targeted storage tests pass
3. deploy docs contain an explicit Neon production setup

Rollback:

1. point `POSTGRES_DSN` back to the previous PostgreSQL host
2. leave Neon data intact for retry

---

### PR3 - Web/API boundary hardening for split-host deployment

Goal: make the web app run correctly when the public web host and public API host are different.

Changes:

1. centralize public API origin handling in the web server/runtime
2. stop assuming same-host `/v1/*` availability inside the web app
3. make auth/device approval flows explicitly target `api.agenttools.dev` through config
4. audit cookie, callback, redirect, and forwarded-header behavior for:
   - `agenttools.dev`
   - `api.agenttools.dev`
5. add browser and SSR tests for split-host mode
6. keep current Node SSR deployment working during this refactor

Acceptance:

1. current Node-hosted web app works correctly when its API target is remote
2. auth flows still pass in e2e coverage
3. no VM/Worker cutover yet

Rollback:

1. restore same-host defaults

---

### PR4 - Worker portability refactor for the web runtime

Goal: remove Node-only assumptions that block Cloudflare Worker deployment.

Changes:

1. replace Node-specific crypto and encoding helpers in:
   - [auth.js](/home/nic/workspace/gitslice/web/server/auth.js)
   - [auth-middleware.js](/home/nic/workspace/gitslice/web/auth-middleware.js)
   - any Buffer-only browser/runtime helpers
2. introduce a small runtime abstraction for:
   - HMAC/signing
   - random bytes
   - base64url encoding/decoding
3. ensure server-side proxy/fetch code uses Web-standard Request/Response semantics
4. keep Node SSR support as a fallback during this PR
5. add tests that run the shared auth/runtime helpers in a Worker-compatible environment

Acceptance:

1. the web app can run in both Node and Worker-compatible runtimes
2. auth/session/device approval still pass
3. no production hosting cutover yet

Rollback:

1. restore Node-only helpers

---

### PR5 - Cloudflare Worker deployment target for the web app

Goal: make the web app deployable to Cloudflare Workers without changing production traffic yet.

Changes:

1. add Wrangler config for the web app
2. add Worker build/deploy scripts under `web/`
3. add Worker env/secrets documentation
4. add preview deployment support in GitHub Actions
5. bind static assets correctly for the React Router build output
6. add a Worker-local dev path alongside the existing Node preview path

Acceptance:

1. the web app can be preview-deployed to a Workers hostname
2. preview smoke tests pass
3. Node deploy path still exists as fallback

Rollback:

1. disable preview deployment and continue serving the web from the VM

---

### PR6 - Production Worker cutover for `agenttools.dev`

Goal: move public web traffic to Cloudflare Workers while keeping core traffic on the VM.

Changes:

1. assign `agenttools.dev` to the Worker deployment
2. keep `api.agenttools.dev` on the VM + Nginx origin path
3. reduce VM web responsibilities:
   - stop treating the VM web process as the public source of truth
   - optionally keep it as a short-term rollback path
4. update Nginx docs and origin runbooks so the VM is described as API-only
5. add explicit end-to-end smoke checks for:
   - Worker web
   - public `/auth/*`
   - public `/v1/*` browser path, if retained
   - public gRPC at `api.agenttools.dev`

Acceptance:

1. `agenttools.dev` is Worker-hosted
2. `api.agenttools.dev` remains healthy for CLI and browser API traffic
3. user-visible web behavior is unchanged

Rollback:

1. point `agenttools.dev` back to the VM-hosted web deploy
2. keep the Worker deploy intact for retry

---

### PR7 - VM deploy cleanup, observability, and rollback hardening

Goal: make the split deployment operationally safe after cutover.

Changes:

1. update VM deploy scripts to treat the VM as core-only in production
2. remove stale assumptions that public web health comes from `127.0.0.1:4173`
3. add deployment verification scripts for:
   - local core health
   - public `api.agenttools.dev` gRPC/gateway health
   - public Worker web health
4. document rollback procedures for:
   - Neon failure
   - Worker deploy failure
   - Worker/auth regression
   - API origin regression
5. add CI or scripted smoke checks that hit both public hosts

Acceptance:

1. production runbooks match the actual split architecture
2. deploy verification is repeatable
3. rollback instructions are explicit and tested

Rollback:

1. keep older VM web docs/scripts available until the new runbook is trusted

---

## Cutover Sequence

Recommended production order:

1. merge PR1 through PR4
2. run web preview deployment on Workers
3. cut core to Neon on the VM
4. verify core behavior on Neon before any public web cutover
5. merge PR5 and verify Worker previews
6. merge PR6 and cut `agenttools.dev` to the Worker
7. merge PR7 and remove stale VM-web assumptions from production ops

---

## Success Criteria

The migration is complete when:

1. `agenttools.dev` is served from a Cloudflare Worker
2. `api.agenttools.dev` serves public gRPC and `/v1/*` from the current VM origin
3. the Go core uses Neon as its authoritative PostgreSQL database
4. the VM no longer needs to run the public web app in production
5. auth flows, CLI flows, and browser flows all continue to work

---

## Risks

### Highest-risk items

1. web auth runtime portability to Workers
2. cross-host cookie and redirect behavior between `agenttools.dev` and `api.agenttools.dev`
3. Neon connection tuning under real production concurrency
4. stale production assumptions in current PM2/Nginx/docs scripts

### Lower-risk items

1. core behavior on Neon itself, because the app already targets PostgreSQL
2. preserving public gRPC, because the VM + Nginx origin path stays in place

---

## Recommended Defaults

If we choose explicit defaults in follow-up PRs, the recommended production defaults are:

1. keep `api.agenttools.dev` as the CLI and public API origin
2. keep the core on the VM behind Nginx on `443`
3. use Neon direct PostgreSQL connection for the long-running Go core
4. reserve pooled/HTTP-style Neon access for future serverless jobs only if needed
5. keep object storage unchanged until there is a separate reason to migrate it

---

## Sources

1. Cloudflare Workers Wrangler config and assets: https://developers.cloudflare.com/workers/wrangler/configuration/
2. Cloudflare gRPC requirements: https://developers.cloudflare.com/network/grpc-connections/
3. Neon connection pooling: https://neon.com/docs/connect/connection-pooling
4. Neon compute lifecycle: https://neon.com/docs/introduction/compute-lifecycle

