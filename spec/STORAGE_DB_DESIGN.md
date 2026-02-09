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

- `slices(id PK, owner, name, parent_id NULL, status, created_at, updated_at)`
- `slice_metadata(slice_id PK FK->slices, head_commit_hash NULL, last_modified, modified_files_count, modified_files jsonb)`
- `slice_commits(slice_id FK->slices, seq bigint, commit_hash, author, message, committed_at, PRIMARY KEY(slice_id, seq), UNIQUE(slice_id, commit_hash))`
- `changesets(id PK, slice_id FK->slices, status, title, description, created_at, updated_at, merged_commit_hash NULL)`

### File and directory model

- `directory_entries(entry_id PK, slice_id FK->slices, parent_id NULL FK->directory_entries, path text, name text, type text, created_at, updated_at, UNIQUE(slice_id, path))`
- `file_contents(file_id PK, slice_id FK->slices, path text, content_hash text, byte_size bigint, created_at, UNIQUE(slice_id, path))`
- `commit_snapshots(commit_hash PK, snapshot_inline jsonb NULL, snapshot_s3_key NULL, created_at)`

### Conflict/index model

- `file_slice_index(file_id text, slice_id text FK->slices, active boolean, created_at, PRIMARY KEY(file_id, slice_id))`
- `file_conflicts(file_id PK, preferred_slice_id NULL, updated_at)`
- `slice_file_locks(file_id PK, owner_slice_id FK->slices, lock_token uuid, expires_at, created_at)`
- `slice_locks(slice_id PK FK->slices, lock_token uuid, expires_at, created_at)`

### History and global state

- `file_changes(change_id bigserial PK, slice_id FK->slices, path text, commit_hash text, change_type text, author text, message text, committed_at timestamptz, metadata jsonb)`
- `global_state(id bool PRIMARY KEY DEFAULT true CHECK (id), root_slice_id text FK->slices, updated_at)`
- `object_gc_candidates(object_key PK, reason text, marked_at timestamptz, not_before timestamptz)`

## Index Strategy

### Hot path indexes

- `CREATE INDEX idx_slices_owner_updated ON slices(owner, updated_at DESC);`
- `CREATE INDEX idx_changesets_slice_status_updated ON changesets(slice_id, status, updated_at DESC);`
- `CREATE INDEX idx_entries_slice_parent_name ON directory_entries(slice_id, parent_id, name);`
- `CREATE UNIQUE INDEX idx_entries_slice_path ON directory_entries(slice_id, path);`
- `CREATE INDEX idx_file_contents_slice_path ON file_contents(slice_id, path);`
- `CREATE INDEX idx_file_slice_active ON file_slice_index(file_id) WHERE active = true;`
- `CREATE INDEX idx_file_changes_slice_path_time ON file_changes(slice_id, path, committed_at DESC);`
- `CREATE INDEX idx_file_changes_commit ON file_changes(commit_hash);`

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
