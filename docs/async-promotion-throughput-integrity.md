# Design: Async Promotion, Throughput, and Conflict Integrity

## Problem Statement

The current merge workflow treats home/root promotion as part of the
user-visible merge operation. That gives strong read-after-merge behavior for
materialized home/root views, but it also makes every merge pay for work that is
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

1. Is the changeset based on the current slice head?
2. Does it conflict with any other active slice for the touched paths?
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
checking the active slices for that path and comparing their current file
manifest hashes.

Expected workflow:

1. Slice A changes `alice/app/foo.go`.
2. Slice A merges successfully.
3. Slice B tries to merge a different version based on an older file state.
4. Merge checks `file_slice_index` plus source-slice manifest hashes.
5. Slice B receives a conflict before it can append an accepted merge event.

This logic does not require the home slice to already reflect slice A. The home
slice is just a view. The active source slices and their manifest refs are the
conflict authority.

---

## Throughput Versus Integrity Tradeoff

### Fully synchronous promotion

Current behavior:

```
MergeChangeset
  -> validate conflicts
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

Proposed behavior:

```
MergeChangeset
  -> validate conflicts
  -> mark source changeset merged
  -> append source slice commit
  -> append durable promotion event
  -> return success

Promotion worker
  -> read accepted promotion events
  -> update home/root materialized views in batches
```

Advantages:

- Merge latency tracks correctness work, not view materialization.
- Promotions can be batched by home and path.
- Workers can be tuned independently from request concurrency.
- Retry/idempotency is easier because promotion consumes durable events.

Costs:

- Home/root views become eventually consistent.
- APIs that need a fully materialized home/root view need an explicit wait,
  freshness token, or promotion status check.
- Promotion must be order-independent and idempotent, or stale jobs can
  overwrite newer accepted content.

The integrity tradeoff is acceptable only if the merge transaction remains the
single authority and promotion uses monotonic guards.

---

## Required Safety Rule: Monotonic Per-Path Promotion

Async promotion must not be "last worker wins." Queue execution order is not a
valid correctness mechanism.

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
2. Read active source slices for those paths from `file_slice_index`.
3. For each active slice, read the current path manifest hash from
   `file_manifests`.
4. Compare the preferred slice hash to other active slice hashes.
5. Return a conflict if hashes differ.
6. Only append a merge event after conflict checks pass.

This means home materialization lag does not hide conflicts. A stale home view
can affect reads from the home slice, but it cannot make an invalid source-slice
merge valid.

Conflict resolution flow:

```
MergeChangeset(slice B)
  -> active slices for alice/app/foo.go = [slice A, slice B]
  -> hash(slice A, foo.go) != hash(slice B, foo.go)
  -> return MERGE_STATUS_CONFLICT
  -> no merge event appended
  -> no promotion event exists
```

---

## Proactive Changeset State

The merge-time conflict check remains the final authority, but users should not
have to discover stale or conflicting work only after pressing merge. Changeset
detail and list views should proactively evaluate each open changeset and show a
state that tells the user what to do next.

Recommended states:

| State | Meaning | User action |
| --- | --- | --- |
| `READY_FOR_MERGE` | Base commit matches the slice head and touched files do not currently diverge from other active slices. | Merge. |
| `NEEDS_SYNC` | The source slice head moved after the changeset base commit. | Sync/rebase the changeset against the latest slice head. |
| `HAS_CONFLICTS` | One or more touched paths diverge from another active slice. | Resolve conflicts before merge. |
| `MERGED` | The changeset was accepted and has a merged timestamp/commit. | No merge action. |

This state is exposed as `ChangesetInfo.review_status` for open changesets in
list/detail responses. `MERGED` remains the lifecycle `ChangesetStatus`, not a
review status. The status can be computed on demand for open changesets:

1. Load the changeset and source slice metadata.
2. Compare `changeset.base_commit_hash` with the source slice head.
3. If the base is stale, return `NEEDS_SYNC`.
4. Read active source slices for the changeset's modified files.
5. Compare current manifest hashes across those active slices.
6. If any touched path diverges, return `HAS_CONFLICTS`.
7. Otherwise return `READY_FOR_MERGE`.

The proactive state is advisory and may be cached, but it must not replace the
merge transaction's synchronous validation. Another slice can merge after the UI
renders `READY_FOR_MERGE`, so `MergeChangeset` must still repeat the stale-base
and conflict checks before appending a merge event.

Async promotion does not change this logic. The proactive status should inspect
source-slice heads, active slice membership, and manifest refs. It should not
depend on whether home/root materialized views are current.

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
- Add optional CLI/UI wait behavior where users expect immediate home/root
  visibility.
- Surface promotion lag in admin/debug endpoints.
- Surface proactive changeset state in list/detail APIs so users see whether to
  merge, sync, or resolve conflicts before attempting the merge.

### Phase 5: Defer non-critical stats

- Move line-count/diff-stat computation out of the critical merge path, or store
  line counts in file manifests at write time.
- Keep the commit/file history rows synchronous only if product behavior
  requires immediate file history after merge.

---

## Invariants

The design is correct only if these invariants hold:

1. A changeset cannot be marked merged unless conflict checks pass.
2. A merge event is appended in the same transaction as the accepted source
   slice commit.
3. Promotion workers never overwrite a path with an older `merge_seq`.
4. Promotion is idempotent and safe to retry.
5. Home/root view lag never participates in conflict authority.
6. APIs that require home/root freshness explicitly wait for promotion.
7. File/folder overlap within the same home is treated the same as overlap
   across different homes: active source slices decide conflicts.
8. Proactive changeset state is advisory; the merge transaction repeats all
   stale-base and conflict checks before accepting a merge.

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

1. Should CLI `gs changeset merge` wait for home/root promotion by default, or
   return immediately with a "publishing" status?
2. Should the web UI read source slices immediately after merge and only use
   home/root views when promotion is current?
3. How much promotion lag is acceptable before surfacing an admin warning?
4. Do we need per-path promotion status, or is per-commit status enough?
5. Should line stats be synchronous for history correctness, or eventually
   enriched after merge?
