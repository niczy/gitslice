package fileservice

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"github.com/pmezard/go-difflib/difflib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fileServiceServer struct {
	filev1.UnimplementedFileServiceServer
	storage storage.Storage
}

func newFileServiceServer(st storage.Storage) *fileServiceServer {
	return &fileServiceServer{storage: st}
}

// RegisterGRPCServer registers the file service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	filev1.RegisterFileServiceServer(srv, newFileServiceServer(st))
}

// NewService constructs the file service implementation for use without gRPC.
func NewService(st storage.Storage) filev1.FileServiceServer {
	return newFileServiceServer(st)
}

// resolveVersion extracts the effective slice and commit from oneof version specifiers.
// Returns sliceID and resolvedCommit (empty string means use current HEAD).
func (s *fileServiceServer) resolveVersion(ctx context.Context, commitHash string, sliceVer *filev1.SliceVersion) (sliceID, resolvedCommit string, err error) {
	// Case 1: slice_version specified
	if sliceVer != nil {
		sliceID = sliceVer.SliceId
		if sliceVer.SliceHash != "" {
			return sliceID, sliceVer.SliceHash, nil
		}
		// No slice_hash: use slice HEAD
		metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
		if err != nil {
			if errors.Is(err, storage.ErrSliceNotFound) {
				return "", "", status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
			}
			return "", "", status.Error(codes.Internal, fmt.Sprintf("failed to get slice metadata: %v", err))
		}
		return sliceID, metadata.HeadCommitHash, nil
	}

	// Case 2: commit_hash specified (use root_slice)
	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return "", "", status.Error(codes.Internal, "root slice not found")
	}
	if commitHash != "" {
		return rootSlice.ID, commitHash, nil
	}

	// Case 3: Nothing specified (use root_slice HEAD)
	metadata, err := s.storage.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		return "", "", status.Error(codes.Internal, fmt.Sprintf("failed to get root slice metadata: %v", err))
	}
	return rootSlice.ID, metadata.HeadCommitHash, nil
}

func (s *fileServiceServer) effectiveSlicePaths(ctx context.Context, sliceID string, slice *models.Slice) ([]string, error) {
	paths := make([]string, 0, len(slice.Files))
	seen := make(map[string]bool, len(slice.Files))
	for _, raw := range slice.Files {
		p := cleanPath(raw)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}

	metadata, err := s.storage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return paths, nil
		}
		return nil, err
	}
	for _, raw := range metadata.ModifiedFiles {
		p := cleanPath(raw)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths, nil
}

func (s *fileServiceServer) ListEntries(ctx context.Context, req *filev1.ListEntriesRequest) (*filev1.ListEntriesResponse, error) {
	// Resolve version from oneof
	sliceID, _, err := s.resolveVersion(ctx, req.GetCommitHash(), req.GetSliceVersion())
	if err != nil {
		return nil, err
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	normalizedPath := cleanPath(req.Path)
	prefix := ""
	if normalizedPath != "" {
		prefix = normalizedPath + "/"
	}

	entriesByName := map[string]*filev1.DirectoryEntry{}
	matchedAny := false
	exactFile := false

	effectivePaths, err := s.effectiveSlicePaths(ctx, sliceID, slice)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get slice metadata: %v", err))
	}

	for _, filePath := range effectivePaths {
		if filePath == "" {
			continue
		}

		if normalizedPath != "" && filePath == normalizedPath {
			exactFile = true
		}

		if prefix != "" && !strings.HasPrefix(filePath, prefix) {
			continue
		}
		matchedAny = true

		remaining := strings.TrimPrefix(filePath, prefix)
		if remaining == "" {
			continue
		}

		parts := strings.Split(remaining, "/")
		name := parts[0]
		entryPath := name
		if normalizedPath != "" {
			entryPath = normalizedPath + "/" + name
		}

		entry, ok := entriesByName[name]
		if !ok {
			entry = &filev1.DirectoryEntry{
				Name: name,
				Path: entryPath,
			}
			entriesByName[name] = entry
		}

		if len(parts) == 1 {
			// Only classify as FILE if not already known to be a DIRECTORY.
			// slice.Files may contain both bare directory paths and nested
			// file paths; Go map iteration order is random, so a bare
			// directory path could be processed after a nested path and
			// incorrectly downgrade the entry from DIRECTORY to FILE.
			if entry.Type != filev1.EntryType_ENTRY_TYPE_DIRECTORY {
				entry.Type = filev1.EntryType_ENTRY_TYPE_FILE
				entry.HasChildren = false
				// Best-effort size lookup only for files directly under the requested path.
				if content, err := s.storage.GetSliceFileByPath(ctx, sliceID, filePath); err == nil && content != nil {
					entry.Size = contentSize(content)
				}
			}
		} else {
			entry.Type = filev1.EntryType_ENTRY_TYPE_DIRECTORY
			entry.HasChildren = true
		}
	}

	if len(entriesByName) == 0 && normalizedPath != "" {
		if exactFile {
			return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
		}
		if matchedAny {
			// Path exists but contains no children (e.g. empty dir in model).
			return &filev1.ListEntriesResponse{SliceId: sliceID, Path: normalizedPath, Entries: []*filev1.DirectoryEntry{}, Truncated: false}, nil
		}
		return nil, status.Error(codes.NotFound, "path not found")
	}

	entries := make([]*filev1.DirectoryEntry, 0, len(entriesByName))
	for _, entry := range entriesByName {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	truncated := false
	if req.Limit > 0 && int(req.Limit) < len(entries) {
		entries = entries[:req.Limit]
		truncated = true
	}

	return &filev1.ListEntriesResponse{
		SliceId:   sliceID,
		Path:      normalizedPath,
		Entries:   entries,
		Truncated: truncated,
	}, nil
}

func (s *fileServiceServer) GetFile(ctx context.Context, req *filev1.GetFileRequest) (*filev1.GetFileResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	// Resolve version from oneof
	sliceID, _, err := s.resolveVersion(ctx, req.GetCommitHash(), req.GetSliceVersion())
	if err != nil {
		return nil, err
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	normalizedPath := cleanPath(req.Path)
	effectivePaths, effectiveErr := s.effectiveSlicePaths(ctx, sliceID, slice)
	if effectiveErr != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get slice metadata: %v", effectiveErr))
	}

	content, err := s.storage.GetSliceFileByPath(ctx, sliceID, normalizedPath)
	if err != nil {
		if sliceHasPath(effectivePaths, normalizedPath) {
			return nil, status.Error(codes.NotFound, "file content not available")
		}
		return nil, status.Error(codes.NotFound, "file not found")
	}

	file := &filev1.File{
		Path:    normalizedPath,
		Content: content.Content,
		Size:    contentSize(content),
		Hash:    content.Hash,
	}

	return &filev1.GetFileResponse{File: file}, nil
}

func cleanPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	cleaned := path.Clean("/" + trimmed)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func fileContentIndex(st storage.Storage, ctx context.Context, sliceID string) map[string]*models.FileContent {
	files, err := st.GetSliceFiles(ctx, sliceID)
	if err != nil {
		return map[string]*models.FileContent{}
	}

	result := make(map[string]*models.FileContent, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		filePath := cleanPath(file.Path)
		if filePath == "" {
			filePath = cleanPath(file.FileID)
		}
		if filePath == "" {
			continue
		}
		result[filePath] = file
	}
	return result
}

func contentSize(content *models.FileContent) int64 {
	if content == nil {
		return 0
	}
	if content.Size != 0 {
		return content.Size
	}
	return int64(len(content.Content))
}

func sliceHasPath(paths []string, path string) bool {
	for _, file := range paths {
		if cleanPath(file) == path {
			return true
		}
	}
	return false
}

// GetFileHistory retrieves the change history for a specific file.
func (s *fileServiceServer) GetFileHistory(ctx context.Context, req *filev1.GetFileHistoryRequest) (*filev1.GetFileHistoryResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	sliceID := req.SliceId
	if sliceID == "" {
		rootSlice, err := s.storage.GetRootSlice(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to get root slice")
		}
		sliceID = rootSlice.ID
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	normalizedPath := cleanPath(req.Path)
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 50
	}

	changes, err := s.storage.GetFileHistory(ctx, sliceID, normalizedPath, limit+1, req.FromCommit)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get file history: %v", err))
	}

	hasMore := len(changes) > limit
	if hasMore {
		changes = changes[:limit]
	}

	var nextCommit string
	if hasMore && len(changes) > 0 {
		nextCommit = changes[len(changes)-1].CommitHash
	}

	protoChanges := make([]*filev1.FileChangeRecord, 0, len(changes))
	for _, change := range changes {
		protoChanges = append(protoChanges, modelToProtoChange(change, ""))
	}

	return &filev1.GetFileHistoryResponse{
		Changes:    protoChanges,
		HasMore:    hasMore,
		NextCommit: nextCommit,
	}, nil
}

// GetDirectoryHistory retrieves change history for all files under a directory.
func (s *fileServiceServer) GetDirectoryHistory(ctx context.Context, req *filev1.GetDirectoryHistoryRequest) (*filev1.GetDirectoryHistoryResponse, error) {
	sliceID := req.SliceId
	if sliceID == "" {
		rootSlice, err := s.storage.GetRootSlice(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to get root slice")
		}
		sliceID = rootSlice.ID
	}

	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", sliceID))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	normalizedPath := cleanPath(req.Path)
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 100
	}

	changes, err := s.storage.GetDirectoryHistory(ctx, sliceID, normalizedPath, limit+1, req.FromCommit)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get directory history: %v", err))
	}

	hasMore := len(changes) > limit
	if hasMore {
		changes = changes[:limit]
	}

	var nextCommit string
	if hasMore && len(changes) > 0 {
		nextCommit = changes[len(changes)-1].CommitHash
	}

	protoChanges := make([]*filev1.FileChangeRecord, 0, len(changes))
	for _, change := range changes {
		protoChanges = append(protoChanges, modelToProtoChange(change, ""))
	}

	// Get summary
	summary, err := s.storage.GetDirectorySummary(ctx, sliceID, normalizedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get directory summary: %v", err))
	}

	return &filev1.GetDirectoryHistoryResponse{
		Changes:    protoChanges,
		HasMore:    hasMore,
		NextCommit: nextCommit,
		Summary:    modelToProtoSummary(summary),
	}, nil
}

// GetCommitChanges retrieves all file changes made in a specific commit.
func (s *fileServiceServer) GetCommitChanges(ctx context.Context, req *filev1.GetCommitChangesRequest) (*filev1.GetCommitChangesResponse, error) {
	if req.CommitHash == "" {
		return nil, status.Error(codes.InvalidArgument, "commit_hash is required")
	}

	changes, err := s.storage.GetCommitChanges(ctx, req.CommitHash)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to get commit changes: %v", err))
	}

	protoChanges := make([]*filev1.FileChangeRecord, 0, len(changes))
	var added, modified, deleted, renamed int32
	for _, change := range changes {
		patch := s.buildChangePatch(ctx, change)
		protoChanges = append(protoChanges, modelToProtoChange(change, patch))
		switch change.ChangeType {
		case models.ChangeTypeAdd:
			added++
		case models.ChangeTypeModify:
			modified++
		case models.ChangeTypeDelete:
			deleted++
		case models.ChangeTypeRename:
			renamed++
		}
	}

	return &filev1.GetCommitChangesResponse{
		CommitHash:    req.CommitHash,
		Changes:       protoChanges,
		FilesAdded:    added,
		FilesModified: modified,
		FilesDeleted:  deleted,
		FilesRenamed:  renamed,
	}, nil
}

// modelToProtoChange converts a model FileChangeRecord to protobuf.
func modelToProtoChange(change *models.FileChangeRecord, patch string) *filev1.FileChangeRecord {
	return &filev1.FileChangeRecord{
		Id:           change.ID,
		SliceId:      change.SliceID,
		CommitHash:   change.CommitHash,
		Path:         change.Path,
		OldPath:      change.OldPath,
		ChangeType:   modelToProtoChangeType(change.ChangeType),
		OldHash:      change.OldHash,
		NewHash:      change.NewHash,
		LinesAdded:   int32(change.LinesAdded),
		LinesDeleted: int32(change.LinesDeleted),
		Author:       change.Author,
		Message:      change.Message,
		Timestamp:    change.Timestamp.Unix(),
		Patch:        patch,
	}
}

func (s *fileServiceServer) buildChangePatch(ctx context.Context, change *models.FileChangeRecord) string {
	if change == nil {
		return ""
	}

	newPath := cleanPath(change.Path)
	oldPath := cleanPath(change.OldPath)
	if oldPath == "" {
		oldPath = newPath
	}

	beforeLines := []string{}
	afterLines := []string{}

	shouldLoadBefore := change.OldHash != "" || change.ChangeType == models.ChangeTypeModify || change.ChangeType == models.ChangeTypeDelete || change.ChangeType == models.ChangeTypeRename
	beforeUndiffable := false
	if shouldLoadBefore {
		parentHash, err := s.findParentCommitHash(ctx, change.SliceID, change.CommitHash)
		if err == nil && parentHash != "" {
			if prev, ferr := s.storage.GetFileAtCommit(ctx, parentHash, oldPath); ferr == nil && prev != nil {
				if lines, ok := splitLinesForDiff(prev.Content); ok {
					beforeLines = lines
				} else {
					beforeUndiffable = true
				}
			}
		}
	}

	shouldLoadAfter := change.NewHash != "" || change.ChangeType == models.ChangeTypeAdd || change.ChangeType == models.ChangeTypeModify || change.ChangeType == models.ChangeTypeRename
	afterUndiffable := false
	if shouldLoadAfter {
		if curr, err := s.storage.GetFileAtCommit(ctx, change.CommitHash, newPath); err == nil && curr != nil {
			if lines, ok := splitLinesForDiff(curr.Content); ok {
				afterLines = lines
			} else {
				afterUndiffable = true
			}
		}
	}
	if beforeUndiffable || afterUndiffable {
		return ""
	}

	if len(beforeLines) == 0 && len(afterLines) == 0 {
		return ""
	}

	unified, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: "a/" + oldPath,
		ToFile:   "b/" + newPath,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return unified
}

func (s *fileServiceServer) findParentCommitHash(ctx context.Context, sliceID, commitHash string) (string, error) {
	if sliceID == "" || commitHash == "" {
		return "", nil
	}

	commits, err := s.storage.ListSliceCommits(ctx, sliceID, 0, "")
	if err != nil {
		return "", err
	}
	for _, c := range commits {
		if c.CommitHash == commitHash {
			return c.ParentHash, nil
		}
	}
	return "", nil
}

func splitLinesForDiff(content []byte) ([]string, bool) {
	if len(content) == 0 {
		return []string{}, true
	}
	// Diff patches are encoded as protobuf string fields, so invalid UTF-8 or
	// binary data cannot be returned safely as patch text.
	if !utf8.Valid(content) || bytesContainsNUL(content) {
		return nil, false
	}
	lines := strings.SplitAfter(string(content), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, true
}

func bytesContainsNUL(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

// modelToProtoChangeType converts model ChangeType to protobuf.
func modelToProtoChangeType(ct models.ChangeType) filev1.ChangeType {
	switch ct {
	case models.ChangeTypeAdd:
		return filev1.ChangeType_CHANGE_TYPE_ADD
	case models.ChangeTypeModify:
		return filev1.ChangeType_CHANGE_TYPE_MODIFY
	case models.ChangeTypeDelete:
		return filev1.ChangeType_CHANGE_TYPE_DELETE
	case models.ChangeTypeRename:
		return filev1.ChangeType_CHANGE_TYPE_RENAME
	default:
		return filev1.ChangeType_CHANGE_TYPE_UNSPECIFIED
	}
}

// modelToProtoSummary converts a model DirectoryChangeSummary to protobuf.
func modelToProtoSummary(summary *models.DirectoryChangeSummary) *filev1.DirectoryChangeSummary {
	changesByType := make(map[string]int32)
	for ct, count := range summary.ChangesByType {
		changesByType[string(ct)] = int32(count)
	}

	var lastChange *filev1.FileChangeRecord
	if summary.LastChange != nil {
		lastChange = modelToProtoChange(summary.LastChange, "")
	}

	return &filev1.DirectoryChangeSummary{
		Path:          summary.Path,
		TotalChanges:  int32(summary.TotalChanges),
		FilesChanged:  int32(summary.FilesChanged),
		ChangesByType: changesByType,
		LastChange:    lastChange,
	}
}
