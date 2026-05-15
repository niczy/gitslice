# Path-Scoped Slice CI Design

## Implementation Status

- Current status: `implemented`
- Last updated: `2026-05-09`

---

## Executive Summary

Gitslice CI should be path-scoped, not slice-scoped.

Users own a canonical home tree at `/{home}`. The home tree contains one
home-level CI platform policy file:

```text
/{home}/.gitslice/ci.yaml
```

Each project or folder can then define a local manifest:

```text
/{home}/project-a/.gs-ci.yaml
/{home}/project-b/.gs-ci.yaml
/{home}/shared/lib/.gs-ci.yaml
```

The home-level file defines how CI is allowed to run: runner pools, images,
timeouts, caches, merge policy, secrets policy, and trigger defaults. Folder
manifests define what commands to run when files covered by that folder change.

This model fits custom slices because a file or folder can appear in multiple
slices under the same home. CI planning is resolved against canonical home
paths, so there is one deterministic CI answer for a changed path regardless of
which custom slice exported the changeset.

---

## Goals

1. Provide first-class Gitslice CI without requiring GitHub Actions.
2. Let users run jobs on their own VMs through outbound `gs runner` agents.
3. Resolve CI by changed home paths, not by slice identity alone.
4. Attach CI status to the exact immutable changeset version being merged.
5. Gate `gs changeset merge` on required checks for that exact version.
6. Keep conflict detection and path-head authority separate from CI.
7. Make the default workflow easy for a single user while leaving room for
   managed runners, org policy, secrets, caches, and artifacts later.

## Non-Goals

- Replacing path-head conflict checks.
- Requiring users to give Gitslice SSH access to their VMs.
- Building a fully managed untrusted compute platform in the first PR.
- Supporting arbitrary host absolute paths in manifests.
- Treating local checkout `.gs/` metadata or `~/.gitslice/` CLI state as
  versioned CI configuration.

---

## Terminology

- **Home tree**: The canonical tree rooted at `/{home}`.
- **Platform config**: `/{home}/.gitslice/ci.yaml`, the trusted home-level CI
  policy and runner configuration.
- **Folder manifest**: A per-folder file named `.gs-ci.yaml` that defines jobs
  for that folder and optionally for additional dependency paths.
- **Candidate tree**: The base home tree plus the latest changeset version
  overlay, materialized for CI before merge.
- **Changeset version**: The immutable version/snapshot produced by
  `gs slice export`. CI results are valid only for one exact version.
- **Runner**: A user-hosted process started with `gs runner start` that polls
  Gitslice for jobs and executes them on the user's VM.
- **Runner pool**: A named group of runners and execution defaults selected by
  platform config and job manifests.

---

## File Placement

### Home Platform Config

Path:

```text
/{home}/.gitslice/ci.yaml
```

This is versioned content in the home tree. It is not:

- local checkout metadata under `.gs/`
- global CLI state under `~/.gitslice/`
- a per-custom-slice directory

All custom slices under the same home resolve this same home-level platform
config.

### Folder Manifest

Default path:

```text
.gs-ci.yaml
```

Examples:

```text
/{home}/api/.gs-ci.yaml
/{home}/web/.gs-ci.yaml
/{home}/shared/lib/.gs-ci.yaml
```

Use a file in the folder, not `folder/.gitslice/ci.yaml`, because the
`.gitslice` directory is home-level policy space and should not pretend to be a
folder-local namespace.

---

## Home Platform Config Schema

Example:

```yaml
version: 1

triggers:
  changeset_export: true
  merge_requested: true
  manual: true

defaults:
  runner_pool: default
  image: gitslice/base:2026-05
  shell: bash
  timeout_seconds: 900
  working_directory: "."
  network: restricted

runner_pools:
  default:
    labels: ["linux", "docker"]
    executor: docker
    max_parallel_jobs_per_runner: 2
    allowed_images:
      - gitslice/base:2026-05
      - golang:1.24
      - node:22

  gpu:
    labels: ["linux", "gpu", "docker"]
    executor: docker
    max_parallel_jobs_per_runner: 1
    allowed_images:
      - pytorch/pytorch:2.9.0-cuda13.0-cudnn9-runtime

cache:
  enabled: true
  retention_days: 14
  paths:
    - "~/.cache/go-build"
    - "~/.npm"

artifacts:
  retention_days: 14

secrets:
  allow:
    - NPM_TOKEN
    - CARGO_REGISTRY_TOKEN

merge_policy:
  require_success: true
  missing_manifest: allow
  stale_ci: block
  allow_force_merge: true
```

### Platform Config Fields

- `version`: Required schema version.
- `triggers`: Which lifecycle events may enqueue runs.
- `defaults`: Job defaults inherited by manifests.
- `runner_pools`: Named execution pools. Runners register into one pool.
- `cache`: Default cache behavior. Jobs can add narrower cache keys and paths.
- `artifacts`: Upload retention defaults.
- `secrets`: Which named secrets jobs may request.
- `merge_policy`: Required CI behavior in the merge path.

### Trust Boundary

The platform config controls policy, so merge gating should evaluate it from the
current trusted home head, not from an unmerged changeset overlay.

A changeset that edits `/{home}/.gitslice/ci.yaml` must satisfy the currently
merged policy. Once that changeset is merged, the new policy applies to future
changesets.

Folder manifests can be read from the candidate tree so CI definition changes
can be tested together with code changes. If the platform config needs stricter
control, it can later add a policy such as `manifest_changes: require_owner`.

---

## Folder Manifest Schema

Example:

```yaml
version: 1
name: api

watch:
  - "**/*.go"
  - "go.mod"
  - "go.sum"

ignore:
  - "tmp/**"
  - "vendor/**"

applies_to:
  - "."
  - "/shared/proto"

jobs:
  unit:
    required: true
    image: golang:1.24
    commands:
      - go test ./...

  lint:
    required: false
    commands:
      - golangci-lint run ./...

  integration:
    required: true
    needs: ["unit"]
    working_directory: "/"
    timeout_seconds: 1200
    commands:
      - make integration-api
```

### Manifest Fields

- `version`: Required schema version.
- `name`: Optional display name. Defaults to the folder path.
- `watch`: Glob patterns that trigger this manifest. Relative to the manifest
  directory unless the pattern starts with `/`.
- `ignore`: Glob patterns that suppress matches. Same path rules as `watch`.
- `applies_to`: Additional paths outside the manifest folder that should trigger
  this manifest, such as shared libraries or generated proto directories.
- `jobs`: Map of job keys to job definitions.

### Job Fields

- `required`: Whether the job gates merge.
- `needs`: Job keys that must pass before this job runs.
- `runner_pool`: Overrides platform default.
- `image`: Overrides platform default, subject to pool `allowed_images`.
- `shell`: Defaults to platform shell.
- `working_directory`: Defaults to `"."`.
- `timeout_seconds`: Defaults to platform timeout.
- `commands`: Ordered shell commands.
- `env`: Plain env vars or secret references.
- `cache`: Job-specific cache settings.
- `artifacts`: Paths to upload on completion.

---

## Path Semantics

All manifest paths are logical paths inside the materialized home tree.

- `.` means the manifest directory.
- `./foo` means `foo` under the manifest directory.
- `foo` means `foo` under the manifest directory.
- `/foo` means `/{home}/foo`, not the host filesystem root.
- `..` segments are normalized and rejected if they escape the home root.
- Host absolute paths are rejected.

### Default Working Directory

The default working directory for each job is the folder containing the matched
manifest.

Example materialization:

```text
/workspace
  .gitslice/ci.yaml
  api/.gs-ci.yaml
  api/go.mod
  api/server/server.go
  shared/proto/types.proto
```

For `api/.gs-ci.yaml`:

- default `working_directory` is `/workspace/api`
- `watch: ["**/*.go"]` is evaluated relative to `/workspace/api`
- `working_directory: "/"` means `/workspace`
- `working_directory: "/shared/proto"` means `/workspace/shared/proto`

The runner should expose:

- `GS_HOME_ROOT=/workspace`
- `GS_MANIFEST_PATH=/api/.gs-ci.yaml`
- `GS_MANIFEST_DIR=/workspace/api`
- `GS_CHANGED_FILES` as newline-separated logical home paths
- `GS_CHANGESET_ID`
- `GS_CHANGESET_VERSION`
- `GS_RUN_ID`
- `GS_JOB_ID`

---

## Manifest Resolution

Input:

- `home_id`
- `slice_id`
- `changeset_id`
- latest `changeset_version`
- `base_commit_hash`
- changed canonical home paths

Output:

- a deterministic plan containing manifests, jobs, dependencies, check names,
  required flags, runner pool selection, and materialization requirements.

### Candidate Path Mapping

Every changed file from a custom slice must map to a canonical path under
`/{home}` before CI planning. If the same file is included in two custom slices,
CI still sees the same canonical home path and resolves the same manifests.

Path-head authority remains the source of truth for whether a changeset is stale
or conflicts with newer home changes. CI can pass and merge can still block if
path-head checks require sync or conflict resolution.

### Candidate Manifest Set

For each changed path, the planner finds candidate manifests in two ways:

1. Ancestor lookup: walk upward from the changed file's parent directory to the
   home root and include any `.gs-ci.yaml` found.
2. Dependency lookup: include indexed manifests whose `applies_to` patterns
   match the changed path.

The ancestor lookup allows layered checks. A root project manifest can define
integration checks, and a nested folder manifest can define narrower unit tests.

Example:

```text
changed path:
  /api/server/routes.go

candidate manifests:
  /api/.gs-ci.yaml
  /.gs-ci.yaml
```

If both manifests match their `watch` rules, both contribute jobs.

### Watch Matching

For each candidate manifest:

1. Convert changed home paths into paths relative to the manifest directory.
2. Match `watch`; if omitted, default to all files under the manifest directory.
3. Apply `ignore`.
4. Match `applies_to` against logical home paths.
5. If no changed path matches, skip the manifest.

### Manifest Index

For small homes, the planner can scan ancestor paths directly and skip the
dependency lookup unless `applies_to` is used.

For production, maintain a `ci_manifest_index` per home head:

- manifest path
- manifest directory
- manifest content hash
- watch globs
- ignore globs
- applies_to globs
- parsed schema version
- parse error, if any

The index lets planning answer "which manifests care about `/shared/lib/x.go`?"
without scanning the whole home tree.

### Plan Hash

The planner computes a stable `plan_hash` from:

- changeset version id
- base commit hash
- platform config hash
- matched manifest paths and hashes
- changed path list
- job definitions after defaults are applied

Merge gating requires successful checks for the current `plan_hash`, not just
the current changeset id. If the platform config or any matched manifest changes,
previous CI is stale.

---

## Triggers

Supported trigger events:

- `changeset_export`: enqueue CI after `gs slice export` appends a new version.
- `manual`: user runs `gs ci run`.
- `merge_requested`: merge path can enqueue or require a run if no valid run
  exists.

Recommended v1 behavior:

1. `gs slice export` enqueues a run when platform config enables
   `changeset_export`.
2. New changeset versions cancel queued jobs for older versions of the same
   changeset.
3. Running jobs for older versions are marked `superseded` when they finish, or
   cancelled immediately if the runner supports cancellation.
4. `gs changeset merge` checks for a successful run for the latest version and
   current `plan_hash`.

---

## Runner Model

The first implementation should use user-hosted runners.

Registration:

```bash
gs runner token create --name vm-1 --pool default
gs runner register --token <token>
gs runner start
```

The runner uses outbound polling only:

```text
runner start
  -> authenticate with runner token
  -> heartbeat capabilities and pool labels
  -> poll for queued jobs
  -> claim job lease
  -> materialize candidate tree
  -> execute commands
  -> stream logs and status
  -> upload artifacts
  -> complete job
```

Gitslice does not SSH into user machines. Runners do not connect directly to
Postgres.

### Runner Executors

V1 should support two executor modes:

- `docker`: preferred default; commands run inside the configured image.
- `shell`: local shell on the runner VM; useful for demos and trusted internal
  use, but not recommended for shared runners.

The platform config chooses allowed executors per runner pool.

### Job Leasing

Each job claim should have:

- lease id
- lease expiration
- runner id
- heartbeat interval
- cancellation token

If a runner misses heartbeats past the lease timeout, the scheduler requeues the
job unless the job exceeded retry policy.

---

## Materialization

For v1, materialize the full candidate home tree into an ephemeral workspace.
This is simplest and avoids surprising commands that expect repository-level
files.

Environment files needed by a custom slice are materialized from the home-slice
sidecar requirements file described in
[`ongoing_SLICE_ENV_MATERIALIZATION_DESIGN.md`](ongoing_SLICE_ENV_MATERIALIZATION_DESIGN.md).
CI must read those requirements from trusted home head, include the requirements
hash in the plan hash, and exclude sensitive materialized paths from caches,
artifacts, logs, and changeset export surfaces.

Later optimizations can materialize only:

- matched manifest directories
- declared `applies_to` paths
- declared job input paths
- home-level config files needed by the job

All file contents should be fetched from content-addressed blobs. The runner can
reuse local blob cache across jobs.

The workspace must be fresh per job or per run:

- default: fresh per job for isolation
- optional future optimization: shared read-only run workspace plus writable job
  overlay

---

## Data Model

### `ci_runners`

- `id`
- `home_id`
- `name`
- `pool`
- `labels`
- `status` (`offline|idle|busy|disabled`)
- `token_hash`
- `version`
- `last_seen_at`
- `created_at`
- `disabled_at`

### `ci_runs`

- `id`
- `home_id`
- `slice_id`
- `changeset_id`
- `changeset_version_id`
- `base_commit_hash`
- `candidate_tree_hash`
- `platform_config_hash`
- `plan_hash`
- `trigger_event` (`changeset_export|manual|merge_requested`)
- `triggered_by_user_id`
- `status` (`queued|planning|running|passed|failed|cancelled|superseded|skipped`)
- `created_at`
- `started_at`
- `finished_at`

Unique constraint:

```text
(changeset_id, changeset_version_id, plan_hash, trigger_event)
```

Manual reruns should either create a new `attempt` value or bypass this
automatic-trigger dedupe constraint. Automatic `changeset_export` and
`merge_requested` triggers should dedupe by the same key.

### `ci_run_manifests`

- `id`
- `run_id`
- `manifest_path`
- `manifest_dir`
- `manifest_hash`
- `matched_paths`
- `parse_status`
- `parse_error`

### `ci_jobs`

- `id`
- `run_id`
- `manifest_run_id`
- `manifest_path`
- `job_key`
- `check_name`
- `required`
- `runner_pool`
- `image`
- `working_directory`
- `status` (`queued|leased|running|passed|failed|cancelled|skipped`)
- `runner_id`
- `lease_id`
- `lease_expires_at`
- `exit_code`
- `infra_failure`
- `started_at`
- `finished_at`

Recommended unique constraint:

```text
(run_id, manifest_path, job_key)
```

### `ci_job_dependencies`

- `run_id`
- `job_id`
- `depends_on_job_id`

### `ci_steps`

- `id`
- `job_id`
- `step_index`
- `command`
- `status`
- `exit_code`
- `started_at`
- `finished_at`

### `ci_log_chunks`

- `id`
- `job_id`
- `chunk_index`
- `stream` (`stdout|stderr|system`)
- `object_key` or inline payload for small chunks
- `byte_count`
- `created_at`

Logs should be chunked rather than one row per line to avoid database write
amplification.

### `ci_checks`

- `changeset_id`
- `changeset_version_id`
- `plan_hash`
- `manifest_path`
- `job_key`
- `check_name`
- `required`
- `status`
- `run_id`
- `updated_at`

Merge gating reads `ci_checks` for the latest changeset version and current
`plan_hash`.

### `ci_manifest_index`

- `home_id`
- `home_commit_hash`
- `manifest_path`
- `manifest_dir`
- `manifest_hash`
- `watch_globs`
- `ignore_globs`
- `applies_to_globs`
- `parse_status`
- `parse_error`
- `updated_at`

---

## API Design

All APIs should be gRPC-first with grpc-gateway bindings.

### User CI API

Service: `ci.v1.CIService`

- `StartRun(StartRunRequest) returns (StartRunResponse)`
- `GetRun(GetRunRequest) returns (Run)`
- `ListRuns(ListRunsRequest) returns (ListRunsResponse)`
- `CancelRun(CancelRunRequest) returns (CancelRunResponse)`
- `Rerun(RerunRequest) returns (StartRunResponse)`
- `StreamLogs(StreamLogsRequest) returns (stream LogEvent)`
- `ListChecks(ListChecksRequest) returns (ListChecksResponse)`

Gateway examples:

- `POST /v1/ci/runs`
- `GET /v1/ci/runs/{run_id}`
- `GET /v1/changesets/{changeset_id}/ci`
- `POST /v1/ci/runs/{run_id}:cancel`
- `POST /v1/ci/runs/{run_id}:rerun`

### Runner Management API

Service: `ci.v1.RunnerAdminService`

These methods use normal user or org admin authentication:

- `ListRunnerPools(ListRunnerPoolsRequest) returns (ListRunnerPoolsResponse)`
- `ListRunners(ListRunnersRequest) returns (ListRunnersResponse)`
- `GetRunner(GetRunnerRequest) returns (Runner)`
- `CreateRunnerToken(CreateRunnerTokenRequest) returns (CreateRunnerTokenResponse)`
- `DisableRunner(DisableRunnerRequest) returns (DisableRunnerResponse)`
- `EnableRunner(EnableRunnerRequest) returns (EnableRunnerResponse)`
- `RevokeRunner(RevokeRunnerRequest) returns (RevokeRunnerResponse)`
- `ListRunnerJobs(ListRunnerJobsRequest) returns (ListRunnerJobsResponse)`
- `ListQueuedJobs(ListQueuedJobsRequest) returns (ListQueuedJobsResponse)`

Gateway examples:

- `GET /v1/ci/runner-pools`
- `GET /v1/ci/runners`
- `GET /v1/ci/runners/{runner_id}`
- `POST /v1/ci/runner-tokens`
- `POST /v1/ci/runners/{runner_id}:disable`
- `POST /v1/ci/runners/{runner_id}:enable`
- `POST /v1/ci/runners/{runner_id}:revoke`
- `GET /v1/ci/runners/{runner_id}/jobs`
- `GET /v1/ci/queued-jobs`

### Runner API

Service: `ci.v1.RunnerService`

- `RegisterRunner(RegisterRunnerRequest) returns (RegisterRunnerResponse)`
- `Heartbeat(HeartbeatRequest) returns (HeartbeatResponse)`
- `PollJobs(PollJobsRequest) returns (PollJobsResponse)`
- `ClaimJob(ClaimJobRequest) returns (ClaimJobResponse)`
- `GetJobPayload(GetJobPayloadRequest) returns (JobPayload)`
- `AppendLog(AppendLogRequest) returns (AppendLogResponse)`
- `CompleteStep(CompleteStepRequest) returns (CompleteStepResponse)`
- `CompleteJob(CompleteJobRequest) returns (CompleteJobResponse)`
- `UploadArtifact(UploadArtifactRequest) returns (UploadArtifactResponse)`

Runner requests authenticate with scoped runner credentials, not user session
tokens.

---

## Web App Management

The web app should expose CI executor management under settings, separate from
changeset CI run details.

Recommended routes:

- `/settings/ci`
- `/settings/ci/runners`
- `/settings/ci/runners/{runner_id}`
- `/settings/ci/runs`

### Runner Pools View

Show runner pools resolved from `/{home}/.gitslice/ci.yaml`:

- pool name
- executor type (`docker` or `shell`)
- required labels
- allowed images
- online runner count
- busy runner count
- queued job count
- max parallel jobs per runner

The web app should not mutate runner pool policy directly in the database. Pool
policy is versioned in `/{home}/.gitslice/ci.yaml`; the UI can link to that file
or provide an "edit config" workflow that creates a normal changeset.

### Runners View

Show registered runners:

- runner name
- runner id
- pool
- labels and capabilities
- executor mode
- runner version
- status (`offline|idle|busy|disabled`)
- last heartbeat
- current job, if any
- recent jobs and outcomes

Allowed actions:

- create a short-lived registration token
- copy the `gs runner register --token ...` command
- disable runner
- enable runner
- revoke runner credential
- view recent jobs and logs

Revoking a runner should invalidate its long-lived runner credential and cancel
or requeue leased jobs depending on job state. Disabling a runner should prevent
new leases while allowing an already running job to finish unless the user also
cancels the job.

### Registration Token UX

Creating a registration token should require:

- runner name
- runner pool
- optional labels
- expiration, default short-lived

The token is shown once. After the dialog closes, the UI should only show token
metadata, never the token value.

Example command shown in the dialog:

```bash
gs runner register --token <runner-registration-token>
gs runner start
```

### Queue and Executor Health

The CI settings page should include a queue and health summary:

- queued jobs by pool
- oldest queued job age
- online runners by pool
- stuck leases
- runners on old versions
- recent infrastructure failures

This page should make it obvious when CI is blocked because no compatible runner
is online for the selected pool or image.

### Changeset CI UI

Changeset pages should show:

- latest run for the exact changeset version and `plan_hash`
- required versus optional checks
- manifest path for each check
- runner and pool selected for each job
- live status and logs
- rerun, rerun failed, cancel
- stale result warning when a newer version or plan exists

The UI should clearly distinguish a failed user command from an infrastructure
failure such as no runner, runner crash, expired lease, image pull failure, or
workspace materialization failure.

### Security Rules

- Web users never see a registered runner's long-lived credential.
- Registration tokens are short-lived, one-time-use, and visible once.
- Runner management actions are audited.
- Runner revocation is scoped to one home/org.
- Pool policy edits go through normal versioned file changes.

---

## CLI Design

### User Commands

```bash
gs ci run
gs ci run --changeset cs_...
gs ci run --manifest /api/.gs-ci.yaml
gs ci run --job unit
gs ci status
gs ci status --run ci_run_...
gs ci logs --run ci_run_... --job unit --follow
gs ci cancel --run ci_run_...
gs ci rerun --run ci_run_... --failed-only
```

Inside a checkout, `gs ci run` should default to the tracked changeset for that
checkout. If no tracked changeset exists, it should explain that the user needs
to run `gs slice export` or pass `--changeset`.

### Runner Management Commands

These commands use normal user or org admin authentication. They are the CLI
equivalent of the web runner management UI.

```bash
gs runner pool list
gs runner pool show default

gs runner list
gs runner list --pool default
gs runner list --status idle
gs runner show runner_...

gs runner token create --name vm-1 --pool default --label linux --label docker --ttl 30m

gs runner disable runner_...
gs runner enable runner_...
gs runner revoke runner_...
gs runner revoke runner_... --requeue-leased
gs runner revoke runner_... --cancel-leased

gs runner jobs runner_... --limit 20
gs runner queue list
gs runner queue list --pool default
gs runner queue explain --pool default --image golang:1.24
```

`gs runner queue explain` should diagnose why a queued job cannot run, for
example no online runner in the pool, missing labels, disallowed image, stale
runner version, or exhausted concurrency.

Equivalent web and CLI actions:

| Web action | CLI command |
| --- | --- |
| View pools | `gs runner pool list` |
| View pool details | `gs runner pool show <pool>` |
| View runners | `gs runner list` |
| View runner details | `gs runner show <runner-id>` |
| Create registration token | `gs runner token create --name <name> --pool <pool>` |
| Disable runner | `gs runner disable <runner-id>` |
| Enable runner | `gs runner enable <runner-id>` |
| Revoke runner | `gs runner revoke <runner-id>` |
| View runner jobs | `gs runner jobs <runner-id>` |
| View queued jobs | `gs runner queue list` |
| Diagnose blocked queue | `gs runner queue explain` |

### Runner Host Commands

These commands run on the VM that will execute jobs. They use runner
credentials, not user session credentials.

```bash
gs runner enroll --token <runner-registration-token>
gs runner register --token <runner-registration-token> # alias for enroll
gs runner start
gs runner start --executor docker
gs runner start --executor shell
gs runner status
gs runner doctor
gs runner unenroll
```

`gs runner enroll` should store the runner credential locally. `gs runner start`
should read that credential and poll Gitslice for jobs.

Runner local state should live under `~/.gitslice/runner/`, separate from slice
checkout `.gs/` metadata.

---

## Merge Gating

`gs changeset merge` should enforce CI after conflict/staleness checks and
before committing the merge.

Required state:

1. Latest changeset version is known.
2. Current path-head checks report no unresolved conflicts or stale paths.
3. Planner can compute the current `plan_hash`.
4. Required checks for `(changeset_id, latest_version, plan_hash)` are `passed`.

If checks are missing:

- If `merge_policy.require_success` is true, return `FailedPrecondition`.
- If `trigger_event: merge_requested` is enabled, enqueue a run and return a
  message telling the user which run was started.

If no manifest matches:

- `missing_manifest: allow` means merge is allowed with no CI checks.
- `missing_manifest: block` means merge fails until a matching manifest exists
  or the user force-merges.

Force merge:

```bash
gs changeset merge --force --reason "emergency fix"
```

Force merge should be audited and may be disabled by platform policy.

---

## Interaction with Conflict Resolution

CI is not the source of path authority.

Path-head conflict logic still decides whether a changeset is based on the right
file heads and whether it can merge without clobbering another user's changes.

CI planning uses the same canonical home paths as conflict detection. This keeps
the behavior correct when:

- one file appears in two custom slices
- two changesets touch overlapping files through different slices
- async home promotion is enabled
- a changeset has green CI but becomes stale before merge

If a changeset becomes stale, previous CI does not need to be deleted, but it no
longer makes the changeset mergeable. The user must sync/export a new version,
which creates a new CI target.

---

## Security

V1 security posture:

- User-hosted runners are trusted by the user who registered them.
- Runners authenticate with scoped runner tokens.
- Jobs do not receive user session tokens.
- Runner tokens are scoped to one home and one runner pool.
- Docker executor should run non-privileged containers by default.
- Host absolute paths are rejected in manifests.
- Workspace paths are normalized under the home root.
- Secrets are injected only if allowed by platform config.
- Logs are redacted using configured secret values and patterns.

Production hardening before managed runners:

- per-job microVM or hardened container isolation
- network egress policy
- CPU, memory, disk, and runtime quotas
- artifact and cache size quotas
- signed runner binaries or version policy
- audit log for runner registration, force merge, and secret access

---

## Failure Handling

- Parse error in platform config: CI planning fails closed when
  `merge_policy.require_success` was enabled by last valid policy.
- Parse error in a matched folder manifest: create a required failed check for
  that manifest.
- Runner crash: job lease expires and scheduler requeues within retry limit.
- Runner reports infrastructure failure: mark job failed with `infra_failure`.
- New changeset version: older queued jobs are cancelled; older running jobs are
  cancelled if possible or marked superseded at completion.
- Duplicate trigger: dedupe by `(changeset_id, version, plan_hash)`.
- Log upload failure: runner retries chunk append; job completion includes last
  acknowledged log offset.

---

## Performance Plan

V1:

- full candidate tree materialization
- content-addressed blob cache on runner
- log chunks instead of per-line database writes
- job leases and polling with exponential backoff

V2:

- `ci_manifest_index` for fast reverse dependency matching
- partial workspace materialization using declared inputs
- cache keys based on input file content hashes
- reusable read-only run workspace with per-job writable overlays
- warm runner pools by image
- queue fairness by home and runner pool

---

## Observability

Metrics:

- `ci_runs_total{status,trigger}`
- `ci_jobs_total{status,pool}`
- `ci_job_duration_seconds{pool,job_key}`
- `ci_queue_latency_seconds{pool}`
- `ci_runner_heartbeats_total{pool}`
- `ci_runner_online{pool}`
- `ci_log_bytes_total`
- `ci_superseded_runs_total`

Structured logs should include:

- `run_id`
- `job_id`
- `runner_id`
- `home_id`
- `changeset_id`
- `changeset_version_id`
- `plan_hash`

Health endpoints:

- core `/health` includes scheduler state
- CI service health includes queue depth and online runners by pool

---

## Implementation Plan

### PR 1: Proto and Schema

- Add `proto/ci/ci_service.proto` and gateway bindings.
- Add migrations for runner, run, job, check, log chunk, and manifest index
  tables.
- Add storage interfaces and no-op server implementation.
- Add CLI help stubs for `gs ci` and `gs runner`.

### PR 2: Config Parsers and Planner

- Parse `/{home}/.gitslice/ci.yaml`.
- Parse `.gs-ci.yaml` folder manifests.
- Implement path normalization and glob matching.
- Implement ancestor manifest discovery.
- Compute `plan_hash`.
- Unit test path semantics, default cwd, applies_to, and stale plan behavior.

### PR 3: Manual CI Runs

- Implement `gs ci run/status/logs`.
- Plan and persist a run for a selected changeset version.
- Create check rows from required jobs.
- No automatic runner execution yet; jobs can remain queued.

### PR 4: User-Hosted Runner MVP

- Implement runner token creation, registration, polling, claims, heartbeats,
  log append, and job completion.
- Implement `gs runner start --executor shell` for trusted demo use.
- Materialize full candidate tree.
- Execute commands sequentially per job.

### PR 5: Docker Executor

- Add Docker executor.
- Enforce allowed images, timeout, cwd, env, and normalized paths.
- Add artifact upload and basic cache paths.

### PR 6: Changeset Triggers

- Enqueue CI on `gs slice export` when enabled.
- Cancel/supersede older version runs.
- Show CI status in changeset detail APIs and web UI.

### PR 7: Merge Gating

- Recompute current plan during merge.
- Block merge when required checks are missing, failed, stale, or running.
- Add `--force --reason` with audit trail, controlled by platform policy.

### PR 8: Manifest Index and Dependency Triggers

- Build/update `ci_manifest_index` after home commits.
- Use the index for `applies_to` reverse matching.
- Add tests for shared library changes triggering project manifests.

### PR 9: Hardening and Docs

- Add quotas, retry policy, runner disable/revoke, and redaction tests.
- Document runner setup and recommended Docker mode.
- Add integration test: checkout slice, export changeset, runner executes CI,
  merge gate blocks/fails/passes correctly.

### PR 10: Web Runner Management

- Add CI settings routes for runner pools, registered runners, and queue health.
- Add registration-token creation flow with copyable CLI commands.
- Add disable, enable, revoke, and runner job history actions.
- Add changeset CI panels with exact version and `plan_hash` status.
- Add stale CI and missing compatible runner warnings.
- Add equivalent CLI commands for runner pool, runner, token, job history, and
  queue management.

---

## Acceptance Criteria

- Users can define platform config at `/{home}/.gitslice/ci.yaml`.
- Users can define folder manifests at `.gs-ci.yaml` under project folders.
- Default job working directory is the manifest directory.
- `/` in manifest paths refers to the home root, not host root.
- CI runs are attached to exact changeset versions and plan hashes.
- `gs runner start` on a user VM can execute queued jobs.
- Web users can view runner pools, register runners, disable/revoke runners, and
  diagnose queued jobs with no compatible executor.
- CLI users can perform the same runner management actions as the web UI.
- Required checks gate `gs changeset merge`.
- A file included in two custom slices resolves the same canonical home-path CI
  plan.
- Existing path-head conflict behavior remains authoritative and unchanged.
