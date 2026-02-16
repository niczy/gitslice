# Agent Session Backend Design

## Executive Summary

This spec defines a production backend for coding-agent sessions.

- Starting a session launches an isolated container (or Kubernetes pod).
- The agent runtime inside that container exposes an internal WebSocket endpoint.
- The browser never connects to containers directly.
- The browser connects to a public proxy endpoint, and the proxy bridges traffic to the container endpoint.

The design separates control-plane actions (session lifecycle) from data-plane actions (interactive WebSocket traffic), supports reconnect/resume, and is built for multi-tenant security.

## Goals

1. Launch one isolated execution environment per user session.
2. Provide low-latency interactive terminal/tool streaming over WebSocket.
3. Keep runtime endpoints private; only proxy is internet-facing.
4. Support reconnect/resume after network loss.
5. Provide strong auditing and observability.
6. Scale horizontally with stateless API/proxy tiers.

## Non-Goals

1. Defining the agent model/tool business logic itself.
2. Replacing existing file/slice APIs in this repository.
3. Multi-region active-active in v1.

## High-Level Architecture

### Components

1. API Service (control plane)
- AuthN/AuthZ
- Session create/stop/get/token endpoints
- Writes session metadata to Postgres
- Publishes lifecycle events

2. Session Controller
- Consumes session start/stop jobs
- Starts/stops containers via Docker or Kubernetes API
- Tracks state transitions and readiness heartbeats

3. Agent Runtime (inside session container)
- Runs agent daemon and PTY multiplexer
- Exposes internal WS endpoint: `ws://0.0.0.0:9000/ws`
- Streams terminal output, tool events, and status updates
- Implements bounded replay buffer by sequence number

4. WS Proxy Service (data plane)
- Public endpoint: `wss://<host>/ws/sessions/{session_id}`
- Validates short-lived session token
- Resolves session route and dials runtime WS over private network
- Forwards frames bidirectionally with backpressure handling

5. Postgres
- Source of truth for sessions, routing metadata, event log, audit records

6. Redis
- Hot route cache (`session_id -> runtime endpoint`)
- Presence, short-lived locks, and token nonce replay protection

7. Object Storage (S3/GCS/filesystem)
- Optional large transcript/log artifact persistence

### Trust Boundaries

1. Browser to proxy: public internet (TLS required).
2. Proxy to runtime: private network only (VPC overlay, no public ingress).
3. API/Controller to infrastructure APIs: private control network.

## Deployment Topology

### v1 (single region)

1. API Deployment (N replicas, stateless)
2. WS Proxy Deployment (N replicas, stateless)
3. Session Controller Deployment (M replicas, leader-elected workers)
4. Session Runtime Pods/Containers (1 per active session)
5. Managed Postgres + Redis

### Runtime networking

1. Runtime pods are not exposed through public LoadBalancer/Ingress.
2. Proxy reaches runtime via internal DNS/service discovery.
3. NetworkPolicy allows runtime ingress only from proxy namespace.

## Session Lifecycle

### State machine

`creating -> starting -> running -> idle -> stopping -> stopped`

Failure states:

`creating|starting|running|idle|stopping -> failed`

### Transition rules

1. `creating -> starting`: controller accepted job and requested container creation.
2. `starting -> running`: runtime heartbeat and WS readiness received.
3. `running -> idle`: no input/output for configured idle window.
4. `idle -> running`: user activity resumes.
5. `running|idle -> stopping`: user stop request or TTL expiry.
6. `stopping -> stopped`: runtime terminated and cleanup complete.
7. Any active state -> `failed`: startup timeout, container crash loop, infra error.

## API Specification

Base path: `/v1/agent-sessions`

### 1) Create session

`POST /v1/agent-sessions`

Request:

```json
{
  "workspaceRef": "repo:github.com/niczy/gitslice",
  "image": "ghcr.io/org/agent-runtime:2026-02-16",
  "cpu": "2",
  "memoryMb": 4096,
  "idleTimeoutSec": 1800,
  "ttlSec": 14400,
  "env": {
    "FEATURE_FLAGS": "tool_replay_v1"
  }
}
```

Response `201`:

```json
{
  "sessionId": "sess_01JQ...",
  "state": "creating",
  "ws": {
    "url": "wss://app.example.com/ws/sessions/sess_01JQ...",
    "token": "<jwt>",
    "expiresAt": "2026-02-16T11:00:30Z"
  },
  "createdAt": "2026-02-16T10:59:30Z"
}
```

Notes:

1. API returns before container is fully ready.
2. Client listens for `status` events over WS and/or polls session status endpoint.

### 2) Get session

`GET /v1/agent-sessions/{session_id}`

Response `200`:

```json
{
  "sessionId": "sess_01JQ...",
  "state": "running",
  "runtime": {
    "node": "worker-a-3",
    "endpoint": "ws://10.42.7.19:9000/ws"
  },
  "lastActivityAt": "2026-02-16T11:03:01Z",
  "idleTimeoutSec": 1800,
  "ttlSec": 14400,
  "createdAt": "2026-02-16T10:59:30Z"
}
```

`runtime.endpoint` is returned only to privileged internal callers; hidden for normal user tokens.

### 3) Stop session

`POST /v1/agent-sessions/{session_id}/stop`

Request:

```json
{
  "reason": "user_requested"
}
```

Response `202`:

```json
{
  "sessionId": "sess_01JQ...",
  "state": "stopping"
}
```

### 4) Mint reconnect token

`POST /v1/agent-sessions/{session_id}/token`

Response `200`:

```json
{
  "url": "wss://app.example.com/ws/sessions/sess_01JQ...",
  "token": "<jwt>",
  "expiresAt": "2026-02-16T11:05:22Z"
}
```

### 5) List recent events (debug/support)

`GET /v1/agent-sessions/{session_id}/events?sinceSeq=1234&limit=200`

Response `200`:

```json
{
  "events": [
    {"seq": 1235, "stream": "status", "type": "state", "payload": {"state": "running"}}
  ],
  "nextSeq": 1236
}
```

## WebSocket Protocol

### WS endpoint

Client connects to:

`wss://app.example.com/ws/sessions/{session_id}?token=<jwt>&lastSeq=<n>`

`lastSeq` is optional and used for replay after reconnect.

### Envelope

All messages use the same envelope:

```json
{
  "seq": 1024,
  "ts": "2026-02-16T11:01:44.123456Z",
  "stream": "pty",
  "type": "stdout",
  "payload": {}
}
```

Fields:

1. `seq` (uint64): monotonically increasing per session.
2. `ts` (RFC3339 with microseconds).
3. `stream`: `pty | tool | status | log | control`.
4. `type`: stream-specific event type.
5. `payload`: JSON object.

### Client -> server message types

1. `control/hello`

```json
{"stream":"control","type":"hello","payload":{"client":"web","protocolVersion":"1"}}
```

2. `pty/stdin`

```json
{"stream":"pty","type":"stdin","payload":{"data":"ls -la\n"}}
```

3. `pty/resize`

```json
{"stream":"pty","type":"resize","payload":{"cols":120,"rows":34}}
```

4. `control/ping`

```json
{"stream":"control","type":"ping","payload":{"nonce":"abc"}}
```

### Server -> client message types

1. `status/state`

```json
{"stream":"status","type":"state","payload":{"state":"running"}}
```

2. `pty/stdout` and `pty/stderr`
3. `tool/event` (tool call started/completed/failed)
4. `control/pong`
5. `control/error`

### Replay behavior

1. Runtime keeps an in-memory ring buffer (default 10,000 frames).
2. On reconnect with `lastSeq`, proxy asks runtime to replay frames where `seq > lastSeq`.
3. If `lastSeq` is too old (evicted), runtime sends `control/error` with code `REPLAY_GAP`; client falls back to `/events` API.

### Backpressure and limits

1. Max inbound frame size: 256 KiB.
2. Max outbound queue per connection: 8 MiB.
3. If queue exceeds limit, close with code 1013 and reason `backpressure`.

## Authentication and Authorization

### User auth

1. API endpoints require user bearer token (OIDC/JWT).
2. Session ownership enforced on all session-scoped endpoints.

### WS token

Short-lived JWT minted by API for a specific session.

Required claims:

1. `sub`: user id
2. `sid`: session id
3. `aud`: `agent-ws`
4. `exp`: <= 60 seconds from mint time
5. `jti`: unique nonce

Validation rules:

1. Signature valid and not expired.
2. `sid` matches URL session id.
3. `jti` not previously used (Redis nonce set with TTL).
4. Session is in `starting|running|idle`.

## Data Model (Postgres)

### Table: `agent_sessions`

```sql
CREATE TABLE agent_sessions (
  session_id           TEXT PRIMARY KEY,
  user_id              TEXT NOT NULL,
  workspace_ref        TEXT NOT NULL,
  state                TEXT NOT NULL,
  image_ref            TEXT NOT NULL,
  cpu_millicores       INT NOT NULL,
  memory_mb            INT NOT NULL,
  idle_timeout_sec     INT NOT NULL,
  ttl_sec              INT NOT NULL,
  runtime_endpoint     TEXT,
  runtime_node         TEXT,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  started_at           TIMESTAMPTZ,
  last_activity_at     TIMESTAMPTZ,
  stopped_at           TIMESTAMPTZ,
  failure_code         TEXT,
  failure_message      TEXT
);

CREATE INDEX idx_agent_sessions_user_created
  ON agent_sessions (user_id, created_at DESC);

CREATE INDEX idx_agent_sessions_state_updated
  ON agent_sessions (state, updated_at DESC);
```

### Table: `agent_session_events`

```sql
CREATE TABLE agent_session_events (
  session_id           TEXT NOT NULL,
  seq                  BIGINT NOT NULL,
  ts                   TIMESTAMPTZ NOT NULL,
  stream               TEXT NOT NULL,
  type                 TEXT NOT NULL,
  payload_json         JSONB NOT NULL,
  PRIMARY KEY (session_id, seq)
);

CREATE INDEX idx_agent_session_events_ts
  ON agent_session_events (session_id, ts DESC);
```

### Table: `agent_session_audit`

```sql
CREATE TABLE agent_session_audit (
  id                   BIGSERIAL PRIMARY KEY,
  session_id           TEXT NOT NULL,
  actor_user_id        TEXT,
  action               TEXT NOT NULL,
  metadata_json        JSONB NOT NULL,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_session_audit_session_created
  ON agent_session_audit (session_id, created_at DESC);
```

## Redis Keys

1. `agent:route:{session_id}` -> runtime endpoint (TTL 5m, refreshed by heartbeat).
2. `agent:ws_nonce:{jti}` -> `1` (TTL token expiry + skew, set-if-not-exists).
3. `agent:lock:start:{session_id}` -> distributed lock for start idempotency.
4. `agent:presence:{session_id}` -> active connection count.

## Controller and Runtime Contracts

### Controller -> Runtime bootstrap environment

1. `SESSION_ID`
2. `SESSION_TOKEN_SIGNING_PUBLIC_KEY` (or JWKS URL)
3. `RUNTIME_PORT=9000`
4. `WORKSPACE_MOUNT_PATH=/workspace`
5. `MAX_REPLAY_FRAMES=10000`

### Runtime heartbeats

Runtime emits heartbeat every 5 seconds to controller/API:

```json
{
  "sessionId": "sess_01JQ...",
  "state": "running",
  "endpoint": "ws://10.42.7.19:9000/ws",
  "lastActivityAt": "2026-02-16T11:04:00Z",
  "bufferHeadSeq": 2048,
  "bufferTailSeq": 1890
}
```

If heartbeat missing for > 20 seconds, controller marks session `failed` and begins cleanup.

## Failure Handling

1. Start timeout (default 90s): transition to `failed`, store failure details.
2. Runtime crash while active: transition to `failed`, emit audit/event.
3. Proxy cannot route session: return WS close `1011` + `SESSION_UNAVAILABLE`.
4. Postgres transient errors: retry with exponential backoff and idempotency keys.
5. Stop request on already stopped session: return `200` idempotent success.

## Security Hardening

1. Runtime container runs as non-root with dropped Linux capabilities.
2. Read-only root filesystem; writable workspace mount only.
3. CPU/memory/pid limits enforced per session.
4. Egress policy defaults deny; allowlist required endpoints.
5. Secret injection via workload identity or scoped secret mounts.
6. Full audit of session creation, token minting, stop, and force-terminate actions.

## Scalability Targets (v1)

1. 2,000 concurrent sessions in one region.
2. p95 session create accepted latency < 250 ms (API only, async start).
3. p95 startup time to running < 20 s for warm nodes.
4. p95 WS frame proxy latency < 50 ms intra-region.

## Observability

### Metrics

1. `agent_session_create_requests_total{result}`
2. `agent_session_start_duration_seconds`
3. `agent_session_active_total`
4. `agent_ws_connect_total{result}`
5. `agent_ws_disconnect_total{code}`
6. `agent_ws_frame_latency_ms`
7. `agent_runtime_heartbeat_lag_seconds`

### Logs

All logs must include:

1. `session_id`
2. `user_id` (if known)
3. `request_id`
4. `component` (`api|controller|proxy|runtime`)

### Tracing

Propagate trace headers from browser -> API -> proxy -> runtime for end-to-end debugging.

## Rollout Plan

1. Phase 1: Internal beta with Docker single-host controller.
2. Phase 2: Kubernetes controller + runtime pods + NetworkPolicy.
3. Phase 3: Enable reconnect replay and event persistence by default.
4. Phase 4: Autoscaling policy tuning and load test sign-off.

## Test Plan

1. Unit tests
- Token validation and nonce replay checks
- Session state transition guards
- WS envelope validation

2. Integration tests
- Create -> running -> stop lifecycle
- Proxy bridge end-to-end PTY echo test
- Reconnect with `lastSeq` replay test

3. Failure tests
- Runtime crash mid-session
- Controller restart during session start
- Redis/Postgres transient outage handling

4. Security tests
- Unauthorized WS connection attempts
- Token reuse attempt (same `jti`)
- Cross-session access attempt by different user

## Open Questions

1. Should runtime event replay survive container restarts (requires durable queue)?
2. Should we support shared pair-programming sessions in v1 or defer?
3. Should session artifacts be retained per org policy (for example 7/30/90 days)?
