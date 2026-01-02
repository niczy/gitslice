package sliceservice

import (
	"context"
	"fmt"
	"log"
	"time"

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

// NewGRPCServer constructs a gRPC server for the slice service using the provided storage backend.
func NewGRPCServer(st storage.Storage) *grpc.Server {
	srv := grpc.NewServer()
	slicev1.RegisterSliceServiceServer(srv, newSliceServiceServer(st))
	return srv
}

// NewService constructs the slice service implementation for use without gRPC.
func NewService(st storage.Storage) slicev1.SliceServiceServer {
	return newSliceServiceServer(st)
}

func (s *sliceServiceServer) CheckoutSlice(ctx context.Context, req *slicev1.CheckoutRequest) (*slicev1.CheckoutResponse, error) {
	log.Printf("CheckoutSlice called: slice_id=%s, commit_hash=%s", req.SliceId, req.CommitHash)

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

	// Get file contents
	files, err := s.storage.GetSliceFiles(ctx, req.SliceId)
	if err != nil {
		files = []*models.FileContent{}
	}

	// Build manifest with file metadata
	var fileMetadata []*slicev1.FileMetadata
	for _, file := range files {
		fileMetadata = append(fileMetadata, &slicev1.FileMetadata{
			FileId:     file.FileID,
			Path:       file.Path,
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
				Path:       fileID,
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

	// Verify slice exists
	if _, err := s.storage.GetSlice(ctx, req.SliceId); err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
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
		Author:         req.Author,
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

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
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

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
	}

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
		_ = s.storage.UpdateSliceMetadata(ctx, cs.SliceID, metadata)

		_ = s.storage.AddSliceCommit(ctx, cs.SliceID, &models.Commit{
			CommitHash: newCommit,
			ParentHash: parentHash,
			Timestamp:  now,
			Message:    cs.Message,
		})
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

	cs, err := s.storage.GetChangeset(ctx, req.ChangesetId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("changeset not found: %s", req.ChangesetId))
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
	log.Printf("CreateSliceFromFolder called: parent_slice_id=%s, folder_path=%s, new_slice_id=%s",
		req.ParentSliceId, req.FolderPath, req.NewSliceId)

	parentSlice, err := s.storage.GetSlice(ctx, req.ParentSliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("parent slice not found: %s", req.ParentSliceId))
	}

	newSlice := &models.Slice{
		ID:          req.NewSliceId,
		Name:        req.Name,
		Description: req.Description,
		Files:       []string{},
		Owners:      parentSlice.Owners,
		CreatedBy:   "user",
		ParentSlice: parentSlice.ID,
		IsRoot:      false,
	}

	if err := s.storage.CreateSlice(ctx, newSlice); err != nil {
		return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("slice already exists: %s", req.NewSliceId))
	}

	return &slicev1.CreateSliceFromFolderResponse{
		SliceId: req.NewSliceId,
		Status:  "created",
		Files:   []string{},
	}, nil
}
