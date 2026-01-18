# Scalability Review of Current Prototype

## Executive Summary

The architecture spec targets a distributed system backed by an object store and Redis indexes, but the current prototype is a single-process, in-memory implementation. This document summarizes the gaps and near-term steps to align the codebase with the target architecture.

## Current Prototype Constraints

- **Process-local state:** Slice and admin services each create their own `InMemoryStorage`, so state is not shared across processes and is lost on restart. See [`slice_service/main.go`](../slice_service/main.go) and [`admin_service/main.go`](../admin_service/main.go).
- **Global mutex contention:** `internal/storage/memory.go` guards all maps behind one RWMutex, which serializes high-volume operations.
- **In-memory scans:** Listing slices and batch merge paths iterate full in-memory collections, which will not scale as slice counts grow. See [`internal/storage/memory.go`](../internal/storage/memory.go) and [`internal/services/admin/server.go`](../internal/services/admin/server.go).
- **No durable blob store:** File contents and metadata live in memory only, bypassing the planned content-addressable object store.

## Recommendations to Reach Design Targets

- **Introduce a shared backend:** Implement the `Storage` interface with a Redis-backed index layer plus an S3-compatible object store, as described in [ARCHITECTURE.md](./ARCHITECTURE.md).
- **Shard locks and indexes:** Move from a single mutex to per-slice or per-file locks (stored in Redis), and replace full scans with keyed lookups.
- **Queue-based batch merge:** Replace full-slice scans with a bounded queue and cursor-based pagination to keep memory bounded.
- **Durability + recovery:** Persist slice manifests and commits to the object store and provide a bootstrap routine to rebuild Redis indexes after restart.

## Redis vs. Relational DB (Context)

- **Redis strengths:** Fast set membership and intersection for conflict detection, plus native data structures for queues and locks (see [ARCHITECTURE.md](./ARCHITECTURE.md)).
- **Relational strengths:** Strong transactional guarantees and flexible reporting, but slower set operations for conflict detection at scale.
- **Hybrid approach:** Redis for hot indexes and locks, with an object store (or relational backing store) for durable state.
