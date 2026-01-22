# CLI Design: gitslice (gs)

## Executive Summary

The `gs` CLI is a lightweight client for the slice-based prototype. It talks to the SliceService and AdminService over gRPC, stores the current slice metadata TOML path in `.gs/config`, and provides commands for slice checkout, change lists, and conflict resolution. See [`gs_cli/main.go`](../gs_cli/main.go) for the authoritative command list.

---

## Installation

Build from source:

```bash
go build -o gs_cli ./gs_cli/
```

---

## Configuration & Connection

- `--slice-addr` (default `localhost:50051`) and `--admin-addr` (default `localhost:50052`) control gRPC endpoints.
- `gs init <metadata-toml-path>` creates `.gs/config` in the current directory. The directory must be empty. Metadata files are TOML documents with a required `slice_id` field and must be specified as absolute paths.

---

## Core Commands (Implemented)

### Slice Commands

```bash
gs slice checkout /abs/path/to/slice.toml --commit HEAD
gs slice clone /abs/path/to/slice.toml --commit HEAD
```

> `gs slice clone` is an alias for `gs slice checkout`.

### Changeset Commands

```bash
gs changeset create --message "Add feature" --files foo.go,bar.go
gs changeset review cs-123
gs changeset merge cs-123
gs changeset rebase cs-123
gs changeset list --limit 20 --status pending
```

Notes:
- `changeset create` uses the slice metadata TOML path from `.gs/config` and accepts `--files` or positional file arguments.
- `changeset list` supports status filters: `pending`, `approved`, `rejected`, `merged`.

### Conflict Commands

```bash
gs conflict list --slice /u/alice/slices/payments --detailed --severity
gs conflict show path/to/file.go
gs conflict resolve --theirs other-slice path/to/file.go
gs conflict resolve --ours path/to/file.go
```

### Status & History

```bash
gs status
gs log
gs log /abs/path/to/slice.toml
```

### Root Slice & Forking

```bash
gs root
gs fork /u/alice/slices/new-slice ./folder --parent /u/alice/slices/payments --description "Forked slice"
```

---

## CLI ↔ API Mapping

Refer to [API_DESIGN.md](./API_DESIGN.md) for RPC details and [gs_cli/main.go](../gs_cli/main.go) for argument parsing specifics.
