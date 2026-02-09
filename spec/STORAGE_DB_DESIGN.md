# Storage DB Design: PostgreSQL + S3

## Goals

- Preserve correctness for all current `Storage` interface operations under high write/read concurrency.
- Make metadata strongly consistent and durable through PostgreSQL transactions.
- Keep large/immutable payloads in S3 with content-addressed keys.
- Support incremental rollout from current in-memory/Redis-backed implementation.

## Scope and Data Ownership

### Path Tracking Decision (DB vs S3)

We **should track file paths and directory paths in PostgreSQL**, not in S3 object keys.

- S3 object keys should represent immutable blob identity (`blobs/sha256/<hash>`) and optional snapshot artifacts.
- Repository semantics (`slice_id`, file `path`, directory hierarchy, rename/move history) belong to transactional metadata in Postgres.
- A single blob hash may be referenced by multiple `(slice_id, path)` rows, so path cannot be inferred from object storage.

Therefore:

- Store canonical path mappings in `directory_entries(slice_id, path, parent_id, ...)` and `file_contents(slice_id, path, content_hash, ...)`.
- Keep S3 path-free from logical repo structure to preserve deduplication and immutability.
- Use DB indexes for path lookups/history (`(slice_id, path)`), while S3 only serves bytes by content key.

### PostgreSQL (authoritative metadata)

PostgreSQL is the source of truth for:

- Slice lifecycle and metadata (`CreateSlice`, `GetSlice`, `UpdateSliceMetadata`)
- Changesets and state transitions (`CreateChangeset`, `UpdateChangeset`)
- Directory tree and path indexes (`AddEntry`, `GetEntryByPath`, `ListEntries`)
- Conflict membership and lock ownership (`AddFileToSlice`, `ResolveConflict`, `LockSliceAndFiles`)
- Commit history and queryable change logs (`AddSliceCommit`, `GetFileHistory`, `GetCommitChanges`)
- Global singleton state (`GetGlobalState`, `GetRootSlice`)

### S3 (immutable object payloads)

S3 stores:

- File blobs: `blobs/sha256/<hash>`
- Large commit snapshots: `snapshots/<commit_hash>.json`
- Optional exported manifests for bootstrap/audit: `manifests/<slice_id>/<commit_hash>.json`

Metadata rows reference S3 object keys; S3 is never used as the transactional coordinator.

## Logical Schema

The schema below focuses on correctness and query behavior used by the existing interface.

### Core entities

- `slices(id PK, name, description, created_by, parent_id NULL, is_root bool, status, created_at, updated_at)`
- `slice_owners(slice_id FK->slices, owner text, PRIMARY KEY(slice_id, owner))`
- `slice_metadata(slice_id PK FK->slices, head_commit_hash NULL, last_modified, modified_files_count, modified_files jsonb)`
- `slice_commits(slice_id FK->slices, seq bigint, commit_hash, parent_hash NULL, author, message, committed_at, PRIMARY KEY(slice_id, seq), UNIQUE(slice_id, commit_hash))`
- `changesets(id PK, hash UNIQUE, slice_id FK->slices, base_commit_hash, status, title, description, author, message, created_at, updated_at, merged_commit_hash NULL, merged_at NULL)`
- `changeset_files(changeset_id FK->changesets, path text, PRIMARY KEY(changeset_id, path))`

### File and directory model

- `directory_entries(entry_id PK, slice_id FK->slices, parent_id NULL FK->directory_entries, path text, name text, type text, created_at, updated_at, UNIQUE(slice_id, path))`
- `file_contents(file_id PK, slice_id FK->slices, path text, content_hash text, byte_size bigint, created_at, UNIQUE(slice_id, path))`
- `slice_files(slice_id FK->slices, file_id text, PRIMARY KEY(slice_id, file_id))`
- `commit_snapshots(commit_hash PK, slice_id FK->slices, timestamp timestamptz, snapshot_inline jsonb NULL, snapshot_s3_key NULL, created_at)`

### Conflict/index model

- `file_slice_index(file_id text, slice_id text FK->slices, active boolean, created_at, PRIMARY KEY(file_id, slice_id))`
- `file_conflicts(file_id PK, path text, conflicting_slices text[], preferred_slice_id NULL, resolved bool, updated_at)`
- `slice_file_locks(file_id PK, owner_slice_id FK->slices, lock_token uuid, expires_at, created_at)`
- `slice_locks(slice_id PK FK->slices, lock_token uuid, expires_at, created_at)`

### History and global state

- `file_changes(change_id bigserial PK, slice_id FK->slices, path text, old_path text NULL, commit_hash text, change_type text, old_hash text NULL, new_hash text NULL, lines_added int, lines_deleted int, author text, message text, committed_at timestamptz, metadata jsonb)`
- `global_state(id bool PRIMARY KEY DEFAULT true CHECK (id), root_slice_id text FK->slices, global_commit_hash text NULL, updated_at)`
- `global_commits(global_commit_hash PK, committed_at timestamptz, merged_slice_ids text[])`
- `object_gc_candidates(object_key PK, reason text, marked_at timestamptz, not_before timestamptz)`

### Entity Coverage Audit (missing entities to include)

To fully cover the current in-memory domain models and `Storage` interface behavior, add the following entities/columns (some are not explicit in earlier drafts):

- **Slice owners and authoring metadata**
  - Keep normalized `slice_owners(slice_id, owner)` and `slices.created_by` / `slices.description`.
- **Slice file membership table**
  - Keep `slice_files(slice_id, file_id)` as canonical membership (separate from lock/conflict index tables).
- **Changeset modified files**
  - Keep `changeset_files(changeset_id, path)` to persist `ModifiedFiles` in queryable form.
- **Commit parent linkage**
  - Keep `slice_commits.parent_hash` to preserve linear history traversal and validation.
- **Conflict read model fields**
  - Keep `file_conflicts(path, conflicting_slices, resolved)` for `ListConflicts` compatibility.

If these are omitted, parity gaps will appear versus current API/CLI expectations.

## File and Directory Change History Design

Change history should be tracked in PostgreSQL as an append-only event log, with directory history derived from file events.

### Canonical history record

Use `file_changes` as the source of truth for history events:

- Required fields: `(slice_id, path, commit_hash, change_type, committed_at)`
- Useful denormalized fields for query speed: `(author, message, metadata)`
- Recommended uniqueness guard: `UNIQUE(slice_id, commit_hash, path)` to prevent duplicate publish on retries.

### How directory history is represented

Do not store a separate mutable "directory history" object in S3.

- Directory history for `path_prefix` is computed by querying `file_changes` where `path LIKE '<prefix>/%'` (or equivalent index-friendly predicate).
- Optional materialized aggregates can be maintained in Postgres for hot directories, but raw `file_changes` remains the authoritative log.

### Rename/move modeling

Represent rename as events on affected paths so history remains auditable:

- Emit `rename_from` (old path) and `rename_to` (new path) in same commit.
- Store `metadata.renamed_to` / `metadata.renamed_from` for reverse navigation.
- Directory moves expand to per-file rename events during commit publish.

### Query patterns and indexes

- File history: `WHERE slice_id=$1 AND path=$2 ORDER BY committed_at DESC LIMIT $N`.
- Directory history: `WHERE slice_id=$1 AND path >= $prefix_low AND path < $prefix_high` (preferred over wildcard when feasible).
- Commit view: `WHERE commit_hash=$1 ORDER BY path`.

Indexes:

- `idx_file_changes_slice_path_time (slice_id, path, committed_at DESC)` for file queries.
- `idx_file_changes_slice_time (slice_id, committed_at DESC)` for recent activity feeds.
- `idx_file_changes_commit (commit_hash)` for commit drill-down.

### Correctness guarantees

- History rows are inserted in the same transaction as `slice_commits` append and head update.
- If commit transaction rolls back, no history rows become visible.
- Consumers never read history from S3; S3 remains blob/snapshot storage only.

## Index Strategy

### Hot path indexes

- `CREATE INDEX idx_slice_owners_owner ON slice_owners(owner);`
- `CREATE INDEX idx_changesets_slice_status_updated ON changesets(slice_id, status, updated_at DESC);`
- `CREATE INDEX idx_entries_slice_parent_name ON directory_entries(slice_id, parent_id, name);`
- `CREATE UNIQUE INDEX idx_entries_slice_path ON directory_entries(slice_id, path);`
- `CREATE INDEX idx_file_contents_slice_path ON file_contents(slice_id, path);`
- `CREATE INDEX idx_file_slice_active ON file_slice_index(file_id) WHERE active = true;`
- `CREATE INDEX idx_file_changes_slice_path_time ON file_changes(slice_id, path, committed_at DESC);`
- `CREATE INDEX idx_file_changes_commit ON file_changes(commit_hash);`
- `CREATE UNIQUE INDEX idx_file_changes_slice_commit_path ON file_changes(slice_id, commit_hash, path);`
- `CREATE INDEX idx_slice_files_slice ON slice_files(slice_id);`
- `CREATE INDEX idx_changeset_files_changeset ON changeset_files(changeset_id);`

### History and pagination indexes

- `CREATE INDEX idx_slice_commits_slice_seq_desc ON slice_commits(slice_id, seq DESC);`
- `CREATE INDEX idx_changesets_updated_id ON changesets(updated_at DESC, id);`

### Notes on cardinality and contention

- Use append-only patterns (`file_changes`, `slice_commits`) to avoid update hotspots.
- Keep lock rows narrow and short-lived to reduce btree churn.
- Use partial indexes for `active=true` where read ratio is high.

## Transaction and Correctness Design

## 1) Create/update metadata-only flows

For methods like `CreateSlice`, `UpdateSliceMetadata`, `CreateChangeset`, `UpdateChangeset`:

- `BEGIN`
- `SELECT ... FOR UPDATE` on target row(s) when transition validation is required.
- Apply mutation with explicit state checks in `WHERE` clauses.
- `COMMIT`

Correctness properties:

- Prevents lost updates through row locks + optimistic guards.
- Enforces legal state transitions (e.g., open -> merged/closed only once).

## 2) Lock acquisition (`LockSliceAndFiles`)

- `BEGIN`
- Upsert/check `slice_locks` for `slice_id` with TTL and lock token.
- For each file id, upsert/check `slice_file_locks` with owner equality semantics.
- If any lock conflict exists, rollback and return `ErrLockHeld`.
- `COMMIT`

Correctness properties:

- Atomic lock set acquisition across slice + file IDs.
- No partial lock success.
- TTL enables safe orphan recovery after crash.

## 3) Blob write + metadata pointer (`AddFileContent`)

- Compute hash locally.
- Upload blob to S3 key by hash (idempotent).
- `BEGIN`
- Upsert `file_contents` row with `(slice_id, path, content_hash, byte_size)`.
- Insert history entry if called from commit flow.
- `COMMIT`

Failure policy:

- If DB commit fails after S3 write, object may be orphaned; enqueue with `object_gc_candidates` asynchronously.

## 4) Commit append (`AddSliceCommit`, snapshots)

- `BEGIN`
- Lock metadata row `slice_metadata` for target slice (`FOR UPDATE`).
- Insert into `slice_commits` using next `seq` from monotonic allocator (DB sequence or computed lock-safe increment).
- Upsert `commit_snapshots` metadata pointer.
- Update `slice_metadata.head_commit_hash`.
- Insert batch `file_changes` rows.
- `COMMIT`

Correctness properties:

- Total ordering per slice via `(slice_id, seq)`.
- Commit and history publication are atomic.

## Isolation Level Guidance

- Default: `READ COMMITTED` for most flows with row-level locks.
- Use `SERIALIZABLE` only for highly contended reconciliation paths where predicate anomalies matter (for example complex conflict resolution scans).
- Keep transactions short; do not hold DB transactions while uploading to S3.

## High-Concurrency Patterns

- Use cursor pagination (`updated_at`, `id`) instead of large offset scans.
- Batch inserts (`file_changes`) with prepared statements and bounded batch size.
- Introduce worker pools for independent slice operations; avoid global mutexes.
- Add per-slice logical partitioning for high-volume tables as growth demands (native Postgres partitioning by hash on `slice_id`).
- Enable connection pooling (`pgxpool`) with strict max connections and timeout budgets.

## Invariants (must always hold)

- Exactly one root slice exists in `global_state`.
- `slice_metadata.slice_id` exists in `slices`.
- `directory_entries(slice_id, path)` is unique.
- Active lock rows with unexpired TTL cannot have conflicting owners.
- `slice_commits` ordering per slice is gap-tolerant but strictly monotonic.
- `file_changes.commit_hash` must reference a known commit for published history rows.

## Operational Safety

- Migrations: forward-only SQL migrations with online-safe DDL where possible (`CONCURRENTLY` for indexes).
- Backups: PITR for Postgres + S3 versioning.
- Recovery drill: restore DB, verify referenced S3 objects, rebuild derived caches if introduced.
- Observability: emit metrics for lock conflicts, txn retries, DB latency, S3 latency, GC backlog.

## Phased Implementation Plan

### Phase 0 — Foundations

- Add config for Postgres DSN and migration tooling.
- Introduce `PostgresStorage` skeleton implementing `Storage` interface behind feature flag.
- Add integration test harness that can run same storage test suite against memory and postgres backends.

### Phase 1 — Metadata migration

- Implement slice, changeset, entry, global state CRUD in Postgres.
- Keep file content paths backed by current object store abstraction.
- Verify parity with existing storage behavior through `internal/storage/storage_test.go` extensions.

### Phase 2 — Commit/history correctness

- Implement transactional commit append (`slice_commits`, `file_changes`, `slice_metadata` head updates).
- Add cursor-based history queries and benchmark against target latency.
- Add retry logic for serialization/deadlock failures.

### Phase 3 — Locking and conflict paths

- Implement atomic slice+file lock acquisition/release with TTL and lock tokens.
- Move conflict detection/index writes to DB transactions.
- Validate under concurrent workload tests.

### Phase 4 — S3 durability hardening

- Finalize content-addressed blob writes and orphan-GC job.
- Add consistency checker for dangling metadata/object references.
- Document operational runbooks for backup/restore and GC.

### Phase 5 — Rollout and deprecation

- Deploy dual-read validation in staging (compare memory/redis vs postgres responses for sampled calls).
- Cut over to Postgres primary storage.
- Remove legacy durable-state-in-object-store path after confidence window.
