# Agent Instructions

These instructions apply to `services/filesystem`.

- This package implements the mutable workspace filesystem API. It owns workspace create/delete/list/info plus file writes, edits, deletes, moves, copies, mkdir, batch operations, streaming transfer, upload planning/finalization, snapshots, diffs, forks, merges, repo imports, search indexing, and root projection side effects.
- Do not add committed-code browsing or public read-only slice history endpoints here when they can be served by `services/file`.
- Keep the gRPC surface aligned with `proto/filesystem/filesystem_service.proto` and expose HTTP routes through grpc-gateway bindings. Avoid standalone `net/http` handlers for `/v1/*`.
- Use the existing workspace helpers for authorization and path resolution. In home-workspace mode, keep paths constrained to the owning user's home namespace and avoid allowing writes outside tracked workspace/home paths.
- Mutations should flow through the existing commit/snapshot helpers so workspace metadata, search artifacts, async root projection, and conflict/diff behavior remain consistent.
- When changing behavior, update focused tests in `services/filesystem/server_test.go` or the specific companion test file; run `gofmt` for Go edits and `make test` for Go changes.
