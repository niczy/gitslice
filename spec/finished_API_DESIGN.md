# API Design: gRPC Protocol

## Executive Summary

The current API surface is defined in three protobuf services: SliceService for slice workflows, AdminService for admin workflows (including conflict management), and FileService for read-only file browsing over HTTP via gRPC-Gateway. See the proto sources for the authoritative definitions in `proto/slice/slice_service.proto`, `proto/admin/admin_service.proto`, and `proto/file/file_service.proto`.

## Overview

The API is divided into three services:
- **SliceService**: Core operations for slice management and change list workflows
- **AdminService**: Administrative operations for batch merging, monitoring, and global state management
- **FileService**: Read-only file browsing for a slice (exposed via gRPC-Gateway HTTP routes)

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

  // Get root slice info
  rpc GetRootSlice(GetRootSliceRequest) returns (GetRootSliceResponse);

  // Create a new slice from an existing folder
  rpc CreateSliceFromFolder(CreateSliceFromFolderRequest) returns (CreateSliceFromFolderResponse);

  // Stream checkout for large slices (server streaming)
  rpc StreamCheckoutSlice(CheckoutRequest) returns (stream CheckoutChunk);

  // Stream changeset creation (client streaming)
  rpc StreamCreateChangeset(stream ChangesetChunk) returns (CreateChangesetResponse);
}
```

### Admin Service: AdminService

```protobuf
service AdminService {
  // Trigger batch merge to global
  rpc BatchMerge(BatchMergeRequest) returns (BatchMergeResponse);

  // Get current conflicts across slices
  rpc GetConflicts(ConflictsRequest) returns (ConflictsResponse);

  // Resolve a conflict by choosing a preferred slice
  rpc ResolveConflict(ResolveConflictRequest) returns (ResolveConflictResponse);

  // Get global state
  rpc GetGlobalState(GlobalStateRequest) returns (GlobalStateResponse);

  // List slices stored in the system
  rpc ListSlices(ListSlicesRequest) returns (ListSlicesResponse);

  // Stream conflict updates
  rpc WatchConflicts(WatchConflictsRequest) returns (stream ConflictUpdate);
}
```

HTTP (gRPC-Gateway):
- `GET /v1/slices` lists slice definitions stored in the metadata layer.

### File Service: FileService

```protobuf
service FileService {
  // List entries within a slice at a given path.
  rpc ListEntries(ListEntriesRequest) returns (ListEntriesResponse);

  // Fetch the contents of a file within a slice.
  rpc GetFile(GetFileRequest) returns (GetFileResponse);
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
  string content_url = 5;  // Reserved for future object-store URLs
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
  MERGE_STATUS_SUCCESS = 0;
  MERGE_STATUS_CONFLICT = 1;
  MERGE_STATUS_ERROR = 2;
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
  REBASE_STATUS_SUCCESS = 0;
  REBASE_STATUS_CONFLICT = 1;
  REBASE_STATUS_NEEDS_MERGE = 2;
  REBASE_STATUS_ERROR = 3;
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

#### Get Root Slice

```protobuf
message GetRootSliceRequest {}

message GetRootSliceResponse {
  string slice_id = 1;
  string commit_hash = 2;
}
```

#### Create Slice From Folder

```protobuf
message CreateSliceFromFolderRequest {
  string parent_slice_id = 1;
  string folder_path = 2;
  string new_slice_id = 3;
  string name = 4;
  string description = 5;
}

message CreateSliceFromFolderResponse {
  string slice_id = 1;
  string status = 2;
  repeated string files = 3;
}
```

### Admin Operations

#### Batch Merge

```protobuf
message BatchMergeRequest {
  int32 max_slices = 1;
}

message BatchMergeResponse {
  string global_commit_hash = 1;
  int32 merged_slice_count = 2;
  repeated string merged_slice_ids = 3;
  int64 timestamp = 4;
}
```

#### Get Conflicts

```protobuf
message ConflictsRequest {
  string slice_id = 1;  // Optional filter; omit to list all conflicts
}

message ConflictsResponse {
  repeated Conflict conflicts = 1;
  int32 total_conflicts = 2;
}
```

#### Resolve Conflict

```protobuf
message ResolveConflictRequest {
  string file_id = 1;
  string preferred_slice_id = 2;
}

message ResolveConflictResponse {
  Conflict resolved_conflict = 1;
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

#### Watch Conflicts

```protobuf
message WatchConflictsRequest {
  string slice_id = 1;
}

message ConflictUpdate {
  repeated Conflict new_conflicts = 1;
  repeated Conflict resolved_conflicts = 2;
}
```

## Streaming Operations

### Checkout Large Slices (Server Streaming)

```protobuf
rpc StreamCheckoutSlice(CheckoutRequest) returns (stream CheckoutChunk);

message CheckoutChunk {
  oneof chunk {
    SliceManifest manifest = 1;
    FileContent file = 2;
  }
}
```

**Benefit:** Stream files incrementally instead of loading all into memory.

**Prototype status:** Defined in the proto but not yet implemented on the server.

**Implementation Notes:**
- Server streams manifest first, then files one by one
- Client can start processing files immediately
- Reduces server memory usage for large slices
- Supports cancellation mid-stream

### Create Changeset (Client Streaming)

```protobuf
rpc StreamCreateChangeset(stream ChangesetChunk) returns (CreateChangesetResponse);

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

**Prototype status:** Defined in the proto but not yet implemented on the server.

**Implementation Notes:**
- Client streams metadata first, then objects
- Server validates metadata before accepting objects
- Supports large file uploads without memory issues
- Server can reject invalid changesets early

### Real-time Conflict Updates (Server Streaming)

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

## Implementation Notes: Prototype Status

The current prototype uses an in-memory storage backend and implements the core unary RPCs in the slice and admin services. The storage and service logic lives in `internal/storage` and `services`, respectively. For details on how data is stored and locked, see [`internal/storage/memory.go`](../internal/storage/memory.go) and the service handlers in [`services/slice/server.go`](../services/slice/server.go) and [`services/admin/server.go`](../services/admin/server.go).

Key behaviors in the prototype:
- **No object store integration:** File contents and metadata are stored in-memory; `content_url` is unused.
- **Authentication + authorization enforced:** Clients send identity via `Authorization: User <username>` (HTTP) or gRPC metadata. Slice/Admin RPCs enforce slice-level access (unauthenticated -> `Unauthenticated`, unauthorized -> `PermissionDenied`). The current HTTP login is a lightweight “fake” user provisioning step; no real OAuth integration yet.
- **Streaming RPCs are defined but not implemented:** `StreamCheckoutSlice` and `StreamCreateChangeset` return `UNIMPLEMENTED` until server support is added.
- **Conflict tracking is in-memory:** Locks and conflict ownership are managed via `InMemoryStorage`.
- **FileService gateway:** the core server hosts the FileService gRPC-Gateway on `:8080` for HTTP access to `ListEntries` and `GetFile`.

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
| `gs init <metadata-toml-path>` | N/A (local only) |
| `gs slice checkout <metadata-toml-path>` | `CheckoutSlice` |
| `gs changeset create` | `CreateChangeset` |
| `gs changeset review <id>` | `ReviewChangeset` |
| `gs changeset merge <id>` | `MergeChangeset` |
| `gs changeset rebase <id>` | `RebaseChangeset` |
| `gs changeset list` | `ListChangesets` |
| `gs log` | `GetSliceCommits` |
| `gs status` | `GetSliceState` |
| `gs root` | `GetRootSlice` |
| `gs fork <new-slice> <folder>` | `CreateSliceFromFolder` |
| `gs conflict list` | `GetConflicts` (admin) |
| `gs conflict resolve <file>` | `ResolveConflict` (admin) |

## References

- [PRODUCT_VISION.md](./PRODUCT_VISION.md) - Product requirements and goals
- [DATA_MODEL.md](./DATA_MODEL.md) - Data structures and relationships
- [ALGORITHMS.md](./ALGORITHMS.md) - Core algorithms and workflows
- [CLI_DESIGN.md](./CLI_DESIGN.md) - CLI commands and user workflows
- [ARCHITECTURE.md](./ARCHITECTURE.md) - System architecture and components
