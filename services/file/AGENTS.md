# Agent Instructions

These instructions apply to `services/file`.

- This package implements the read-only committed-code browsing API for slices. Keep it focused on listing entries, reading files, public file access, file/directory history, and commit change inspection.
- Do not add workspace mutation behavior here. Writes, edits, deletes, mkdir, snapshots, forks, merges, upload flows, repo imports, and other mutable filesystem operations belong in `services/filesystem`.
- Keep the gRPC surface aligned with `proto/file/file_service.proto` and expose HTTP routes through grpc-gateway bindings. Avoid standalone `net/http` handlers for `/v1/*`.
- Preserve versioned reads: callers may read root commits, slice HEAD, or explicit slice versions. Be careful around folder mounts, home slices, and display-path-to-stored-path mapping.
- Preserve access semantics: private slice reads require view access, while public routes must only reveal public slices or public paths and descendants.
- When changing behavior, update focused tests in `services/file/server_test.go`; run `gofmt` for Go edits and `make test` for Go changes.
