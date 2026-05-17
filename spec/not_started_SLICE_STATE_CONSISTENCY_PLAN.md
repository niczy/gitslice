# Slice State Consistency Plan

**Status:** Implemented and Verified
**Created:** 2026-05-16
**Last Updated:** 2026-05-17

---

## Executive Summary

Gitslice needs a consistent source-control model for files, commits, search, and
rename operations across overlapping slices.

The current direction is correct in one important way: merge acceptance should be
guarded by authoritative path state, not by lagging root projection. However, the
current model is incomplete for source-control semantics because several read
models can observe different moments in time:

- the file tree can show one state
- the commit list can show another state
- search can be indexed at a third state
- web edits can save without proving they are based on the version the user
  opened
- rename currently behaves like client-side copy plus delete

This plan makes the consistency model explicit.

Core rule:

> Every user-visible file/tree/search/commit view must either be consistent with
> the same state token or clearly report that its projection is not caught up.

Merge validity remains based on immutable base state plus path-head compare and
set. Projections are allowed to lag, but they must expose freshness and must not
decide whether a merge is valid.

## Implementation Summary

The staged rollout has been completed through the planned merge, read-model,
rename, conflict, and auto-merge work. The final verification pass adds an
end-to-end workflow regression test that exercises the consistency contract
through the public gRPC services with shared storage.

Verified coverage now includes:

- file reads returning `PathBase` and `SliceStateToken`
- commit history constrained by the read-time state token
- stale browser-style saves rejected by expected path bases
- non-overlapping same-file edits automatically three-way merged
- overlapping same-file edits persisted as durable changeset conflicts
- first-class file rename facts recorded in merge events
- directory move records persisted for directory renames
- search returning only after the index catches up to the required state token

The Postgres CI job runs the consistency verification test so these guarantees
are checked against the same storage backend used by staging.

---

## Goals

- Prevent stale browser edits from overwriting newer file versions.
- Keep file tree, file content, commit list, and search results consistent for a
  viewed slice state.
- Keep merge acceptance fast by avoiding synchronous root materialization.
- Preserve `home_path_heads` as the current authoritative latest path state.
- Treat commit lists and search indexes as read models with explicit freshness.
- Make rename a first-class operation instead of browser-side copy/delete.
- Support scalable directory rename through move records rather than eager
  subtree rewrites.
- Provide a rollout path that can ship in small PRs.

## Non-Goals

- Do not restore synchronous root promotion as the source of truth.
- Do not make async projections part of merge validity.
- Do not block merge latency on full search indexing.
- Do not attempt full Git compatibility in the first pass.
- Do not require directory rename to rewrite every child path immediately.
- Do not preserve stale web edit behavior for compatibility.

---

## Current State

### Authoritative state

Accepted merges now advance `home_path_heads` with path-version compare and set.
That gives us a strong storage-level guardrail:

- independent paths can merge concurrently
- conflicting same-path writes fail as stale
- the database atomically prevents two writers from advancing the same path head
  from the same base version

This should remain the final write guard.

### Missing source-control semantics

Path-head CAS alone is not a complete source-control model:

- it does not perform three-way text merge
- it does not capture browser read-time bases
- it does not distinguish rename intent from delete plus add
- it does not provide rich conflict artifacts
- it does not guarantee that commit list and search views match the file tree
  being displayed

### Web edit path

The web edit path creates and merges a changeset at save time. If the browser is
viewing the latest state without a pinned `sliceHash`, the changeset snapshot
captures base path versions at save time, not when the user opened the file.

That means:

1. User opens `foo.py`.
2. Another actor edits and merges `foo.py`.
3. User saves the stale tab.
4. The save can capture the new path version as its base and overwrite the newer
   content.

The merge race itself is protected, but stale editor state is not.

### Rename path

Web rename currently behaves as:

1. read old file content from the browser API
2. write same content to a new path
3. delete the old path
4. merge the result as normal file content changes

This is expensive for directories and loses rename intent.

### Commit list and search

Commit list and search are read models. They must not decide whether a merge is
valid, but the UI cannot silently mix a tree from one state with commits or
search results from another.

---

## Consistency Invariants

### Merge authority

Merge acceptance is valid only when:

1. The changeset has an immutable base.
2. The current authoritative path state still matches that base for touched
   paths.
3. The final atomic path-head CAS succeeds.

Root views, materialized entries, history indexes, and search indexes are not
merge authority.

### Read consistency

Every tree/file response must identify the state it represents.

Every dependent query must either:

- use the same state token, or
- report that it is stale or not ready for that state.

Dependent queries include:

- commit list
- file history
- directory history
- search
- local change export previews
- changeset status summaries

### No silent mixed state

If the file tree is displaying state `T`, then:

- commit list must be from state `<= T` and include all commits needed for `T`
- search results must be indexed through `T`
- file reads launched from the tree must use the tree's state token
- edit saves must prove they are based on the read-time file base

If any projection cannot satisfy `T`, the API must return a freshness status
rather than stale data.

---

## State Token Model

Add a shared state token concept to the API.

```proto
message SliceStateToken {
  string slice_id = 1;
  string slice_hash = 2;
  repeated StateCursor cursors = 3;
}

message StateCursor {
  string home_id = 1;
  int32 merge_shard = 2;
  int64 merge_seq = 3;
}
```

For normal home/custom-slice views, this is usually one cursor. For root or
cross-home views, it may become a vector of cursors.

The token should be opaque to clients in the long term, but using explicit fields
initially keeps debugging straightforward.

### File base metadata

Add per-file base metadata to file and entry responses:

```proto
message PathBase {
  string path = 1;
  bool exists = 2;
  string content_hash = 3;
  int64 path_version = 4;
  string source_slice_id = 5;
  string source_commit_hash = 6;
  int64 move_generation = 7;
}
```

`content_hash` identifies bytes. `path_version` identifies path-head state.
`source_commit_hash` connects the visible file to commit history. `move_generation`
will matter once directory move records exist.

### API additions

Add state token and base metadata to:

- `ListEntriesResponse`
- `GetFileResponse`
- file history responses
- directory history responses
- slice commit list responses
- search responses

Add expected base metadata to write requests:

- direct file edit changesets
- create/delete/rename tree operations
- agent changeset export
- local checkout sync/export

---

## Web Edit Concurrency

The web editor should use read-time bases.

### Load flow

`GetFile` returns:

- file content
- `PathBase`
- `SliceStateToken`

The browser stores this with the draft.

### Save flow

The browser sends:

- new content
- expected `PathBase`
- optional expected `SliceStateToken`

The server creates the changeset snapshot using the expected base, not the
current path head at save time.

### Server behavior

If current path head differs from expected base:

- return `MERGE_STATUS_STALE_BASE`
- include the current base metadata
- do not update file contents

Later, the server can offer a rebase/three-way merge path. The first pass should
block stale overwrites.

---

## Merge And Conflict Model

### Existing path-head CAS remains

Path-head CAS should remain the final atomic write guard:

```text
update path head
where path_version = expected_base_version
```

If the update does not apply, the merge is stale.

### Add semantic merge before CAS

Long term, merge should compare:

1. base snapshot
2. current target state
3. proposed changeset snapshot

This enables:

- fast-forward when target did not move
- automatic same-file merge for non-overlapping text edits
- explicit conflict for overlapping edits
- rename-vs-edit and delete-vs-edit detection

### Rich conflict records

Extend conflict payloads beyond `file_id` and message:

```proto
message MergeConflict {
  string path = 1;
  string old_path = 2;
  ConflictType type = 3;
  PathBase base = 4;
  PathBase ours = 5;
  PathBase theirs = 6;
  string base_patch = 7;
  string ours_patch = 8;
  string theirs_patch = 9;
  string message = 10;
}
```

Conflicts should be durable changeset artifacts so users can resolve them later.

---

## Rename And Move Model

### File rename

File rename should be a first-class server operation.

Request:

```proto
message RenamePathChange {
  string old_path = 1;
  string new_path = 2;
  bool directory = 3;
  PathBase expected_old_base = 4;
  PathBase expected_new_base = 5;
}
```

Server behavior:

1. Validate old path still matches expected base.
2. Validate new path is absent or matches explicit replace base.
3. Reuse the existing manifest hash.
4. Tombstone old path.
5. Create new path head.
6. Record rename metadata in the changeset snapshot and merge event.

This is cheap and preserves rename intent.

### Directory rename

Directory rename must not download and re-upload every child from the browser.
It should use a move record.

```text
directory_moves (
  home_id,
  move_id,
  old_prefix,
  new_prefix,
  base_subtree_version,
  new_subtree_version,
  source_slice_id,
  source_commit_hash,
  merge_seq,
  created_at
)
```

Read/list path resolution applies active move records:

```text
visible path = apply_moves(stored path)
```

Directory listings can be accelerated by projected indexes, but the move record
is the source of truth.

### Directory conflict detection

Directory rename checks:

- old prefix still has expected subtree version
- new prefix is still absent or matches an explicit replace base
- no conflicting move already owns the same target prefix

If a child was edited after the old prefix base, the subtree version changes and
the rename must rebase or conflict.

### Compaction

Background compaction may flatten old move records into path heads for speed.
Compaction is optional and must be idempotent.

Compaction must not decide merge validity.

---

## Commit List Consistency

Commit lists must be consistent with the viewed slice state.

### Authoritative commit visibility index

The merge transaction should synchronously write a small commit visibility index.
This is not root promotion. It is metadata, not file materialization.

`content_commit_dirs` is the right direction and should become the authoritative
minimal index for:

- commit hash
- home ID
- directory scopes affected by the commit
- source slice
- parent hash
- merge seq
- message/author/time

### Query contract

Commit list APIs should accept a `SliceStateToken`.

```text
ListSliceCommits(slice_id, path, state_token)
```

The response must include all commits visible up to the requested token and must
not include commits after that token unless explicitly requested.

### Rename and move history

Rename/move merge events should index both sides:

- old path/prefix for historical lookup
- new path/prefix for current lookup

The UI should render rename as rename, not delete plus add.

---

## Search Index Consistency

Search remains async, but must be freshness-aware.

### Search documents

Each indexed document should include:

- home ID
- visible path
- stored path
- content hash
- source slice ID
- source commit hash
- merge shard
- merge seq
- deleted flag
- move generation

### Query contract

Search APIs should accept a required state token:

```text
Search(slice_id, query, required_state_token)
```

If the search index is caught up to the token, return results with the indexed
token.

If not caught up:

- wait briefly if requested
- return `SEARCH_INDEX_NOT_READY`
- include current indexed token
- optionally offer slow authoritative fallback for small scopes

### No silent stale search

The UI must not show stale search results next to a newer file tree unless it
clearly labels them as stale or asks the user to retry.

---

## Projection Model

Projections are read models:

- root/global tree cache
- directory child indexes
- file history
- commit history enrichments
- search index
- diff stats

They may lag accepted merges, but every projection must expose freshness:

```text
projection_name
home_id
merge_shard
applied_merge_seq
updated_at
```

Projection workers must be monotonic. A worker processing an older event must not
overwrite a newer projected state.

---

## Rollout Plan

### PR 1: State token types and plumbing - Done

- Add proto messages for `SliceStateToken`, `StateCursor`, and `PathBase`.
- Return state token from `ListEntries` and `GetFile`.
- Include path base metadata for file entries and file content.
- Keep existing clients working where possible.

### PR 2: Stale web edit protection - Done

- Store read-time `PathBase` in browser draft state.
- Send expected base with direct file edit save.
- Server creates changeset snapshots from expected bases.
- Return stale-base if the path changed since the file was opened.
- Add e2e test for stale tab overwrite prevention.

### PR 3: Commit list state-token contract - Done

- Make slice commit list API accept state token.
- Ensure `content_commit_dirs` is written synchronously for accepted merges.
- Query commits up to requested merge seq.
- Add tests proving tree state and commit list match.

### PR 4: Search freshness contract - Done

- Add search state-token request/response fields.
- Track search projection offsets by home/shard.
- Return not-ready when index is behind required token.
- Add small-scope slow fallback only if straightforward.

### PR 5: First-class file rename - Done

- Add rename operation to changeset API.
- Stop browser copy/delete for file rename.
- Record rename metadata in snapshot and merge event.
- Add file rename conflict tests.

### PR 6: Directory move records - Done

- Add `directory_moves` storage model.
- Add path resolver that applies active move records.
- Switch directory rename to create move records.
- Add subtree version/digest checks.

### PR 7: Rename-aware commit/search projections - Done

- Teach commit visibility index about old and new paths.
- Teach search index to update visible paths for moves without reindexing
  unchanged content.
- Add UI rendering for rename/move history.

### PR 8: Rich conflict artifacts - Done

- Add durable merge conflict records.
- Include base/ours/theirs metadata and patches.
- Add conflict detail UI and resolution APIs.

### PR 9: Three-way content merge - Done

- For text files, attempt automatic three-way merge before returning conflict.
- Keep binary files and unsupported encodings as explicit conflicts.
- Add tests for non-overlapping same-file edits.

### Verification PR - Done

- Add `TestSliceStateConsistencyVerification` under `workflow_test`.
- Run it locally with in-memory storage and in CI against Postgres.
- Verify the combined state-token, stale-base, auto-merge, conflict artifact,
  rename, directory move, and search freshness behavior in one workflow.

---

## Testing Strategy

### Unit tests

- path base extraction from `home_path_heads`
- state token generation for home/custom/root reads
- stale expected base rejection
- path-head CAS conflict under concurrent updates
- file rename base validation
- directory move path resolution
- projection freshness comparisons

### Integration tests

- stale browser edit is rejected after another merge
- file tree and commit list agree for the same token
- search returns not-ready when projection lags required token
- search returns results after projection catches up
- file rename appears as rename in history
- directory rename does not download/re-upload all children
- rename-vs-edit returns conflict or stale base

### Race tests

- two edits to same path from different slices
- edit while directory move is merging
- target path created while rename is merging
- projection worker processes old event after newer event

---

## Migration And Backfill

Because compatibility is not a hard requirement for staging, rollout can reset
staging data when needed. Production migration should still be structured:

1. Backfill `home_path_heads` from current materialized state.
2. Backfill `content_commit_dirs` from merge events and commit snapshots.
3. Initialize projection offsets.
4. Build search indexes from current path heads.
5. Enable state-token enforcement for new writes.
6. Add stricter stale-base rejection after clients are updated.

---

## Open Questions

- Should state tokens be explicit fields forever, or encoded as opaque strings
  after the model stabilizes?
- How much should search wait for projection catch-up before returning
  not-ready?
- Should small-scope authoritative search fallback be part of v1 or deferred?
- What is the right subtree version source for directory moves: path-head child
  digest, merge seq range, or separate subtree counter?
- Should directory move compaction be automatic, manual, or based on query cost?
- How should agent sessions surface stale-base and merge-conflict resolution in
  conversation UI?

---

## Success Criteria

- A stale browser tab cannot overwrite a newer file version.
- A slice page can load tree, file content, commit list, and search results for
  one coherent state token.
- Search never silently returns stale results for a newer tree state.
- File rename is represented as rename in history and diffs.
- Directory rename is near constant-time at merge acceptance.
- Merge validity is independent of root/search/history projection lag.
