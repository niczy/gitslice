# File Service Improvements

This document captures concrete improvements to the File Service API and implementation,
ordered by priority (correctness > performance > maintainability).

---

## API Issues

### 1. `GetCommitChanges` has no auth check

**Severity:** High (security gap)

Every other File Service RPC validates slice access via `authz.HasSliceViewAccess`, but
`GetCommitChanges` (`services/file/server.go:929`) accepts any commit hash with zero
authorization. Since commits are tied to slices via `FileChangeRecord.SliceID`, the handler
should resolve the owning slice from the change records and enforce the same access check.

**Fix:** After fetching changes, extract the slice ID(s), load each slice, and call
`authz.HasSliceViewAccess`. Return `PermissionDenied` if the caller lacks access to any
referenced slice.

---

### 2. `findParentCommitHash` loads ALL commits -- O(n) linear scan

**Severity:** High (performance)

`findParentCommitHash` (`services/file/server.go:1061`) calls
`ListSliceCommits(ctx, sliceID, 0, "")` which fetches the **entire** commit history just to
find one parent hash. This is called per unique `(sliceID, commitHash)` pair in
`GetCommitChanges`.

The `Commit` model already stores `ParentHash`. The storage interface should expose a
`GetCommitByHash(ctx, sliceID, commitHash) (*models.Commit, error)` method for O(1) lookup
instead of requiring a full history scan.

**Depends on:** Storage interface change (see item #11 below).

---

### 3. No streaming for large file content

**Severity:** Medium

`GetFile` returns the entire file as `bytes content` in a unary response. For large files
(multi-MB binaries, data files) this blows up server memory and gRPC message limits.

**Options:**
- Add a server-streaming RPC `StreamFile` that returns content in chunks
- Set a size threshold (e.g., 10MB) and return a `too_large` error with a presigned URL for
  direct object-store download
- At minimum, enforce a max response size and return a clear error when exceeded

---

### 4. No `ETag` / conditional request support

**Severity:** Medium

File content is content-addressed (the `hash` field is already returned). The HTTP gateway
should set `ETag: <hash>` on `GetFile` responses and support `If-None-Match` headers to
return `304 Not Modified`. This is essentially free to implement and would significantly
reduce bandwidth for repeated reads (e.g., browser-based file viewers, IDE integrations).

**Implementation:**
- gRPC-Gateway metadata: set `ETag` header from `File.hash` in a response modifier
- Check incoming `If-None-Match` header before fetching content; if hash matches, skip
  the storage read entirely

---

### 5. `ListEntries` doesn't return content hashes

**Severity:** Medium

`DirectoryEntry` has `name`, `path`, `type`, `size`, `has_children` but no `hash` field.
Clients doing checkout or sync need to know which files changed since their last fetch.
Currently they must call `GetFile` on every path to discover this.

**Fix:** Add `string hash = 6;` to the `DirectoryEntry` proto message and populate it from
storage metadata. This enables efficient client-side diffing: compare local hashes against
listing hashes, only fetch files that differ.

---

### 6. ~~Patch generation in `GetCommitChanges` is eager and unbounded~~ **DONE**

**Status:** Resolved

**Severity:** Medium (performance)

Every change record gets a full unified diff computed via `buildChangePatch`, loading file
content from storage twice per change (before + after). For commits touching many files,
this is 2N storage reads with no parallelism and no way to opt out.

**Fix (any combination):**
- ~~Add `bool include_patches = 2;` to `GetCommitChangesRequest` so clients can opt in~~ ✅
- ~~Parallelize blob fetches with a bounded worker pool (e.g., `errgroup` with limit)~~ ✅ (8-worker bounded concurrency)
- ~~Cap patches: skip diff generation for commits with >100 changed files, or skip individual
  files larger than a threshold (e.g., 1MB)~~ ✅ (capped at 100 changed files)

---

## Implementation Issues

### 7. Massive code duplication in `ListEntries`

**Severity:** Medium (maintainability)

The directory entry construction logic (build `[]*filev1.DirectoryEntry` from
`[]*models.DirectoryEntry`, sort by name, apply limit/truncation) is copy-pasted **four
times** across the mounted-slice fast path, materialized-tree path, and two fallback paths
(lines ~284-316, ~348-384, ~399-436, ~444-484).

**Fix:** Extract a helper:

```go
func buildListResponse(sliceID, path string, children []*models.DirectoryEntry,
    slice *models.Slice, limit int32) *filev1.ListEntriesResponse
```

Each code path produces `[]*models.DirectoryEntry` children; the helper handles the
proto conversion, display-path mapping, sorting, and truncation uniformly.

---

### 8. `GetFile` has two divergent code paths

**Severity:** Medium (maintainability)

The mounted-slice path (`server.go:643-697`) and the non-mounted path (`server.go:699-758`)
duplicate the same fallback chain: `GetFileAtCommit` -> `GetSliceFileByPath` -> parent
slice fallback.

**Fix:** Unify into a single helper:

```go
func (s *fileServiceServer) resolveFileContent(
    ctx context.Context, sliceID string, slice *models.Slice,
    storedPath, resolvedCommit string,
) (*models.FileContent, error)
```

Both paths resolve a `storedPath` and `resolvedCommit` differently, but the content-loading
logic is identical and should live in one place.

---

### 9. Path cache has no invalidation strategy

**Severity:** Low

The `slicePathCache` is keyed by `sliceID|commit|mounts` with 64 LRU slots. Entries are
never proactively evicted. Since commit hashes are immutable this is safe for pinned-commit
queries, but HEAD-based queries could briefly serve stale data if the cache key resolves to
a commit hash that was just replaced.

**Current risk:** Low -- the cache key includes the resolved commit hash, so a HEAD advance
produces a new key. The only waste is stale entries occupying cache slots.

**Possible improvement:** Add a short TTL (e.g., 30s) or version the cache key with the
slice metadata's `LastModified` timestamp.

---

### 10. `effectiveSlicePaths` silently falls through on snapshot errors

**Severity:** Low (observability)

At `server.go:221`, if `GetCommitSnapshot` returns an error, the code silently falls through
to the legacy file-list path without logging. This can mask data consistency issues where
snapshots should exist but don't.

**Fix:** Log at `warn` level when a snapshot lookup fails for a non-empty commit hash:

```go
if snapshot, err := s.storage.GetCommitSnapshot(ctx, commitHash); err != nil {
    log.Printf("WARN: snapshot lookup failed for commit %s: %v, falling back to file list", commitHash, err)
} else if snapshot != nil {
    // use snapshot paths
}
```

---

## Storage Interface Issues

### 11. Missing `GetCommitByHash` on the storage interface

**Severity:** High (enables fix for item #2)

`Commit` stores `ParentHash`, but there's no way to look up a single commit by hash without
scanning the full history via `ListSliceCommits`. This forces O(n) scans wherever parent
commit resolution is needed.

**Fix:** Add to `storage.Storage`:

```go
// GetCommitByHash retrieves a single commit by its hash within a slice.
GetCommitByHash(ctx context.Context, sliceID, commitHash string) (*models.Commit, error)
```

In-memory implementation: maintain a `map[string]*models.Commit` index keyed by commit hash.
PostgreSQL implementation: `SELECT * FROM commits WHERE slice_id = $1 AND commit_hash = $2`.

---

### 12. `ListSliceCommits` with `limit=0` means "no limit"

**Severity:** Low (foot-gun)

A caller forgetting to set a limit silently gets the entire history. This is dangerous for
slices with deep history.

**Fix:** Either:
- Treat `limit <= 0` as "use default" (e.g., 100)
- Require an explicit limit and return `InvalidArgument` when it's missing
- At minimum, cap the maximum to a sane upper bound (e.g., 10000)

---

## Summary / Priority Order

| # | Issue | Severity | Effort |
|---|-------|----------|--------|
| 1 | Auth gap in `GetCommitChanges` | High | Small |
| 2 | O(n) parent commit lookup | High | Medium |
| 11 | Add `GetCommitByHash` to storage | High | Medium |
| 3 | No streaming for large files | Medium | Large |
| 4 | ETag / conditional requests | Medium | Small |
| 5 | Hash in `DirectoryEntry` | Medium | Small |
| 6 | ~~Opt-in patch generation~~ | ~~Medium~~ | ~~Small~~ **DONE** |
| 7 | Deduplicate `ListEntries` | Medium | Medium |
| 8 | Unify `GetFile` code paths | Medium | Medium |
| 9 | Path cache invalidation | Low | Small |
| 10 | Log snapshot fallback | Low | Trivial |
| 12 | Default limit for commit listing | Low | Trivial |
