# Data Models

## Overview

This document describes all data models used in the slice-based version control system, including:
- Object store models (immutable data)
- Metadata models (state tracking)
- Conflict models
- Manifest models
- Changeset models
- Batch merge models

All models are defined using Protocol Buffers (protobuf) for serialization and communication.

---

## 1. Object Store Models

### High-Level Concepts

The object store stores all immutable data objects using content-addressable storage (CAS).

```
Objects:
- Blob: File content (addressed by hash)
  - hash = SHA256(content)
  - Stores raw file bytes

- Tree: Directory structure
  - hash = SHA256(serialized entries)
  - Each entry: {name, mode, child_hash}
  - Forms Merkle trees for efficient change detection

- Commit: Snapshot of a slice
  - hash = SHA256(tree_hash + parent_hash + metadata)
  - Contains: {tree_hash, parent_hash, slice_id, author, timestamp, message}

- SliceDef: Slice definition
  - hash = SHA256(slice_name + file_set + parent_hash)
  - Contains: {name, file_set: Set<file_id>, parent_slice_def_hash}
  - Versioned - slice definitions can evolve

- ChangeList: Collection of modifications to be merged into a slice
  - hash = SHA256(slice_id + base_commit + modified_files + timestamps)
  - Contains: {slice_id, base_commit_hash, modified_files: Set<file_id>, objects: [blob_hash, tree_hash], author, timestamp, status}
  - Status: pending, approved, rejected, merged
  - Can be re-based on new slice head
  - Conflict-free guarantees when created, re-checked on merge
```

### Protobuf Definitions

```protobuf
syntax = "proto3";

package object_store.v1;

// Blob represents file content
message Blob {
  string hash = 1;                    // SHA256(content)
  bytes content = 2;                   // Raw file content
  int64 size = 3;                      // Size in bytes
}

// TreeEntry represents a single entry in a tree (directory)
message TreeEntry {
  string name = 1;                      // File/directory name
  string mode = 2;                      // File mode (e.g., "100644" for file, "040000" for dir)
  string hash = 3;                      // Hash of child blob or tree
}

// Tree represents a directory structure
message Tree {
  string hash = 1;                      // SHA256(serialized entries)
  repeated TreeEntry entries = 2;        // Directory contents
  int64 size = 3;                       // Size in bytes
}

// Commit represents a snapshot of a slice
message Commit {
  string hash = 1;                      // SHA256(tree_hash + parent_hash + metadata)
  string tree_hash = 2;                 // Root tree hash
  string parent_hash = 3;                // Parent commit hash (empty string for initial commit)
  string slice_id = 4;                   // Slice this commit belongs to
  string author = 5;                     // Author identifier
  int64 timestamp = 6;                   // Unix timestamp in nanoseconds
  string message = 7;                     // Commit message
  repeated string modified_files = 8;     // List of file paths modified in this commit
}

// SliceDef represents a slice definition (which files belong to a slice)
message SliceDef {
  string hash = 1;                      // SHA256(slice_name + file_set + parent_hash)
  string slice_id = 2;                   // Unique slice identifier
  string name = 3;                       // Human-readable slice name
  repeated string file_patterns = 4;      // File/folder patterns (glob patterns)
  repeated string file_ids = 5;           // Explicit file IDs
  string parent_slice_def_hash = 6;       // Parent slice definition (for versioning)
  int64 created_at = 7;                 // Creation timestamp
  string creator = 8;                     // Creator identifier
  map<string, string> metadata = 9;       // Additional metadata (key-value pairs)
}

// ChangeList represents a collection of modifications to be merged into a slice
message ChangeList {
  string hash = 1;                      // SHA256(slice_id + base_commit + modified_files + timestamps)
  string changeset_id = 2;               // Unique changeset identifier
  string slice_id = 3;                   // Target slice
  string base_commit_hash = 4;           // Base commit this changeset is based on
  repeated string modified_files = 5;     // List of file IDs modified
  repeated Object objects = 6;            // Objects (blobs, trees) in this changeset
  ChangesetStatus status = 7;             // Current status
  string author = 8;                     // Author identifier
  int64 created_at = 9;                 // Creation timestamp
  int64 merged_at = 10;                 // Merge timestamp (if merged)
  string message = 11;                    // Changeset description
  repeated string reviewer_ids = 12;       // List of reviewers
  map<string, string> metadata = 13;      // Additional metadata
}

// Object is a union type for all object store objects
message Object {
  ObjectType type = 1;                   // Object type
  string hash = 2;                       // Object hash
  bytes data = 3;                        // Serialized object data (Blob, Tree, Commit, etc.)
  int64 size = 4;                        // Object size in bytes
}

enum ObjectType {
  BLOB = 0;                             // File content
  TREE = 1;                             // Directory structure
  COMMIT = 2;                            // Slice commit
  SLICE_DEF = 3;                         // Slice definition
  CHANGESET = 4;                         // Change list
  MANIFEST = 5;                          // Slice manifest (precomputed file list)
}
```

---

## 2. Metadata Models

### Protobuf Definitions

```protobuf
syntax = "proto3";

package metadata.v1;

import "object_store/v1/objects.proto";

// SliceState tracks the current state of a slice
message SliceState {
  string slice_id = 1;                   // Slice identifier
  string latest_commit_hash = 2;         // Latest commit hash
  repeated string modified_files = 3;     // Files currently modified by this slice
  int64 last_modified = 4;              // Last modification timestamp
  string last_merged_commit_hash = 5;     // Last commit merged to global state
  int32 pending_changesets = 6;         // Number of pending changesets
  SliceHealthStatus health_status = 7;    // Health indicator
}

enum SliceHealthStatus {
  HEALTHY = 0;                         // Normal operation
  CONFLICT_DETECTED = 1;                // Conflicts pending resolution
  MERGE_IN_PROGRESS = 2;                // Batch merge in progress
  STALE = 3;                            // No recent activity
}

// CommitMetadata stores additional metadata for commits
message CommitMetadata {
  string commit_hash = 1;                // Commit identifier
  string tree_hash = 2;                  // Root tree hash
  string parent_hash = 3;                // Parent commit hash
  string slice_id = 4;                   // Slice identifier
  string author = 5;                     // Author identifier
  int64 timestamp = 6;                  // Unix timestamp
  string message = 7;                    // Commit message
  repeated string file_hashes = 8;        // All file hashes in this commit
  int32 file_count = 9;                 // Number of files
  int64 tree_size = 10;                 // Total size in bytes
  string global_commit_hash = 11;         // Global commit this was merged into (if any)
}

// GlobalState represents the global repository state
message GlobalState {
  string global_commit_hash = 1;          // Current global commit hash
  int64 timestamp = 2;                   // Last update timestamp
  int32 merged_slice_count = 3;          // Total slices merged
  repeated string active_slice_ids = 4;    // Slices with unmerged changes
  repeated string merged_slice_ids = 5;     // Slices fully merged
  repeated GlobalCommitHistory history = 6;  // Global commit history
}

// GlobalCommitHistory represents a single global commit
message GlobalCommitHistory {
  string commit_hash = 1;                // Global commit hash
  int64 timestamp = 2;                   // Commit timestamp
  repeated string merged_slice_ids = 3;    // Slices merged in this commit
  int32 merged_changeset_count = 4;       // Total changesets merged
  string parent_hash = 5;                 // Parent global commit hash
}
```

---

## 3. Conflict Models

### Protobuf Definitions

```protobuf
syntax = "proto3";

package conflict.v1;

// Conflict represents a file-level conflict
message Conflict {
  string file_id = 1;                    // Conflicting file identifier
  string file_path = 2;                  // File path (for display)
  repeated string conflicting_slice_ids = 3;  // Slices that have modified this file
  string changeset_id = 4;               // Changeset that detected the conflict
  int64 detected_at = 5;                 // Detection timestamp
  ConflictSeverity severity = 6;          // Conflict severity
  string resolution_hint = 7;            // Optional resolution hint
  repeated FileModification modifications = 8;  // Conflicting modifications
}

// FileModification tracks how a file was modified
message FileModification {
  string slice_id = 1;                   // Slice that made the modification
  string commit_hash = 2;                // Commit containing the modification
  string old_hash = 3;                   // Previous file hash (if any)
  string new_hash = 4;                   // New file hash
  int64 timestamp = 5;                   // Modification timestamp
  string author = 6;                     // Author who made the change
  ModificationType modification_type = 7;  // Type of modification
}

enum ModificationType {
  ADD = 0;                              // File added
  MODIFY = 1;                           // File modified
  DELETE = 2;                           // File deleted
  RENAME = 3;                           // File renamed
}

enum ConflictSeverity {
  LOW = 0;                              // Can be auto-merged
  MEDIUM = 1;                           // Requires manual review
  HIGH = 2;                             // Requires manual resolution
  CRITICAL = 3;                          // Blocks merge until resolved
}
```

---

## 4. Manifest Models

### Protobuf Definitions

```protobuf
syntax = "proto3";

package manifest.v1;

// SliceManifest represents a precomputed file manifest for a slice commit
message SliceManifest {
  string commit_hash = 1;                // Commit this manifest is for
  repeated FileMetadata file_metadata = 2;  // List of file metadata
  int64 total_size = 3;                 // Total size in bytes
  int32 file_count = 4;                 // Total number of files
  int64 generated_at = 5;               // Manifest generation timestamp
  string tree_hash = 6;                  // Root tree hash
}

// FileMetadata represents metadata for a single file
message FileMetadata {
  string file_id = 1;                    // File identifier (hash)
  string path = 2;                       // File path (relative to repo root)
  int64 size = 3;                        // File size in bytes
  string hash = 4;                       // Content hash (SHA256)
  string content_url = 5;                 // Presigned URL for object fetching
  string mode = 6;                       // File mode (permissions)
  int64 last_modified = 7;                // Last modification timestamp
  string commit_hash = 8;                 // Last commit that modified this file
}

// DiffSummary represents a summary of changes between two states
message DiffSummary {
  int32 files_added = 1;                 // Number of files added
  int32 files_modified = 2;              // Number of files modified
  int32 files_deleted = 3;               // Number of files deleted
  int32 files_renamed = 4;               // Number of files renamed
  int64 lines_added = 5;                  // Number of lines added
  int64 lines_removed = 6;                // Number of lines removed
  int64 bytes_added = 7;                  // Number of bytes added
  int64 bytes_removed = 8;                // Number of bytes removed
  repeated DiffEntry diff_entries = 9;     // Detailed diff entries (optional)
}

// DiffEntry represents a single file diff
message DiffEntry {
  string file_path = 1;                  // File path
  DiffType type = 2;                     // Type of change
  string old_hash = 3;                   // Old file hash (if any)
  string new_hash = 4;                   // New file hash (if any)
  repeated Hunk hunks = 5;                // Diff hunks (optional, for small files)
}

enum DiffType {
  ADDED = 0;                            // File was added
  MODIFIED = 1;                          // File was modified
  DELETED = 2;                           // File was deleted
  RENAMED = 3;                          // File was renamed
  UNCHANGED = 4;                         // File unchanged
}

// Hunk represents a diff hunk (section of file changes)
message Hunk {
  int32 old_start = 1;                   // Starting line in old file
  int32 old_lines = 2;                   // Number of lines in old hunk
  int32 new_start = 3;                   // Starting line in new file
  int32 new_lines = 4;                   // Number of lines in new hunk
  string header = 5;                      // Hunk header
  repeated string lines = 6;               // Diff lines
}
```

---

## 5. Changeset Models

### Protobuf Definitions

```protobuf
syntax = "proto3";

package changeset.v1;

// ChangesetInfo represents information about a changeset
message ChangesetInfo {
  string changeset_id = 1;               // Changeset identifier
  string changeset_hash = 2;            // Changeset hash
  string slice_id = 3;                   // Target slice
  string base_commit_hash = 4;            // Base commit
  repeated string modified_files = 5;      // Modified file IDs
  repeated string modified_file_paths = 6;  // Modified file paths (for display)
  ChangesetStatus status = 7;            // Current status
  string author = 8;                     // Author
  int64 created_at = 9;                 // Creation timestamp
  int64 updated_at = 10;                // Last update timestamp
  int64 merged_at = 11;                 // Merge timestamp (if merged)
  string message = 12;                   // Changeset message
  repeated string reviewer_ids = 13;       // Reviewers
  repeated string approver_ids = 14;       // Approvers
  int32 comment_count = 15;             // Number of comments
  int32 review_status = 16;             // Review status (number of approvals needed)
  map<string, string> metadata = 17;      // Additional metadata
}

// ChangesetReview represents a review of a changeset
message ChangesetReview {
  string review_id = 1;                  // Review identifier
  string changeset_id = 2;              // Changeset being reviewed
  string reviewer_id = 3;                // Reviewer identifier
  ReviewStatus status = 4;               // Review status
  string comment = 5;                    // Review comment
  repeated FileReview file_reviews = 6;   // File-level reviews
  int64 created_at = 7;                 // Review timestamp
  int64 updated_at = 8;                 // Last update timestamp
}

// FileReview represents a review of a specific file
message FileReview {
  string file_id = 1;                    // File identifier
  string file_path = 2;                  // File path
  ReviewStatus status = 3;               // Review status
  string comment = 4;                    // Review comment
  repeated LineReview line_reviews = 5;   // Line-level reviews
}

// LineReview represents a review of a specific line
message LineReview {
  int32 line_number = 1;                // Line number
  ReviewStatus status = 2;               // Review status
  string comment = 3;                    // Review comment
}

enum ChangesetStatus {
  PENDING = 0;                          // Awaiting review
  IN_REVIEW = 1;                         // Being reviewed
  APPROVED = 2;                          // Approved, ready for merge
  REJECTED = 3;                          // Rejected
  MERGED = 4;                           // Merged into slice
  ABANDONED = 5;                        // Abandoned by author
}

enum ReviewStatus {
  PENDING = 0;                          // Pending review
  APPROVED = 1;                          // Approved
  REQUESTED_CHANGES = 2;                  // Changes requested
  COMMENTED = 3;                        // Commented only
  DISMISSED = 4;                        // Review dismissed
}

// ChangesetStats represents statistics for a changeset
message ChangesetStats {
  int32 files_added = 1;                 // Files added
  int32 files_modified = 2;              // Files modified
  int32 files_deleted = 3;               // Files deleted
  int64 lines_added = 4;                 // Lines added
  int64 lines_removed = 5;                // Lines removed
  int32 reviewer_count = 6;               // Number of reviewers
  int32 approver_count = 7;               // Number of approvers
  int32 comment_count = 8;                // Number of comments
  int64 review_time_seconds = 9;         // Time spent in review
}
```

---

## 6. Batch Merge Models

### Protobuf Definitions

```protobuf
syntax = "proto3";

package batch_merge.v1;

// BatchMergeConfig represents configuration for batch merge
message BatchMergeConfig {
  int32 max_commits_per_batch = 1;       // Maximum commits per batch
  int64 max_batch_size_bytes = 2;         // Maximum batch size in bytes
  int64 min_merge_interval_ms = 3;        // Minimum time between merges
  int32 max_parallel_workers = 4;        // Maximum parallel merge workers
  bool auto_merge_enabled = 5;            // Enable automatic merging
  repeated string priority_slice_ids = 6;   // Priority slices to merge first
}

// BatchMergeResult represents result of a batch merge
message BatchMergeResult {
  string global_commit_hash = 1;          // Resulting global commit hash
  int32 merged_commit_count = 2;          // Number of commits merged
  repeated string merged_commit_hashes = 3;  // Merged commit hashes
  repeated string merged_slice_ids = 4;     // Merged slice IDs
  int64 timestamp = 5;                   // Merge timestamp
  int64 duration_ms = 6;                // Merge duration
  MergeStatus status = 7;                 // Merge status
  repeated string error_messages = 8;       // Error messages (if any)
  BatchMergeStats stats = 9;              // Merge statistics
}

// BatchMergeStats represents statistics for a batch merge
message BatchMergeStats {
  int64 tree_merge_time_ms = 1;          // Time spent merging trees
  int64 hash_computation_time_ms = 2;     // Time spent computing hashes
  int64 index_update_time_ms = 3;         // Time spent updating indexes
  int64 total_bytes_processed = 4;        // Total bytes processed
  int64 total_files_processed = 5;        // Total files processed
  int32 active_conflicts_before = 6;       // Active conflicts before merge
  int32 active_conflicts_after = 7;        // Active conflicts after merge
}

enum MergeStatus {
  SUCCESS = 0;                           // Merge successful
  PARTIAL_SUCCESS = 1;                   // Partial success (some commits merged)
  CONFLICT = 2;                          // Conflict detected
  ERROR = 3;                             // Error occurred
  SKIPPED = 4;                           // Merge skipped (no commits to merge)
}
```

---

## 7. Model Relationships

```
SliceDef
  └── SliceState
        └── Commit
              ├── Tree
              │     ├── TreeEntry → Blob
              │     └── TreeEntry → Tree (recursive)
              └── ChangeList
                    ├── Object (Blob/Tree)
                    └── ChangesetInfo
                          ├── ChangesetReview
                          │     ├── FileReview
                          │     │     └── LineReview
                          │     └── ChangesetStats
                          └── Conflict

GlobalState
  └── GlobalCommitHistory

SliceManifest
  └── FileMetadata

DiffSummary
  └── DiffEntry
        └── Hunk
```

---

## 8. Lifecycle Diagrams

### Changeset Lifecycle

```
PENDING → IN_REVIEW → APPROVED → MERGED
   ↓         ↓          ↓
  REJECTED  REQUESTED_CHANGES
   ↓
  ABANDONED
```

### Merge Status Lifecycle

```
PENDING → CONFLICT → SUCCESS
   ↓         ↓
  ERROR   PARTIAL_SUCCESS
   ↓
  SKIPPED
```

### Conflict Severity Levels

```
LOW: Auto-mergeable (same content)
  ↓
MEDIUM: Requires manual review (whitespace, formatting)
  ↓
HIGH: Requires manual resolution (semantic conflicts)
  ↓
CRITICAL: Blocks merge (structural conflicts)
```

---

## 9. Data Serialization Notes

### Hashing

- All objects use SHA-256 for content addressing
- Hash computed over serialized protobuf message
- Ensures deterministic hashing across implementations

### Timestamps

- All timestamps use Unix time in nanoseconds
- Stored as `int64` in protobuf
- Allows microsecond precision for operations

### Strings

- All string fields are UTF-8 encoded
- Path separators use forward slash (`/`)
- Hash strings are hex-encoded (64 characters for SHA-256)

### Collections

- `repeated` fields preserve insertion order
- `map` fields have no guaranteed ordering
- Sets modeled as `repeated` with uniqueness constraints

### Enums

- Default value is always the first enum value (0)
- Unknown enum values preserved on wire
- Proto3 `optional` pattern used for forward compatibility

---

## 10. Storage Considerations

### Object Store

- Objects stored as immutable blobs
- Addressed by hash (content-addressable)
- No need for locking or transactions
- Natural deduplication (same hash = same file)

### Metadata Store

- Indexes stored in Redis for fast lookups
- Keys prefixed with type for namespace isolation
- TTL used for temporary data (conflicts, locks)
- Persistent storage used for long-lived data

### Versioning

- Objects are immutable, never modified
- New versions create new objects with new hashes
- Old objects garbage collected when unreferenced
- Reference counting tracks object usage

### Compression

- Large objects optionally compressed (blobs > 1MB)
- Compression metadata stored in object
- Client decompresses on fetch
- Configurable per storage backend

---

## Conclusion

This data model provides a complete foundation for the slice-based version control system, with:

✅ Immutable, content-addressable object store
✅ Rich metadata for tracking state and history
✅ Comprehensive conflict detection models
✅ Flexible changeset and review workflow
✅ Efficient batch merge support
✅ Clear separation of concerns across packages

All models are designed for scalability, performance, and maintainability.