# Design: Async Promotion, Throughput, and Conflict Integrity

## Problem Statement

The historical merge workflow treated home/root promotion as part of the
user-visible merge operation. That gave strong read-after-merge behavior for
materialized home/root views, but it also made every merge pay for work that was
not needed to decide whether the merge is valid.

In the recent Postgres benchmark, the average merge profile was roughly:

| Step | Average latency |
| --- | ---: |
| Conflict check | 27 ms |
| Finalize source slice merge | 226 ms |
| Home/root promotion | 1319 ms |
| Total merge | 1760 ms |

The database can handle far more than 43 simple writes per second. The issue is
that a logical merge expands into multiple indexed reads, lock table writes,
metadata updates, commit-history inserts, directory materialization, and
home/root view updates. The slowest piece is synchronous promotion.

The goal is to increase merge throughput without weakening conflict resolution
or allowing derived views to overwrite newer accepted content.

---

## Core Principle

Merge correctness must be decided by the source slices and accepted merge log,
not by whether home/root materialized views have caught up.

Put differently:

```
Merge transaction = source of truth
Home/root promotion = derived materialization
```

The merge path should synchronously decide:

1. Does the changeset carry complete base path versions for every touched path?
2. Do those versions still match the current `home_path_heads` rows?
3. Can the changeset be marked merged?
4. What source slice commit/event was accepted?

Home/root promotion should asynchronously reflect accepted merge events into
convenient read views. It should not be the authority for whether a merge is
allowed.

---

## Current Conflict Semantics

Gitslice allows overlapping slice membership. The same file or folder can be
included in more than one slice, including multiple slices under the same home.

Example:

```
home: alice

slice A includes alice/app/foo.go
slice B includes alice/app/foo.go
```

If slice A and slice B both edit `alice/app/foo.go`, correctness depends on
checking the home path head for that path and comparing it with each
changeset's recorded base path version.

Expected workflow:

1. Slice A changes `alice/app/foo.go`.
2. Slice A merges successfully.
3. Slice B tries to merge a different version based on an older file state.
4. Merge checks `home_path_heads[alice/app/foo.go]` against slice B's snapshot
   base path version.
5. Slice B receives `NEEDS_SYNC` before it can append an accepted merge event.

This logic does not require the home slice to already reflect slice A. The home
slice is just a view. `home_path_heads` is the conflict authority.

---

## Throughput Versus Integrity Tradeoff

### Fully synchronous promotion

Legacy fully synchronous behavior:

```
MergeChangeset
  -> validate path-head versions
  -> mark source changeset merged
  -> append source slice commit
  -> copy refs into home/root views
  -> update home/root metadata
  -> return success
```

Advantages:

- Simple mental model for read-after-merge from home/root views.
- Fewer eventual-consistency states to expose.
- Tests can immediately assert home/root file tree state after merge.

Costs:

- Merge latency includes materialization latency.
- Throughput is bounded by home/root view update speed.
- Large batches and hot homes can slow unrelated source-slice merges.
- The merge path performs work that is not required for conflict correctness.

### Async promotion

Current request-time behavior:

```
MergeChangeset
  -> validate path-head versions
  -> mark source changeset merged
  -> append source slice commit
  -> enqueue promotion work
  -> return success

Promotion worker
  -> update home/root materialized views in batches
```

Config changes are the exception for now: changesets touching
`.gitslice/config.yaml` wait for promotion before applying config because config
sync reads the root file tree.

Promotion is intentionally backpressured relative to foreground merge traffic:
the in-process worker queue uses fewer concurrent promotion workers and larger
batches so derived-view writes do not saturate the same Postgres pool used by
`CreateSliceFromFolder`, `CreateChangeset`, and `MergeChangeset`.

Advantages:

- Merge latency tracks correctness work, not view materialization.
- Promotions can be batched by home and path.
- Workers can be tuned independently from request concurrency.
- Durable retry/idempotency can be added around a smaller worker boundary.

Costs:

- Home/root views become eventually consistent.
- The current in-process queue is not durable; a process crash after merge
  success but before queue drain can leave materialized views stale until a
  reconciliation worker exists.
- APIs that need a fully materialized home/root view need an explicit wait,
  freshness token, or promotion status check.
- Promotion must be order-independent and idempotent, or stale jobs can
  overwrite newer accepted content.

The integrity tradeoff is acceptable only if the merge transaction remains the
single authority and promotion uses monotonic guards.

---

## Required Safety Rule: Monotonic Per-Path Promotion

Durable async promotion must not be "last worker wins." Queue execution order is
not a valid correctness mechanism once promotion can be retried, distributed
across processes, or replayed after a crash. The current first phase keeps
home-scoped jobs on the same in-process FIFO shard, but that is a throughput
step, not the final integrity model.

Bad case without guards:

1. Slice B queues an older promotion for `alice/app/foo.go`.
2. Slice A queues a newer promotion for `alice/app/foo.go`.
3. Worker applies A first.
4. Worker later applies stale B.
5. Home view incorrectly points at B's older content.

The fix is to assign every accepted merge a monotonic sequence and apply
promotion per path only when the event is newer than the currently materialized
path state.

Conceptual table:

```sql
home_path_state (
  home_slice_id text not null,
  path text not null,
  source_slice_id text not null,
  content_hash text not null,
  merge_seq bigint not null,
  commit_hash text not null,
  updated_at timestamptz not null,
  primary key (home_slice_id, path)
)
```

Promotion upsert:

```sql
INSERT INTO home_path_state (
  home_slice_id,
  path,
  source_slice_id,
  content_hash,
  merge_seq,
  commit_hash,
  updated_at
)
VALUES (...)
ON CONFLICT (home_slice_id, path) DO UPDATE
SET source_slice_id = EXCLUDED.source_slice_id,
    content_hash = EXCLUDED.content_hash,
    merge_seq = EXCLUDED.merge_seq,
    commit_hash = EXCLUDED.commit_hash,
    updated_at = EXCLUDED.updated_at
WHERE home_path_state.merge_seq < EXCLUDED.merge_seq;
```

This makes promotion:

- **Idempotent**: replaying the same event does not change the result.
- **Order-independent**: stale events cannot overwrite newer materialization.
- **Batchable**: many paths can be promoted with one ordered upsert.
- **Retryable**: worker failure can resume from durable events.

---

## Durable Merge Event Model

The merge transaction should append an event after it has decided the merge is
valid and before it returns success.

Conceptual table:

```sql
merge_events (
  seq bigserial primary key,
  changeset_id text not null,
  source_slice_id text not null,
  home_slice_id text,
  commit_hash text not null,
  committed_at timestamptz not null,
  modified_files jsonb not null,
  status text not null default 'pending',
  attempts integer not null default 0,
  last_error text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
)
```

The source slice commit and `merge_events` row must be in the same DB
transaction. If the transaction commits, the event exists. If the transaction
rolls back, there is no accepted merge to promote.

The promotion worker can then:

1. Claim pending events with `FOR UPDATE SKIP LOCKED`.
2. Group them by home slice and path.
3. Apply only the latest event for each `(home_slice_id, path)`.
4. Upsert manifest refs and directory entries.
5. Update home metadata and root/global state.
6. Mark events promoted.

---

## Conflict Resolution With Same-Home Overlap

Same-home overlap is the most important correctness case.

Scenario:

```
alice/api-a includes alice/app/foo.go
alice/api-b includes alice/app/foo.go
```

Conflict resolution must continue to work even when home promotion lags.

Required merge-time behavior:

1. Normalize the modified paths.
2. Load the changeset snapshot's `base_path_versions`.
3. Read `home_path_heads` for the touched `(home_id, path)` rows.
4. Return `NEEDS_SYNC` if any current path version differs from the snapshot
   base version.
5. Atomically compare-and-set the path heads to their new versions and append
   the accepted merge event in the same transaction.

This means home materialization lag does not hide conflicts. A stale home view
can affect reads from the home slice, but it cannot make an invalid source-slice
merge valid.

Conflict resolution flow:

```
MergeChangeset(slice B)
  -> snapshot base_path_versions[alice/app/foo.go] = 12
  -> home_path_heads[alice/app/foo.go].path_version = 13
  -> return MERGE_STATUS_STALE_BASE
  -> no merge event appended
  -> no promotion event exists
```

`file_slice_index` and materialized home/root trees are read models for
compatibility and discovery. For snapshot-backed changesets, they must not be
used as merge conflict authority.

---

## Proactive Changeset State

The merge-time conflict check remains the final authority, but users should not
have to discover stale or conflicting work only after pressing merge. Changeset
detail and list views should proactively evaluate each open changeset and show a
state that tells the user what to do next.

Recommended states:

| State | Meaning | User action |
| --- | --- | --- |
| `READY_FOR_MERGE` | Touched path heads still match the changeset snapshot's base path versions. | Merge. |
| `NEEDS_SYNC` | One or more touched path heads changed after the changeset was exported. | Sync/rebase the changeset against the latest path heads. |
| `MERGED` | The changeset was accepted and has a merged timestamp/commit. | No merge action. |

This state is exposed as `ChangesetInfo.review_status` for open changesets in
list/detail responses. `MERGED` remains the lifecycle `ChangesetStatus`, not a
review status. The status can be computed on demand for open changesets:

1. Load the changeset and source slice metadata.
2. Load the latest changeset snapshot.
3. If the snapshot has base versions for every touched path, compare those
   versions with `home_path_heads`.
4. If any path diverged, return `NEEDS_SYNC`; otherwise return
   `READY_FOR_MERGE`.
5. If no complete path-head snapshot exists, return `NEEDS_SYNC`; the changeset
   must be re-exported before it can merge.

The proactive state is advisory and may be cached, but it must not replace the
merge transaction's synchronous validation. Another slice can merge after the UI
renders `READY_FOR_MERGE`, so `MergeChangeset` must still repeat the path-head
CAS before appending a merge event.

Async promotion does not change this logic. The proactive status should inspect
path heads and snapshot manifest refs. It should not depend on whether
home/root materialized views are current.

---

## API Consistency Options

Async promotion creates a product/API choice.

### Option A: merge returns after source commit only

Response:

```json
{
  "status": "SUCCESS",
  "newCommitHash": "commit_...",
  "promotionStatus": "PENDING"
}
```

Best for throughput. Callers that need home/root freshness can wait explicitly.

### Option B: merge supports an optional wait flag

Request:

```json
{
  "changesetId": "chg_...",
  "waitForPromotion": true
}
```

Default can be fast. Tests, CLI commands, and user workflows that need immediate
home/root consistency can opt into waiting.

### Option C: separate promotion status endpoint

Expose:

```
GET /v1/merge-events/{commit_hash}/promotion-status
```

Useful when web UI wants to show "merged, publishing..." without blocking the
merge request.

Recommendation: implement A internally first, keep test helpers waiting on the
queue, then add B or C only where the UI/CLI needs explicit freshness.

---

## Implementation Plan

### Phase 1: Remove avoidable merge writes

- Replace `slice_locks` and `file_locks` table writes with transaction-scoped
  advisory locks.
- Keep lock keys sorted by `(slice_id, path)` to avoid deadlocks.
- Use `pg_try_advisory_xact_lock` when the API should return `LOCKED` instead
  of blocking.

This reduces write amplification before changing consistency behavior.

### Phase 2: Durable merge event append

- Add `merge_events` with a monotonic `seq`.
- Append the event in the same transaction that marks the changeset merged and
  updates source slice metadata.
- Include `modified_files`, `commit_hash`, `source_slice_id`, and derived
  `home_slice_id` when available.

### Phase 3: Async home/root promotion worker

- Change request-time merge to enqueue/append promotion work and return.
- Let background workers claim pending events with `FOR UPDATE SKIP LOCKED`.
- Group events by home slice.
- Apply per-path monotonic promotion guards.
- Mark events promoted only after all materialized writes succeed.

### Phase 4: Freshness APIs and test updates

- Keep `WaitForQueuedPromotions` or equivalent for integration tests and
  benchmark integrity checks.
- Treat `GetSliceCommits` and `gs slice history` as projection-backed reads.
- Add optional CLI/UI wait behavior where users expect immediate history or
  home/root visibility. `gs changeset merge --wait` waits for the projection
  tokens returned by the merge response, while default merge returns after the
  accepted merge event commits.
- Surface promotion lag in admin/debug endpoints.
- Surface proactive changeset state in list/detail APIs so users see whether to
  merge, sync, or resolve conflicts before attempting the merge.
- Backfill older merge events with
  `storage_migrate backfill-history-projection --dsn <dsn> --namespace <ns>`.

### Phase 5: Defer non-critical stats

- Move line-count/diff-stat computation out of the critical merge path, or store
  line counts in file manifests at write time.
- Keep commit/file history out of the synchronous merge path. Product flows that
  need immediate file history should wait on the projection token rather than
  reintroducing foreground writes.

---

## Invariants

The design is correct only if these invariants hold:

1. A changeset cannot be marked merged unless path-head CAS passes.
2. A merge event is appended in the same transaction as the accepted source
   slice commit.
3. Promotion workers never overwrite a path with an older `merge_seq`.
4. Promotion is idempotent and safe to retry.
5. Home/root view lag never participates in conflict authority.
6. APIs that require history or home/root freshness explicitly wait for the
   relevant projection.
7. File/folder overlap within the same home is treated the same as overlap
   across different homes: home path heads decide conflicts.
8. Proactive changeset state is advisory; the merge transaction repeats all
   path-head CAS checks before accepting a merge.

---

## Expected Throughput Impact

The target is to make merge throughput depend on the small source-slice commit
transaction rather than on home/root materialization.

Expected changes:

- Merge latency should drop by roughly the current promotion latency for the
  fast path.
- Promotion throughput can improve through batching because workers can process
  many accepted events together.
- Hot homes still serialize per path via monotonic upserts, but unrelated homes
  and unrelated paths can proceed in parallel.

This does not make total system work disappear. It moves derived-view work out
of the user-facing merge request, makes it retryable, and prevents stale workers
from corrupting home/root materialization.

---

## Open Questions

1. Should CLI `gs changeset merge` ever wait for home/root promotion by default,
   or should only explicit wait modes block after the accepted merge event?
2. Should the web UI read source slices immediately after merge and only use
   home/root views when promotion is current?
3. How much promotion lag is acceptable before surfacing an admin warning?
4. Do we need per-path promotion status, or is per-commit status enough?
5. Should line stats be synchronous for history correctness, or eventually
   enriched after merge?
