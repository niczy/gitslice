# Cloudflare Worker Web + Neon DB + VM Core Deployment Plan

## Implementation Status

- Current status: `not started`
- Last updated: `2026-03-29`

---

## Executive Summary

The target production topology is:

1. `gitslice.io`
   - served by a Cloudflare Worker running the web app
2. `api.gitslice.io`
   - served from the current VM
   - Cloudflare-proxied to the VM origin
   - terminates public gRPC and grpc-gateway HTTP traffic
3. `Neon`
   - authoritative PostgreSQL metadata database for the Go core
4. object storage
   - stays on the current backend for the initial cutover (`filesystem` or `gcs`)
   - may move to R2 in a follow-on phase after the core/web/database split is stable

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

R2 can still be a good second-phase improvement because:

1. it removes dependence on VM-local blob storage if production is still using `filesystem`
2. it aligns blob hosting with the Cloudflare edge footprint
3. it gives an S3-compatible object-store target without requiring the D1 migration from the broader Cloudflare plan

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

## Worker Readiness Audit

Based on the current `web/` tree, the web app is **migratable to Cloudflare Workers**, but not yet deployable there without a portability pass.

### Already in good shape

1. the app is already SSR React Router rather than an ad hoc custom Node server in [package.json](/home/nic/workspace/gitslice/web/package.json)
2. the route handlers already use Web `Request`/`Response` style instead of Express-only APIs:
   - [auth.$.jsx](/home/nic/workspace/gitslice/web/app/routes/auth.$.jsx)
   - [auth.session.jsx](/home/nic/workspace/gitslice/web/app/routes/auth.session.jsx)
   - [auth.dev-login.jsx](/home/nic/workspace/gitslice/web/app/routes/auth.dev-login.jsx)
   - [auth.dev-logout.jsx](/home/nic/workspace/gitslice/web/app/routes/auth.dev-logout.jsx)
   - [auth.device.jsx](/home/nic/workspace/gitslice/web/app/routes/auth.device.jsx)
   - [v1.$.jsx](/home/nic/workspace/gitslice/web/app/routes/v1.$.jsx)
3. the React Router config is minimal in [react-router.config.js](/home/nic/workspace/gitslice/web/react-router.config.js), which reduces framework migration risk
4. the active proxy path in [proxy.js](/home/nic/workspace/gitslice/web/server/proxy.js) already uses `fetch` and Web request bodies

### Actual blockers

1. there is no Worker deployment target yet:
   - no Wrangler config
   - no Worker build script
   - no Worker preview or production deploy path
2. the active auth runtime in [auth.js](/home/nic/workspace/gitslice/web/server/auth.js) still assumes Node-oriented helpers:
   - `node:crypto`
   - `Buffer`
   - `process.env`
3. shared/client helpers still contain `Buffer` fallback code:
   - [highlight.js](/home/nic/workspace/gitslice/web/src/utils/highlight.js)
   - [CommitDiffPage.jsx](/home/nic/workspace/gitslice/web/src/components/CommitDiffPage.jsx)
4. the public API base URL still needs to become an explicit Worker/runtime binding rather than just a Node-side SSR default

### Legacy code that should be cleaned up

1. [auth-middleware.js](/home/nic/workspace/gitslice/web/auth-middleware.js) is Node `req`/`res` style middleware and does not appear to be part of the active route path anymore
2. that file should be either:
   - removed if fully dead
   - or isolated as Node-only compatibility code that is not part of the Worker bundle

### Working conclusion

1. the app does **not** need a rewrite to move to Workers
2. the migration is mainly:
   - runtime portability cleanup
   - explicit split-host API config
   - Worker deployment plumbing
3. the biggest real risk is auth/session portability, not React Router itself

---

## Locked Decisions

These decisions keep the rollout tractable:

1. keep the Go core on the current VM
2. keep public native gRPC on `api.gitslice.io`
3. keep Nginx on the VM as the TLS and Cloudflare-facing origin for the core
4. move only the web app to a Cloudflare Worker
5. use Neon as PostgreSQL, not D1
6. keep current object storage unchanged for the initial cutover
7. do not rewrite service APIs or move gRPC handling into Workers

---

## Non-Goals

This plan does not do the following:

1. move the Go core off the VM
2. move metadata from PostgreSQL to D1
3. move object storage from the current backend to R2 as part of the initial cutover
4. replace protobuf or grpc-gateway contracts
5. replace Nginx for the core origin path
6. collapse public gRPC and public web onto the same Cloudflare Worker

---

## Target Topology

### Public topology

1. `gitslice.io`
   - Cloudflare Worker
   - serves web HTML, assets, auth routes, and web SSR/data loader traffic
   - may proxy compatibility paths like `/v1/*` to `api.gitslice.io` if needed
2. `api.gitslice.io`
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
   - unchanged from current production in the initial phase
   - `filesystem` or `gcs`, whichever remains configured
   - optional future target: `r2`

### Operational topology

1. VM responsibilities after cutover:
   - `core_server`
   - Nginx for `api.gitslice.io`
   - no public web SSR process required
2. Worker responsibilities after cutover:
   - public web app at `gitslice.io`
   - web-side auth/session routes
   - optional compatibility proxying for browser HTTP calls

---

## Key Constraints

### Cloudflare Worker constraints

1. Workers are appropriate for HTTP and asset serving, not for public native gRPC termination.
2. The current web runtime must be made Worker-compatible because the active auth and shared helpers still use Node-oriented crypto and Buffer APIs.
3. Worker deployment should own `gitslice.io`, not `api.gitslice.io`.
4. The route layer is already close to Worker-ready; the main gap is shared runtime code and deployment plumbing.

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
- `R2_*` if/when using R2 in the follow-on object-store phase

### Worker config

- `PUBLIC_API_BASE_URL=https://api.gitslice.io`
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

1. `gs` CLI still targets `api.gitslice.io:443` for gRPC and grpc-gateway HTTP
2. browser users load `https://gitslice.io/` from the Worker
3. browser auth flows still work:
   - sign in
   - device approval
   - session inspection
4. browser-origin API calls still work
5. VM restarts for core do not require restarting the public web
6. web deploys do not require touching the VM core
7. if a later R2 cutover happens, blob reads/writes remain transparent to users and CLI clients

---

## Migration Strategy

Low-risk order:

1. make the web runtime portable before changing hosting
2. make the DB target portable before changing production DSN
3. cut over Neon first or in parallel canary
4. cut over the Worker-hosted web only after web/API boundary cleanup
5. keep the current VM web path available until the Worker path is proven
6. only consider R2 after the Worker + Neon + VM split is already stable

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
4. document that object storage is unchanged in the initial cutover, with R2 called out as a later optional phase
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

### PR3 - Optional R2 readiness and adapter validation

Goal: make a later R2 cutover low-risk without coupling it to the initial web/database migration.

Changes:

1. audit the current object-store backend usage and document production state:
   - `filesystem`
   - `gcs`
2. if not already present or production-ready, add or harden the R2/S3-compatible object-store adapter under [internal/storage](/home/nic/workspace/gitslice/internal/storage)
3. add copy and verification tooling for:
   - current object store -> R2
4. add parity tests for:
   - put/get/delete
   - missing-object semantics
   - concurrent object reads
5. add rollout docs for:
   - canary reads
   - full cutover
   - rollback to the previous object store

Acceptance:

1. the app still runs unchanged on the current object store
2. R2 can be validated independently in staging or canary
3. no production object-store cutover is required yet

Rollback:

1. keep the current object store authoritative
2. leave copied R2 data in place for later retry

---

### PR4 - Web/API boundary hardening for split-host deployment

Goal: make the web app run correctly when the public web host and public API host are different.

Changes:

1. centralize public API origin handling in the web server/runtime
2. stop assuming same-host `/v1/*` availability inside the web app
3. make auth/device approval flows explicitly target `api.gitslice.io` through config
4. audit cookie, callback, redirect, and forwarded-header behavior for:
   - `gitslice.io`
   - `api.gitslice.io`
5. add browser and SSR tests for split-host mode
6. keep current Node SSR deployment working during this refactor

Acceptance:

1. current Node-hosted web app works correctly when its API target is remote
2. auth flows still pass in e2e coverage
3. no VM/Worker cutover yet

Rollback:

1. restore same-host defaults

---

### PR5 - Worker portability refactor for the web runtime

Goal: remove Node-only assumptions that block Cloudflare Worker deployment.

Changes:

1. replace Node-specific crypto and encoding helpers in the active Worker path:
   - [auth.js](/home/nic/workspace/gitslice/web/server/auth.js)
   - [highlight.js](/home/nic/workspace/gitslice/web/src/utils/highlight.js)
   - [CommitDiffPage.jsx](/home/nic/workspace/gitslice/web/src/components/CommitDiffPage.jsx)
2. introduce a small runtime abstraction for:
   - HMAC/signing
   - random bytes
   - base64url encoding/decoding
3. remove or isolate [auth-middleware.js](/home/nic/workspace/gitslice/web/auth-middleware.js) so dead Node middleware is not treated as part of the Worker path
4. ensure server-side proxy/fetch code stays on Web-standard Request/Response semantics
5. keep Node SSR support as a fallback during this PR
6. add tests that run the shared auth/runtime helpers in a Worker-compatible environment

Acceptance:

1. the web app can run in both Node and Worker-compatible runtimes
2. auth/session/device approval still pass
3. no production hosting cutover yet

Rollback:

1. restore Node-only helpers

---

### PR6 - Cloudflare Worker deployment target for the web app

Goal: make the web app deployable to Cloudflare Workers without changing production traffic yet.

Changes:

1. add Wrangler config for the web app
2. add Worker build/deploy scripts under `web/`
3. add Worker env/secrets documentation
4. explicitly bind the public API base URL for split-host mode:
   - `PUBLIC_API_BASE_URL=https://api.gitslice.io`
5. add preview deployment support in GitHub Actions
6. bind static assets correctly for the React Router build output
7. add a Worker-local dev path alongside the existing Node preview path

Acceptance:

1. the web app can be preview-deployed to a Workers hostname
2. preview smoke tests pass
3. auth routes and `/v1` compatibility proxying work against a remote API host
4. Node deploy path still exists as fallback

Rollback:

1. disable preview deployment and continue serving the web from the VM

---

### PR7 - Production Worker cutover for `gitslice.io`

Goal: move public web traffic to Cloudflare Workers while keeping core traffic on the VM.

Changes:

1. assign `gitslice.io` to the Worker deployment
2. keep `api.gitslice.io` on the VM + Nginx origin path
3. reduce VM web responsibilities:
   - stop treating the VM web process as the public source of truth
   - optionally keep it as a short-term rollback path
4. update Nginx docs and origin runbooks so the VM is described as API-only
5. add explicit end-to-end smoke checks for:
   - Worker web
   - public `/auth/*`
   - public `/v1/*` browser path, if retained
   - public gRPC at `api.gitslice.io`

Acceptance:

1. `gitslice.io` is Worker-hosted
2. `api.gitslice.io` remains healthy for CLI and browser API traffic
3. user-visible web behavior is unchanged

Rollback:

1. point `gitslice.io` back to the VM-hosted web deploy
2. keep the Worker deploy intact for retry

---

### PR8 - VM deploy cleanup, observability, and rollback hardening

Goal: make the split deployment operationally safe after cutover.

Changes:

1. update VM deploy scripts to treat the VM as core-only in production
2. remove stale assumptions that public web health comes from `127.0.0.1:4173`
3. add deployment verification scripts for:
   - local core health
   - public `api.gitslice.io` gRPC/gateway health
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

1. merge PR1 through PR5
2. run web preview deployment on Workers
3. cut core to Neon on the VM
4. verify core behavior on Neon before any public web cutover
5. merge PR6 and verify Worker previews
6. merge PR7 and cut `gitslice.io` to the Worker
7. merge PR8 and remove stale VM-web assumptions from production ops
8. treat R2 as a separate optional cutover after the above is stable

---

## Success Criteria

The migration is complete when:

1. `gitslice.io` is served from a Cloudflare Worker
2. `api.gitslice.io` serves public gRPC and `/v1/*` from the current VM origin
3. the Go core uses Neon as its authoritative PostgreSQL database
4. the VM no longer needs to run the public web app in production
5. auth flows, CLI flows, and browser flows all continue to work

Optional extended success criteria for a later R2 phase:

1. object blobs are served from R2 with no user-visible API change
2. the VM is no longer responsible for production blob durability if `filesystem` was previously in use

---

## Risks

### Highest-risk items

1. web auth runtime portability to Workers
2. cross-host cookie and redirect behavior between `gitslice.io` and `api.gitslice.io`
3. Neon connection tuning under real production concurrency
4. stale production assumptions in current PM2/Nginx/docs scripts
5. later R2 data migration correctness if object storage is moved after the main cutover

### Lower-risk items

1. core behavior on Neon itself, because the app already targets PostgreSQL
2. preserving public gRPC, because the VM + Nginx origin path stays in place
3. later R2 adoption, because it can be decoupled from the web/database migration

---

## Recommended Defaults

If we choose explicit defaults in follow-up PRs, the recommended production defaults are:

1. keep `api.gitslice.io` as the CLI and public API origin
2. keep the core on the VM behind Nginx on `443`
3. use Neon direct PostgreSQL connection for the long-running Go core
4. reserve pooled/HTTP-style Neon access for future serverless jobs only if needed
5. keep object storage unchanged until the Worker + Neon + VM split is stable
6. if production still depends on VM-local filesystem blobs, plan an explicit R2 follow-on cutover instead of leaving that coupling in place indefinitely

---

## Sources

1. Cloudflare Workers Wrangler config and assets: https://developers.cloudflare.com/workers/wrangler/configuration/
2. Cloudflare gRPC requirements: https://developers.cloudflare.com/network/grpc-connections/
3. Neon connection pooling: https://neon.com/docs/connect/connection-pooling
4. Neon compute lifecycle: https://neon.com/docs/introduction/compute-lifecycle
