# API Design: gRPC Protocol

## Overview

This document defines the gRPC API for the Gitslice service. The API is divided into two main services:
- **SliceService**: Core operations for slice management and change list workflows
- **AdminService**: Administrative operations for batch merging, monitoring, and global state management

## Service Definitions

### Core Service: SliceService

```protobuf
syntax = "proto3";

package slice.v1;

service SliceService {
  // Checkout slice for editing
  rpc CheckoutSlice(CheckoutRequest) returns (CheckoutResponse);

  // Create a new change list
  rpc CreateChangeset(CreateChangesetRequest) returns (CreateChangesetResponse);

  // Review a change list (check against slice head)
  rpc ReviewChangeset(ReviewChangesetRequest) returns (ReviewChangesetResponse);

  // Merge change list into slice (with conflict detection)
  rpc MergeChangeset(MergeChangesetRequest) returns (MergeChangesetResponse);

  // Rebase change list on new slice head
  rpc RebaseChangeset(RebaseChangesetRequest) returns (RebaseChangesetResponse);

  // Get slice commit history
  rpc GetSliceCommits(CommitHistoryRequest) returns (CommitHistoryResponse);

  // Get current slice state
  rpc GetSliceState(StateRequest) returns (StateResponse);

  // List pending change lists for a slice
  rpc ListChangesets(ListChangesetsRequest) returns (ListChangesetsResponse);
}
```

### Admin Service: AdminService

```protobuf
service AdminService {
  // Trigger batch merge to global
  rpc BatchMerge(BatchMergeRequest) returns (BatchMergeResponse);

  // List all active slices
  rpc ListSlices(ListSlicesRequest) returns (ListSlicesResponse);

  // Get current conflicts across slices
  rpc GetConflicts(ConflictsRequest) returns (ConflictsResponse);

  // Get global state
  rpc GetGlobalState(GlobalStateRequest) returns (GlobalStateResponse);
}
```

## Message Definitions

### Core Operations

#### Checkout Slice

```protobuf
message CheckoutRequest {
  string slice_id = 1;
  string commit_hash = 2;  // "HEAD" for latest, specific hash for historical
}

message CheckoutResponse {
  SliceManifest manifest = 1;
  repeated FileContent files = 2;
}

message SliceManifest {
  string commit_hash = 1;
  repeated FileMetadata file_metadata = 2;
}

message FileMetadata {
  string file_id = 1;
  string path = 2;
  int64 size = 3;
  string hash = 4;
  string content_url = 5;  // Presigned URL for object fetching
}

message FileContent {
  string file_id = 1;
  bytes content = 2;
}
```

#### Create Changeset

```protobuf
message CreateChangesetRequest {
  string slice_id = 1;
  string base_commit_hash = 2;
  repeated Object objects = 3;  // blobs, trees
  repeated string modified_files = 4;
  string author = 5;
  string message = 6;
}

message CreateChangesetResponse {
  string changeset_id = 1;
  string changeset_hash = 2;
  ChangesetStatus status = 3;
}

message Object {
  ObjectType type = 1;
  string hash = 2;
  bytes data = 3;
}

enum ObjectType {
  BLOB = 0;
  TREE = 1;
  COMMIT = 2;
  SLICE_DEF = 3;
  CHANGESET = 4;
}
```

#### Review Changeset

```protobuf
message ReviewChangesetRequest {
  string changeset_id = 1;
}

message ReviewChangesetResponse {
  ChangesetInfo changeset = 1;
  DiffSummary diff = 2;
  ReviewStatus review_status = 3;
  repeated string warnings = 4;
}

message DiffSummary {
  int32 files_added = 1;
  int32 files_modified = 2;
  int32 files_deleted = 3;
  int64 lines_added = 4;
  int64 lines_removed = 5;
}

enum ReviewStatus {
  READY_FOR_MERGE = 0;
  NEEDS_REBASE = 1;
  HAS_CONFLICTS = 2;
}
```

#### Merge Changeset

```protobuf
message MergeChangesetRequest {
  string changeset_id = 1;
}

message MergeChangesetResponse {
  MergeStatus status = 1;
  string new_commit_hash = 2;
  string changeset_id = 3;
  repeated Conflict conflicts = 4;
}

enum MergeStatus {
  SUCCESS = 0;
  CONFLICT = 1;
  ERROR = 2;
}

message Conflict {
  string file_id = 1;
  repeated string conflicting_slice_ids = 2;
}
```

#### Rebase Changeset

```protobuf
message RebaseChangesetRequest {
  string changeset_id = 1;
}

message RebaseChangesetResponse {
  RebaseStatus status = 1;
  string new_base_commit_hash = 2;
  repeated string slice_commits_to_apply = 3;
  repeated Conflict conflicts = 4;
}

enum RebaseStatus {
  SUCCESS = 0;
  CONFLICT = 1;
  NEEDS_MERGE = 2;
  ERROR = 3;
}
```

#### List Changesets

```protobuf
message ListChangesetsRequest {
  string slice_id = 1;
  ChangesetStatus status_filter = 2;  // Optional filter
  int32 limit = 3;
}

message ListChangesetsResponse {
  repeated ChangesetInfo changesets = 1;
}

message ChangesetInfo {
  string changeset_id = 1;
  string changeset_hash = 2;
  string slice_id = 3;
  string base_commit_hash = 4;
  repeated string modified_files = 5;
  ChangesetStatus status = 6;
  string author = 7;
  int64 created_at = 8;
  int64 merged_at = 9;
  string message = 10;
}

enum ChangesetStatus {
  PENDING = 0;
  APPROVED = 1;
  REJECTED = 2;
  MERGED = 3;
}
```

#### Get Slice Commits

```protobuf
message CommitHistoryRequest {
  string slice_id = 1;
  int64 limit = 2;
  string from_commit_hash = 3;
}

message CommitHistoryResponse {
  repeated CommitInfo commits = 1;
}

message CommitInfo {
  string commit_hash = 1;
  int64 timestamp = 2;
  string parent_hash = 3;
  string message = 4;
}
```

#### Get Slice State

```protobuf
message StateRequest {
  string slice_id = 1;
}

message StateResponse {
  string latest_commit_hash = 1;
  repeated string modified_files = 2;
  int64 last_modified = 3;
}
```

### Admin Operations

#### Batch Merge

```protobuf
message BatchMergeRequest {
  optional int32 max_slices = 1;  // Optional: limit batch size
}

message BatchMergeResponse {
  string global_commit_hash = 1;
  int32 merged_slice_count = 2;
  repeated string merged_slice_ids = 3;
  int64 timestamp = 4;
}
```

#### List Slices

```protobuf
message ListSlicesRequest {
  int32 limit = 1;
  int32 offset = 2;
}

message ListSlicesResponse {
  repeated SliceInfo slices = 1;
}

message SliceInfo {
  string slice_id = 1;
  string latest_commit_hash = 2;
  int32 modified_files_count = 3;
  int64 last_modified = 4;
}
```

#### Get Conflicts

```protobuf
message ConflictsRequest {
  optional string slice_id = 1;  // Optional: filter by slice
}

message ConflictsResponse {
  repeated Conflict conflicts = 1;
  int32 total_conflicts = 2;
}
```

#### Get Global State

```protobuf
message GlobalStateRequest {
  bool include_history = 1;
}

message GlobalStateResponse {
  string global_commit_hash = 1;
  int64 timestamp = 2;
  repeated GlobalCommitHistory history = 3;
}

message GlobalCommitHistory {
  string commit_hash = 1;
  int64 timestamp = 2;
  repeated string merged_slice_ids = 3;
}
```

## Streaming Operations

### Checkout Large Slices (Server Streaming)

```protobuf
rpc CheckoutSlice(CheckoutRequest) returns (stream CheckoutChunk);

message CheckoutChunk {
  oneof chunk {
    SliceManifest manifest = 1;
    FileContent file = 2;
  }
}
```

**Benefit:** Stream files incrementally instead of loading all into memory.

**Implementation Notes:**
- Server streams manifest first, then files one by one
- Client can start processing files immediately
- Reduces server memory usage for large slices
- Supports cancellation mid-stream

### Create Changeset (Client Streaming)

```protobuf
rpc CreateChangeset(stream ChangesetChunk) returns (CreateChangesetResponse);

message ChangesetChunk {
  oneof chunk {
    ChangesetMetadata metadata = 1;  // slice_id, base_commit_hash, etc.
    Object object = 2;
  }
}

message ChangesetMetadata {
  string slice_id = 1;
  string base_commit_hash = 2;
  string author = 3;
  string message = 4;
}
```

**Benefit:** Stream large change lists with many files without buffering.

**Implementation Notes:**
- Client streams metadata first, then objects
- Server validates metadata before accepting objects
- Supports large file uploads without memory issues
- Server can reject invalid changesets early

### Real-time Conflict Updates (Bidirectional Streaming)

```protobuf
rpc WatchConflicts(WatchConflictsRequest) returns (stream ConflictUpdate);

message ConflictUpdate {
  repeated Conflict new_conflicts = 1;
  repeated Conflict resolved_conflicts = 2;
}
```

**Benefit:** Real-time conflict notifications for collaboration.

**Implementation Notes:**
- Client subscribes to conflict updates for specific slices
- Server pushes updates as conflicts are detected/resolved
- Supports multiple concurrent watchers
- Heartbeat messages to detect stale connections

## Implementation Notes: Service Interface

### SliceService Implementation

#### CheckoutSlice

**Algorithm:**
```
1. Validate slice_id exists
2. Fetch slice manifest from metadata layer
3. Generate presigned URLs for all files in manifest
4. Stream manifest to client
5. Stream files to client (optional, can use direct S3 access)
```

**Error Handling:**
- `NOT_FOUND`: slice_id does not exist
- `INVALID_ARGUMENT`: commit_hash not found
- `UNAVAILABLE`: metadata layer unreachable

**Performance:**
- O(1) metadata lookup
- O(N) file URL generation (N = number of files)
- Streaming reduces memory footprint

#### CreateChangeset

**Algorithm:**
```
1. Validate slice_id and base_commit_hash
2. Validate user has write access to slice
3. Calculate change list hash from objects
4. Create unique changeset_id
5. Store objects in object store
6. Store changeset metadata in metadata layer
7. Return changeset_id and hash
```

**Error Handling:**
- `NOT_FOUND`: slice_id or base_commit_hash does not exist
- `PERMISSION_DENIED`: user lacks write access
- `INVALID_ARGUMENT`: invalid objects or metadata

**Performance:**
- O(N) object storage (N = number of objects)
- O(1) metadata write
- Async object upload for performance

#### ReviewChangeset

**Algorithm:**
```
1. Fetch changeset metadata
2. Fetch current slice state
3. Check for file-level conflicts
4. Calculate diff summary
5. Return review status and warnings
```

**Error Handling:**
- `NOT_FOUND`: changeset_id does not exist
- `FAILED_PRECONDITION`: changeset already merged

**Performance:**
- O(M) conflict check (M = number of modified files)
- O(L) diff calculation (L = line count)

#### MergeChangeset

**Algorithm:**
```
1. Validate user permissions
2. Fetch current slice state
3. Run conflict detection
4. On success:
   a. Create new commit
   b. Update slice state
   c. Add commit to batch merge queue
5. On conflict:
   a. Return error with conflict details
6. Update changeset status
```

**Error Handling:**
- `PERMISSION_DENIED`: user lacks merge permissions
- `ALREADY_EXISTS`: conflicting changeset merged first
- `FAILED_PRECONDITION`: conflicts detected

**Performance:**
- O(M) conflict detection
- O(1) commit creation
- O(1) slice state update

#### RebaseChangeset

**Algorithm:**
```
1. Validate slice_id and changeset_id
2. Check for file-level conflicts with slice commits since base_commit
3. On conflict:
   a. Return error with rebase instructions
4. On success:
   a. Update changeset base commit
   b. Return new base and commits to apply
```

**Error Handling:**
- `NOT_FOUND`: changeset_id does not exist
- `FAILED_PRECONDITION`: conflicts detected during rebase

**Performance:**
- O(K) conflict check (K = commits since base)
- O(1) metadata update

#### ListChangesets

**Algorithm:**
```
1. Query metadata layer for changesets
2. Apply filters (slice_id, status, limit)
3. Return paginated results
```

**Performance:**
- O(N) query (N = total changesets)
- O(L) result set (L = limit)
- Indexed queries for performance

#### GetSliceCommits

**Algorithm:**
```
1. Fetch commit history from metadata layer
2. Apply pagination (limit, from_commit_hash)
3. Return commits in reverse chronological order
```

**Performance:**
- O(N) query (N = commit history length)
- O(L) result set (L = limit)

#### GetSliceState

**Algorithm:**
```
1. Fetch slice state from metadata layer
2. Return current commit hash and metadata
```

**Performance:**
- O(1) lookup

### AdminService Implementation

#### BatchMerge

**Algorithm:**
```
1. Fetch all active slices
2. Merge slice trees into global tree
3. Create global commit
4. Update global state
5. Return merge results
```

**Error Handling:**
- `UNAVAILABLE`: object store or metadata layer unreachable
- `FAILED_PRECONDITION`: conflicts detected (should not happen)

**Performance:**
- O(N * M) tree merge (N = slices, M = avg tree size)
- O(N) global state update

#### ListSlices

**Algorithm:**
```
1. Query metadata layer for all slices
2. Apply pagination (limit, offset)
3. Return slice metadata
```

**Performance:**
- O(N) query (N = total slices)
- O(L) result set (L = limit)

#### GetConflicts

**Algorithm:**
```
1. Query conflict index from metadata layer
2. Filter by slice_id if provided
3. Return conflict list
```

**Performance:**
- O(C) query (C = total conflicts)
- O(K) result set (K = conflicts matching filter)

#### GetGlobalState

**Algorithm:**
```
1. Fetch global state from metadata layer
2. Optionally fetch commit history
3. Return global metadata
```

**Performance:**
- O(1) lookup
- O(H) history fetch (H = history length)

## Error Handling

### Standard gRPC Error Codes

| Code | Description | Usage |
|------|-------------|-------|
| `OK` | Success | Operation completed successfully |
| `NOT_FOUND` | Resource not found | Invalid slice_id, changeset_id, commit_hash |
| `PERMISSION_DENIED` | Authorization failed | User lacks permissions |
| `INVALID_ARGUMENT` | Invalid request | Malformed request, invalid parameters |
| `ALREADY_EXISTS` | Resource already exists | Duplicate changeset_id |
| `FAILED_PRECONDITION` | Precondition failed | Conflicts detected, invalid state |
| `UNAVAILABLE` | Service unavailable | Metadata or object store unreachable |
| `INTERNAL` | Internal error | Server-side error |
| `DEADLINE_EXCEEDED` | Timeout | Operation took too long |

### Error Response Format

```protobuf
message ErrorDetail {
  string code = 1;
  string message = 2;
  map<string, string> details = 3;
}
```

## Advantages of gRPC over REST

### 1. Performance
- Binary serialization (Protocol Buffers) - 5-10x faster than JSON
- HTTP/2 multiplexing - parallel requests over single connection
- Built-in compression (gzip)

### 2. Type Safety
- Strongly typed message definitions
- Compile-time type checking
- Auto-generated client/server code

### 3. Streaming Support
- Bidirectional streaming for large file transfers
- Server-side streaming for commit history pagination
- Client-side streaming for batch uploads

### 4. Better for High-Throughput Operations
- Push operations with many objects
- Checkout with large file sets
- Batch merge operations

## CLI to API Mapping

See [CLI_DESIGN.md](./CLI_DESIGN.md) for detailed command-to-API mapping.

### Quick Reference

| CLI Command | API Method |
|-------------|------------|
| `gitslice init` | N/A (local only) |
| `gitslice checkout <slice_id>` | `CheckoutSlice` |
| `gitslice push` | `CreateChangeset` |
| `gitslice review` | `ReviewChangeset` |
| `gitslice merge` | `MergeChangeset` |
| `gitslice rebase` | `RebaseChangeset` |
| `gitslice log` | `GetSliceCommits` |
| `gitslice status` | `GetSliceState` |
| `gitslice list-changesets` | `ListChangesets` |
| `gitslice batch-merge` | `BatchMerge` (admin) |
| `gitslice list-slices` | `ListSlices` (admin) |
| `gitslice conflicts` | `GetConflicts` (admin) |

## References

- [PRODUCT_VISION.md](./PRODUCT_VISION.md) - Product requirements and goals
- [DATA_MODEL.md](./DATA_MODEL.md) - Data structures and relationships
- [ALGORITHMS.md](./ALGORITHMS.md) - Core algorithms and workflows
- [CLI_DESIGN.md](./CLI_DESIGN.md) - CLI commands and user workflows
- [ARCHITECTURE.md](./ARCHITECTURE.md) - System architecture and components
