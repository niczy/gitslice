# Slice-Based Version Control System Architecture

## Executive Summary

A source control system that scales to billions of files and millions of commits per day, using a git-like object store with slice-based versioning. Users define "slices" of code, create change lists for modifications, review them, and merge into slices with automatic conflict detection against overlapping slices.

---

## 1. Core Concepts

For detailed data model definitions and protobuf schemas, see [DATA_MODEL.md](./DATA_MODEL.md).

For detailed algorithms and operations, see [ALGORITHMS.md](./ALGORITHMS.md).

### Metadata Indexes (Hybrid Approach)

**Primary source of truth:** Object store (immutable, versioned)
**Performance layer:** Redis indexes (fast lookups)

#### Index 1: Slice → Files
```
Key: slice:{slice_id}:files
Type: Set
Value: {file_id1, file_id2, ...}
Purpose: Fast file listing for checkout, push operations
Redis Set for O(1) membership and O(N) iteration
```

#### Index 2: File → Active Slices
```
Key: file:{file_id}:active_slices
Type: Set
Value: {slice_id1, slice_id2, ...}
Purpose: Critical for conflict detection - find overlapping slices
Redis Set for O(1) intersection queries
```

#### Index 3: Slice State
```
Key: slice:{slice_id}:state
Type: Hash
Fields:
  - latest_commit_hash: string
  - modified_files: Set<file_id> (stored as separate key for efficiency)
  - last_modified: timestamp (Unix epoch)

Purpose: Track current slice state and modified files for conflict detection
```

#### Index 4: Commit Metadata
```
Key: commit:{commit_hash}
Type: Hash
Fields:
  - tree_hash: string
  - parent_hash: string
  - slice_id: string
  - timestamp: int64
  - message: string

Sorted index for batch merge:
Key: commits:pending (Sorted Set)
Score: timestamp
Value: commit_hash
Purpose: Queue commits for batch merging to global
```

#### Index 5: Change List Metadata
```
Key: changeset:{changeset_id}
Type: Hash
Fields:
  - changeset_hash: string
  - slice_id: string
  - base_commit_hash: string
  - modified_files: Set<file_id> (stored separately)
  - status: string (pending, approved, rejected, merged)
  - author: string
  - created_at: int64
  - merged_at: int64 (if merged)

Sorted index for change list queue:
Key: changeset:{slice_id}:pending (Sorted Set)
Score: timestamp
Value: changeset_id
Purpose: Queue pending change lists for review
```

---

## 2. Storage Architecture

### Object Store Storage

#### Option A: S3-like Object Store (Recommended)

**Why:** Industry-standard, battle-tested, handles billions of objects

```
Storage structure:
/objects/{hash_prefix2}/{hash}
Example: /objects/ab/cdef1234567890...

Features:
- Immutable writes (once uploaded, never modified)
- Content-addressable (deduplication built-in)
- Built-in replication, durability (99.999999999%)
- Presigned URLs for direct client access
- Lifecycle policies for cost optimization (infrequent access)
```

**Advantages:**
- Scales to billions of objects without operational overhead
- Built-in CDN integration for fast global access
- Cost-effective (storage classes: Standard, IA, Glacier)
- Handles large files efficiently (multi-part upload)

**Estimated cost for 10PB:**
- Standard storage: ~$200K/month
- With lifecycle policies: ~$50K/month (80% in cheaper tiers)

#### Option B: Custom Object Store (If cost optimization critical)

```
Design:
- Sharded by hash prefix (consistent hashing)
- Local storage on commodity hardware
- Deduplication layer (identical files stored once)
- LRU cache for hot objects
- Background garbage collection for orphaned objects

Tradeoffs:
- Lower cost (~60% of S3)
- Higher operational complexity
- Need to implement replication, durability, monitoring
```

### Metadata Storage

#### Redis Cluster

**Why:** Purpose-built for fast set operations, O(1) conflict detection

```
Redis Cluster Configuration:
- 10-100 nodes (depending on scale)
- Hash slot sharding (16384 slots across nodes)
- Sentinel for high availability (3 nodes per shard)
- AOF persistence for durability
- 16GB-64GB RAM per node

Data structures:
- Sets: Slice membership, conflict tracking
- Hashes: Slice state, commit metadata
- Sorted Sets: Pending commit queue, global history
```

**Advantages:**
- O(1) set intersection for conflict detection
- O(1) add/remove operations for active slice tracking
- In-memory performance (sub-millisecond latency)
- Cluster scales horizontally
- Native support for data structures we need

**Estimated capacity:**
- Single node: ~100k set ops/sec
- Cluster of 50 nodes: ~5M set ops/sec
- Supports millions of concurrent slices

#### Backup & Recovery

```
Strategy:
- Primary: AOF (Append Only File) - every write logged
- Secondary: RDB snapshots every hour
- Object store: Rebuild Redis indexes from SliceDef objects

Recovery process:
1. Restore Redis from latest snapshot
2. Replay AOF logs
3. For any missing data: Query object store for SliceDef objects
4. Rebuild indexes from SliceDef data
5. Verify consistency against object store
```

---

## 3. Scalability Strategy

### Horizontal Scaling

#### Sharding Strategy 1: By Slice ID (Checkout/Push Operations)

```
Shard selection:
  shard_id = hash(slice_id) % num_shards

Benefits:
- All slice operations hit same shard (no cross-shard queries)
- Enables per-shard caching and optimization
- Easy to add/remove shards

Example:
- 10 shards for initial deployment
- Scale to 100+ shards as needed
- Each shard: 1 Redis cluster + 1 object store partition

Implementation:
- Consistent hashing to minimize shard movement
- Each shard independent (no single point of failure)
```

#### Sharding Strategy 2: By File Hash (Object Store)

```
Shard selection:
  shard_id = hash(file_id) % num_shards

Benefits:
- Even distribution across billions of files
- No single shard becomes bottleneck
- Parallel object fetch during checkout

Implementation:
- Content-addressable storage (CAS)
- Same file in different slices = same shard
- Natural deduplication
```

#### Sharding Strategy 3: By Slice ID (Batch Merge Workers)

```
Worker assignment:
  worker_id = hash(slice_id) % num_workers

Benefits:
- Parallel merging across workers
- Each worker handles subset of slices
- No worker contention on slice merges

Example:
- 10 workers processing 1M slices/day
- Each worker: 100k slices/day
- Parallel tree merging
```

### Performance Optimizations

#### Checkout < 1s

**Strategies:**

1. **Pre-computed manifests**
   ```
   At commit time: Generate and store full file list
   At checkout: O(1) manifest retrieval, no tree traversal
   Storage: manifest:{commit_hash} → Set<file_metadata>
   ```

2. **Manifest caching**
   ```
   Cache hot slice manifests in Redis
   LRU eviction (cache top 10% of slices)
   TTL: 1 hour (stale but acceptable)
   ```

3. **Parallel object fetching**
   ```
   Client: Fetch 100+ files concurrently
   HTTP/2 multiplexing or batch API
   Presigned URLs for direct S3 access
   ```

4. **Sparse checkout** (future optimization)
   ```
   Client: Request subset of files
   Server: Return only requested files
   Reduces network transfer for large slices
   ```

**Performance estimate:**
- Small slice (100 files): 50-200ms
- Medium slice (10K files): 200-500ms
- Large slice (100K files): 500-1000ms

#### Push < 1s

**Strategies:**

1. **O(k) conflict check**
   ```
   k = number of modified files (typically 5-10)
   Redis set operations: O(1) average per file
   Total: O(k) where k << slice size
   ```

2. **Batch index updates**
   ```
   Single transaction for atomic updates
   Redis pipeline for network efficiency
   Batch size: 1000 operations per transaction
   ```

3. **Fast metadata storage**
   ```
   Commit metadata: Hash (O(1) read/write)
   Conflict tracking: Set operations (O(1))
   No scanning entire slice
   ```

**Performance estimate:**
- Small push (5 files): 10-50ms
- Medium push (50 files): 50-200ms
- Large push (500 files): 200-500ms

#### Batch Merge < 10s

**Strategies:**

1. **Limit batch size**
   ```
   Configurable: 100-1000 slices per batch
   Tradeoff: Larger batch = fewer merges, slower
   Auto-tune based on load
   ```

2. **Tree merging optimization**
   ```
   Reuse unchanged subtrees (Merkle tree property)
   If file unchanged: Use same tree node hash
   Only recompute hash for changed files
   ```

3. **Parallel merge workers**
   ```
   Each worker: 1 batch merge in parallel
   10 workers = 10x throughput
   Load balance by slice_id sharding
   ```

4. **Defer global hash computation**
   ```
   Compute only once per batch (not per commit)
   Last step in batch merge
   Parallel tree merging, single hash compute
   ```

**Performance estimate:**
- Small batch (100 slices): 1-3s
- Medium batch (500 slices): 3-7s
- Large batch (1000 slices): 7-10s

### Handling Billions of Files

#### Efficient Indexing

```
File ID Scheme: Content-addressable
  file_id = SHA256(file_content)

Benefits:
- Same file across slices = same ID
- Natural deduplication (store once)
- Fast lookup: O(1) via hash
- No need for file name in ID

Directory Trees: Merkle trees
  tree_hash = hash({name1: child_hash1, name2: child_hash2, ...})

Benefits:
- Change detection: O(log n) not O(n)
- Efficient for large repos
- Parallel tree traversal
- Can diff two trees efficiently
```

#### Memory Optimization

```
Strategy:
- Keep only active slice metadata in Redis
  - Historical data stays in object store
  - Indexes contain only current state

- Offload cold data
  - Move inactive slice indexes to disk
  - Query object store if needed

- Compressed data structures
  - Redis Sets: Optimize for memory
  - Hash fields: Use small strings
  - Numeric values: Use int encoding

Garbage Collection:
- Orphaned objects: Not referenced by any commit/slice
- Periodic GC job: Scan all commits, find unreferenced objects
- Mark and sweep: Mark all referenced objects, delete rest
- Run frequency: Daily during low-traffic hours
```

---

## 4. API Design: gRPC Protocol

The gRPC API design has been extracted to a separate document: **[API_DESIGN.md](./API_DESIGN.md)**

The API specification includes:
- Complete service definitions (SliceService, AdminService)
- Message definitions for all requests and responses
- Streaming operations (checkout, changeset creation, conflict watching)
- Error handling and status codes
- Implementation notes for each RPC method
- CLI to API mapping

**Key API Features:**
- **SliceService**: Core operations for slice management and change list workflows
- **AdminService**: Administrative operations for batch merging, monitoring, and global state
- **Streaming Support**: Server, client, and bidirectional streaming for high-throughput operations
- **Type Safety**: Protocol Buffers for compile-time type checking
- **Performance**: Binary serialization and HTTP/2 multiplexing

---

## 4. Consistency & Reliability

### Conflict Guarantees

**Strong Consistency for Conflict Detection:**
- Redis transactions ensure atomic conflict checking and index updates
- No two conflicting commits can be accepted into slices
- File → Active Slices Index always reflects current state
- Race conditions prevented via transactional operations
- Conflicts detected during change list merge (not on creation)

**Eventual Consistency for Global State:**
- Global state can lag behind slice states (batch merge)
- Clients see slice state immediately, global state eventually
- Acceptable tradeoff for performance (avoid per-commit global hash)

**No Conflicting Commits Reach Global State:**
- Pre-merge conflict check (during changeset merge) guarantees no overlaps
- Batch merge verifies again (safety check)
- Conflicts must be resolved before changeset can be merged
- Global state is always conflict-free

### Failure Recovery

**Object Store Failures:**
- Immutable objects can be rebuilt from replicas
- S3: 99.999999999% durability (11 9's)
- Object-level replication across multiple AZs
- Recovery: Fetch replica from other AZ

**Redis Failures:**
- AOF persistence: Every write logged to disk
- RDB snapshots: Hourly backups
- Sentinel failover: Automatic master promotion
- Recovery: Rebuild from object store if needed

**Batch Merge Failures:**
- Idempotent operations (can retry)
- Failed batch stays in pending queue
- Retry with exponential backoff
- No data loss (commits in object store)

**Client Operation Failures:**
- Automatic retries for transient failures
- Exponential backoff (100ms → 1s → 10s → 60s)
- Max retries: 3-5 attempts
- User notification for persistent failures

### Garbage Collection

**Orphaned Objects:**
- Objects not referenced by any commit/slice
- Occur when commits are deleted or branches pruned

**GC Algorithm:**
```
1. Mark phase:
   - Scan all commits in all slices
   - Mark all referenced trees, blobs
   - Mark all slice definitions
   - Mark global commit history

2. Sweep phase:
   - Scan all objects in object store
   - Delete objects not marked
   - Batch deletes (1000 objects at a time)

3. Frequency: Daily during low-traffic hours
```

**Optimization:**
- Reference counting on object write (increment)
- Decrement on commit deletion (if ref_count == 0 → delete)
- Faster for active repo, background sweep for orphaned refs

---

## 5. Estimated Capacity & Performance

### Benchmarks (Estimated)

#### Single Redis Node
- **Set operations:** ~100k ops/sec
- **Hash operations:** ~150k ops/sec
- **Memory:** 16GB RAM holds ~10M keys
- **Latency:** < 1ms average

#### Redis Cluster (50 nodes)
- **Set operations:** ~5M ops/sec
- **Hash operations:** ~7.5M ops/sec
- **Memory:** 800GB RAM holds ~500M keys
- **Latency:** < 1ms average (intra-cluster)

#### Object Store (S3)
- **Read throughput:** 10k-100k GETs/sec (depends on prefix distribution)
- **Write throughput:** 5k-50k PUTs/sec
- **Latency:** 50-200ms average (global)
- **Presigned URL:** Direct S3 access, bypasses server

### Operation Performance

#### Change List Merge Throughput
```
Conflict check per changeset merge:
- Small changeset (5 files): 5 set lookups = ~5ms
- Medium changeset (50 files): 50 set lookups = ~50ms
- Large changeset (500 files): 500 set lookups = ~500ms

Total throughput:
- Single node: ~100k merges/sec (small)
- Cluster (50 nodes): ~5M merges/sec (small)
```

#### Checkout Throughput
```
Manifest retrieval: O(1) from cache or ~10ms from Redis
Object fetch: Limited by S3 bandwidth and client parallelization
- Small slice (100 files): 50-200ms (presigned URLs, client parallel fetch)
- Medium slice (10K files): 200-500ms
- Large slice (100K files): 500-1000ms

Total throughput: Limited by S3 and client, not metadata layer
```

#### Batch Merge Throughput
```
Tree merging: O(N * avg_tree_size)
- Small batch (100 slices): 1-3s
- Medium batch (500 slices): 3-7s
- Large batch (1000 slices): 7-10s

Parallel workers (10 workers):
- 10x throughput = ~50M slices/day merge capacity
```

### Storage Capacity

#### Object Store (10PB example)
```
Files: 1B files, avg 10KB each
- Storage: 10TB raw, ~5TB compressed/deduped

Files: 10B files, avg 100KB each
- Storage: 1PB raw, ~500TB compressed/deduped

Files: 100B files, avg 100KB each
- Storage: 10PB raw, ~5PB compressed/deduped

Cost (S3 Standard):
- 10PB: ~$200K/month
- 1PB: ~$20K/month
- 100TB: ~$2K/month

Cost (with lifecycle policies, 80% in IA/Glacier):
- 10PB: ~$40K/month
- 1PB: ~$4K/month
- 100TB: ~$400/month
```

#### Metadata Storage
```
Redis cluster (50 nodes, 32GB RAM each):
- Total RAM: 1.6TB
- Keys: ~500M (assuming 3KB per key)
- Supports: 1M active slices

Redis cluster (100 nodes, 64GB RAM each):
- Total RAM: 6.4TB
- Keys: ~2B (assuming 3KB per key)
- Supports: 4M active slices
```

### Network Bandwidth

#### Checkout Traffic
```
Small slice (100 files, avg 10KB):
- Data: 1MB
- Network: ~10ms on 1Gbps

Medium slice (10K files, avg 10KB):
- Data: 100MB
- Network: ~800ms on 1Gbps

Large slice (100K files, avg 10KB):
- Data: 1GB
- Network: ~8s on 1Gbps

Optimization: Client-side caching, delta transfers
```

#### Push Traffic
```
Small push (5 files, avg 10KB):
- Data: 50KB
- Network: < 1ms on 1Gbps

Large push (500 files, avg 10KB):
- Data: 5MB
- Network: ~40ms on 1Gbps
```

---

## 6. Open Questions for Implementation

### 1. Object Store Choice
**Options:**
- S3/GCS (Managed service, higher cost, low ops)
- Custom object store (Commodity hardware, lower cost, high ops)

**Decision factors:**
- CAPEX vs OPEX tradeoff
- Team expertise in distributed storage
- Cost sensitivity
- Regulatory/compliance requirements

### 2. Redis Cluster Size
**Options:**
- Start small (10 nodes) and scale
- Deploy large cluster (100 nodes) upfront

**Decision factors:**
- Expected initial load
- Growth projections
- Budget constraints
- Team capacity for operations

### 3. Batch Merge Frequency
**Options:**
- Continuous (every N seconds)
- Scheduled (every M minutes)
- Trigger-based (when X commits accumulate)
- Hybrid (continuous with backpressure)

**Decision factors:**
- Freshness requirements (how stale can global state be?)
- Load patterns (batch of pushes vs steady stream)
- Cost vs freshness tradeoff

### 4. Authentication/Authorization
**Options:**
- JWT (Stateless tokens, simple)
- OAuth 2.0 (Third-party integration, complex)
- Custom API keys (Simple, less secure)

**Decision factors:**
- Security requirements
- User base size
- Integration needs (SSO, etc.)
- Team expertise

### 5. Client Protocol Features
**Options:**
- gRPC only (High performance, requires gRPC libraries)
- gRPC + REST gateway (Compatibility, performance)
- Custom protocol (Optimization, complexity)

**Decision factors:**
- Client diversity (Web, mobile, CLI)
- Performance requirements
- Team preference
- External integrations

---

## 7. Deployment Architecture

### Recommended Setup

```
┌─────────────────────────────────────────────────────────────┐
│                        Client Layer                          │
│  CLI Clients | Web Clients | Mobile Apps | IDE Plugins       │
└─────────────────────────┬───────────────────────────────────┘
                          │ gRPC (HTTP/2)
                          │ Load Balancer (Envoy/Nginx)
                          │
┌─────────────────────────▼───────────────────────────────────┐
│                     Application Layer                        │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐      │
│  │ Checkout API │  │  Push API    │  │ Admin API    │      │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘      │
└─────────┼─────────────────┼─────────────────┼──────────────┘
          │                 │                 │
          ▼                 ▼                 ▼
┌─────────────────────────────────────────────────────────────┐
│                   Metadata Layer (Redis Cluster)             │
│  Shard 1  Shard 2  Shard 3  ...  Shard 50                   │
│  (Consistent hashing by slice_id)                           │
└─────────────────────────┬───────────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────────┐
│                   Object Store Layer                         │
│  S3 / GCS / Custom Object Store                             │
│  Sharded by file hash prefix                                │
└─────────────────────────────────────────────────────────────┘
```

### Components

#### Load Balancer
- **Envoy** or **Nginx** with gRPC support
- HTTP/2 termination
- Connection pooling
- Circuit breaking
- Request routing (by slice_id for sharding)

#### Application Servers
- **gRPC server** (Go/Java/Python/C++)
- Stateless (no session state)
- Horizontal scaling (auto-scaling group)
- Health checks
- Metrics collection (Prometheus)

#### Redis Cluster
- **Redis Sentinel** for high availability
- 3 master nodes per shard (replication)
- Automatic failover
- AOF persistence
- RDB snapshots

#### Object Store
- **S3** with versioning enabled
- Lifecycle policies (IA, Glacier)
- Multi-region replication
- CloudFront CDN for global access

#### Monitoring & Observability
- **Prometheus** for metrics
- **Grafana** for dashboards
- **Jaeger** for distributed tracing
- **ELK Stack** for logs
- **PagerDuty** for alerts

---

## 8. Migration Strategy

### Phase 1: MVP (3 months)
- Single Redis node (small scale)
- S3 object store
- Basic gRPC APIs
- Support 100 slices, 10K files

### Phase 2: Scale-up (6 months)
- Redis cluster (10 nodes)
- Sharding by slice_id
- Streaming APIs
- Support 10K slices, 10M files
- Millions of commits/day

### Phase 3: Production (12 months)
- Redis cluster (50-100 nodes)
- Global deployment (multi-region)
- Advanced features (collaboration, real-time)
- Support 1M slices, 1B files

---

## 9. Security Considerations

### Authentication
- JWT tokens with expiration
- Refresh token mechanism
- Rate limiting per user/token

### Authorization
- Slice-level permissions (read/write/admin)
- Team-based access control
- Audit logging for all operations

### Data Security
- Encryption at rest (S3 server-side encryption)
- Encryption in transit (TLS 1.3)
- Secrets management (Hashicorp Vault, AWS KMS)

### Network Security
- VPC isolation (private subnets)
- Security groups (restrict access)
- DDoS protection (Cloudflare, AWS Shield)

---

## 10. Disaster Recovery

### Backup Strategy
- Daily snapshots of Redis cluster
- S3 versioning (object store)
- Database backups (if using separate DB for metadata)
- Offsite backups (cross-region replication)

### RTO/RPO Targets
- **RTO (Recovery Time Objective):** 4 hours
- **RPO (Recovery Point Objective):** 1 hour

### Recovery Procedure
1. Failover to standby Redis cluster
2. Restore from latest snapshot
3. Rebuild from object store if needed
4. Verify data integrity
5. Cut traffic to recovered cluster

---

## Conclusion

This architecture provides a scalable, performant source control system that:

✅ Scales to billions of files and millions of commits per day
✅ Supports slice-based versioning with change list workflow
✅ Provides sub-second performance for checkout and changeset merge operations
✅ Uses efficient data structures (Redis sets, Merkle trees)
✅ Leverages proven technologies (Redis, S3, gRPC)
✅ Balances consistency (conflict detection on merge) with performance (batch merge)
✅ Designed for horizontal scaling and high availability

The system is production-ready for large-scale code repositories and can be deployed incrementally starting from an MVP and scaling to full production capacity.