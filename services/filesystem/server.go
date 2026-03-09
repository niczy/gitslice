package filesystemservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	"github.com/pmezard/go-difflib/difflib"
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
	if err := s.createWorkspaceShell(ctx, workspace, "create workspace"); err != nil {
		switch {
		case errors.Is(err, storage.ErrSliceAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "workspace already exists")
		case errors.Is(err, storage.ErrInvalidInput):
			return nil, status.Error(codes.InvalidArgument, "invalid workspace")
		default:
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create workspace: %v", err))
		}
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

	content, err := s.readWorkspaceFileContent(ctx, workspace.ID, filePath)
	if err != nil {
		return nil, err
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
	hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, filePath, content)
	if err != nil {
		return nil, err
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("write %s", filePath))
	if err != nil {
		return nil, err
	}
	return &filesystemv1.WriteFileResponse{
		WorkspaceId: workspace.ID,
		Path:        filePath,
		Size:        size,
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

	if err := s.deleteWorkspaceFile(ctx, workspace.ID, filePath); err != nil {
		return nil, err
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

func (s *filesystemServiceServer) MoveFile(ctx context.Context, req *filesystemv1.MoveFileRequest) (*filesystemv1.MoveFileResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	sourcePath, err := validateWorkspacePath(req.GetSourcePath(), true)
	if err != nil {
		return nil, err
	}
	destinationPath, err := validateWorkspacePath(req.GetDestinationPath(), true)
	if err != nil {
		return nil, err
	}
	if sourcePath == destinationPath {
		return nil, status.Error(codes.InvalidArgument, "source_path and destination_path must differ")
	}

	content, err := s.readWorkspaceFileContent(ctx, workspace.ID, sourcePath)
	if err != nil {
		return nil, err
	}
	if err := s.ensureWorkspaceFileTarget(ctx, workspace.ID, destinationPath); err != nil {
		return nil, err
	}

	if _, _, err := s.writeWorkspaceFileContent(ctx, workspace, destinationPath, append([]byte(nil), content.Content...)); err != nil {
		return nil, err
	}
	if err := s.deleteWorkspaceFile(ctx, workspace.ID, sourcePath); err != nil {
		return nil, err
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("move %s -> %s", sourcePath, destinationPath))
	if err != nil {
		return nil, err
	}
	return &filesystemv1.MoveFileResponse{
		WorkspaceId:     workspace.ID,
		SourcePath:      sourcePath,
		DestinationPath: destinationPath,
		CommitHash:      commitHash,
	}, nil
}

func (s *filesystemServiceServer) CopyFile(ctx context.Context, req *filesystemv1.CopyFileRequest) (*filesystemv1.CopyFileResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	sourcePath, err := validateWorkspacePath(req.GetSourcePath(), true)
	if err != nil {
		return nil, err
	}
	destinationPath, err := validateWorkspacePath(req.GetDestinationPath(), true)
	if err != nil {
		return nil, err
	}
	if sourcePath == destinationPath {
		return nil, status.Error(codes.InvalidArgument, "source_path and destination_path must differ")
	}

	content, err := s.readWorkspaceFileContent(ctx, workspace.ID, sourcePath)
	if err != nil {
		return nil, err
	}
	if err := s.ensureWorkspaceFileTarget(ctx, workspace.ID, destinationPath); err != nil {
		return nil, err
	}

	hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, destinationPath, append([]byte(nil), content.Content...))
	if err != nil {
		return nil, err
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("copy %s -> %s", sourcePath, destinationPath))
	if err != nil {
		return nil, err
	}
	return &filesystemv1.CopyFileResponse{
		WorkspaceId:     workspace.ID,
		SourcePath:      sourcePath,
		DestinationPath: destinationPath,
		Size:            size,
		Hash:            hash,
		CommitHash:      commitHash,
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

func (s *filesystemServiceServer) ReadFiles(ctx context.Context, req *filesystemv1.ReadFilesRequest) (*filesystemv1.ReadFilesResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	response := &filesystemv1.ReadFilesResponse{
		WorkspaceId: workspace.ID,
		Files:       make([]*filesystemv1.ReadFileResult, 0, len(req.GetPaths())),
	}
	for _, rawPath := range req.GetPaths() {
		filePath, err := validateWorkspacePath(rawPath, true)
		if err != nil {
			return nil, err
		}

		result := &filesystemv1.ReadFileResult{Path: filePath}
		content, err := s.readWorkspaceFileContent(ctx, workspace.ID, filePath)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				result.Error = "file not found"
				response.Files = append(response.Files, result)
				continue
			}
			return nil, err
		}

		hash := strings.TrimSpace(content.Hash)
		if hash == "" {
			hash = hashContent(content.Content)
		}
		result.Content = append([]byte(nil), content.Content...)
		result.Size = int64(len(content.Content))
		result.Hash = hash
		result.Found = true
		response.Files = append(response.Files, result)
	}
	return response, nil
}

func (s *filesystemServiceServer) WriteFiles(ctx context.Context, req *filesystemv1.WriteFilesRequest) (*filesystemv1.WriteFilesResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	if len(req.GetFiles()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "files is required")
	}

	type preparedWrite struct {
		path    string
		content []byte
	}

	prepared := make([]preparedWrite, 0, len(req.GetFiles()))
	seen := make(map[string]struct{}, len(req.GetFiles()))
	for _, file := range req.GetFiles() {
		if file == nil {
			return nil, status.Error(codes.InvalidArgument, "files must not contain null items")
		}

		filePath, err := validateWorkspacePath(file.GetPath(), true)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[filePath]; ok {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("duplicate file path %q", filePath))
		}
		if err := s.ensureWorkspaceFileTarget(ctx, workspace.ID, filePath); err != nil {
			return nil, err
		}

		seen[filePath] = struct{}{}
		prepared = append(prepared, preparedWrite{
			path:    filePath,
			content: append([]byte(nil), file.GetContent()...),
		})
	}

	response := &filesystemv1.WriteFilesResponse{
		WorkspaceId: workspace.ID,
		Files:       make([]*filesystemv1.WriteFileResult, 0, len(prepared)),
	}
	for _, file := range prepared {
		hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, file.path, file.content)
		if err != nil {
			return nil, err
		}
		response.Files = append(response.Files, &filesystemv1.WriteFileResult{
			Path: file.path,
			Size: size,
			Hash: hash,
		})
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, fmt.Sprintf("write %d files", len(prepared)))
	if err != nil {
		return nil, err
	}
	response.CommitHash = commitHash
	return response, nil
}

func (s *filesystemServiceServer) Glob(ctx context.Context, req *filesystemv1.GlobRequest) (*filesystemv1.GlobResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	pattern, err := validateGlobPattern(req.GetPattern(), true)
	if err != nil {
		return nil, err
	}

	entries, err := s.collectWorkspaceEntries(ctx, workspace.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace entries: %v", err))
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}
		if globMatch(pattern, entry.Path) {
			paths = append(paths, entry.Path)
		}
	}
	sort.Strings(paths)

	return &filesystemv1.GlobResponse{
		WorkspaceId: workspace.ID,
		Pattern:     pattern,
		Paths:       paths,
	}, nil
}

func (s *filesystemServiceServer) Search(ctx context.Context, req *filesystemv1.SearchRequest) (*filesystemv1.SearchResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	globPattern, err := validateGlobPattern(req.GetGlob(), false)
	if err != nil {
		return nil, err
	}

	entries, err := s.collectWorkspaceEntries(ctx, workspace.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace entries: %v", err))
	}

	matches := make([]*filesystemv1.SearchMatch, 0)
	for _, entry := range entries {
		if entry == nil || entry.Type != "file" {
			continue
		}
		if globPattern != "" && !globMatch(globPattern, entry.Path) {
			continue
		}

		content, err := s.readWorkspaceFileContent(ctx, workspace.ID, entry.Path)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				continue
			}
			return nil, err
		}
		for _, match := range findSearchMatches(entry.Path, string(content.Content), query) {
			matches = append(matches, match)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].GetPath() != matches[j].GetPath() {
			return matches[i].GetPath() < matches[j].GetPath()
		}
		if matches[i].GetLineNumber() != matches[j].GetLineNumber() {
			return matches[i].GetLineNumber() < matches[j].GetLineNumber()
		}
		return matches[i].GetMatchStart() < matches[j].GetMatchStart()
	})

	return &filesystemv1.SearchResponse{
		WorkspaceId: workspace.ID,
		Query:       query,
		Glob:        globPattern,
		Matches:     matches,
	}, nil
}

func (s *filesystemServiceServer) Snapshot(ctx context.Context, req *filesystemv1.SnapshotRequest) (*filesystemv1.SnapshotResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	message := strings.TrimSpace(req.GetMessage())
	if message == "" {
		message = "snapshot"
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, message)
	if err != nil {
		return nil, err
	}
	snapshotInfo, err := s.snapshotInfoByCommitHash(ctx, workspace.ID, commitHash)
	if err != nil {
		return nil, err
	}

	return &filesystemv1.SnapshotResponse{
		WorkspaceId: workspace.ID,
		Snapshot:    snapshotInfo,
	}, nil
}

func (s *filesystemServiceServer) ListSnapshots(ctx context.Context, req *filesystemv1.ListSnapshotsRequest) (*filesystemv1.ListSnapshotsResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	limit := int(req.GetLimit())
	if limit < 0 {
		return nil, status.Error(codes.InvalidArgument, "limit must be >= 0")
	}
	if limit == 0 {
		limit = 50
	}
	fromSnapshotID := strings.TrimSpace(req.GetFromSnapshotId())
	if fromSnapshotID != "" {
		if _, err := s.storage.GetCommitByHash(ctx, workspace.ID, fromSnapshotID); err != nil {
			if err == storage.ErrCommitNotFound {
				return nil, status.Error(codes.NotFound, "snapshot not found")
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot cursor: %v", err))
		}
	}

	commits, err := s.storage.ListSliceCommits(ctx, workspace.ID, limit, fromSnapshotID)
	if err != nil {
		if err == storage.ErrSliceNotFound {
			return nil, status.Error(codes.NotFound, "workspace not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list snapshots: %v", err))
	}

	response := &filesystemv1.ListSnapshotsResponse{
		WorkspaceId: workspace.ID,
		Snapshots:   make([]*filesystemv1.SnapshotInfo, 0, len(commits)),
	}
	for _, commit := range commits {
		info, err := s.snapshotInfoFromCommit(ctx, workspace.ID, commit)
		if err != nil {
			return nil, err
		}
		response.Snapshots = append(response.Snapshots, info)
	}
	return response, nil
}

func (s *filesystemServiceServer) RestoreSnapshot(ctx context.Context, req *filesystemv1.RestoreSnapshotRequest) (*filesystemv1.RestoreSnapshotResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	snapshotID := strings.TrimSpace(req.GetSnapshotId())
	if snapshotID == "" {
		return nil, status.Error(codes.InvalidArgument, "snapshot_id is required")
	}

	targetSnapshot, err := s.storage.GetCommitSnapshot(ctx, snapshotID)
	if err != nil {
		if err == storage.ErrCommitNotFound {
			return nil, status.Error(codes.NotFound, "snapshot not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot: %v", err))
	}
	if targetSnapshot.SliceID != workspace.ID {
		return nil, status.Error(codes.FailedPrecondition, "snapshot does not belong to workspace")
	}

	if err := s.resetWorkspaceToSnapshot(ctx, workspace, targetSnapshot); err != nil {
		return nil, err
	}

	message := strings.TrimSpace(req.GetMessage())
	if message == "" {
		message = fmt.Sprintf("restore snapshot %s", snapshotID)
	}

	commitHash, err := s.commitWorkspaceMutation(ctx, workspace, message)
	if err != nil {
		return nil, err
	}
	snapshotInfo, err := s.snapshotInfoByCommitHash(ctx, workspace.ID, commitHash)
	if err != nil {
		return nil, err
	}

	return &filesystemv1.RestoreSnapshotResponse{
		WorkspaceId:        workspace.ID,
		RestoredSnapshotId: snapshotID,
		Snapshot:           snapshotInfo,
	}, nil
}

func (s *filesystemServiceServer) Diff(ctx context.Context, req *filesystemv1.DiffRequest) (*filesystemv1.DiffResponse, error) {
	_, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, meta, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	toSnapshotID := strings.TrimSpace(req.GetToSnapshotId())
	if toSnapshotID == "" {
		toSnapshotID = strings.TrimSpace(meta.HeadCommitHash)
	}
	if toSnapshotID == "" {
		return nil, status.Error(codes.NotFound, "snapshot not found")
	}

	toCommit, toSnapshot, err := s.resolveWorkspaceSnapshot(ctx, workspace.ID, toSnapshotID, false)
	if err != nil {
		return nil, err
	}

	fromSnapshotID := strings.TrimSpace(req.GetFromSnapshotId())
	if fromSnapshotID == "" && toCommit != nil {
		fromSnapshotID = strings.TrimSpace(toCommit.ParentHash)
	}

	_, fromSnapshot, err := s.resolveWorkspaceSnapshot(ctx, workspace.ID, fromSnapshotID, true)
	if err != nil {
		return nil, err
	}

	files, summary, err := s.buildFilesystemDiff(ctx, fromSnapshot, toSnapshot, req.GetIncludePatches())
	if err != nil {
		return nil, err
	}

	return &filesystemv1.DiffResponse{
		WorkspaceId:    workspace.ID,
		FromSnapshotId: fromSnapshotID,
		ToSnapshotId:   toSnapshotID,
		Summary:        summary,
		Files:          files,
	}, nil
}

func (s *filesystemServiceServer) Fork(ctx context.Context, req *filesystemv1.ForkRequest) (*filesystemv1.ForkResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	sourceWorkspace, sourceMeta, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	sourceSnapshotID := strings.TrimSpace(req.GetSnapshotId())
	if sourceSnapshotID == "" {
		sourceSnapshotID = strings.TrimSpace(sourceMeta.HeadCommitHash)
	}
	if sourceSnapshotID == "" {
		return nil, status.Error(codes.NotFound, "snapshot not found")
	}

	_, sourceSnapshot, err := s.resolveWorkspaceSnapshot(ctx, sourceWorkspace.ID, sourceSnapshotID, false)
	if err != nil {
		return nil, err
	}

	forkWorkspaceID := strings.TrimSpace(req.GetForkWorkspaceId())
	if forkWorkspaceID == "" {
		forkWorkspaceID = slugifyWorkspaceID(req.GetName())
	}
	if forkWorkspaceID == "" {
		forkWorkspaceID = fmt.Sprintf("%s-fork", sourceWorkspace.ID)
	}
	if err := common.ValidateSliceID(forkWorkspaceID); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid fork_workspace_id: %v", err))
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		baseName := strings.TrimSpace(sourceWorkspace.Name)
		if baseName == "" {
			baseName = sourceWorkspace.ID
		}
		name = baseName + " Fork"
	}

	description := strings.TrimSpace(req.GetDescription())
	if description == "" {
		description = fmt.Sprintf("Fork of %s", sourceWorkspace.ID)
	}

	forkWorkspace := &models.Slice{
		ID:          forkWorkspaceID,
		Name:        name,
		Description: description,
		Owners:      []string{username},
		CreatedBy:   username,
		Files:       []string{},
	}
	if err := s.createWorkspaceShell(ctx, forkWorkspace, "create workspace"); err != nil {
		switch {
		case errors.Is(err, storage.ErrSliceAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, "workspace already exists")
		case errors.Is(err, storage.ErrInvalidInput):
			return nil, status.Error(codes.InvalidArgument, "invalid workspace")
		default:
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create fork workspace: %v", err))
		}
	}

	if err := s.resetWorkspaceToSnapshot(ctx, forkWorkspace, sourceSnapshot); err != nil {
		return nil, err
	}

	if _, err := s.commitWorkspaceMutation(ctx, forkWorkspace, fmt.Sprintf("fork from %s@%s", sourceWorkspace.ID, sourceSnapshotID)); err != nil {
		return nil, err
	}

	workspaceInfo, err := s.workspaceInfo(ctx, forkWorkspace)
	if err != nil {
		return nil, err
	}

	return &filesystemv1.ForkResponse{
		Workspace:         workspaceInfo,
		SourceWorkspaceId: sourceWorkspace.ID,
		SourceSnapshotId:  sourceSnapshotID,
	}, nil
}

func (s *filesystemServiceServer) readWorkspaceFileContent(ctx context.Context, workspaceID, filePath string) (*models.FileContent, error) {
	if _, err := s.requireWorkspaceFileEntry(ctx, workspaceID, filePath); err != nil {
		return nil, err
	}

	content, err := s.storage.GetSliceFileByPath(ctx, workspaceID, filePath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "file not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read file: %v", err))
	}
	return content, nil
}

func (s *filesystemServiceServer) requireWorkspaceFileEntry(ctx context.Context, workspaceID, filePath string) (*models.DirectoryEntry, error) {
	entry, err := s.storage.GetEntryByPath(ctx, workspaceID, filePath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "file not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file entry: %v", err))
	}
	if entry.Type != "file" {
		return nil, status.Error(codes.FailedPrecondition, "path is not a file")
	}
	return entry, nil
}

func (s *filesystemServiceServer) ensureWorkspaceFileTarget(ctx context.Context, workspaceID, filePath string) error {
	entry, err := s.storage.GetEntryByPath(ctx, workspaceID, filePath)
	if err == nil {
		if entry.Type != "file" {
			return status.Error(codes.FailedPrecondition, "destination path is not a file")
		}
		return nil
	}
	if err == storage.ErrEntryNotFound {
		return nil
	}
	return status.Error(codes.Internal, fmt.Sprintf("failed to validate destination path: %v", err))
}

func (s *filesystemServiceServer) writeWorkspaceFileContent(ctx context.Context, workspace *models.Slice, filePath string, content []byte) (string, int64, error) {
	if workspace == nil {
		return "", 0, status.Error(codes.Internal, "workspace is nil")
	}
	if err := s.ensureWorkspaceFileTarget(ctx, workspace.ID, filePath); err != nil {
		return "", 0, err
	}

	hash := hashContent(content)
	size := int64(len(content))
	fileContentID := workspaceFileContentID(workspace.ID, filePath)
	if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(workspace.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: workspace.ID,
		Size:     size,
	}); err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to write file entry: %v", err))
	}
	if err := s.storage.AddFileContent(ctx, &models.FileContent{
		FileID:  fileContentID,
		Path:    filePath,
		Content: append([]byte(nil), content...),
		Size:    size,
		Hash:    hash,
	}); err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to persist file content: %v", err))
	}
	if err := s.storage.AddFileToSlice(ctx, filePath, workspace.ID); err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to update workspace file index: %v", err))
	}
	return hash, size, nil
}

func (s *filesystemServiceServer) deleteWorkspaceFile(ctx context.Context, workspaceID, filePath string) error {
	entry, err := s.requireWorkspaceFileEntry(ctx, workspaceID, filePath)
	if err != nil {
		return err
	}

	if err := s.storage.DeleteEntry(ctx, entry.ID); err != nil && err != storage.ErrEntryNotFound {
		return status.Error(codes.Internal, fmt.Sprintf("failed to delete file entry: %v", err))
	}
	if err := s.storage.RemoveFileFromSlice(ctx, filePath, workspaceID); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to update workspace file index: %v", err))
	}
	return nil
}

func validateGlobPattern(raw string, required bool) (string, error) {
	pattern := strings.TrimSpace(raw)
	if pattern == "" {
		if required {
			return "", status.Error(codes.InvalidArgument, "pattern is required")
		}
		return "", nil
	}
	if strings.Contains(pattern, "\x00") {
		return "", status.Error(codes.InvalidArgument, "pattern contains null byte")
	}
	if strings.HasPrefix(pattern, "/") {
		return "", status.Error(codes.InvalidArgument, "pattern must be relative")
	}
	if strings.Contains(pattern, "..") {
		return "", status.Error(codes.InvalidArgument, "pattern must not contain '..'")
	}

	lowerPattern := strings.ToLower(pattern)
	for _, suspicious := range []string{"/etc/", "/proc/", "/sys/", "/dev/", "/root/", "~"} {
		if strings.Contains(lowerPattern, suspicious) {
			return "", status.Error(codes.InvalidArgument, "pattern contains suspicious path segment")
		}
	}

	cleaned := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(pattern, "\\", "/")), "/")
	if cleaned == "" || cleaned == "." {
		return "", status.Error(codes.InvalidArgument, "pattern is required")
	}

	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "**" {
			continue
		}
		if _, err := path.Match(segment, segment); err != nil {
			return "", status.Error(codes.InvalidArgument, fmt.Sprintf("invalid pattern: %v", err))
		}
	}
	return cleaned, nil
}

func globMatch(pattern, candidate string) bool {
	pattern = strings.TrimSpace(pattern)
	candidate = common.CleanRelativePath(candidate)
	if pattern == "" || candidate == "" {
		return false
	}
	return matchGlobSegments(strings.Split(pattern, "/"), strings.Split(candidate, "/"))
}

func matchGlobSegments(patternSegments, candidateSegments []string) bool {
	if len(patternSegments) == 0 {
		return len(candidateSegments) == 0
	}

	current := patternSegments[0]
	if current == "**" {
		if matchGlobSegments(patternSegments[1:], candidateSegments) {
			return true
		}
		if len(candidateSegments) == 0 {
			return false
		}
		return matchGlobSegments(patternSegments, candidateSegments[1:])
	}

	if len(candidateSegments) == 0 {
		return false
	}

	matched, err := path.Match(current, candidateSegments[0])
	if err != nil || !matched {
		return false
	}
	return matchGlobSegments(patternSegments[1:], candidateSegments[1:])
}

func findSearchMatches(filePath, body, query string) []*filesystemv1.SearchMatch {
	lines := strings.Split(body, "\n")
	results := make([]*filesystemv1.SearchMatch, 0)
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		searchStart := 0
		for {
			matchOffset := strings.Index(line[searchStart:], query)
			if matchOffset < 0 {
				break
			}
			matchStart := searchStart + matchOffset
			results = append(results, &filesystemv1.SearchMatch{
				Path:       filePath,
				LineNumber: int32(i + 1),
				Line:       line,
				MatchStart: int32(matchStart),
				MatchEnd:   int32(matchStart + len(query)),
			})
			searchStart = matchStart + 1
			if searchStart > len(line) {
				break
			}
		}
	}
	return results
}

func (s *filesystemServiceServer) snapshotInfoByCommitHash(ctx context.Context, workspaceID, commitHash string) (*filesystemv1.SnapshotInfo, error) {
	commit, err := s.storage.GetCommitByHash(ctx, workspaceID, commitHash)
	if err != nil {
		if err == storage.ErrCommitNotFound {
			return nil, status.Error(codes.NotFound, "snapshot not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot commit: %v", err))
	}
	return s.snapshotInfoFromCommit(ctx, workspaceID, commit)
}

func (s *filesystemServiceServer) snapshotInfoFromCommit(ctx context.Context, workspaceID string, commit *models.Commit) (*filesystemv1.SnapshotInfo, error) {
	if commit == nil {
		return nil, status.Error(codes.Internal, "commit is nil")
	}

	fileCount := 0
	snapshot, err := s.storage.GetCommitSnapshot(ctx, commit.CommitHash)
	if err != nil {
		if err != storage.ErrCommitNotFound {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot state: %v", err))
		}
	} else {
		if snapshot.SliceID != "" && snapshot.SliceID != workspaceID {
			return nil, status.Error(codes.FailedPrecondition, "snapshot does not belong to workspace")
		}
		fileCount = len(snapshot.Files)
	}

	return &filesystemv1.SnapshotInfo{
		SnapshotId:       commit.CommitHash,
		ParentSnapshotId: commit.ParentHash,
		Message:          commit.Message,
		CreatedAt:        commit.Timestamp.Unix(),
		FileCount:        int32(fileCount),
	}, nil
}

func (s *filesystemServiceServer) resetWorkspaceToSnapshot(ctx context.Context, workspace *models.Slice, snapshot *models.CommitSnapshot) error {
	if workspace == nil {
		return status.Error(codes.Internal, "workspace is nil")
	}
	if snapshot == nil {
		return status.Error(codes.Internal, "snapshot is nil")
	}

	entries, err := s.collectWorkspaceEntries(ctx, workspace.ID)
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace entries: %v", err))
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].Path) == len(entries[j].Path) {
			return entries[i].Path > entries[j].Path
		}
		return len(entries[i].Path) > len(entries[j].Path)
	})

	for _, entry := range entries {
		if entry == nil {
			continue
		}
		if entry.Type == "file" {
			if err := s.storage.RemoveFileFromSlice(ctx, entry.Path, workspace.ID); err != nil {
				return status.Error(codes.Internal, fmt.Sprintf("failed to clear workspace file index: %v", err))
			}
		}
		if err := s.storage.DeleteEntry(ctx, entry.ID); err != nil && err != storage.ErrEntryNotFound {
			return status.Error(codes.Internal, fmt.Sprintf("failed to clear workspace entry: %v", err))
		}
	}

	paths := make([]string, 0, len(snapshot.Files))
	for filePath := range snapshot.Files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	for _, filePath := range paths {
		contentHash := strings.TrimSpace(snapshot.Files[filePath])
		if contentHash == "" {
			continue
		}
		content, err := s.storage.GetFileContentByHash(ctx, contentHash)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return status.Error(codes.NotFound, fmt.Sprintf("snapshot content missing for %s", filePath))
			}
			return status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot content for %s: %v", filePath, err))
		}
		if _, _, err := s.writeWorkspaceFileContent(ctx, workspace, filePath, append([]byte(nil), content.Content...)); err != nil {
			return err
		}
	}

	return nil
}

func (s *filesystemServiceServer) resolveWorkspaceSnapshot(ctx context.Context, workspaceID, snapshotID string, allowEmpty bool) (*models.Commit, *models.CommitSnapshot, error) {
	if strings.TrimSpace(snapshotID) == "" {
		if allowEmpty {
			return nil, &models.CommitSnapshot{
				SliceID: workspaceID,
				Files:   map[string]string{},
			}, nil
		}
		return nil, nil, status.Error(codes.InvalidArgument, "snapshot_id is required")
	}

	commit, err := s.storage.GetCommitByHash(ctx, workspaceID, snapshotID)
	if err != nil {
		if err == storage.ErrCommitNotFound {
			return nil, nil, status.Error(codes.NotFound, "snapshot not found")
		}
		return nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot commit: %v", err))
	}

	snapshot, err := s.storage.GetCommitSnapshot(ctx, snapshotID)
	if err != nil {
		if err == storage.ErrCommitNotFound {
			return nil, nil, status.Error(codes.NotFound, "snapshot not found")
		}
		return nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to load snapshot state: %v", err))
	}
	if snapshot.SliceID != "" && snapshot.SliceID != workspaceID {
		return nil, nil, status.Error(codes.FailedPrecondition, "snapshot does not belong to workspace")
	}
	if snapshot.Files == nil {
		snapshot.Files = map[string]string{}
	}
	return commit, snapshot, nil
}

func (s *filesystemServiceServer) buildFilesystemDiff(ctx context.Context, fromSnapshot, toSnapshot *models.CommitSnapshot, includePatches bool) ([]*filesystemv1.FileDiff, *filesystemv1.DiffSummary, error) {
	if fromSnapshot == nil {
		fromSnapshot = &models.CommitSnapshot{Files: map[string]string{}}
	}
	if toSnapshot == nil {
		toSnapshot = &models.CommitSnapshot{Files: map[string]string{}}
	}
	if fromSnapshot.Files == nil {
		fromSnapshot.Files = map[string]string{}
	}
	if toSnapshot.Files == nil {
		toSnapshot.Files = map[string]string{}
	}

	pathSet := make(map[string]struct{}, len(fromSnapshot.Files)+len(toSnapshot.Files))
	for filePath := range fromSnapshot.Files {
		pathSet[filePath] = struct{}{}
	}
	for filePath := range toSnapshot.Files {
		pathSet[filePath] = struct{}{}
	}

	paths := make([]string, 0, len(pathSet))
	for filePath := range pathSet {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	summary := &filesystemv1.DiffSummary{}
	files := make([]*filesystemv1.FileDiff, 0, len(paths))
	for _, filePath := range paths {
		oldHash := strings.TrimSpace(fromSnapshot.Files[filePath])
		newHash := strings.TrimSpace(toSnapshot.Files[filePath])
		if oldHash == newHash {
			continue
		}

		fileDiff := &filesystemv1.FileDiff{
			Path:    filePath,
			OldHash: oldHash,
			NewHash: newHash,
		}
		switch {
		case oldHash == "":
			fileDiff.ChangeType = filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_ADD
			summary.FilesAdded++
		case newHash == "":
			fileDiff.ChangeType = filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_DELETE
			summary.FilesDeleted++
		default:
			fileDiff.ChangeType = filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY
			summary.FilesModified++
		}

		patch, linesAdded, linesDeleted := s.buildFilesystemDiffPatch(ctx, filePath, oldHash, newHash)
		fileDiff.LinesAdded = int32(linesAdded)
		fileDiff.LinesDeleted = int32(linesDeleted)
		if includePatches {
			fileDiff.Patch = patch
		}
		summary.LinesAdded += int32(linesAdded)
		summary.LinesDeleted += int32(linesDeleted)
		files = append(files, fileDiff)
	}

	return files, summary, nil
}

func (s *filesystemServiceServer) buildFilesystemDiffPatch(ctx context.Context, filePath, oldHash, newHash string) (string, int, int) {
	beforeLines, beforeOK := s.loadFilesystemDiffLines(ctx, oldHash)
	if !beforeOK {
		return "", 0, 0
	}
	afterLines, afterOK := s.loadFilesystemDiffLines(ctx, newHash)
	if !afterOK {
		return "", 0, 0
	}

	if len(beforeLines) == 0 && len(afterLines) == 0 {
		return "", 0, 0
	}

	patch, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: "a/" + filePath,
		ToFile:   "b/" + filePath,
		Context:  3,
	})
	if err != nil {
		return "", 0, 0
	}
	linesAdded, linesDeleted := summarizePatchLineDelta(patch)
	return patch, linesAdded, linesDeleted
}

func (s *filesystemServiceServer) loadFilesystemDiffLines(ctx context.Context, contentHash string) ([]string, bool) {
	cleaned := strings.TrimSpace(contentHash)
	if cleaned == "" {
		return []string{}, true
	}

	content, err := s.storage.GetFileContentByHash(ctx, cleaned)
	if err != nil || content == nil {
		return nil, false
	}
	return splitLinesForDiff(content.Content)
}

func splitLinesForDiff(content []byte) ([]string, bool) {
	if len(content) == 0 {
		return []string{}, true
	}
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
	return bytes.IndexByte(content, 0) >= 0
}

func summarizePatchLineDelta(patch string) (added int, deleted int) {
	if strings.TrimSpace(patch) == "" {
		return 0, 0
	}
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			deleted++
		}
	}
	return added, deleted
}

func (s *filesystemServiceServer) createWorkspaceShell(ctx context.Context, workspace *models.Slice, initialMessage string) error {
	if err := s.storage.CreateSlice(ctx, workspace); err != nil {
		return err
	}

	meta, err := s.storage.GetSliceMetadata(ctx, workspace.ID)
	if err != nil {
		return fmt.Errorf("load workspace metadata: %w", err)
	}

	message := strings.TrimSpace(initialMessage)
	if message == "" {
		message = "create workspace"
	}

	if err := s.storage.AddSliceCommit(ctx, workspace.ID, &models.Commit{
		CommitHash: meta.HeadCommitHash,
		ParentHash: "",
		Timestamp:  workspace.CreatedAt,
		Message:    message,
	}); err != nil {
		return fmt.Errorf("record initial workspace commit: %w", err)
	}

	if err := s.storage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: meta.HeadCommitHash,
		SliceID:    workspace.ID,
		Files:      map[string]string{},
		Timestamp:  workspace.CreatedAt,
	}); err != nil {
		return fmt.Errorf("save initial workspace snapshot: %w", err)
	}

	return nil
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
