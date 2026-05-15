# Workflow Tests

`workflow_test` is the end-to-end CLI harness for Gitslice.

The suite starts real in-process gRPC and gateway servers in [integration_test.go](/home/nic/workspace/gitslice/workflow_test/integration_test.go), builds the real `gs` binary, and exercises product behavior through CLI commands against those services.

## How It Runs

- The package only runs when `RUN_INTEGRATION_TESTS=1` is set.
- Storage defaults to in-memory.
- If `TEST_POSTGRES_DSN` is set, the same suite runs against Postgres-backed storage.
- Each test gets its own isolated CLI environment:
  - unique `HOME`
  - unique legacy username for commands that still require one
  - separate local checkout/cache state
- Dirty tracker is disabled by default for determinism. Tests that need it opt in with `GS_DISABLE_DIRTY_TRACKER=0`.

## Common Commands

Run the full workflow suite:

```bash
RUN_INTEGRATION_TESTS=1 go test -count=1 ./workflow_test
```

Run one workflow test:

```bash
RUN_INTEGRATION_TESTS=1 go test -count=1 ./workflow_test -run TestSliceWorkflowCommands -v
```

Run against Postgres:

```bash
RUN_INTEGRATION_TESTS=1 TEST_POSTGRES_DSN=postgres://... go test -count=1 ./workflow_test
```

GitHub Actions runs the package in the main `Build and Test` job, and a dedicated `workflow-postgres` job runs the stable core CLI workflow subset against Postgres-backed storage.

Run the opt-in workflow perf regression checks:

```bash
RUN_INTEGRATION_TESTS=1 RUN_WORKFLOW_PERF_TESTS=1 go test -count=1 ./workflow_test -run TestPerf -v
```

Run the repo-wide Go suite including workflow tests:

```bash
RUN_INTEGRATION_TESTS=1 make test
```

## What To Update

Keep this package current whenever CLI or service behavior changes in areas like:

- slice checkout, sync, status, diff, restore, publish
- changeset create, review, merge, list, show
- cache and checkout registry behavior
- dirty tracker behavior
- one-shot repo import flows
- auth/session behavior that affects CLI workflows

## Test Helpers

The most commonly used helpers are:

- `runCLIOrFail`: run the compiled `gs` binary in a test-scoped environment
- `runCLIJSONOrFail`: same, but with `--json` appended and decoded
- `runCLIWithEnvOrFail`: run the CLI with extra env overrides
- `runCLIJSONWithEnvOrFail`: JSON helper with extra env overrides
- `workflowFailureDiagnostics`: captures checkout and home-cache snapshots on failure

## Notes

- These tests are not unit tests. They intentionally cover the CLI, service layer, storage, and local filesystem together.
- Prefer JSON assertions over scraping human-formatted CLI output when adding new workflow coverage.
- If you change dirty tracker behavior, add at least one opt-in tracker test instead of relying only on the default deterministic path.
- Perf regression tests are opt-in and use broad budgets by default. Override them with `WORKFLOW_PERF_*_MAX_MS` env vars when profiling slower or faster machines.
