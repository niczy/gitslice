# File Service Exposure to CLI

## Implementation Status

- Current status: `finished`
- Last updated: `2026-02-24`
- Completion PR: `#205`

---

## Goal

Expose read-only `FileService` operations directly in `gs_cli` so users can inspect files, trees, and file history from CLI workflows without relying on web-only routes.

## Non-Goals

- No new write/edit RPCs in `FileService`.
- No bypass of `changeset` + `merge` workflow.
- No replacement of current conflict detection/resolution model.

---

## Delivered CLI Surface

Implemented command group:

```bash
gs file ls [path] [--slice <slice-id>] [--commit <hash>] [--limit <n>]
gs file cat <path> [--slice <slice-id>] [--commit <hash>] [--raw]
gs file history <path> [--slice <slice-id>] [--limit <n>] [--from-commit <hash>]
gs file dir-history [path] [--slice <slice-id>] [--limit <n>] [--from-commit <hash>] [--type add,modify,delete,rename]
gs file commit-changes <commit-hash> [--patches]
```

Implemented in:

- `gs_cli/commands_file.go`
- wired from `gs_cli/main.go`
- documented in CLI help at `gs_cli/help.go`

---

## Version and Slice Resolution Behavior

### Slice selection precedence

Implemented precedence for file commands:

1. `--slice` (explicit flag)
2. `.gs/config` slice ID (if present)
3. empty slice ID (server-side fallback behavior, including root-slice compatible reads)

Implemented in `resolveFileSliceID(...)`.

### Version selector behavior

Implemented behavior:

- If `--slice` is set, request uses `slice_version` and optional `slice_hash` from `--commit`.
- If only `--commit` is set, request uses global `commit_hash` selector.
- If neither is set, request is unversioned and resolved server-side.

Implemented in:

- `applyListEntriesVersion(...)`
- `applyGetFileVersion(...)`

---

## Architecture Changes (Delivered)

### CLI client wiring

`CLI` now includes FileService connectivity:

- `fileConn *grpc.ClientConn`
- `fileClient filev1.FileServiceClient`

Address strategy delivered:

- `--addr` overrides all service endpoints (`slice/admin/file`)
- `--slice-addr`, `--admin-addr`, `--file-addr` remain available
- default `--file-addr` is `localhost:50051`

### Command routing

Delivered in `gs_cli/main.go`:

- `case "file": handleFileCommand(ctx, cli, args[1:])`

Auth behavior remains unchanged:

- file RPCs run through existing `withUserAuth(...)` context metadata path

---

## Conflict Model Compatibility

The CLI exposure keeps conflict semantics unchanged:

- `gs file *` commands are read-only and do not modify ownership/index/head state.
- Conflict detection still occurs during merge (`SliceService.MergeChangeset`).
- Conflict resolution remains explicit via admin/conflict APIs.

Practical effect:

- File reads continue to work during conflict states.
- Merge remains blocked until conflict resolution is completed.

---

## UX and Error Handling (Current)

Current output behavior:

- Human-readable output for all commands.
- `file cat` detects non-UTF8 content and suggests `--raw`.
- `file commit-changes --patches` prints inline patch text.

Current limitations (accepted for this phase):

- No dedicated `--json` mode yet.
- gRPC errors are surfaced directly via CLI fatal logging (not yet remapped into polished user-facing categories).
- No explicit `ResourceExhausted` guidance text yet.

---

## Test Coverage Status

### Implemented tests

- `gs_cli/commands_file_test.go`:
1. `parseChangeTypesCSV` parsing and invalid value handling
2. version selector request-shape helpers
3. slice resolution precedence (`--slice`, config, fallback)

### Existing integration coverage

- `workflow_test/integration_test.go` exercises FileService file/history behavior in end-to-end server/storage flows.

### Gap kept for follow-up

- No dedicated CLI process-level integration tests for all `gs file *` subcommands yet.

---

## Rollout Result

Completed rollout (single implementation PR):

1. CLI plumbing: DONE
2. Read tree/content commands (`ls`, `cat`): DONE
3. History commands (`history`, `dir-history`, `commit-changes`): DONE
4. Conflict model preservation: DONE (no mutation path added)

---

## Acceptance Criteria

- CLI can invoke `FileService` directly over gRPC: DONE
- No new mutation path outside changeset/merge/admin APIs: DONE
- Existing merge-time conflict behavior unchanged: DONE
- File command test coverage added: DONE (unit coverage); CLI integration expansion pending follow-up

---

## Follow-up Backlog (Non-Blocking)

1. Add `--json` output mode for `ls/history/dir-history/commit-changes`.
2. Improve grpc-status-to-user-message mapping for common errors.
3. Add CLI-level integration tests that execute `gs file *` end-to-end.
4. Refresh README CLI usage section with `gs file` examples.
