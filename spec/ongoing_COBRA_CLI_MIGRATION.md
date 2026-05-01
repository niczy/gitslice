# Cobra CLI Migration Plan

## Status

- Current status: `ongoing`
- Last updated: `2026-05-01`
- Current PR: PR 3, auth commands

## Goal

Move the `gs` CLI from a single `flag`-based dispatcher to a Cobra command tree that is easier to read, test, and extend.

The migration must stay incremental. Each PR should leave the CLI buildable, smoke-tested, and usable for existing workflows.

## Non-Goals

- Do not rewrite command behavior while moving routing code.
- Do not change gRPC APIs as part of the Cobra migration.
- Do not remove legacy command aliases until a later explicit compatibility PR.
- Do not combine broad UX redesign with command-router refactoring.

## Agent Rules

Use this section as the operating contract for future agents.

1. Work on one PR-sized slice at a time.
2. Keep existing handler functions working until their command group is migrated.
3. Add or update tests in the same PR as routing changes.
4. Run CLI smoke checks after each PR-sized change.
5. Record any CLI bugs found during testing in this spec before moving to the next PR.
6. Prefer moving one command group per PR, for example `cache`, then `jobs`, then `auth`.
7. Avoid changing command output unless the PR explicitly documents that compatibility break.
8. Keep generated protobuf files out of commits.

## Target Architecture

The final CLI should use this package shape:

```text
gs/main.go                 thin binary entrypoint
gs_cli/root.go             Cobra root command, global flags, shared command context
gs_cli/commands_*.go       Cobra command group builders and existing command handlers
gs_cli/help.go             transitional help text until command help fully lives in Cobra
```

The root command should own global flags:

```text
--addr
--account-addr
--slice-addr
--admin-addr
--file-addr
--fs-addr
--tls
--non-interactive
--api-key
--user
```

Command group builders should eventually return `*cobra.Command` values, for example:

```go
func newCacheCommand() *cobra.Command
func newJobsCommand() *cobra.Command
func newAuthCommand(deps commandDeps) *cobra.Command
```

## PR Plan

### PR 1: Cobra Root Scaffold

Scope:
- Add Cobra as a dependency.
- Route `gs` through a Cobra root command.
- Preserve the existing legacy dispatcher behind the root command.
- Add this migration spec and bug log process.

Acceptance:
- `go test ./gs_cli` passes.
- `go build -o bin/gs ./gs` passes.
- `./bin/gs --help` prints the existing top-level help.
- `./bin/gs cache stats --json` still runs without needing a server.

### PR 2: Local-Only Commands

Scope:
- Convert `cache`, `jobs`, `__watch-checkout`, and `__run-job` to Cobra commands.
- Keep local-only commands independent from gRPC client initialization.

Acceptance:
- Local command tests pass. DONE in PR 2.
- `gs cache stats --json` works. DONE in PR 2.
- `gs jobs list --json` works. DONE in PR 2.
- Leading global flags still work before local-only commands. DONE in PR 2.

### PR 3: Auth Commands

Scope:
- Convert `auth`, `login`, and `logout` to Cobra.
- Keep current auth resolution order unchanged.
- Add command tests for help, JSON mode, and non-interactive behavior.

Acceptance:
- `gs auth status --json` works. DONE in PR 3.
- `gs logout --json` works. DONE in PR 3.
- `gs login --non-interactive --json` fails without opening an interactive flow when auth is missing. DONE in PR 3.

### PR 4: Read-Heavy Service Commands

Scope:
- Convert `doctor`, `context`, `file`, and read-only `slice` commands.
- Introduce shared Cobra helpers for connection and auth initialization.

Acceptance:
- Existing unit tests pass.
- A server-backed smoke test can run `gs context --json` and `gs doctor --json`.

### PR 5: Mutation Commands

Scope:
- Convert `slice` mutation commands, `changeset`, `repo`, `fs`, `import`, and `conflict`.
- Preserve aliases: `init`, `status`, `log`, `root`.

Acceptance:
- `make test` passes.
- Integration tests pass when relevant with `RUN_INTEGRATION_TESTS=1 make test`.

### PR 6: Help and Compatibility Cleanup

Scope:
- Move command help text into Cobra commands.
- Remove duplicate manual top-level routing.
- Document final command tree in README/spec.

Acceptance:
- `gs help`, `gs --help`, and `gs <command> --help` are consistent.
- Backward-compatible commands still work or have explicit migration notes.

## Test Checklist After Each PR

Run the smallest useful set for the changed scope:

```bash
go test ./gs_cli
go build -o bin/gs ./gs
./bin/gs --help
./bin/gs cache stats --json
```

When changing server-backed command behavior, also run:

```bash
make test
RUN_INTEGRATION_TESTS=1 make test
```

## Bug Log

Use this format for every bug found while testing a PR:

```text
### YYYY-MM-DD: short title

- PR/scope:
- Command:
- Expected:
- Actual:
- Repro:
- Status:
```

### 2026-05-01: `make test` times out in benchmark suite

- PR/scope: PR 1 test gate
- Command: `make test`
- Expected: full repository test target completes or skips load benchmarks that exceed the default Go test timeout.
- Actual: `benchmark_suite.TestSimulate100kUsers` hit the 10-minute Go test timeout during `go test ./...`; `go test ./gs_cli`, `go build -o bin/gs ./gs`, `./bin/gs --help`, and `./bin/gs cache stats --json` passed.
- Repro: run `make test`; the target invokes `go test ./...`, which includes `benchmark_suite` with its default `BENCHMARK_USERS=100000`.
- Status: open; likely test-target configuration issue, not a Cobra CLI regression. Use short-mode benchmark checks or reduce `BENCHMARK_USERS` when this PR only changes CLI routing.
