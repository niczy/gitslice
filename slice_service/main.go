package main

import (
	"context"
	"fmt"
	"log"
	"net"
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

func (s *sliceServiceServer) CheckoutSlice(ctx context.Context, req *slicev1.CheckoutRequest) (*slicev1.CheckoutResponse, error) {
	log.Printf("CheckoutSlice called: slice_id=%s, commit_hash=%s", req.SliceId, req.CommitHash)

	// Get slice metadata
	metadata, err := s.storage.GetSliceMetadata(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	// Return manifest with slice metadata
	manifest := &slicev1.SliceManifest{
		CommitHash:   metadata.HeadCommitHash,
		FileMetadata: []*slicev1.FileMetadata{},
	}

	return &slicev1.CheckoutResponse{
		Manifest: manifest,
		Files:    []*slicev1.FileContent{},
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

	newCommit := fmt.Sprintf("commit-%d", time.Now().UnixNano())
	cs.Status = models.ChangesetStatusMerged
	now := time.Now()
	cs.MergedAt = &now

	if err := s.storage.UpdateChangeset(ctx, cs); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update changeset: %v", err))
	}

	metadata, err := s.storage.GetSliceMetadata(ctx, cs.SliceID)
	if err == nil {
		metadata.HeadCommitHash = newCommit
		metadata.ModifiedFiles = cs.ModifiedFiles
		metadata.ModifiedFilesCount = len(cs.ModifiedFiles)
		_ = s.storage.UpdateSliceMetadata(ctx, cs.SliceID, metadata)
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

	// TODO: Implement commit history
	return &slicev1.CommitHistoryResponse{
		Commits: []*slicev1.CommitInfo{},
	}, nil
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
	if req.StatusFilter != slicev1.ChangesetStatus(0) {
		converted := convertProtoStatusToModel(req.StatusFilter)
		statusFilter = &converted
	}

	changesets, err := s.storage.ListChangesets(ctx, req.SliceId, statusFilter, int(req.Limit))
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changesets: %v", err))
	}

	infos := make([]*slicev1.ChangesetInfo, 0, len(changesets))
	for _, cs := range changesets {
		infos = append(infos, convertChangesetToProto(cs))
	}

	return &slicev1.ListChangesetsResponse{
		Changesets: infos,
	}, nil
}

func (s *sliceServiceServer) StreamCheckoutSlice(req *slicev1.CheckoutRequest, stream slicev1.SliceService_StreamCheckoutSliceServer) error {
	log.Printf("StreamCheckoutSlice called: slice_id=%s", req.SliceId)

	// TODO: Implement streaming checkout
	return nil
}

func (s *sliceServiceServer) StreamCreateChangeset(stream slicev1.SliceService_StreamCreateChangesetServer) error {
	log.Printf("StreamCreateChangeset called")

	// TODO: Implement streaming changeset creation
	return nil
}

func convertChangesetToProto(cs *models.Changeset) *slicev1.ChangesetInfo {
	info := &slicev1.ChangesetInfo{
		ChangesetId:    cs.ID,
		ChangesetHash:  cs.Hash,
		SliceId:        cs.SliceID,
		BaseCommitHash: cs.BaseCommitHash,
		ModifiedFiles:  cs.ModifiedFiles,
		Status:         convertModelStatusToProto(cs.Status),
		Author:         cs.Author,
		CreatedAt:      cs.CreatedAt.Unix(),
		Message:        cs.Message,
		MergedAt:       0,
	}

	if cs.MergedAt != nil {
		info.MergedAt = cs.MergedAt.Unix()
	}

	return info
}

func convertModelStatusToProto(status models.ChangesetStatus) slicev1.ChangesetStatus {
	switch status {
	case models.ChangesetStatusApproved:
		return slicev1.ChangesetStatus_APPROVED
	case models.ChangesetStatusRejected:
		return slicev1.ChangesetStatus_REJECTED
	case models.ChangesetStatusMerged:
		return slicev1.ChangesetStatus_MERGED
	default:
		return slicev1.ChangesetStatus_PENDING
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

func main() {
	// Initialize storage
	st := storage.NewInMemoryStorage()

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	slicev1.RegisterSliceServiceServer(s, newSliceServiceServer(st))

	log.Println("SliceService server listening on :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
