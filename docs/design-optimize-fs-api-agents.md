# Design: Optimize Filesystem API for Agent Workloads

## Problem Statement

The `FilesystemService` API has three performance bottlenecks that compound badly
for agent workloads (AI coding agents making many small file edits):

### Bottleneck 1: Full-file round-trip for every edit

To change one line in a file, an agent must:

1. `ReadFile` — download the entire file content over the wire
2. Apply the edit client-side (string replacement, line insert, etc.)
3. `WriteFile` — upload the entire file content back

For a 50 KB file where the agent changes one line, this moves ~100 KB over the
network. Agents doing dozens of edits per task pay this cost every time.

### Bottleneck 2: Full tree walk on every mutation commit

Every call to `WriteFile`, `DeleteFile`, `MoveFile`, `CopyFile`, or
`MakeDirectory` calls `commitWorkspaceMutation`, which does:

```
commitWorkspaceMutation
  ├── workspaceStats()              ← walks entire directory tree
  │     └── collectWorkspaceEntries()   ← recursive ListEntries from root
  ├── collectWorkspaceSnapshotFiles()  ← walks entire tree AGAIN
  │     ├── collectWorkspaceEntries()   ← same recursive walk
  │     └── GetSliceFileByPath() × N   ← loads every file's content to hash it
  └── SaveCommitSnapshot()
```

**Two full tree walks plus N file-content reads per single-file write.**
For a workspace with 500 files, writing one file triggers ~1000 storage calls.

### Bottleneck 3: No partial file reads

`ReadFile` always returns the complete `bytes content`. Agents frequently only
need a portion of a file (e.g., lines 100-120 to understand context around a
function). There is no way to request a byte range or line range.

---

## Proposed Changes

### 1. Add `EditFile` RPC — server-side text replacement

Add a new RPC to `FilesystemService` that applies text edits server-side,
eliminating the read-modify-write round trip.

#### Proto changes (`proto/filesystem/filesystem_service.proto`)

```protobuf
rpc EditFile(EditFileRequest) returns (EditFileResponse) {
  option (google.api.http) = {
    post: "/v1/fs/workspaces/{workspace_id}/files/{path=**}:edit"
    body: "*"
  };
}

message TextEdit {
  string old_text = 1;      // Exact text to find and replace
  string new_text = 2;      // Replacement text
  bool replace_all = 3;     // Replace all occurrences (default: first only)
}

message EditFileRequest {
  string workspace_id = 1;
  string path = 2;
  repeated TextEdit edits = 3;   // Applied sequentially
  string expected_hash = 4;      // Optional: fail if file hash doesn't match (optimistic concurrency)
}

message EditFileResponse {
  string workspace_id = 1;
  string path = 2;
  int64 size = 3;
  string hash = 4;
  string commit_hash = 5;
  bool applied = 6;              // False if expected_hash mismatch
  int32 edits_applied = 7;       // How many edits succeeded
}
```

#### Server implementation (`services/filesystem/server.go`)

```go
func (s *filesystemServiceServer) EditFile(ctx context.Context, req *filesystemv1.EditFileRequest) (*filesystemv1.EditFileResponse, error) {
    username, err := s.requireUser(ctx)
    if err != nil {
        return nil, err
    }

    workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
    if err != nil {
        return nil, err
    }

    filePath, displayPath, err := s.resolveOperationPath(username, homeMode, req.GetPath(), true)
    if err != nil {
        return nil, err
    }

    // Read current content
    content, err := s.storage.GetSliceFileByPath(ctx, workspace.ID, filePath)
    if err != nil {
        return nil, status.Error(codes.NotFound, fmt.Sprintf("file not found: %s", filePath))
    }

    // Optimistic concurrency check
    if req.ExpectedHash != "" {
        currentHash := strings.TrimSpace(content.Hash)
        if currentHash == "" {
            currentHash = hashContent(content.Content)
        }
        if currentHash != req.ExpectedHash {
            return &filesystemv1.EditFileResponse{
                WorkspaceId: workspace.ID,
                Path:        displayPath,
                Applied:     false,
            }, nil
        }
    }

    // Apply edits sequentially
    data := content.Content
    appliedCount := int32(0)
    for _, edit := range req.GetEdits() {
        old := []byte(edit.GetOldText())
        new := []byte(edit.GetNewText())
        if len(old) == 0 {
            continue
        }
        if edit.GetReplaceAll() {
            data = bytes.ReplaceAll(data, old, new)
        } else {
            data = bytes.Replace(data, old, new, 1)
        }
        appliedCount++
    }

    // Write back
    hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, filePath, data)
    if err != nil {
        return nil, err
    }

    commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("edit %s", filePath))
    if err != nil {
        return nil, err
    }

    return &filesystemv1.EditFileResponse{
        WorkspaceId:  workspace.ID,
        Path:         displayPath,
        Size:         size,
        Hash:         hash,
        CommitHash:   commitHash,
        Applied:      true,
        EditsApplied: appliedCount,
    }, nil
}
```

#### Why this matters

- Agent sends only the old/new strings (~200 bytes) instead of the full file (~50 KB) × 2
- Eliminates the client-side read step entirely
- `expected_hash` provides safe concurrent editing without locking
- Multiple edits in one request = one commit instead of N commits

---

### 2. Incremental commit snapshots — eliminate redundant tree walks

Replace the current `commitWorkspaceMutation` implementation that walks the
entire tree twice with an incremental approach that only processes changed files.

#### Core idea

Instead of rebuilding the snapshot from scratch, copy the parent snapshot and
patch in only the changed file(s).

#### New method signature

```go
// commitWorkspaceMutationIncremental creates a commit by patching the parent
// snapshot with only the specified changed paths, avoiding a full tree walk.
func (s *filesystemServiceServer) commitWorkspaceMutationIncremental(
    ctx context.Context,
    workspace *models.Slice,
    message string,
    changedPaths []string,    // paths that were added/modified
    deletedPaths []string,    // paths that were removed
) (string, error)
```

#### Implementation

```go
func (s *filesystemServiceServer) commitWorkspaceMutationIncremental(
    ctx context.Context,
    workspace *models.Slice,
    message string,
    changedPaths []string,
    deletedPaths []string,
) (string, error) {
    if workspace == nil {
        return "", status.Error(codes.Internal, "workspace is nil")
    }

    meta, err := s.storage.GetSliceMetadata(ctx, workspace.ID)
    if err != nil {
        return "", status.Error(codes.Internal, fmt.Sprintf("failed to load workspace metadata: %v", err))
    }

    // Start from parent snapshot instead of walking the tree
    files := make(map[string]string)
    if meta.HeadCommitHash != "" {
        parentSnapshot, err := s.storage.GetCommitSnapshot(ctx, meta.HeadCommitHash)
        if err == nil && parentSnapshot != nil {
            for k, v := range parentSnapshot.Files {
                files[k] = v
            }
        }
    }

    // If no parent snapshot exists, fall back to full collection
    // (first commit in workspace — cold start only)
    if len(files) == 0 && meta.HeadCommitHash == "" {
        collected, err := s.collectWorkspaceSnapshotFiles(ctx, workspace.ID)
        if err != nil {
            return "", status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace snapshot: %v", err))
        }
        files = collected
    }

    // Patch in changed files
    for _, p := range changedPaths {
        content, err := s.storage.GetSliceFileByPath(ctx, workspace.ID, p)
        if err != nil {
            continue // file may have been deleted between write and commit
        }
        hash := strings.TrimSpace(content.Hash)
        if hash == "" {
            hash = hashContent(content.Content)
        }
        files[p] = hash
    }

    // Remove deleted files
    for _, p := range deletedPaths {
        delete(files, p)
    }

    // Build paths list from snapshot keys (avoids tree walk)
    paths := make([]string, 0, len(files))
    for p := range files {
        paths = append(paths, p)
    }
    sort.Strings(paths)

    now := time.Now()
    commitHash := fmt.Sprintf("fs-%d", now.UnixNano())

    if err := s.storage.AddSliceCommit(ctx, workspace.ID, &models.Commit{
        CommitHash: commitHash,
        ParentHash: meta.HeadCommitHash,
        Timestamp:  now,
        Message:    message,
    }); err != nil {
        return "", status.Error(codes.Internal, fmt.Sprintf("failed to record workspace commit: %v", err))
    }

    if err := s.storage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
        CommitHash: commitHash,
        SliceID:    workspace.ID,
        Files:      files,
        Timestamp:  now,
    }); err != nil {
        return "", status.Error(codes.Internal, fmt.Sprintf("failed to save workspace snapshot: %v", err))
    }

    if err := s.storage.UpdateSliceMetadata(ctx, workspace.ID, &models.SliceMetadata{
        SliceID:            workspace.ID,
        HeadCommitHash:     commitHash,
        ModifiedFiles:      paths,
        LastModified:       now,
        ModifiedFilesCount: len(paths),
    }); err != nil {
        return "", status.Error(codes.Internal, fmt.Sprintf("failed to update workspace metadata: %v", err))
    }

    return commitHash, nil
}
```

#### Migration strategy

1. Add `commitWorkspaceMutationIncremental` alongside existing `commitWorkspaceMutation`
2. Convert each mutation RPC one at a time (WriteFile, DeleteFile, etc.) to use
   the incremental variant, since each already knows which paths changed
3. Keep `commitWorkspaceMutation` as fallback for operations where changed paths
   aren't easily enumerable (shouldn't be any, but safety net)
4. Remove the old method once all callers are migrated

#### Call sites to update

| RPC | changedPaths | deletedPaths |
|-----|-------------|-------------|
| `WriteFile` | `[filePath]` | `[]` |
| `DeleteFile` | `[]` | `[filePath]` |
| `MoveFile` | `[destinationPath]` | `[sourcePath]` |
| `CopyFile` | `[destinationPath]` | `[]` |
| `MakeDirectory` | `[dirPath]` | `[]` |
| `WriteFiles` | `[...paths]` | `[]` |
| `EditFile` (new) | `[filePath]` | `[]` |

#### Performance impact

For a workspace with 500 files where one file is edited:

| | Before | After |
|--|--------|-------|
| Tree walks | 2 full walks | 0 |
| `ListEntries` calls | ~500 × 2 | 0 |
| `GetSliceFileByPath` calls | ~500 | 1 |
| `GetCommitSnapshot` calls | 0 | 1 |
| Total storage calls | ~1500 | 4 |

---

### 3. Add `ReadFile` with offset/limit — partial file reads

Extend the existing `ReadFile` RPC to support byte-range and line-range reads.

#### Proto changes (`proto/filesystem/filesystem_service.proto`)

```protobuf
message ReadFileRequest {
  string workspace_id = 1;
  string path = 2;
  int64 byte_offset = 3;    // Start reading from this byte (0 = beginning)
  int64 byte_limit = 4;     // Max bytes to return (0 = entire file)
  int32 line_offset = 5;    // Start reading from this line (1-based, 0 = ignore)
  int32 line_limit = 6;     // Max lines to return (0 = all remaining)
}

message ReadFileResponse {
  string workspace_id = 1;
  string path = 2;
  bytes content = 3;
  int64 size = 4;            // Total file size (always returned)
  string hash = 5;           // Hash of FULL file (always returned)
  int64 content_offset = 6;  // Byte offset of returned content within the file
  bool truncated = 7;        // True if content was trimmed by limit
  int32 total_lines = 8;     // Total line count (only when line_offset/limit used)
}
```

#### Server implementation

```go
func (s *filesystemServiceServer) ReadFile(ctx context.Context, req *filesystemv1.ReadFileRequest) (*filesystemv1.ReadFileResponse, error) {
    // ... existing workspace/path resolution ...

    content, err := s.storage.GetSliceFileByPath(ctx, workspace.ID, filePath)
    if err != nil {
        // ... existing error handling ...
    }

    data := content.Content
    totalSize := int64(len(data))
    hash := strings.TrimSpace(content.Hash)
    if hash == "" {
        hash = hashContent(data)
    }

    resp := &filesystemv1.ReadFileResponse{
        WorkspaceId: workspace.ID,
        Path:        displayPath,
        Size:        totalSize,
        Hash:        hash,
    }

    // Line-based slicing (takes precedence over byte-based)
    if req.LineOffset > 0 || req.LineLimit > 0 {
        lines := bytes.Split(data, []byte("\n"))
        resp.TotalLines = int32(len(lines))

        start := int(req.LineOffset)
        if start > 0 {
            start-- // Convert 1-based to 0-based
        }
        if start > len(lines) {
            start = len(lines)
        }

        end := len(lines)
        if req.LineLimit > 0 && start+int(req.LineLimit) < end {
            end = start + int(req.LineLimit)
            resp.Truncated = true
        }

        data = bytes.Join(lines[start:end], []byte("\n"))
        // Calculate byte offset of the start line
        byteOffset := int64(0)
        for i := 0; i < start; i++ {
            byteOffset += int64(len(lines[i])) + 1
        }
        resp.ContentOffset = byteOffset
        resp.Content = data
        return resp, nil
    }

    // Byte-range slicing
    if req.ByteOffset > 0 || req.ByteLimit > 0 {
        offset := req.ByteOffset
        if offset > totalSize {
            offset = totalSize
        }
        data = data[offset:]
        resp.ContentOffset = offset

        if req.ByteLimit > 0 && int64(len(data)) > req.ByteLimit {
            data = data[:req.ByteLimit]
            resp.Truncated = true
        }
    }

    resp.Content = data
    return resp, nil
}
```

#### Backward compatibility

All new fields have zero-value defaults. Existing clients that send no
offset/limit get the full file as before — no breaking change.

---

## Implementation Plan

### Phase 1: Incremental snapshots (highest impact, lowest risk)

1. Add `commitWorkspaceMutationIncremental` method
2. Update `WriteFile` to use it (single file = easy to validate)
3. Add unit test comparing snapshot output of old vs new path
4. Migrate remaining mutation RPCs one by one
5. Remove old `commitWorkspaceMutation`

### Phase 2: `EditFile` RPC

1. Add proto message definitions
2. Run `make proto` to regenerate
3. Implement `EditFile` in `services/filesystem/server.go`
4. Wire into incremental commit from Phase 1
5. Add tests for: single edit, multiple edits, `replace_all`, hash mismatch,
   file not found, empty old_text

### Phase 3: Partial `ReadFile`

1. Add new fields to existing proto messages
2. Run `make proto`
3. Implement slicing logic in `ReadFile`
4. Test: byte offset, byte limit, line offset, line limit, combinations,
   backward compat (no params = full file)

### Phase 4: Batch `EditFiles` (optional follow-up)

If agents commonly edit multiple files in one logical operation, add a batch
variant similar to `WriteFiles`/`ReadFiles`:

```protobuf
rpc EditFiles(EditFilesRequest) returns (EditFilesResponse);
```

This would apply edits to multiple files with a single commit.

---

## Testing Strategy

All changes should be testable with existing infrastructure:

- **Unit tests**: `services/filesystem/server_test.go` — test each new RPC
  directly against in-memory storage
- **Snapshot correctness**: Write a test that performs the same sequence of
  mutations via old and new commit paths, assert identical snapshots
- **Integration tests**: `workflow_test/` — add a test that exercises EditFile
  through the full gRPC stack
- **Build verification**: `go build ./servers/core/` and `go build ./gs_cli/`

---

## Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| Incremental snapshot diverges from truth | Fallback: if parent snapshot is missing, do full tree walk (already in design). Add periodic consistency check. |
| `EditFile` old_text not found in file | Return success with `edits_applied=0`. Agent can detect and fall back to full WriteFile. |
| Large file line-split allocates too much memory | For files > 10 MB, reject line-based reads and require byte-range instead. |
| Proto field additions break existing clients | All new fields use zero-value semantics. Protobuf is backward-compatible by design. |
