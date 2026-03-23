# Agent Public Key Auth

## Implementation Status

- Current status: `not started`
- Last updated: `2026-03-23`

---

## Goal

Make `gs` easy for agents to authenticate without passwords, browser prompts, or long-lived copied bearer tokens by supporting public/private key-based agent enrollment and login.

The design keeps the current bearer-session model for all service authorization. Public keys are used to prove possession and mint normal short-lived bearer sessions.

---

## Why This Exists

The current auth surface is usable for humans:

- `gs login` starts OAuth device login
- `gs --api-key` accepts a bearer access token
- `~/.gitslice/credentials.json` stores refreshable session credentials

That is not ideal for agents:

- browser-based approval is awkward for first-time automation
- `--api-key` is really an access token, not a first-class machine credential
- password signup is the wrong primitive for unattended agents
- there is no machine-owned credential lifecycle in Settings

The platform already has:

- gRPC-first account APIs in `proto/account/account_service.proto`
- refreshable auth sessions in `services/account/server.go`
- session-backed bearer auth across services

So the right design is not "replace auth with signed requests." The right design is "let agents authenticate with keys, then receive scoped bearer sessions."

---

## Non-Goals

- Do not replace bearer tokens on service-to-service or CLI-to-server requests.
- Do not add SSH-compatible transport or Git-over-SSH behavior.
- Do not solve organization SSO or workforce identity in this feature.
- Do not implement hardware attestation or TPM-bound keys in v1.
- Do not remove OAuth device login for humans.

---

## Product Principles

1. **Bearer remains the runtime auth primitive**
   - Existing services already authorize via `Authorization: Bearer <token>`.
   - That should stay unchanged.

2. **Public key auth is for bootstrap and renewal**
   - Agent proves possession of a private key.
   - Server returns a normal access token and refresh token.

3. **Keys belong to users**
   - A human account owns the key registrations.
   - Agents authenticate as that account unless scoped differently later.

4. **Agent signup and login are both supported**
   - New account bootstrap with a public key.
   - Existing account key enrollment and key-based login.

5. **Everything is gRPC-first**
   - No standalone `/v1/*` HTTP handlers.
   - Add RPCs to `AccountService` and expose them through grpc-gateway.

---

## User Stories

### Story 1: Existing user adds an agent key

1. User signs in on the web.
2. User opens Settings.
3. User goes to `Agent Keys`.
4. User pastes a public key or generates one locally and uploads the public half.
5. User names the key, for example `codex-laptop` or `build-bot-prod`.
6. Key becomes available for `gs auth login --key`.

### Story 2: Agent logs in non-interactively with a key

1. Agent has an `ed25519` private key on disk.
2. Agent runs `gs auth login --key ~/.config/gitslice/agent_ed25519 --json`.
3. CLI asks server for a short-lived challenge tied to that public key.
4. CLI signs challenge bytes locally.
5. Server verifies signature and issues normal bearer + refresh tokens.
6. CLI stores them in `~/.gitslice/credentials.json`.

### Story 3: Agent signs up with a key

1. Agent generates a new `ed25519` keypair.
2. Agent runs:

```bash
gs auth signup --username buildbot --email buildbot@example.com --name "Build Bot" --key ~/.config/gitslice/buildbot_ed25519 --json
```

3. CLI sends signup metadata and the public key.
4. Server creates the account, provisions the home slice, records the agent key, and returns a normal bearer + refresh token pair.

### Story 4: User revokes a compromised agent key

1. User opens Settings `Agent Keys`.
2. User revokes `ci-runner-3`.
3. Future key-login attempts fail immediately.
4. Existing refresh tokens issued through that key are revoked as part of key revocation.

---

## High-Level UX

### CLI surface

New command group:

```bash
gs auth signup --username <u> --email <e> --name <name> --key <private-key-path> [--json]
gs auth login --key <private-key-path> [--json]
gs auth keygen --out <private-key-path> [--json]
gs auth keys list [--json]
gs auth keys add --public-key <path> --name <name> [--json]
gs auth keys revoke <key-id> [--json]
gs auth status [--json]
```

Behavior:

- `gs auth keygen`
  - generates an `ed25519` keypair
  - writes private key with `0600`
  - writes public key alongside it or to stdout/json
- `gs auth signup --key`
  - signs up and stores refreshable credentials
- `gs auth login --key`
  - logs in using challenge/response
- `gs auth keys *`
  - manages registered agent keys for an authenticated user

### Settings UI

Add a new Settings section:

- `Agent Keys`

Show:

- key name
- key ID
- created at
- last used at
- last used IP/device info if available
- state: active/revoked

Actions:

- add key
- revoke key
- copy key ID

Optional follow-up:

- show sessions issued by each key
- revoke sessions for a specific key

---

## Technical Design

## Authentication Model

### Current model

Current auth resolution in `gs`:

1. `--api-key`
2. `GS_API_KEY`
3. `~/.gitslice/credentials.json`
4. legacy username auth

Current runtime authorization in services:

- `Authorization: Bearer <token>`
- legacy `Authorization: User <username>`

### Proposed model

Keep runtime authorization unchanged:

- service RPCs still use bearer tokens

Add a new asymmetric login bootstrap:

1. client proves possession of an enrolled private key
2. server creates a normal auth session
3. client stores bearer + refresh tokens
4. all future requests use bearer auth exactly like today

This minimizes invasive service changes.

---

## Key Type and Formats

### v1 algorithm

- `ed25519` only

Why:

- small keys
- fast signatures
- good library support in Go
- simpler than RSA/ECDSA for CLI and storage

### Stored public key format

Persist normalized raw key bytes plus metadata:

- algorithm: `ed25519`
- public key bytes
- fingerprint

CLI can accept:

- OpenSSH public key format
- raw base64/hex only if we deliberately add it later

Private key on disk:

- use a stable CLI-managed format
- simplest v1 is PKCS8 PEM for private keys and OpenSSH-compatible `.pub`

---

## Challenge / Response Flow

### Key login flow

1. Client calls `StartAgentKeyLogin` with:
   - key fingerprint or public key
   - optional device info
2. Server validates the key exists and is active.
3. Server returns:
   - `challenge_id`
   - `challenge_bytes`
   - `expires_at`
4. Client signs exactly the returned challenge bytes.
5. Client calls `CompleteAgentKeyLogin` with:
   - `challenge_id`
   - `signature`
6. Server verifies:
   - challenge exists
   - challenge unexpired
   - key is active
   - signature valid
7. Server creates a refreshable auth session.
8. Server returns normal `AuthResponse`.

### Challenge contents

Challenge payload should include:

- challenge ID
- key ID
- user ID
- audience / service host
- issued at
- expires at
- nonce

The client signs server-provided bytes directly. Do not reconstruct or stringify locally.

### Replay protection

- one-time challenge IDs
- short TTL, for example `60s`
- mark used on successful completion
- reject replays and expired completions

---

## Signup Flow

### Key signup flow

1. CLI generates or loads a local keypair.
2. CLI calls `SignupWithAgentKey`.
3. Server validates:
   - username
   - email
   - key algorithm
   - public key format
   - key not already linked to another account
4. Server:
   - creates user
   - provisions home slice
   - stores agent key
   - creates refreshable auth session
5. Server returns normal `AuthResponse`.

### Why signup does not need challenge/response

For first signup, key ownership is established by the client supplying the public key and immediately receiving an authenticated session.

If we want stricter proof-of-possession at signup time, we can require:

- `StartAgentKeySignup`
- `CompleteAgentKeySignup`

But that adds complexity with limited security gain in v1 because there is no prior trust anchor yet. The simpler v1 is acceptable if the signup call includes the public key and the first session is issued only over TLS.

---

## Data Model

Add a new persistent model:

- **AgentKey**
  - `id`
  - `user_id`
  - `name`
  - `algorithm`
  - `public_key`
  - `fingerprint`
  - `state` (`active`, `revoked`)
  - `created_at`
  - `updated_at`
  - `last_used_at`
  - `revoked_at`

Add a new ephemeral or persistent challenge model:

- **AgentKeyChallenge**
  - `id`
  - `agent_key_id`
  - `user_id`
  - `challenge_bytes`
  - `created_at`
  - `expires_at`
  - `used_at`
  - `device_info`

Optional session attribution field:

- `auth_sessions.agent_key_id`

That attribution is useful for:

- key-specific session revocation
- Settings display
- auditability

---

## API Design

Extend `AccountService`.

### New RPCs

```proto
rpc SignupWithAgentKey(SignupWithAgentKeyRequest) returns (AuthResponse)
rpc StartAgentKeyLogin(StartAgentKeyLoginRequest) returns (StartAgentKeyLoginResponse)
rpc CompleteAgentKeyLogin(CompleteAgentKeyLoginRequest) returns (AuthResponse)
rpc ListAgentKeys(ListAgentKeysRequest) returns (ListAgentKeysResponse)
rpc CreateAgentKey(CreateAgentKeyRequest) returns (AgentKey)
rpc DeleteAgentKey(DeleteAgentKeyRequest) returns (google.protobuf.Empty)
```

### Message sketch

```proto
message AgentKey {
  string id = 1;
  string name = 2;
  string algorithm = 3;
  string fingerprint = 4;
  string created_at = 5;
  string updated_at = 6;
  string last_used_at = 7;
  string revoked_at = 8;
  bool revoked = 9;
}

message SignupWithAgentKeyRequest {
  string username = 1;
  string email = 2;
  string name = 3;
  string algorithm = 4;
  bytes public_key = 5;
  string key_name = 6;
}

message StartAgentKeyLoginRequest {
  string key_id = 1;
  string fingerprint = 2;
  bytes public_key = 3;
}

message StartAgentKeyLoginResponse {
  string challenge_id = 1;
  bytes challenge = 2;
  string expires_at = 3;
}

message CompleteAgentKeyLoginRequest {
  string challenge_id = 1;
  bytes signature = 2;
}

message CreateAgentKeyRequest {
  string name = 1;
  string algorithm = 2;
  bytes public_key = 3;
}

message ListAgentKeysRequest {}

message ListAgentKeysResponse {
  repeated AgentKey keys = 1;
}

message DeleteAgentKeyRequest {
  string key_id = 1;
}
```

### HTTP bindings

Use grpc-gateway bindings only:

```text
POST /v1/auth/agent/signup
POST /v1/auth/agent/login/start
POST /v1/auth/agent/login/complete
GET  /v1/auth/agent/keys
POST /v1/auth/agent/keys
DELETE /v1/auth/agent/keys/{key_id}
```

---

## Authorization Rules

### Who can create keys

- authenticated user can create keys for self

### Who can list/revoke keys

- authenticated user can list/revoke own keys
- org admins do not automatically get user-key access

### What revocation does

Revoking a key should:

- block future challenge creation
- block challenge completion
- revoke all sessions attributed to that key

That last point is important. Otherwise revoked keys remain partially effective.

---

## CLI Details

### `gs auth keygen`

Responsibilities:

- generate `ed25519` keypair
- write private key to given path with `0600`
- write public key to `<path>.pub`
- print JSON when `--json` is set

### `gs auth signup --key`

Responsibilities:

- load private key
- derive public key
- call `SignupWithAgentKey`
- write returned tokens into `~/.gitslice/credentials.json`

### `gs auth login --key`

Responsibilities:

- load private key
- derive public key/fingerprint
- call `StartAgentKeyLogin`
- sign `challenge`
- call `CompleteAgentKeyLogin`
- store returned tokens

### Error handling

Add stable machine-readable auth errors:

- `AGENT_KEY_NOT_FOUND`
- `AGENT_KEY_REVOKED`
- `AGENT_KEY_INVALID`
- `AGENT_KEY_SIGNATURE_INVALID`
- `AGENT_KEY_CHALLENGE_EXPIRED`
- `AGENT_KEY_CHALLENGE_USED`
- `AGENT_KEY_ALREADY_REGISTERED`

---

## Settings UI Details

Add a section to the existing Settings page:

- heading: `Agent Keys`
- short description: `Use public keys for non-interactive gs auth`

Empty state:

- explain that agents can sign in with public/private keys
- show a CLI example

Create flow:

- name input
- public key textarea/file upload
- submit

List row fields:

- key name
- fingerprint
- created at
- last used at
- status

Actions:

- revoke

We do not need browser-side private key handling.

---

## Security Model

### What this improves

- no password needed for agents
- no copied long-lived bearer token required for initial bootstrap
- revocable machine credentials
- clearer audit trail by key

### Main risks

- private key theft on the client
- replay of signed challenge if challenge handling is weak
- long-lived refresh token abuse if key-attributed sessions are not revoked
- accidental logging of challenge/signature material

### Mitigations

- short challenge TTL
- one-time challenge use
- TLS required
- revoke key => revoke key-attributed sessions
- never log raw private key, public key bytes, or signatures
- only log fingerprints and key IDs

---

## Compatibility and Migration

This feature is additive.

Existing flows remain:

- OAuth device login
- bearer token env/config
- current web login

No service auth behavior outside account/login flows needs to change.

---

## Open Questions

1. Should `SignupWithAgentKey` require email verification in v1?
2. Should key signup be allowed for all users or only from invited/org-managed contexts?
3. Should `gs auth keygen` default to a repo-local path or user config path?
4. Should we support multiple active keys per user immediately? Recommendation: yes.
5. Should keys have scopes in v1? Recommendation: no, only user identity; add scopes later if needed.

---

## PR-by-PR Execution Plan

### Delivery rules

- gRPC-first only; add/extend `AccountService` and grpc-gateway bindings.
- Do not add standalone HTTP handlers for `/v1/auth/agent/*`.
- Keep each PR independently mergeable and production-safe.
- Preserve existing OAuth device flow and bearer auth.
- Complete one PR at a time: branch, implement, test, push, wait for CI, merge, fast-forward `main`, then continue.

### PR1 - Proto and storage scaffolding

Scope:

- Extend `proto/account/account_service.proto` with:
  - `SignupWithAgentKey`
  - `StartAgentKeyLogin`
  - `CompleteAgentKeyLogin`
  - `ListAgentKeys`
  - `CreateAgentKey`
  - `DeleteAgentKey`
- Add `AgentKey` messages and challenge request/response messages.
- Add storage interfaces for:
  - create/get/list/revoke agent keys
  - create/get/use agent key challenges
- Add Postgres migration(s) for:
  - `agent_keys`
  - `agent_key_challenges`
- Add in-memory storage parity.

Exit criteria:

- build passes
- storage interface compiles
- no behavior exposed yet beyond skeletons

### PR2 - Server-side key management APIs

Scope:

- Implement authenticated key CRUD in `services/account/server.go`:
  - list keys
  - create key
  - delete/revoke key
- Add fingerprint normalization and duplicate-key checks.
- Add server tests for create/list/revoke behavior.

Exit criteria:

- authenticated users can manage their own keys
- duplicate key registration is rejected
- revoked keys stay listed but inactive

### PR3 - Agent key login flow

Scope:

- Implement:
  - `StartAgentKeyLogin`
  - `CompleteAgentKeyLogin`
- Add `ed25519` signature verification helpers.
- Add one-time challenge semantics and expiry checks.
- Attribute created auth sessions to `agent_key_id`.
- Add tests for:
  - happy path
  - expired challenge
  - replayed challenge
  - revoked key
  - invalid signature

Exit criteria:

- key login returns a normal `AuthResponse`
- replay and expiry protections are enforced

### PR4 - Agent key signup flow

Scope:

- Implement `SignupWithAgentKey`.
- Create user + home slice + initial key + refreshable session in one flow.
- Reject username/email/key collisions cleanly.
- Add tests for:
  - successful signup
  - duplicate username
  - duplicate key
  - invalid public key

Exit criteria:

- agents can create a new account and immediately receive usable tokens

### PR5 - CLI auth surface

Scope:

- Add `gs auth` command group if not already present as a first-class surface.
- Implement:
  - `gs auth keygen`
  - `gs auth signup --key`
  - `gs auth login --key`
  - `gs auth keys list`
  - `gs auth keys add`
  - `gs auth keys revoke`
- Store returned credentials in `~/.gitslice/credentials.json`.
- Add `--json` everywhere in the new auth surface.

Exit criteria:

- end-to-end agent signup/login works entirely from CLI

### PR6 - Settings UI for agent keys

Scope:

- Add `Agent Keys` section to Settings.
- Show list of current keys and status.
- Add create and revoke actions.
- Wire through grpc-gateway account routes.
- Add web tests for key list and revoke flow.

Exit criteria:

- users can manage agent keys from the web UI

### PR7 - Revocation hardening and session cleanup

Scope:

- On key revoke, revoke all sessions attributed to that key.
- Surface `last_used_at` updates on successful key logins.
- Expose session attribution in account/session handling where needed.
- Add tests for post-revocation session invalidation.

Exit criteria:

- revoked keys cannot mint new sessions
- old sessions from that key stop working

### PR8 - Docs, polish, and rollout completion

Scope:

- Update `README.md` auth/CLI sections.
- Add agent-auth examples to the docs page and settings docs if relevant.
- Add workflow/integration coverage for:
  - key signup
  - key login
  - key revocation
- Add `gs doctor` / `gs context --json` auth fields for agent-key sessions if missing.

Exit criteria:

- docs match shipped behavior
- integration coverage exists for the main agent auth flows

---

## Acceptance Criteria

- Agents can sign up without passwords using an `ed25519` keypair.
- Existing users can register one or more agent public keys.
- Agents can log in non-interactively using a private key and receive normal bearer credentials.
- Revoking a key revokes sessions minted from that key.
- All new APIs are gRPC-first and exposed via grpc-gateway.
- Settings shows and manages GitSlice agent keys.
- CLI supports fully machine-readable auth flows with `--json`.
