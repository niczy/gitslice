package sliceservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sliceServiceServer struct {
	slicev1.UnimplementedSliceServiceServer
	storage storage.Storage
}

func newSliceServiceServer(st storage.Storage) *sliceServiceServer {
	return &sliceServiceServer{
		storage: st,
	}
}

// RegisterGRPCServer registers the slice service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	slicev1.RegisterSliceServiceServer(srv, newSliceServiceServer(st))
}

// NewGRPCServer constructs a gRPC server for the slice service using the provided storage backend.
func NewGRPCServer(st storage.Storage) *grpc.Server {
	srv := grpc.NewServer()
	RegisterGRPCServer(srv, st)
	return srv
}

// NewService constructs the slice service implementation for use without gRPC.
func NewService(st storage.Storage) slicev1.SliceServiceServer {
	return newSliceServiceServer(st)
}

// RunGenesisInit populates the root slice from the git repository by creating
// and merging a changeset through the service's own RPC methods.
func RunGenesisInit(ctx context.Context, st storage.Storage) error {
	svc := newSliceServiceServer(st)
	return svc.PopulateGenesisFromGit(ctx)
}

func (s *sliceServiceServer) CheckoutSlice(ctx context.Context, req *slicev1.CheckoutRequest) (*slicev1.CheckoutResponse, error) {
	log.Printf("CheckoutSlice called: slice_id=%s, commit_hash=%s", req.SliceId, req.CommitHash)

	// Validate slice ID
	if err := common.ValidateSliceID(req.SliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	// Get slice metadata
	metadata, err := s.storage.GetSliceMetadata(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	// Get slice
	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	// Get file contents
	files, err := s.storage.GetSliceFiles(ctx, req.SliceId)
	if err != nil {
		files = []*models.FileContent{}
	}

	// Build manifest with file metadata
	var fileMetadata []*slicev1.FileMetadata
	for _, file := range files {
		storedPath := file.Path
		if strings.TrimSpace(storedPath) == "" {
			storedPath = file.FileID
		}
		fileMetadata = append(fileMetadata, &slicev1.FileMetadata{
			FileId:     file.FileID,
			Path:       common.SliceDisplayPath(slice, storedPath),
			Size:       file.Size,
			Hash:       file.Hash,
			ContentUrl: "", // No presigned URL for in-memory storage
		})
	}

	// If no files in storage, create metadata from slice definition
	if len(fileMetadata) == 0 {
		for _, fileID := range slice.Files {
			fileMetadata = append(fileMetadata, &slicev1.FileMetadata{
				FileId:     fileID,
				Path:       common.SliceDisplayPath(slice, fileID),
				Size:       0,
				Hash:       "",
				ContentUrl: "",
			})
		}
	}

	// Convert file contents to proto format
	var fileContents []*slicev1.FileContent
	for _, file := range files {
		fileContents = append(fileContents, &slicev1.FileContent{
			FileId:  file.FileID,
			Content: file.Content,
		})
	}

	manifest := &slicev1.SliceManifest{
		CommitHash:   metadata.HeadCommitHash,
		FileMetadata: fileMetadata,
	}

	return &slicev1.CheckoutResponse{
		Manifest: manifest,
		Files:    fileContents,
	}, nil
}

func (s *sliceServiceServer) CreateChangeset(ctx context.Context, req *slicev1.CreateChangesetRequest) (*slicev1.CreateChangesetResponse, error) {
	log.Printf("CreateChangeset called: slice_id=%s, author=%s", req.SliceId, req.Author)

	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}

	// Validate slice ID
	if err := common.ValidateSliceID(req.SliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice ID: %v", err))
	}

	// Validate modified files
	for _, fileID := range req.ModifiedFiles {
		if err := common.ValidateFileID(fileID); err != nil {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid file ID %s: %v", fileID, err))
		}
	}

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	id := fmt.Sprintf("cs-%d", time.Now().UnixNano())
	hash := fmt.Sprintf("hash-%d", time.Now().UnixNano())

	cs := &models.Changeset{
		ID:             id,
		Hash:           hash,
		SliceID:        req.SliceId,
		BaseCommitHash: req.BaseCommitHash,
		ModifiedFiles:  req.ModifiedFiles,
		Status:         models.ChangesetStatusPending,
		Author:         username,
		Message:        req.Message,
		CreatedAt:      time.Now(),
	}

	if err := s.storage.CreateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create changeset: %v", err))
	}

	return &slicev1.CreateChangesetResponse{
		ChangesetId:   cs.ID,
		ChangesetHash: cs.Hash,
		Status:        slicev1.ChangesetStatus_PENDING,
	}, nil
}

func (s *sliceServiceServer) ReviewChangeset(ctx context.Context, req *slicev1.ReviewChangesetRequest) (*slicev1.ReviewChangesetResponse, error) {
	log.Printf("ReviewChangeset called: changeset_id=%s", req.ChangesetId)

	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	diff := &slicev1.DiffSummary{
		FilesAdded:    int32(len(cs.ModifiedFiles)),
		FilesModified: 0,
		FilesDeleted:  0,
		LinesAdded:    int64(len(cs.ModifiedFiles)),
		LinesRemoved:  0,
	}

	return &slicev1.ReviewChangesetResponse{
		Changeset:    convertChangesetToProto(cs),
		Diff:         diff,
		ReviewStatus: slicev1.ReviewStatus_READY_FOR_MERGE,
		Warnings:     []string{},
	}, nil
}

func (s *sliceServiceServer) MergeChangeset(ctx context.Context, req *slicev1.MergeChangesetRequest) (*slicev1.MergeChangesetResponse, error) {
	log.Printf("MergeChangeset called: changeset_id=%s", req.ChangesetId)

	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	if err := s.storage.LockSliceAndFiles(ctx, cs.SliceID, cs.ModifiedFiles); err != nil {
		if errors.Is(err, storage.ErrLockHeld) {
			return nil, status.Error(codes.Aborted, "slice or files are locked by another operation")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to acquire locks: %v", err))
	}
	defer s.storage.UnlockSliceAndFiles(ctx, cs.SliceID, cs.ModifiedFiles)

	var conflicts []*slicev1.Conflict
	for _, fileID := range cs.ModifiedFiles {
		slices, err := s.storage.GetActiveSlicesForFile(ctx, fileID)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to check conflicts: %v", err))
		}

		var conflictingSlices []string
		for _, sliceID := range slices {
			if sliceID != cs.SliceID {
				conflictingSlices = append(conflictingSlices, sliceID)
			}
		}

		if len(conflictingSlices) > 0 {
			conflicts = append(conflicts, &slicev1.Conflict{FileId: fileID, ConflictingSliceIds: conflictingSlices})
		}
	}

	if len(conflicts) > 0 {
		return &slicev1.MergeChangesetResponse{
			Status:        slicev1.MergeStatus_MERGE_STATUS_CONFLICT,
			NewCommitHash: "",
			ChangesetId:   cs.ID,
			Conflicts:     conflicts,
		}, nil
	}

	for _, fileID := range cs.ModifiedFiles {
		if _, err := s.storage.ResolveConflict(ctx, fileID, cs.SliceID); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve conflicts for %s: %v", fileID, err))
		}
		if err := s.storage.AddFileToSlice(ctx, fileID, cs.SliceID); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to mark file ownership: %v", err))
		}
	}

	newCommit := fmt.Sprintf("commit-%d", time.Now().UnixNano())
	cs.Status = models.ChangesetStatusMerged
	now := time.Now()
	cs.MergedAt = &now

	if err := s.storage.UpdateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update changeset: %v", err))
	}

	metadata, err := s.storage.GetSliceMetadata(ctx, cs.SliceID)
	if err == nil {
		parentHash := metadata.HeadCommitHash
		metadata.HeadCommitHash = newCommit
		metadata.ModifiedFiles = cs.ModifiedFiles
		metadata.ModifiedFilesCount = len(cs.ModifiedFiles)

		if err := s.storage.UpdateSliceMetadata(ctx, cs.SliceID, metadata); err != nil {
			log.Printf("Warning: failed to update slice metadata for %s: %v", cs.SliceID, err)
		}

		if err := s.storage.AddSliceCommit(ctx, cs.SliceID, &models.Commit{
			CommitHash: newCommit,
			ParentHash: parentHash,
			Timestamp:  now,
			Message:    cs.Message,
		}); err != nil {
			log.Printf("Warning: failed to add commit to slice %s: %v", cs.SliceID, err)
		}

		// Create commit snapshot for versioned file access
		if err := s.createCommitSnapshot(ctx, cs.SliceID, newCommit, now); err != nil {
			log.Printf("Warning: failed to create commit snapshot for %s: %v", newCommit, err)
		}

		// Record file change history for each modified file
		if err := s.recordFileChanges(ctx, cs, newCommit, parentHash, now); err != nil {
			log.Printf("Warning: failed to record file changes for commit %s: %v", newCommit, err)
		}

		if err := s.promoteSlice(ctx, cs.SliceID, newCommit, cs.ModifiedFiles, now); err != nil {
			log.Printf("failed to promote slice %s to global state: %v", cs.SliceID, err)
		}
	}

	return &slicev1.MergeChangesetResponse{
		Status:        slicev1.MergeStatus_MERGE_STATUS_SUCCESS,
		NewCommitHash: newCommit,
		ChangesetId:   cs.ID,
		Conflicts:     []*slicev1.Conflict{},
	}, nil
}

func (s *sliceServiceServer) RebaseChangeset(ctx context.Context, req *slicev1.RebaseChangesetRequest) (*slicev1.RebaseChangesetResponse, error) {
	log.Printf("RebaseChangeset called: changeset_id=%s", req.ChangesetId)

	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", cs.SliceID))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	newBase := fmt.Sprintf("base-%d", time.Now().UnixNano())
	cs.BaseCommitHash = newBase
	if err := s.storage.UpdateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update changeset: %v", err))
	}

	return &slicev1.RebaseChangesetResponse{
		Status:              slicev1.RebaseStatus_REBASE_STATUS_SUCCESS,
		NewBaseCommitHash:   newBase,
		SliceCommitsToApply: []string{},
		Conflicts:           []*slicev1.Conflict{},
	}, nil
}

func (s *sliceServiceServer) GetSliceCommits(ctx context.Context, req *slicev1.CommitHistoryRequest) (*slicev1.CommitHistoryResponse, error) {
	log.Printf("GetSliceCommits called: slice_id=%s", req.SliceId)

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		if err == storage.ErrSliceNotFound {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice: %v", err))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	commits, err := s.storage.ListSliceCommits(ctx, req.SliceId, int(req.Limit), req.FromCommitHash)
	if err != nil {
		if err == storage.ErrSliceNotFound {
			return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list commits: %v", err))
	}

	response := &slicev1.CommitHistoryResponse{}
	for _, commit := range commits {
		response.Commits = append(response.Commits, &slicev1.CommitInfo{
			CommitHash: commit.CommitHash,
			Timestamp:  commit.Timestamp.Unix(),
			ParentHash: commit.ParentHash,
			Message:    commit.Message,
		})
	}

	return response, nil
}

func (s *sliceServiceServer) GetSliceState(ctx context.Context, req *slicev1.StateRequest) (*slicev1.StateResponse, error) {
	log.Printf("GetSliceState called: slice_id=%s", req.SliceId)

	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	username := auth.UsernameFromGRPCContext(ctx)
	if !authz.HasSliceViewAccess(slice, username) {
		if username == "" {
			return nil, status.Error(codes.Unauthenticated, "login required")
		}
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	// Get slice metadata
	metadata, err := s.storage.GetSliceMetadata(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	return &slicev1.StateResponse{
		LatestCommitHash: metadata.HeadCommitHash,
		ModifiedFiles:    metadata.ModifiedFiles,
		LastModified:     metadata.LastModified.Unix(),
	}, nil
}

func (s *sliceServiceServer) ListChangesets(ctx context.Context, req *slicev1.ListChangesetsRequest) (*slicev1.ListChangesetsResponse, error) {
	log.Printf("ListChangesets called: slice_id=%s", req.SliceId)

	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}
	slice, err := s.storage.GetSlice(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}
	if !authz.HasSliceViewAccess(slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	var statusFilter *models.ChangesetStatus
	if req.StatusFilter >= 0 {
		converted := convertProtoStatusToModel(req.StatusFilter)
		statusFilter = &converted
	}

	changesets, err := s.storage.ListChangesets(ctx, req.SliceId, statusFilter, int(req.Limit))
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changesets: %v", err))
	}

	response := &slicev1.ListChangesetsResponse{}
	for _, cs := range changesets {
		response.Changesets = append(response.Changesets, convertChangesetToProto(cs))
	}

	return response, nil
}

func convertChangesetToProto(cs *models.Changeset) *slicev1.ChangesetInfo {
	status := slicev1.ChangesetStatus_PENDING
	switch cs.Status {
	case models.ChangesetStatusApproved:
		status = slicev1.ChangesetStatus_APPROVED
	case models.ChangesetStatusRejected:
		status = slicev1.ChangesetStatus_REJECTED
	case models.ChangesetStatusMerged:
		status = slicev1.ChangesetStatus_MERGED
	}

	var mergedAt int64
	if cs.MergedAt != nil {
		mergedAt = cs.MergedAt.Unix()
	}

	return &slicev1.ChangesetInfo{
		ChangesetId:    cs.ID,
		ChangesetHash:  cs.Hash,
		SliceId:        cs.SliceID,
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  cs.ModifiedFiles,
		Status:         status,
		Author:         cs.Author,
		Message:        cs.Message,
		CreatedAt:      cs.CreatedAt.Unix(),
		MergedAt:       mergedAt,
	}
}

func convertProtoStatusToModel(status slicev1.ChangesetStatus) models.ChangesetStatus {
	switch status {
	case slicev1.ChangesetStatus_APPROVED:
		return models.ChangesetStatusApproved
	case slicev1.ChangesetStatus_REJECTED:
		return models.ChangesetStatusRejected
	case slicev1.ChangesetStatus_MERGED:
		return models.ChangesetStatusMerged
	default:
		return models.ChangesetStatusPending
	}
}

func (s *sliceServiceServer) GetRootSlice(ctx context.Context, req *slicev1.GetRootSliceRequest) (*slicev1.GetRootSliceResponse, error) {
	log.Printf("GetRootSlice called")

	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return nil, status.Error(codes.NotFound, "root slice not found")
	}

	metadata, _ := s.storage.GetSliceMetadata(ctx, rootSlice.ID)

	return &slicev1.GetRootSliceResponse{
		SliceId:    rootSlice.ID,
		CommitHash: metadata.HeadCommitHash,
	}, nil
}

func (s *sliceServiceServer) CreateSliceFromFolder(ctx context.Context, req *slicev1.CreateSliceFromFolderRequest) (*slicev1.CreateSliceFromFolderResponse, error) {
	log.Printf("CreateSliceFromFolder called: parent_slice_id=%s, folder_path=%s, folder_paths=%d, new_slice_id=%s",
		req.ParentSliceId, req.FolderPath, len(req.FolderPaths), req.NewSliceId)

	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}

	// Validate IDs and paths
	if err := common.ValidateSliceID(req.ParentSliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid parent slice ID: %v", err))
	}
	if err := common.ValidateSliceID(req.NewSliceId); err != nil {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid new slice ID: %v", err))
	}
	folderPaths, err := collectRequestedFolderPaths(req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	parentSlice, err := s.storage.GetSlice(ctx, req.ParentSliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("parent slice not found: %s", req.ParentSliceId))
	}
	if !authz.HasSliceViewAccess(parentSlice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for parent slice")
	}

	selectedFiles := make([]string, 0)
	if len(folderPaths) > 0 {
		selectedSet := make(map[string]struct{})
		for _, folderPath := range folderPaths {
			prefix := folderPath + "/"
			for _, rawFileID := range parentSlice.Files {
				fileID := common.CleanRelativePath(rawFileID)
				if fileID == folderPath || strings.HasPrefix(fileID, prefix) {
					selectedSet[fileID] = struct{}{}
				}
			}
		}
		for fileID := range selectedSet {
			selectedFiles = append(selectedFiles, fileID)
		}
		sort.Strings(selectedFiles)
	}

	newSlice := &models.Slice{
		ID:           req.NewSliceId,
		Name:         req.Name,
		Description:  req.Description,
		Files:        selectedFiles,
		FolderMounts: buildSliceFolderMounts(folderPaths),
		Owners:       []string{username},
		CreatedBy:    username,
		ParentSlice:  parentSlice.ID,
		IsRoot:       false,
	}

	if err := s.storage.CreateSlice(ctx, newSlice); err != nil {
		if errors.Is(err, storage.ErrSliceAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("slice already exists: %s", req.NewSliceId))
		}
		if errors.Is(err, storage.ErrInvalidInput) {
			return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid slice: %v", err))
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to create slice: %v", err))
	}

	return &slicev1.CreateSliceFromFolderResponse{
		SliceId: req.NewSliceId,
		Status:  "created",
		Files:   selectedFiles,
	}, nil
}

func collectRequestedFolderPaths(req *slicev1.CreateSliceFromFolderRequest) ([]string, error) {
	paths := make([]string, 0, len(req.GetFolderPaths())+1)
	if raw := strings.TrimSpace(req.GetFolderPath()); raw != "" {
		paths = append(paths, raw)
	}
	paths = append(paths, req.GetFolderPaths()...)

	dedup := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		cleaned := common.CleanRelativePath(raw)
		if cleaned == "" {
			continue
		}
		if err := common.ValidateFilePath(cleaned); err != nil {
			return nil, fmt.Errorf("invalid folder path %q: %v", raw, err)
		}
		if _, exists := dedup[cleaned]; exists {
			continue
		}
		dedup[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized, nil
}

func buildSliceFolderMounts(folderPaths []string) []models.SliceFolderMount {
	if len(folderPaths) == 0 {
		return nil
	}

	mounts := make([]models.SliceFolderMount, 0, len(folderPaths))
	usedAliases := make(map[string]struct{}, len(folderPaths))
	for _, sourcePath := range folderPaths {
		alias := path.Base(sourcePath)
		if alias == "." || alias == "/" || alias == "" {
			alias = strings.ReplaceAll(sourcePath, "/", "_")
		}
		if alias == "" {
			alias = "folder"
		}

		baseAlias := alias
		for n := 2; ; n++ {
			if _, exists := usedAliases[alias]; !exists {
				break
			}
			alias = fmt.Sprintf("%s_%d", baseAlias, n)
		}
		usedAliases[alias] = struct{}{}

		mounts = append(mounts, models.SliceFolderMount{
			SourcePath: sourcePath,
			Alias:      alias,
		})
	}
	return mounts
}

func (s *sliceServiceServer) promoteSlice(ctx context.Context, sliceID, commitHash string, files []string, commitTime time.Time) error {
	// Ensure root slice is initialized
	if err := common.EnsureRootSliceInitialized(ctx, s.storage); err != nil {
		return fmt.Errorf("failed to ensure root slice: %w", err)
	}

	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return fmt.Errorf("failed to load root slice: %w", err)
	}

	rootMetadata, err := s.storage.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		return fmt.Errorf("failed to load root metadata: %w", err)
	}

	rootMetadata.HeadCommitHash = commitHash
	rootMetadata.ModifiedFiles = files
	rootMetadata.ModifiedFilesCount = len(files)
	rootMetadata.LastModified = commitTime

	if err := s.storage.UpdateSliceMetadata(ctx, rootSlice.ID, rootMetadata); err != nil {
		return fmt.Errorf("failed to update root metadata: %w", err)
	}

	for _, fileID := range files {
		if err := s.storage.AddFileToSlice(ctx, fileID, rootSlice.ID); err != nil {
			return fmt.Errorf("failed to add file to root slice: %w", err)
		}
	}

	state, err := s.storage.GetGlobalState(ctx)
	if err != nil && !errors.Is(err, storage.ErrInvalidInput) {
		return fmt.Errorf("failed to load global state: %w", err)
	}
	if state == nil {
		state = &models.GlobalState{}
	}

	newCommit := &models.GlobalCommit{CommitHash: commitHash, Timestamp: commitTime, MergedSliceIDs: []string{sliceID}}
	state.GlobalCommitHash = commitHash
	state.Timestamp = commitTime
	state.History = append([]*models.GlobalCommit{newCommit}, state.History...)

	if err := s.storage.UpdateGlobalState(ctx, state); err != nil {
		return fmt.Errorf("failed to update global state: %w", err)
	}

	if updatedState, err := s.storage.GetGlobalState(ctx); err == nil {
		rootMetadata.HeadCommitHash = updatedState.GlobalCommitHash
		rootMetadata.LastModified = updatedState.Timestamp
		if err := s.storage.UpdateSliceMetadata(ctx, rootSlice.ID, rootMetadata); err != nil {
			log.Printf("Warning: failed to update root slice metadata: %v", err)
		}
	}

	return nil
}

// createCommitSnapshot creates a snapshot of the current file state for a commit.
func (s *sliceServiceServer) createCommitSnapshot(ctx context.Context, sliceID, commitHash string, timestamp time.Time) error {
	// Get slice files
	slice, err := s.storage.GetSlice(ctx, sliceID)
	if err != nil {
		return fmt.Errorf("failed to get slice: %w", err)
	}

	// Build file hash map
	files := make(map[string]string)
	for _, fileID := range slice.Files {
		// Use the file path as the content hash for now
		// In a real implementation, this would be the actual content hash
		files[fileID] = fileID
	}

	snapshot := &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Files:      files,
		Timestamp:  timestamp,
	}

	return s.storage.SaveCommitSnapshot(ctx, snapshot)
}

// recordFileChanges creates FileChangeRecord entries for each file in the changeset.
func (s *sliceServiceServer) recordFileChanges(ctx context.Context, cs *models.Changeset, commitHash string, parentHash string, timestamp time.Time) error {
	// Build set of files that existed in the previous commit
	previousFiles := make(map[string]bool)
	if parentHash != "" {
		snapshot, err := s.storage.GetCommitSnapshot(ctx, parentHash)
		if err == nil && snapshot != nil {
			for path := range snapshot.Files {
				previousFiles[path] = true
			}
		}
	}

	var changes []*models.FileChangeRecord
	for _, filePath := range cs.ModifiedFiles {
		changeType := models.ChangeTypeModify
		if !previousFiles[filePath] {
			changeType = models.ChangeTypeAdd
		}

		var newHash string
		linesAdded := 0
		content, err := s.storage.GetSliceFileByPath(ctx, cs.SliceID, filePath)
		if err == nil && content != nil {
			newHash = content.Hash
			// Only report line counts for new files where the entire content is the delta.
			// For modifications we lack a proper diff against the parent, so leave at 0.
			if changeType == models.ChangeTypeAdd {
				linesAdded = strings.Count(string(content.Content), "\n") + 1
			}
		}

		changes = append(changes, &models.FileChangeRecord{
			ID:         fmt.Sprintf("%s-%s", commitHash, filePath),
			SliceID:    cs.SliceID,
			CommitHash: commitHash,
			Path:       filePath,
			ChangeType: changeType,
			LinesAdded: linesAdded,
			NewHash:    newHash,
			Author:     cs.Author,
			Message:    cs.Message,
			Timestamp:  timestamp,
		})
	}

	if len(changes) == 0 {
		return nil
	}
	return s.storage.AddFileChanges(ctx, changes)
}
