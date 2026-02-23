# Slice CI Design

## Implementation Status

- Current status: `ongoing`
- Last updated: `2026-02-23`

---

## Executive Summary

Design a CI framework with GitHub Actions-like ergonomics, but optimized for gitslice:

- Pipelines are defined in-repo and resolved per slice snapshot.
- Runs are triggered by changeset lifecycle events.
- Execution is content-addressed and path-aware for speed.
- Merge gating is bound to `changeset_id + snapshot_version`.

This keeps existing conflict resolution semantics unchanged and adds a fast, deterministic quality gate.

---

## Goals

1. **Slice-native pipelines**: workflow can vary by slice.
2. **Fast feedback**: skip unaffected jobs; reuse cache aggressively.
3. **Deterministic gating**: merge allowed only when required checks pass for the latest snapshot.
4. **Action-like UX**: jobs/steps/dependencies/log streaming/status checks.
5. **Operational simplicity**: fits current core server + storage model.

## Non-Goals

- Replacing merge conflict detection/resolution model.
- Replacing external CI for non-gitslice repos.
- Supporting arbitrary untrusted privileged runners in v1.

---

## Current System Constraints

- Changesets can have multiple snapshots; latest snapshot is merge-relevant.
- Merge conflicts are file-ownership based and resolved via existing admin APIs.
- Core server already tracks changesets, commits, snapshots, and file history.
- Object content is content-addressed (enables deterministic cache keys).

---

## UX Model (GitHub Actions-like)

### Workflow File

Path in slice tree:

- `.gitslice/ci/pipeline.yaml`

Top-level model:

- `version`
- `defaults`
- `slices` (selector-specific job configs)
- `on` triggers
- `jobs` with `id`, `needs`, `paths`, `run`, `cache`, `required`, `timeout`

Example:

```yaml
version: 1
on:
  - changeset.created
  - changeset.snapshot.updated
  - changeset.merge.requested

defaults:
  image: gitslice/go-node:2026-02
  timeout_seconds: 900

slices:
  "payments_*":
    jobs:
      - id: unit
        required: true
        paths: ["services/payments/**", "internal/common/**"]
        run:
          - "go test ./services/payments/..."
      - id: lint
        run:
          - "golangci-lint run ./services/payments/..."

  "*":
    jobs:
      - id: smoke
        required: true
        run:
          - "make test"
```

### User Operations

- `gs ci run <changeset-id> [--snapshot <n>]`
- `gs ci status <run-id>`
- `gs ci logs <run-id> [--job <id>] --follow`
- `gs ci rerun <run-id> [--failed-only]`

Web:

- CI panel on changeset page with required/optional checks.
- live logs per job step.

---

## Architecture

### Components

1. **CI Service (new gRPC service in core process initially)**
- API for run creation, status query, log streaming, cancellation.

2. **Planner**
- Loads workflow from the exact slice snapshot.
- Resolves applicable jobs for the slice.
- Applies path filters based on changeset modified files.
- Produces executable DAG.

3. **Scheduler**
- Queue runs and jobs.
- Prioritize latest snapshot runs.
- Cancel superseded runs for same `changeset_id` when newer snapshot appears.

4. **Runner Manager**
- Launches sandbox/container jobs.
- Injects immutable input manifest and env.
- Captures stdout/stderr incrementally.

5. **Cache + Artifact Layer**
- Content-addressed cache keys.
- Optional artifact upload/download.

6. **Run Store (Postgres)**
- Durable state for runs/jobs/steps/log index/check results.

### Deployment Phases

- **Phase 1**: run CI service in core server process.
- **Phase 2**: split into dedicated `ci_worker` processes behind queue.

---

## Execution Flow

### Trigger Path

1. Changeset created or snapshot appended.
2. CI trigger hook enqueues run request with:
- `changeset_id`
- `snapshot_version`
- `slice_id`
- `base_commit_hash`
- `modified_files`

3. Planner resolves workflow from that snapshot.
4. Planner computes DAG + required checks.
5. Scheduler dispatches jobs respecting `needs`.
6. Runner executes each step, streams logs, updates state.
7. Final run status persisted and attached to changeset snapshot.

### Merge Gating Path

`MergeChangeset` must enforce:

- Latest snapshot for the changeset has a completed CI run.
- All **required** jobs for that run are `PASS`.

If missing/failing, return `FailedPrecondition` with failing checks.

---

## Data Model (new)

### Tables

- `ci_runs`
  - `id` (pk)
  - `changeset_id`
  - `snapshot_version`
  - `slice_id`
  - `trigger_event` (`created|snapshot_updated|merge_requested|manual`)
  - `workflow_hash`
  - `status` (`queued|running|passed|failed|cancelled|skipped`)
  - `started_at`, `finished_at`

- `ci_jobs`
  - `id` (pk)
  - `run_id` (fk)
  - `job_key`
  - `required` (bool)
  - `status`
  - `started_at`, `finished_at`
  - `runner_id`

- `ci_steps`
  - `id` (pk)
  - `job_id` (fk)
  - `step_index`
  - `command`
  - `status`
  - `exit_code`
  - `started_at`, `finished_at`

- `ci_job_dependencies`
  - `run_id`, `job_key`, `depends_on_job_key`

- `ci_logs`
  - `id` (pk)
  - `job_id` (fk)
  - `seq`
  - `stream` (`stdout|stderr|system`)
  - `line`
  - `created_at`

- `ci_check_results`
  - `changeset_id`, `snapshot_version`, `job_key`
  - `required`
  - `status`
  - `run_id`

Indexes:

- `ci_runs(changeset_id, snapshot_version desc)`
- `ci_jobs(run_id, status)`
- `ci_logs(job_id, seq)`
- `ci_check_results(changeset_id, snapshot_version)`

---

## API Sketch (gRPC-first)

`proto/ci/ci_service.proto`:

- `StartPipeline(StartPipelineRequest) returns (StartPipelineResponse)`
- `GetPipeline(GetPipelineRequest) returns (Pipeline)`
- `ListPipelines(ListPipelinesRequest) returns (ListPipelinesResponse)`
- `CancelPipeline(CancelPipelineRequest) returns (CancelPipelineResponse)`
- `StreamPipelineLogs(StreamPipelineLogsRequest) returns (stream PipelineLogEvent)`
- `RerunPipeline(RerunPipelineRequest) returns (StartPipelineResponse)`

Gateway bindings for:

- `POST /v1/ci/runs`
- `GET /v1/ci/runs/{run_id}`
- `GET /v1/changesets/{changeset_id}/ci`
- `POST /v1/ci/runs/{run_id}:cancel`
- `POST /v1/ci/runs/{run_id}:rerun`

---

## Fast-Path Optimizations

1. **Path-based job pruning**
- If job `paths` do not intersect `modified_files`, mark `skipped`.

2. **Content-addressed cache**
- Cache key includes:
  - command
  - toolchain/image digest
  - env hash
  - input file content hashes

3. **Snapshot-scoped checkout**
- Materialize only files needed by relevant jobs.

4. **Warm runners**
- Keep hot pool per common image.

5. **Superseded run cancellation**
- New snapshot cancels queued/running older snapshot runs for same changeset.

6. **Fail-fast required checks**
- If required job fails, optional jobs may continue but merge eligibility is decided immediately.

7. **Bounded concurrency controls**
- Global worker cap + per-slice cap to avoid noisy-neighbor starvation.

---

## Security Model

- Jobs run in sandboxed, non-privileged containers.
- Read-only checkout by default; writable temp workspace only.
- Secret injection allowlist per slice/org (phase 2).
- Egress policy support (deny-by-default optional).
- Logs are redacted via secret-pattern filtering.

---

## Interaction with Conflict Resolution

No semantic changes to existing conflict model:

- Conflict detection remains in `MergeChangeset` ownership checks.
- Conflict resolution remains explicit `ResolveConflict` owner selection.
- CI does not mutate file ownership index.

Effective merge gate becomes:

1. No unresolved ownership conflicts.
2. Required CI checks pass for latest snapshot.

---

## Failure Handling

- Runner crash: mark job `failed` with infra reason.
- Service restart: recover queued/running jobs from DB; requeue idempotently.
- Duplicate trigger: dedupe by `(changeset_id, snapshot_version, workflow_hash)`.
- Stale run: if newer snapshot exists, stale run marked `cancelled/superseded`.

---

## Observability

Metrics:

- `ci_runs_total{status,trigger}`
- `ci_jobs_total{status,job_key}`
- `ci_job_duration_seconds{job_key}`
- `ci_queue_latency_seconds`
- `ci_cache_hit_ratio`
- `ci_superseded_runs_total`

Health:

- `/health/ci` includes scheduler lag and runner availability.

Logs:

- structured logs with `run_id`, `job_id`, `changeset_id`, `snapshot_version`.

---

## Rollout Plan (PR-by-PR)

1. **PR1: Schema + model primitives**
- Add CI tables and storage interfaces.
- Add proto definitions and server stubs.

2. **PR2: Planner + static DAG execution**
- Parse pipeline yaml from slice snapshot.
- Execute local shell jobs sequentially per job; DAG across jobs.

3. **PR3: Changeset trigger integration**
- Trigger run on create/snapshot update.
- Store check results keyed by snapshot version.

4. **PR4: Merge gating**
- Block merge when required checks missing/failing.
- Return clear precondition error messages.

5. **PR5: Log streaming + CLI commands**
- Add `gs ci status/logs/run/rerun`.
- Add web endpoint integration.

6. **PR6: Performance pass**
- Path pruning, cache keys, warm runner pool, supersede cancellation.

7. **PR7: Hardening**
- Retry semantics, quota limits, observability dashboards, docs finalize.

---

## Acceptance Criteria

- Pipeline can be defined in `.gitslice/ci/pipeline.yaml` and resolved per slice snapshot.
- CI runs are attached to `changeset_id + snapshot_version`.
- Required checks are enforced in merge path.
- End-to-end run latency for small slice changes is significantly lower than full-repo CI due to path pruning and cache reuse.
- Existing conflict resolution behavior remains unchanged.
