# WorkOS Auth Integration Plan

## Implementation Status

- Current status: `completed`
- Last updated: `2026-04-06`

Completed:

- `PR1 - Auth Provider Abstraction + Config`
- `PR2 - Worker-side WorkOS Bootstrap`
- `PR3 - Core API WorkOS JWT Verification`
- `PR4 - Local User Provisioning + Account Linkage`
- `PR5 - Agent-Created Account Claim Flow`
- `PR6 - Replace Web Sign-In Surface`
- `PR7 - Web Account Management`
- `PR8 - Linked Auth Methods Implementation`
- `PR9 - WorkOS Organizations Linkage`
- `PR10 - Optional Human CLI Auth via WorkOS`
- `PR11 - Cleanup + Remove Legacy Human Web Auth`

---

## Goal

Integrate WorkOS as the primary human authentication system for the web product while keeping Gitslice as the source of truth for:

- local accounts and user records
- home slice provisioning
- organizations, teams, and invites
- agent-key auth
- CLI-specific auth flows

The key design constraint is that **accounts may be created by agents before any human identity exists**. WorkOS must attach to local Gitslice accounts; it must not be the only way accounts come into existence.

The intent is to replace the current split human web auth surface with a production-grade managed identity layer without regressing agent workflows.

---

## Why This Exists

The current account system is stronger in the backend than in the shipped product surface.

What already exists:

- account/session APIs in `proto/account/account_service.proto`
- local password auth, session auth, device auth, and agent-key auth in `services/account/server.go`
- web OAuth/dev session handling in `web/server/auth.js`
- agent-key management UI in `web/src/components/SettingsPage.jsx`

What is missing or incomplete:

- no full web signup flow
- no full web password login/reset flow
- no web session management UI
- no linked auth-method management
- no SAML/SSO implementation
- human web auth is still split between Worker session cookies and API-side local account behavior

WorkOS is a good fit for the human-facing identity layer:

- hosted sign-in
- passwords
- social login
- organizations and memberships
- invitations
- future SSO support

Agent-key auth should remain local to Gitslice, and agent-created accounts must remain claimable by humans later.

---

## Product Principles

1. **WorkOS is for human identity**
   - Web users authenticate via WorkOS.
   - Human CLI auth may use WorkOS later.

2. **Gitslice remains the application account system**
   - Local accounts and users still exist in Postgres.
   - Local usernames remain first-class.
   - Home slices still provision in Gitslice.

3. **Local account existence does not depend on WorkOS**
   - Agents can create accounts before any human signs in.
   - WorkOS attaches human identity to an existing local account or helps create one.

4. **Agent-key auth stays local**
   - `gs auth signup --key`
   - `gs auth login --key`
   - agent-key enrollment/revocation
   - these should not depend on WorkOS.

5. **Human identity is attachable later**
   - A human can claim or attach to an agent-created account.
   - Linking must be explicit and secure.

6. **Do not migrate everything at once**
   - First replace web human sign-in.
   - Then integrate organizations and account management.

7. **Keep the current infra model**
   - web on Cloudflare Worker
   - core API on the VM
   - staging on `agenttools.dev` / `api.agenttools.dev`
   - production on `gitslice.io` / `api.gitslice.io`

---

## Non-Goals

- Do not remove agent-key auth.
- Do not remove CLI device auth in the first phase.
- Do not move org authorization fully into WorkOS in the first phase.
- Do not replace Gitslice authorization with WorkOS FGA in v1.
- Do not attempt a backward-compatible migration for old auth cookies if it complicates rollout; there is no production traffic requirement.
- Do not require a human WorkOS identity before an agent can start using Gitslice.

---

## Current-State Gaps

### Web auth surface

- `LoginPage.jsx` supports OAuth buttons and dev username login.
- There is no normal production signup page.
- There is no password login page.
- There is no forgot/reset-password page.

### Backend account surface

The proto advertises more than the server currently implements.

Implemented in `services/account/server.go`:

- `Signup`
- `Login`
- `Logout`
- `StartDeviceAuthorization`
- `ApproveDeviceAuthorization`
- `PollDeviceAuthorization`
- `RefreshAccessToken`
- `ResetPassword`
- `ListSessions`
- `DeleteSession`
- agent-key APIs
- `GetMe`, `UpdateMe`, `DeleteMe`, `GetUser`
- org/team/invite APIs

Not implemented in `services/account/server.go`:

- `ListAuthMethods`
- `LinkAuthMethod`
- `DeleteAuthMethod`
- `OAuthCallback`
- `SamlCallback`

### Session model

Current web behavior is a mix of:

- Auth.js-style Worker session handling in `web/server/auth.js`
- dev username cookies
- API-side local account/session auth

This should be unified.

---

## Recommended Target Architecture

## Human Web Sign-In

1. Browser starts login with WorkOS AuthKit.
2. WorkOS redirects back to the Worker callback route with an authorization code.
3. Worker exchanges code for WorkOS session credentials.
4. Worker stores the app-facing session state in cookies.
5. Worker attaches a WorkOS bearer access token to API requests or forwards the session state in a controlled way.
6. Go core verifies the WorkOS JWT and resolves a local Gitslice account/user.
7. If this is the first login for that human identity, Gitslice either:
   - attaches the human to an existing local account, or
   - provisions a new local account/user and `home.<username>`.

## Agent-First Account Creation

1. Agent generates a local keypair.
2. Agent runs `gs auth signup --key ...`.
3. Gitslice creates a local account/user immediately.
4. Gitslice provisions the home slice immediately.
5. The account starts in an `agent_only` or unlinked state.
6. Later, a human can attach WorkOS identity to that account through an explicit linking flow.

## Local Account and User Mapping

Add local identity links:

- `users.account_id`
- `users.workos_user_id`
- `users.auth_source`
- `organizations.workos_organization_id`
- `memberships.workos_membership_id`

Add account-level ownership/linkage state:

- `accounts.id`
- `accounts.owner_mode` = `agent_only | human_attached | org_managed`
- `accounts.claim_state`
- `accounts.claim_token_hash` or equivalent one-time claim mechanism if needed

The local username remains stable and must not be recomputed on every WorkOS login.

Recommended rule:

- if explicit account claim or link proof is presented, attach WorkOS to that account
- otherwise, if email matches an existing local user and policy allows it, attach WorkOS to that user
- otherwise create a local account/user once and persist the chosen username

## Organizations

Phase 1:

- local orgs remain primary
- WorkOS org and membership IDs are stored as linked identity metadata

Phase 2:

- WorkOS organizations/invitations can become the primary UX for membership lifecycle
- local org rows remain the authorization/application model

## CLI and Agents

Keep current behavior:

- agent-key auth stays local
- local bearer/refresh token storage stays local
- `gs auth signup --key`
- `gs auth login --key`

Possible later extension:

- add WorkOS CLI auth for humans only
- add a CLI-assisted account-claim flow if humans need to attach to agent-created accounts outside the web app

---

## Identity and Session Model

### Human users

- WorkOS manages primary human sign-in methods
- Gitslice stores local account, user, and org metadata
- Go backend trusts verified WorkOS access tokens

### Agent users

- Gitslice-managed only
- not dependent on WorkOS
- may exist before any human identity exists

### Account ownership states

- `agent_only`
  - created by agent-key signup
  - no human identity linked yet
- `human_attached`
  - at least one WorkOS identity linked
- `org_managed`
  - owned or administered through local org policy

The account state should be explicit in the data model so linking and authorization rules stay predictable.

### Web session transport

Preferred model:

- Worker owns browser session cookies
- Worker exchanges/refreshes WorkOS session tokens
- Worker passes verified bearer access to the core API

Avoid:

- continuing long-term reliance on `Authorization: User <username>` for real signed-in web traffic

---

## Required Configuration

### Shared

- `AUTH_PROVIDER=workos`
- `PUBLIC_WEB_BASE_URL`
- `PUBLIC_API_BASE_URL`

### Worker

- `WORKOS_CLIENT_ID`
- `WORKOS_API_KEY`
- `WORKOS_REDIRECT_URI`
- `WORKOS_COOKIE_PASSWORD` or equivalent session secret
- optional custom AuthKit domain config if used

### Core API

- `WORKOS_CLIENT_ID`
- `WORKOS_API_KEY` or JWKS verification config
- `WORKOS_JWKS_URL` if explicit verification config is needed
- `AUTH_PROVIDER=workos`

### Environment split

Staging:

- `agenttools.dev`
- `api.agenttools.dev`
- separate WorkOS staging app / redirect URIs if needed

Production:

- `gitslice.io`
- `api.gitslice.io`
- separate WorkOS production app / redirect URIs if needed

Do not share staging and production secrets.

---

## Username Policy

This is the main product decision to lock up front.

Recommended:

- keep local `username` as the primary Gitslice identity
- do not use WorkOS user ID or email as the public username
- on first WorkOS login:
  - match existing local user by verified email if exactly one match exists
  - otherwise create a new local user with a stable derived username
  - if collision occurs, resolve once and persist it

Never derive the username fresh on every login.

---

## Account Claim / Human Attachment

This is the second major product decision to lock up front.

Recommended:

- do **not** auto-attach a WorkOS user to an agent-created account based only on email similarity
- support explicit linking through one of:
  - a one-time claim token generated from an authenticated agent/local session
  - an existing authenticated local bearer session in the browser
  - explicit org admin approval for org-managed accounts

Optional low-friction rule:

- allow verified-email auto-attach only when there is exactly one matching unclaimed local account and policy is explicitly enabled

Default safer rule:

- explicit claim or explicit link proof required

---

## Risks

### 1. Split-brain auth during migration

If Auth.js/dev-login and WorkOS both remain first-class for too long, the product will have two competing web session models.

Mitigation:

- migrate the main web path quickly
- keep dev-login only for local/dev

### 2. Username mapping mistakes

Incorrect email-based attachment could connect a WorkOS identity to the wrong local user.

Mitigation:

- verified-email only
- exact-match only
- fail closed when ambiguous

### 3. Account hijacking during human attachment

If a human can claim an agent-created account too easily, the agent-first model becomes unsafe.

Mitigation:

- explicit claim token or existing authenticated local session
- no loose email-based claiming by default
- audit account-link events

### 4. Org duplication

If WorkOS orgs and local orgs are both editable without clear ownership rules, data drift will follow.

Mitigation:

- phase 1 uses local orgs as the system of record
- WorkOS org IDs are linked metadata

### 5. Worker/runtime integration drift

The Worker and Go backend need a clean contract for login, refresh, logout, and session inspection.

Mitigation:

- define the auth boundary first
- add contract tests early

---

## Migration Strategy

Because there is no meaningful production traffic, the migration can be direct rather than backward-compatible.

Recommended path:

1. build WorkOS integration behind config
2. model local account ownership and claim state
3. validate in staging end to end
4. switch production web auth to WorkOS
5. keep dev-login only for local/dev fallback
6. keep agent-key auth untouched

No need to preserve old production auth cookies or old OAuth callback behavior if the cutover path is clean.

---

## PR-by-PR Execution Plan

### Delivery Rules

- APIs remain gRPC-first for account data that belongs in the core API.
- Do not add standalone `/v1/*` HTTP handlers for account routes when they belong in `AccountService`.
- Worker auth callback/session routes are acceptable in the web app because they are part of the web auth runtime.
- Complete one PR at a time and keep each PR mergeable.
- PR numbering is retained for planning continuity even though `PR6` through `PR8` landed before `PR5`.

### PR1 - Auth Provider Abstraction + Config

Scope:

- add `AUTH_PROVIDER` config with values like `local` and `workos`
- add WorkOS config surface to worker and core
- add local data model fields for:
  - account ownership state
  - WorkOS IDs on users/orgs/memberships
- keep current auth behavior as default

Changes:

- config structs and env parsing
- storage models and migrations
- docs/env examples

Exit criteria:

- repo can build with WorkOS config present
- local account/ownership schema exists
- no behavior change yet

### PR2 - Worker-side WorkOS Bootstrap

Scope:

- add Worker-side login start, callback, logout, and session bootstrap helpers
- integrate WorkOS authorization-code exchange
- keep current login UI behind a feature/config switch

Changes:

- `web/server/*`
- Worker auth session handling
- new auth helper layer replacing direct Auth.js dependency for the WorkOS path

Exit criteria:

- staging Worker can complete a WorkOS login round-trip and report signed-in session state

### PR3 - Core API WorkOS JWT Verification

Scope:

- verify WorkOS access tokens in the Go API
- map WorkOS claims to request identity
- add request context plumbing for WorkOS-backed users

Changes:

- auth middleware / token verification
- account identity resolution

Exit criteria:

- API accepts verified WorkOS-authenticated requests without using `User <username>` fallback

### PR4 - Local User Provisioning + Account Linkage

Scope:

- on first WorkOS login, find or create a local user
- resolve or create the owning local account
- persist `workos_user_id`
- ensure home slice exists
- define and enforce username creation/attachment rules

Changes:

- account service linkage helpers
- account lookup by claim/link state
- storage lookup by WorkOS ID
- home slice provisioning on WorkOS login

Exit criteria:

- first-time WorkOS user lands in a valid local account with `home.<username>`

### PR5 - Agent-Created Account Claim Flow

Scope:

- add explicit human attachment flow for agent-created accounts
- support safe account claim or link semantics
- define audit trail for claim/link events

Changes:

- account claim token or equivalent secure linking primitive
- worker/web claim flow
- backend claim validation

Exit criteria:

- a human can securely attach WorkOS identity to an existing agent-created account
- ambiguous or unauthorized attachment is rejected

Current status:

- scaffolding for account ownership and claim state has landed
- explicit claim/link flow is still missing

### PR6 - Replace Web Sign-In Surface

Scope:

- replace the production login page with WorkOS-first sign-in
- keep dev username login only in local/dev or behind explicit config
- remove production dependence on the current ad hoc auth split

Changes:

- `LoginPage.jsx`
- app auth bootstrapping
- session/load UI behavior

Exit criteria:

- normal staging web sign-in works only through WorkOS for human users

### PR7 - Web Account Management

Scope:

- add session list/revoke UI
- add profile edit UI
- add account deletion UI
- wire to existing backend APIs

Changes:

- `SettingsPage.jsx`
- `ProfilePage.jsx`
- `web/src/utils/api.js`

Exit criteria:

- user can inspect sessions, update profile fields, and delete account from the web UI

### PR8 - Linked Auth Methods Implementation

Scope:

- implement `ListAuthMethods`, `LinkAuthMethod`, and `DeleteAuthMethod` in the backend
- add web UI for linked identity methods
- define how WorkOS provider identities map into local auth methods

Changes:

- `proto/account/account_service.proto` if request/response refinement is needed
- `services/account/server.go`
- settings UI

Exit criteria:

- linked auth methods are real, not proto-only

### PR9 - WorkOS Organizations Linkage

Scope:

- store WorkOS org and membership IDs
- sync or attach WorkOS org identity to local org/member rows
- decide whether invite acceptance is local-first or WorkOS-first in this phase

Changes:

- org and membership linkage logic
- account/org service methods

Exit criteria:

- signed-in WorkOS users can be associated with the correct local org context

Current status:

- actively in progress

### PR10 - Optional Human CLI Auth via WorkOS

Scope:

- add WorkOS-backed human CLI login flow if still desired
- preserve current agent-key auth unchanged

Changes:

- `gs auth login --device` or a new WorkOS-oriented human auth subflow
- credential storage integration

Exit criteria:

- humans can authenticate the CLI through WorkOS without affecting agent-key flows

Current status:

- pending after organization linkage

### PR11 - Cleanup + Remove Legacy Human Web Auth

Scope:

- remove obsolete Auth.js-specific human paths
- remove production reliance on username dev-login
- reduce old local-password web auth surface if no longer needed
- update docs and tests

Changes:

- web auth cleanup
- docs
- e2e coverage

Exit criteria:

- human web auth is WorkOS-based end to end
- agent-key auth still works
- staging and production rollout docs are updated

Current status:

- pending after CLI hardening

---

## Testing Plan

### Unit / service

- WorkOS token verification
- local user linkage
- username collision handling
- org linkage behavior

### Workflow / integration

- agent-first signup provisions a usable local account and home slice
- human claim attaches to the correct existing account
- first login provisions home slice
- returning login reuses same local account
- session revoke works
- agent-key auth still works unchanged

### Web e2e

- account claim flow
- login redirect
- callback completion
- session persistence
- logout
- profile update
- session revoke
- organization context selection if applicable

### Staging verification

- `https://agenttools.dev/` sign-in works
- after sign-in, user lands in home slice
- `https://api.agenttools.dev/v1/global/state` healthy
- agent-key CLI auth still works against staging

---

## Success Criteria

- human users can sign in on the web through WorkOS
- agent-created accounts can exist before any human identity exists
- humans can securely attach to agent-created accounts
- first login creates or links a stable local Gitslice user
- home slice provisioning still happens automatically
- web no longer depends on dev username login in production
- agent-key auth remains fully functional
- account/session management UI covers the common user lifecycle
- staging and production use separate WorkOS credentials/config

---

## Recommended Immediate Start

Start with:

1. `PR1 - Auth Provider Abstraction + Config`
2. `PR2 - Worker-side WorkOS Bootstrap`
3. `PR3 - Core API WorkOS JWT Verification`
4. `PR4 - Local User Provisioning + Account Linkage`

That gets the auth boundary and account model right before touching claim flows, organizations, or the broader account UX.
