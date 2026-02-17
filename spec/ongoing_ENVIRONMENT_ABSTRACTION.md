# Environment Abstraction

## Implementation Status

- Current status: `ongoing`
- Last updated: `2026-02-17`

PR checklist:

- [x] PR1 - Environment registry: `environments` table, CRUD HTTP endpoints, `Environment` model
- [x] PR2 - Slice environment field: rename `e2b_template_id` → `environment` on slices, update all references
- [x] PR3 - Session resolution: agent session creation resolves environment name → provider config, clean user-facing API
- [ ] PR4 - Config file reader: parse `.gitslice/config.yaml`, apply on import/merge
- [ ] PR5 - UI settings panel: environment dropdown per slice in web app

## Executive Summary

Users should never see "E2B" in any interface. This spec introduces an **environment** abstraction — a named, reusable sandbox configuration that users assign to slices. Under the hood, each environment resolves to a provider-specific template (E2B today, potentially others in the future).

Users interact with friendly environment names (`node20`, `python311-ml`). Admins register environments via API. Developers configure per-slice defaults in a `.gitslice/config.yaml` file committed to the repo.

## Goals

1. Remove all E2B branding from user-facing APIs, proto messages, and UI.
2. Let users assign named environments to slices via API, UI, or config file.
3. Let admins register environments that map to provider-specific templates.
4. Support a repo-level config file (`.gitslice/config.yaml`) as a declarative source for slice environment settings.
5. Keep E2B-specific fields internal to the agent session and storage layers.

## Non-Goals

1. Multi-provider support in v1 (E2B is the only backend; the abstraction just hides it).
2. Building/publishing custom sandbox images from within gitslice.
3. Environment version management or promotion workflows.

## Current State (Before)

The following user-facing surfaces currently expose E2B branding:

| Surface | E2B Reference | File |
|---------|--------------|------|
| Slice model | `E2BTemplateID string` | `internal/models/slice.go:18` |
| Slice DB column | `e2b_template_id` | `migrations/003_slice_e2b_template.sql` |
| Storage interface | `UpdateSliceE2BTemplateID()` | `internal/storage/storage.go:38` |
| HTTP: create session request | `e2bTemplateId`, `e2bRegion`, `provider` fields | `internal/httpapi/agent_sessions.go:41-43` |
| HTTP: create session response | `e2bTemplateId`, `provider` fields | `internal/httpapi/agent_sessions.go:57-58` |
| HTTP: get session response | `e2bSandboxId`, `provider` fields | `internal/httpapi/agent_sessions.go:70-71` |
| HTTP: slice template endpoint | `/v1/slices/{id}/e2b-template` | `internal/httpapi/slices.go` |
| Proto: SliceInfo | `e2b_template_id` field 9 | `proto/admin/admin_service.proto:163` |
| Proto: GetSliceByNameResponse | `e2b_template_id` field 6 | `proto/slice/slice_service.proto:302` |
| Integration tests | `"provider": "e2b"`, `"e2bTemplateId"` | `workflow_test/integration_test.go:1528-1530` |
| Server routing | `/e2b-template` suffix match | `servers/core/main.go:95` |

Internal (not user-facing, keep as-is):

| Surface | E2B Reference | File |
|---------|--------------|------|
| AgentSession model | `Provider`, `E2BTemplateID`, `E2BSandboxID`, `E2BRegion` | `internal/models/agent_session.go` |
| Agent session DB table | `provider`, `e2b_template_id`, `e2b_sandbox_id`, `e2b_region` | `migrations/002_agent_sessions.sql` |
| Agent session service | `CreateRequest.E2BTemplateID`, provider validation | `internal/agentsession/service.go` |

## Target State (After)

### User-facing API

Session creation:

```json
POST /v1/agent-sessions
{
  "sliceId": "auth-svc",
  "environment": "node20",
  "idleTimeoutSec": 1800,
  "ttlSec": 14400,
  "env": {"DEBUG": "1"}
}
```

Session creation response:

```json
{
  "sessionId": "sess_abc",
  "sliceId": "auth-svc",
  "environment": "node20",
  "state": "creating",
  "ws": {"url": "wss://...", "token": "...", "expiresAt": "..."},
  "createdAt": "...",
  "idleTimeoutSec": 1800,
  "ttlSec": 14400
}
```

Get session response:

```json
{
  "sessionId": "sess_abc",
  "sliceId": "auth-svc",
  "environment": "node20",
  "state": "running",
  "lastActivityAt": "...",
  "idleTimeoutSec": 1800,
  "ttlSec": 14400,
  "createdAt": "..."
}
```

Fields removed from user-facing responses: `provider`, `e2bTemplateId`, `e2bSandboxId`.

Fields removed from user-facing requests: `provider`, `e2bTemplateId`, `e2bRegion`.

### Slice configuration

Proto messages:

```protobuf
message SliceInfo {
  // ...existing fields 1-8...
  string environment = 9;   // was: e2b_template_id
}

message GetSliceByNameResponse {
  // ...existing fields 1-5...
  string environment = 6;   // was: e2b_template_id
}
```

HTTP endpoint:

```
GET /v1/slices/{id}/environment     → {"sliceId": "...", "environment": "node20"}
PUT /v1/slices/{id}/environment     ← {"environment": "node20"}
```

### Environment registry

```
GET    /v1/environments             → list all registered environments
POST   /v1/environments             ← {"name": "node20", "displayName": "Node.js 20", ...}
GET    /v1/environments/{name}      → get one
PUT    /v1/environments/{name}      ← update
DELETE /v1/environments/{name}      → delete
```

Admin-only fields (not in config file, only via API):

```json
{
  "name": "node20",
  "displayName": "Node.js 20",
  "provider": "e2b",
  "providerId": "tmpl-abc123",
  "region": "us-west-2",
  "createdBy": "admin",
  "createdAt": "...",
  "updatedAt": "..."
}
```

### Config file

Path: `.gitslice/config.yaml` (committed to repo, lives in root slice)

```yaml
# Named environments for use by slices in this repo.
# The actual provider mapping (e.g. E2B template ID) is managed
# server-side via the /v1/environments API.
environments:
  node20:
    display_name: "Node.js 20"
  python311-ml:
    display_name: "Python 3.11 + ML libs"

# Per-slice defaults
slices:
  auth-service:
    environment: node20
  ml-pipeline:
    environment: python311-ml

# Fallback for slices without an explicit environment
defaults:
  environment: node20
```

### Resolution chain

When creating an agent session, the environment is resolved in this order:

```
1. Session request has "environment" field?  → use it
2. Slice has "environment" set?              → use it
3. Config file has defaults.environment?     → use it
4. → Error: "no environment configured for this slice"

Then resolve environment name to provider config:
  environments table: name="node20" → provider="e2b", provider_id="tmpl-abc123", region="us-west-2"

If environment name not found in registry:
  → Error: "unknown environment: node20"
```

## Data Model

### New model: `Environment`

```go
// internal/models/environment.go

type Environment struct {
    Name        string    `json:"name"`          // primary key, e.g. "node20"
    DisplayName string    `json:"display_name"`  // "Node.js 20"
    Provider    string    `json:"provider"`       // "e2b" (internal)
    ProviderID  string    `json:"provider_id"`    // E2B template ID (internal)
    Region      string    `json:"region"`         // default region
    CreatedBy   string    `json:"created_by"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

### Changed model: `Slice`

```go
type Slice struct {
    // ...existing fields...
    Environment string `json:"environment,omitempty"` // was: E2BTemplateID
}
```

### Unchanged (internal): `AgentSession`

The `AgentSession` model keeps its E2B-specific fields. These are internal plumbing — the session service resolves environment name → provider config and populates them.

## Database Schema

### New table: `environments`

```sql
-- Migration: 004_environments.sql

CREATE TABLE IF NOT EXISTS environments (
    name TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT 'e2b',
    provider_id TEXT NOT NULL,
    region TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_environments_provider
    ON environments (provider);
```

### Rename slice column

```sql
-- Migration: 005_slice_environment_rename.sql

ALTER TABLE slices RENAME COLUMN e2b_template_id TO environment;
```

## Storage Interface Changes

### New methods

```go
// Environment registry
CreateEnvironment(ctx context.Context, env *models.Environment) error
GetEnvironment(ctx context.Context, name string) (*models.Environment, error)
ListEnvironments(ctx context.Context, limit, offset int) ([]*models.Environment, error)
UpdateEnvironment(ctx context.Context, env *models.Environment) error
DeleteEnvironment(ctx context.Context, name string) error
```

### Renamed methods

```
UpdateSliceE2BTemplateID(sliceID, templateID)  →  UpdateSliceEnvironment(sliceID, envName)
```

### Queries updated

All `SELECT ... e2b_template_id` on slices become `SELECT ... environment`.

## Config File: `.gitslice/config.yaml`

### Location

The file lives at `.gitslice/config.yaml` within the root slice's file tree. It is a regular file tracked by gitslice — editable via changeset, visible in the file browser.

### Schema

```go
// internal/sliceconfig/config.go

type SliceConfigFile struct {
    Environments map[string]EnvEntry   `yaml:"environments"`
    Slices       map[string]SliceEntry `yaml:"slices"`
    Defaults     DefaultsEntry         `yaml:"defaults"`
}

type EnvEntry struct {
    DisplayName string `yaml:"display_name"`
}

type SliceEntry struct {
    Environment string `yaml:"environment"`
}

type DefaultsEntry struct {
    Environment string `yaml:"environment"`
}
```

### Sync: file → DB

Trigger points:

1. **After `ImportGitRepo`** — if the imported repo contains `.gitslice/config.yaml`, parse it and apply.
2. **After `MergeChangeset`** — if the changeset modifies `.gitslice/config.yaml`, re-parse and apply.
3. **On-demand** — new HTTP endpoint `POST /v1/config/sync` for manual trigger.

Apply logic:

```
for each slices entry in config:
    slice = storage.GetSliceByName(name)
    if slice != nil && slice.Environment != entry.Environment:
        storage.UpdateSliceEnvironment(slice.ID, entry.Environment)

for each environments entry in config:
    env = storage.GetEnvironment(name)
    if env == nil:
        // Environment name declared in file but not registered server-side.
        // Log warning; do not auto-create (admin must register provider mapping).
        log.Warn("environment %q in config file but not registered", name)
    elif env.DisplayName != entry.DisplayName:
        env.DisplayName = entry.DisplayName
        storage.UpdateEnvironment(env)
```

The config file does NOT contain provider IDs. It declares environment names and display names. The provider mapping (`name → e2b template ID`) must be registered server-side by an admin via the `/v1/environments` API.

### Sync: DB → file

Not automatic. If a user changes the environment via the UI/API, the web UI can show a hint: "Update `.gitslice/config.yaml` to persist this in your repo." A future enhancement could auto-generate a changeset.

---

## PR Breakdown

### PR1 — Environment Registry

**Scope:** New `environments` table, model, storage methods, HTTP CRUD endpoints.

**Files to create:**

| File | Purpose |
|------|---------|
| `internal/models/environment.go` | `Environment` struct |
| `internal/storage/migrations/004_environments.sql` | `environments` table |
| `internal/httpapi/environments.go` | HTTP CRUD handlers |

**Files to modify:**

| File | Change |
|------|--------|
| `internal/storage/storage.go` | Add `CreateEnvironment`, `GetEnvironment`, `ListEnvironments`, `UpdateEnvironment`, `DeleteEnvironment` |
| `internal/storage/memory.go` | In-memory implementation |
| `internal/storage/postgres_native.go` | Postgres implementation |
| `servers/core/main.go` | Register `/v1/environments` and `/v1/environments/` routes |

**Tests:**

- Storage compliance test for environment CRUD (create, get, list, update, delete, not-found).
- HTTP handler tests for each endpoint (auth, validation, 404, conflict).

**Acceptance criteria:**

1. `POST /v1/environments` creates an environment with name, displayName, provider, providerId, region.
2. `GET /v1/environments` lists all environments (paginated).
3. `GET /v1/environments/{name}` returns one environment.
4. `PUT /v1/environments/{name}` updates display name, provider ID, region.
5. `DELETE /v1/environments/{name}` removes an environment.
6. Duplicate name on create returns 409 Conflict.
7. User-facing responses include: `name`, `displayName`, `region`, `createdAt`, `updatedAt`.
8. User-facing responses do NOT include: `provider`, `providerId` (these are admin-only; exposed only with an admin header or future role system).

---

### PR2 — Slice Environment Field Rename

**Scope:** Rename `e2b_template_id` → `environment` everywhere on the slice surface. This is a rename-only PR — no behavior change.

**Files to modify:**

| File | Change |
|------|--------|
| `internal/models/slice.go` | `E2BTemplateID string` → `Environment string` |
| `internal/storage/migrations/005_slice_environment_rename.sql` | New migration: `ALTER TABLE slices RENAME COLUMN e2b_template_id TO environment` |
| `internal/storage/storage.go` | `UpdateSliceE2BTemplateID()` → `UpdateSliceEnvironment()` |
| `internal/storage/memory.go` | Rename method + field references |
| `internal/storage/postgres_native.go` | Rename method, update all SQL queries (`e2b_template_id` → `environment`) |
| `internal/httpapi/slices.go` | Rename endpoint `/e2b-template` → `/environment`, rename request/response types and JSON fields (`e2bTemplateId` → `environment`) |
| `internal/httpapi/agent_sessions.go` | `slice.E2BTemplateID` → `slice.Environment` in fallback logic |
| `proto/admin/admin_service.proto` | `e2b_template_id` → `environment` on `SliceInfo` (field number 9 stays) |
| `proto/slice/slice_service.proto` | `e2b_template_id` → `environment` on `GetSliceByNameResponse` (field number 6 stays) |
| `services/admin/server.go` | `E2BTemplateId:` → `Environment:` in `SliceInfo` population |
| `services/slice/server.go` | `E2BTemplateId:` → `Environment:` in `GetSliceByNameResponse` population |
| `servers/core/main.go` | Route suffix match: `/e2b-template` → `/environment` |
| `internal/storage/storage_test.go` | Update any test references |

**Tests:**

- All existing storage and service tests must still pass.
- Verify renamed HTTP endpoint works: `GET/PUT /v1/slices/{id}/environment`.
- Verify proto field name change propagates correctly.

**Acceptance criteria:**

1. No string `e2b` or `E2B` appears in any user-facing API response, proto field name, or HTTP path.
2. The renamed DB column works for both fresh installs (migration 003+005) and existing installs (005 renames).
3. All existing tests pass with renamed fields.

---

### PR3 — Session Resolution via Environment

**Scope:** Agent session creation resolves the `environment` name to provider config via the environments table, instead of accepting raw E2B fields from users.

**Files to modify:**

| File | Change |
|------|--------|
| `internal/httpapi/agent_sessions.go` | Replace `e2bTemplateId`/`e2bRegion`/`provider` request fields with single `environment` field. Remove E2B fields from responses. Resolution logic: environment name → `storage.GetEnvironment()` → populate internal `CreateRequest`. |
| `internal/agentsession/service.go` | `CreateRequest` keeps internal `E2BTemplateID`/`E2BRegion`/`Provider` fields (filled by HTTP handler after resolution). No change to service logic itself. |
| `workflow_test/integration_test.go` | Update session creation payload: `{"sliceId": "...", "environment": "test-env"}`. Pre-seed an environment in test setup. |

**Request/response type changes in `agent_sessions.go`:**

```go
// Before
type createAgentSessionRequest struct {
    SliceID        string            `json:"sliceId"`
    Provider       string            `json:"provider"`
    E2BTemplateID  string            `json:"e2bTemplateId"`
    E2BRegion      string            `json:"e2bRegion"`
    IdleTimeoutSec int               `json:"idleTimeoutSec"`
    TTLSec         int               `json:"ttlSec"`
    Env            map[string]string `json:"env"`
}

// After
type createAgentSessionRequest struct {
    SliceID        string            `json:"sliceId"`
    Environment    string            `json:"environment"`
    IdleTimeoutSec int               `json:"idleTimeoutSec"`
    TTLSec         int               `json:"ttlSec"`
    Env            map[string]string `json:"env"`
}
```

```go
// Before
type createAgentSessionResponse struct {
    SessionID      string            `json:"sessionId"`
    SliceID        string            `json:"sliceId"`
    Provider       string            `json:"provider"`
    E2BTemplateID  string            `json:"e2bTemplateId"`
    State          string            `json:"state"`
    WS             wsConnectResponse `json:"ws"`
    CreatedAt      string            `json:"createdAt"`
    IdleTimeoutSec int               `json:"idleTimeoutSec"`
    TTLSec         int               `json:"ttlSec"`
}

// After
type createAgentSessionResponse struct {
    SessionID      string            `json:"sessionId"`
    SliceID        string            `json:"sliceId"`
    Environment    string            `json:"environment"`
    State          string            `json:"state"`
    WS             wsConnectResponse `json:"ws"`
    CreatedAt      string            `json:"createdAt"`
    IdleTimeoutSec int               `json:"idleTimeoutSec"`
    TTLSec         int               `json:"ttlSec"`
}
```

```go
// Before
type getAgentSessionResponse struct {
    SessionID      string `json:"sessionId"`
    SliceID        string `json:"sliceId"`
    Provider       string `json:"provider"`
    E2BSandboxID   string `json:"e2bSandboxId,omitempty"`
    State          string `json:"state"`
    // ...
}

// After
type getAgentSessionResponse struct {
    SessionID      string `json:"sessionId"`
    SliceID        string `json:"sliceId"`
    Environment    string `json:"environment"`
    State          string `json:"state"`
    // ... (e2bSandboxId and provider removed)
}
```

**Resolution logic in `HandleCollection` (create session handler):**

```go
// Resolve environment name
envName := strings.TrimSpace(req.Environment)
if envName == "" {
    envName = slice.Environment  // from slice default
}
if envName == "" {
    writeError(w, http.StatusBadRequest, "no environment configured for this slice")
    return
}

env, err := a.st.GetEnvironment(r.Context(), envName)
if err != nil {
    writeError(w, http.StatusBadRequest, "unknown environment: "+envName)
    return
}

session, token, err := a.svc.CreateSession(r.Context(), userID, agentsession.CreateRequest{
    SliceID:        req.SliceID,
    Provider:       env.Provider,       // from environment registry
    E2BTemplateID:  env.ProviderID,     // from environment registry
    E2BRegion:      env.Region,         // from environment registry
    IdleTimeoutSec: req.IdleTimeoutSec,
    TTLSec:         req.TTLSec,
    Env:            req.Env,
})
```

**Get session handler** — needs to reverse-resolve the E2B template ID back to an environment name for the response. Options:

- (a) Store the environment name on the `AgentSession` model (new field, simplest).
- (b) Look up by provider+providerID in the environments table.

Recommend **(a)**: add `EnvironmentName string` to `AgentSession` model and `agent_sessions` table. Set it at creation time. This avoids reverse-lookup issues if the environment registry changes later.

**Additional migration:**

```sql
-- Migration: 006_agent_session_environment_name.sql
ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS environment_name TEXT NOT NULL DEFAULT '';
```

**Tests:**

- HTTP test: create session with `{"environment": "node20"}` succeeds when environment is registered.
- HTTP test: create session with unknown environment returns 400.
- HTTP test: create session without environment uses slice default.
- HTTP test: create session without environment and no slice default returns 400.
- HTTP test: get session response contains `environment`, not `provider`/`e2bTemplateId`.
- Integration test updated to seed environment and use `"environment"` field.

**Acceptance criteria:**

1. User sends `"environment": "node20"` instead of `"provider"/"e2bTemplateId"/"e2bRegion"`.
2. User receives `"environment": "node20"` in responses, never sees `provider`/`e2bTemplateId`/`e2bSandboxId`.
3. Resolution chain works: request → slice default → error.
4. Unknown environment name returns clear error message.
5. All E2B fields are internal to the backend — present in `AgentSession` model and DB, but never in HTTP responses.

---

### PR4 — Config File Reader

**Scope:** Parse `.gitslice/config.yaml` from the file tree and apply environment settings to slices. Hook into import and merge flows.

**Files to create:**

| File | Purpose |
|------|---------|
| `internal/sliceconfig/config.go` | YAML schema types + `ParseConfig([]byte)` |
| `internal/sliceconfig/apply.go` | `ApplyConfig(ctx, storage, config)` — applies parsed config to DB |
| `internal/sliceconfig/config_test.go` | Unit tests for parsing and apply logic |

**Files to modify:**

| File | Change |
|------|--------|
| `services/admin/server.go` | After `ImportGitRepo` completes, call `sliceconfig.ApplyFromFileTree(ctx, storage)` |
| `services/slice/server.go` | After `MergeChangeset` completes, if changeset touches `.gitslice/config.yaml`, call apply |
| `go.mod` | Add `gopkg.in/yaml.v3` dependency (if not already present) |

**Config file path constant:**

```go
const ConfigFilePath = ".gitslice/config.yaml"
```

**Apply logic:**

```go
func ApplyConfig(ctx context.Context, st storage.Storage, cfg *SliceConfigFile) error {
    // 1. Update environment display names
    for name, entry := range cfg.Environments {
        env, err := st.GetEnvironment(ctx, name)
        if err != nil {
            log.Printf("sliceconfig: environment %q declared in config but not registered", name)
            continue
        }
        if entry.DisplayName != "" && env.DisplayName != entry.DisplayName {
            env.DisplayName = entry.DisplayName
            env.UpdatedAt = time.Now()
            _ = st.UpdateEnvironment(ctx, env)
        }
    }

    // 2. Apply per-slice environment defaults
    for sliceName, entry := range cfg.Slices {
        if entry.Environment == "" {
            continue
        }
        slice, err := st.GetSliceByName(ctx, sliceName)
        if err != nil {
            continue // slice doesn't exist yet
        }
        if slice.Environment != entry.Environment {
            _ = st.UpdateSliceEnvironment(ctx, slice.ID, entry.Environment)
        }
    }

    return nil
}
```

**Reading the config from file tree:**

```go
func ApplyFromFileTree(ctx context.Context, st storage.Storage) error {
    rootSlice, err := st.GetRootSlice(ctx)
    if err != nil {
        return err
    }
    file, err := st.GetSliceFileByPath(ctx, rootSlice.ID, ConfigFilePath)
    if err != nil {
        return nil // no config file; not an error
    }
    cfg, err := ParseConfig(file.Content)
    if err != nil {
        return fmt.Errorf("invalid %s: %w", ConfigFilePath, err)
    }
    return ApplyConfig(ctx, st, cfg)
}
```

**Tests:**

- Parse valid YAML with all sections.
- Parse YAML with missing sections (partial config).
- Parse invalid YAML returns error.
- Apply updates slice environment when config differs from DB.
- Apply logs warning for unregistered environment names.
- Apply skips slices that don't exist.
- Integration: import a repo containing `.gitslice/config.yaml`, verify slices get environment set.

**Acceptance criteria:**

1. Importing a repo with `.gitslice/config.yaml` applies the environment settings.
2. Merging a changeset that modifies `.gitslice/config.yaml` re-applies settings.
3. Unknown environment names in config file produce a log warning, not an error.
4. Missing config file is silently ignored.
5. Malformed YAML returns an error to the caller.

---

### PR5 — UI Settings Panel

**Scope:** Add environment selection to the web UI for each slice.

**Files to create/modify:**

| File | Change |
|------|--------|
| `web/src/components/SliceSettings.jsx` | New component: environment dropdown, save button |
| `web/src/components/RepoBrowser.jsx` | Add "Settings" tab that renders `SliceSettings` |
| `web/src/utils/api.js` | Add `fetchEnvironments()`, `getSliceEnvironment()`, `updateSliceEnvironment()` helpers |

**UI flow:**

1. User navigates to a slice in the file browser.
2. User clicks "Settings" tab (new tab alongside Files/History).
3. Settings panel shows a dropdown of registered environments.
4. Current environment is pre-selected (from `GET /v1/slices/{id}/environment`).
5. User selects a different environment and clicks Save.
6. `PUT /v1/slices/{id}/environment` is called.
7. Success toast: "Environment updated. To persist in your repo, update `.gitslice/config.yaml`."

**API calls:**

```javascript
// Fetch available environments for dropdown
GET /v1/environments → [{name, displayName}, ...]

// Get current slice environment
GET /v1/slices/{id}/environment → {sliceId, environment}

// Update slice environment
PUT /v1/slices/{id}/environment ← {environment: "node20"}
```

**Acceptance criteria:**

1. Settings tab visible when viewing a non-root slice.
2. Dropdown lists all registered environments by display name.
3. Current environment is pre-selected.
4. Save calls the API and shows success feedback.
5. No E2B branding visible anywhere in the UI.

---

## Migration Path

For existing deployments that already have `e2b_template_id` values on slices and in session creation:

1. **PR1** is additive — new table, no breaking changes.
2. **PR2** renames the column — existing values become environment references. If an existing slice has `e2b_template_id = "tmpl-abc123"`, after rename it becomes `environment = "tmpl-abc123"`. Admin should register an environment with `name = "tmpl-abc123"` to maintain compatibility, or update slices to use new names.
3. **PR3** changes the HTTP API contract — this is a **breaking change** for API consumers. Coordinate with frontend deployment. Old `e2bTemplateId` field is no longer accepted. Clients must switch to `environment` field.
4. **PR4** and **PR5** are additive.

If backward compatibility is needed for PR3, the session creation handler can accept both `environment` and `e2bTemplateId` during a transition period, preferring `environment` when both are present, and log a deprecation warning when the old field is used.

## Open Questions

1. Should environments be scoped per-organization, or global? (v1: global)
2. Should the config file support environment-level `env` vars (e.g. default `ENV` map per environment)? (v1: no)
3. Should we support an `environment` field on `CreateSliceFromFolderRequest` so new slices get an environment at creation time? (v1: defer, set via API/config after creation)
4. Should `DELETE /v1/environments/{name}` fail if slices reference it, or orphan them? (v1: fail with 409)
