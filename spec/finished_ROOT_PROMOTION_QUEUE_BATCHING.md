# Root Promotion Queue and Same-Slice Batching

## Implementation Status

- Current status: `finished`
- Last updated: `2026-02-19`

## Summary

Root promotion is now asynchronous from `MergeChangeset`.

Instead of writing root/global state inline on every merge, `MergeChangeset` enqueues a promotion job. A background worker drains the queue and applies jobs in batched writes. Batching also coalesces repeated file ownership updates from the same slice (and any duplicate file IDs across queued jobs).

Implementation is in `services/slice/server.go`.

## Why

The previous path promoted to root synchronously and serialized all promotions behind one mutex. Under concurrent merges this made root/global-state writes a hot path bottleneck.

Queueing + batching improves throughput by:

1. Removing root promotion I/O from merge request latency.
2. Deduplicating repeated root file ownership writes for same-slice bursts.
3. Collapsing multiple global/root metadata writes into one write batch.

## Flow

1. `MergeChangeset` computes the new slice commit and updates slice-local metadata.
2. It enqueues a `rootPromotionJob` (`sliceID`, `commitHash`, `files`, `commitTime`).
3. A singleton worker goroutine drains `promotionQueue`.
4. Worker batching rule:
   - Start from the first queued job.
   - Collect additional queued jobs until:
     - batch window timeout (`defaultPromotionBatchWindow`), or
     - max batch size (`defaultPromotionBatchMaxSize`).
5. Worker applies one `promoteSliceBatch` call for the collected jobs.

## Batch Apply Semantics

For a batch of N queued jobs:

1. Root file ownership writes (`AddFileToSlice`) run once per unique file across all N jobs.
2. Global history still records all N commits (newest-first), preserving commit timeline.
3. `GlobalCommitHash` and root metadata head point to the newest commit in the batch.
4. Root `ModifiedFiles` reflects the newest commit's modified file list (same final state as sequential per-commit promotion).

## Consistency Model

This introduces eventual consistency between:

1. successful `MergeChangeset` response, and
2. root/global-state visibility of that merge.

Merges remain accepted/validated synchronously (locking/conflict checks are unchanged). Only root promotion persistence is deferred.

If the async queue is saturated, merge falls back to synchronous promotion for that request to preserve correctness and avoid indefinite queue backpressure stalls.

## Failure Behavior

If batched promotion fails, the worker logs the error and continues processing later jobs. Merge status is not retroactively changed.

## Defaults

- Queue size: `1024`
- Batch window: `200ms`
- Max batch size: `64`

These defaults are intentionally conservative and can be tuned later with workload data.

## Test Coverage Added

`services/slice/server_test.go` now includes:

1. `TestMergeChangesetDeduplicatesModifiedFiles` updated to wait for async queue drain.
2. `TestRootPromotionQueueBatchesSameSlice` validating that same-slice bursts collapse to:
   - one global state write,
   - one root metadata write,
   - one root ownership write per unique file.
