# Cloudflare Migration Plan

## Implementation Status

- Current status: `not started`
- Last updated: `2026-03-06`

PR checklist:

- [ ] PR1 - Target topology, config surface, and migration guardrails
- [ ] PR2 - R2 object-store adapter and copy/verify tooling
- [ ] PR3 - D1 portability prep: schema normalization and storage contract hardening
- [ ] PR4 - Internal metadata Worker backed by D1
- [ ] PR5 - Core dual-write and D1 read canary
- [ ] PR6 - Cloudflare-hosted core ingress with TLS gRPC/gateway support
- [ ] PR7 - Web app conversion from Vite SPA to Next-compatible app
- [ ] PR8 - vinext migration and Worker deployment for the web app
- [ ] PR9 - CI/CD, secrets, observability, and runbook updates
- [ ] PR10 - Data backfill, canary cutover, cleanup, and rollback hardening

## Executive Summary

The target state is:

1. `agenttools.dev` served by a Cloudflare Worker running the web app.
2. Blob/object payloads stored in R2.
3. SQL metadata moved from PostgreSQL to D1.
4. Existing gRPC-first API contracts preserved during the migration.

This should not be a big-bang rewrite. The repo is not Cloudflare-native today:

1. `web/` is a Vite SPA, not a Next.js app, so vinext is not a package swap yet.
2. `internal/storage/postgres_native.go` is tightly coupled to PostgreSQL features.
3. Production boot still assumes PM2 + Nginx + a locally built Vite preview.

The low-risk path is:

1. Move object storage to R2 first.
2. Build a D1 path behind a dedicated Worker and dual-write before cutover.
3. Convert the web app to a Next-compatible structure.
4. Adopt vinext only after the web app is Next-compatible.

## Current Repo Reality

Relevant facts from the current codebase:

1. `web/package.json` is Vite + React, not Next.
2. `web/vite.config.js` contains custom Auth.js middleware and `/v1` proxying.
3. `web/src/App.jsx` is a hash-routed SPA with a single client entrypoint.
4. `ops/start_web_server.sh` builds the web bundle, starts `core_server`, then runs `vite preview`.
5. `ops/ecosystem.config.cjs` and `ops/nginx.conf` assume PM2 + Nginx on a single host.
6. `internal/storage/objectstore.go` already has a clean object-store abstraction.
7. `internal/storage/postgres_native.go` uses PostgreSQL-only features such as `JSONB`, `ILIKE`, `ANY(...)`, `FOR UPDATE`, `BIGSERIAL`, `search_path`, and `RETURNING`.
8. `servers/cloudflare_control_plane/` already proves the repo can ship Worker + Durable Object services with Wrangler.

## Locked Decisions

These decisions keep the migration tractable:

1. Keep the Go core during this migration. Do not rewrite the gRPC services into Worker code.
2. Use a Cloudflare Worker for the web app.
3. Use R2 for object/blob payloads and large snapshot artifacts.
4. Use D1 through a Worker-native access layer, not as a blind DSN swap in Go.
5. Preserve protobuf definitions and grpc-gateway HTTP routes as the public contract.

## Platform Constraints (Verified 2026-03-06)

The plan assumes these current Cloudflare constraints:

1. D1 is Worker-native, has a 10 GB per-database limit on paid plans, a 2 MB row/BLOB limit, and each database is single-threaded.
2. R2 is available both through Worker bindings and an S3-compatible API, which makes it suitable for direct use from the Go core.
3. Cloudflare proxied gRPC requires the origin to listen on port 443 with TLS and HTTP/2.
4. Cloudflare Access does not protect gRPC traffic when gRPC proxying is enabled, and Cloudflare Tunnel public hostnames are not the supported path for public gRPC.
5. Cloudflare Workers can serve static assets and dynamic code from the same deployment, which is the right fit for a vinext-hosted web app.

These constraints make D1 the highest-risk part of the migration and justify a separate portability track before cutover.

## Target Architecture

### Public topology

1. `agenttools.dev`
   - Cloudflare Worker running the vinext web app.
   - Owns web rendering, static assets, and `/auth/*`.
2. `api.agenttools.dev`
   - Cloudflare-proxied origin for the Go core.
   - Serves grpc-gateway `/v1/*` and, initially, the existing gRPC service paths.
3. `internal-metadata.<workers.dev or private route>`
   - Internal Worker with D1 binding.
   - Protected by service token or another machine-to-machine auth layer.
   - Called by the Go core for D1-backed metadata operations during migration.

### Data topology

1. R2
   - File blobs
   - Commit snapshots too large for D1
   - Export/manifests used for migration or audit
2. D1
   - Slice metadata
   - File indexes
   - Changesets, commits, environments, auth/session metadata
   - Agent session state and event metadata, unless canary testing shows that runtime hot paths need a second D1 database

### Operational topology

1. PM2/Nginx remain only until the Cloudflare origin path is proven.
2. The old Postgres path stays available until D1 parity and rollback tooling are complete.
3. R2 cutover can happen before D1 cutover.

## PR-by-PR Plan

### PR1 - Target topology, config surface, and migration guardrails

Goal: make the migration explicit in code and docs without changing runtime behavior.

Changes:

1. Add Cloudflare deployment terminology and env naming to `README.md`, `ops/.env.example`, and this spec.
2. Define target backend switches in config:
   - `OBJECT_STORE_TYPE=r2`
   - `METADATA_BACKEND=postgres|d1_worker`
   - `CLOUDFLARE_ACCOUNT_ID`, `R2_BUCKET`, `D1_DATABASE_ID`, Worker base URLs, service-token secrets
3. Document hostnames and cutover sequence:
   - `agenttools.dev` for web Worker
   - `api.agenttools.dev` for Go core
   - internal metadata Worker route
4. Add missing contract tests where current storage semantics are under-specified.

Acceptance:

1. No behavior change in production.
2. CI remains green.
3. Migration env surface is stable enough for follow-up PRs.

Rollback:

1. Revert config/docs only.

### PR2 - R2 object-store adapter and copy/verify tooling

Goal: move blobs off filesystem/GCS without touching SQL yet.

Changes:

1. Implement `R2ObjectStore` under `internal/storage/` using the S3-compatible R2 API.
2. Extend `internal/config/config.go` and `servers/core/main.go` to support `OBJECT_STORE_TYPE=r2`.
3. Add a copy tool and verification command:
   - filesystem -> R2
   - GCS -> R2
4. Add object-store parity tests covering:
   - put/get/delete
   - missing-object semantics
   - concurrent writes to the same key
5. Update docs and ops defaults to allow canary R2 usage before D1 migration.

Acceptance:

1. Existing Go tests pass.
2. New object-store tests pass for in-memory, filesystem, and R2-backed adapters.
3. A canary environment can read/write blobs from R2 with no API changes.

Rollback:

1. Switch `OBJECT_STORE_TYPE` back to `filesystem` or `gcs`.
2. Keep copied R2 data; do not delete on rollback.

### PR3 - D1 portability prep: schema normalization and storage contract hardening

Goal: remove PostgreSQL assumptions that will block D1.

Changes:

1. Audit every PostgreSQL-only usage in `internal/storage/postgres_native.go`.
2. Normalize schema where D1/SQLite portability demands it:
   - replace array/`JSONB` queries used as indexes with explicit tables where needed
   - replace `ILIKE` search patterns with portable search strategy
   - replace `BIGSERIAL` assumptions with explicit ids or SQLite-compatible sequences
   - remove namespace/search-path dependence
3. Add a dialect-neutral storage contract test suite that future D1 implementations must pass.
4. Keep current Postgres implementation authoritative, but refactor SQL-building seams so a D1 backend is realistic.
5. Add data export tooling from Postgres to a portable backfill format.

Acceptance:

1. No user-visible behavior change.
2. Storage contract tests pass against current in-memory and Postgres implementations.
3. The D1 schema can be expressed without hidden Postgres-only dependencies.

Rollback:

1. Pure refactor rollback if needed.

### PR4 - Internal metadata Worker backed by D1

Goal: introduce a Cloudflare-native SQL layer without moving public API traffic yet.

Changes:

1. Add a new Worker service for metadata access with:
   - D1 binding
   - migration runner
   - service-to-service auth
2. Implement the first internal endpoints for storage operations needed by the Go core.
3. Mirror current storage invariants in the Worker tests.
4. Add local development workflow with Wrangler and local/remote D1 bindings.
5. Keep the Worker internal-only; no browser traffic goes here directly.

Acceptance:

1. Worker tests pass against local D1.
2. A seeded D1 database can serve representative read/write flows from automated tests.
3. Service auth blocks unauthenticated access.

Rollback:

1. Leave the Worker undeployed or unused.
2. Postgres remains the only authoritative store.

### PR5 - Core dual-write and D1 read canary

Goal: prove parity before switching authority.

Changes:

1. Add a `d1_worker` metadata backend path in the Go core.
2. Start with dual-write:
   - Postgres remains authoritative
   - writes also flow to the metadata Worker/D1
3. Add parity verification:
   - slice counts
   - commit counts
   - environment records
   - auth sessions
   - agent sessions/events
4. Add a read-canary flag for selected endpoints or selected users.
5. Emit metrics for:
   - D1 latency
   - D1 overload errors
   - parity mismatches
   - replication lag/backfill lag

Acceptance:

1. `make test` passes.
2. Integration tests pass with dual-write enabled.
3. Parity reports are clean in staging or canary.

Rollback:

1. Disable dual-write/read-canary flags.
2. Keep Postgres authoritative.

### PR6 - Cloudflare-hosted core ingress with TLS gRPC/gateway support

Goal: move the public API entrypoint behind Cloudflare without breaking CLI or web clients.

Changes:

1. Deploy the Go core to a Cloudflare-reachable origin that supports:
   - TLS on 443
   - HTTP/2
   - gRPC and grpc-gateway on the same public hostname, or a split hostname if needed
2. Remove the production dependency on local Vite preview for public traffic.
3. Replace Nginx/PM2 assumptions in docs and ops scripts with Cloudflare-aware runbooks.
4. Add direct smoke tests for:
   - `GET /health`
   - `GET /v1/global/state`
   - CLI gRPC calls over the Cloudflare-proxied hostname
5. Document the gRPC auth model explicitly because Cloudflare Access is not the control point here.

Acceptance:

1. CLI works against the Cloudflare-proxied gRPC host.
2. Web/API calls work through Cloudflare.
3. Existing rollback path can switch traffic back to the old origin.

Rollback:

1. Point DNS/routes back to the old origin.
2. Keep old PM2/Nginx deployment available until Cloudflare origin stability is proven.

### PR7 - Web app conversion from Vite SPA to Next-compatible app

Goal: make the current web app eligible for vinext.

Changes:

1. Convert `web/` from a Vite-only SPA to a minimal Next-compatible app.
2. Keep the current UI and behavior as-is during this PR.
3. Move Auth.js handling out of `vite.config.js` into route handlers or API routes.
4. Replace the current build/dev/test scripts so the app can run under Next semantics.
5. Keep client-heavy rendering initially; do not attempt a full routing redesign in the same PR.

Notes:

1. This repo is not currently a Next.js app, so the `migrate-to-vinext` workflow cannot be applied directly yet.
2. The purpose of this PR is to create that baseline with minimal product churn.

Acceptance:

1. Existing Playwright coverage still passes.
2. OAuth and username sign-in still work.
3. The app can run in a Next-compatible local dev/build flow before vinext is introduced.

Rollback:

1. Revert to the existing Vite app if parity is not met.

### PR8 - vinext migration and Worker deployment for the web app

Goal: ship the web app on Cloudflare Workers.

Changes:

1. Run `vinext check` and address compatibility findings.
2. Migrate the Next-compatible web app to vinext.
3. Add Wrangler config, Worker build scripts, and environment bindings for the web app.
4. Deploy static assets and dynamic routes through the Worker.
5. Point `agenttools.dev` to the new Worker after staging validation.

Acceptance:

1. `vinext` dev/build/start flows work locally.
2. Preview deployment works on Cloudflare Workers.
3. Web smoke tests pass against the Worker-hosted app.

Rollback:

1. Restore the previous web deployment target while keeping the vinext branch deployable for follow-up fixes.

### PR9 - CI/CD, secrets, observability, and runbook updates

Goal: make Cloudflare deployment operationally safe.

Changes:

1. Add GitHub Actions jobs for:
   - Worker preview deploys
   - production deploys
   - D1 migrations
   - R2 validation/smoke checks
2. Define secret management for:
   - Wrangler auth
   - R2 credentials used by Go core
   - internal metadata Worker service auth
   - OAuth secrets for the web Worker
3. Add dashboards and alerts for:
   - Worker deploy failures
   - D1 overloads and latency
   - R2 errors
   - parity drift
4. Update `README.md`, `ops/restart_all.sh`, `ops/start_web_server.sh`, `ops/ecosystem.config.cjs`, and `ops/nginx.conf` to reflect the new reality or clearly mark them as legacy.

Acceptance:

1. PR previews deploy automatically.
2. Production deployment is documented and repeatable.
3. On-call documentation includes rollback steps for web, API, D1, and R2.

Rollback:

1. Keep existing deployment workflows until the new CI/CD path is trusted.

### PR10 - Data backfill, canary cutover, cleanup, and rollback hardening

Goal: move authority to Cloudflare-managed services and remove the old stack safely.

Changes:

1. Backfill all blobs to R2 and all metadata to D1.
2. Run parity verification until clean.
3. Enable D1 reads for canary traffic first.
4. Cut `agenttools.dev` to the Worker-hosted web app.
5. Cut core metadata authority from Postgres to D1 only after canary stability.
6. Keep dual-write for a fixed stabilization window.
7. Remove or archive legacy Postgres/GCS/filesystem/Nginx/PM2 assumptions only after the stabilization window closes.

Acceptance:

1. Web smoke tests pass on the Worker deployment.
2. CLI gRPC traffic remains healthy through Cloudflare.
3. No parity drift remains after final backfill.
4. Rollback has been exercised at least once in staging.

Rollback:

1. Switch reads back to Postgres.
2. Keep R2 data in place.
3. Point `agenttools.dev` back to the prior web target if needed.
4. Keep D1 dual-write disabled until the failure is understood.

## Recommended Cutover Order

1. Land config and guardrails.
2. Move blobs to R2.
3. Make SQL portable.
4. Build D1 Worker and dual-write path.
5. Move the public API origin behind Cloudflare.
6. Convert the web app to Next-compatible structure.
7. Migrate the web app to vinext and deploy it on Workers.
8. Cut reads from Postgres to D1.
9. Remove the old hosting assumptions only after a stable soak period.

## Main Risks

1. D1 throughput may be insufficient if the current metadata/event write profile is too hot for a single database.
2. Some current Postgres query patterns may need schema changes, not just SQL rewrites.
3. Public gRPC on Cloudflare requires stricter origin/TLS handling than the current Nginx + h2c setup.
4. The web migration is a two-step process because the current app is not already a Next.js app.

## Exit Criteria

The migration is complete only when all of the following are true:

1. `agenttools.dev` is served by a Worker-hosted vinext app.
2. Blob payloads are stored in R2.
3. Metadata reads and writes are served from D1 through the supported Cloudflare path.
4. CLI gRPC and `/v1/*` HTTP endpoints remain functional.
5. Legacy PM2/Nginx/Postgres-only production assumptions have been removed from docs and runbooks.

## Sources

1. Cloudflare D1 limits: https://developers.cloudflare.com/d1/platform/limits/
2. Cloudflare D1 Worker Binding API: https://developers.cloudflare.com/d1/worker-api/
3. Cloudflare R2 Workers API: https://developers.cloudflare.com/r2/api/workers/workers-api-reference/
4. Cloudflare R2 S3-compatible upload guidance: https://developers.cloudflare.com/r2/objects/upload-objects/
5. Cloudflare gRPC requirements and limitations: https://developers.cloudflare.com/network/grpc-connections/
6. Cloudflare Tunnel gRPC limitation for public hostnames: https://developers.cloudflare.com/cloudflare-one/faq/cloudflare-tunnels-faq/
7. Cloudflare Workers static assets bindings: https://developers.cloudflare.com/workers/static-assets/binding/
