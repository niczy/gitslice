# Gitslice Import Benchmark Results

Benchmarking the speed of importing top 100 GitHub repos (by stars) using the
`gs import git` CLI command against a local server with in-memory storage.

## Test Environment

- **Server**: `core_server` with `STORAGE_TYPE=memory` (in-memory storage)
- **Transport**: gRPC on `localhost:50051`
- **Import settings**: `--max-commits 1 --first-parent` (single commit per repo)
- **Parallelism**: 10 concurrent imports
- **Date**: 2026-02-14

## Summary Results

| Metric | Value |
|---|---|
| Total repos attempted | 100 |
| Successful imports | 83 |
| Failed (timeout) | 17 |
| Wall clock time | 493.44s (~8.2 min) |
| Sum of all import times | 1,239.93s |
| Effective parallelism speedup | 2.51x |
| Min import time | 0.76s |
| Max import time | 59.80s |
| Avg import time (successful) | 14.93s |
| Median import time | 7.25s |
| Throughput | 0.16 repos/sec |

## Bottleneck Analysis

### 1. Primary Bottleneck: `git clone --bare` (Network I/O)

The dominant cost is cloning the repository on the server side. The import
process calls `git clone --bare --quiet <url> <tmpdir>` before processing any
commits. For large repos, this is the vast majority of the import time.

**Correlation between repo size and import time (successful imports):**

| Repo Size | Avg Import Time | Example Repos |
|---|---|---|
| < 5 MB | ~1.1s | nocode, awesome, public-apis |
| 5-50 MB | ~4.5s | vue, axios, bootstrap |
| 50-200 MB | ~12s | ollama, electron, 996.ICU |
| 200-500 MB | ~35s | react, PowerToys, transformers |
| 500+ MB | FAILED (>60s) | linux, tensorflow, vscode |

### 2. Critical Bug: Hard-coded 60s Context Timeout

**File**: `gs_cli/main.go:48`

```go
ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
```

The CLI creates a 60-second parent context that overrides the per-command
`--timeout` flag. Even when users pass `--timeout 30m`, the parent context
cancels after 60 seconds. This causes all imports of repos >~400MB to fail
with `DeadlineExceeded`.

**All 17 failed repos exceeded 60s**, with the smallest being flutter/flutter
(419MB). All failed at exactly ~60.06s.

**Failed repos (sorted by size):**

| Repo | Size (MB) | Time at Failure |
|---|---|---|
| microsoft/generative-ai-for-beginners | 6,432 | 60.05s |
| torvalds/linux | 5,917 | 60.07s |
| microsoft/TypeScript | 2,868 | 60.09s |
| vercel/next.js | 2,425 | 60.05s |
| supabase/supabase | 2,223 | 60.07s |
| godotengine/godot | 1,725 | 60.09s |
| mrdoob/three.js | 1,521 | 60.06s |
| kubernetes/kubernetes | 1,410 | 60.06s |
| nodejs/node | 1,382 | 60.05s |
| tensorflow/tensorflow | 1,231 | 60.06s |
| pytorch/pytorch | 1,209 | 60.07s |
| iptv-org/iptv | 1,159 | 60.07s |
| langflow-ai/langflow | 1,127 | 60.06s |
| microsoft/vscode | 1,101 | 60.07s |
| facebook/react-native | 939 | 60.06s |
| rust-lang/rust | 825 | 60.06s |
| flutter/flutter | 419 | 60.06s |

### 3. Commit Processing Cost

For the sindresorhus/awesome repo (1.5MB), measured commit processing
overhead:

| Commits | Total Time | Time per Commit |
|---|---|---|
| 1 | 759ms | — (clone overhead) |
| 10 | 960ms | ~20ms/commit |
| 50 | 2,320ms | ~31ms/commit |
| 100 | 3,593ms | ~28ms/commit |

Clone takes ~700ms for this small repo. Each additional commit costs ~28ms,
which includes: git show (metadata), git diff-tree (diff), git cat-file
(content), SHA-256 hash, and in-memory storage writes.

### 4. Parallelism Effectiveness

| Mode | 5 Small Repos | Wall Time |
|---|---|---|
| Sequential | 8,345ms | — |
| Parallel (5 concurrent) | ~6,420ms | 1.3x speedup |

With 100 repos at parallelism=10:
- Sum of times: 1,239.93s
- Wall clock: 493.44s
- Effective speedup: **2.51x** (not 10x)

The parallelism benefit is limited by:
1. **Network bandwidth contention**: Multiple `git clone` operations compete
   for bandwidth
2. **Large repo domination**: Wall time is limited by the slowest concurrent
   batch
3. **Server-side sequential processing**: Each import holds a gRPC handler
   thread and performs sequential git operations

### 5. Per-Repo Import Breakdown (Server-Side)

For a typical import (e.g., 50MB repo, 1 commit):

| Phase | Approx. Time | % of Total |
|---|---|---|
| `git clone --bare` (network) | ~8s | ~90% |
| `git rev-list` (commit list) | <10ms | <1% |
| `git show` (commit metadata) | <10ms | <1% |
| `git diff-tree` (file changes) | <10ms | <1% |
| `git cat-file` (blob content) | <50ms | <1% |
| SHA-256 + storage writes | <50ms | ~5% |
| gRPC overhead | <50ms | ~5% |

## Recommendations

### Fix: Remove 60s hard-coded timeout (`gs_cli/main.go:48`)

The parent context should not have a timeout, or should use a much larger
default (e.g., 1 hour). The per-command `--timeout` flag should be the
authoritative timeout.

### Optimization: Shallow clone for import

Use `git clone --bare --depth 1` when `--max-commits 1` to avoid downloading
full history. For a repo like freeCodeCamp (550MB full clone), a shallow clone
would be significantly faster.

### Optimization: Pre-clone repos locally

For batch imports, clone repos to a local cache first (possibly with `--depth`
or `--filter=blob:none`), then point the import at local paths. This
separates network I/O from import processing.

### Optimization: Parallel commit processing

The current import processes commits sequentially within a single gRPC call.
For repos with many commits, processing diffs and blob extraction in parallel
(using goroutines) would reduce per-commit overhead.

### Optimization: True per-slice parallelism

Currently all imports go to `root_slice` because non-root slices must be
pre-created. Adding auto-creation of slices during import would enable
true isolation and potentially better parallelism at the storage level.

## Raw Data

See `results_100repos_parallel10.csv` for the full dataset.
