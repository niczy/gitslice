# Slice Environment Materialization Design

## Implementation Status

- Current status: `not_started`
- Last updated: `2026-05-15`

---

## Executive Summary

Custom slices sometimes need local or CI-only files that should exist in a
checkout but must not be tracked by that custom slice. Common examples are
`.env.local`, `.npmrc`, language-specific credentials files, and generated
tooling config.

The source of truth for those requirements should be a tracked file in the
user's home slice:

```text
/{home}/.gitslice/slices/{slice_slug}/env.yaml
```

This is the Option A sidecar design. It works with the current slice model,
where custom slices can only track files and folders from the global home tree.
It avoids adding reserved custom-slice metadata files, avoids scanning every
tracked folder during checkout, and keeps requirements reviewable as normal
home-slice file changes.

The requirements file stores only declarations and templates. Secret values are
never stored in this file. Local checkout secrets live in a local secret store,
and CI secrets live in the CI secret store. Materialized files are written into
checkouts or CI workspaces and are explicitly ignored by gitslice status, diff,
export, agent local-change collection, cache upload, and artifact upload.

---

## Goals

1. Let a custom slice declare checkout materialization requirements without
   tracking those generated files.
2. Keep requirements file-backed, reviewable, and versioned through the home
   slice.
3. Make checkout fast by loading one known path for a slice instead of scanning
   all tracked folders.
4. Keep secret values out of tracked files, changesets, snapshots, logs, agent
   events, CI artifacts, and caches.
5. Use the same materialization model for local checkout, local agent session
   checkout, and CI workspace materialization.
6. Preserve CI's existing trust boundary: policy and secret exposure are based
   on trusted home head, not unmerged candidate content.

## Non-Goals

- Adding first-class custom-slice control files independent of the global file
  tree.
- Storing secret values in `/{home}/.gitslice`.
- Making `.gitignore` the source of truth for generated-file exclusion.
- Scanning each tracked folder for env manifests.
- Letting unmerged changes request new CI secrets and immediately receive them.

---

## File Placement

### Requirements File

Path:

```text
/{home}/.gitslice/slices/{slice_slug}/env.yaml
```

This path is ordinary tracked home-slice content. Editing it produces normal
home-slice file changes and can be reviewed, exported, merged, and rolled back.

For example, a slice with slug `payments-api` uses:

```text
/{home}/.gitslice/slices/payments-api/env.yaml
```

The checkout implementation must not discover this file by scanning the custom
slice checkout. It resolves the one canonical home-slice path from the slice
slug and fetches that file directly from the selected home commit.

### Secret Values

Secret values are deliberately not placed under `/{home}/.gitslice`, because
that directory is tracked by the home slice.

Recommended local storage:

```text
$XDG_STATE_HOME/gitslice/secrets/{home_id}/{slice_slug}/{profile}.yaml
```

Fallback when `XDG_STATE_HOME` is unset:

```text
~/.local/state/gitslice/secrets/{home_id}/{slice_slug}/{profile}.yaml
```

The CLI must create local secret files with `0600` permissions and should later
support OS keychain backends. Server-side CI secrets belong in a separate
encrypted secret store with no read-back API.

### Materialized Files

Materialized files are written inside the checkout or CI workspace:

```text
<checkout-root>/.env.local
<checkout-root>.npmrc
<ci-workspace>/.env.test
```

Every materialized path must be recorded in checkout metadata so gitslice never
exports it by accident.

---

## Requirements Schema

Example:

```yaml
version: 1

profiles:
  local:
    files:
      - path: .env.local
        mode: "0600"
        sensitive: true
        template: |
          OPENAI_API_KEY={{ secret "OPENAI_API_KEY" }}
          DATABASE_URL={{ secret "DATABASE_URL" }}
        required_secrets:
          - OPENAI_API_KEY
          - DATABASE_URL

      - path: .npmrc
        mode: "0600"
        sensitive: true
        template: |
          //registry.npmjs.org/:_authToken={{ secret "NPM_TOKEN" }}
        required_secrets:
          - NPM_TOKEN

  ci:
    files:
      - path: .env.test
        mode: "0600"
        sensitive: true
        template: |
          DATABASE_URL={{ secret "CI_DATABASE_URL" }}
        required_secrets:
          - CI_DATABASE_URL

ignored_paths:
  - .env.local
  - .env.test
  - .npmrc
```

### Top-Level Fields

- `version`: Required schema version.
- `profiles`: Named materialization profiles such as `local`, `agent`, `ci`,
  `staging`, or `prod`.
- `ignored_paths`: Additional paths that must be ignored even if no file is
  materialized for the active profile.

### File Fields

- `path`: Relative path inside the checkout or CI workspace.
- `mode`: File mode. Default `0600` for sensitive files, otherwise `0644`.
- `sensitive`: Defaults to `true` when the template uses secrets. Sensitive
  files are excluded from status/export/cache/artifact surfaces.
- `template`: Text template rendered with secret references and safe variables.
- `required_secrets`: Secret names that must exist before rendering.
- `optional_secrets`: Secret names that may be absent and render as empty.

### Template Inputs

Allowed template functions:

```text
{{ secret "NAME" }}       required secret value
{{ env "NAME" }}          non-secret runtime environment variable
{{ slice "field" }}       slice metadata such as slug or id
{{ checkout "field" }}    checkout metadata such as commit hash
```

The renderer must reject unknown functions and unresolved required secrets.
Secret values must not be logged, stored in events, or included in error
messages.

### Path Validation

The parser must reject:

- absolute paths
- `..` escapes
- paths under `.gs/`
- paths equal to `.gitslice/slices/{slice_slug}/env.yaml`
- symlink materialization in v1
- host-specific absolute paths in templates

The parser may allow materializing under `.gitslice/` in the checkout only if
the path is not the source requirements file and is explicitly marked
`sensitive: false`. V1 should avoid this unless needed.

---

## Checkout Lifecycle

### Local Checkout

`gs slice checkout <custom-slice>` should:

1. Resolve the custom slice id and slug.
2. Checkout tracked custom-slice files normally.
3. Fetch exactly one home-slice sidecar file:

   ```text
   /{home}/.gitslice/slices/{slice_slug}/env.yaml
   ```

4. If the file is missing, continue without materialization.
5. Parse and validate the file.
6. Select the materialization profile. Default: `local`.
7. Resolve secret values from local secret storage.
8. Render files into the checkout.
9. Record materialized and ignored paths in `.gs/index`.
10. Print a concise summary:

   ```text
   Materialized environment files: 2
   Missing secrets: 1
     DATABASE_URL
   ```

Missing required secrets should not fail checkout by default, because users may
want to inspect code before configuring secrets. Commands that need the files,
such as `gs env materialize --strict`, `gs agent run`, or CI, can require strict
resolution.

### Agent Session Checkout

Local agent sessions already create per-session checkouts. Before starting
Codex, Claude, or another local agent, the runner should run the same
materialization flow with profile selection:

```text
agent profile order:
  1. requested profile from session metadata
  2. "agent" profile if present
  3. "local" profile
```

The session metadata should record:

- requirements path
- requirements content hash
- selected profile
- materialized path list
- missing secret names, without values

The agent conversation can show missing secret names so the user understands why
a command may fail, but it must never show secret values.

### Sync And Restore

`gs slice sync` should re-evaluate the sidecar file after updating tracked
content. If the requirements hash changed, the CLI should re-render generated
files after warning about local edits to previously materialized sensitive
files.

`gs slice restore` should not delete materialized sensitive files unless the
user passes an explicit flag such as:

```bash
gs slice restore --materialized
```

The default restore behavior should keep secrets intact while still restoring
tracked files.

---

## Checkout Metadata

Extend `.gs/index` with materialization metadata:

```json
{
  "materialization": {
    "requirements_path": "/home_nicholas/.gitslice/slices/payments-api/env.yaml",
    "requirements_home_commit": "cmt_...",
    "requirements_hash": "sha256:...",
    "profile": "local",
    "materialized_paths": [
      {
        "path": ".env.local",
        "mode": "0600",
        "sensitive": true,
        "content_hash": "sha256:..."
      }
    ],
    "ignored_paths": [
      ".env.local",
      ".npmrc"
    ]
  }
}
```

This metadata is local checkout state and is not exported as slice content.

The following commands must respect `ignored_paths` and `sensitive`
materialized paths:

- `gs slice status`
- `gs slice diff`
- `gs slice export`
- `gs changeset export`
- local agent change collection
- local change drawer APIs
- search overlay generation

If a materialized path is also tracked by the custom slice, checkout must fail
with a clear conflict. A generated secret file cannot safely share a path with
tracked content.

---

## CLI Surface

### Requirements Authoring

The requirements file is normal home-slice content, so users can edit it
directly. The CLI should add helper commands that write the file safely:

```bash
gs env init --slice payments-api
gs env edit --slice payments-api
gs env validate --slice payments-api
```

`gs env init` writes:

```text
/{home}/.gitslice/slices/{slice_slug}/env.yaml
```

and exports it through the home slice workflow.

### Secret Values

Local secrets:

```bash
gs secret set OPENAI_API_KEY --slice payments-api --profile local
gs secret list --slice payments-api --profile local
gs secret unset OPENAI_API_KEY --slice payments-api --profile local
```

CI secrets:

```bash
gs ci secret set CI_DATABASE_URL --home {home}
gs ci secret list --home {home}
gs ci secret unset CI_DATABASE_URL --home {home}
```

Secret list commands show names and configured/missing status only.

### Materialization

```bash
gs env materialize --slice payments-api --profile local
gs env materialize --profile agent --strict
gs env status
```

`--strict` fails when required secrets are missing. Agent startup and CI should
use strict mode.

---

## Server And API Design

The source of truth remains the tracked file. The server may index parsed
requirements for fast UI display and CI planning.

### Suggested Index Table

```text
slice_env_requirement_index
  home_id text
  slice_id text
  slice_slug text
  requirements_path text
  home_commit_hash text
  requirements_hash text
  parsed_json jsonb
  parse_error text
  updated_at timestamptz
```

This table is cache/index data. It can be rebuilt from home-slice content and
must not be the authoritative requirements store.

### Suggested gRPC APIs

Requirements are exposed gRPC-first with grpc-gateway bindings:

```protobuf
service SliceEnvironmentService {
  rpc GetSliceEnvRequirements(GetSliceEnvRequirementsRequest)
      returns (GetSliceEnvRequirementsResponse);
  rpc ValidateSliceEnvRequirements(ValidateSliceEnvRequirementsRequest)
      returns (ValidateSliceEnvRequirementsResponse);
  rpc GetSliceEnvSecretStatus(GetSliceEnvSecretStatusRequest)
      returns (GetSliceEnvSecretStatusResponse);
}
```

These APIs should return requirements, parse errors, and secret status by name.
They must not return secret values.

Editing requirements should go through normal file editing and changeset
workflow, not a DB mutation API. The web UI can provide a form editor that
writes `/{home}/.gitslice/slices/{slice_slug}/env.yaml` as a home-slice file
change.

---

## Web UI

The slice settings page should show:

- requirements source path
- source home commit hash
- parse status
- profiles
- materialized file paths
- required secret names
- configured/missing secret status for the active profile

The UI should support:

- create/edit requirements as a home-slice file changeset
- set/update/delete secret values through a no-readback secret form
- trigger `gs env materialize` instructions for local checkout
- show whether the current agent checkout has the expected requirements hash

The UI must not display secret values. It should display only:

```text
OPENAI_API_KEY configured
DATABASE_URL missing
```

---

## CI Design Impact

The CI system already has a home-level platform config:

```text
/{home}/.gitslice/ci.yaml
```

Environment materialization adds a second home-slice config input:

```text
/{home}/.gitslice/slices/{slice_slug}/env.yaml
```

Both are home-slice files, but they have different trust roles:

- `ci.yaml` is platform policy.
- `env.yaml` is slice-specific materialization requirements.

### Trust Boundary

CI must not let an unmerged changeset expose new secrets to itself.

For CI runs on a custom-slice changeset:

1. Read platform policy from the trusted merged home head.
2. Read env requirements from the trusted merged home head.
3. Read folder CI manifests from the candidate tree, as currently designed.
4. Materialize secret-backed files only if requested secrets are allowed by the
   trusted platform policy.

If a changeset modifies:

```text
/{home}/.gitslice/slices/{slice_slug}/env.yaml
```

then CI should validate the candidate file's schema and policy compatibility,
but it should not grant newly requested secrets until that file is merged into
trusted home head.

This mirrors the existing CI rule for `/{home}/.gitslice/ci.yaml`: policy
changes are tested under the old trusted policy and apply to future changesets
after merge.

### CI Planning Inputs

Add env requirements to the CI plan input set:

- changeset version id
- base commit hash
- platform config hash
- matched folder manifest hashes
- env requirements path
- env requirements hash
- selected materialization profile
- requested secret names, not values
- changed path list

The `plan_hash` should include the env requirements hash and selected profile.
If requirements change, previous CI results are stale for future runs that use
the new requirements.

Secret values should not be included in `plan_hash`. CI run metadata may record
secret version ids for audit and reproducibility, but never secret values.

### Runner Materialization

Before executing job commands, a CI runner should:

1. Claim the job lease.
2. Materialize the candidate tree into an ephemeral workspace.
3. Fetch the trusted env requirements file for the target slice.
4. Select profile `ci`, falling back to `local` only if platform policy allows
   it. Recommended v1 behavior: require an explicit `ci` profile.
5. Validate requested secrets against `/{home}/.gitslice/ci.yaml`.
6. Resolve secret values from the CI secret store.
7. Render files into the workspace.
8. Mark generated sensitive paths as excluded from cache and artifacts.
9. Run job commands.

The runner should expose non-secret metadata:

```text
GS_ENV_PROFILE=ci
GS_ENV_REQUIREMENTS_PATH=/.gitslice/slices/payments-api/env.yaml
GS_ENV_REQUIREMENTS_HASH=sha256:...
GS_ENV_MATERIALIZED_FILES=.env.test
```

It should not expose secret values except through the rendered files or explicit
job environment variables allowed by CI policy.

### Secrets Policy

Extend `/{home}/.gitslice/ci.yaml` secrets policy to cover both job env refs and
materialized file refs:

```yaml
secrets:
  allow:
    - CI_DATABASE_URL
    - NPM_TOKEN
  materialization:
    allow_profiles:
      - ci
    deny_artifacts: true
    deny_cache: true
```

CI fails planning if an env requirements file requests a secret not allowed by
trusted platform policy.

### Cache And Artifacts

Sensitive materialized paths are never cached or uploaded as artifacts. This is
true even if a job manifest explicitly lists a broad cache or artifact path such
as:

```yaml
cache:
  paths:
    - "."
artifacts:
  paths:
    - "."
```

The final cache/artifact path set must subtract sensitive materialized paths.

### Logs And Redaction

The runner must register all resolved secret values with the log redactor before
job execution. Logs should mask exact values and common encoded forms where
practical.

Planning and materialization errors should mention only secret names:

```text
missing secret CI_DATABASE_URL
```

not values or rendered template fragments.

---

## Performance

Checkout and CI planning must be O(1) with respect to tracked folder count for
env requirements:

```text
requirements_path = /{home}/.gitslice/slices/{slice_slug}/env.yaml
```

The system performs at most one direct metadata lookup and one blob fetch for a
slice's requirements file. Missing-file results can be cached by home commit and
slice slug.

The parsed server index avoids repeatedly parsing YAML for web display and CI
planning, but it is not required for correctness.

---

## Failure Modes

### Missing Requirements File

Local checkout succeeds with:

```text
No environment materialization requirements found for slice payments-api.
```

CI proceeds without env materialization unless the platform policy or job
manifest requires it.

### Invalid Requirements File

Local checkout should warn and continue unless `--strict` is used.

Agent startup and CI should fail fast, because a broken requirements file makes
the runtime environment ambiguous.

### Missing Secrets

Local checkout warns and records missing secret names.

Agent startup and CI fail in strict mode before starting the agent or job.

### Generated Path Conflicts With Tracked Content

Fail before writing generated files. The user must either remove the tracked
file or choose a different generated path.

### Secret Appears In Diff

This should be structurally prevented by ignored materialized paths. As defense
in depth, export should scan generated sensitive paths and fail if one appears
in the pending export set.

---

## Migration Plan

1. Add checkout ignored-local-path support independent of env materialization.
2. Add parser and validator for `env.yaml`.
3. Add local secret storage and `gs secret` commands.
4. Add `gs env materialize` and wire it into `gs slice checkout`.
5. Wire materialization into local agent per-session checkouts.
6. Add server-side parsed requirements index for web and CI.
7. Add no-readback CI secret storage.
8. Wire CI planning and runners to materialize `ci` profile files.
9. Add web UI for requirements and secret status.

---

## Open Questions

1. If slice slugs are mutable, should the canonical directory use `slice_id`
   with slug aliases for readability?
2. Should local checkout default to non-strict materialization forever, or
   should slices be able to require strict materialization?
3. Should local secret storage use OS keychain as the default before file-based
   storage is supported?
4. Should CI require an explicit `ci` profile, or is fallback to `local` useful
   enough to justify the risk?
5. Should editing requirements from the web create a home-slice changeset
   automatically, or should it only modify the file in a local checkout?
