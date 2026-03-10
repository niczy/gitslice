# Design: Optimize Filesystem API for Agent Workloads

## Problem Statement

The `FilesystemService` API has performance bottlenecks that compound for agent
workloads (AI coding agents making many small file edits). The root cause is that
file content is stored and transferred as monolithic blobs.

### Current storage model

```
AddFileContent(FileContent{
    FileID:  "slice-1:src/main.go",
    Content: []byte{...entire file...},   ← one opaque blob
    Hash:    "sha256-of-whole-file",
    Size:    52431,
})
    ↓
ObjectStore.PutObject("file_content:src/main.go", json(FileContent))
```

Every read, write, edit, and snapshot operation must load or store the **entire
file** as a single object. This creates cascading inefficiencies:

1. **Full-file round-trip for edits**: Changing one line requires downloading
   ~50 KB, editing client-side, re-uploading ~50 KB.
2. **Full tree walk on every commit**: `commitWorkspaceMutation` calls
   `GetSliceFileByPath()` for *every* file to compute hashes for the snapshot —
   even though only one file changed.
3. **No partial reads**: Reading lines 100-120 of a file requires transferring
   the entire blob from object storage into server memory.
4. **No deduplication**: Two files with identical regions store redundant bytes.
   Two versions of a file that differ by one line store two full copies.
5. **O(total-size) writes**: Writing a 1-byte edit to a 1 MB file rewrites 1 MB
   to object storage.

---

## Core Proposal: Content-Addressable Block Storage

Split file content into fixed-size, content-addressed blocks stored on R2/GCS.
This is the same model used by git packfiles, rsync, IPFS, and every modern CAS.

### Block model

```
┌─────────────────────────────────────────────────────┐
│ File: src/main.go  (52 KB)                          │
│                                                     │
│ FileManifest {                                      │
│   path: "src/main.go"                               │
│   total_size: 52431                                 │
│   hash: "sha256-of-whole-file"                      │
│   blocks: [                                         │
│     { hash: "abc123", offset: 0,     size: 16384 }, │
│     { hash: "def456", offset: 16384, size: 16384 }, │
│     { hash: "ghi789", offset: 32768, size: 16384 }, │
│     { hash: "jkl012", offset: 49152, size: 3279  }, │
│   ]                                                 │
│ }                                                   │
└─────────────────────────────────────────────────────┘

ObjectStore keys:
  block:abc123 → 16384 bytes
  block:def456 → 16384 bytes
  block:ghi789 → 16384 bytes
  block:jkl012 → 3279 bytes
  manifest:slice-1:src/main.go → FileManifest JSON
```

Each block is keyed by the SHA-256 of its content. Identical blocks across files
and versions are stored once.

### Data structures

#### New Go types (`internal/models/block.go`)

```go
package models

// Block represents a content-addressed chunk of file data.
type Block struct {
    Hash string `json:"hash"`          // SHA-256 of content
    Size int    `json:"size"`          // Byte length
}

// FileManifest describes a file as an ordered list of blocks.
type FileManifest struct {
    Path      string  `json:"path"`
    TotalSize int64   `json:"total_size"`
    Hash      string  `json:"hash"`        // SHA-256 of full file content
    Blocks    []Block `json:"blocks"`
}
```

#### Block size selection

| Block size | Blocks per 50 KB file | Blocks per 1 MB file | Overhead |
|-----------|----------------------|---------------------|----------|
| 4 KB      | 13                   | 256                 | High manifest size |
| 16 KB     | 4                    | 64                  | Good balance |
| 64 KB     | 1                    | 16                  | Less dedup opportunity |

**Recommendation: 16 KB.** Source code files are typically 1-100 KB. At 16 KB
blocks, a typical file has 1-6 blocks — small manifests, good dedup, and edits
usually touch only 1-2 blocks.

For files smaller than the block size, the manifest has exactly one block — no
overhead compared to the current monolithic model.

### New storage interface methods (`internal/storage/storage.go`)

```go
// Block storage operations (content-addressable)
PutBlock(ctx context.Context, hash string, data []byte) error
GetBlock(ctx context.Context, hash string) ([]byte, error)
HasBlock(ctx context.Context, hash string) (bool, error)
PutBlocks(ctx context.Context, blocks map[string][]byte) error  // batch

// File manifest operations
PutFileManifest(ctx context.Context, sliceID, path string, manifest *models.FileManifest) error
GetFileManifest(ctx context.Context, sliceID, path string) (*models.FileManifest, error)
DeleteFileManifest(ctx context.Context, sliceID, path string) error
```

### ObjectStore key layout

```
blocks/{hash}                               ← content-addressed, shared
manifests/{sliceID}/{path}                 ← per-slice-file
```

On R2/GCS, blocks are immutable and globally deduplicated. Manifests are small
JSON documents (~200 bytes per file) that point to blocks.

---

## How Chunking Enables Each Optimization

### 1. Efficient `EditFile` — rewrite only affected blocks

```
EditFile(slice_id, "src/main.go", edits=[{old: "foo", new: "bar"}])

Server:
  1. Load manifest (1 small GET)         ← ~200 bytes
  2. Determine which blocks contain "foo"
     - Option A (fast): load block hashes, try each block
     - Option B (indexed): use a line→block index (see below)
  3. Load only affected block(s) (1 GET)  ← ~16 KB
  4. Apply edit in memory
  5. Hash the new block
  6. PUT new block if hash is new         ← ~16 KB
  7. Update manifest with new block hash  ← ~200 bytes
  8. Incremental commit (manifest already has all hashes)
```

**Before**: Edit touches 50 KB file → 50 KB GET + 50 KB PUT = 100 KB I/O.
**After**: Edit touches 1 block → 200B GET + 16 KB GET + 16 KB PUT + 200B PUT ≈ 33 KB I/O.

For larger files the savings compound: editing a 1 MB file drops from 2 MB I/O
to ~33 KB I/O.

#### Optional: line-to-block index

For text files, store a small side index mapping line number ranges to block
indices. This allows `EditFile` to skip straight to the right block(s) without
scanning:

```go
// LineIndex maps line ranges to block indices within a FileManifest.
type LineIndex struct {
    Entries []LineBlockEntry `json:"entries"`
}

type LineBlockEntry struct {
    StartLine  int `json:"start_line"`   // 1-based
    EndLine    int `json:"end_line"`     // inclusive
    BlockIndex int `json:"block_index"` // index into FileManifest.Blocks
}
```

Stored alongside the manifest. Rebuilt on write (cheap — just scan for newlines
while chunking). Makes line-based reads O(1) lookup instead of O(blocks) scan.

**Whether to build this index depends on typical file sizes.** For files with
1-4 blocks (< 64 KB), sequential scan is fine. For files with 64+ blocks (> 1 MB),
the index pays off. **Recommendation: defer the line index to Phase 2 and
measure first.** The block-level architecture works without it.

### 2. Incremental commit snapshots — free with manifests

The current bottleneck in `commitWorkspaceMutation`:

```
workspaceStats()                    ← walks ALL entries
collectWorkspaceSnapshotFiles()     ← walks ALL entries AGAIN
  └── GetSliceFileByPath() × N     ← loads EVERY file to hash it
```

With block storage, the manifest already contains the file hash. The commit
snapshot becomes:

```go
func (s *server) commitWorkspaceMutationIncremental(
    ctx context.Context,
    workspace *models.Slice,
    message string,
    changedPaths []string,
    deletedPaths []string,
) (string, error) {
    meta, _ := s.storage.GetSliceMetadata(ctx, workspace.ID)

    // Start from parent snapshot
    files := copyParentSnapshot(ctx, meta.HeadCommitHash)

    // Patch in changed files — just read manifests, NOT content
    for _, p := range changedPaths {
        manifest, err := s.storage.GetFileManifest(ctx, workspace.ID, p)
        if err != nil {
            continue
        }
        files[p] = manifest.Hash  // Hash already computed during write
    }

    for _, p := range deletedPaths {
        delete(files, p)
    }

    // Save snapshot + metadata (same as before)
    return saveCommitAndSnapshot(ctx, workspace, files, message)
}
```

**Before**: 500-file workspace, 1 edit → ~1500 storage calls.
**After**: 1 GetCommitSnapshot + 1 GetFileManifest + 3 saves = **5 storage calls**.

The key insight: **block storage makes file hashes available from the manifest
without loading content**. This is why chunking is foundational — it fixes the
snapshot problem as a side effect.

### 3. Partial file reads — serve only the needed blocks

```
ReadFile(path="src/main.go", byte_offset=32768, byte_limit=16384)

Server:
  1. Load manifest                            ← 200 bytes
  2. Compute: offset 32768 falls in block[2]  ← arithmetic
  3. GET block[2] only                        ← 16 KB
  4. Slice to requested range
  5. Return
```

**Before**: Read 16 KB of a 1 MB file → transfer 1 MB from object store.
**After**: Transfer 16 KB. The server never loads the rest.

Line-based reads work the same way with the optional line index — look up which
block(s) contain the target lines, fetch only those blocks.

### 4. Cross-version deduplication

When an agent edits line 50 of a 200-line file, only the block containing that
line changes. The other blocks share the same hash as the previous version and
are **not re-stored**. `HasBlock` short-circuits the PUT.

```
Version 1: [block:aaa, block:bbb, block:ccc, block:ddd]
Version 2: [block:aaa, block:bbb, block:XYZ, block:ddd]
                                       ↑ only new block stored
```

For agent workloads doing 20-50 small edits to a codebase, this cuts storage
writes dramatically — most blocks across most files never change.

---

## Implementation Plan

### Phase 0: Block storage layer (foundation)

1. Add `Block` and `FileManifest` types to `internal/models/block.go`
2. Add `PutBlock`, `GetBlock`, `HasBlock`, `PutBlocks`, `PutFileManifest`,
   `GetFileManifest`, `DeleteFileManifest` to the `Storage` interface
3. Implement for `InMemoryStorage` (map-based, straightforward)
4. Implement for `PostgresNativeStorage` using existing `ObjectStore`:
   - Blocks: `PutObject("blocks/{hash}", data)` — idempotent, content-addressed
   - Manifests: `PutObject("manifests/{sliceID}/{path}", json)` — small metadata
   - Postgres metadata table: `file_manifests(slice_id, path, hash, total_size, block_count)`
5. Add helper functions:
   - `ChunkFile(data []byte, blockSize int) ([]Block, map[string][]byte)` — splits file into blocks
   - `AssembleFile(manifest *FileManifest, getBlock func(hash string) ([]byte, error)) ([]byte, error)` — reassembles
   - `FindBlocksForRange(manifest *FileManifest, offset, length int64) []int` — which blocks to fetch

### Phase 1: Replace file storage with block storage

1. **Replace `AddFileContent`** with block-based write:
   chunk content → PutBlocks (skip existing via HasBlock) → PutFileManifest
2. **Replace `GetSliceFileByPath`** with block-based read:
   GetFileManifest → GetBlock for each block → assemble
3. **Remove legacy `FileContent` storage path entirely** — no dual-write,
   no fallback. All file I/O goes through manifests + blocks.
4. **Replace `commitWorkspaceMutation`** with `commitWorkspaceMutationIncremental`:
   use manifest hashes instead of loading content
5. **Drop legacy storage methods** that are now unused:
   - Remove `AddFileContent`, `GetSliceFileByPath`, `GetFileContentByHash`
   - Remove `collectWorkspaceSnapshotFiles`, `workspaceStats`
   - Drop `file_contents` and `versioned_content` Postgres tables
   - Remove corresponding `ObjectStore` keys (`file_content:*`, `versioned_content:*`)

### Phase 2: EditFile RPC with block-level targeting

1. Add `EditFile` proto definition and RPC
2. Server-side implementation:
   - Load manifest
   - For each edit: scan blocks to find which contain `old_text`
     (for files with ≤6 blocks, just load and scan all — still fast)
   - Load only affected blocks, apply edits, re-chunk the modified region
   - PutBlock for new blocks, update manifest
3. Add `expected_hash` for optimistic concurrency (compare against manifest.Hash)
4. Wire into incremental commit

### Phase 3: Partial ReadFile with block-level serving

1. Add `byte_offset`/`byte_limit`/`line_offset`/`line_limit` to ReadFileRequest
2. Compute which blocks overlap the requested range
3. Fetch only those blocks, slice to exact range
4. Optional: add LineIndex for O(1) line→block lookup on large files

### Phase 4: Batch EditFiles + dedup metrics

1. `EditFiles` RPC: multiple files, one commit
2. Add metrics: block dedup ratio, blocks reused vs written, manifest sizes
3. Optional: background GC for orphaned blocks (blocks with no manifest references)

---

## Migration Strategy

Breaking change — no backward compatibility required. Clean cutover.

### What gets removed

| Legacy component | Replacement |
|-----------------|-------------|
| `FileContent` model (monolithic blob) | `FileManifest` + `Block` models |
| `AddFileContent()` | `PutBlocks()` + `PutFileManifest()` |
| `GetSliceFileByPath()` | `GetFileManifest()` + `GetBlock()` |
| `GetFileContentByHash()` | `GetBlock()` (blocks are content-addressed) |
| `collectWorkspaceSnapshotFiles()` | Read manifest hashes directly |
| `workspaceStats()` / `collectWorkspaceEntries()` | Derive from snapshot/manifests |
| `file_contents` Postgres table | `file_manifests` table |
| `versioned_content` Postgres table | `blocks` table (or just ObjectStore keys) |
| `file_content:*` ObjectStore keys | `blocks/{hash}` keys |
| `versioned_content:*` ObjectStore keys | `blocks/{hash}` keys (same) |

### Storage interface changes

Remove:
```go
// DELETE these methods from Storage interface
AddFileContent(ctx context.Context, content *models.FileContent) error
GetSliceFiles(ctx context.Context, sliceID string) ([]*models.FileContent, error)
GetSliceFileByPath(ctx context.Context, sliceID, path string) (*models.FileContent, error)
GetFileContentByHash(ctx context.Context, contentHash string) (*models.FileContent, error)
```

Add:
```go
// ADD these methods to Storage interface
PutBlock(ctx context.Context, hash string, data []byte) error
GetBlock(ctx context.Context, hash string) ([]byte, error)
HasBlock(ctx context.Context, hash string) (bool, error)
PutBlocks(ctx context.Context, blocks map[string][]byte) error
PutFileManifest(ctx context.Context, sliceID, path string, manifest *models.FileManifest) error
GetFileManifest(ctx context.Context, sliceID, path string) (*models.FileManifest, error)
DeleteFileManifest(ctx context.Context, sliceID, path string) error
```

### Database migration

```sql
-- New tables
CREATE TABLE file_manifests (
    slice_id   TEXT NOT NULL,
    path       TEXT NOT NULL,
    hash       TEXT NOT NULL,
    total_size BIGINT NOT NULL,
    block_count INT NOT NULL,
    PRIMARY KEY (slice_id, path)
);

-- Drop old tables
DROP TABLE IF EXISTS file_contents;
DROP TABLE IF EXISTS versioned_content;
```

Existing slice data is discarded. Slices start fresh after the migration.

---

## Performance Analysis

### Single-file edit (50 KB file, 1-line change)

| Operation | Current | With blocks |
|-----------|---------|-------------|
| Client → Server | 50 KB (full file) | ~200 B (edit instruction) |
| Server → Object Store reads | 50 KB (full blob) | 200 B (manifest) + 16 KB (1 block) |
| Server → Object Store writes | 50 KB (full blob) | 16 KB (1 block) + 200 B (manifest) |
| Commit snapshot work | ~1500 storage calls (full tree walk) | 5 calls (incremental) |
| **Total I/O** | **~100 KB + 1500 calls** | **~33 KB + 7 calls** |

### Partial read (lines 100-120 of 1 MB file)

| Operation | Current | With blocks |
|-----------|---------|-------------|
| Object Store → Server | 1 MB (full file) | 200 B (manifest) + 16 KB (1 block) |
| Server → Client | ~2 KB (after server-side trim) | ~2 KB |
| **Object Store I/O** | **1 MB** | **~16 KB** |

### Cross-version storage (20 edits to 500-file workspace)

| Metric | Current | With blocks |
|--------|---------|-------------|
| New objects stored per edit | 1 × full file | 1-2 blocks (16-32 KB) |
| Total new storage for 20 edits | 20 × ~50 KB = 1 MB | 20 × ~16 KB = 320 KB |
| Shared blocks reused | N/A | ~95% of blocks unchanged |

---

## Block Size Considerations

The 16 KB recommendation assumes source code files. Different workloads might
want different sizes:

| Content type | Recommended block size | Rationale |
|-------------|----------------------|-----------|
| Source code (1-100 KB) | 16 KB | 1-6 blocks per file, good edit locality |
| Config/JSON (< 4 KB) | 16 KB | Single block, no overhead |
| Large generated files (> 1 MB) | 64 KB | Fewer blocks in manifest |
| Binary assets | 64 KB or 256 KB | Dedup unlikely, minimize manifest size |

Start with a single global block size (16 KB). Make it configurable per-slice
later if needed — the manifest format supports variable block sizes since each
block entry records its own size.

---

## Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| Block GC complexity (orphaned blocks) | Blocks are immutable and cheap. Defer GC. Reference counting can be added later via a `block_refs` table. |
| Manifest consistency (crash between block write and manifest update) | Blocks are content-addressed — writing an extra block is harmless. Manifest update is atomic (single PutObject). If manifest write fails, old manifest is still valid. |
| Small file overhead (manifest larger than file) | Files < 16 KB have a single block. Manifest adds ~200 bytes. Acceptable. Could inline tiny files (< 1 KB) directly in manifest. |
| Cross-block edits (edit spans a block boundary) | Load both blocks, concatenate, apply edit, re-chunk. Slightly more I/O but correct. Rare for typical edits. |
| Existing slice data lost | Acceptable — breaking change. Slices start fresh after migration. |
| R2/GCS latency for many small GETs | `PutBlocks`/`GetBlocks` batch API. Manifest reads are cacheable (small, frequent). |

---

## Testing Strategy

- **Unit tests**: `ChunkFile` / `AssembleFile` round-trip for various file sizes
  and block sizes. Verify content-hash stability. Test cross-block edits.
- **Storage tests**: `internal/storage/` — test `PutBlock`/`GetBlock`/`HasBlock`
  for both in-memory and Postgres+ObjectStore backends.
- **End-to-end tests**: Full write → read → edit → snapshot cycle through the
  new block storage path. Verify file content integrity after chunking and
  reassembly.
- **Snapshot correctness**: Verify incremental snapshots produce correct hashes
  by writing multiple files, editing one, and checking the snapshot diff.
- **Build verification**: `go build ./servers/core/` and `go build ./gs_cli/`
