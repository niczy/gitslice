# Path to 100k Merges Per Second

## Goal

Support a sustained target on the order of 100,000 accepted changeset merges per
second while preserving conflict correctness and keeping user-facing read models
usable.

This is not a tuning target for the current merge implementation. It requires a
change in what the synchronous merge path is allowed to do.

The core shift:

```text
Current shape:
  merge request -> validate -> update source slice -> materialize derived views -> return

Target shape:
  merge request -> validate -> append ordered merge fact -> return
  background workers -> build every derived view
```

At 100k merges/sec, any synchronous work that grows with directory depth,
history fanout, root materialization, search indexing, file history enrichment,
or UI list projection will dominate the budget. Merge must become a small,
partitioned, append-oriented operation.

## Current Baseline

Recent local Postgres benchmark:

```text
BENCHMARK_USERS=5000
BENCHMARK_WORKERS=128
BENCHMARK_HOME_SHARDS=64
BENCHMARK_POSTGRES_MAX_CONNS=64

Throughput: 56.0 full workflows/sec

CreateSliceFromFolder P50: 699.65 ms
CreateChangeset       P50: 759.24 ms
MergeChangeset        P50: 771.14 ms
End-to-end            P50: 2270.66 ms
```

Follow-up local Postgres run after moving commit history fully behind
`history-projection`:

```text
BENCHMARK_USERS=5000
BENCHMARK_WORKERS=128
BENCHMARK_POSTGRES_MAX_CONNS=64

Throughput: 35.2 full workflows/sec
MergeChangeset P50: 1971.09 ms
Foreground pool empty acquires: 225,577
Foreground pool cumulative acquire wait: 3h8m22s
Promotion drain: 0.09 s
```

With a separate small promotion pool:

```text
BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS=4

Throughput: 38.7 full workflows/sec
MergeChangeset P50: 1906.81 ms
Foreground pool empty acquires: 179,459
Foreground pool cumulative acquire wait: 2h15m55s
Promotion drain: 0.12 s
```

Merge-acceptance-only local Postgres benchmark after pre-creating ready
changesets:

```text
BENCHMARK_USERS=5000
BENCHMARK_WORKERS=128
BENCHMARK_POSTGRES_MAX_CONNS=64
BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS=4

Merge throughput: 150.8 accepted merges/sec
MergeChangeset P50/P95/P99: 836.36 ms / 978.82 ms / 1303.74 ms
Foreground pool acquisitions: 60,000
Foreground pool empty acquires: 1,158
Foreground pool cumulative acquire wait: 47.22 s
Promotion drain: 0.44 s
```

With more workers and more foreground connections, staying below this local
Postgres instance's `max_connections=100` cap:

```text
BENCHMARK_USERS=5000
BENCHMARK_WORKERS=192
BENCHMARK_POSTGRES_MAX_CONNS=88
BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS=4

Merge throughput: 153.5 accepted merges/sec
MergeChangeset P50/P95/P99: 1176.08 ms / 1744.71 ms / 2215.93 ms
Foreground pool acquisitions: 60,000
Foreground pool empty acquires: 1,670
Foreground pool cumulative acquire wait: 2m4s
Promotion drain: 0.39 s
```

After switching merge acceptance to the Postgres fast path, removing merge-time
history/root promotion, collapsing acceptance to one statement, and trimming
non-hot-path indexes:

```text
BENCHMARK_USERS=5000
BENCHMARK_WORKERS=128
BENCHMARK_HOME_SHARDS=64
BENCHMARK_POSTGRES_MAX_CONNS=64
BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS=4

Merge throughput: 733.7 accepted merges/sec
MergeChangeset P50/P95/P99: 140.75 ms / 394.89 ms / 1008.00 ms
Foreground pool acquisitions: 5,000
Foreground pool empty acquires: 1,782
Foreground pool cumulative acquire wait: 1m54s
Promotion drain: 0.00 s
```

Raising the foreground pool to 92 connections on the same local Postgres
instance reduced throughput to 543.9 accepted merges/sec and worsened tail
latency. This points to database write/CPU/WAL pressure and statement work, not
just a connection shortage.

A larger attempted pool (`BENCHMARK_POSTGRES_MAX_CONNS=128`,
`BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS=8`) failed during benchmark setup
because local Postgres rejected new sessions with `FATAL: sorry, too many
clients already`; the instance reported `max_connections=100`.

The extra foreground improvement from removing the synchronous
`slice_commits` insert is modest because the benchmark is dominated by
connection acquisition and other foreground reads/writes in create-slice,
create-changeset, validation, and merge finalization. The change still removes
one synchronous commit-history write from accepted merge, but it is not enough
to move full-workflow throughput by itself.

This benchmark is a full workflow, not just merge acceptance:

```text
CreateSliceFromFolder -> CreateChangeset -> MergeChangeset
```

The throughput also lines up with the concurrency limit:

```text
128 workers / 2.27s P50 end-to-end latency ~= 56 workflows/sec
```

The merge-acceptance-only benchmark shows that the current accepted-merge path
is also well below 1k/sec on this setup. Raising the foreground connection pool
from 64 to 88 did not materially improve throughput and worsened latency. That
does not mean connection count is irrelevant, but it does mean `max_connections`
alone is not the right optimization lever. The synchronous merge path is still
doing too many independent storage operations per accepted merge.

The current merge-only run performed about 60,000 foreground pool acquisitions
for 5,000 accepted merges, or about 12 foreground DB acquisitions per merge,
before counting the separate promotion drain pool. The path to 100k/sec cannot
depend on increasing goroutines and Postgres connections. The synchronous write
set and round-trip count must shrink.

## Definitions

Accepted merge:
: A durable fact that a changeset was accepted after conflict checks passed.

Merge event:
: The immutable record of an accepted merge. It includes the home scope, source
  slice, changeset, touched paths, base versions, new content refs, commit time,
  and a monotonic sequence inside the conflict scope.

Conflict authority:
: The strongly consistent data used to decide if a merge can be accepted. This
  must be small and partitionable.

Projection:
: Any read-optimized view derived from accepted merge events. Home file trees,
  root/global views, file history, search indexes, counters, and UI list data
  are projections.

Materialization lag:
: The distance between the latest accepted merge sequence and the latest sequence
  visible in a derived read model.

## Design Principles

1. **Accepted merge is the source of truth.**
   If a merge event is committed, the merge happened. If no event exists, it did
   not happen.

2. **Conflict checks happen before event append.**
   Async projection must never decide whether a merge was valid.

3. **Root is a projection, not a synchronous write target.**
   The root tree should be derived from home/path heads and merge events. It
   cannot be updated synchronously on every merge at high throughput.

4. **Every hot-path write is partitioned.**
   A merge should write only to the shard responsible for its home/path conflict
   scope. No global locks, no global root row, no global head update.

5. **Files are content-addressed before merge.**
   Merge accepts references to immutable blob/chunk manifests. It does not write
   file bytes.

6. **Derived views are monotonic and idempotent.**
   Projection workers may retry, replay, reorder across shards, and batch. They
   must not overwrite newer state with older events.

7. **Clients receive freshness metadata.**
   Merge responses include a sequence token. Reads that require freshness can
   wait for a projection to catch up.

## Why Root Is Materialized Today

Root is materialized today because older read APIs treat root as an ordinary
slice tree backed by `directory_entries`, file manifests, slice metadata, and a
head commit. That made simple read-after-merge behavior easy: after merge, the
root tree already contained the copied file refs.

That materialized root is a compatibility/read model, not the merge truth. In
the high-throughput design, the truth is the accepted merge event plus
home-scoped path heads. Root should be one of these:

- an async projection over merge events/path heads
- a query-time view over projected home/path heads
- a cached read model with explicit freshness status

Keeping root materialization synchronous would reintroduce a global write target
and make every merge pay for derived view maintenance. It is useful for legacy
reads, but it should not participate in merge authority or the foreground merge
latency budget.

## Why the Current Shape Cannot Reach 100k/sec

The current workflow does too many synchronous things:

- creates or updates changeset records
- writes file content/manifests
- updates directory entries
- appends source slice commit state
- updates file history records
- updates changeset status
- enqueues or runs home/root promotion
- updates root/global metadata
- supports read-after-merge behavior through materialized views

Even if each accepted merge wrote only 10 rows, 100k merges/sec would mean
roughly 1 million row writes/sec before indexes, locks, replication, and
projection work. The current path can write significantly more than that across
multiple tables and indexes.

The bottleneck is architectural: too much derived state is coupled to the
foreground merge request.

## Target Hot Path

The target synchronous merge path should fit this shape:

```text
1. Authenticate and authorize.
2. Resolve merge scope: home_id and touched path set.
3. Verify all file content refs already exist in CAS.
4. Read current path heads for touched paths.
5. Compare current path versions against changeset base versions.
6. If any path diverged, reject with conflict.
7. Assign a monotonic sequence in the merge shard.
8. Atomically update path heads for touched paths.
9. Append one accepted merge event.
10. Return merge_seq and projection freshness tokens.
```

The hot path should not:

- materialize home or root directory trees
- rebuild directory ancestors
- compute search indexes
- compute full file history
- update global/root metadata
- publish files into another materialized slice
- wait for projection workers

## Conflict Authority Model

Changesets are scoped to the user's home directory. That simplifies the design:
the primary conflict authority can be home-scoped instead of global.

Conceptual table:

```sql
home_path_heads (
  home_id text not null,
  path text not null,
  path_version bigint not null,
  content_hash text,
  manifest_hash text,
  source_slice_id text not null,
  source_commit_hash text not null,
  last_merge_seq bigint not null,
  deleted boolean not null default false,
  updated_at timestamptz not null,
  primary key (home_id, path)
);
```

Merge-time compare-and-set:

```sql
UPDATE home_path_heads
SET path_version = path_version + 1,
    content_hash = $new_content_hash,
    manifest_hash = $new_manifest_hash,
    source_slice_id = $source_slice_id,
    source_commit_hash = $source_commit_hash,
    last_merge_seq = $merge_seq,
    deleted = $deleted,
    updated_at = NOW()
WHERE home_id = $home_id
  AND path = $path
  AND path_version = $expected_base_version;
```

For new files, insert with a known expected-empty precondition:

```sql
INSERT INTO home_path_heads (...)
VALUES (...)
ON CONFLICT (home_id, path) DO NOTHING;
```

If the affected row count does not match the touched path count, the merge sees
a conflict. The API can then return the specific paths whose observed versions
no longer match the changeset base.

Important behavior:

- Two changesets modifying the same `(home_id, path)` serialize through the same
  path-head row.
- Two changesets modifying disjoint paths under the same home can proceed
  concurrently if the sequence allocator and transaction shape permit it.
- Two homes are fully independent.

## Merge Event Model

The merge event is the durable, replayable fact consumed by all projections.

Conceptual table:

```sql
merge_events (
  home_id text not null,
  shard_id int not null,
  merge_seq bigint not null,
  event_id text not null,
  changeset_id text not null,
  source_slice_id text not null,
  source_commit_hash text not null,
  author text not null,
  message text,
  touched_paths jsonb not null,
  path_updates jsonb not null,
  created_at timestamptz not null,
  primary key (shard_id, merge_seq),
  unique (event_id),
  unique (changeset_id)
);
```

`path_updates` should contain enough information for projections to rebuild
views without rereading mutable source slice state:

```json
[
  {
    "path": "alice/app/main.go",
    "baseVersion": 17,
    "newVersion": 18,
    "contentHash": "sha256:...",
    "manifestHash": "sha256:...",
    "deleted": false
  }
]
```

The event append and path-head updates must commit atomically. If the transaction
commits, the merge is accepted. If it rolls back, nothing should observe the
merge as accepted.

## Sequence Allocation

The system needs monotonic ordering for correctness, but only inside the
conflict/projection scope that needs ordering.

Avoid:

- one global sequence for every merge
- one global root commit head
- one global advisory lock

Prefer:

```text
shard_id = hash(home_id) % N
merge_seq = next sequence value inside shard_id
```

For even higher scale, split large homes by path prefix:

```text
shard_id = hash(home_id + ":" + first_path_component) % N
```

The tradeoff is conflict complexity for operations that touch many path shards.
For the first high-throughput design, home-level sharding is simpler and likely
enough until individual homes become hot.

## Content-Addressed File Storage

To keep merge small, content must already be stored before merge acceptance.

Upload/export path:

```text
1. Chunk file bytes.
2. Store chunks by content hash.
3. Store immutable file manifest by manifest hash.
4. Build changeset snapshot that references manifest hashes.
5. Merge references manifests; it does not write file content.
```

Conceptual data:

```sql
cas_chunks (
  hash text primary key,
  size_bytes bigint not null,
  storage_uri text not null,
  created_at timestamptz not null
);

file_manifests_by_hash (
  manifest_hash text primary key,
  total_size bigint not null,
  chunk_count int not null,
  manifest_json jsonb not null,
  created_at timestamptz not null
);
```

The merge path only verifies that the referenced manifest hashes exist. That
verification can be batched by changeset export time, so merge may only need to
trust a previously validated changeset snapshot.

## Projection Model

Everything users browse after merge is a projection.

Examples:

- home directory tree
- root/global tree
- slice commit history
- file history
- changeset list status
- search index
- stats and counts
- notifications

Each projection tracks progress:

```sql
projection_offsets (
  projection_name text not null,
  shard_id int not null,
  applied_seq bigint not null,
  updated_at timestamptz not null,
  primary key (projection_name, shard_id)
);
```

Projection workers:

```text
1. Claim a range of merge events for a shard.
2. Batch by home_id and path.
3. Apply only events newer than the currently materialized path.
4. Update projection offset after writes commit.
5. Retry failed ranges until successful.
```

Materialized path writes must be monotonic:

```sql
INSERT INTO materialized_home_paths (
  home_id,
  path,
  manifest_hash,
  source_slice_id,
  merge_seq,
  deleted,
  updated_at
)
VALUES (...)
ON CONFLICT (home_id, path) DO UPDATE
SET manifest_hash = EXCLUDED.manifest_hash,
    source_slice_id = EXCLUDED.source_slice_id,
    merge_seq = EXCLUDED.merge_seq,
    deleted = EXCLUDED.deleted,
    updated_at = EXCLUDED.updated_at
WHERE materialized_home_paths.merge_seq < EXCLUDED.merge_seq;
```

This prevents stale projection workers from overwriting newer state.

## Freshness API

Merge responses should include enough information for clients to reason about
eventual consistency.

Example response:

```json
{
  "changesetId": "chg_...",
  "status": "MERGED",
  "homeId": "home:alice",
  "mergeShard": 12,
  "mergeSeq": 88420192,
  "sourceCommitHash": "chgver_...",
  "projections": {
    "homeTree": "PENDING",
    "rootTree": "PENDING",
    "fileHistory": "PENDING"
  }
}
```

Read APIs that require freshness can accept a token:

```text
GET /v1/files?home_id=home:alice&min_seq=88420192&wait_ms=2000
```

Behavior:

- If the projection has caught up, return immediately.
- If it catches up before `wait_ms`, return fresh data.
- If it does not catch up, return stale data with a freshness warning or return
  a timeout depending on API semantics.

CLI default can be stricter than web UI:

```text
gs changeset merge          -> return accepted merge when event commits
gs changeset merge --wait   -> wait for returned merge projections before returning
gs slice status             -> show projection lag
```

`GetSliceCommits` and `gs slice history` read the projected commit-history view.
They can lag an accepted merge unless the caller waits on the
projection tokens returned by `MergeChangeset`. The CLI `--wait` path waits for
those tokens so workflows that need deterministic read-after-merge history or
home/root visibility can opt in.

## Benchmark Strategy

The existing benchmark measures full workflow throughput. We need separate
benchmarks for each layer.

### Current-system tuning benchmarks

Run a matrix for the full workflow and merge acceptance-only paths:

```text
BENCHMARK_WORKERS:            128, 256, 512
BENCHMARK_POSTGRES_MAX_CONNS: 64, 96, 128
BENCHMARK_HOME_SHARDS:        64, 256, 1024
promotion workers:            1, 2, 4
promotion batch size:         512, 1024, 2048
promotion batch window:       50ms, 100ms, 250ms
```

Log pgx pool stats:

- max acquired connections
- acquire count
- empty acquire count
- acquire wait duration
- canceled acquire count
- acquired and idle count at end

This tells us whether benchmark workers, DB pool size, lock contention, or query
cost is the immediate limiter.

Current result:

- `64 -> 88` foreground connections did not raise merge-only throughput past
  roughly `150/sec`.
- Higher attempted pools failed against local Postgres `max_connections=100`.
- The next useful benchmark is not simply a larger connection count; it is an
  optimized merge path with fewer DB acquisitions per accepted merge.

### Future hot-path benchmarks

`TestMergeAcceptanceThroughput` now skips checkout/export timing and measures
only accepted merge calls against the current implementation:

```text
Prepare ready changesets.
Run N workers.
Each worker merges one changeset touching one path.
Measure accepted merge events/sec.
Measure conflicts/sec under deliberate overlap.
Measure p50/p95/p99 merge acceptance latency.
```

A future low-level hot-path benchmark should skip the legacy changeset and
slice metadata machinery entirely and measure the target append-oriented event
path:

```text
Prepare changeset snapshots with CAS manifests.
Run N workers.
Each worker appends one accepted merge event with path-head CAS.
Do not update slice metadata, file ownership indexes, or projection queues.
Measure accepted merge events/sec.
Measure p50/p95/p99 event-append latency.
```

Then add projection benchmarks separately:

```text
Consume merge_events from shard ranges.
Batch materialize home paths.
Measure events/sec consumed.
Measure projection lag under sustained ingest.
```

The service is production-ready for high scale only when both numbers are known:

```text
accepted merges/sec
projection events/sec per projection
```

## Scaling Ladder

### Stage 0: Current local improvements

Status:

- async in-process promotion
- promotion worker backpressure
- batched home/root promotion

Expected range:

```text
tens to low hundreds of full workflows/sec on one Postgres instance,
depending on workers, pool size, and hardware
```

Useful next work:

- benchmark worker/connection matrix
- pgx pool stats in benchmark logs
- separate foreground and promotion DB pools
- reduce synchronous round trips in CreateSliceFromFolder and CreateChangeset

### Stage 1: Durable queue and projection isolation

Goal:

```text
hundreds to low thousands of merges/sec
```

Changes:

- append durable merge/promotion events in the merge transaction
- projection workers claim events with `FOR UPDATE SKIP LOCKED`
- foreground DB pool separate from projection DB pools
- queue depth and projection lag metrics
- reconciliation worker for stale materialized views

This stage keeps Postgres as the primary store but removes most projection work
from user-visible latency.

### Stage 2: Path-head conflict authority

Goal:

```text
thousands to tens of thousands of accepted merges/sec
```

Changes:

- introduce `home_path_heads`
- merge uses compare-and-set on touched paths
- changeset base records path versions, not just source slice head
- root is no longer a merge-time participant
- file history and commit history become projections
- changeset status is a projection plus final merge-time validation

This is the point where conflict correctness is fully separated from
materialized file trees.

### Stage 3: Sharded merge log

Goal:

```text
tens of thousands to 100k accepted merges/sec
```

Changes:

- partition merge events by home or home/path shard
- eliminate global sequence and global root head
- route merge requests to shard owners or shard-specific DB partitions
- batch path-head CAS updates and event appends
- use append-optimized storage if a single Postgres primary cannot keep up

Possible storage choices:

- partitioned Postgres if the write rate remains inside one cluster's envelope
- Citus or another distributed Postgres layer for horizontal write scale
- FoundationDB/CockroachDB-style transactional KV for path-head CAS
- Redpanda/Kafka/Pulsar for append log plus transactional path-head store

The exact choice depends on whether the bottleneck is transactional CAS, event
append throughput, projection throughput, or operational complexity.

### Stage 4: Distributed projections and root query service

Goal:

```text
100k accepted merges/sec with bounded read-model lag
```

Changes:

- root/global tree is served by a query service over projected home/path heads
- projections scale independently per shard
- search indexing consumes merge events independently
- cold views can be built lazily
- hot home/path prefixes can be split into more shards

Root should not be a single mutable tree at this scale. It should be a derived
view assembled from sharded, monotonic path state.

## Operational Metrics

High-throughput merge support needs these metrics before production rollout:

Foreground:

- merge acceptance QPS
- merge acceptance p50/p95/p99 latency
- conflict rate
- DB pool acquire wait
- DB transaction time
- path-head CAS failure rate
- request timeout rate

Event log:

- events appended/sec
- append latency
- shard skew
- per-shard high-water mark

Projection:

- events consumed/sec per projection
- projection lag by shard
- oldest unprojected event age
- failed projection attempts
- retry count
- dead-letter count

Storage:

- row writes/sec by table
- index write amplification
- lock wait time
- WAL volume
- replication lag
- object store put/get latency

Product:

- percent of reads served stale
- wait-for-freshness success rate
- wait-for-freshness timeout rate
- user-visible "sync required" states

## Conflict Resolution With Async Projections

Async projection is safe only if conflict checks read conflict authority, not
materialized views.

Correct flow:

```text
1. User exports changeset with base path versions.
2. Other changes may merge and update home_path_heads.
3. User attempts merge.
4. Merge transaction compares base path versions to current home_path_heads.
5. If versions differ, reject as conflict.
6. If versions match, update heads and append merge event.
7. Projections catch up later.
```

Incorrect flow:

```text
1. User attempts merge.
2. Service checks stale home/root materialized tree.
3. Service accepts merge because projection has not caught up.
```

That incorrect flow must never exist. Materialized trees are read models, not
conflict authorities.

## Biggest Design Decisions

### 1. Home-level versus path-prefix sharding

Home-level sharding is simpler and matches the current product model. It works
well if most homes have moderate write rates.

Path-prefix sharding is more scalable for hot homes, but multi-path changesets
can span shards. That requires either a small distributed transaction, a
deterministic multi-shard lock order, or a higher-level conflict protocol.

Recommendation:

```text
Start with home-level shards.
Design event schemas so path-prefix shards can be added later.
```

### 2. Postgres versus append log

Postgres is the right next step because it preserves transaction semantics and
keeps implementation complexity manageable.

At 100k accepted merges/sec, Postgres may still be viable only with partitioning,
careful schema design, and controlled indexes. If event append or path-head CAS
outgrows a single primary, move the merge log to an append-optimized system and
keep conflict authority in a transactional shard store.

Recommendation:

```text
Use Postgres for Stage 1 and Stage 2.
Do not design Stage 2 APIs around Postgres-specific assumptions.
```

### 3. Read-after-merge semantics

Immediate read-after-merge from materialized home/root views is expensive. At
high throughput, the product contract should change:

```text
Merge acceptance is immediate and authoritative.
Projection freshness is explicit.
```

Recommendation:

```text
Default web reads can tolerate bounded lag.
CLI and tests can request freshness with wait tokens.
Admin surfaces should show lag clearly.
```

## Near-Term Action Plan

1. Add pgx pool stats to the current benchmark output.
2. Run the worker/connection matrix to find the current single-node ceiling.
3. Split foreground and promotion DB pools.
4. Add durable merge/promotion events in Postgres.
5. Move promotion workers to claim durable events.
6. Add projection lag and queue depth metrics.
7. Introduce home/path head tables for conflict authority.
8. Convert merge to path-head CAS plus event append.
9. Move file history, commit history enrichment, and root views behind
   projection workers.
10. Rebenchmark accepted merge events independently from full workflow setup.

## PR-by-PR Implementation Plan

The work should be split so each PR is reviewable, benchmarkable, and safe to
deploy independently. Avoid PRs that both introduce a new data model and switch
production reads/writes to it at the same time.

### PR 1: Benchmark observability

Scope:

- Add pgx pool stats to `benchmark_suite`.
- Log acquired connections, idle connections, max acquired connections, acquire
  count, empty acquire count, acquire wait time, canceled acquires, and
  connection creation/destruction counts.
- Add benchmark output fields for promotion queue drain time when the benchmark
  can observe it.

Validation:

- Existing benchmark still passes.
- Output clearly distinguishes foreground workflow duration from post-workload
  projection/promotion drain duration.

Behavior change:

- None.

### PR 2: Worker and connection matrix runner

Scope:

- Add a script or make target that runs the current Postgres benchmark across
  worker and pool-size combinations.
- Capture results into a machine-readable artifact such as JSON or CSV.
- Include the tested matrix in this document or in a companion benchmark note.

Initial matrix:

```text
BENCHMARK_WORKERS:            128, 256, 512
BENCHMARK_POSTGRES_MAX_CONNS: 64, 96, 128
BENCHMARK_HOME_SHARDS:        64, 256
```

The local runner for this matrix is:

```bash
BENCHMARK_POSTGRES_DSN=postgres://... make benchmark-postgres-matrix
```

It writes raw logs plus `results.csv` under `benchmark_suite/results/` by
default. `BENCHMARK_MATRIX_*` variables can narrow or expand the matrix without
changing benchmark code.

Validation:

- Matrix can be run locally against the benchmark Postgres DSN.
- Results include throughput, p50/p95/p99 latency, error count, pool wait, and
  promotion drain time.

Behavior change:

- None.

### PR 3: Separate foreground and promotion DB pools

Scope:

- Add configuration for a promotion/projection Postgres pool.
- Keep request-path storage on the foreground pool.
- Route root/home promotion workers through the promotion pool.
- Cap promotion pool size independently from foreground pool size.

Suggested defaults:

```text
POSTGRES_MAX_CONNS=64
POSTGRES_PROMOTION_MAX_CONNS=4
```

Benchmark override:

```text
BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS=4
```

Validation:

- Existing tests pass.
- Benchmark shows foreground pool wait is not dominated by promotion workers.
- Promotion still drains and integrity checks pass.

Behavior change:

- Lower risk of async promotion starving foreground merge/create requests.
- Possible increase in materialization lag under heavy load because promotion is
  intentionally capped.

### PR 4: Durable merge event schema

Scope:

- Add Postgres tables for accepted merge events and projection offsets.
- Add storage interfaces for appending and reading merge events.
- Do not switch production merge or promotion behavior yet.

Conceptual tables:

```sql
merge_events (...)
projection_offsets (...)
```

Storage API shape:

```text
AppendMergeEvent
GetMergeEventByChangeset
ListMergeEvents(shard_id, after_seq, limit)
UpdateProjectionOffset
GetProjectionOffset
```

Validation:

- Storage tests cover insert, uniqueness, shard ordering, range reads, and
  offset updates.
- No generated protobuf outputs are committed unless API definitions change.

Behavior change:

- None.

### PR 5: Dual-write accepted merge events

Scope:

- Append a durable merge event in the same transaction that marks the changeset
  merged and appends the source slice commit.
- Keep existing in-process promotion behavior active.
- Add a debug/admin read path to inspect recently appended merge events.

Validation:

- If a merge succeeds, the corresponding merge event exists.
- If a merge fails or conflicts, no accepted merge event exists.
- Existing merge workflow tests still pass.

Behavior change:

- Merge does one additional durable write.
- No user-visible behavior change.

### PR 6: Durable promotion worker behind a feature flag

Scope:

- Add a worker that claims unpromoted merge events from Postgres.
- Use `FOR UPDATE SKIP LOCKED` or equivalent claim semantics.
- Apply promotion idempotently and update projection offsets.
- Keep the current in-process queue as the default until the durable worker has
  parity.

Feature flag:

```text
MERGE_EVENT_PROMOTION_ENABLED=true
MERGE_EVENT_PROMOTION_WORKERS=1
MERGE_EVENT_PROMOTION_BATCH_SIZE=256
MERGE_EVENT_PROMOTION_SHARDS=1024
MERGE_EVENT_PROMOTION_POLL_INTERVAL=250ms
```

When enabled, the worker holds a projection claim while applying the existing
promotion logic. A dedicated promotion pool should therefore be shared or have
at least two connections so a claim holder does not starve its own promotion
writes.

Validation:

- Worker can replay events without corrupting materialized state.
- Worker can resume after simulated process restart.
- Projection lag metrics move as expected.

Behavior change:

- None by default if feature flag is off.

### PR 7: Switch promotion to durable events

Scope:

- Make request-time merge return after durable merge event append.
- Remove dependence on the in-process queue for normal promotion.
- Keep synchronous wait behavior only for paths that still require it, such as
  config changes, until those reads are redesigned.

Runtime default:

```text
MERGE_EVENT_PROMOTION_ENABLED=true
```

Set `MERGE_EVENT_PROMOTION_ENABLED=false` to fall back to the in-process queue
while investigating projection lag or worker issues.

Validation:

- Crash/restart test: a merge accepted before restart is eventually promoted
  after restart.
- Existing root/home materialization tests use explicit waits.
- Benchmark verifies foreground throughput and promotion lag separately.

Behavior change:

- Home/root views become durably eventually consistent.
- Merge response may return before materialized views reflect the merge.

### PR 8: Freshness tokens and wait API

Scope:

- Return merge shard and merge sequence from merge responses.
- Add a projection status or wait endpoint.
- Add optional wait behavior for CLI/tests that need read-after-merge
  materialization.

Implemented API shape:

```text
MergeChangesetResponse.merge_home_id
MergeChangesetResponse.merge_shard
MergeChangesetResponse.merge_seq
MergeChangesetResponse.projections[]

GET /v1/projections/{projection_name}/status?shard_id=N&merge_seq=N&wait_ms=N
```

Validation:

- A client can merge, wait for home/root projection to reach the returned
  sequence, then read fresh materialized state.
- A timeout returns a clear stale/pending state instead of hiding lag.

Behavior change:

- Clients get explicit materialization status instead of assuming immediate
  freshness.

### PR 9: Content-addressed changeset snapshot hardening

Scope:

- Ensure changeset export writes immutable file manifests/chunks before merge.
- Make merge validate manifest references instead of writing file bytes.
- Add tests for repeated references to the same content hash and missing content
  refs.

Validation:

- Merge rejects missing or invalid content refs.
- Merge does not perform file-content writes in the hot path for exported
  changesets.

Behavior change:

- Changeset export carries more responsibility for content preparation.

### PR 10: Home path-head schema and backfill

Scope:

- Add `home_path_heads`.
- Backfill heads from existing home/root materialized state.
- Add storage methods for reading current path versions by home and path.
- Add validation tooling to compare path heads against materialized views.

Validation:

- Backfill is idempotent.
- Validation reports no drift on staging data or gives actionable drift output.
- No merge behavior switches yet.

Behavior change:

- None.

### PR 11: Record changeset base path versions

Scope:

- Store base path versions in changeset snapshots.
- Update export/create flows to include expected path versions for modified
  files.
- Update proactive changeset state to use path-head versions where available.

Validation:

- Open changesets show `READY` or `NEEDS_SYNC` from base-version data.
- Changesets without complete base path versions are treated as needing sync and
  must be re-exported.

Behavior change:

- More precise proactive conflict/sync status.

### PR 12: Remove legacy conflict authority

Scope:

- Delete source-slice-head and active-slice conflict checks from merge/review.
- Require complete changeset snapshot `base_path_versions` before merge.
- Return `NEEDS_SYNC`/`STALE_BASE` when path-head authority is unavailable.

Validation:

- Deliberate stale path-head tests prove overlapping writes are rejected.
- Snapshot-backed changesets are not blocked by stale active-slice indexes.

Behavior change:

- Path-head CAS is the only merge conflict authority.

### PR 13: Switch merge authority to path-head CAS

Scope:

- Make merge acceptance atomically update `home_path_heads` and append the merge
  event.
- Existing source slice commit/history updates become projections or secondary
  writes after the accepted event.
- Remove root/home materialization from merge authority.
- Treat `file_slice_index` as a read/discovery index, not merge authority.

Validation:

- Conflicting changes to the same path cannot both merge.
- Disjoint-path changes under the same home can merge concurrently.
- Changesets are not blocked by stale active-slice indexes when their path-head
  base versions are current.
- Accepted merge event and path-head updates commit atomically.
- Benchmarks measure accepted merge QPS separately from projections.

Behavior change:

- Conflict correctness is based on path heads, not materialized views or root
  state.

### PR 14: Move history projections off the merge path

Scope:

- Build file history and commit history from merge events.
- Keep read APIs compatible by reading projected history.
- Add backfill/reconciliation for existing history.

Validation:

- History APIs return the same logical data after projection catches up.
- Projection lag is visible when history is behind.

Behavior change:

- History may be eventually consistent unless callers wait for freshness.

### PR 15: Shard merge events and projections

Scope:

- Partition merge events by home shard.
- Partition projection workers by shard.
- Add per-shard high-water marks and lag metrics.
- Route hot homes or path prefixes to additional shards when needed.

Validation:

- Increasing shard count increases accepted merge throughput or projection
  throughput in benchmark.
- Shard skew is visible in metrics.

Behavior change:

- None expected at API level.

### PR 16: Accepted-merge benchmark suite

Scope:

- Add a benchmark that measures only merge acceptance against prepared
  changesets and CAS manifests.
- Add separate projection benchmarks for home tree, root view, file history, and
  search indexing.
- Report accepted merges/sec and projection events/sec independently.

Validation:

- The benchmark can answer whether the bottleneck is merge acceptance,
  projection, or read-model catchup.

Behavior change:

- None.

## PR Sequencing Rules

- Land observability before behavior changes.
- Land schema and dual-write before switching reads or writes.
- Keep every projection worker idempotent before enabling retries.
- Keep every user-visible eventual-consistency change paired with freshness
  status or wait support.
- Do not reintroduce source-slice-head or active-slice conflict authority.
- Every PR that changes merge semantics should include an e2e workflow test:
  checkout slice, make change, create changeset, export, merge, wait if needed,
  and verify file tree and history behavior.

## Non-Goals For The First Step

- Do not start with Kafka or another log system before the Postgres event model
  proves where the actual limit is.
- Do not optimize root materialization as if root remains a synchronous merge
  dependency.
- Do not increase Postgres connections without measuring pool wait, lock wait,
  and tail latency.
- Do not weaken conflict checks to improve throughput.

## Summary

The path to 100k merges/sec is:

```text
content-addressed changesets
  -> path-head conflict authority
  -> atomic accepted merge events
  -> sharded append path
  -> async monotonic projections
  -> explicit freshness tokens
```

The near-term work can still use Postgres and improve the current system, but
the high-scale design requires removing every derived view update from the
synchronous merge request. Merge should accept facts. Workers should build views.
