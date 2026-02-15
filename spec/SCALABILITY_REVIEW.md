# Scalability Review of Current Prototype

## Executive Summary

The architecture spec originally targeted a distributed system backed by an object store and Redis-style indexes, but the current implementation path is PostgreSQL + GCS with in-memory compatibility. This document summarizes the gaps and near-term steps to align the codebase with that target architecture.

## Current Prototype Constraints

- **Process-local state:** The core server uses an `InMemoryStorage`, so state is not shared across processes and is lost on restart. See [`servers/core/main.go`](../servers/core/main.go).
- **Global mutex contention:** `internal/storage/memory.go` guards all maps behind one RWMutex, which serializes high-volume operations.
- **In-memory scans:** Listing slices and batch merge paths iterate full in-memory collections, which will not scale as slice counts grow. See [`internal/storage/memory.go`](../internal/storage/memory.go) and [`services/admin/server.go`](../services/admin/server.go).
- **No durable blob store:** File contents and metadata live in memory only, bypassing the planned content-addressable object store.

## Recommended Persistent Storage Design: PostgreSQL + GCS

To align the existing `Storage` interface with production durability requirements, use a split backend:

- **PostgreSQL for transactional metadata and indexes** (slices, entries, changesets, commits, locks).
- **GCS-compatible object storage for immutable blobs** (file content, snapshots, large payloads).

This preserves the current in-memory programming model while adding crash safety, multi-instance consistency, and recovery.

### Data Placement

#### PostgreSQL (source of truth for metadata)

- `slices` + `slice_metadata` tables for slice identity, owner, parent, timestamps, and head commit pointers.
- `slice_commits` and `commit_snapshots` tables for commit ordering and snapshot manifests.
- `changesets` table for lifecycle state and merge metadata.
- `directory_entries` table with `(slice_id, path)` unique index for path lookups.
- `file_changes` append-only table indexed by `(slice_id, path, committed_at DESC)` for history queries.
- `file_slice_index` table for conflict detection (file -> active slices), replacing in-memory maps.
- `global_state` singleton row for root slice and system pointers.
- `locks` table (or advisory locks) for `LockSliceAndFiles` semantics.

#### GCS (immutable binary/state objects)

- Content-addressed blobs under `blobs/sha256/<hash>` for file content.
- Snapshot payloads under `snapshots/<commit_hash>.json` when snapshot manifests are large.
- Optional archival export under `manifests/<slice_id>/<commit_hash>.json` for offline rebuild.
- Enable bucket versioning + lifecycle rules (hot storage for recent objects, infrequent-access/archive for old snapshots).

### Storage API Mapping

- `AddFileContent`:
  1. Hash content.
  2. Write blob to GCS (idempotent by hash key).
  3. Upsert PostgreSQL metadata row with blob key, size, and checksum.
- `GetSliceFiles` / `GetSliceFileByPath`: query PostgreSQL for manifest rows, then hydrate content bytes from GCS only when needed.
- `CreateSlice`, `CreateChangeset`, `UpdateChangeset`, `AddSliceCommit`: single PostgreSQL transactions with row-level locking.
- `ResolveConflict`: transactionally update `file_slice_index` and conflict rows, then commit.
- `GetFileHistory` and directory history queries: execute directly from indexed `file_changes` in PostgreSQL.

### Consistency and Transactions

- Use PostgreSQL as the commit boundary for metadata correctness.
- For write paths touching both Postgres and GCS:
  - Upload object to GCS first.
  - Commit metadata transaction second.
  - If metadata commit fails, leave unreferenced object for async GC.
- Add an `object_gc_candidates` table populated when metadata references are removed.
- Run a periodic GC job to delete unreferenced GCS keys safely after grace period.

### Operational Notes

- **Schema migrations:** use versioned SQL migrations checked into repo.
- **Connection management:** pgx pool with bounded max connections per service instance.
- **Backups:** PostgreSQL PITR + GCS versioning.
- **Recovery:** restore PostgreSQL first, then validate referenced GCS keys; rebuild derived indexes if needed.
- **Multi-region path (future):** primary Postgres region with read replicas; GCS cross-region replication for blobs.

### Why This Fits Current Code

- The existing `ObjectStore` abstraction already supports a GCS backend (`internal/storage/objectstore.go`).
- The current `Storage` interface cleanly maps to relational operations plus blob lookup without service API changes (`internal/storage/storage.go`).
- `PostgresNativeStorage` preserves the `Storage` interface so handler code in `services/` remains unchanged.

## Detailed Schema and Transaction Design

For the concrete PostgreSQL schema, index strategy, transaction boundaries, concurrency model, invariants, and phased rollout plan, see [STORAGE_DB_DESIGN.md](./STORAGE_DB_DESIGN.md).
