# File Service Exposure to CLI

## Implementation Status

- Current status: `ongoing`
- Last updated: `2026-02-23`

---

## Goal

Expose read-only `FileService` operations directly in `gs_cli` so users can inspect files, trees, and file history from the CLI without going through web-only endpoints.

## Non-Goals

- No new write/edit RPCs in `FileService`.
- No bypass of `changeset` + `merge` workflow.
- No replacement of current conflict detection/resolution model.

---

## Current State

- `gs_cli` currently connects to `SliceService` and `AdminService` only.
- `FileService` already exists and is gRPC-first with gateway bindings.
- Conflict detection is enforced in `MergeChangeset` by checking file ownership overlap (`file_slice_index` / in-memory `fileIndex`).
- Conflict resolution is explicit ownership selection via `AdminService.ResolveConflict`.

---

## Proposed CLI Surface

Add `file` command group:

```bash
gs file ls [path] [--slice <slice-id>] [--commit <hash>] [--limit <n>]
gs file cat <path> [--slice <slice-id>] [--commit <hash>] [--raw]
gs file history <path> [--slice <slice-id>] [--limit <n>] [--from-commit <hash>]
gs file dir-history [path] [--slice <slice-id>] [--limit <n>] [--type add,modify,delete,rename]
gs file commit-changes <commit-hash> [--patches]
```

Default resolution rules:

1. If `--slice` is passed, use it.
2. Else use `.gs/config` slice if present.
3. Else fall back to root-slice behavior already supported by `FileService`.

Version selector behavior:

- `--commit` and `--slice` can be combined (`slice_version.slice_id + slice_hash`).
- `--commit` alone means global commit path (current proto behavior).

---

## Architecture Changes

### CLI Client Wiring

Extend `CLI` struct to include:

- `fileConn *grpc.ClientConn`
- `fileClient filev1.FileServiceClient`

Connection strategy:

- Keep one core-address default (`--addr`) for all services.
- Keep existing `--slice-addr` / `--admin-addr` compatibility.
- Add optional `--file-addr` only if we actually deploy `FileService` separately later; otherwise default to `--addr` / `--slice-addr`.

### Command Handling

- Add `case "file": handleFileCommand(...)` in `gs_cli/main.go`.
- Implement handlers in a new `gs_cli/commands_file.go`.
- Reuse existing metadata/auth injection (`withUserAuth`).

---

## Conflict Model with Existing Slice Mutation

This is the critical compatibility point.

### Existing Mutation Model (must remain true)

- `CreateChangeset` describes intended file mutations.
- `MergeChangeset`:
1. Locks slice + files.
2. Checks active ownership overlaps for each modified file.
3. Returns `MERGE_STATUS_CONFLICT` if another slice owns any modified file.
4. On success, updates ownership/index and commit metadata.
- `ResolveConflict` keeps one owner slice per conflicted file.

### Impact of Exposing FileService in CLI

`FileService` remains read-only, so it does not change mutation semantics:

- It must not write file ownership index.
- It must not update slice metadata/head.
- It must not auto-resolve conflicts.

So exposing it in CLI is safe if we keep commands read-only.

### Read Semantics During/After Conflicts

- A conflict is an ownership state, not a read lockout.
- `gs file *` commands should continue to read data even when conflicts exist.
- Users resolve conflicts only via:
1. `gs conflict resolve ...`
2. Retry `gs changeset merge ...`

### Why This Matches Current Design

- Ownership conflict checks live at merge time (`SliceService`), not file-read time (`FileService`).
- Read APIs are for inspection, review, and debugging; mutation authority stays in slice/changeset/admin flows.

---

## UX and Error Handling

- If `GetFile` response is too large and server returns `ResourceExhausted`, print actionable guidance:
1. retry with smaller target
2. use `--raw > file`
- Preserve grpc status mapping in output:
1. `PermissionDenied`: not authorized for slice
2. `NotFound`: file/path/commit missing
3. `InvalidArgument`: bad flags or mutually-exclusive args

Output modes:

- Human-readable default.
- `--json` for machine use (phase 2) for `ls/history/commit-changes`.

---

## Test Plan

### Unit

- CLI flag parsing and request-shape tests for each new command.
- Version selector precedence tests (`--slice`, `.gs/config`, root fallback).

### Service Integration (existing stack)

- `gs file ls` and `gs file cat` against seeded root + non-root slices.
- `gs file history` and `gs file commit-changes` after merge.
- Conflict scenario:
1. create conflicting ownership state
2. verify `gs file` reads still work
3. verify merge still blocked until `gs conflict resolve`

---

## Rollout Plan (PR-by-PR)

1. CLI plumbing
- Add `FileServiceClient` wiring and `file` command skeleton.
- Add help text and no-op stubs.

2. Read tree/content commands
- Implement `file ls`, `file cat`.
- Add tests.

3. History commands
- Implement `file history`, `file dir-history`, `file commit-changes`.
- Add tests.

4. Conflict-focused validation
- Add integration tests that combine file reads with conflicting slice ownership + resolution.
- Docs refresh in `spec/finished_CLI_DESIGN.md` once complete.

---

## Acceptance Criteria

- CLI can invoke `FileService` directly over gRPC.
- No new mutation path is introduced outside changeset/merge/admin APIs.
- Existing conflict behavior remains unchanged:
1. conflicts detected during merge
2. resolved only by explicit ownership choice
3. merge succeeds after resolution
- New file commands pass unit + integration tests.
