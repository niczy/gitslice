# Agent Protocol

## Purpose

Gitslice agent sessions are controlled through the `agent.v1.AgentService`
protobuf API. The protocol is intentionally event based so the same session can
be driven from the web UI, a remote runtime, or a user-hosted local runner.

The web UI submits control events, the runtime consumes them, and the runtime
appends output events back to the same durable event log. This keeps reconnects,
audit history, and local/remote providers on one lifecycle.

## Proto Files

The protocol lives in `proto/agent`:

- `agent_service.proto` defines the session API and HTTP gateway bindings.
- `agent_protocol.proto` defines the shared event envelope and typed payloads.

`EventEnvelope` is the durable wire shape:

```proto
message EventEnvelope {
  uint64 seq = 1;
  string ts = 2;
  string stream = 3;
  string type = 4;
  bytes payload = 5;
}
```

The `payload` bytes contain JSON encoded from the protobuf payload messages. The
current typed payloads cover agent input, interrupts, output, errors, status, and
PTY resize/input events.

## Session Lifecycle

1. A client calls `CreateSession` with a `slice_id`, `agent_type`, and runtime
   provider.
2. The service creates an active session and appends lifecycle state events.
3. The selected runtime provider starts and moves the session to `running`.
4. Web or CLI clients send input through `SendInput` or the websocket bridge.
5. The runtime appends output through `AppendEvent`.
6. A client calls `SendInterrupt` or `StopSession` to control or terminate the
   session.

Active states are `creating`, `starting`, `running`, `idle`, and `stopping`.
Terminal states are `stopped` and `failed`.

## Event Streams

Use stable `stream/type` pairs so clients can route events without needing to
understand every payload.

| Stream | Type | Direction | Payload |
| --- | --- | --- | --- |
| `status` | `state` | service to clients | `AgentStatePayload` |
| `agent` | `input` | user to runtime | `AgentInputPayload` |
| `agent` | `interrupt` | user/service to runtime | `AgentInterruptPayload` |
| `agent` | `output_delta` | runtime to clients | `AgentOutputPayload` |
| `agent` | `output_final` | runtime to clients | `AgentOutputPayload` |
| `control` | `error` | any component to clients | `AgentErrorPayload` |
| `pty` | `stdin` | user to runtime | `AgentPtyInputPayload` |
| `pty` | `resize` | user to runtime | `AgentPtyResizePayload` |

Consumers should ignore unknown stream/type pairs and continue from the next
sequence number.

## Control RPCs

`ListEvents` reads the durable event log from `since_seq`. `AppendEvent` writes a
new event and returns the assigned sequence number and timestamp.

`SendInput` and `SendInterrupt` are higher-level control RPCs. They validate the
session owner and active state, then hand the command to the configured runtime
provider. For the local provider, those commands are appended as `agent/input`
and `agent/interrupt` events for a local runner to consume.

## Local Provider

The local provider uses provider name `local`. It does not require a provider ID
in environment configuration and does not start a remote sandbox. Starting a
local session marks the runtime endpoint as `local://<session_id>` and waits for
a user-hosted runner.

`gs agent run` is the first runner implementation. It can create or attach to a
local-provider session, poll `ListEvents`, execute a local coding agent command
for each `agent/input` event, and append `agent/output_delta` and
`agent/output_final` events through `AppendEvent`.

Default commands:

- `codex`: `codex exec <prompt>`
- `claude`: `claude -p <prompt>`

Users can override the command after `--`, for example:

```bash
gs agent run --session ags_123 -- ./my-agent-script
```

When a custom command is used, the prompt is sent on stdin.
