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
   - uses Cloudflare R2 as the authoritative blob store

The environment split is:

1. staging
   - web: `agenttools.dev`
   - api: `api.agenttools.dev`
2. production
   - web: `gitslice.io`
   - api: `api.gitslice.io`

Each environment must use separate infrastructure namespaces:

1. separate Neon database or branch/role namespace for staging vs production
2. separate R2 bucket or, at minimum, an isolated environment prefix for staging vs production
3. separate Worker deployments and secrets for staging vs production
4. separate VM config and origin hostnames for staging vs production where applicable
5. separate internal core listeners, PM2 app names, and Nginx upstreams for staging vs production on the shared VM

This plan deliberately avoids the larger Cloudflare migration path in [cludflare_migration.md](/home/nic/workspace/gitslice/spec/cludflare_migration.md). That older plan assumes D1 and a broader Cloudflare-hosted architecture. This plan does not.

There is currently no meaningful production traffic, so this migration does not need to preserve backward compatibility for:

1. the VM-hosted public web path
2. current browser sessions or cookies
3. current production deploy topology
4. temporary compatibility shims that only exist to ease live traffic cutover

This is the lowest-risk architecture change from the current repo state:

1. keep the Go core on a normal long-running VM
2. keep native gRPC on the VM path
3. move only the web app to Cloudflare Workers
4. move the metadata database to Neon
5. move blob storage to R2

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
3. moving blob storage to R2 does not require API or protocol changes

The real deployment simplification is:

1. Worker for web
2. VM for core
3. Neon for DB
4. R2 for blob storage

R2 is part of the main plan because:

1. production should not depend on VM-local blob durability
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

## Environment Topology And Namespace Rules

This plan assumes two long-lived environments:

### Staging

1. public web host: `agenttools.dev`
2. public API host: `api.agenttools.dev`
3. purpose:
   - pre-production verification
   - Worker validation
   - Neon cutover rehearsal
   - R2 validation

### Production

1. public web host: `gitslice.io`
2. public API host: `api.gitslice.io`
3. purpose:
   - public production environment

### Isolation requirements

The environments must not share mutable infrastructure state.

Required isolation:

1. Neon
   - separate project, database, or branch per environment
   - separate credentials per environment
   - staging migrations must not mutate production data
2. Worker deployments
   - separate Worker environments/routes
   - separate secrets for OAuth, auth cookies, and API base URLs
3. Core VM config
   - separate env files or explicit deploy environment selection
   - no shared production DSN in staging deploy scripts
4. R2
   - separate bucket per environment if possible
   - otherwise a strict environment prefix such as:
     - `staging/...`
     - `production/...`
   - no mixed staging/production object namespace

Recommended default naming:

1. staging web: `agenttools.dev`
2. staging api: `api.agenttools.dev`
3. production web: `gitslice.io`
4. production api: `api.gitslice.io`
5. staging Worker env: `staging`
6. production Worker env: `production`
7. staging object namespace: `staging`
8. production object namespace: `production`

Recommended shared-VM internal listener layout:

1. production core listener: `127.0.0.1:50051`
2. staging core listener: `127.0.0.1:50052`
3. production PM2 app: `gitslice-core-production`
4. staging PM2 app: `gitslice-core-staging`
5. production Nginx upstream hostname: `api.gitslice.io`
6. staging Nginx upstream hostname: `api.agenttools.dev`

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
6. use R2 as the target object store for both staging and production
7. do not rewrite service APIs or move gRPC handling into Workers
8. do not preserve the VM-hosted web deploy as a long-term fallback once the Worker path is ready
9. it is acceptable for the cutover to invalidate existing browser sessions/cookies

---

## Non-Goals

This plan does not do the following:

1. move the Go core off the VM
2. move metadata from PostgreSQL to D1
3. replace protobuf or grpc-gateway contracts
4. replace Nginx for the core origin path
5. collapse public gRPC and public web onto the same Cloudflare Worker
6. preserve compatibility with the pre-cutover production web deployment

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
   - routes `api.gitslice.io` gRPC and `/v1/*` to local production core on `127.0.0.1:50051`
   - routes `api.agenttools.dev` gRPC and `/v1/*` to local staging core on `127.0.0.1:50052`
   - no longer serves the public web app once cutover is complete

### Data topology

1. `Neon`
   - authoritative metadata DB for the Go core
   - accessed directly by the core with PostgreSQL wire protocol
2. object store
   - R2
   - staging and production must use separate buckets or strict prefixes

### Operational topology

1. VM responsibilities after cutover:
   - `core_server`
   - Nginx for `api.gitslice.io`
   - separate staging and production core processes on distinct local listeners
   - no public web SSR process required
   - no production blob durability responsibility on local filesystem
2. Worker responsibilities after cutover:
   - public web app at `gitslice.io`
   - web-side auth/session routes
   - optional compatibility proxying for browser HTTP calls

---

## Key Constraints

### Migration principle

Because there is no real production traffic, the migration should optimize for a clean final architecture, not incremental compatibility.

That means:

1. staging is still the proving ground
2. production can be cut directly to the target topology once staging is validated
3. old web deploy paths do not need to remain available after the cutover
4. session/cookie continuity is not a requirement
5. temporary compatibility code should only be kept if it reduces implementation work materially

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
3. Staging and production must use separate Neon namespaces and credentials.

---

## Config Surface

The plan should standardize the following deployment config:

### Core / VM config

- `DEPLOY_ENV=staging|production`
- `CORE_BIND_ADDR`
- `CORE_SERVICE_PORT`
- `PM2_APP_NAME`
- `NGINX_UPSTREAM_NAME`
- `POSTGRES_DSN`
- `POSTGRES_MIGRATION_DSN`
- `POSTGRES_MAX_CONNS`
- `POSTGRES_MIN_CONNS`
- `POSTGRES_MAX_CONN_LIFETIME`
- `PUBLIC_WEB_BASE_URL`
- `PUBLIC_API_BASE_URL`
- `OBJECT_STORE_TYPE`
- `R2_*`

### Worker config

- `DEPLOY_ENV=staging|production`
- `PUBLIC_WEB_BASE_URL`
- `PUBLIC_API_BASE_URL`
- `AUTH_SECRET`
- `WORKOS_CLIENT_ID`
- `WORKOS_API_KEY`
- `WORKOS_COOKIE_PASSWORD`
- `WORKOS_REDIRECT_URI`
- `WORKER_ENV`
- `CF_ACCOUNT_ID`
- `CF_ZONE_ID`
- `CLOUDFLARE_WORKER_NAME`

### Environment-specific resource config

- `NEON_PROJECT_ID` or equivalent environment-scoped reference
- `NEON_DATABASE_NAME`
- `R2_BUCKET`
- `R2_PREFIX`
- `CORE_INTERNAL_PORT`

### Deployment mode flags

- `WEB_DEPLOY_TARGET=node|cloudflare_worker`
- `WEB_COMPAT_RUNTIME=node|worker`

The exact env names can change in implementation, but the split itself should not.

Required environment-specific defaults on the shared VM:

1. production:
   - `CORE_BIND_ADDR=127.0.0.1`
   - `CORE_SERVICE_PORT=50051`
2. staging:
   - `CORE_BIND_ADDR=127.0.0.1`
   - `CORE_SERVICE_PORT=50052`

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
7. blob reads/writes through R2 remain transparent to users and CLI clients
8. staging changes on `agenttools.dev` must not mutate production data or production object blobs
9. production is allowed to invalidate old web sessions during cutover
10. production must not rely on VM-local filesystem object storage
11. staging and production core instances can run concurrently on the same VM without port collisions

---

## Migration Strategy

Simplified order:

1. make the web runtime portable and split-host safe
2. make the core Neon-ready with explicit staging and production namespaces
3. make R2 the production-ready object-store target with explicit staging and production namespaces
4. prove the final architecture end to end on staging:
   - Worker web on `agenttools.dev`
   - VM core on `api.agenttools.dev`
   - staging Neon namespace
   - staging R2 namespace
5. cut production directly to the final architecture:
   - Worker web on `gitslice.io`
   - VM core on `api.gitslice.io`
   - production Neon namespace
   - production R2 namespace
6. remove old VM-web assumptions instead of preserving them for compatibility

This is intentionally a staging-first direct cutover, not a canary/dual-run migration.

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
4. document that R2 is part of the target architecture and VM-local blob storage is not
5. add environment naming and namespace rules:
   - `agenttools.dev` staging
   - `gitslice.io` production
6. add rollout and rollback checklist sections to `README.md`
7. document explicitly that the migration does not need backward compatibility for current production web traffic or sessions

Acceptance:

1. no runtime behavior changes
2. docs and env names are stable enough for follow-up PRs
3. existing local and prod docs no longer imply that public web must live on the VM

Rollback:

1. revert docs/config-only changes

---

### PR2 - Neon readiness for the core service

Goal: make the Go core ready to run on isolated staging and production Neon namespaces.

Changes:

1. add explicit Postgres/Neon pool configuration to [config.go](/home/nic/workspace/gitslice/internal/config/config.go)
2. thread pool tuning through the Postgres-native storage setup in [postgres_native.go](/home/nic/workspace/gitslice/internal/storage/postgres_native.go)
3. add startup validation and logging for remote Postgres configuration
4. add a core health check path that distinguishes:
   - process healthy
   - database reachable
5. document direct DSN vs migration DSN expectations
6. make environment isolation explicit in config and runbooks:
   - staging Neon target
   - production Neon target

Acceptance:

1. core runs cleanly against a Neon database
2. targeted storage tests pass
3. deploy docs contain explicit staging and production Neon setup

Rollback:

1. point the affected environment back to the previous PostgreSQL host

---

### PR3 - R2 readiness and object-store cutover support

Goal: make R2 the production-ready object store for both staging and production.

Changes:

1. audit the current object-store backend usage and document staging vs production state:
   - `filesystem`
   - `gcs`
2. add or harden the R2/S3-compatible object-store adapter under [internal/storage](/home/nic/workspace/gitslice/internal/storage)
3. add copy and verification tooling for:
   - current object store -> R2
4. enforce environment namespace separation in tooling:
   - staging target
   - production target
   - no default mixed writes
5. add parity tests for:
   - put/get/delete
   - missing-object semantics
   - concurrent object reads
6. add rollout docs for direct staging and production object-store cutover
7. document bucket/prefix naming per environment

Acceptance:

1. R2 is production-ready for staging and production namespaces
2. VM-local object storage is no longer part of the target production architecture
3. staging and production object namespaces are provably isolated

Rollback:

1. keep the previous object store authoritative until the cutover is performed

---

### PR4 - Web runtime portability and split-host cleanup

Goal: make the web app Worker-compatible and explicit about talking to a separate API host.

Changes:

1. replace Node-specific crypto and encoding helpers in the active Worker path:
   - [auth.js](/home/nic/workspace/gitslice/web/server/auth.js)
   - [highlight.js](/home/nic/workspace/gitslice/web/src/utils/highlight.js)
   - [CommitDiffPage.jsx](/home/nic/workspace/gitslice/web/src/components/CommitDiffPage.jsx)
2. introduce a small runtime abstraction for:
   - HMAC/signing
   - random bytes
   - base64url encoding/decoding
3. centralize public API origin handling in the web runtime
4. stop assuming same-host `/v1/*` availability inside the web app
5. make auth/device approval flows explicitly target the configured API origin:
   - `api.agenttools.dev`
   - `api.gitslice.io`
6. remove or isolate [auth-middleware.js](/home/nic/workspace/gitslice/web/auth-middleware.js) so dead Node middleware is not treated as part of the Worker path
7. add browser and SSR tests for split-host mode

Acceptance:

1. the web app can run in a Worker-compatible runtime
2. the web app works correctly against a remote API origin
3. auth/session/device approval still pass in tests

Rollback:

1. restore the previous Node-only runtime helpers

---

### PR5 - Cloudflare Worker deployment target and staging cutover

Goal: deploy the web app to Workers in staging and validate the final target architecture end to end.

Changes:

1. add Wrangler config for the web app
2. add Worker build/deploy scripts under `web/`
3. add Worker env/secrets documentation
4. explicitly bind the staging API base URL:
   - `PUBLIC_API_BASE_URL=https://api.agenttools.dev`
5. add preview deployment support in GitHub Actions
6. bind static assets correctly for the React Router build output
7. add a Worker-local dev path alongside the existing local preview path
8. cut staging web to the Worker on `agenttools.dev`
9. point staging core at the staging Neon namespace
10. point staging object storage at the staging R2 namespace
11. verify the staging split-host environment end to end

Acceptance:

1. `agenttools.dev` is Worker-hosted
2. `api.agenttools.dev` remains VM-hosted and healthy
3. staging browser auth, browser API traffic, CLI traffic, core health, and object reads/writes all work
4. staging and production remain isolated
5. staging and production cores run concurrently on the shared VM without listener conflicts

Rollback:

1. point staging back to the previous web host if needed

---

### PR6 - Production direct cutover to the final topology

Goal: replace the current production deployment with the final Worker + VM + Neon topology.

Changes:

1. assign `gitslice.io` to the production Worker deployment
2. keep `api.gitslice.io` on the VM + Nginx origin path
3. point production core at the production Neon namespace
4. point production object storage at the production R2 namespace
5. remove production assumptions that the VM still serves the public web or stores durable blobs locally
6. invalidate or rotate production web session/cookie secrets if needed rather than preserving compatibility
7. update docs and runbooks so production is described as:
   - Worker web on `gitslice.io`
   - VM core on `api.gitslice.io`
   - R2-backed object storage
8. add explicit production smoke checks for:
   - `https://gitslice.io/`
   - public auth flow
   - `https://api.gitslice.io/v1/global/state`
   - CLI access to `api.gitslice.io:443`
   - object read/write path through R2
   - staging API remains healthy on `api.agenttools.dev` after production cutover

Acceptance:

1. `gitslice.io` is Worker-hosted
2. `api.gitslice.io` remains healthy for CLI and browser API traffic
3. production uses its own Neon namespace
4. production uses its own R2 namespace
5. the VM no longer needs to be considered the production web origin or durable object store
6. invalidating old production browser sessions is acceptable
7. staging remains healthy on its separate local port and hostname after production cutover

Rollback:

1. point production web DNS/routes back to the previous origin if the cutover fails immediately

---

### PR7 - VM deploy cleanup and final runbook alignment

Goal: make ops and deploy tooling match the final architecture instead of the old VM-web model.

Changes:

1. update VM deploy scripts to treat the VM as core-only in production
2. remove stale assumptions that public web health comes from `127.0.0.1:4173`
3. add deployment verification scripts for:
   - local core health
   - local staging core health on `127.0.0.1:50052`
   - local production core health on `127.0.0.1:50051`
   - public staging web health
   - public staging API health
   - public production Worker web health
   - public production API health
   - staging and production object-store health against R2
4. document the final staging and production topology clearly
5. remove stale docs about Node SSR as the production web host
6. remove stale docs or scripts that imply production durability depends on VM-local object storage
7. update ops docs/scripts to show separate PM2 process names and Nginx upstreams for staging vs production

Acceptance:

1. production runbooks match the final split architecture
2. deploy verification is repeatable for both environments
3. stale VM-web assumptions are removed

Rollback:

1. restore the previous ops scripts/docs if needed

---

## Cutover Sequence

Recommended production order:

1. merge PR1 through PR5
2. verify staging on the target architecture:
   - Worker web on `agenttools.dev`
   - VM core on `api.agenttools.dev`
   - staging Neon namespace
   - staging R2 namespace
   - staging core on `127.0.0.1:50052`
3. verify staging end to end
4. merge PR6 and cut production directly to the target architecture:
   - Worker web on `gitslice.io`
   - VM core on `api.gitslice.io`
   - production Neon namespace
   - production R2 namespace
   - production core on `127.0.0.1:50051`
5. merge PR7 and remove old VM-web assumptions from ops/docs

---

## Success Criteria

The migration is complete when:

1. `agenttools.dev` exists as a staging environment with isolated infrastructure
2. `api.agenttools.dev` exists as the staging API origin with isolated infrastructure
3. `gitslice.io` is served from a Cloudflare Worker
4. `api.gitslice.io` serves public gRPC and `/v1/*` from the current VM origin
5. the Go core uses separate Neon namespaces for staging and production
6. the VM no longer needs to run the public web app in production
7. staging and production object blobs are isolated in separate R2 namespaces
8. object blobs are served from R2 with no user-visible API change
9. auth flows, CLI flows, and browser flows all continue to work
10. no compatibility requirement remains for the pre-cutover production web deployment
11. the VM is no longer responsible for production blob durability
12. staging and production core instances coexist on the same VM with separate local ports and no process or upstream collisions

---

## Risks

### Highest-risk items

1. web auth runtime portability to Workers
2. cross-host cookie and redirect behavior between `gitslice.io` and `api.gitslice.io`
3. Neon connection tuning under real production concurrency
4. R2 data migration correctness during staging and production cutover
5. stale production assumptions in current PM2/Nginx/docs scripts
6. shared-VM misconfiguration that points both hostnames at the same local core listener

### Lower-risk items

1. core behavior on Neon itself, because the app already targets PostgreSQL
2. preserving public gRPC, because the VM + Nginx origin path stays in place
3. Worker deployment plumbing, because the route/runtime shape is already close to Worker-ready

---

## Recommended Defaults

If we choose explicit defaults in follow-up PRs, the recommended production defaults are:

1. keep `api.gitslice.io` as the CLI and public API origin
2. keep the core on the VM behind Nginx on `443`
3. use Neon direct PostgreSQL connection for the long-running Go core
4. reserve pooled/HTTP-style Neon access for future serverless jobs only if needed
5. use R2 as the object store for both staging and production
6. do not leave production coupled to VM-local filesystem blobs
7. keep `agenttools.dev` as a permanent staging namespace, not a temporary alias for production
8. prefer direct cutover over compatibility layers because there is no production traffic to preserve
9. use separate local core ports on the shared VM:
   - production `127.0.0.1:50051`
   - staging `127.0.0.1:50052`

---

## Sources

1. Cloudflare Workers Wrangler config and assets: https://developers.cloudflare.com/workers/wrangler/configuration/
2. Cloudflare gRPC requirements: https://developers.cloudflare.com/network/grpc-connections/
3. Neon connection pooling: https://neon.com/docs/connect/connection-pooling
4. Neon compute lifecycle: https://neon.com/docs/introduction/compute-lifecycle
