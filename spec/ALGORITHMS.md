# Algorithms

This document describes the key algorithms used in the slice-based version control system.

---

## 1. Create Change List

**Goal:** Create a change list from local modifications

```
Input: slice_id, base_commit_hash, modified_files M, objects O

Algorithm:
# Generate change list hash
changeset_hash = hash(slice_id + base_commit_hash + M + O + timestamp)

# Store change list metadata
HSET changeset:{changeset_id} changeset_hash
HSET changeset:{changeset_id} slice_id slice_id
HSET changeset:{changeset_id} base_commit_hash base_commit_hash
HSET changeset:{changeset_id} status pending
HSET changeset:{changeset_id} author author
HSET changeset:{changeset_id} created_at current_timestamp

# Store modified files
DEL changeset:{changeset_id}:modified_files
FOR EACH file f IN M:
  SADD changeset:{changeset_id}:modified_files f

# Add to pending queue for review
ZADD changeset:{slice_id}:pending current_timestamp changeset_id

RETURN SUCCESS: {changeset_id, changeset_hash}

Complexity: O(k) where k = number of modified files
```

---

## 2. Review Change List

**Goal:** Review change list against slice head (basic validation, no conflict check yet)

```
Input: changeset_id

Algorithm:
# Get change list metadata
changeset = HGETALL changeset:{changeset_id}
slice_id = changeset.slice_id
base_commit_hash = changeset.base_commit_hash

# Get current slice head
slice_state = HGETALL slice:{slice_id}:state
current_head = slice_state.latest_commit_hash

# Verify base commit is still valid (can't review old changeset)
IF base_commit_hash != current_head:
  RETURN WARNING: "Changeset based on old commit. Consider rebase."

# Get modified files
modified_files = SMEMBERS changeset:{changeset_id}:modified_files

# Calculate diff against base commit
# (This is for display purposes, not conflict detection)
diff = calculate_diff(base_commit_hash, modified_files, objects)

RETURN REVIEW_DATA: {
  changeset_id,
  base_commit_hash,
  current_head,
  modified_files_count: len(modified_files),
  diff_summary: diff,
  status: "ready_for_merge" | "needs_rebase"
}

Complexity: O(k) where k = number of modified files
```

---

## 3. Merge Change List into Slice (with Conflict Detection)

**Goal:** Merge change list into slice, checking for conflicts with other slices

```
Input: changeset_id

Algorithm:
# Get change list metadata
changeset = HGETALL changeset:{changeset_id}
slice_id = changeset.slice_id
base_commit_hash = changeset.base_commit_hash
modified_files M = SMEMBERS changeset:{changeset_id}:modified_files

# Step 1: Conflict detection against other slices
FOR EACH file f IN M:
  active_slices = SGET file:{f}:active_slices

  # Check if other slices have modified this file
  # since base commit was created
  IF slice_id NOT IN active_slices AND active_slices IS NOT EMPTY:
    # Conflict detected - other slices modified this file
    RETURN CONFLICT: {
      changeset_id,
      file: f,
      conflicting_slices: active_slices - {slice_id}
    }

# Step 2: Create new slice commit from change list
# Build tree from modified files and base commit
new_tree = build_tree(base_commit_hash, M, changeset.objects)
new_commit_hash = hash(new_tree + base_commit_hash + metadata)

# Step 3: Store commit metadata
HSET commit:{new_commit_hash} tree_hash new_tree
HSET commit:{new_commit_hash} parent_hash base_commit_hash
HSET commit:{new_commit_hash} slice_id slice_id
HSET commit:{new_commit_hash} timestamp current_timestamp
HSET commit:{new_commit_hash} author changeset.author

# Step 4: Update slice state and active tracking atomically
BEGIN TRANSACTION:
  FOR EACH file f IN M:
    SADD file:{f}:active_slices slice_id

  HSET slice:{slice_id}:state latest_commit_hash new_commit_hash
  HSET slice:{slice_id}:state last_modified current_timestamp

  # Update modified files set
  DEL slice:{slice_id}:modified_files
  FOR EACH file f IN M:
    SADD slice:{slice_id}:modified_files f

  # Update changeset status
  HSET changeset:{changeset_id} status merged
  HSET changeset:{changeset_id} merged_at current_timestamp

  # Remove from pending queue
  ZREM changeset:{slice_id}:pending changeset_id

  # Add commit to batch merge queue
  ZADD commits:pending current_timestamp new_commit_hash
COMMIT TRANSACTION

RETURN SUCCESS: {new_commit_hash, changeset_id}

Complexity: O(k) where k = number of modified files
```

---

## 4. Rebase Change List

**Goal:** Rebase change list on new slice head after conflicts detected

```
Input: changeset_id

Algorithm:
# Get change list metadata
changeset = HGETALL changeset:{changeset_id}
slice_id = changeset.slice_id
old_base_commit_hash = changeset.base_commit_hash

# Get current slice head
slice_state = HGETALL slice:{slice_id}:state
new_base_commit_hash = slice_state.latest_commit_hash

# Calculate diff between old and new base commits
# (Find what changed in the slice since changeset was created)
slice_diff = calculate_commits_between(old_base_commit_hash, new_base_commit_hash)

# If slice modified same files as changeset → potential conflicts
modified_files_in_changeset = SMEMBERS changeset:{changeset_id}:modified_files
modified_files_in_slice = []
FOR EACH commit c IN slice_diff:
  commit_data = HGETALL commit:{c}
  # Get files modified by this commit
  # (This would be stored in commit metadata or derived from tree diff)
  modified_files_in_slice.extend(commit_data.modified_files)

# Check for file-level conflicts
conflicting_files = SET_INTERSECTION(modified_files_in_changeset, modified_files_in_slice)

IF len(conflicting_files) > 0:
  RETURN CONFLICT: {
    changeset_id,
    message: "Slice modified conflicting files since changeset created",
    conflicting_files
  }

# No conflicts - update base commit
HSET changeset:{changeset_id} base_commit_hash new_base_commit_hash

# Return updated changeset info for client to apply
RETURN SUCCESS: {
  changeset_id,
  new_base_commit_hash,
  old_base_commit_hash,
  slice_commits_to_apply: slice_diff
}

Complexity: O(n + k) where n = slice commits, k = changeset files
```

---

## 5. Batch Merge to Global

**Goal:** Merge slice commits into global state, avoid bottleneck of computing global hash on every commit

```
Algorithm:
# Step 1: Select commits ready for merge (FIFO by timestamp)
max_commits = 1000  # Configurable batch size
pending_commits = ZRANGE commits:pending 0 max_commits

# Step 2: Verify no conflicts (should always pass due to pre-check during changeset merge)
# Note: This is a safety check, conflicts should be caught during changeset merge
FOR EACH commit c IN pending_commits:
  commit_data = HGETALL commit:{c}
  slice_id = commit_data.slice_id
  modified_files = SMEMBERS slice:{slice_id}:modified_files

  FOR EACH file f IN modified_files:
    active_slices = SGET file:{f}:active_slices
    IF len(active_slices) > 1:
      LOG ERROR: "Unexpected conflict detected"
      RETURN ERROR

# Step 3: Create global commit tree
# Merge slice trees into single global tree
global_tree = merge_trees([commit.tree_hash FOR commit IN pending_commits])
# For same file in multiple slices: Last write wins (no conflicts expected)

# Step 4: Compute global commit hash
global_commit = {
  tree_hash: global_tree.hash,
  parent_hash: current_global_hash,
  merged_slices: [c.slice_id FOR c IN pending_commits],
  timestamp: current_timestamp
}
global_commit_hash = hash(global_commit)

# Step 5: Store global commit
HSET commit:{global_commit_hash} global_commit
ZADD commits:global current_timestamp global_commit_hash
SET global:head global_commit_hash

# Step 6: Clean up - remove merged slices from active tracking
FOR EACH commit c IN pending_commits:
  slice_id = commit_data.slice_id
  modified_files = SMEMBERS slice:{slice_id}:modified_files

  FOR EACH file f IN modified_files:
    SREM file:{f}:active_slices slice_id

  # Clear slice's modified files
  DEL slice:{slice_id}:modified_files
  # Update slice state to point to new global state
  HSET slice:{slice_id}:state last_merged_commit global_commit_hash

  # Remove from pending queue
  ZREM commits:pending c.commit_hash

RETURN SUCCESS: {
  global_commit_hash,
  merged_slice_count: len(pending_commits),
  merged_slice_ids: [...]
}

Complexity: O(N * avg_tree_size) where N = batch size
Optimization: Reuse unchanged subtrees, parallel merge across workers
```

---

## 6. Checkout Slice

**Goal:** Efficiently retrieve slice state for client

```
Input: slice_id, commit_hash (or "HEAD" for latest)

Algorithm:
IF commit_hash == "HEAD":
  commit_hash = HGET slice:{slice_id}:state latest_commit_hash

# Get commit metadata
commit_data = HGETALL commit:{commit_hash}

# Get tree and recursively traverse to build file list
tree = load_tree(commit_data.tree_hash)
manifest = build_manifest(tree)

# Return manifest with file metadata and content URLs
manifest_files = []
FOR EACH file IN manifest:
  file_metadata = {
    file_id: file.hash,
    path: file.path,
    size: file.size,
    hash: file.hash,
    content_url: generate_presigned_url(file.hash)  # For S3/object store
  }
  manifest_files.append(file_metadata)

RETURN {
  commit_hash: commit_hash,
  manifest: manifest_files
}

Optimization: Cache manifests for hot slices in Redis
Complexity: O(f) where f = number of files in slice
```

---

## Algorithm Complexity Summary

| Algorithm | Time Complexity | Space Complexity | Notes |
|-----------|-----------------|------------------|--------|
| Create Change List | O(k) | O(k) | k = number of modified files |
| Review Change List | O(k + d) | O(k + d) | k = modified files, d = diff size |
| Merge Change List | O(k) | O(k) | k = modified files |
| Rebase Change List | O(n + k) | O(n + k) | n = slice commits, k = changeset files |
| Batch Merge to Global | O(N * t) | O(N) | N = batch size, t = avg tree size |
| Checkout Slice | O(f) | O(f) | f = number of files in slice |

---

## Optimization Strategies

### 1. Pre-computed Manifests
**Purpose:** Avoid tree traversal on checkout
**Implementation:**
- At commit time: Generate and store full file list
- At checkout: O(1) manifest retrieval
**Tradeoff:** Extra storage (manifest:{commit_hash}), faster checkout

### 2. Manifest Caching
**Purpose:** Fast checkout for hot slices
**Implementation:**
- Cache top 10% of slice manifests in Redis
- LRU eviction, TTL: 1 hour
**Tradeoff:** Memory usage, stale cache acceptable

### 3. Parallel Object Fetching
**Purpose:** Reduce checkout latency
**Implementation:**
- Client: Fetch 100+ files concurrently
- Presigned URLs for direct S3 access
**Tradeoff:** More client complexity, faster checkout

### 4. Batch Index Updates
**Purpose:** Reduce network round-trips
**Implementation:**
- Single transaction for atomic updates
- Redis pipeline for efficiency
- Batch size: 1000 operations per transaction
**Tradeoff:** Transaction size vs latency

### 5. Tree Merging Optimization
**Purpose:** Faster batch merge
**Implementation:**
- Reuse unchanged subtrees (Merkle tree property)
- Only recompute hash for changed files
- Parallel merge across workers
**Tradeoff:** Complexity vs performance gain

### 6. Sparse Checkout (Future)
**Purpose:** Reduce network transfer for large slices
**Implementation:**
- Client: Request subset of files
- Server: Return only requested files
**Tradeoff:** Additional API complexity, better for large slices

---

## Pseudo-Code Examples

### Redis Transaction Example

```
BEGIN TRANSACTION:
  # Add slice to active tracking for all modified files
  FOR EACH file f IN modified_files:
    SADD file:{f}:active_slices slice_id

  # Update slice state
  HSET slice:{slice_id}:state latest_commit_hash new_commit_hash
  HSET slice:{slice_id}:state last_modified current_timestamp

  # Update modified files set
  DEL slice:{slice_id}:modified_files
  FOR EACH file f IN modified_files:
    SADD slice:{slice_id}:modified_files f

  # Update changeset status
  HSET changeset:{changeset_id} status merged
  HSET changeset:{changeset_id} merged_at current_timestamp

  # Remove from pending queue, add to merge queue
  ZREM changeset:{slice_id}:pending changeset_id
  ZADD commits:pending current_timestamp new_commit_hash
COMMIT TRANSACTION
```

### Conflict Detection Example

```
FOR EACH file f IN modified_files:
  active_slices = SGET file:{f}:active_slices

  # Check if other slices have modified this file
  # since base commit was created
  IF slice_id NOT IN active_slices AND active_slices IS NOT EMPTY:
    # Conflict detected
    conflicting_slices = active_slices - {slice_id}
    RETURN CONFLICT: {
      file_id: f,
      file_path: get_file_path(f),
      conflicting_slices: conflicting_slices,
      severity: HIGH
    }
```

### Tree Merge Example

```
FUNCTION merge_trees(slice_tree_hashes):
  # Start with empty tree
  merged_tree = {}

  FOR EACH tree_hash IN slice_tree_hashes:
    tree = load_tree(tree_hash)

    FOR EACH entry IN tree.entries:
      IF entry.name IN merged_tree:
        # Conflict: same file in multiple slices
        # Last write wins
        merged_tree[entry.name] = entry
      ELSE:
        # No conflict
        merged_tree[entry.name] = entry

  # Compute hash of merged tree
  merged_tree_hash = hash(serialize(merged_tree))

  RETURN merged_tree_hash
```

---

## Error Handling

### Conflict Resolution Flow

```
1. Detect conflict during changeset merge
   ↓
2. Return CONFLICT error to client with:
   - File(s) with conflicts
   - Conflicting slice IDs
   - Modification details
   ↓
3. Client calls RebaseChangeset
   ↓
4. Server checks for file-level conflicts with slice commits
   ↓
5. IF conflicts exist:
     - Return CONFLICT with conflicting files
     - Client must resolve manually
   ELSE:
     - Update base commit to current slice head
     - Return SUCCESS with commits to apply
     ↓
6. Client applies slice commits, resolves conflicts
     ↓
7. Client retries MergeChangeset
     ↓
8. Success (no conflicts this time)
```

### Transaction Retry Logic

```
RETRY_COUNT = 0
MAX_RETRIES = 3

WHILE RETRY_COUNT < MAX_RETRIES:
  BEGIN TRANSACTION:
    # ... operations ...
  COMMIT TRANSACTION:
    BREAK  # Success
  CATCH TRANSACTION_ERROR:
    RETRY_COUNT++
    IF RETRY_COUNT >= MAX_RETRIES:
      RETURN ERROR: "Transaction failed after retries"
    SLEEP (2^RETRY_COUNT * 100ms)  # Exponential backoff
```

---

## Performance Considerations

### Throughput Optimization

**Changeset Merge Throughput:**
```
Small changeset (5 files): 5 set lookups = ~5ms
Medium changeset (50 files): 50 set lookups = ~50ms
Large changeset (500 files): 500 set lookups = ~500ms

Single node: ~100k merges/sec (small)
Cluster (50 nodes): ~5M merges/sec (small)
```

**Batch Merge Throughput:**
```
Small batch (100 slices): 1-3s
Medium batch (500 slices): 3-7s
Large batch (1000 slices): 7-10s

Parallel workers (10 workers): 10x throughput = ~50M slices/day
```

### Latency Targets

- **Create changeset:** < 100ms
- **Review changeset:** < 200ms
- **Merge changeset:** < 500ms
- **Rebase changeset:** < 1s
- **Checkout slice:** < 1s
- **Batch merge:** < 10s

---

## Scalability Notes

### Horizontal Scaling

**By Slice ID:**
- All slice operations hit same shard
- Enables per-shard caching
- Consistent hashing for even distribution

**By File Hash:**
- Object store sharding
- Even distribution across billions of files
- Natural deduplication

**By Worker ID:**
- Parallel batch merge workers
- Each worker handles subset of slices
- No worker contention

### Vertical Scaling

**Redis Cluster:**
- Start with 10 nodes
- Scale to 100+ nodes as needed
- 16GB-64GB RAM per node
- ~5M set ops/sec (50 nodes)

**Object Store:**
- S3/GCS for managed service
- Custom store for cost optimization
- Multi-region replication
- CDN integration for global access

---

## Conclusion

These algorithms provide:

✅ **Efficient conflict detection** - O(k) where k = modified files
✅ **Fast changeset workflow** - Create, review, merge, rebase
✅ **Scalable batch merge** - Parallel workers, tree reuse
✅ **Optimized checkout** - Manifest caching, pre-computation
✅ **Atomic operations** - Redis transactions for consistency

The system is designed to handle millions of commits per day with sub-second operation latency.