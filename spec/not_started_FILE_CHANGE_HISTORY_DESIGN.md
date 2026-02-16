# File Change History Storage Design

## Overview

This document describes the storage design for associating commit history with each file and directory entry in gitslice. The design enables efficient querying of "what changed in this file/directory over time."

## Problem Statement

Currently, gitslice tracks:
- **Commits** at the slice level (hash, parent, timestamp, message)
- **CommitSnapshots** mapping files to content hashes at a point in time

However, to answer "show me the change history for `src/main.go`", the system must:
1. Iterate through all commits
2. Load each CommitSnapshot
3. Compare file hashes between consecutive snapshots
4. Reconstruct what changed

This is O(n) in the number of commits and requires loading large snapshot objects.

## Solution: FileChangeRecord Index

### Core Data Model

```go
type FileChangeRecord struct {
    ID           string     // Unique identifier
    SliceID      string     // Which slice this change belongs to
    CommitHash   string     // Links to the commit
    Path         string     // File path (after change)
    OldPath      string     // For renames only
    ChangeType   ChangeType // add, modify, delete, rename
    OldHash      string     // Content hash before (empty for add)
    NewHash      string     // Content hash after (empty for delete)
    LinesAdded   int        // Lines added
    LinesDeleted int        // Lines removed
    Author       string     // Denormalized from commit
    Message      string     // Denormalized from commit
    Timestamp    time.Time  // Denormalized from commit
}
```

### Why Denormalize Author/Message/Timestamp?

The `Author`, `Message`, and `Timestamp` fields duplicate data from the `Commit` model. This is intentional:

1. **Query Efficiency**: File history queries can return complete records without joining to the commits table
2. **Pagination Performance**: Sorting and filtering by timestamp works on a single index
3. **Storage Trade-off**: Commit messages are typically short (<200 bytes), and the storage overhead is minimal compared to the query performance gain

### Storage Indexes

#### Primary Indexes

| Index | Key Pattern | Purpose |
|-------|-------------|---------|
| **By Path** | `slice:{sliceID}:file_changes:path:{path}` | Get history for a specific file |
| **By Commit** | `file_changes:commit:{commitHash}` | Get all changes in a commit |
| **By Directory** | `slice:{sliceID}:file_changes:dir:{dirPrefix}` | Directory-level queries |

#### In-Memory Implementation

```go
type MemoryStorage struct {
    // Existing fields...

    // File change history indexes
    fileChangesByPath   map[string][]*models.FileChangeRecord  // "sliceID:path" -> changes
    fileChangesByCommit map[string][]*models.FileChangeRecord  // commitHash -> changes
    fileChangesByDir    map[string][]*models.FileChangeRecord  // "sliceID:dirPrefix" -> changes
}
```

#### Redis Implementation

```
# Per-file history (sorted set by timestamp, descending)
ZADD slice:{sliceID}:file_changes:path:{path} {timestamp} {changeID}

# Per-commit changes (set)
SADD file_changes:commit:{commitHash} {changeID}

# Per-directory history (sorted set)
ZADD slice:{sliceID}:file_changes:dir:{dirPrefix} {timestamp} {changeID}

# Change record storage (hash)
HSET file_change:{changeID} id ... slice_id ... path ... etc
```

## API Design

### Storage Interface Methods

```go
// Record a file change
AddFileChange(ctx, change *FileChangeRecord) error

// Batch insert (for efficiency when processing commits)
AddFileChanges(ctx, changes []*FileChangeRecord) error

// Get history for a specific file (newest first)
GetFileHistory(ctx, sliceID, path string, limit int, fromCommit string) ([]*FileChangeRecord, error)

// Get history for files under a directory
GetDirectoryHistory(ctx, sliceID, pathPrefix string, limit int, fromCommit string) ([]*FileChangeRecord, error)

// Get all changes in a commit
GetCommitChanges(ctx, commitHash string) ([]*FileChangeRecord, error)

// Flexible query with filters
QueryFileHistory(ctx, query *FileHistoryQuery) (*FileHistoryResult, error)

// Aggregated directory stats
GetDirectorySummary(ctx, sliceID, pathPrefix string) (*DirectoryChangeSummary, error)
```

### gRPC/HTTP API

```protobuf
service FileService {
  // Get change history for a file
  rpc GetFileHistory(GetFileHistoryRequest) returns (GetFileHistoryResponse);

  // Get change history for a directory
  rpc GetDirectoryHistory(GetDirectoryHistoryRequest) returns (GetDirectoryHistoryResponse);

  // Get all changes in a commit
  rpc GetCommitChanges(GetCommitChangesRequest) returns (GetCommitChangesResponse);
}
```

**HTTP Endpoints:**
- `GET /v1/files/history/{path}` - File history
- `GET /v1/directories/history/{path}` - Directory history
- `GET /v1/commits/{hash}/changes` - Commit changes

## Write Path

When a commit is created or synced:

```
1. Compute diff between current and previous CommitSnapshot
2. For each changed file:
   a. Create FileChangeRecord with:
      - Change type (add/modify/delete/rename)
      - Old and new content hashes
      - Line change counts (if available)
      - Denormalized commit metadata
   b. Generate unique ID
   c. Insert into all indexes
3. Store records atomically
```

### Rename Detection

Renames are detected by:
1. Finding files with same content hash that were "deleted" and "added"
2. Marking as rename with `OldPath` set
3. Single record instead of delete + add

## Query Patterns

### Get File History

```sql
-- Conceptual query (not actual SQL)
SELECT * FROM file_changes
WHERE slice_id = ? AND path = ?
ORDER BY timestamp DESC
LIMIT ? OFFSET ?
```

**Complexity:** O(log n) for indexed lookup + O(k) for k results

### Get Directory History

```sql
SELECT * FROM file_changes
WHERE slice_id = ? AND path LIKE ?%
ORDER BY timestamp DESC
LIMIT ?
```

**Implementation:** Use prefix index (`dir:{prefix}`) to avoid full scan

### Get Commit Changes

```sql
SELECT * FROM file_changes
WHERE commit_hash = ?
```

**Complexity:** O(1) lookup + O(k) for k files changed

## Storage Efficiency

### Estimated Record Size

| Field | Typical Size |
|-------|--------------|
| ID | 36 bytes (UUID) |
| SliceID | 36 bytes |
| CommitHash | 40 bytes |
| Path | 100 bytes avg |
| OldPath | 0-100 bytes |
| Hashes | 64 bytes (2×32) |
| Counters | 16 bytes |
| Author | 50 bytes avg |
| Message | 100 bytes avg |
| Timestamp | 8 bytes |
| **Total** | ~450 bytes/record |

### Scaling Considerations

For a repository with:
- 10,000 files
- 50,000 commits
- Average 10 files changed per commit

**Total records:** 500,000
**Storage:** ~225 MB for change records + index overhead

Redis sorted sets add ~100 bytes per entry, so total storage is approximately **400-500 MB** for this scale.

## Migration Strategy

For existing gitslice installations:

1. **Backfill from CommitSnapshots:**
   - Iterate through commits in chronological order
   - Compare consecutive snapshots to compute changes
   - Generate FileChangeRecords

2. **Incremental sync:**
   - New commits automatically generate records
   - Background job for historical data

## Future Enhancements

1. **Blame information:** Track which commit introduced each line
2. **Diff hunks:** Store actual line-level diffs for inline viewing
3. **Cross-slice tracking:** Follow file history across slice boundaries
4. **Search by author/message:** Full-text search on change records

## Files Changed

- `internal/models/file_change.go` - New model definitions
- `internal/storage/storage.go` - Extended interface
- `proto/file/file_service.proto` - API definitions
