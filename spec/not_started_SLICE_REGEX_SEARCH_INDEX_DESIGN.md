# Slice Regex Search Index Design

## Implementation Status

- Current status: `not started`
- Last updated: `2026-03-24`

---

## Goal

Make regex search fast and agent-friendly across both:

- local no-git slice checkouts
- remote filesystem workspaces and browser views

The design uses a Cursor-style sparse n-gram index as a candidate filter, then verifies matches with the real regex engine.

For local slice workflows, the base index should be downloaded during checkout and maintained incrementally on top of local edits.

For remote filesystem workflows, the server should maintain the canonical index for the current workspace state.

---

## Problem

Current search behavior is much weaker than what agents need:

- `gs fs search` is server-side substring search, not indexed regex search.
- `FilesystemService.Search` currently collects every file, reads full contents, and runs `strings.Index` line scans in `services/filesystem/server.go`.
- There is no first-class `gs slice search` for local no-git checkouts.
- Search work is repeated even when the same file bytes already exist elsewhere in slices, workspaces, or cached checkouts.

That is acceptable for small trees, but it does not scale to:

- large home slices
- large custom slices
- remote agent workspaces
- repeated search-heavy agent loops in the same repo

We already have the right structural ingredients:

- content-addressed manifests and blocks
- commit-addressed slice snapshots
- local checkout metadata under `.gs/`
- mutation-driven server APIs for remote workspaces

So the missing piece is a dedicated regex-search index model.

---

## Why This Fits Gitslice

Gitslice is not a plain filesystem. It already has:

- immutable snapshot state for slices
- mutable overlay state for local edits and remote workspaces
- content-addressed storage
- cached local checkout metadata

That means we can split search into:

1. immutable base indexes for known slice commits and workspace snapshots
2. mutable incremental overlays for changed files

This is cleaner than trying to make every search behave like a fresh `grep`.

---

## Design Principles

1. **Exact regex verification remains the source of truth**
   - The index only narrows the candidate file set.
   - Final matching still runs through the normal regex engine.

2. **Index by file bytes, not by path**
   - Paths are mutable.
   - File bytes are reusable across slices, commits, and workspaces.

3. **Use one logical search algorithm in both environments**
   - local checkout search
   - remote workspace search

4. **Keep immutable base state separate from mutable overlays**
   - base snapshot index
   - changed-path overlay

5. **Optimize for agent workflows**
   - predictable latency
   - local-first when working in a checkout
   - no mandatory upload of local edits just to search them

6. **Fall back safely**
   - if the index is missing, stale, or incompatible, search still works through a slower scan path

---

## Non-Goals

- Do not replace all textual search with semantic search.
- Do not index binary files in v1.
- Do not search symlink targets in v1 unless explicitly added later.
- Do not try to make the index itself the authoritative file store.
- Do not require all search to round-trip to the server.
- Do not attempt a full text-engine replacement for `ripgrep` in arbitrary non-slice directories.

---

## User-Facing Outcomes

### Local slice search

Add:

```bash
gs slice search <pattern> [--regex] [--glob <pattern>] [--json]
```

Expected behavior:

- runs locally inside a checked-out slice
- uses a downloaded base index for the checkout commit
- applies an incremental overlay for local edits
- returns results without needing a remote round-trip for unchanged base content

### Remote filesystem search

Extend:

```bash
gs fs search <pattern> [--regex] [--glob </absolute/pattern>] [--json]
```

Expected behavior:

- uses the indexed remote workspace state
- supports substring mode and regex mode
- avoids full-tree content scans for most regexes

### Browser / remote agent search

The same indexed remote search path should eventually power:

- browser file search
- remote agent context gathering
- repo-bound workspace search

---

## Current State

### Filesystem search

`FilesystemService.Search` in `services/filesystem/server.go`:

- resolves the workspace
- enumerates all entries
- reads every file body
- finds matches using `strings.Index`

This is simple but fundamentally O(total searchable bytes).

### Local slices

Local slice workflows already maintain:

- `.gs/index` checkout metadata in `gs_cli/checkout_index.go`
- dirty tracker state in `gs_cli/dirty_tracker.go`

That gives us the right hook points for incremental local search indexing.

### Content hashes

Current manifest hashes in `internal/storage/manifest_content.go` may include file metadata such as:

- executable bit
- symlink target

That is correct for versioned file identity, but it is not the right key for search dedupe.

Search should key off raw file bytes for regular files.

---

## Core Design

## Search Identity

Introduce a search-specific content hash:

- `search_content_hash = sha256(file_bytes)`

Rules:

- regular files: bytes only
- symlinks: excluded from v1 search indexing
- binary files: excluded from v1 search indexing

Why this is separate from the manifest hash:

- search should dedupe identical text content even when executable bits differ
- search should not rebuild per-file search blobs when only metadata changes

---

## Search Algorithm

Use a sparse n-gram inverted index inspired by the Cursor design:

1. assign deterministic weights to adjacent character pairs
2. prefer a rarity-based weight function over random hashing
3. emit sparse covering n-grams for each text file
4. for a regex query, parse and extract required literal structure
5. derive a sparse covering set of query n-grams
6. intersect posting lists to get candidate files
7. verify candidates with the real regex engine

Why not plain trigrams:

- too many posting lookups
- too many broad candidate sets

Why not suffix arrays:

- harder to update incrementally
- less aligned with file-level content dedupe and overlays

---

## Search Index Layers

### 1. File-level search blob

Immutable, keyed by `search_content_hash`.

Contains enough data to query and verify that file efficiently:

- file text metadata
- sparse n-grams for candidate filtering
- compact line offset table for snippet extraction
- text/binary classification
- index format version

These blobs should be reusable across:

- slices
- commits
- workspaces
- local checkouts

### 2. Snapshot / commit artifact

Immutable, keyed by:

- slice ID
- commit hash
- index version

Contains the full searchable view of a slice commit:

- `path -> search_content_hash`
- optional `search_content_hash -> path IDs`
- postings structure over the files visible in that commit

This is the artifact downloaded during `gs slice checkout`.

### 3. Mutable overlay

Environment-specific mutable state:

- local checkout overlay under `.gs/search/`
- remote workspace overlay in server storage

Tracks only changed paths since the immutable base snapshot.

---

## Local Slice Search Model

## Checkout behavior

When `gs slice checkout` runs:

1. materialize the files as today
2. download the base search artifact for the checked-out commit if available
3. store it under `.gs/search/base/`
4. initialize an empty mutable overlay under `.gs/search/overlay/`
5. keep using the dirty tracker to identify candidate changed paths

If the server does not have an artifact:

- checkout still succeeds
- local search falls back to building the base index locally
- that build should be cached locally for the commit

### Why download instead of always rebuilding

Downloading is better for checked-out slices because:

- the server already knows the exact commit
- the immutable base content is shared
- rebuilding large trees on every checkout wastes local CPU and disk IO
- agents benefit from fast first-search latency after checkout

### Local search query flow

When `gs slice search` runs:

1. load the base artifact for the checkout commit
2. reconcile dirty tracker candidate paths into the overlay
3. decompose the regex into sparse n-grams
4. query base + overlay indexes
5. map candidate hashes back to paths
6. run exact regex verification only on candidate files
7. return matches

---

## Remote Workspace Search Model

Remote filesystem workspaces do not have a local checkout.

So the server maintains:

- a base snapshot search state for the current committed workspace state
- a mutable overlay for remote writes not yet compacted into a new base

### Remote mutation flow

On:

- `WriteFile`
- `EditFile`
- `EditFiles`
- `DeleteFile`
- `MoveFile`
- `CopyFile`
- `Batch`
- repo pull/import flows

the server should update search state incrementally:

1. identify touched paths
2. compute `search_content_hash` for changed text files
3. reuse an existing file-level search blob if present
4. otherwise build and store the blob once
5. update workspace path mappings
6. update or invalidate overlay postings

### Remote search query flow

`FilesystemService.Search` in regex mode should:

1. load the workspace search state
2. decompose the query into sparse n-grams
3. use the index to get candidate paths
4. verify the regex on those paths only
5. return exact match lines and ranges

Fallback behavior:

- if the workspace index is missing or invalid, perform the current scan path

---

## Incremental Indexing

Incremental indexing should happen at the file level.

Do not rebuild a whole corpus when one file changes.

### File-level update steps

For each changed text file:

1. read current bytes
2. compute `search_content_hash`
3. if the blob already exists, reuse it
4. otherwise build sparse n-grams and line offsets once
5. update `path -> search_content_hash`
6. update overlay postings or path membership

For deletes:

1. remove the path mapping
2. decrement blob references or leave GC to background cleanup

For moves:

1. move path mapping
2. keep the same content hash when bytes are unchanged

### Local incremental sources

Use:

- dirty tracker candidates
- checkout/sync/restore touched-path knowledge

### Remote incremental sources

Use:

- mutation RPC inputs
- repo binding pull/push/import touched-path sets

---

## Artifact and Storage Layout

## Shared server-side objects

### Search blob object

Keyed by:

- `search_content_hash`
- search index version

Contains:

- sparse n-grams
- line offsets
- text metadata

### Slice commit artifact

Keyed by:

- slice ID
- commit hash
- search index version

Contains:

- searchable file table for that commit
- compact postings for the visible file set
- optional embedded references to file-level blobs

### Workspace search state

Stored as:

- base snapshot artifact reference
- mutable overlay path table
- overlay postings and changed-file blob references

## Local checkout files

Under `.gs/search/`:

- `base.*` immutable downloaded artifact for the current checkout commit
- `overlay.*` mutable local changed-path index state
- metadata file with artifact version and commit hash

The exact file layout can remain implementation-defined as long as it supports:

- memory mapping or efficient random access
- versioned upgrades
- atomic replace on write

---

## API Changes

## CLI

### New local search command

Add:

```bash
gs slice search <pattern> [--regex] [--glob <pattern>] [--json]
```

### Extend remote search

Extend:

```bash
gs fs search <pattern> [--regex] [--glob </absolute/pattern>] [--json]
```

## gRPC / proto

### Slice artifact fetch

Add a gRPC-first path for search artifact download.

Likely on `SliceService`:

- `GetSliceSearchArtifact`
- or a streamed `StreamSliceSearchArtifact`

This should be commit-aware and version-aware.

### Filesystem search

Extend `proto/filesystem/filesystem_service.proto`:

- add regex mode
- add result limits and maybe case sensitivity later

Example fields:

- `bool regex`
- `int32 max_matches`
- `bool case_sensitive`

Keep grpc-gateway bindings.

---

## Query Semantics

### What counts as searchable in v1

- UTF-8 text regular files
- maybe ASCII-compatible text files even if not strict UTF-8 later

Not searchable in v1:

- binary files
- symlink targets
- directories

### Exactness

The index is not allowed to return false negatives.

It may return false positives at the candidate stage, but final regex verification must preserve correctness.

### Globs

Path globs should be applied before regex verification and ideally during candidate narrowing where possible.

---

## Build and Refresh Strategy

## Slice commits

Build commit artifacts:

- eagerly on merge / publish / import
- or lazily on first checkout/search if missing

Preferred behavior:

- lazy first, then cache

This avoids doing unnecessary work for commits no one searches.

## Local checkout

During checkout:

- try download first
- fallback to local build if missing

During local edits:

- update overlay incrementally

During sync:

- replace base artifact if commit changed
- rebuild or clear overlay for touched files

During restore:

- update overlay for restored paths
- drop overlay entries that now match base

## Remote workspaces

Update incrementally on mutation APIs.

Compact overlays opportunistically:

- after snapshots
- after large batch edits
- during idle maintenance

---

## Performance Expectations

Primary win:

- avoid scanning all file contents for most regex queries

Expected cost model:

- index build cost paid once per unique file content
- snapshot artifact cost paid once per searched commit
- local edits update only touched files
- regex verification only runs on candidate files

The design should improve:

- repeated search in the same checkout
- repeated search across similar slices
- large workspace regex search
- agent context gathering loops

---

## Failure and Fallback Behavior

If any of the following happens:

- artifact missing
- version mismatch
- corrupt local index
- server index unavailable
- regex decomposition yields no useful n-grams

then search should fall back to:

- local full scan for `gs slice search`
- server full scan for `gs fs search`

Correctness matters more than fast-path availability.

---

## Security and Privacy

### Local slice indexes

Remain on the user machine under the checkout.

### Server-side workspace indexes

Remain scoped to the authenticated user or authorized workspace.

### Shared dedupe objects

If file-level search blobs are shared by raw content hash, the storage layer must not expose cross-tenant existence information directly through user APIs.

That means:

- reuse internally
- do not expose blob presence as user-visible state

---

## Testing Strategy

### Unit tests

- regex decomposition to sparse n-grams
- file-level blob build and query
- exact-regex verification fallback
- binary/text classification
- overlay update correctness

### Integration tests

- `gs slice search` on clean checkout
- `gs slice search` after local edits
- `gs slice search` after restore and sync
- `gs fs search --regex` against remote workspace writes
- artifact download fallback to local build

### Benchmarks

- local search on large checked-out slice
- first search after checkout with downloaded artifact
- first search after checkout with local rebuild fallback
- remote workspace search on large repo import
- repeated query latency on warm index

---

## Open Questions

1. Should v1 search only current checkout/workspace state, or also support historical commit search?
   - Recommendation: current state only in v1.

2. Should line snippets come from stored line offsets or on-demand rescans?
   - Recommendation: store compact line offsets in file-level blobs.

3. Should the slice artifact embed everything in one file or reference per-file blobs?
   - Recommendation: implementation may choose either, but keep the logical split between immutable shared file blobs and commit/workspace path mappings.

4. Should symlink targets be searchable?
   - Recommendation: no in v1.

5. Should we surface search through `file` service too?
   - Recommendation: no, keep local slice search CLI-local and remote search in `FilesystemService`.

---

## Recommended Rollout

1. Build the shared indexing library and file-level search blob format.
2. Add server-side commit artifact generation and fetch APIs.
3. Add local `gs slice search` using downloaded base artifacts plus local overlays.
4. Extend remote `gs fs search` to regex mode with indexed candidate pruning.
5. Add browser and remote-agent search UX on top of the indexed remote path.

---

## PR-by-PR Execution Plan

## PR 1: Search Blob Foundations

### Scope

- Add `internal/searchindex` package.
- Define:
  - `search_content_hash`
  - text/binary classification
  - sparse n-gram generation
  - regex query decomposition
  - file-level search blob format

### Deliverables

- file-level blob builder
- file-level candidate query support
- unit tests and microbenchmarks

### Validation

- package unit tests
- deterministic blob golden tests
- benchmark output checked into test expectations only if stable enough

---

## PR 2: Slice Commit Artifact Format and Storage

### Scope

- Add storage support for slice search artifacts keyed by commit hash and index version.
- Add artifact build code from a slice commit snapshot.
- Reuse file-level search blobs by `search_content_hash`.

### Deliverables

- artifact builder
- object storage or storage-layer persistence
- commit artifact metadata model

### Validation

- unit tests for artifact construction from seeded slice snapshots
- storage tests for load/store/version behavior

---

## PR 3: Slice Search Artifact Download API

### Scope

- Extend `proto/slice/slice_service.proto` with artifact fetch RPCs.
- Expose artifact download through grpc-gateway.
- Implement server-side fetch path with version checks.

### Deliverables

- gRPC API for artifact retrieval
- CLI plumbing for artifact fetch
- server tests for commit/version resolution

### Validation

- proto compatibility checks
- service tests
- no generated proto files committed

---

## PR 4: Local Base Artifact Integration on Checkout

### Scope

- Extend `gs slice checkout` and `gs slice sync` to fetch and persist base search artifacts under `.gs/search/`.
- Fallback to local base build when the server artifact is missing.

### Deliverables

- checkout-time artifact persistence
- local metadata for commit/version tracking
- local rebuild fallback path

### Validation

- workflow tests for:
  - artifact download on checkout
  - fallback local build
  - artifact replacement on sync when commit changes

---

## PR 5: Local Overlay and `gs slice search`

### Scope

- Add `gs slice search`.
- Build mutable local overlay files under `.gs/search/overlay/`.
- Reconcile overlay from dirty tracker candidates, restore, sync, and checkout operations.

### Deliverables

- local regex search command
- local exact-match verification
- incremental overlay maintenance

### Validation

- workflow tests on:
  - clean checkout
  - modified files
  - added files
  - deleted files
  - restore and sync

---

## PR 6: Server Workspace Index State

### Scope

- Add workspace base+overlay index state on the server.
- Update mutation paths in `FilesystemService` incrementally.
- Reuse shared file-level blobs by `search_content_hash`.

### Deliverables

- workspace search state model
- mutation-driven incremental updates
- background compaction hooks if needed

### Validation

- filesystem service tests for write/edit/delete/move/copy updates
- repo binding import/pull flows update index state correctly

---

## PR 7: Indexed Regex Mode for `gs fs search`

### Scope

- Extend `FilesystemService.Search` with regex mode and indexed candidate pruning.
- Keep substring mode working.
- Fall back to the current full scan if index state is missing.

### Deliverables

- proto updates
- CLI `gs fs search --regex`
- indexed remote search path

### Validation

- remote search correctness tests
- large-workspace benchmark compared to current scan path
- full scan fallback coverage

---

## PR 8: Browser / Remote Agent Search Integration

### Scope

- Expose indexed remote search in the web/browser flow.
- Add structured UI for regex search results.
- Reuse the same remote API path.

### Deliverables

- web search integration
- browser tests
- docs updates

### Validation

- web build
- browser e2e tests
- remote search smoke tests in prod-like environment

---

## PR 9: Backfill, Metrics, and Hardening

### Scope

- background backfill for hot slice commits
- artifact/version repair path
- metrics for:
  - candidate set size
  - exact verification time
  - fallback scan rate
  - artifact download/build latency

### Deliverables

- admin/backfill tooling
- observability
- corruption repair / rebuild commands

### Validation

- operational smoke tests
- benchmark suite additions
- rollout playbook

---

## Recommendation

Start with local slice search first.

Reason:

- lower system complexity
- immediate benefit for agent-driven checkout workflows
- no multi-tenant server invalidation problems in the first step

Then reuse the same core indexing library and file-level blob design for remote workspaces.
