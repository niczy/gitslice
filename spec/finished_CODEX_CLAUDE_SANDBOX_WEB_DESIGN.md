# Codex/Claude Sandbox Web Session Design

## Implementation Status

- Current status: `finished`
- Last updated: `2026-02-22`

PR checklist:

- [x] PR1 - Proto + contract baseline for model-aware sessions
- [x] PR2 - Storage + model schema extensions (`agent_type`, provider run metadata)
- [x] PR3 - Session service refactor: real runtime lifecycle interface (replace fake bootstrap)
- [x] PR4 - Sandbox runtime bridge service (E2B-backed) and WS relay integration
- [x] PR5 - Codex runtime adapter in sandbox
- [x] PR6 - Claude runtime adapter in sandbox
- [x] PR7 - Web UI integration (replace mock AgentSession with backend sessions)
- [x] PR8 - Security hardening + policy controls
- [x] PR9 - Observability + reliability controls
- [x] PR10 - Integration/E2E hardening + rollout + mark spec finished

## Executive Summary

This spec defines how gitslice users can run **Codex** or **Claude** inside isolated sandboxes while interacting entirely through the gitslice web interface.

The design builds on existing agent session foundations already in this repo:

- gRPC/gateway APIs exist at `/v1/agent-sessions` (`proto/agent/agent_service.proto`).
- Session persistence and lifecycle state machine exist (`internal/agentsession/service.go`, `internal/storage/migrations/002_agent_sessions.sql`).
- WebSocket endpoint exists (`/ws/sessions/{session_id}`) with token auth (`internal/httpapi/agent_session_ws.go`).
- Environment abstraction exists (`/v1/environments`, `/v1/slices/{id}/environment`) for runtime mapping.

Current behavior is simulated (mock events and echo), not real sandbox runtime execution. This spec replaces simulation with production runtime plumbing while preserving current API style and rollout safety.

### Integration Decision (Locked)

Codex and Claude integration for this design is **CLI-only in v1**:

1. Run vendor CLI binaries inside the sandbox (`codex` and `claude`).
2. Do not call vendor HTTP APIs directly from core server in v1.
3. Use a sandbox-local runtime shim to normalize CLI IO/events into gitslice WS event envelopes.

## Current State (Code Reality)

### What already exists

1. **Control-plane API**
- Create/get/stop/token/events session APIs are implemented via gRPC + grpc-gateway (`services/agent/server.go`, `proto/agent/agent_service.proto`).

2. **Session persistence and lifecycle**
- `agent_sessions`, `agent_session_events`, `agent_session_audit` tables and storage methods already exist (`internal/storage/migrations/002_agent_sessions.sql`).
- Lifecycle loop exists (creating/starting/running/idle/stopping/stopped/failed) (`internal/agentsession/service.go`).

3. **WS client path**
- Browser connects to `/ws/sessions/{session_id}` with short-lived JWT token.
- Replay, backpressure, and nonce anti-replay are already implemented (`internal/httpapi/agent_session_ws.go`, `internal/agentsession/service.go`).

4. **Environment abstraction**
- Slice/session environment resolution exists (`internal/httpapi/agent_sessions.go`, `services/agent/server.go`, `internal/storage/environments.go`).

### Key gaps

1. **No real sandbox orchestration in active runtime path**
- Session startup currently sets `RuntimeEndpoint = runtime://{session_id}` and transitions state with delays.

2. **No CLI runtime adapter**
- No concrete runtime adapter for Codex/Claude CLI execution, streaming, tool-call handling, or structured output.

3. **WS is loopback simulation**
- `pty/stdin` currently appends synthetic `pty/stdout` events in-process.

4. **Web UI uses mock session data**
- `web/src/App.jsx` and `web/src/components/AgentSession.jsx` create fake provider sessions and fake terminal output.

5. **User-facing session proto still has provider/E2B fields**
- `CreateSessionRequest/Response` and `GetSessionResponse` still expose provider internals.

## Goals

1. Let users start a session in the web app and choose `codex` or `claude`.
2. Run the selected agent inside an isolated sandbox mapped from environment settings.
3. Keep users on gitslice UI only; no direct sandbox endpoint exposure.
4. Preserve reconnect/replay semantics on websocket interruptions.
5. Keep APIs gRPC-first and gateway-exposed.
6. Keep rollout safe with feature flags and fallback paths.

## Non-Goals

1. Building a brand-new multi-cloud sandbox platform (v1 uses current environment/provider stack).
2. Full cross-provider abstraction for every model vendor in v1 (only Codex + Claude required).
3. Billing/seat management.
4. Replacing existing slice/file workflows.

## Product and UX Requirements

1. User can start session from slice context with:
- `sliceId`
- `environment`
- `agentType` (`codex` or `claude`)

2. Session panel shows:
- live streaming assistant output
- command/tool output
- state transitions
- explicit stop/retry controls

3. User can reconnect without losing context:
- reconnect token mint
- event replay by sequence

4. Failure modes are user-visible and actionable:
- startup timeout
- auth/credentials missing
- sandbox unavailable
- model provider unavailable

## High-Level Architecture

### Components

1. **Agent API service (existing, extended)**
- Owns session CRUD, token minting, event listing.
- Resolves environment and validates allowed agents.

2. **Session runtime manager (new internal package)**
- Replaces fake bootstrap/finalize with real runtime state transitions.
- Starts/stops runtime bridge attached to sandbox.

3. **Sandbox runtime bridge (new)**
- Connects core server WS plane to runtime process in sandbox.
- Converts WS frames <-> runtime protocol.

4. **Runtime shim inside sandbox (new)**
- Runtime process that launches vendor CLIs as subprocesses:
  - `codex` CLI adapter
  - `claude` CLI adapter
- Emits normalized event envelopes for UI.

5. **Environment registry (existing, extended semantics)**
- Defines sandbox template/provider mapping.
- Defines allowed/default agents per environment.

6. **Web app (existing, replace mock logic)**
- Session list and active session panel backed by `/v1/agent-sessions` and `/ws/sessions/*`.

### Trust boundaries

1. Browser never gets sandbox direct URL or provider keys.
2. Provider credentials are injected server-side into runtime launch, scoped per session.
3. WebSocket token remains short-lived and one-time (existing anti-replay is reused).

## API and Proto Design

### Agent service changes (`proto/agent/agent_service.proto`)

#### Request/response model

1. Add `agent_type` to create request:

```protobuf
message CreateSessionRequest {
  string slice_id = 1;
  string environment = 2;
  string agent_type = 3; // "codex" | "claude"
  int32 idle_timeout_sec = 4;
  int32 ttl_sec = 5;
  map<string, string> env = 6;
}
```

2. Make session response user-facing and provider-agnostic:

```protobuf
message CreateSessionResponse {
  string session_id = 1;
  string slice_id = 2;
  string environment = 3;
  string agent_type = 4;
  string state = 5;
  WSConnectInfo ws = 6;
  string created_at = 7;
  int32 idle_timeout_sec = 8;
  int32 ttl_sec = 9;
}
```

3. Align `GetSessionResponse` similarly:

```protobuf
message GetSessionResponse {
  string session_id = 1;
  string slice_id = 2;
  string environment = 3;
  string agent_type = 4;
  string state = 5;
  string last_activity_at = 6;
  int32 idle_timeout_sec = 7;
  int32 ttl_sec = 8;
  string created_at = 9;
  RuntimeInfo runtime = 10; // internal caller only
}
```

4. Add capabilities endpoint:

```protobuf
rpc ListCapabilities(ListCapabilitiesRequest) returns (ListCapabilitiesResponse) {
  option (google.api.http) = {
    get: "/v1/agent-sessions/capabilities"
  };
}
```

Capabilities include:
- supported `agent_types`
- environment-specific allowed agents
- runtime limits (max session TTL, file size, tool policy flags)

### Admin environment proto changes (`proto/admin/admin_service.proto`)

Extend `EnvironmentInfo` with optional agent policy fields:

- `default_agent_type`
- `repeated allowed_agent_types`
- `map<string,string> provider_config` (optional internal-only projection via internal caller)

HTTP shape remains `/v1/environments`.

## Data Model and Storage Design

### `agent_sessions` extensions

Add columns via new migration:

- `agent_type TEXT NOT NULL DEFAULT ''`
- `runtime_provider TEXT NOT NULL DEFAULT ''` (sandbox provider, e.g. `e2b`)
- `runtime_session_id TEXT NOT NULL DEFAULT ''` (sandbox ID / runtime handle)
- `runtime_status TEXT NOT NULL DEFAULT ''`
- `runtime_error_code TEXT NOT NULL DEFAULT ''`

Keep existing E2B columns for compatibility during rollout; remove only in follow-up migration after full cutover.

### `environments` extensions

Add columns via migration:

- `default_agent_type TEXT NOT NULL DEFAULT 'codex'`
- `allowed_agent_types_json JSONB NOT NULL DEFAULT '["codex","claude"]'::jsonb`

Validation rules:

1. `default_agent_type` must be in `allowed_agent_types`.
2. `allowed_agent_types` values must be known enum set.

## Runtime Lifecycle Design

### State machine (preserved, now real)

`creating -> starting -> running -> idle -> stopping -> stopped`

Failure transition:

`creating|starting|running|idle|stopping -> failed`

### Startup flow

1. API validates user/slice access and environment.
2. API validates selected `agent_type` against environment policy.
3. Session row inserted as `creating`.
4. Runtime manager requests sandbox start from environment provider config.
5. Runtime bridge dials sandbox runtime endpoint.
6. Runtime bridge sends `runtime.start` to sandbox shim with:
- session metadata
- selected agent type
- mount path / workspace context
- scoped env vars (non-secret) and secret references
7. Sandbox shim launches selected CLI binary (`codex` or `claude`) and emits `runtime.ready`.
8. On runtime ready signal, session becomes `running`.

### CLI runtime contract

1. For `agent_type=codex`, shim executes configured `codex` binary path from environment config.
2. For `agent_type=claude`, shim executes configured `claude` binary path from environment config.
3. Shim is responsible for:
- stdin/write, interrupt, and graceful shutdown handling
- stdout/stderr streaming
- mapping CLI-specific tool output into normalized `tool/*` events
4. Core server never executes vendor binaries directly; execution stays sandbox-local.

### Sandbox image/runtime requirements

1. Sandbox template must include:
- `codex` CLI binary
- `claude` CLI binary
- `agent-runtime` shim binary/service
2. Version pinning of CLI binaries must be explicit in environment/template metadata.
3. Missing binary must fail session startup with deterministic error codes:
- `AGENT_BINARY_MISSING`
- `AGENT_BINARY_EXEC_FAILED`

### Stop flow

1. User/API marks `stopping`.
2. Runtime manager sends `runtime.stop` and waits grace period.
3. If timeout, force terminate sandbox.
4. Persist terminal state `stopped` and audit event.

## WebSocket Protocol (Normalized Event Envelope)

Use existing envelope shape with standardized streams/types.

### Incoming (browser -> server)

1. `control/hello`
2. `control/ping`
3. `agent/input` payload:

```json
{"text":"Refactor this function and run tests"}
```

4. `agent/interrupt` payload:

```json
{"reason":"user_cancel"}
```

5. `pty/stdin` and `pty/resize` for terminal mode.

### Outgoing (server -> browser)

1. `status/state` (`creating|starting|running|idle|stopping|stopped|failed`)
2. `agent/output_delta` (token/delta stream)
3. `agent/output_final` (final assistant turn)
4. `tool/start`, `tool/output`, `tool/end`
5. `pty/stdout`
6. `control/error`

All frames continue carrying `seq` and `ts` for replay.

## Web App Integration Plan

### Replace mock session layer

1. `web/src/App.jsx`
- Replace `AGENT_PROVIDERS` mock workflow with capability-driven options from backend.
- Replace fake session creation with POST `/v1/agent-sessions`.

2. `web/src/components/AgentSession.jsx`
- Replace local fake line animation with WS stream consumption.
- Render event-driven transcript and terminal output.
- Add reconnect path using `/v1/agent-sessions/{id}/token` + `lastSeq`.

3. Session drawer behavior remains mostly unchanged (reuse UX).

### Backward-compatible rollout flag

- `WEB_AGENT_REAL_RUNTIME=0` -> legacy mock mode for local dev fallback.
- `WEB_AGENT_REAL_RUNTIME=1` -> real backend mode.

## Security Design

1. Provider credentials never sent to browser.
2. Credentials sourced from server env/secret manager and injected at runtime start.
  - Codex CLI credentials via sandbox env (example: `OPENAI_API_KEY`).
  - Claude CLI credentials via sandbox env (example: `ANTHROPIC_API_KEY`).
3. WS token remains single-use and short TTL (existing logic retained).
4. Runtime egress policy:
- allow: provider API domains required by Codex/Claude CLIs, git hosts as needed
- deny by default for all other outbound destinations
5. Audit every:
- session create/stop
- agent type selection
- runtime start/stop/failure
- interrupt and error events

## Reliability and Observability

### Metrics

1. Session lifecycle:
- `agent_session_create_total{agent_type,state}`
- `agent_session_start_latency_seconds`
- `agent_session_runtime_fail_total{code}`

2. WS channel:
- `agent_ws_connect_total`
- `agent_ws_replay_gap_total`
- `agent_ws_backpressure_close_total`

3. Runtime CLI adapters:
- `agent_runtime_request_total{agent_type,outcome}`
- `agent_runtime_token_out_total{agent_type}`

### Logs

Structured logs with:
- `session_id`, `slice_id`, `user_id`, `environment`, `agent_type`, `state`, `failure_code`.

## Testing Strategy

### Unit tests

1. Environment + agent policy resolution.
2. Session create validation for invalid/missing agent type.
3. Runtime manager transitions (success, timeout, provider error, forced stop).
4. WS protocol mapping and replay semantics.

### Integration tests (`workflow_test/integration_test.go`)

1. Create session with `agentType=codex` and `agentType=claude`.
2. WS connect, send `agent/input`, receive streamed output.
3. Token replay/reuse rejection remains enforced.
4. Stop and state convergence to `stopped`.
5. Negative cases: unknown environment, disallowed agent type, missing credentials.

### Web E2E

1. Start Codex session from browser, stream appears.
2. Switch to Claude in another session.
3. Reconnect after tab reload using token mint + replay.

## Rollout and Migration

1. Add schema columns with defaults (non-breaking).
2. Release proto/API fields with backward compatibility:
- accept legacy provider fields temporarily
- prefer `environment + agent_type`
3. Enable runtime manager in shadow mode with synthetic health checks.
4. Enable Codex only for canary environments.
5. Enable Claude for canary environments.
6. Remove legacy mock bootstrap path and obsolete provider request fields.

## PR-by-PR Execution Plan

### Delivery Rules

- gRPC-first only: all `/v1/agent-sessions*` routes must stay in proto + grpc-gateway.
- No standalone `net/http` REST handlers for `/v1/agent-sessions`.
- Keep `/ws/sessions/*` as explicit WS handler (not grpc-gateway).
- One PR at a time: implement, test, push, wait for CI, merge, fast-forward main.

### PR1 - Proto + API contract baseline

Scope:

1. Update `proto/agent/agent_service.proto` to include `agent_type` and capability endpoint.
2. Update `services/agent/server.go` request/response mappings.
3. Keep compatibility handling for old fields during migration.

Exit criteria:

1. `/v1/agent-sessions` accepts `agentType`.
2. `/v1/agent-sessions/capabilities` returns supported agent types.
3. Unit tests for request validation pass.

### PR2 - Storage/model schema

Scope:

1. Add migration for `agent_sessions` runtime/agent fields.
2. Add migration for environment agent policy fields.
3. Extend `internal/models` and storage adapters (memory + postgres).

Exit criteria:

1. Storage parity for memory and postgres.
2. CRUD tests for environment policy fields.

### PR3 - Session service runtime interface

Scope:

1. Introduce `RuntimeProvider` interface in `internal/agentsession`.
2. Replace fake bootstrap/finalize with provider-driven start/stop.
3. Preserve lifecycle loop and event/audit semantics.

Exit criteria:

1. Session state transitions driven by provider callbacks/events.
2. Existing token/replay tests continue to pass.

### PR4 - Sandbox runtime bridge

Scope:

1. Implement concrete E2B runtime provider for sandbox start/stop/connect.
2. Implement bridge that forwards WS frames to runtime endpoint and back.
3. Wire heartbeat and startup timeout handling.

Exit criteria:

1. End-to-end session reaches `running` only after runtime ready.
2. Runtime disconnect transitions session to `failed` with code.

### PR5 - Codex adapter

Scope:

1. Implement Codex CLI adapter inside sandbox runtime shim.
2. Normalize Codex stream/tool events to WS envelope.
3. Add CLI binary/config validation for Codex.

Exit criteria:

1. Codex session produces streamed output in integration tests.

### PR6 - Claude adapter

Scope:

1. Implement Claude CLI adapter inside sandbox runtime shim.
2. Normalize Claude events to same envelope.
3. Add CLI binary/config validation for Claude and validate environment policy with `allowed_agent_types`.

Exit criteria:

1. Claude session produces streamed output in integration tests.

### PR7 - Web integration

Scope:

1. Replace mock providers and fake terminal in `web/src/App.jsx` + `web/src/components/AgentSession.jsx`.
2. Hook create/list/stop/token/events + websocket stream.
3. Add reconnect with `lastSeq` replay.

Exit criteria:

1. Web can start and interact with real Codex/Claude sessions.
2. No mock output path in production mode.

### PR8 - Security hardening

Scope:

1. Runtime credential handling and secret injection hardening.
2. Egress policy enforcement and validation.
3. Audit metadata expansion for model/runtime actions.

Exit criteria:

1. Security tests cover credential leak and unauthorized model selection paths.

### PR9 - Observability and reliability

Scope:

1. Add metrics, structured logs, health checks for runtime provider.
2. Add retry/backoff policy for startup and transient provider errors.
3. Add dashboards/alerts baseline docs.

Exit criteria:

1. Operators can diagnose startup failure cause by metric + log correlation.

### PR10 - E2E hardening and completion

Scope:

1. Expand `workflow_test/integration_test.go` to Codex/Claude real runtime paths.
2. Update README and ops notes for required env vars and deployment behavior.
3. Remove obsolete compatibility fields/paths if no longer needed.
4. Mark this spec finished.

Exit criteria:

1. CI green with integration coverage for both agent types.
2. Spec moved to `finished_*` state after merge.

## Open Questions

1. Should environment policy be org-admin only, or user-editable in personal spaces?
2. Do we need per-session model version pinning (`claude-sonnet-*`, `gpt-*`) in v1?
3. What is the default retention period for session event/audit payloads in postgres?
