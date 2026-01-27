package fileservice

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
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

	normalizedPath := cleanPath(req.Path)
	prefix := ""
	if normalizedPath != "" {
		prefix = normalizedPath + "/"
	}

	fileSet := make(map[string]bool, len(slice.Files))
	for _, file := range slice.Files {
		normalized := cleanPath(file)
		if normalized == "" {
			continue
		}
		fileSet[normalized] = true
	}

	contentByPath := fileContentIndex(s.storage, ctx, sliceID)

	entriesByName := map[string]*filev1.DirectoryEntry{}
	for filePath := range fileSet {
		if prefix != "" && !strings.HasPrefix(filePath, prefix) {
			continue
		}

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
			entry.Type = filev1.EntryType_ENTRY_TYPE_FILE
			entry.HasChildren = false
			if content, ok := contentByPath[filePath]; ok {
				entry.Size = contentSize(content)
			}
		} else {
			entry.Type = filev1.EntryType_ENTRY_TYPE_DIRECTORY
			entry.HasChildren = true
		}
	}

	if len(entriesByName) == 0 && normalizedPath != "" {
		if fileSet[normalizedPath] {
			return nil, status.Error(codes.FailedPrecondition, "path refers to a file")
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

	normalizedPath := cleanPath(req.Path)
	content, err := s.storage.GetSliceFileByPath(ctx, sliceID, normalizedPath)
	if err != nil {
		if sliceHasPath(slice, normalizedPath) {
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

func sliceHasPath(slice *models.Slice, path string) bool {
	for _, file := range slice.Files {
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
		protoChanges = append(protoChanges, modelToProtoChange(change))
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
		protoChanges = append(protoChanges, modelToProtoChange(change))
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
		protoChanges = append(protoChanges, modelToProtoChange(change))
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
func modelToProtoChange(change *models.FileChangeRecord) *filev1.FileChangeRecord {
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
	}
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
		lastChange = modelToProtoChange(summary.LastChange)
	}

	return &filev1.DirectoryChangeSummary{
		Path:          summary.Path,
		TotalChanges:  int32(summary.TotalChanges),
		FilesChanged:  int32(summary.FilesChanged),
		ChangesByType: changesByType,
		LastChange:    lastChange,
	}
}
