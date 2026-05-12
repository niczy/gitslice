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
| `control` | `runtime_session` | runtime to service/clients | JSON runtime metadata |
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

`gs agent start` and `gs agent run` are the local runner implementations.
`start` launches the runner in the background; `run` keeps it in the foreground.
Both commands watch the user's local-provider sessions, check out each web-created
session's slice into a subdirectory of the configured working directory, poll
`ListEvents`, run a local coding agent for `agent/input` events, and append
`agent/output_delta`, `agent/output_final`, and tool lifecycle events through
`AppendEvent`.

Default commands:

- `codex`: a persistent Codex app-server session over the Codex
  remote-control protocol when available, with `codex exec` fallback
- `claude`: a persistent Claude Code stream-json session when available, with
  `claude -p <prompt>` fallback

In `--codex-mode auto`, the runner starts `codex app-server --listen stdio://`,
initializes one Codex thread, and sends each Gitslice `agent/input` event as a
Codex `turn/start` request. Codex `item/agentMessage/delta` notifications become
`agent/output_delta`, final assistant messages become `agent/output_final`, and
command/tool notifications are forwarded as `tool/start`, `tool/output`, and
`tool/end` events. Gitslice `agent/interrupt` events are translated to Codex
`turn/interrupt` while the turn is still running, so the web UI can stop an
active Codex turn without waiting for the process to exit. If app-server startup
fails in auto mode, the runner appends a control error and falls back to
`codex exec`.

When the Codex thread is created, the local runner appends
`control/runtime_session` with the Codex thread ID. The service stores that
runtime metadata on the server-side `agent_sessions` row, so reconnects and
server-side inspection do not depend on client-only runner memory.

Use `--codex-mode exec` to force the previous one-process-per-input behavior:

```bash
gs agent run --dir ~/gitslice-agents --agent codex --codex-mode exec
```

In `--claude-mode auto`, the runner starts Claude Code in headless stream-json
mode:

```bash
claude -p --input-format stream-json --output-format stream-json \
  --include-partial-messages --verbose
```

Each Gitslice `agent/input` event is written as a Claude JSONL user message.
Claude assistant text becomes `agent/output_delta` and `agent/output_final`;
Claude `tool_use` and `tool_result` blocks are forwarded as `tool/start`,
`tool/output`, and `tool/end` events. Claude does not currently expose a
Codex-style websocket or JSON-RPC interrupt method, so Gitslice interrupts stop
the local Claude subprocess and the runner starts a fresh stream for later
inputs. When Claude reports its session ID, the local runner appends
`control/runtime_session`; the service stores that ID server-side with a
`claude-stream-json://<session_id>` endpoint.

Use `--claude-mode print` to force the previous one-process-per-input behavior:

```bash
gs agent run --session ags_123 --agent claude --claude-mode print
```

Users can override the command after `--`, for example:

```bash
gs agent run --dir ~/gitslice-agents -- ./my-agent-script
```

When a custom command is used, the prompt is sent on stdin.
