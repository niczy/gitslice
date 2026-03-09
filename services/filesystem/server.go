package filesystemservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type filesystemServiceServer struct {
	filesystemv1.UnimplementedFilesystemServiceServer
	storage storage.Storage
}

func newFilesystemServiceServer(st storage.Storage) *filesystemServiceServer {
	return &filesystemServiceServer{storage: st}
}

// RegisterGRPCServer registers the filesystem service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	filesystemv1.RegisterFilesystemServiceServer(srv, newFilesystemServiceServer(st))
}

// NewService constructs the filesystem service implementation for use without gRPC.
func NewService(st storage.Storage) filesystemv1.FilesystemServiceServer {
	return newFilesystemServiceServer(st)
}

func (s *filesystemServiceServer) CreateWorkspace(ctx context.Context, req *filesystemv1.CreateWorkspaceRequest) (*filesystemv1.WorkspaceInfo, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspaceID := strings.TrimSpace(req.GetWorkspaceId())
	if workspaceID == "" {
		workspaceID = slugifyWorkspaceID(req.GetName())
	}
	if workspaceID == "" {
		workspaceID = common.GenerateSliceID()
	}
	if err := common.ValidateSliceID(workspaceID); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid workspace_id: %v", err))
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		name = workspaceID
	}

	workspace := &models.Slice{
		ID:          workspaceID,
		Name:        name,
		Description: strings.TrimSpace(req.GetDescription()),
		Owners:      []string{username},
		CreatedBy:   username,
		Files:       []string{},
	}
	if err := s.storage.CreateSlice(ctx, workspace); err != nil {
		switch err {
		case storage.ErrSliceAlreadyExists:
			return nil, status.Error(codes.AlreadyExists, "workspace already exists")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid workspace")
		default:
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create workspace: %v", err))
		}
	}

	meta, err := s.storage.GetSliceMetadata(ctx, workspaceID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load workspace metadata: %v", err))
	}
	if err := s.storage.AddSliceCommit(ctx, workspaceID, &models.Commit{
		CommitHash: meta.HeadCommitHash,
		ParentHash: "",
		Timestamp:  workspace.CreatedAt,
		Message:    "create workspace",
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to record initial workspace commit: %v", err))
	}
	if err := s.storage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: meta.HeadCommitHash,
		SliceID:    workspaceID,
		Files:      map[string]string{},
		Timestamp:  workspace.CreatedAt,
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to save initial workspace snapshot: %v", err))
	}

	return s.workspaceInfo(ctx, workspace)
}

func (s *filesystemServiceServer) ListWorkspaces(ctx context.Context, req *filesystemv1.ListWorkspacesRequest) (*filesystemv1.ListWorkspacesResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspaces := make([]*models.Slice, 0)
	seen := make(map[string]struct{})
	if root, rootErr := s.storage.GetRootSlice(ctx); rootErr == nil && root != nil {
		workspaces = append(workspaces, root)
		seen[root.ID] = struct{}{}
	}

	owned, err := s.storage.ListSlicesByOwner(ctx, username, int(^uint(0)>>1), 0)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list workspaces: %v", err))
	}
	for _, workspace := range owned {
		if workspace == nil {
			continue
		}
		if _, ok := seen[workspace.ID]; ok {
			continue
		}
		seen[workspace.ID] = struct{}{}
		workspaces = append(workspaces, workspace)
	}

	total := len(workspaces)
	offset := int(req.GetOffset())
	if offset < 0 {
		return nil, status.Error(codes.InvalidArgument, "offset must be >= 0")
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = total
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	response := &filesystemv1.ListWorkspacesResponse{
		Workspaces: make([]*filesystemv1.WorkspaceInfo, 0, end-offset),
		Total:      int32(total),
	}
	for _, workspace := range workspaces[offset:end] {
		info, err := s.workspaceInfo(ctx, workspace)
		if err != nil {
			return nil, err
		}
		response.Workspaces = append(response.Workspaces, info)
	}
	return response, nil
}

func (s *filesystemServiceServer) GetWorkspaceInfo(ctx context.Context, req *filesystemv1.GetWorkspaceInfoRequest) (*filesystemv1.WorkspaceInfo, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	return s.workspaceInfo(ctx, workspace)
}

func (s *filesystemServiceServer) ReadFile(ctx context.Context, req *filesystemv1.ReadFileRequest) (*filesystemv1.ReadFileResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	filePath, err := validateWorkspacePath(req.GetPath(), true)
	if err != nil {
		return nil, err
	}

	content, err := s.storage.GetSliceFileByPath(ctx, workspace.ID, filePath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "file not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read file: %v", err))
	}

	hash := strings.TrimSpace(content.Hash)
	if hash == "" {
		hash = hashContent(content.Content)
	}
	return &filesystemv1.ReadFileResponse{
		WorkspaceId: workspace.ID,
		Path:        filePath,
		Content:     append([]byte(nil), content.Content...),
		Size:        int64(len(content.Content)),
		Hash:        hash,
	}, nil
}

func (s *filesystemServiceServer) WriteFile(ctx context.Context, req *filesystemv1.WriteFileRequest) (*filesystemv1.WriteFileResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	filePath, err := validateWorkspacePath(req.GetPath(), true)
	if err != nil {
		return nil, err
	}

	content := append([]byte(nil), req.GetContent()...)
	hash := hashContent(content)
	fileContentID := workspaceFileContentID(workspace.ID, filePath)
	if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(workspace.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: workspace.ID,
		Size:     int64(len(content)),
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to write file entry: %v", err))
	}
	if err := s.storage.AddFileContent(ctx, &models.FileContent{
		FileID:  fileContentID,
		Path:    filePath,
		Content: content,
		Size:    int64(len(content)),
		Hash:    hash,
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to persist file content: %v", err))
	}
	if err := s.storage.AddFileToSlice(ctx, filePath, workspace.ID); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update workspace file index: %v", err))
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("write %s", filePath))
	if err != nil {
		return nil, err
	}
	return &filesystemv1.WriteFileResponse{
		WorkspaceId: workspace.ID,
		Path:        filePath,
		Size:        int64(len(content)),
		Hash:        hash,
		CommitHash:  commitHash,
	}, nil
}

func (s *filesystemServiceServer) DeleteFile(ctx context.Context, req *filesystemv1.DeleteFileRequest) (*filesystemv1.DeleteFileResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	filePath, err := validateWorkspacePath(req.GetPath(), true)
	if err != nil {
		return nil, err
	}

	entry, err := s.storage.GetEntryByPath(ctx, workspace.ID, filePath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "file not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file entry: %v", err))
	}
	if entry.Type != "file" {
		return nil, status.Error(codes.FailedPrecondition, "path is not a file")
	}

	if err := s.storage.DeleteEntry(ctx, entry.ID); err != nil && err != storage.ErrEntryNotFound {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete file entry: %v", err))
	}
	if err := s.storage.RemoveFileFromSlice(ctx, filePath, workspace.ID); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update workspace file index: %v", err))
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("delete %s", filePath))
	if err != nil {
		return nil, err
	}
	return &filesystemv1.DeleteFileResponse{
		WorkspaceId: workspace.ID,
		Path:        filePath,
		CommitHash:  commitHash,
	}, nil
}

func (s *filesystemServiceServer) ListDirectory(ctx context.Context, req *filesystemv1.ListDirectoryRequest) (*filesystemv1.ListDirectoryResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	dirPath, err := validateWorkspacePath(req.GetPath(), false)
	if err != nil {
		return nil, err
	}

	parentID := workspace.ID
	if dirPath != "" {
		entry, err := s.storage.GetEntryByPath(ctx, workspace.ID, dirPath)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.NotFound, "directory not found")
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load directory entry: %v", err))
		}
		if entry.Type != "directory" {
			return nil, status.Error(codes.FailedPrecondition, "path is not a directory")
		}
		parentID = entry.ID
	}

	entries, err := s.storage.ListEntries(ctx, workspace.ID, parentID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list directory: %v", err))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	response := &filesystemv1.ListDirectoryResponse{
		WorkspaceId: workspace.ID,
		Path:        dirPath,
		Entries:     make([]*filesystemv1.WorkspaceEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		response.Entries = append(response.Entries, entryToProto(entry))
	}
	return response, nil
}

func (s *filesystemServiceServer) MakeDirectory(ctx context.Context, req *filesystemv1.MakeDirectoryRequest) (*filesystemv1.MakeDirectoryResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	dirPath, err := validateWorkspacePath(req.GetPath(), true)
	if err != nil {
		return nil, err
	}
	if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(workspace.ID, dirPath),
		Path:     dirPath,
		Type:     "directory",
		ParentID: workspace.ID,
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create directory: %v", err))
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("mkdir %s", dirPath))
	if err != nil {
		return nil, err
	}
	return &filesystemv1.MakeDirectoryResponse{
		WorkspaceId: workspace.ID,
		Path:        dirPath,
		CommitHash:  commitHash,
	}, nil
}

func (s *filesystemServiceServer) Stat(ctx context.Context, req *filesystemv1.StatRequest) (*filesystemv1.StatResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	statPath, err := validateWorkspacePath(req.GetPath(), false)
	if err != nil {
		return nil, err
	}
	if statPath == "" {
		return &filesystemv1.StatResponse{
			Exists: true,
			Entry: &filesystemv1.WorkspaceEntry{
				Name: path.Base(workspace.ID),
				Path: "",
				Type: filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY,
			},
		}, nil
	}

	entry, err := s.storage.GetEntryByPath(ctx, workspace.ID, statPath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return &filesystemv1.StatResponse{Exists: false}, nil
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to stat path: %v", err))
	}
	return &filesystemv1.StatResponse{
		Exists: true,
		Entry:  entryToProto(entry),
	}, nil
}

func (s *filesystemServiceServer) Exists(ctx context.Context, req *filesystemv1.ExistsRequest) (*filesystemv1.ExistsResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	existsPath, err := validateWorkspacePath(req.GetPath(), false)
	if err != nil {
		return nil, err
	}
	if existsPath == "" {
		return &filesystemv1.ExistsResponse{Exists: true}, nil
	}

	_, err = s.storage.GetEntryByPath(ctx, workspace.ID, existsPath)
	if err == nil {
		return &filesystemv1.ExistsResponse{Exists: true}, nil
	}
	if err == storage.ErrEntryNotFound {
		return &filesystemv1.ExistsResponse{Exists: false}, nil
	}
	return nil, status.Error(codes.Internal, fmt.Sprintf("failed to check path existence: %v", err))
}

func (s *filesystemServiceServer) requireUser(ctx context.Context) (string, error) {
	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return "", status.Error(codes.Unauthenticated, "login required")
	}
	if _, err := s.storage.EnsureUser(ctx, username); err != nil {
		return "", status.Error(codes.InvalidArgument, "invalid user")
	}
	return username, nil
}

func (s *filesystemServiceServer) requireWorkspaceViewAccess(ctx context.Context, workspaceID string) (*models.Slice, *models.SliceMetadata, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "workspace_id is required")
	}

	workspace, err := s.storage.GetSlice(ctx, workspaceID)
	if err != nil {
		if err == storage.ErrSliceNotFound {
			return nil, nil, status.Error(codes.NotFound, "workspace not found")
		}
		return nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to load workspace: %v", err))
	}

	username := auth.UsernameFromGRPCContext(ctx)
	if !canViewWorkspace(workspace, username) {
		return nil, nil, status.Error(codes.PermissionDenied, "not authorized for workspace")
	}

	meta, err := s.storage.GetSliceMetadata(ctx, workspace.ID)
	if err != nil {
		return nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to load workspace metadata: %v", err))
	}
	return workspace, meta, nil
}

func (s *filesystemServiceServer) requireWorkspaceWriteAccess(ctx context.Context, workspaceID, username string) (*models.Slice, *models.SliceMetadata, error) {
	workspace, meta, err := s.requireWorkspaceViewAccess(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !canWriteWorkspace(workspace, username) {
		return nil, nil, status.Error(codes.PermissionDenied, "not authorized to modify workspace")
	}
	return workspace, meta, nil
}

func (s *filesystemServiceServer) workspaceInfo(ctx context.Context, workspace *models.Slice) (*filesystemv1.WorkspaceInfo, error) {
	if workspace == nil {
		return nil, status.Error(codes.Internal, "workspace is nil")
	}

	meta, err := s.storage.GetSliceMetadata(ctx, workspace.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load workspace metadata: %v", err))
	}
	paths, fileCount, err := s.workspaceStats(ctx, workspace.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load workspace stats: %v", err))
	}

	updatedAt := workspace.UpdatedAt
	if meta.LastModified.After(updatedAt) {
		updatedAt = meta.LastModified
	}

	return &filesystemv1.WorkspaceInfo{
		WorkspaceId:    workspace.ID,
		Name:           workspace.Name,
		Description:    workspace.Description,
		CreatedBy:      workspace.CreatedBy,
		Owners:         append([]string(nil), workspace.Owners...),
		HeadCommitHash: meta.HeadCommitHash,
		CreatedAt:      workspace.CreatedAt.Unix(),
		UpdatedAt:      updatedAt.Unix(),
		PathCount:      int32(len(paths)),
		FileCount:      int32(fileCount),
		IsRoot:         workspace.IsRoot,
	}, nil
}

func (s *filesystemServiceServer) workspaceStats(ctx context.Context, workspaceID string) ([]string, int, error) {
	entries, err := s.collectWorkspaceEntries(ctx, workspaceID)
	if err != nil {
		return nil, 0, err
	}
	paths := make([]string, 0, len(entries))
	fileCount := 0
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		paths = append(paths, entry.Path)
		if entry.Type == "file" {
			fileCount++
		}
	}
	sort.Strings(paths)
	return paths, fileCount, nil
}

func (s *filesystemServiceServer) commitWorkspaceMutation(ctx context.Context, workspace *models.Slice, message string) (string, error) {
	if workspace == nil {
		return "", status.Error(codes.Internal, "workspace is nil")
	}

	meta, err := s.storage.GetSliceMetadata(ctx, workspace.ID)
	if err != nil {
		return "", status.Error(codes.Internal, fmt.Sprintf("failed to load workspace metadata: %v", err))
	}

	paths, _, err := s.workspaceStats(ctx, workspace.ID)
	if err != nil {
		return "", status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace paths: %v", err))
	}
	files, err := s.collectWorkspaceSnapshotFiles(ctx, workspace.ID)
	if err != nil {
		return "", status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace snapshot: %v", err))
	}

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

func (s *filesystemServiceServer) collectWorkspaceEntries(ctx context.Context, workspaceID string) ([]*models.DirectoryEntry, error) {
	rootChildren, err := s.storage.ListEntries(ctx, workspaceID, workspaceID)
	if err != nil {
		return nil, err
	}

	result := make([]*models.DirectoryEntry, 0, len(rootChildren))
	var walk func(parentID string) error
	walk = func(parentID string) error {
		children, err := s.storage.ListEntries(ctx, workspaceID, parentID)
		if err != nil {
			return err
		}
		for _, child := range children {
			result = append(result, child)
			if child != nil && child.Type == "directory" {
				if err := walk(child.ID); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, child := range rootChildren {
		result = append(result, child)
		if child != nil && child.Type == "directory" {
			if err := walk(child.ID); err != nil {
				return nil, err
			}
		}
	}
	return dedupeEntries(result), nil
}

func (s *filesystemServiceServer) collectWorkspaceSnapshotFiles(ctx context.Context, workspaceID string) (map[string]string, error) {
	entries, err := s.collectWorkspaceEntries(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	files := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}
		content, err := s.storage.GetSliceFileByPath(ctx, workspaceID, entry.Path)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				continue
			}
			return nil, err
		}
		hash := strings.TrimSpace(content.Hash)
		if hash == "" {
			hash = hashContent(content.Content)
		}
		files[entry.Path] = hash
	}
	return files, nil
}

func dedupeEntries(entries []*models.DirectoryEntry) []*models.DirectoryEntry {
	if len(entries) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(entries))
	result := make([]*models.DirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		key := entry.ID + "|" + entry.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, entry)
	}
	return result
}

func entryToProto(entry *models.DirectoryEntry) *filesystemv1.WorkspaceEntry {
	if entry == nil {
		return nil
	}
	return &filesystemv1.WorkspaceEntry{
		Name: path.Base(entry.Path),
		Path: entry.Path,
		Type: entryTypeToProto(entry.Type),
		Size: entry.Size,
		Hash: strings.TrimSpace(entry.Hash),
	}
}

func entryTypeToProto(value string) filesystemv1.EntryType {
	switch strings.TrimSpace(value) {
	case "directory":
		return filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY
	case "file":
		return filesystemv1.EntryType_ENTRY_TYPE_FILE
	default:
		return filesystemv1.EntryType_ENTRY_TYPE_UNSPECIFIED
	}
}

func validateWorkspacePath(raw string, required bool) (string, error) {
	cleaned := common.CleanRelativePath(raw)
	if cleaned == "" {
		if required {
			return "", status.Error(codes.InvalidArgument, "path is required")
		}
		return "", nil
	}
	if err := common.ValidateFilePath(cleaned); err != nil {
		return "", status.Error(codes.InvalidArgument, fmt.Sprintf("invalid path: %v", err))
	}
	return cleaned, nil
}

func canViewWorkspace(workspace *models.Slice, username string) bool {
	if workspace == nil {
		return false
	}
	if workspace.IsRoot {
		return true
	}
	return canWriteWorkspace(workspace, username)
}

func canWriteWorkspace(workspace *models.Slice, username string) bool {
	if workspace == nil || username == "" {
		return false
	}
	if workspace.CreatedBy == username {
		return true
	}
	for _, owner := range workspace.Owners {
		if owner == username {
			return true
		}
	}
	return false
}

func slugifyWorkspaceID(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func workspaceFileContentID(workspaceID, filePath string) string {
	return common.GenerateEntryID(workspaceID, filePath)
}
