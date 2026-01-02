# Scalability Review of Current Prototype

## Summary
The architecture specification targets multi-node deployments backed by Redis metadata and an object store for immutable blobs, enabling horizontal scaling across shards and workers.【F:spec/ARCHITECTURE.md†L17-L168】 The current prototype runs entirely in-memory inside each service process, so its behavior diverges sharply from those expectations.

## Implementation Gaps Affecting Scale
- **Process-local state:** Both the slice and admin services create their own `InMemoryStorage` instances on startup. State never leaves process memory, leading to divergent views between services, no replication, and data loss on restart.【F:slice_service/main.go†L11-L25】【F:admin_service/main.go†L11-L31】
- **Single mutex bottleneck:** `InMemoryStorage` guards all maps with one `sync.RWMutex`, so high-volume operations serialize on that lock. There is no sharding or per-collection isolation, which constrains concurrent writes as slice counts grow.【F:internal/storage/memory.go†L12-L145】
- **Unbounded in-memory scans:** Listing slices builds an unsorted slice of every entry and then slices the array for pagination, which is O(n) and requires holding the read lock across the entire collection. Batch merge requests fetch every slice into memory before selecting candidates, amplifying the cost as the repository grows.【F:internal/storage/memory.go†L164-L185】【F:internal/services/admin/server.go†L37-L75】
- **No durability or object storage layer:** File content and metadata live only in maps (`fileContents`, `entries`, `sliceCommits`), with no backing object store, deduplication, or versioning. This bypasses the design’s content-addressable storage plan and cannot accommodate large histories or file volumes.【F:internal/storage/memory.go†L24-L143】

## Recommendations to Reach Design Targets
- Replace the in-memory backend with a shared persistence layer: Redis (for indexes and locks) plus an S3-compatible object store for blobs and manifests, matching the documented sharding and durability model.【F:spec/ARCHITECTURE.md†L91-L170】 Implement the `Storage` interface with these systems so both services share state.
- Introduce shard-aware locking and indexing: move from a single global mutex to per-slice or per-file locks stored in Redis, and restructure queries to operate on keyed indexes rather than scanning maps, enabling horizontal scaling of conflict detection and merge operations.
- Streamline batch merge and listing paths: paginate via cursor-based queries, order deterministically, and process merges in bounded batches pulled from a queue instead of loading all slices into memory. This aligns with the planned worker model and prevents memory blow-ups as slice counts increase.
- Add durability and recovery plumbing: persist commit history and file trees in the object store, keep Redis append-only logging enabled, and add bootstrap routines that rebuild indexes from persisted slice definitions to survive restarts and support multi-node deployments.
