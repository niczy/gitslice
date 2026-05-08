# Benchmark Suite

Comprehensive load-testing and integrity-verification suite for the gitslice
**file service** and **slice service**.

The suite runs entirely in-process using in-memory storage — no external
infrastructure required.

---

## What it tests

| Test | Description |
|---|---|
| `TestSimulate100kUsers` | Simulates N concurrent users each performing the full `CreateSliceFromFolder → CreateChangeset → MergeChangeset` workflow; reports P50/P95/P99 latencies and throughput; verifies integrity on a 1% random sample |
| `TestIntegrity` | Creates 50 slices with known data, merges all changesets, then verifies slice state, commit history, changeset status, and file history are self-consistent |
| `TestConflictDetection` | Verifies that when two slices claim the same file, exactly one merge succeeds and the other receives `MERGE_STATUS_CONFLICT` |
| `TestConflictResolution` | Verifies that after an admin resolves a conflict, the preferred slice can merge and the other remains blocked |
| `TestConflictDetectionUnderLoad` | Fans out 20 concurrent merges on a single hot file; asserts exactly one success and N-1 conflicts |
| `TestFileServiceReadLoad` | Runs 40 concurrent readers (ListEntries, GetFileHistory, GetDirectoryHistory) over 20 pre-populated slices and verifies every response |
| `TestFileServiceCommitChangesConsistency` | Creates multiple commits on one slice and verifies `GetCommitChanges` returns the correct modified files for each commit |

---

## Running the tests

### Quick smoke-test (short mode)

Runs all tests with a reduced user count (skips the large load test):

```bash
cd /path/to/gitslice
go test -v -short -timeout 120s ./benchmark_suite/
```

### Full 100 000-user load test (default)

```bash
go test -v -timeout 600s ./benchmark_suite/ -run TestSimulate100kUsers
```

Expected output:

```
=== Load Test Results ===
Users simulated:   100000
Workers:           16
Elapsed:           28.43 s (foreground workflow)
Throughput:        3516.8 users/sec

Outcomes:
  Successful merges: 100000
  Conflicts:         0
  Errors:            0

End-to-end latency per user (ms):
  P50:  4.21
  P95:  9.87
  P99:  14.53
  Max:  38.11

...

Promotion drain elapsed: 0.00 s

Integrity OK: all 1000 sampled users passed
```

### All tests

```bash
go test -v -timeout 600s ./benchmark_suite/
```

### Recent benchmark run (2026-02-19)

Command:

```bash
BENCHMARK_USERS=30000 \
go test -v -timeout 600s ./benchmark_suite/ \
  -run 'TestSimulate100kUsers|TestFileServiceReadLoad|TestConflictDetectionUnderLoad'
```

Observed results:

- `TestConflictDetectionUnderLoad`: `success=1`, `conflict=19`, `error=0`
- `TestFileServiceReadLoad`: `16169 RPC/s` (`0` read errors)
- `TestSimulate100kUsers` (`BENCHMARK_USERS=30000`):
  - Elapsed: `175.91s`
  - Throughput: `170.5 users/sec`
  - Successful merges: `30000`
  - Conflicts: `0`
  - Errors: `0`
  - End-to-end latency: `P50 14.95ms`, `P95 80.00ms`, `P99 108.11ms`, `Max 190.49ms`
  - Merge latency: `P50 8.20ms`, `P95 52.79ms`, `P99 81.58ms`

---

## Configuration

Both values can be overridden via environment variables:

| Variable | Default | Description |
|---|---|---|
| `BENCHMARK_USERS` | `100000` | Total number of simulated users |
| `BENCHMARK_WORKERS` | `2 × NumCPU` | Number of concurrent goroutines |
| `BENCHMARK_STORAGE` | `memory` | Storage backend: `memory` or `postgres` |
| `BENCHMARK_POSTGRES_DSN` | empty | Postgres DSN when `BENCHMARK_STORAGE=postgres`; falls back to `TEST_POSTGRES_DSN` |
| `BENCHMARK_POSTGRES_MAX_CONNS` | pgx default | Postgres pool max connections for benchmark storage |
| `BENCHMARK_HOME_SHARDS` | `1` | Spread users across N home roots for home-scoped promotion tests |

When using Postgres storage, `TestSimulate100kUsers` also logs pgx pool
observability for the foreground workload and promotion-drain phase:

- current acquired, idle, total, constructing, and max connections
- observed max acquired, idle, total, and constructing connections
- acquire count, empty-acquire count, canceled acquires, acquire duration,
  empty-acquire wait time, new connections, and destroy counts

Examples:

```bash
# 10 000 users, 8 workers
BENCHMARK_USERS=10000 BENCHMARK_WORKERS=8 \
  go test -v -timeout 300s ./benchmark_suite/ -run TestSimulate100kUsers

# Custom workers only
BENCHMARK_WORKERS=64 go test -v -timeout 600s ./benchmark_suite/ -run TestSimulate100kUsers
```

---

## How each "user" works

Each simulated user performs three sequential gRPC calls:

1. **`CreateSliceFromFolder`** – Creates a new slice branched from `root`
2. **`CreateChangeset`** – Registers a changeset touching one unique file
   (`bench/<padded-index>/main.go`)
3. **`MergeChangeset`** – Merges the changeset into the slice

Because each user owns a unique file, **no conflicts** are expected during the
load test.  The test fails if any conflict or error is observed.

---

## Integrity verification

After the load phase, `TestSimulate100kUsers` samples 1% of users (minimum 10)
at random and asserts:

- `GetSliceState` returns a non-empty commit hash
- `GetSliceCommits` returns at least one commit whose hash matches the state
- `GetFileHistory` (scoped to the slice) returns at least one change record

`TestIntegrity` performs a deeper check on 50 deterministic users:

- Slice state hash matches the commit returned by `MergeChangeset`
- `GetSliceCommits` history matches the merge response
- `ListChangesets` (filtered to `MERGED`) finds the changeset
- `GetFileHistory` returns at least one change record

---

## Conflict scenario

`TestConflictDetection` verifies the conflict model:

1. Two slices (`conflict-a-*`, `conflict-b-*`) are created from `root`
2. Both create a changeset touching `conflict/shared-*.go`
3. Slice A merges first → `MERGE_STATUS_SUCCESS`
4. Slice B merges second → `MERGE_STATUS_CONFLICT`
5. The conflict record names the shared file and lists slice A as the owner

`TestConflictDetectionUnderLoad` fans out 20 concurrent merges on the same
file and asserts exactly 1 success and 19 conflicts.

`TestConflictResolution` shows the admin workflow:

1. Both slices pre-register as owners of the same file
2. `GetConflicts` confirms the conflict is visible
3. `ResolveConflict` picks B as the preferred owner
4. B merges → `MERGE_STATUS_SUCCESS`
5. A merges → `MERGE_STATUS_CONFLICT` (B now owns the file)

---

## File structure

```
benchmark_suite/
  setup_test.go            – TestMain, shared gRPC server, helper functions
  load_test.go             – TestSimulate100kUsers, TestIntegrity
  conflict_test.go         – TestConflictDetection, TestConflictResolution,
                             TestConflictDetectionUnderLoad
  file_service_test.go     – TestFileServiceReadLoad,
                             TestFileServiceCommitChangesConsistency
  README.md                – This file
```
