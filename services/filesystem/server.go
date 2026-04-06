package filesystemservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/niczy/gitslice/internal/authresolver"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/rootpromote"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
	"github.com/niczy/gitslice/internal/visibility"
	commonv1 "github.com/niczy/gitslice/proto/common"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	"github.com/pmezard/go-difflib/difflib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type filesystemServiceServer struct {
	filesystemv1.UnimplementedFilesystemServiceServer
	storage               storage.Storage
	promotionQueueMu      sync.Mutex
	promotionQueue        *rootpromote.Queue
	promotionBatchWindow  time.Duration
	promotionBatchMaxSize int
}

const (
	defaultFilesystemStreamChunkSize = 256 * 1024
	maxFilesystemStreamChunkSize     = 1024 * 1024
)

type readFileOptions struct {
	byteOffset int64
	byteLimit  int64
	lineOffset int64
	lineLimit  int64
	byteRange  bool
	lineRange  bool
}

type preparedFilesystemEdit struct {
	path        string
	displayPath string
	updated     []byte
}

type preparedFilesystemWrite struct {
	path        string
	displayPath string
	content     []byte
}

type preparedFilesystemUpload struct {
	path        string
	displayPath string
	manifest    *models.FileManifest
	unchanged   bool
}

func newFilesystemServiceServer(st storage.Storage) *filesystemServiceServer {
	return &filesystemServiceServer{
		storage:               st,
		promotionBatchWindow:  rootpromote.DefaultBatchWindow,
		promotionBatchMaxSize: rootpromote.DefaultBatchMaxSize,
	}
}

// RegisterGRPCServer registers the filesystem service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	filesystemv1.RegisterFilesystemServiceServer(srv, newFilesystemServiceServer(st))
}

// NewService constructs the filesystem service implementation for use without gRPC.
func NewService(st storage.Storage) filesystemv1.FilesystemServiceServer {
	return newFilesystemServiceServer(st)
}

func modelVisibilityToFilesystemProto(v models.Visibility) commonv1.Visibility {
	switch models.NormalizeVisibility(v) {
	case models.VisibilityPublic:
		return commonv1.Visibility_VISIBILITY_PUBLIC
	default:
		return commonv1.Visibility_VISIBILITY_PRIVATE
	}
}

func filesystemProtoToModelVisibility(v commonv1.Visibility) models.Visibility {
	switch v {
	case commonv1.Visibility_VISIBILITY_PUBLIC:
		return models.VisibilityPublic
	default:
		return models.VisibilityPrivate
	}
}

func displayPathFromVisibilityRule(homeMode bool, normalizedPath string) string {
	if homeMode {
		return visibility.NormalizePath(normalizedPath)
	}
	return common.CleanRelativePath(strings.TrimPrefix(normalizedPath, "/"))
}

func storedPathFromVisibilityRule(homeMode bool, normalizedPath string) string {
	if homeMode {
		return strings.TrimPrefix(visibility.NormalizePath(normalizedPath), "/")
	}
	return common.CleanRelativePath(strings.TrimPrefix(normalizedPath, "/"))
}

func (s *filesystemServiceServer) workspacePathExists(ctx context.Context, workspaceID, storedPath string) (bool, bool, error) {
	storedPath = strings.TrimSpace(storedPath)
	if storedPath == "" {
		return true, true, nil
	}

	entry, err := s.storage.GetEntryByPath(ctx, workspaceID, storedPath)
	if err == nil && entry != nil {
		return true, entry.Type == "directory", nil
	}
	if err != nil && err != storage.ErrEntryNotFound {
		return false, false, err
	}

	entries, err := s.collectWorkspaceEntries(ctx, workspaceID)
	if err != nil {
		return false, false, err
	}
	prefix := storedPath + "/"
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		if strings.HasPrefix(entry.Path, prefix) {
			return true, true, nil
		}
	}
	return false, false, nil
}

func (s *filesystemServiceServer) workspaceDirectoryHasPublicDescendant(ctx context.Context, workspace *models.Slice, homeMode bool, displayPath string) (bool, string, error) {
	prefix := ""
	normalizedDisplayPath := displayPathFromVisibilityRule(homeMode, displayPath)
	if normalizedDisplayPath != "" {
		prefix = visibility.NormalizePath(normalizedDisplayPath) + "/"
	}

	rules, err := s.storage.ListPathVisibilityRules(ctx, prefix)
	if err != nil {
		return false, "", err
	}
	for _, rule := range rules {
		if rule == nil || !models.NormalizeVisibility(rule.Visibility).IsPublic() {
			continue
		}
		storedRulePath := storedPathFromVisibilityRule(homeMode, rule.Path)
		exists, _, err := s.workspacePathExists(ctx, workspace.ID, storedRulePath)
		if err != nil {
			return false, "", err
		}
		if exists {
			return true, rule.Path, nil
		}
	}
	return false, "", nil
}

func (s *filesystemServiceServer) buildGlobalPathVisibilityInfo(ctx context.Context, normalizedPath string) (*filesystemv1.PathVisibilityInfo, error) {
	resolution, err := visibility.Resolve(ctx, s.storage, nil, normalizedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve visibility: %v", err))
	}
	return &filesystemv1.PathVisibilityInfo{
		Path:                normalizedPath,
		Visibility:          modelVisibilityToFilesystemProto(resolution.Visibility),
		ExplicitRule:        resolution.ExplicitRule,
		ResolvedFromPath:    resolution.ResolvedFromPath,
		EffectiveVisibility: modelVisibilityToFilesystemProto(resolution.EffectiveVisibility),
	}, nil
}

func (s *filesystemServiceServer) buildWorkspacePathVisibilityInfo(ctx context.Context, workspace *models.Slice, homeMode bool, storedPath, displayPath string, isDirectory bool) (*filesystemv1.PathVisibilityInfo, error) {
	resolution, err := visibility.Resolve(ctx, s.storage, workspace, displayPathFromVisibilityRule(homeMode, displayPath))
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve visibility: %v", err))
	}

	effectiveVisibility := resolution.EffectiveVisibility
	resolvedFromPath := resolution.ResolvedFromPath
	if isDirectory && !effectiveVisibility.IsPublic() {
		hasDescendant, descendantPath, err := s.workspaceDirectoryHasPublicDescendant(ctx, workspace, homeMode, displayPath)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve visibility: %v", err))
		}
		if hasDescendant {
			effectiveVisibility = models.VisibilityPublic
			resolvedFromPath = descendantPath
		}
	}

	return &filesystemv1.PathVisibilityInfo{
		Path:                displayPath,
		Visibility:          modelVisibilityToFilesystemProto(resolution.Visibility),
		ExplicitRule:        resolution.ExplicitRule,
		ResolvedFromPath:    resolvedFromPath,
		EffectiveVisibility: modelVisibilityToFilesystemProto(effectiveVisibility),
	}, nil
}

func (s *filesystemServiceServer) canManageGlobalPathVisibility(ctx context.Context, username, normalizedPath string) (bool, error) {
	userRoot := visibility.NormalizePath(homeslice.VisibleRootPath(username))
	if userRoot != "" && (normalizedPath == userRoot || strings.HasPrefix(normalizedPath, userRoot+"/")) {
		return true, nil
	}

	ownedSlices, err := s.storage.ListSlicesByOwner(ctx, username, int(^uint(0)>>1), 0)
	if err != nil {
		return false, err
	}
	for _, candidate := range ownedSlices {
		if candidate == nil {
			continue
		}
		homeMode := candidate.ID == homeslice.IDForUsername(username)
		storedPath := storedPathFromVisibilityRule(homeMode, normalizedPath)
		exists, _, err := s.workspacePathExists(ctx, candidate.ID, storedPath)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func (s *filesystemServiceServer) inferPathVisibilityEntryType(ctx context.Context, username, normalizedPath string, recursive bool, rawPath string) (models.PathVisibilityEntryType, error) {
	if recursive || strings.HasSuffix(strings.TrimSpace(rawPath), "/") {
		return models.PathVisibilityEntryTypeDirectory, nil
	}

	ownedSlices, err := s.storage.ListSlicesByOwner(ctx, username, int(^uint(0)>>1), 0)
	if err != nil {
		return "", err
	}
	for _, candidate := range ownedSlices {
		if candidate == nil {
			continue
		}
		homeMode := candidate.ID == homeslice.IDForUsername(username)
		storedPath := storedPathFromVisibilityRule(homeMode, normalizedPath)
		exists, isDirectory, err := s.workspacePathExists(ctx, candidate.ID, storedPath)
		if err != nil {
			return "", err
		}
		if !exists {
			continue
		}
		if isDirectory {
			return models.PathVisibilityEntryTypeDirectory, nil
		}
		return models.PathVisibilityEntryTypeFile, nil
	}
	return models.PathVisibilityEntryTypeFile, nil
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
		Visibility:  models.VisibilityPrivate,
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

func (s *filesystemServiceServer) DeleteWorkspace(ctx context.Context, req *filesystemv1.DeleteWorkspaceRequest) (*filesystemv1.DeleteWorkspaceResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}
	if workspace.IsRoot {
		return nil, status.Error(codes.FailedPrecondition, "root workspace cannot be deleted")
	}

	activeSession, err := s.storage.GetActiveAgentSessionBySlice(ctx, workspace.ID)
	switch {
	case err == nil:
		return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("workspace has active agent session %s", activeSession.SessionID))
	case err != storage.ErrAgentSessionNotFound:
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect active agent session: %v", err))
	}

	if err := s.storage.DeleteSlice(ctx, workspace.ID); err != nil {
		switch err {
		case storage.ErrSliceNotFound:
			return nil, status.Error(codes.NotFound, "workspace not found")
		default:
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to delete workspace: %v", err))
		}
	}
	if err := s.storage.DeleteWorkspaceSearchArtifact(ctx, workspace.ID, searchindex.CurrentArtifactVersion); err != nil && err != storage.ErrEntryNotFound {
		log.Printf("filesystem: failed to delete workspace search artifact for %s: %v", workspace.ID, err)
	}

	return &filesystemv1.DeleteWorkspaceResponse{
		WorkspaceId: workspace.ID,
	}, nil
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

func (s *filesystemServiceServer) GetPathVisibility(ctx context.Context, req *filesystemv1.GetPathVisibilityRequest) (*filesystemv1.GetPathVisibilityResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	storedPath, displayPath, err := s.resolveOperationPath(username, homeMode, req.GetPath(), false)
	if err != nil {
		return nil, err
	}

	exists, isDirectory, err := s.workspacePathExists(ctx, workspace.ID, storedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect workspace path: %v", err))
	}
	if !exists && strings.TrimSpace(displayPath) != "" {
		return nil, status.Error(codes.NotFound, "path not found")
	}

	info, err := s.buildWorkspacePathVisibilityInfo(ctx, workspace, homeMode, storedPath, displayPath, isDirectory)
	if err != nil {
		return nil, err
	}
	return &filesystemv1.GetPathVisibilityResponse{
		WorkspaceId: workspace.ID,
		Visibility:  info,
	}, nil
}

func (s *filesystemServiceServer) SetPathVisibility(ctx context.Context, req *filesystemv1.SetPathVisibilityRequest) (*filesystemv1.SetPathVisibilityResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	normalizedPath := visibility.NormalizePath(req.GetPath())
	if normalizedPath == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	canManage, err := s.canManageGlobalPathVisibility(ctx, username, normalizedPath)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to validate path visibility access: %v", err))
	}
	if !canManage {
		return nil, status.Error(codes.NotFound, "path not found")
	}

	entryType, err := s.inferPathVisibilityEntryType(ctx, username, normalizedPath, req.GetRecursive(), req.GetPath())
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve path visibility type: %v", err))
	}

	if err := s.storage.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       normalizedPath,
		EntryType:  entryType,
		Visibility: filesystemProtoToModelVisibility(req.GetVisibility()),
		UpdatedBy:  username,
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to save path visibility: %v", err))
	}

	info, err := s.buildGlobalPathVisibilityInfo(ctx, normalizedPath)
	if err != nil {
		return nil, err
	}
	return &filesystemv1.SetPathVisibilityResponse{
		Visibility: info,
		Recursive:  req.GetRecursive(),
	}, nil
}

func (s *filesystemServiceServer) ReadFile(ctx context.Context, req *filesystemv1.ReadFileRequest) (*filesystemv1.ReadFileResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	readOpts, err := parseReadFileOptions(req)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	filePath, displayPath, err := s.resolveOperationPath(username, homeMode, req.GetPath(), true)
	if err != nil {
		return nil, err
	}

	content, size, hash, err := s.readWorkspaceFileSelection(ctx, workspace.ID, filePath, readOpts)
	if err != nil {
		return nil, err
	}

	return &filesystemv1.ReadFileResponse{
		WorkspaceId: workspace.ID,
		Path:        displayPath,
		Content:     content,
		Size:        size,
		Hash:        hash,
	}, nil
}

func (s *filesystemServiceServer) WriteFile(ctx context.Context, req *filesystemv1.WriteFileRequest) (*filesystemv1.WriteFileResponse, error) {
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

	content := append([]byte(nil), req.GetContent()...)
	hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, filePath, content)
	if err != nil {
		return nil, err
	}

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("write %s", displayPath), []string{filePath})
	if err != nil {
		return nil, err
	}
	return &filesystemv1.WriteFileResponse{
		WorkspaceId: workspace.ID,
		Path:        displayPath,
		Size:        size,
		Hash:        hash,
		CommitHash:  commitHash,
	}, nil
}

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

	prepared, err := s.prepareFilesystemEdit(ctx, workspace.ID, filePath, displayPath, req.GetExpectedHash(), req.GetEdits())
	if err != nil {
		return nil, err
	}

	hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, prepared.path, prepared.updated)
	if err != nil {
		return nil, err
	}

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("edit %s", prepared.displayPath), []string{prepared.path})
	if err != nil {
		return nil, err
	}
	return &filesystemv1.EditFileResponse{
		WorkspaceId: workspace.ID,
		Path:        prepared.displayPath,
		Size:        size,
		Hash:        hash,
		CommitHash:  commitHash,
	}, nil
}

func (s *filesystemServiceServer) EditFiles(ctx context.Context, req *filesystemv1.EditFilesRequest) (*filesystemv1.EditFilesResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}

	if len(req.GetFiles()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "files is required")
	}

	prepared := make([]preparedFilesystemEdit, 0, len(req.GetFiles()))
	seen := make(map[string]struct{}, len(req.GetFiles()))
	for _, file := range req.GetFiles() {
		if file == nil {
			return nil, status.Error(codes.InvalidArgument, "files must not contain null items")
		}

		filePath, displayPath, err := s.resolveOperationPath(username, homeMode, file.GetPath(), true)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[filePath]; ok {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("duplicate file path %q", filePath))
		}
		seen[filePath] = struct{}{}

		preparedEdit, err := s.prepareFilesystemEdit(ctx, workspace.ID, filePath, displayPath, file.GetExpectedHash(), file.GetEdits())
		if err != nil {
			return nil, annotateFilesystemEditError(displayPath, err)
		}
		prepared = append(prepared, *preparedEdit)
	}

	response := &filesystemv1.EditFilesResponse{
		WorkspaceId: workspace.ID,
		Files:       make([]*filesystemv1.EditFileResult, 0, len(prepared)),
	}
	for _, file := range prepared {
		hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, file.path, file.updated)
		if err != nil {
			return nil, err
		}
		response.Files = append(response.Files, &filesystemv1.EditFileResult{
			Path: file.displayPath,
			Size: size,
			Hash: hash,
		})
	}

	modifiedPaths := make([]string, 0, len(prepared))
	for _, file := range prepared {
		modifiedPaths = append(modifiedPaths, file.path)
	}
	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("edit %d files", len(prepared)), modifiedPaths)
	if err != nil {
		return nil, err
	}
	response.CommitHash = commitHash
	return response, nil
}

func (s *filesystemServiceServer) DeleteFile(ctx context.Context, req *filesystemv1.DeleteFileRequest) (*filesystemv1.DeleteFileResponse, error) {
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

	if err := s.deleteWorkspaceFile(ctx, workspace.ID, filePath); err != nil {
		return nil, err
	}

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("delete %s", displayPath), []string{filePath})
	if err != nil {
		return nil, err
	}
	return &filesystemv1.DeleteFileResponse{
		WorkspaceId: workspace.ID,
		Path:        displayPath,
		CommitHash:  commitHash,
	}, nil
}

func (s *filesystemServiceServer) MoveFile(ctx context.Context, req *filesystemv1.MoveFileRequest) (*filesystemv1.MoveFileResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}

	sourcePath, sourceDisplayPath, err := s.resolveOperationPath(username, homeMode, req.GetSourcePath(), true)
	if err != nil {
		return nil, err
	}
	destinationPath, destinationDisplayPath, err := s.resolveOperationPath(username, homeMode, req.GetDestinationPath(), true)
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

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("move %s -> %s", sourceDisplayPath, destinationDisplayPath), []string{sourcePath, destinationPath})
	if err != nil {
		return nil, err
	}
	return &filesystemv1.MoveFileResponse{
		WorkspaceId:     workspace.ID,
		SourcePath:      sourceDisplayPath,
		DestinationPath: destinationDisplayPath,
		CommitHash:      commitHash,
	}, nil
}

func (s *filesystemServiceServer) CopyFile(ctx context.Context, req *filesystemv1.CopyFileRequest) (*filesystemv1.CopyFileResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}

	sourcePath, sourceDisplayPath, err := s.resolveOperationPath(username, homeMode, req.GetSourcePath(), true)
	if err != nil {
		return nil, err
	}
	destinationPath, destinationDisplayPath, err := s.resolveOperationPath(username, homeMode, req.GetDestinationPath(), true)
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

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("copy %s -> %s", sourceDisplayPath, destinationDisplayPath), []string{sourcePath, destinationPath})
	if err != nil {
		return nil, err
	}
	return &filesystemv1.CopyFileResponse{
		WorkspaceId:     workspace.ID,
		SourcePath:      sourceDisplayPath,
		DestinationPath: destinationDisplayPath,
		Size:            size,
		Hash:            hash,
		CommitHash:      commitHash,
	}, nil
}

func (s *filesystemServiceServer) ListDirectory(ctx context.Context, req *filesystemv1.ListDirectoryRequest) (*filesystemv1.ListDirectoryResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	dirPath, displayPath, err := s.resolveOperationPath(username, homeMode, req.GetPath(), false)
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
		Path:        displayPath,
		Entries:     make([]*filesystemv1.WorkspaceEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		protoEntry := entryToProto(entry, homeMode)
		info, err := s.buildWorkspacePathVisibilityInfo(ctx, workspace, homeMode, entry.Path, protoEntry.GetPath(), entry.Type == "directory")
		if err != nil {
			return nil, err
		}
		protoEntry.EffectiveVisibility = info.GetEffectiveVisibility()
		response.Entries = append(response.Entries, protoEntry)
	}
	return response, nil
}

func (s *filesystemServiceServer) MakeDirectory(ctx context.Context, req *filesystemv1.MakeDirectoryRequest) (*filesystemv1.MakeDirectoryResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}

	dirPath, displayPath, err := s.resolveOperationPath(username, homeMode, req.GetPath(), true)
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

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("mkdir %s", displayPath), []string{dirPath})
	if err != nil {
		return nil, err
	}
	return &filesystemv1.MakeDirectoryResponse{
		WorkspaceId: workspace.ID,
		Path:        displayPath,
		CommitHash:  commitHash,
	}, nil
}

func (s *filesystemServiceServer) Stat(ctx context.Context, req *filesystemv1.StatRequest) (*filesystemv1.StatResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	statPath, _, err := s.resolveOperationPath(username, homeMode, req.GetPath(), false)
	if err != nil {
		return nil, err
	}
	if !homeMode && statPath == "" {
		rootEntry, err := s.storage.GetEntryByPath(ctx, workspace.ID, "")
		if err != nil && err != storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to stat path: %v", err))
		}
		info, err := s.buildWorkspacePathVisibilityInfo(ctx, workspace, homeMode, "", "", true)
		if err != nil {
			return nil, err
		}
		var size int64
		if rootEntry != nil {
			size = rootEntry.Size
		}
		return &filesystemv1.StatResponse{
			Exists: true,
			Entry: &filesystemv1.WorkspaceEntry{
				Name:                path.Base(workspace.ID),
				Path:                "",
				Type:                filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY,
				Size:                size,
				EffectiveVisibility: info.GetEffectiveVisibility(),
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
	protoEntry := entryToProto(entry, homeMode)
	info, err := s.buildWorkspacePathVisibilityInfo(ctx, workspace, homeMode, entry.Path, protoEntry.GetPath(), entry.Type == "directory")
	if err != nil {
		return nil, err
	}
	protoEntry.EffectiveVisibility = info.GetEffectiveVisibility()
	return &filesystemv1.StatResponse{
		Exists: true,
		Entry:  protoEntry,
	}, nil
}

func (s *filesystemServiceServer) Exists(ctx context.Context, req *filesystemv1.ExistsRequest) (*filesystemv1.ExistsResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	existsPath, _, err := s.resolveOperationPath(username, homeMode, req.GetPath(), false)
	if err != nil {
		return nil, err
	}
	if !homeMode && existsPath == "" {
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
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	response := &filesystemv1.ReadFilesResponse{
		WorkspaceId: workspace.ID,
		Files:       make([]*filesystemv1.ReadFileResult, 0, len(req.GetPaths())),
	}
	for _, rawPath := range req.GetPaths() {
		filePath, displayPath, err := s.resolveOperationPath(username, homeMode, rawPath, true)
		if err != nil {
			return nil, err
		}

		result := &filesystemv1.ReadFileResult{Path: displayPath}
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

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}

	if len(req.GetFiles()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "files is required")
	}

	prepared := make([]preparedFilesystemWrite, 0, len(req.GetFiles()))
	seen := make(map[string]struct{}, len(req.GetFiles()))
	for _, file := range req.GetFiles() {
		if file == nil {
			return nil, status.Error(codes.InvalidArgument, "files must not contain null items")
		}

		filePath, displayPath, err := s.resolveOperationPath(username, homeMode, file.GetPath(), true)
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
		prepared = append(prepared, preparedFilesystemWrite{
			path:        filePath,
			displayPath: displayPath,
			content:     append([]byte(nil), file.GetContent()...),
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
			Path: file.displayPath,
			Size: size,
			Hash: hash,
		})
	}

	modifiedPaths := make([]string, 0, len(prepared))
	for _, file := range prepared {
		modifiedPaths = append(modifiedPaths, file.path)
	}
	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("write %d files", len(prepared)), modifiedPaths)
	if err != nil {
		return nil, err
	}
	response.CommitHash = commitHash
	return response, nil
}

func (s *filesystemServiceServer) Batch(ctx context.Context, req *filesystemv1.BatchRequest) (*filesystemv1.BatchResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}
	if len(req.GetOperations()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "operations is required")
	}

	response := &filesystemv1.BatchResponse{
		WorkspaceId: workspace.ID,
		Results:     make([]*filesystemv1.BatchResult, 0, len(req.GetOperations())),
	}
	modifiedPaths := make([]string, 0, len(req.GetOperations())*2)
	for index, operation := range req.GetOperations() {
		if operation == nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("operations[%d] must not be null", index))
		}

		result, operationPaths, err := s.applyFilesystemBatchOperation(ctx, workspace, username, homeMode, operation)
		if err != nil {
			return nil, err
		}
		response.Results = append(response.Results, result)
		modifiedPaths = append(modifiedPaths, operationPaths...)
	}

	message := strings.TrimSpace(req.GetMessage())
	if message == "" {
		message = fmt.Sprintf("batch %d operations", len(req.GetOperations()))
	}
	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, message, modifiedPaths)
	if err != nil {
		return nil, err
	}
	response.CommitHash = commitHash
	return response, nil
}

func (s *filesystemServiceServer) applyFilesystemBatchOperation(ctx context.Context, workspace *models.Slice, username string, homeMode bool, operation *filesystemv1.BatchOperation) (*filesystemv1.BatchResult, []string, error) {
	if operation == nil {
		return nil, nil, status.Error(codes.InvalidArgument, "operation is required")
	}

	resultID := strings.TrimSpace(operation.GetId())
	switch spec := operation.Operation.(type) {
	case *filesystemv1.BatchOperation_Write:
		filePath, displayPath, err := s.resolveOperationPath(username, homeMode, spec.Write.GetPath(), true)
		if err != nil {
			return nil, nil, err
		}
		if err := s.ensureWorkspaceFileTarget(ctx, workspace.ID, filePath); err != nil {
			return nil, nil, err
		}
		hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, filePath, append([]byte(nil), spec.Write.GetContent()...))
		if err != nil {
			return nil, nil, err
		}
		return &filesystemv1.BatchResult{
			Id:     resultID,
			OpType: filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_WRITE,
			Path:   displayPath,
			Size:   size,
			Hash:   hash,
		}, []string{filePath}, nil
	case *filesystemv1.BatchOperation_Edit:
		filePath, displayPath, err := s.resolveOperationPath(username, homeMode, spec.Edit.GetPath(), true)
		if err != nil {
			return nil, nil, err
		}
		prepared, err := s.prepareFilesystemEdit(ctx, workspace.ID, filePath, displayPath, spec.Edit.GetExpectedHash(), spec.Edit.GetEdits())
		if err != nil {
			return nil, nil, annotateFilesystemEditError(displayPath, err)
		}
		hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, prepared.path, prepared.updated)
		if err != nil {
			return nil, nil, err
		}
		return &filesystemv1.BatchResult{
			Id:     resultID,
			OpType: filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_EDIT,
			Path:   prepared.displayPath,
			Size:   size,
			Hash:   hash,
		}, []string{prepared.path}, nil
	case *filesystemv1.BatchOperation_Delete:
		filePath, displayPath, err := s.resolveOperationPath(username, homeMode, spec.Delete.GetPath(), true)
		if err != nil {
			return nil, nil, err
		}
		if err := s.deleteWorkspaceFile(ctx, workspace.ID, filePath); err != nil {
			return nil, nil, err
		}
		return &filesystemv1.BatchResult{
			Id:     resultID,
			OpType: filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_DELETE,
			Path:   displayPath,
		}, []string{filePath}, nil
	case *filesystemv1.BatchOperation_Move:
		sourcePath, sourceDisplayPath, err := s.resolveOperationPath(username, homeMode, spec.Move.GetSourcePath(), true)
		if err != nil {
			return nil, nil, err
		}
		destinationPath, destinationDisplayPath, err := s.resolveOperationPath(username, homeMode, spec.Move.GetDestinationPath(), true)
		if err != nil {
			return nil, nil, err
		}
		if sourcePath == destinationPath {
			return nil, nil, status.Error(codes.InvalidArgument, "source_path and destination_path must differ")
		}

		content, err := s.readWorkspaceFileContent(ctx, workspace.ID, sourcePath)
		if err != nil {
			return nil, nil, err
		}
		if err := s.ensureWorkspaceFileTarget(ctx, workspace.ID, destinationPath); err != nil {
			return nil, nil, err
		}
		hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, destinationPath, append([]byte(nil), content.Content...))
		if err != nil {
			return nil, nil, err
		}
		if err := s.deleteWorkspaceFile(ctx, workspace.ID, sourcePath); err != nil {
			return nil, nil, err
		}
		return &filesystemv1.BatchResult{
			Id:              resultID,
			OpType:          filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_MOVE,
			SourcePath:      sourceDisplayPath,
			DestinationPath: destinationDisplayPath,
			Size:            size,
			Hash:            hash,
		}, []string{sourcePath, destinationPath}, nil
	case *filesystemv1.BatchOperation_Copy:
		sourcePath, sourceDisplayPath, err := s.resolveOperationPath(username, homeMode, spec.Copy.GetSourcePath(), true)
		if err != nil {
			return nil, nil, err
		}
		destinationPath, destinationDisplayPath, err := s.resolveOperationPath(username, homeMode, spec.Copy.GetDestinationPath(), true)
		if err != nil {
			return nil, nil, err
		}
		if sourcePath == destinationPath {
			return nil, nil, status.Error(codes.InvalidArgument, "source_path and destination_path must differ")
		}

		content, err := s.readWorkspaceFileContent(ctx, workspace.ID, sourcePath)
		if err != nil {
			return nil, nil, err
		}
		if err := s.ensureWorkspaceFileTarget(ctx, workspace.ID, destinationPath); err != nil {
			return nil, nil, err
		}
		hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, destinationPath, append([]byte(nil), content.Content...))
		if err != nil {
			return nil, nil, err
		}
		return &filesystemv1.BatchResult{
			Id:              resultID,
			OpType:          filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_COPY,
			SourcePath:      sourceDisplayPath,
			DestinationPath: destinationDisplayPath,
			Size:            size,
			Hash:            hash,
		}, []string{sourcePath, destinationPath}, nil
	case *filesystemv1.BatchOperation_Mkdir:
		dirPath, displayPath, err := s.resolveOperationPath(username, homeMode, spec.Mkdir.GetPath(), true)
		if err != nil {
			return nil, nil, err
		}
		if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(workspace.ID, dirPath),
			Path:     dirPath,
			Type:     "directory",
			ParentID: workspace.ID,
		}); err != nil {
			return nil, nil, status.Error(codes.Internal, fmt.Sprintf("failed to create directory: %v", err))
		}
		return &filesystemv1.BatchResult{
			Id:     resultID,
			OpType: filesystemv1.BatchOperationType_BATCH_OPERATION_TYPE_MKDIR,
			Path:   displayPath,
		}, []string{dirPath}, nil
	default:
		return nil, nil, status.Error(codes.InvalidArgument, "operation is required")
	}
}

func (s *filesystemServiceServer) PlanUpload(ctx context.Context, req *filesystemv1.PlanUploadRequest) (*filesystemv1.PlanUploadResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}
	if len(req.GetFiles()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "files is required")
	}

	missingBlocks := make(map[string]int64)
	checkedBlocks := make(map[string]struct{})
	skippedPaths := make([]string, 0)
	for index, spec := range req.GetFiles() {
		prepared, err := s.prepareFilesystemUpload(ctx, workspace.ID, username, homeMode, index, spec)
		if err != nil {
			return nil, err
		}
		if prepared.unchanged {
			skippedPaths = append(skippedPaths, prepared.displayPath)
			continue
		}

		versioned, err := s.storage.GetVersionedFileManifest(ctx, prepared.manifest.Hash)
		switch {
		case err == nil && versioned != nil:
			if !filesystemManifestEquivalent(prepared.manifest, versioned) {
				return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("files[%d] manifest does not match stored content hash", index))
			}
			continue
		case err != nil && err != storage.ErrEntryNotFound:
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect versioned manifest: %v", err))
		}

		for _, block := range prepared.manifest.Blocks {
			if _, seen := checkedBlocks[block.Hash]; seen {
				continue
			}
			checkedBlocks[block.Hash] = struct{}{}
			hasBlock, err := s.storage.HasBlock(ctx, block.Hash)
			if err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to check block presence: %v", err))
			}
			if !hasBlock {
				missingBlocks[block.Hash] = int64(block.Size)
			}
		}
	}

	missingHashes := make([]string, 0, len(missingBlocks))
	var missingBytes int64
	for hash, size := range missingBlocks {
		missingHashes = append(missingHashes, hash)
		missingBytes += size
	}
	sort.Strings(missingHashes)
	sort.Strings(skippedPaths)

	return &filesystemv1.PlanUploadResponse{
		WorkspaceId:        workspace.ID,
		MissingBlockHashes: missingHashes,
		SkippedPaths:       skippedPaths,
		FileCount:          int32(len(req.GetFiles())),
		MissingBlockCount:  int32(len(missingHashes)),
		MissingBytes:       missingBytes,
	}, nil
}

func (s *filesystemServiceServer) FinalizeUpload(ctx context.Context, req *filesystemv1.FinalizeUploadRequest) (*filesystemv1.FinalizeUploadResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, true)
	if err != nil {
		return nil, err
	}
	if len(req.GetDirectories()) == 0 && len(req.GetFiles()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "directories or files is required")
	}

	resp := &filesystemv1.FinalizeUploadResponse{WorkspaceId: workspace.ID}
	modifiedPaths := make([]string, 0, len(req.GetDirectories())+len(req.GetFiles()))

	directorySpecs := make([]string, 0, len(req.GetDirectories()))
	directorySeen := make(map[string]struct{}, len(req.GetDirectories()))
	for index, rawDir := range req.GetDirectories() {
		dirPath, _, err := s.resolveOperationPath(username, homeMode, rawDir, true)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("directories[%d]: %s", index, status.Convert(err).Message()))
		}
		if dirPath == "" {
			continue
		}
		if _, seen := directorySeen[dirPath]; seen {
			continue
		}
		directorySeen[dirPath] = struct{}{}
		directorySpecs = append(directorySpecs, dirPath)
	}
	sort.Strings(directorySpecs)

	for _, dirPath := range directorySpecs {
		entry, err := s.storage.GetEntryByPath(ctx, workspace.ID, dirPath)
		if err == nil {
			if entry.Type != "directory" {
				return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("%s is not a directory", displayOperationPath(dirPath, homeMode)))
			}
			continue
		}
		if err != storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect directory: %v", err))
		}
		if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(workspace.ID, dirPath),
			Path:     dirPath,
			Type:     "directory",
			ParentID: workspace.ID,
		}); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create directory: %v", err))
		}
		resp.DirectoriesWritten++
		modifiedPaths = append(modifiedPaths, dirPath)
	}

	for index, spec := range req.GetFiles() {
		prepared, err := s.prepareFilesystemUpload(ctx, workspace.ID, username, homeMode, index, spec)
		if err != nil {
			return nil, err
		}
		if prepared.unchanged {
			resp.FilesSkipped++
			continue
		}

		canonical, err := s.resolveFilesystemUploadManifest(ctx, prepared.manifest)
		if err != nil {
			return nil, annotateFilesystemEditError(prepared.displayPath, err)
		}
		if err := s.writeWorkspaceFileManifest(ctx, workspace.ID, canonical); err != nil {
			return nil, annotateFilesystemEditError(prepared.displayPath, err)
		}
		resp.FilesWritten++
		modifiedPaths = append(modifiedPaths, prepared.path)
	}

	if len(modifiedPaths) == 0 {
		return resp, nil
	}

	message := strings.TrimSpace(req.GetMessage())
	if message == "" {
		message = fmt.Sprintf("upload %d files", resp.GetFilesWritten())
	}
	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, message, modifiedPaths)
	if err != nil {
		return nil, err
	}
	resp.CommitHash = commitHash
	return resp, nil
}

func (s *filesystemServiceServer) UploadBlocks(stream filesystemv1.FilesystemService_UploadBlocksServer) error {
	ctx := stream.Context()
	username, err := s.requireUser(ctx)
	if err != nil {
		return err
	}

	var (
		workspaceID    string
		workspaceReady bool
		currentMeta    *filesystemv1.UploadBlockMetadata
		buffer         bytes.Buffer
		resp           filesystemv1.UploadBlocksResponse
	)

	flush := func() error {
		if currentMeta == nil {
			return nil
		}
		if currentMeta.GetHash() == "" {
			return status.Error(codes.InvalidArgument, "block hash is required")
		}
		if currentMeta.GetSize() != int64(buffer.Len()) {
			return status.Error(codes.InvalidArgument, "block size does not match content")
		}
		content := buffer.Bytes()
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != strings.TrimSpace(currentMeta.GetHash()) {
			return status.Error(codes.InvalidArgument, "block hash does not match content")
		}

		hasBlock, err := s.storage.HasBlock(ctx, currentMeta.GetHash())
		if err != nil {
			return status.Error(codes.Internal, fmt.Sprintf("failed to check block presence: %v", err))
		}
		if hasBlock {
			resp.BlocksReused++
			observeFilesystemBlocks(0, 1)
		} else {
			if err := s.storage.PutBlock(ctx, currentMeta.GetHash(), append([]byte(nil), content...)); err != nil {
				return status.Error(codes.Internal, fmt.Sprintf("failed to persist block: %v", err))
			}
			resp.BlocksWritten++
			observeFilesystemBlocks(1, 0)
		}
		resp.BlocksReceived++
		resp.BytesReceived += int64(len(content))
		buffer.Reset()
		currentMeta = nil
		return nil
	}

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Error(codes.Unknown, fmt.Sprintf("failed to receive upload block chunk: %v", err))
		}

		switch chunk := req.GetChunk().(type) {
		case *filesystemv1.UploadBlocksRequest_Metadata:
			if err := flush(); err != nil {
				return err
			}
			meta := chunk.Metadata
			if meta == nil {
				return status.Error(codes.InvalidArgument, "upload block metadata is required")
			}
			if meta.GetSize() < 0 {
				return status.Error(codes.InvalidArgument, "upload block size must be >= 0")
			}
			resolvedWorkspace, _, _, err := s.resolveOperationWorkspace(ctx, meta.GetWorkspaceId(), username, true)
			if err != nil {
				return err
			}
			if !workspaceReady {
				workspaceID = resolvedWorkspace.ID
				resp.WorkspaceId = workspaceID
				workspaceReady = true
			} else if workspaceID != resolvedWorkspace.ID {
				return status.Error(codes.InvalidArgument, "all uploaded blocks must target the same workspace")
			}
			currentMeta = &filesystemv1.UploadBlockMetadata{
				WorkspaceId: workspaceID,
				Hash:        strings.TrimSpace(meta.GetHash()),
				Size:        meta.GetSize(),
			}
		case *filesystemv1.UploadBlocksRequest_Content:
			if currentMeta == nil {
				return status.Error(codes.InvalidArgument, "upload block metadata must be sent before content")
			}
			if _, err := buffer.Write(chunk.Content); err != nil {
				return status.Error(codes.Internal, fmt.Sprintf("failed to buffer upload block content: %v", err))
			}
		default:
			return status.Error(codes.InvalidArgument, "upload block chunk is required")
		}
	}

	if err := flush(); err != nil {
		return err
	}
	if !workspaceReady {
		return status.Error(codes.InvalidArgument, "upload block metadata is required")
	}
	return stream.SendAndClose(&resp)
}

func (s *filesystemServiceServer) Glob(ctx context.Context, req *filesystemv1.GlobRequest) (*filesystemv1.GlobResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	pattern, err := s.resolveOperationPattern(username, homeMode, req.GetPattern(), true)
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
			paths = append(paths, displayOperationPath(entry.Path, homeMode))
		}
	}
	sort.Strings(paths)

	return &filesystemv1.GlobResponse{
		WorkspaceId: workspace.ID,
		Pattern:     displaySearchGlob(pattern, homeMode),
		Paths:       paths,
	}, nil
}

func (s *filesystemServiceServer) Search(ctx context.Context, req *filesystemv1.SearchRequest) (*filesystemv1.SearchResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, meta, homeMode, err := s.resolveOperationWorkspace(ctx, req.GetWorkspaceId(), username, false)
	if err != nil {
		return nil, err
	}

	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	globPattern, err := s.resolveOperationPattern(username, homeMode, req.GetGlob(), false)
	if err != nil {
		return nil, err
	}

	var matches []*filesystemv1.SearchMatch
	if req.GetRegex() {
		regexMatches, regexErr := s.searchWorkspaceRegex(ctx, workspace.ID, strings.TrimSpace(meta.HeadCommitHash), query, globPattern, homeMode)
		if regexErr != nil {
			return nil, regexErr
		}
		matches = regexMatches
	} else {
		matches, err = s.scanWorkspaceSearch(ctx, workspace.ID, globPattern, homeMode, func(displayPath, body string) []*filesystemv1.SearchMatch {
			return findSearchMatches(displayPath, body, query)
		})
		if err != nil {
			return nil, err
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
		Glob:        displaySearchGlob(globPattern, homeMode),
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

	commitHash, _, err := s.commitWorkspaceMutation(ctx, workspace, message, nil)
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
	homeMode := workspace.ID == homeslice.IDForUsername(username)

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

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, message, nil)
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
		Visibility:  models.VisibilityPrivate,
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

	if _, _, err := s.commitWorkspaceMutation(ctx, forkWorkspace, fmt.Sprintf("fork from %s@%s", sourceWorkspace.ID, sourceSnapshotID), nil); err != nil {
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

func (s *filesystemServiceServer) Merge(ctx context.Context, req *filesystemv1.MergeRequest) (*filesystemv1.MergeResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	targetWorkspace, targetMeta, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	sourceWorkspace, sourceMeta, err := s.requireWorkspaceViewAccess(ctx, req.GetSourceWorkspaceId())
	if err != nil {
		return nil, err
	}
	if sourceWorkspace.ID == targetWorkspace.ID {
		return nil, status.Error(codes.InvalidArgument, "source_workspace_id must differ from workspace_id")
	}

	sourceSnapshotID := strings.TrimSpace(req.GetSourceSnapshotId())
	if sourceSnapshotID == "" {
		sourceSnapshotID = strings.TrimSpace(sourceMeta.HeadCommitHash)
	}
	if sourceSnapshotID == "" {
		return nil, status.Error(codes.NotFound, "source snapshot not found")
	}

	targetSnapshotID := strings.TrimSpace(req.GetTargetSnapshotId())
	if targetSnapshotID == "" {
		targetSnapshotID = strings.TrimSpace(targetMeta.HeadCommitHash)
	}
	if targetSnapshotID == "" {
		return nil, status.Error(codes.NotFound, "target snapshot not found")
	}

	_, sourceSnapshot, err := s.resolveWorkspaceSnapshot(ctx, sourceWorkspace.ID, sourceSnapshotID, false)
	if err != nil {
		return nil, err
	}
	_, targetSnapshot, err := s.resolveWorkspaceSnapshot(ctx, targetWorkspace.ID, targetSnapshotID, false)
	if err != nil {
		return nil, err
	}

	baseWorkspaceID, baseSnapshotID, baseSnapshot, err := s.resolveMergeBase(ctx, req, sourceWorkspace.ID, targetWorkspace.ID)
	if err != nil {
		return nil, err
	}

	mergedSnapshot, mergedPaths, conflicts := s.buildMergedWorkspaceSnapshot(ctx, baseSnapshot, sourceSnapshot, targetSnapshot)
	if len(conflicts) > 0 {
		return &filesystemv1.MergeResponse{
			WorkspaceId:       targetWorkspace.ID,
			SourceWorkspaceId: sourceWorkspace.ID,
			BaseWorkspaceId:   baseWorkspaceID,
			BaseSnapshotId:    baseSnapshotID,
			SourceSnapshotId:  sourceSnapshotID,
			TargetSnapshotId:  targetSnapshotID,
			Status:            filesystemv1.MergeStatus_MERGE_STATUS_CONFLICT,
			Summary:           &filesystemv1.DiffSummary{},
			MergedPaths:       []string{},
			Conflicts:         conflicts,
		}, nil
	}

	if len(mergedPaths) == 0 {
		return &filesystemv1.MergeResponse{
			WorkspaceId:       targetWorkspace.ID,
			SourceWorkspaceId: sourceWorkspace.ID,
			BaseWorkspaceId:   baseWorkspaceID,
			BaseSnapshotId:    baseSnapshotID,
			SourceSnapshotId:  sourceSnapshotID,
			TargetSnapshotId:  targetSnapshotID,
			Status:            filesystemv1.MergeStatus_MERGE_STATUS_SUCCESS,
			CommitHash:        targetSnapshotID,
			Summary:           &filesystemv1.DiffSummary{},
			MergedPaths:       []string{},
			Conflicts:         []*filesystemv1.MergeConflict{},
		}, nil
	}

	if err := s.resetWorkspaceToSnapshot(ctx, targetWorkspace, mergedSnapshot); err != nil {
		return nil, err
	}

	message := strings.TrimSpace(req.GetMessage())
	if message == "" {
		message = fmt.Sprintf("merge %s@%s into %s", sourceWorkspace.ID, sourceSnapshotID, targetWorkspace.ID)
	}

	homeMode := targetWorkspace.ID == homeslice.IDForUsername(username)
	commitHash, err := s.finalizeWorkspaceMutation(ctx, targetWorkspace, homeMode, message, mergedPaths)
	if err != nil {
		return nil, err
	}

	_, summary, err := s.buildFilesystemDiff(ctx, targetSnapshot, mergedSnapshot, false)
	if err != nil {
		return nil, err
	}

	return &filesystemv1.MergeResponse{
		WorkspaceId:       targetWorkspace.ID,
		SourceWorkspaceId: sourceWorkspace.ID,
		BaseWorkspaceId:   baseWorkspaceID,
		BaseSnapshotId:    baseSnapshotID,
		SourceSnapshotId:  sourceSnapshotID,
		TargetSnapshotId:  targetSnapshotID,
		Status:            filesystemv1.MergeStatus_MERGE_STATUS_SUCCESS,
		CommitHash:        commitHash,
		Summary:           summary,
		MergedPaths:       mergedPaths,
		Conflicts:         []*filesystemv1.MergeConflict{},
	}, nil
}

func (s *filesystemServiceServer) ListConflicts(ctx context.Context, req *filesystemv1.ListConflictsRequest) (*filesystemv1.ListConflictsResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceViewAccess(ctx, req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}

	otherWorkspaceID := strings.TrimSpace(req.GetOtherWorkspaceId())
	if otherWorkspaceID != "" {
		if _, _, err := s.requireWorkspaceViewAccess(ctx, otherWorkspaceID); err != nil {
			return nil, err
		}
	}

	conflicts, err := s.listVisibleWorkspaceConflicts(ctx, username, workspace.ID, otherWorkspaceID)
	if err != nil {
		return nil, err
	}

	return &filesystemv1.ListConflictsResponse{
		WorkspaceId:      workspace.ID,
		OtherWorkspaceId: otherWorkspaceID,
		Conflicts:        conflicts,
	}, nil
}

func (s *filesystemServiceServer) ResolveConflict(ctx context.Context, req *filesystemv1.ResolveConflictRequest) (*filesystemv1.ResolveConflictResponse, error) {
	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}

	workspace, _, err := s.requireWorkspaceWriteAccess(ctx, req.GetWorkspaceId(), username)
	if err != nil {
		return nil, err
	}

	conflictPath, err := validateWorkspacePath(req.GetPath(), true)
	if err != nil {
		return nil, err
	}

	preferredWorkspaceID := strings.TrimSpace(req.GetPreferredWorkspaceId())
	if preferredWorkspaceID == "" {
		preferredWorkspaceID = workspace.ID
	}
	if _, _, err := s.requireWorkspaceWriteAccess(ctx, preferredWorkspaceID, username); err != nil {
		return nil, err
	}

	conflicts, err := s.listVisibleWorkspaceConflicts(ctx, username, workspace.ID, "")
	if err != nil {
		return nil, err
	}

	var selected *filesystemv1.WorkspaceConflict
	for _, conflict := range conflicts {
		if conflict.GetPath() == conflictPath {
			selected = conflict
			break
		}
	}
	if selected == nil {
		return nil, status.Error(codes.NotFound, "conflict not found")
	}
	if !containsString(selected.GetWorkspaceIds(), preferredWorkspaceID) {
		return nil, status.Error(codes.InvalidArgument, "preferred_workspace_id is not part of the conflict")
	}
	for _, conflictingWorkspaceID := range selected.GetWorkspaceIds() {
		if _, _, err := s.requireWorkspaceWriteAccess(ctx, conflictingWorkspaceID, username); err != nil {
			return nil, err
		}
	}

	resolved, err := s.storage.ResolveConflict(ctx, conflictPath, preferredWorkspaceID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve conflict: %v", err))
	}

	resolvedIDs := append([]string(nil), resolved.ConflictingSlices...)
	sort.Strings(resolvedIDs)
	return &filesystemv1.ResolveConflictResponse{
		WorkspaceId: workspace.ID,
		Conflict: &filesystemv1.WorkspaceConflict{
			Path:         conflictPath,
			WorkspaceIds: resolvedIDs,
		},
	}, nil
}

func (s *filesystemServiceServer) StreamRead(req *filesystemv1.StreamReadRequest, stream filesystemv1.FilesystemService_StreamReadServer) error {
	username, err := s.requireUser(stream.Context())
	if err != nil {
		return err
	}

	workspace, _, homeMode, err := s.resolveOperationWorkspace(stream.Context(), req.GetWorkspaceId(), username, false)
	if err != nil {
		return err
	}

	filePath, displayPath, err := s.resolveOperationPath(username, homeMode, req.GetPath(), true)
	if err != nil {
		return err
	}

	content, err := s.readWorkspaceFileContent(stream.Context(), workspace.ID, filePath)
	if err != nil {
		return err
	}

	chunkSize := int(req.GetChunkSize())
	switch {
	case chunkSize <= 0:
		chunkSize = defaultFilesystemStreamChunkSize
	case chunkSize > maxFilesystemStreamChunkSize:
		return status.Error(codes.InvalidArgument, fmt.Sprintf("chunk_size must be <= %d", maxFilesystemStreamChunkSize))
	}

	data := content.Content
	if len(data) == 0 {
		return stream.Send(&filesystemv1.StreamReadResponse{
			WorkspaceId: workspace.ID,
			Path:        displayPath,
			Offset:      0,
			Size:        content.Size,
			Hash:        content.Hash,
			Eof:         true,
		})
	}

	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&filesystemv1.StreamReadResponse{
			WorkspaceId: workspace.ID,
			Path:        displayPath,
			Content:     append([]byte(nil), data[offset:end]...),
			Offset:      int64(offset),
			Size:        content.Size,
			Hash:        content.Hash,
			Eof:         end == len(data),
		}); err != nil {
			return status.Error(codes.Unknown, fmt.Sprintf("failed to send stream chunk: %v", err))
		}
	}

	return nil
}

func (s *filesystemServiceServer) StreamWrite(stream filesystemv1.FilesystemService_StreamWriteServer) error {
	ctx := stream.Context()
	username, err := s.requireUser(ctx)
	if err != nil {
		return err
	}

	var (
		workspace   *models.Slice
		filePath    string
		displayPath string
		homeMode    bool
		buffer      bytes.Buffer
		received    bool
	)

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Error(codes.Unknown, fmt.Sprintf("failed to receive stream chunk: %v", err))
		}

		switch chunk := req.GetChunk().(type) {
		case *filesystemv1.StreamWriteRequest_Metadata:
			if received {
				return status.Error(codes.InvalidArgument, "stream metadata already received")
			}
			workspace, _, homeMode, err = s.resolveOperationWorkspace(ctx, chunk.Metadata.GetWorkspaceId(), username, true)
			if err != nil {
				return err
			}
			filePath, displayPath, err = s.resolveOperationPath(username, homeMode, chunk.Metadata.GetPath(), true)
			if err != nil {
				return err
			}
			received = true
		case *filesystemv1.StreamWriteRequest_Content:
			if !received {
				return status.Error(codes.InvalidArgument, "stream metadata must be sent before content")
			}
			if _, err := buffer.Write(chunk.Content); err != nil {
				return status.Error(codes.Internal, fmt.Sprintf("failed to buffer stream content: %v", err))
			}
		default:
			return status.Error(codes.InvalidArgument, "stream chunk is required")
		}
	}

	if !received || workspace == nil {
		return status.Error(codes.InvalidArgument, "stream metadata is required")
	}

	hash, size, err := s.writeWorkspaceFileContent(ctx, workspace, filePath, buffer.Bytes())
	if err != nil {
		return err
	}

	commitHash, err := s.finalizeWorkspaceMutation(ctx, workspace, homeMode, fmt.Sprintf("stream write %s", displayPath), []string{filePath})
	if err != nil {
		return err
	}

	return stream.SendAndClose(&filesystemv1.StreamWriteResponse{
		WorkspaceId: workspace.ID,
		Path:        displayPath,
		Size:        size,
		Hash:        hash,
		CommitHash:  commitHash,
	})
}

func (s *filesystemServiceServer) readWorkspaceFileContent(ctx context.Context, workspaceID, filePath string) (*models.FileContent, error) {
	if _, err := s.requireWorkspaceFileEntry(ctx, workspaceID, filePath); err != nil {
		return nil, err
	}

	content, err := storage.ReadSliceFileContent(ctx, s.storage, workspaceID, filePath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "file not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to read file: %v", err))
	}
	return content, nil
}

func parseReadFileOptions(req *filesystemv1.ReadFileRequest) (readFileOptions, error) {
	opts := readFileOptions{
		byteOffset: req.GetByteOffset(),
		byteLimit:  req.GetByteLimit(),
		lineOffset: req.GetLineOffset(),
		lineLimit:  req.GetLineLimit(),
	}
	if opts.byteOffset < 0 {
		return readFileOptions{}, status.Error(codes.InvalidArgument, "byte_offset must be non-negative")
	}
	if opts.byteLimit < 0 {
		return readFileOptions{}, status.Error(codes.InvalidArgument, "byte_limit must be non-negative")
	}
	if opts.lineOffset < 0 {
		return readFileOptions{}, status.Error(codes.InvalidArgument, "line_offset must be non-negative")
	}
	if opts.lineLimit < 0 {
		return readFileOptions{}, status.Error(codes.InvalidArgument, "line_limit must be non-negative")
	}

	opts.byteRange = opts.byteOffset > 0 || opts.byteLimit > 0
	opts.lineRange = opts.lineOffset > 0 || opts.lineLimit > 0
	if opts.byteRange && opts.lineRange {
		return readFileOptions{}, status.Error(codes.InvalidArgument, "byte and line ranges cannot be combined")
	}
	return opts, nil
}

func (s *filesystemServiceServer) readWorkspaceFileSelection(ctx context.Context, workspaceID, filePath string, opts readFileOptions) ([]byte, int64, string, error) {
	if !opts.byteRange && !opts.lineRange {
		content, err := s.readWorkspaceFileContent(ctx, workspaceID, filePath)
		if err != nil {
			return nil, 0, "", err
		}

		hash := strings.TrimSpace(content.Hash)
		if hash == "" {
			hash = hashContent(content.Content)
		}
		return append([]byte(nil), content.Content...), int64(len(content.Content)), hash, nil
	}

	if _, err := s.requireWorkspaceFileEntry(ctx, workspaceID, filePath); err != nil {
		return nil, 0, "", err
	}

	manifest, err := s.storage.GetFileManifest(ctx, workspaceID, filePath)
	if err == nil {
		return s.readWorkspaceFileFromManifest(ctx, workspaceID, filePath, manifest, opts)
	}
	if err != storage.ErrEntryNotFound {
		return nil, 0, "", status.Error(codes.Internal, fmt.Sprintf("failed to load file manifest: %v", err))
	}

	content, readErr := s.readWorkspaceFileContent(ctx, workspaceID, filePath)
	if readErr != nil {
		return nil, 0, "", readErr
	}
	hash := strings.TrimSpace(content.Hash)
	if hash == "" {
		hash = hashContent(content.Content)
	}

	selected, err := sliceContentForReadRange(content.Content, opts)
	if err != nil {
		return nil, 0, "", err
	}
	return selected, int64(len(content.Content)), hash, nil
}

func (s *filesystemServiceServer) readWorkspaceFileFromManifest(ctx context.Context, workspaceID, filePath string, manifest *models.FileManifest, opts readFileOptions) ([]byte, int64, string, error) {
	if manifest == nil {
		return nil, 0, "", status.Error(codes.Internal, "file manifest is nil")
	}

	hash := strings.TrimSpace(manifest.Hash)
	if hash == "" {
		content, err := s.readWorkspaceFileContent(ctx, workspaceID, filePath)
		if err != nil {
			return nil, 0, "", err
		}
		hash = strings.TrimSpace(content.Hash)
		if hash == "" {
			hash = hashContent(content.Content)
		}
	}

	var (
		selected []byte
		err      error
	)
	switch {
	case opts.byteRange:
		selected, err = s.readManifestByteRange(ctx, manifest, opts.byteOffset, opts.byteLimit)
	case opts.lineRange:
		selected, err = s.readManifestLineRange(ctx, manifest, opts.lineOffset, opts.lineLimit)
	default:
		selected, err = s.readManifestByteRange(ctx, manifest, 0, 0)
	}
	if err != nil {
		return nil, 0, "", err
	}
	return selected, manifest.TotalSize, hash, nil
}

func (s *filesystemServiceServer) readManifestByteRange(ctx context.Context, manifest *models.FileManifest, offset, limit int64) ([]byte, error) {
	start, end, err := normalizeByteRange(manifest.TotalSize, offset, limit)
	if err != nil {
		return nil, err
	}
	if start >= end {
		return []byte{}, nil
	}

	indices := storage.FindBlocksForRange(manifest, start, end-start)
	if len(indices) == 0 {
		return []byte{}, nil
	}

	var out bytes.Buffer
	selectedPos := 0
	var cursor int64
	for idx, block := range manifest.Blocks {
		blockStart := cursor
		blockEnd := blockStart + int64(block.Size)
		cursor = blockEnd
		if selectedPos >= len(indices) {
			break
		}
		if idx != indices[selectedPos] {
			continue
		}
		selectedPos++

		payload, err := s.readBlockPayload(ctx, block.Hash)
		if err != nil {
			return nil, err
		}
		rangeStart := maxInt64(start, blockStart) - blockStart
		rangeEnd := minInt64(end, blockEnd) - blockStart
		if rangeStart < 0 {
			rangeStart = 0
		}
		if rangeEnd > int64(len(payload)) {
			rangeEnd = int64(len(payload))
		}
		if rangeStart >= rangeEnd {
			continue
		}
		if _, err := out.Write(payload[int(rangeStart):int(rangeEnd)]); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to assemble file range: %v", err))
		}
	}
	return out.Bytes(), nil
}

func (s *filesystemServiceServer) readManifestLineRange(ctx context.Context, manifest *models.FileManifest, lineOffset, lineLimit int64) ([]byte, error) {
	if manifest == nil || manifest.TotalSize == 0 || len(manifest.Blocks) == 0 {
		return []byte{}, nil
	}

	remainingOffset := lineOffset
	remainingLines := lineLimit
	collecting := remainingOffset == 0
	var out bytes.Buffer

	for _, block := range manifest.Blocks {
		payload, err := s.readBlockPayload(ctx, block.Hash)
		if err != nil {
			return nil, err
		}

		start := 0
		if !collecting {
			for idx, b := range payload {
				if b != '\n' {
					continue
				}
				remainingOffset--
				if remainingOffset == 0 {
					collecting = true
					start = idx + 1
					break
				}
			}
			if !collecting {
				continue
			}
		}

		if remainingLines == 0 {
			if _, err := out.Write(payload[start:]); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to assemble file lines: %v", err))
			}
			continue
		}

		segmentStart := start
		for idx := start; idx < len(payload); idx++ {
			if payload[idx] != '\n' {
				continue
			}
			remainingLines--
			if remainingLines == 0 {
				if _, err := out.Write(payload[segmentStart : idx+1]); err != nil {
					return nil, status.Error(codes.Internal, fmt.Sprintf("failed to assemble file lines: %v", err))
				}
				return out.Bytes(), nil
			}
		}
		if _, err := out.Write(payload[segmentStart:]); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to assemble file lines: %v", err))
		}
	}

	if !collecting {
		return []byte{}, nil
	}
	return out.Bytes(), nil
}

func (s *filesystemServiceServer) readBlockPayload(ctx context.Context, hash string) ([]byte, error) {
	payload, err := s.storage.GetBlock(ctx, hash)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, fmt.Sprintf("missing file block %s", hash))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load file block %s: %v", hash, err))
	}
	return payload, nil
}

func sliceContentForReadRange(content []byte, opts readFileOptions) ([]byte, error) {
	switch {
	case opts.byteRange:
		start, end, err := normalizeByteRange(int64(len(content)), opts.byteOffset, opts.byteLimit)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), content[int(start):int(end)]...), nil
	case opts.lineRange:
		start, end := findLineByteRange(content, opts.lineOffset, opts.lineLimit)
		return append([]byte(nil), content[start:end]...), nil
	default:
		return append([]byte(nil), content...), nil
	}
}

func normalizeByteRange(totalSize, offset, limit int64) (int64, int64, error) {
	if offset < 0 {
		return 0, 0, status.Error(codes.InvalidArgument, "byte_offset must be non-negative")
	}
	if limit < 0 {
		return 0, 0, status.Error(codes.InvalidArgument, "byte_limit must be non-negative")
	}
	if offset >= totalSize {
		return totalSize, totalSize, nil
	}

	end := totalSize
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return offset, end, nil
}

func findLineByteRange(content []byte, lineOffset, lineLimit int64) (int, int) {
	if len(content) == 0 {
		return 0, 0
	}
	if lineOffset == 0 && lineLimit == 0 {
		return 0, len(content)
	}

	remainingOffset := lineOffset
	start := 0
	if remainingOffset > 0 {
		start = len(content)
		for idx, b := range content {
			if b != '\n' {
				continue
			}
			remainingOffset--
			if remainingOffset == 0 {
				start = idx + 1
				break
			}
		}
		if remainingOffset > 0 {
			return len(content), len(content)
		}
	}

	if lineLimit == 0 {
		return start, len(content)
	}

	remainingLines := lineLimit
	for idx := start; idx < len(content); idx++ {
		if content[idx] != '\n' {
			continue
		}
		remainingLines--
		if remainingLines == 0 {
			return start, idx + 1
		}
	}
	return start, len(content)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
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

func (s *filesystemServiceServer) prepareFilesystemUpload(ctx context.Context, workspaceID, username string, homeMode bool, index int, spec *filesystemv1.UploadFileManifest) (*preparedFilesystemUpload, error) {
	if spec == nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("files[%d] must not be null", index))
	}

	filePath, displayPath, err := s.resolveOperationPath(username, homeMode, spec.GetPath(), true)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("files[%d]: %s", index, status.Convert(err).Message()))
	}
	if err := s.ensureWorkspaceFileTarget(ctx, workspaceID, filePath); err != nil {
		return nil, annotateFilesystemEditError(displayPath, err)
	}

	manifest, err := filesystemManifestFromProto(filePath, spec)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("files[%d]: %s", index, err.Error()))
	}

	currentManifest, err := s.storage.GetFileManifest(ctx, workspaceID, filePath)
	switch {
	case err == nil && currentManifest != nil && strings.TrimSpace(currentManifest.Hash) == strings.TrimSpace(manifest.Hash):
		return &preparedFilesystemUpload{
			path:        filePath,
			displayPath: displayPath,
			manifest:    manifest,
			unchanged:   true,
		}, nil
	case err == nil || err == storage.ErrEntryNotFound:
		return &preparedFilesystemUpload{
			path:        filePath,
			displayPath: displayPath,
			manifest:    manifest,
		}, nil
	default:
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect current file manifest: %v", err))
	}
}

func filesystemManifestFromProto(filePath string, spec *filesystemv1.UploadFileManifest) (*models.FileManifest, error) {
	path := common.CleanRelativePath(filePath)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	manifest := &models.FileManifest{
		Path:          path,
		TotalSize:     spec.GetSize(),
		Hash:          strings.TrimSpace(spec.GetHash()),
		Blocks:        make([]models.Block, 0, len(spec.GetBlocks())),
		Executable:    false,
		SymlinkTarget: "",
	}
	if err := validateFilesystemUploadManifest(manifest, spec.GetBlocks()); err != nil {
		return nil, err
	}
	for _, block := range spec.GetBlocks() {
		manifest.Blocks = append(manifest.Blocks, models.Block{
			Hash: strings.TrimSpace(block.GetHash()),
			Size: int(block.GetSize()),
		})
	}
	return manifest, nil
}

func validateFilesystemUploadManifest(manifest *models.FileManifest, refs []*filesystemv1.UploadBlockRef) error {
	if manifest == nil {
		return fmt.Errorf("manifest is required")
	}
	if manifest.TotalSize < 0 {
		return fmt.Errorf("size must be >= 0")
	}
	if manifest.Hash == "" {
		return fmt.Errorf("hash is required")
	}
	var blockTotal int64
	for index, ref := range refs {
		if ref == nil {
			return fmt.Errorf("blocks[%d] must not be null", index)
		}
		hash := strings.TrimSpace(ref.GetHash())
		if hash == "" {
			return fmt.Errorf("blocks[%d].hash is required", index)
		}
		if ref.GetSize() <= 0 {
			return fmt.Errorf("blocks[%d].size must be > 0", index)
		}
		blockTotal += ref.GetSize()
	}
	if manifest.TotalSize == 0 {
		if len(refs) != 0 {
			return fmt.Errorf("empty files must not include blocks")
		}
		if manifest.Hash != hashContent(nil) {
			return fmt.Errorf("hash does not match empty file content")
		}
		return nil
	}
	if len(refs) == 0 {
		return fmt.Errorf("blocks are required for non-empty files")
	}
	if blockTotal != manifest.TotalSize {
		return fmt.Errorf("block sizes do not match file size")
	}
	return nil
}

func filesystemManifestEquivalent(left, right *models.FileManifest) bool {
	if left == nil || right == nil {
		return left == right
	}
	if strings.TrimSpace(left.Hash) != strings.TrimSpace(right.Hash) {
		return false
	}
	if left.TotalSize != right.TotalSize {
		return false
	}
	if left.Executable != right.Executable {
		return false
	}
	if left.SymlinkTarget != right.SymlinkTarget {
		return false
	}
	if len(left.Blocks) != len(right.Blocks) {
		return false
	}
	for index := range left.Blocks {
		if strings.TrimSpace(left.Blocks[index].Hash) != strings.TrimSpace(right.Blocks[index].Hash) {
			return false
		}
		if left.Blocks[index].Size != right.Blocks[index].Size {
			return false
		}
	}
	return true
}

func cloneFilesystemManifest(manifest *models.FileManifest) *models.FileManifest {
	if manifest == nil {
		return nil
	}
	clone := &models.FileManifest{
		Path:          strings.TrimSpace(manifest.Path),
		TotalSize:     manifest.TotalSize,
		Hash:          strings.TrimSpace(manifest.Hash),
		Executable:    manifest.Executable,
		SymlinkTarget: manifest.SymlinkTarget,
	}
	if len(manifest.Blocks) > 0 {
		clone.Blocks = make([]models.Block, len(manifest.Blocks))
		copy(clone.Blocks, manifest.Blocks)
	}
	return clone
}

func (s *filesystemServiceServer) resolveFilesystemUploadManifest(ctx context.Context, manifest *models.FileManifest) (*models.FileManifest, error) {
	if manifest == nil {
		return nil, status.Error(codes.InvalidArgument, "manifest is required")
	}

	versioned, err := s.storage.GetVersionedFileManifest(ctx, manifest.Hash)
	switch {
	case err == nil && versioned != nil:
		if !filesystemManifestEquivalent(manifest, versioned) {
			return nil, status.Error(codes.InvalidArgument, "manifest does not match stored content hash")
		}
		canonical := cloneFilesystemManifest(versioned)
		canonical.Path = strings.TrimSpace(manifest.Path)
		return canonical, nil
	case err != nil && err != storage.ErrEntryNotFound:
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to inspect versioned manifest: %v", err))
	}

	hasher := sha256.New()
	var totalSize int64
	for _, block := range manifest.Blocks {
		payload, err := s.storage.GetBlock(ctx, block.Hash)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.FailedPrecondition, fmt.Sprintf("missing upload block %s", block.Hash))
			}
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load upload block %s: %v", block.Hash, err))
		}
		if len(payload) != block.Size {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("block %s size does not match manifest", block.Hash))
		}
		if _, err := hasher.Write(payload); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to hash upload block %s: %v", block.Hash, err))
		}
		totalSize += int64(block.Size)
	}
	if totalSize != manifest.TotalSize {
		return nil, status.Error(codes.InvalidArgument, "manifest size does not match uploaded blocks")
	}
	computedHash := hex.EncodeToString(hasher.Sum(nil))
	if computedHash != strings.TrimSpace(manifest.Hash) {
		return nil, status.Error(codes.InvalidArgument, "manifest hash does not match uploaded blocks")
	}
	return cloneFilesystemManifest(manifest), nil
}

func (s *filesystemServiceServer) writeWorkspaceFileManifest(ctx context.Context, workspaceID string, manifest *models.FileManifest) error {
	if manifest == nil {
		return status.Error(codes.InvalidArgument, "manifest is required")
	}
	if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
		ID:            common.GenerateEntryID(workspaceID, manifest.Path),
		Path:          manifest.Path,
		Type:          "file",
		ParentID:      workspaceID,
		Size:          manifest.TotalSize,
		Executable:    manifest.Executable,
		SymlinkTarget: manifest.SymlinkTarget,
	}); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to write file entry: %v", err))
	}
	if err := s.storage.PutFileManifest(ctx, workspaceID, manifest.Path, manifest); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to persist file manifest: %v", err))
	}
	if err := s.storage.PutVersionedFileManifest(ctx, manifest); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to persist versioned file manifest: %v", err))
	}
	if err := s.storage.AddFileToSlice(ctx, manifest.Path, workspaceID); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to update workspace file index: %v", err))
	}
	observeFilesystemManifest(manifest)
	return nil
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
	blocks, payloads := storage.ChunkFile(content, storage.DefaultFileBlockSize)
	manifest := &models.FileManifest{
		Path:      filePath,
		TotalSize: size,
		Hash:      hash,
		Blocks:    blocks,
	}
	writtenBlocks := 0
	if len(payloads) > 0 {
		missing := make(map[string][]byte, len(payloads))
		for blockHash, payload := range payloads {
			hasBlock, err := s.storage.HasBlock(ctx, blockHash)
			if err != nil {
				return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to check block presence: %v", err))
			}
			if !hasBlock {
				missing[blockHash] = payload
			}
		}
		if len(missing) > 0 {
			if err := s.storage.PutBlocks(ctx, missing); err != nil {
				return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to persist file blocks: %v", err))
			}
		}
		writtenBlocks = len(missing)
	}
	if err := s.storage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(workspace.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: workspace.ID,
		Size:     size,
	}); err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to write file entry: %v", err))
	}
	if err := s.storage.PutFileManifest(ctx, workspace.ID, filePath, manifest); err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to persist file manifest: %v", err))
	}
	if err := s.storage.PutVersionedFileManifest(ctx, manifest); err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to persist versioned file manifest: %v", err))
	}
	if err := s.storage.AddFileToSlice(ctx, filePath, workspace.ID); err != nil {
		return "", 0, status.Error(codes.Internal, fmt.Sprintf("failed to update workspace file index: %v", err))
	}
	observeFilesystemManifestWrite(manifest, writtenBlocks, len(payloads)-writtenBlocks)
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
	if err := s.storage.DeleteFileManifest(ctx, workspaceID, filePath); err != nil && err != storage.ErrEntryNotFound {
		return status.Error(codes.Internal, fmt.Sprintf("failed to delete file manifest: %v", err))
	}
	if err := s.storage.RemoveFileFromSlice(ctx, filePath, workspaceID); err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to update workspace file index: %v", err))
	}
	return nil
}

func (s *filesystemServiceServer) prepareFilesystemEdit(ctx context.Context, workspaceID, filePath, displayPath, expectedHash string, edits []*filesystemv1.FileEdit) (*preparedFilesystemEdit, error) {
	current, err := s.readWorkspaceFileContent(ctx, workspaceID, filePath)
	if err != nil {
		return nil, err
	}
	currentHash := strings.TrimSpace(current.Hash)
	if currentHash == "" {
		currentHash = hashContent(current.Content)
	}
	if expected := strings.TrimSpace(expectedHash); expected != "" && expected != currentHash {
		return nil, status.Error(codes.Aborted, "expected_hash does not match current file hash")
	}

	updated, err := applyFilesystemEdits(current.Content, edits)
	if err != nil {
		return nil, err
	}
	return &preparedFilesystemEdit{
		path:        filePath,
		displayPath: displayPath,
		updated:     updated,
	}, nil
}

func annotateFilesystemEditError(displayPath string, err error) error {
	if err == nil {
		return nil
	}
	statusErr, ok := status.FromError(err)
	if !ok {
		return err
	}
	message := statusErr.Message()
	if displayPath != "" {
		message = fmt.Sprintf("%s: %s", displayPath, message)
	}
	return status.Error(statusErr.Code(), message)
}

func applyFilesystemEdits(content []byte, edits []*filesystemv1.FileEdit) ([]byte, error) {
	if len(edits) == 0 {
		return nil, status.Error(codes.InvalidArgument, "edits are required")
	}

	updated := append([]byte(nil), content...)
	for _, edit := range edits {
		if edit == nil {
			return nil, status.Error(codes.InvalidArgument, "edit is required")
		}
		oldText := edit.GetOldText()
		if oldText == "" {
			return nil, status.Error(codes.InvalidArgument, "edit old_text is required")
		}
		oldBytes := []byte(oldText)
		if !bytes.Contains(updated, oldBytes) {
			return nil, status.Error(codes.FailedPrecondition, "edit old_text not found in file")
		}
		updated = bytes.ReplaceAll(updated, oldBytes, []byte(edit.GetNewText()))
	}
	return updated, nil
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

func findRegexSearchMatches(filePath, body string, re *regexp.Regexp) []*filesystemv1.SearchMatch {
	lines := strings.Split(body, "\n")
	results := make([]*filesystemv1.SearchMatch, 0)
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		for _, bounds := range re.FindAllStringIndex(line, -1) {
			if len(bounds) != 2 {
				continue
			}
			results = append(results, &filesystemv1.SearchMatch{
				Path:       filePath,
				LineNumber: int32(i + 1),
				Line:       line,
				MatchStart: int32(bounds[0]),
				MatchEnd:   int32(bounds[1]),
			})
		}
	}
	return results
}

func (s *filesystemServiceServer) scanWorkspaceSearch(ctx context.Context, workspaceID, globPattern string, homeMode bool, matcher func(displayPath, body string) []*filesystemv1.SearchMatch) ([]*filesystemv1.SearchMatch, error) {
	entries, err := s.collectWorkspaceEntries(ctx, workspaceID)
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

		content, err := s.readWorkspaceFileContent(ctx, workspaceID, entry.Path)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				continue
			}
			return nil, err
		}
		matches = append(matches, matcher(displayOperationPath(entry.Path, homeMode), string(content.Content))...)
	}
	return matches, nil
}

func (s *filesystemServiceServer) searchWorkspaceRegex(ctx context.Context, workspaceID, headCommitHash, query, globPattern string, homeMode bool) ([]*filesystemv1.SearchMatch, error) {
	re, err := regexp.Compile(query)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid regex query: %v", err))
	}

	artifactLoadStartedAt := time.Now()
	artifact, artifactOutcome, artifactErr := s.loadWorkspaceSearchArtifact(ctx, workspaceID, headCommitHash)
	if artifactOutcome != "" {
		observeFilesystemSearchArtifactLoad(artifactOutcome, time.Since(artifactLoadStartedAt))
	}
	if artifactErr != nil {
		log.Printf("filesystem: regex search falling back to scan for %s: %v", workspaceID, artifactErr)
		observeFilesystemSearchFallback("artifact_error")
		verifyStartedAt := time.Now()
		defer observeFilesystemSearchVerify("regex_fallback_scan", time.Since(verifyStartedAt))
		return s.scanWorkspaceSearch(ctx, workspaceID, globPattern, homeMode, func(displayPath, body string) []*filesystemv1.SearchMatch {
			return findRegexSearchMatches(displayPath, body, re)
		})
	}

	queryNode, err := searchindex.BuildRegexQuery(query, searchindex.DefaultWeighter(), searchindex.SparseModeCovering)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid regex query: %v", err))
	}
	candidateIndexes := searchindex.CandidateFileIndexes(artifact, queryNode)
	observeFilesystemSearchCandidates("regex_indexed", len(candidateIndexes))
	if len(candidateIndexes) == 0 {
		observeFilesystemSearchFallback("empty_candidates")
		verifyStartedAt := time.Now()
		defer observeFilesystemSearchVerify("regex_fallback_scan", time.Since(verifyStartedAt))
		return s.scanWorkspaceSearch(ctx, workspaceID, globPattern, homeMode, func(displayPath, body string) []*filesystemv1.SearchMatch {
			return findRegexSearchMatches(displayPath, body, re)
		})
	}

	verifyStartedAt := time.Now()
	defer observeFilesystemSearchVerify("regex_indexed", time.Since(verifyStartedAt))
	matches := make([]*filesystemv1.SearchMatch, 0)
	for _, fileIndex := range candidateIndexes {
		if int(fileIndex) >= len(artifact.Files) {
			continue
		}
		file := artifact.Files[fileIndex]
		if globPattern != "" && !globMatch(globPattern, file.Path) {
			continue
		}
		content, err := s.readWorkspaceFileContent(ctx, workspaceID, file.Path)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				continue
			}
			return nil, err
		}
		matches = append(matches, findRegexSearchMatches(displayOperationPath(file.Path, homeMode), string(content.Content), re)...)
	}
	return matches, nil
}

func (s *filesystemServiceServer) loadWorkspaceSearchArtifact(ctx context.Context, workspaceID, headCommitHash string) (*searchindex.SliceArtifact, string, error) {
	headCommitHash = strings.TrimSpace(headCommitHash)
	if headCommitHash == "" {
		return searchindex.BuildSliceArtifact(workspaceID, "", nil), storage.SearchArtifactOutcomeBuilt.String(), nil
	}

	artifact, outcome, err := storage.LoadOrBuildWorkspaceSearchArtifact(ctx, s.storage, workspaceID, headCommitHash)
	if err != nil {
		return nil, "", err
	}
	return artifact, outcome.String(), nil
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
		content, err := storage.ReadVersionedFileContent(ctx, s.storage, contentHash)
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

	content, err := storage.ReadVersionedFileContent(ctx, s.storage, cleaned)
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

func (s *filesystemServiceServer) resolveMergeBase(ctx context.Context, req *filesystemv1.MergeRequest, sourceWorkspaceID, targetWorkspaceID string) (string, string, *models.CommitSnapshot, error) {
	baseSnapshotID := strings.TrimSpace(req.GetBaseSnapshotId())
	baseWorkspaceID := strings.TrimSpace(req.GetBaseWorkspaceId())
	if baseSnapshotID != "" {
		candidates := make([]string, 0, 3)
		if baseWorkspaceID != "" {
			candidates = append(candidates, baseWorkspaceID)
		}
		candidates = append(candidates, targetWorkspaceID, sourceWorkspaceID)

		seen := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}

			if _, _, err := s.requireWorkspaceViewAccess(ctx, candidate); err != nil {
				if status.Code(err) == codes.NotFound {
					continue
				}
				return "", "", nil, err
			}
			_, snapshot, err := s.resolveWorkspaceSnapshot(ctx, candidate, baseSnapshotID, false)
			if err == nil {
				return candidate, baseSnapshotID, snapshot, nil
			}
			if status.Code(err) != codes.NotFound {
				return "", "", nil, err
			}
		}
		return "", "", nil, status.Error(codes.NotFound, "base snapshot not found")
	}

	baseWorkspaceID, baseSnapshotID, err := s.inferMergeBase(ctx, sourceWorkspaceID, targetWorkspaceID)
	if err != nil {
		return "", "", nil, err
	}
	if _, _, err := s.requireWorkspaceViewAccess(ctx, baseWorkspaceID); err != nil {
		return "", "", nil, err
	}
	_, snapshot, err := s.resolveWorkspaceSnapshot(ctx, baseWorkspaceID, baseSnapshotID, false)
	if err != nil {
		return "", "", nil, err
	}
	return baseWorkspaceID, baseSnapshotID, snapshot, nil
}

func (s *filesystemServiceServer) inferMergeBase(ctx context.Context, sourceWorkspaceID, targetWorkspaceID string) (string, string, error) {
	if sourceWorkspaceID == "" || targetWorkspaceID == "" {
		return "", "", status.Error(codes.InvalidArgument, "workspace ids are required to infer merge base")
	}

	lookups := []struct {
		workspaceID   string
		counterpartID string
	}{
		{workspaceID: sourceWorkspaceID, counterpartID: targetWorkspaceID},
		{workspaceID: targetWorkspaceID, counterpartID: sourceWorkspaceID},
	}

	for _, lookup := range lookups {
		commits, err := s.storage.ListSliceCommits(ctx, lookup.workspaceID, 200, "")
		if err != nil {
			if err == storage.ErrSliceNotFound {
				continue
			}
			return "", "", status.Error(codes.Internal, fmt.Sprintf("failed to list workspace commits: %v", err))
		}
		for _, commit := range commits {
			if commit == nil {
				continue
			}
			baseWorkspaceID, baseSnapshotID, ok := parseForkProvenance(commit.Message)
			if ok && baseWorkspaceID == lookup.counterpartID && baseSnapshotID != "" {
				return baseWorkspaceID, baseSnapshotID, nil
			}
		}
	}

	return "", "", status.Error(codes.InvalidArgument, "base_snapshot_id is required when merge base cannot be inferred")
}

func parseForkProvenance(message string) (string, string, bool) {
	const prefix = "fork from "

	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, prefix) {
		return "", "", false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	at := strings.LastIndex(rest, "@")
	if at <= 0 || at >= len(rest)-1 {
		return "", "", false
	}

	workspaceID := strings.TrimSpace(rest[:at])
	snapshotID := strings.TrimSpace(rest[at+1:])
	if workspaceID == "" || snapshotID == "" {
		return "", "", false
	}
	return workspaceID, snapshotID, true
}

func (s *filesystemServiceServer) buildMergedWorkspaceSnapshot(ctx context.Context, baseSnapshot, sourceSnapshot, targetSnapshot *models.CommitSnapshot) (*models.CommitSnapshot, []string, []*filesystemv1.MergeConflict) {
	baseSnapshot = normalizedSnapshot(baseSnapshot)
	sourceSnapshot = normalizedSnapshot(sourceSnapshot)
	targetSnapshot = normalizedSnapshot(targetSnapshot)

	mergedFiles := make(map[string]string, len(targetSnapshot.Files))
	for filePath, contentHash := range targetSnapshot.Files {
		mergedFiles[filePath] = strings.TrimSpace(contentHash)
	}

	pathSet := make(map[string]struct{}, len(baseSnapshot.Files)+len(sourceSnapshot.Files)+len(targetSnapshot.Files))
	for filePath := range baseSnapshot.Files {
		pathSet[filePath] = struct{}{}
	}
	for filePath := range sourceSnapshot.Files {
		pathSet[filePath] = struct{}{}
	}
	for filePath := range targetSnapshot.Files {
		pathSet[filePath] = struct{}{}
	}

	paths := make([]string, 0, len(pathSet))
	for filePath := range pathSet {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	mergedPaths := make([]string, 0)
	conflicts := make([]*filesystemv1.MergeConflict, 0)
	for _, filePath := range paths {
		baseHash := strings.TrimSpace(baseSnapshot.Files[filePath])
		sourceHash := strings.TrimSpace(sourceSnapshot.Files[filePath])
		targetHash := strings.TrimSpace(targetSnapshot.Files[filePath])

		switch {
		case sourceHash == targetHash:
			continue
		case sourceHash == baseHash:
			continue
		case targetHash == baseHash:
			if sourceHash == "" {
				delete(mergedFiles, filePath)
			} else {
				mergedFiles[filePath] = sourceHash
			}
			mergedPaths = append(mergedPaths, filePath)
		default:
			sourcePatch, _, _ := s.buildFilesystemDiffPatch(ctx, filePath, baseHash, sourceHash)
			targetPatch, _, _ := s.buildFilesystemDiffPatch(ctx, filePath, baseHash, targetHash)
			conflicts = append(conflicts, &filesystemv1.MergeConflict{
				Path:             filePath,
				BaseHash:         baseHash,
				SourceHash:       sourceHash,
				TargetHash:       targetHash,
				SourceChangeType: diffChangeTypeForHashes(baseHash, sourceHash),
				TargetChangeType: diffChangeTypeForHashes(baseHash, targetHash),
				SourcePatch:      sourcePatch,
				TargetPatch:      targetPatch,
			})
		}
	}

	return &models.CommitSnapshot{
		SliceID: targetSnapshot.SliceID,
		Files:   mergedFiles,
	}, mergedPaths, conflicts
}

func normalizedSnapshot(snapshot *models.CommitSnapshot) *models.CommitSnapshot {
	if snapshot == nil {
		return &models.CommitSnapshot{Files: map[string]string{}}
	}
	if snapshot.Files == nil {
		snapshot.Files = map[string]string{}
	}
	return snapshot
}

func diffChangeTypeForHashes(oldHash, newHash string) filesystemv1.DiffChangeType {
	oldHash = strings.TrimSpace(oldHash)
	newHash = strings.TrimSpace(newHash)
	switch {
	case oldHash == "" && newHash == "":
		return filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_UNSPECIFIED
	case oldHash == "":
		return filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_ADD
	case newHash == "":
		return filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_DELETE
	default:
		return filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY
	}
}

func (s *filesystemServiceServer) listVisibleWorkspaceConflicts(ctx context.Context, username, workspaceID, otherWorkspaceID string) ([]*filesystemv1.WorkspaceConflict, error) {
	rawConflicts, err := s.storage.ListConflicts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}

	conflicts := make([]*filesystemv1.WorkspaceConflict, 0, len(rawConflicts))
	for _, conflict := range rawConflicts {
		if conflict == nil {
			continue
		}

		pathValue := strings.TrimSpace(conflict.Path)
		if pathValue == "" {
			pathValue = common.CleanRelativePath(conflict.FileID)
		}
		if pathValue == "" {
			continue
		}

		workspaceIDs := append([]string(nil), conflict.ConflictingSlices...)
		sort.Strings(workspaceIDs)
		if !containsString(workspaceIDs, workspaceID) {
			continue
		}
		if otherWorkspaceID != "" && !containsString(workspaceIDs, otherWorkspaceID) {
			continue
		}

		visible := true
		for _, conflictingWorkspaceID := range workspaceIDs {
			otherWorkspace, err := s.storage.GetSlice(ctx, conflictingWorkspaceID)
			if err != nil {
				visible = false
				break
			}
			if !canViewWorkspace(otherWorkspace, username) {
				visible = false
				break
			}
		}
		if !visible {
			continue
		}

		conflicts = append(conflicts, &filesystemv1.WorkspaceConflict{
			Path:         pathValue,
			WorkspaceIds: workspaceIDs,
		})
	}

	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].GetPath() < conflicts[j].GetPath()
	})
	return conflicts, nil
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
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
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	username := identity.Username
	if _, err := s.storage.EnsureUser(ctx, username); err != nil {
		return "", status.Error(codes.InvalidArgument, "invalid user")
	}
	return username, nil
}

func (s *filesystemServiceServer) optionalUsername(ctx context.Context) (string, error) {
	identity, err := authresolver.OptionalGRPCIdentity(ctx, s.storage)
	if err != nil {
		return "", err
	}
	if identity == nil {
		return "", nil
	}
	return strings.TrimSpace(identity.Username), nil
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

	username, err := s.requireUser(ctx)
	if err != nil {
		return nil, nil, err
	}
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
		Visibility:     modelVisibilityToFilesystemProto(workspace.Visibility),
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

func (s *filesystemServiceServer) finalizeWorkspaceMutation(ctx context.Context, workspace *models.Slice, homeMode bool, message string, modifiedPaths []string) (string, error) {
	commitHash, commitTime, err := s.commitWorkspaceMutation(ctx, workspace, message, modifiedPaths)
	if err != nil {
		return "", err
	}
	if !homeMode {
		return commitHash, nil
	}
	modified := normalizePromotionPaths(modifiedPaths)
	if len(modified) == 0 {
		currentPaths, _, err := s.workspaceStats(ctx, workspace.ID)
		if err != nil {
			return "", status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace paths for promotion: %v", err))
		}
		modified = normalizePromotionPaths(currentPaths)
	}
	if err := s.enqueueHomeSlicePromotion(ctx, workspace.ID, commitHash, modified, commitTime); err != nil {
		return "", status.Error(codes.Internal, fmt.Sprintf("failed to enqueue root promotion: %v", err))
	}
	return commitHash, nil
}

func (s *filesystemServiceServer) commitWorkspaceMutation(ctx context.Context, workspace *models.Slice, message string, modifiedPaths []string) (string, time.Time, error) {
	if workspace == nil {
		return "", time.Time{}, status.Error(codes.Internal, "workspace is nil")
	}

	meta, err := s.storage.GetSliceMetadata(ctx, workspace.ID)
	if err != nil {
		return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to load workspace metadata: %v", err))
	}

	paths, _, err := s.workspaceStats(ctx, workspace.ID)
	if err != nil {
		return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace paths: %v", err))
	}

	files := make(map[string]string)
	previousFiles := make(map[string]string)
	if strings.TrimSpace(meta.HeadCommitHash) != "" {
		parentSnapshot, err := s.storage.GetCommitSnapshot(ctx, meta.HeadCommitHash)
		if err != nil && err != storage.ErrCommitNotFound {
			return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to load parent snapshot: %v", err))
		}
		if parentSnapshot != nil && parentSnapshot.Files != nil {
			for filePath, fileHash := range parentSnapshot.Files {
				cleanedPath := common.CleanRelativePath(filePath)
				cleanedHash := strings.TrimSpace(fileHash)
				if cleanedPath == "" {
					continue
				}
				files[cleanedPath] = cleanedHash
				previousFiles[cleanedPath] = cleanedHash
			}
		}
	}
	changedPaths := normalizePromotionPaths(modifiedPaths)
	if len(changedPaths) == 0 {
		files, err = s.collectWorkspaceSnapshotFiles(ctx, workspace.ID)
		if err != nil {
			return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to collect workspace snapshot: %v", err))
		}
	} else {
		for _, filePath := range changedPaths {
			manifest, err := s.storage.GetFileManifest(ctx, workspace.ID, filePath)
			if err == nil && manifest != nil {
				files[filePath] = strings.TrimSpace(manifest.Hash)
				continue
			}
			if err == storage.ErrEntryNotFound {
				delete(files, filePath)
				continue
			}
			if err != nil {
				return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to load file manifest: %v", err))
			}
		}
	}

	now := time.Now()
	commitHash := fmt.Sprintf("fs-%d", now.UnixNano())
	if err := s.storage.AddSliceCommit(ctx, workspace.ID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: meta.HeadCommitHash,
		Timestamp:  now,
		Message:    message,
	}); err != nil {
		return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to record workspace commit: %v", err))
	}
	if err := s.storage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    workspace.ID,
		Files:      files,
		Timestamp:  now,
	}); err != nil {
		return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to save workspace snapshot: %v", err))
	}
	if err := s.storage.UpdateSliceMetadata(ctx, workspace.ID, &models.SliceMetadata{
		SliceID:            workspace.ID,
		HeadCommitHash:     commitHash,
		ModifiedFiles:      paths,
		LastModified:       now,
		ModifiedFilesCount: len(paths),
	}); err != nil {
		return "", time.Time{}, status.Error(codes.Internal, fmt.Sprintf("failed to update workspace metadata: %v", err))
	}
	if _, err := storage.BuildAndStoreWorkspaceSearchArtifact(ctx, s.storage, workspace.ID, commitHash); err != nil {
		log.Printf("filesystem: failed to refresh search artifact for commit %s in %s: %v", commitHash, workspace.ID, err)
	}
	if err := s.recordWorkspaceFileChanges(ctx, workspace, commitHash, meta.HeadCommitHash, message, now, modifiedPaths, previousFiles, files); err != nil {
		log.Printf("filesystem: failed to index file changes for commit %s in %s: %v", commitHash, workspace.ID, err)
	}
	return commitHash, now, nil
}

type filesystemRenameChange struct {
	oldPath string
	newPath string
	hash    string
}

func (s *filesystemServiceServer) recordWorkspaceFileChanges(ctx context.Context, workspace *models.Slice, commitHash, parentHash, message string, timestamp time.Time, modifiedPaths []string, previousFiles, currentFiles map[string]string) error {
	if workspace == nil {
		return fmt.Errorf("workspace is nil")
	}

	author, err := s.optionalUsername(ctx)
	if err != nil {
		return err
	}
	if author == "" {
		author = "system"
	}

	renames := detectFilesystemRenames(message, modifiedPaths, previousFiles, currentFiles)
	handledPaths := make(map[string]struct{}, len(renames)*2)
	changes := make([]*models.FileChangeRecord, 0, len(previousFiles)+len(currentFiles))
	for _, rename := range renames {
		handledPaths[rename.oldPath] = struct{}{}
		handledPaths[rename.newPath] = struct{}{}
		changes = append(changes, &models.FileChangeRecord{
			ID:         fmt.Sprintf("%s-%s", commitHash, rename.newPath),
			SliceID:    workspace.ID,
			CommitHash: commitHash,
			Path:       rename.newPath,
			OldPath:    rename.oldPath,
			ChangeType: models.ChangeTypeRename,
			OldHash:    rename.hash,
			NewHash:    rename.hash,
			Author:     author,
			Message:    message,
			Timestamp:  timestamp,
		})
	}

	pathSet := make(map[string]struct{}, len(previousFiles)+len(currentFiles))
	for filePath := range previousFiles {
		pathSet[filePath] = struct{}{}
	}
	for filePath := range currentFiles {
		pathSet[filePath] = struct{}{}
	}

	paths := make([]string, 0, len(pathSet))
	for filePath := range pathSet {
		if _, handled := handledPaths[filePath]; handled {
			continue
		}
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	for _, filePath := range paths {
		oldHash := strings.TrimSpace(previousFiles[filePath])
		newHash := strings.TrimSpace(currentFiles[filePath])
		if oldHash == newHash {
			continue
		}

		changeType := models.ChangeTypeModify
		switch {
		case oldHash == "":
			changeType = models.ChangeTypeAdd
		case newHash == "":
			changeType = models.ChangeTypeDelete
		}

		_, linesAdded, linesDeleted := s.buildFilesystemDiffPatch(ctx, filePath, oldHash, newHash)
		changes = append(changes, &models.FileChangeRecord{
			ID:           fmt.Sprintf("%s-%s", commitHash, filePath),
			SliceID:      workspace.ID,
			CommitHash:   commitHash,
			Path:         filePath,
			ChangeType:   changeType,
			OldHash:      oldHash,
			NewHash:      newHash,
			LinesAdded:   linesAdded,
			LinesDeleted: linesDeleted,
			Author:       author,
			Message:      message,
			Timestamp:    timestamp,
		})
	}

	if len(changes) == 0 {
		return nil
	}
	return s.storage.AddFileChanges(ctx, changes)
}

func detectFilesystemRenames(message string, modifiedPaths []string, previousFiles, currentFiles map[string]string) []filesystemRenameChange {
	if !strings.HasPrefix(strings.TrimSpace(message), "move ") {
		return nil
	}

	normalized := normalizePromotionPaths(modifiedPaths)
	if len(normalized) != 2 {
		return nil
	}

	addedPaths := make([]string, 0, 1)
	deletedPaths := make([]string, 0, 1)
	for _, filePath := range normalized {
		oldHash := strings.TrimSpace(previousFiles[filePath])
		newHash := strings.TrimSpace(currentFiles[filePath])
		switch {
		case oldHash != "" && newHash == "":
			deletedPaths = append(deletedPaths, filePath)
		case oldHash == "" && newHash != "":
			addedPaths = append(addedPaths, filePath)
		}
	}

	if len(addedPaths) != 1 || len(deletedPaths) != 1 {
		return nil
	}

	oldPath := deletedPaths[0]
	newPath := addedPaths[0]
	oldHash := strings.TrimSpace(previousFiles[oldPath])
	newHash := strings.TrimSpace(currentFiles[newPath])
	if oldHash == "" || oldHash != newHash {
		return nil
	}

	return []filesystemRenameChange{{
		oldPath: oldPath,
		newPath: newPath,
		hash:    oldHash,
	}}
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
		manifest, err := s.storage.GetFileManifest(ctx, workspaceID, entry.Path)
		if err == nil && manifest != nil {
			files[entry.Path] = strings.TrimSpace(manifest.Hash)
			continue
		}
		if err != nil && err != storage.ErrEntryNotFound {
			return nil, err
		}
		content, err := storage.ReadSliceFileContent(ctx, s.storage, workspaceID, entry.Path)
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

func (s *filesystemServiceServer) resolveOperationWorkspace(ctx context.Context, workspaceID, username string, requireWrite bool) (*models.Slice, *models.SliceMetadata, bool, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || workspaceID == homeslice.IDForUsername(username) {
		workspace, err := homeslice.EnsureUserHomeSlice(ctx, s.storage, username)
		if err != nil {
			return nil, nil, false, status.Error(codes.Internal, fmt.Sprintf("failed to ensure home slice: %v", err))
		}
		meta, err := s.storage.GetSliceMetadata(ctx, workspace.ID)
		if err != nil {
			return nil, nil, false, status.Error(codes.Internal, fmt.Sprintf("failed to load workspace metadata: %v", err))
		}
		return workspace, meta, true, nil
	}

	if requireWrite {
		workspace, meta, err := s.requireWorkspaceWriteAccess(ctx, workspaceID, username)
		return workspace, meta, false, err
	}
	workspace, meta, err := s.requireWorkspaceViewAccess(ctx, workspaceID)
	return workspace, meta, false, err
}

func (s *filesystemServiceServer) resolveOperationPath(username string, homeMode bool, raw string, required bool) (string, string, error) {
	if !homeMode {
		filePath, err := validateWorkspacePath(raw, required)
		return filePath, filePath, err
	}

	storedPath, displayPath, err := homeslice.ResolveVisiblePath(username, raw, required)
	if err != nil {
		return "", "", homePathErrorToStatus(err, "path")
	}
	return storedPath, displayPath, nil
}

func (s *filesystemServiceServer) resolveOperationPattern(username string, homeMode bool, raw string, required bool) (string, error) {
	if !homeMode {
		return validateGlobPattern(raw, required)
	}

	storedPattern, err := homeslice.ResolveVisiblePattern(username, raw, required)
	if err != nil {
		return "", homePathErrorToStatus(err, "pattern")
	}
	return validateGlobPattern(storedPattern, required)
}

func entryToProto(entry *models.DirectoryEntry, homeMode bool, effectiveVisibility ...commonv1.Visibility) *filesystemv1.WorkspaceEntry {
	if entry == nil {
		return nil
	}
	visibilityValue := commonv1.Visibility_VISIBILITY_PRIVATE
	if len(effectiveVisibility) > 0 {
		visibilityValue = effectiveVisibility[0]
	}
	displayPath := displayOperationPath(entry.Path, homeMode)
	return &filesystemv1.WorkspaceEntry{
		Name:                path.Base(displayPath),
		Path:                displayPath,
		Type:                entryTypeToProto(entry.Type),
		Size:                entry.Size,
		Hash:                strings.TrimSpace(entry.Hash),
		EffectiveVisibility: visibilityValue,
	}
}

func displayOperationPath(storedPath string, homeMode bool) string {
	if !homeMode {
		return storedPath
	}
	return homeslice.VisiblePathForStored(storedPath)
}

func displaySearchGlob(storedPattern string, homeMode bool) string {
	if !homeMode || strings.TrimSpace(storedPattern) == "" {
		return storedPattern
	}
	return homeslice.VisiblePathForStored(storedPattern)
}

func homePathErrorToStatus(err error, noun string) error {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	switch {
	case strings.Contains(message, "must stay under"):
		return status.Error(codes.PermissionDenied, message)
	case strings.Contains(message, "must be absolute"):
		return status.Error(codes.InvalidArgument, fmt.Sprintf("%s must be absolute", noun))
	default:
		return status.Error(codes.InvalidArgument, message)
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
