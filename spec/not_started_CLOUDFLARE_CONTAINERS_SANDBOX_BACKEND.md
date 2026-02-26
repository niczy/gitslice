# Cloudflare Containers as Alternative Sandbox Backend

## Implementation Status

- Current status: `ongoing`
- Last updated: `2026-02-26`

PR checklist:

- [x] PR1 - Provider model + config groundwork (no runtime cutover)
- [x] PR2 - Cloudflare runtime provider implementation (start/stop/health)
- [ ] PR3 - Edge control plane (Worker + Durable Object session actor)
- [ ] PR4 - Runtime bridge protocol wiring (core <-> edge <-> container shim)
- [ ] PR5 - Environment admin + validation UX
- [ ] PR6 - Integration tests (stub + optional real CF smoke test)
- [ ] PR7 - Controlled rollout and documentation/runbook updates

---

## Executive Summary

Add Cloudflare Containers as a second sandbox backend alongside E2B so environments can choose either provider at runtime.

This is an additive design:

1. Keep current E2B behavior and API compatibility.
2. Extend environment/provider resolution to support `cloudflare_containers`.
3. Route Cloudflare runtime control through a dedicated Worker + Durable Object control plane because Cloudflare Containers do not expose direct inbound runtime endpoints.

The rollout is feature-flagged and reversible by environment configuration.

---

## Why This Change

1. Reduce dependency on a single sandbox provider.
2. Improve portability of the `environment` abstraction.
3. Enable edge-native runtime placement where Cloudflare infrastructure is preferred.

---

## Research Baseline (As of 2026-02-26)

This plan is based on Cloudflare Containers docs/changelog reviewed on `2026-02-26`.

Key platform constraints that shape this design:

1. Containers are still Beta and require paid Workers usage.
2. Request path is Worker -> Durable Object -> Container (not direct public container ingress).
3. First deployment and cold starts can be non-trivial; startup latency must be budgeted.
4. Container disk is ephemeral; persistent state must remain in gitslice storage/object store.
5. Built-in autoscaling/routing is still evolving; plan assumes explicit routing/control.

Before implementation starts, re-verify docs/changelog because these constraints are likely to change.

---

## Current gitslice Runtime Reality

Relevant current behavior in this repo:

1. Agent sessions resolve environment -> provider/provider_id in `services/agent/server.go`.
2. `agentsession.Service` currently accepts only `provider == "e2b"` in `CreateSession`.
3. Runtime provider interface exists (`Start`, `Stop`, `HealthCheck`) in `internal/agentsession/runtime_provider.go`.
4. E2B provider implementation exists in `internal/agentsession/e2b_runtime_provider.go`.
5. Core server wires E2B provider from env config in `servers/core/main.go`.

This gives a clean insertion point for a second provider.

---

## Target Outcome

### Functional

1. Environments can be configured with `provider: cloudflare_containers`.
2. Session creation for those environments starts/stops runtime instances on Cloudflare Containers.
3. Web client behavior remains unchanged from current API contract.
4. Existing E2B environments continue to work unchanged.

### Non-Functional

1. No persistent runtime dependency on container-local disk.
2. Provider startup failures are surfaced via structured runtime error codes.
3. Health endpoints can distinguish E2B and Cloudflare runtime readiness.

---

## Scope and Non-Goals

## In Scope

1. Cloudflare provider integration for agent session lifecycle.
2. Provider selection via existing environment abstraction.
3. Runtime control-plane integration via Worker/DO.
4. Documentation, runbook, and test updates.

## Non-Goals (This Phase)

1. Replacing existing E2B backend.
2. Introducing direct browser access to Cloudflare Worker/Container runtime endpoints.
3. Persisting runtime filesystem state across sessions.
4. Full autoscaling orchestration across providers.

---

## Proposed Architecture

## Control Plane

1. Browser talks only to gitslice core as today.
2. Core selects runtime provider based on resolved environment.
3. For Cloudflare environments, core calls a Cloudflare edge control API (Worker endpoint) using service credentials.
4. Worker delegates to a Durable Object session actor.
5. Durable Object creates/manages the container instance and runtime shim process.

## Runtime I/O Plane

1. Core remains the source of truth for session events persisted in gitslice storage.
2. Core receives runtime events from Cloudflare edge control stream (WS/SSE) and appends them via existing event pipeline.
3. Core forwards user input/interrupt control messages to edge control API for delivery to the container shim.

## Security Boundaries

1. Browser never receives Cloudflare account-level credentials.
2. Core <-> Worker calls require service auth (Cloudflare Access service token or signed internal JWT).
3. Runtime secrets for model providers are injected server-side and scoped per session.

---

## Data Model and API Changes

## Environment Model

Current model has `provider`, `provider_id`, `region`.

Plan:

1. Support `provider = "cloudflare_containers"` in validation.
2. Keep `provider_id` as logical profile key (for backward compatibility and simple routing).
3. Add optional structured provider config for non-E2B backends:
   - migration: `environments.provider_config_json JSONB NOT NULL DEFAULT '{}'::jsonb`
   - model: `ProviderConfig map[string]any`

Suggested Cloudflare config keys:

1. `worker_base_url` (required)
2. `container_class` (required)
3. `instance_type` (required)
4. `runtime_stream_path` (optional, defaulted)
5. `startup_timeout_sec` (optional override)
6. `colo_hint` (optional)

## Agent Session Model

Reuse existing runtime metadata fields:

1. `runtime_provider` -> `cloudflare_containers`
2. `runtime_session_id` -> DO/container runtime id
3. `runtime_endpoint` -> edge stream endpoint (internal)
4. `runtime_status`, `runtime_error_code` as-is

No user-facing API contract break required.

## Proto/API Surface

Keep existing `AgentService` requests/responses; implementation change is provider support.

Optional additive enhancement:

1. Extend `ListCapabilitiesResponse` with runtime backend hints:
   - `repeated string supported_runtime_providers`
   - or keep this internal for first phase.

Admin/Environment APIs:

1. Add provider config fields through gRPC `EnvironmentInfo` and request messages.
2. Preserve existing fields for compatibility.

---

## Core Runtime Provider Design

Add `internal/agentsession/cloudflare_runtime_provider.go`.

`Start(session)` behavior:

1. Validate session + environment/provider config.
2. Build signed start request to Worker control API.
3. Include:
   - session identifiers
   - selected agent type
   - runtime TTL/idle limits
   - env vars required by runtime shim
4. Parse response:
   - runtime session id
   - stream endpoint
   - status
5. Return `RuntimeStartResult{Provider: "cloudflare_containers", ...}`.

`Stop(session, reason)` behavior:

1. Call Worker control API to stop session actor/container.
2. Treat not-found as idempotent success.
3. Map Cloudflare/API errors to stable runtime error codes.

`HealthCheck()` behavior:

1. Verify control API reachability and auth.
2. Verify required credentials/config present.
3. Return provider-specific `RuntimeError` code on failures.

Error code examples:

1. `CFC_AUTH_MISSING`
2. `CFC_START_REQUEST_FAILED`
3. `CFC_START_TIMEOUT`
4. `CFC_RUNTIME_UNAVAILABLE`
5. `CFC_STOP_FAILED`

---

## Edge Control Plane Design (Worker + Durable Object)

### Worker endpoints (internal-only)

1. `POST /internal/runtime/sessions` -> create/start runtime
2. `POST /internal/runtime/sessions/{id}/input` -> forward input
3. `POST /internal/runtime/sessions/{id}/interrupt` -> interrupt runtime
4. `DELETE /internal/runtime/sessions/{id}` -> stop runtime
5. `GET /internal/runtime/sessions/{id}/health` -> runtime health/status
6. `GET /internal/runtime/sessions/{id}/stream` -> event stream (WS/SSE)

### Durable Object responsibilities

1. Single-session actor for lifecycle serialization.
2. Start/monitor container instance.
3. Keep short in-memory event buffer for reconnect windows.
4. Proxy runtime shim messages between container and core.
5. Enforce TTL + idle expiration.

### Container runtime shim requirements

1. Existing normalized event envelope contract retained.
2. Supports `input` and `interrupt` control.
3. Emits lifecycle + output + tool + error events.
4. Runs codex/claude binaries inside container image as configured.

---

## Configuration Plan

Add core config fields (example names):

1. `AGENT_RUNTIME_PROVIDER_DEFAULT` (optional)
2. `CFC_CONTROL_BASE_URL`
3. `CFC_CONTROL_AUDIENCE`
4. `CFC_SERVICE_TOKEN_ID`
5. `CFC_SERVICE_TOKEN_SECRET`
6. `CFC_REQUEST_TIMEOUT_SEC`

Behavior:

1. Provider selected per environment first.
2. Core only initializes Cloudflare provider if required config is present.
3. Health endpoint reports provider readiness independently.

---

## Testing Strategy

## Unit

1. Cloudflare provider start/stop/health success + error mapping.
2. Environment provider validation including `cloudflare_containers`.
3. Provider config parsing and defaults.

## Integration (Repo-local, deterministic)

1. Use local fake Worker server to simulate edge control API.
2. Verify:
   - session transitions `creating -> starting -> running`
   - stop transitions and idempotency
   - event append/replay behavior via existing APIs

## Optional External Smoke (gated)

1. Nightly or manual test against real Cloudflare account with dedicated environment.
2. Not required for every PR (to keep CI stable and cost-bounded).

---

## Rollout Plan

### Phase 0 - Preparation

1. Add provider constants, config parsing, model/proto migration scaffolding.
2. No behavior change for existing environments.

### Phase 1 - Dark Launch

1. Merge Cloudflare provider behind feature flag.
2. Deploy Worker/DO control plane.
3. Add one non-prod environment profile using `cloudflare_containers`.

### Phase 2 - Controlled Usage

1. Enable only for selected slices/users.
2. Monitor startup latency, failure rates, and session event completeness.
3. Keep E2B fallback as default.

### Phase 3 - General Availability Candidate

1. Remove feature flag restrictions.
2. Keep rollback route: switch environment provider back to E2B.
3. Finalize operational docs and on-call runbook.

Rollback strategy:

1. No schema-destructive rollback required.
2. Operational rollback is configuration-level:
   - set environment provider back to `e2b`
   - disable Cloudflare provider initialization.

---

## Observability and SLOs

Add provider-labeled metrics:

1. `agent_runtime_start_total{provider,result}`
2. `agent_runtime_start_duration_seconds{provider}`
3. `agent_runtime_stop_total{provider,result}`
4. `agent_runtime_active_sessions{provider}`
5. `agent_runtime_event_drop_total{provider}`

Initial SLO targets for Cloudflare backend:

1. p95 startup time under 30s (beta-aware target)
2. start failure rate under 2% excluding user/config errors
3. stop success rate above 99%

---

## Risks and Mitigations

1. Beta platform changes
   - Mitigation: feature flag, provider abstraction, explicit fallback to E2B.
2. Cold-start and first-deploy latency
   - Mitigation: startup timeout tuning, user-facing state messaging, warm-up jobs.
3. No persistent container disk
   - Mitigation: keep source of truth in gitslice storage/object store only.
4. Edge control-plane complexity
   - Mitigation: strict API contract tests and local fake Worker integration tests.
5. Secret handling across systems
   - Mitigation: service-to-service auth + short-lived session-scoped secret injection.

---

## Documentation Updates Required

In same implementation track:

1. `README.md` runtime backend configuration section.
2. `README.md` environment examples showing provider selection.
3. Operational runbook for Cloudflare Worker/DO deployment and health checks.
4. Troubleshooting matrix for provider-specific runtime error codes.

---

## Acceptance Criteria

1. Session create/stop works with `cloudflare_containers` environments end-to-end.
2. Existing E2B session behavior remains unchanged.
3. Runtime events still flow through current `/v1/agent-sessions/...` and `/ws/sessions/...` surfaces.
4. Provider-specific health checks and metrics are available.
5. Integration tests cover success and failure paths for both providers.

---

## Open Questions

1. Should `provider_config_json` be exposed to non-admin callers at all, or internal only?
2. Do we require strict per-session model credential injection, or allow worker-bound static secrets for first release?
3. Should Cloudflare edge control live in this repo (under `servers/`), or in a separate deployable repo?
4. What are acceptable startup latency thresholds for user experience before forcing fallback?
5. Should runtime provider be selectable per session request, or strictly environment-driven?
