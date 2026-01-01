package server

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SliceServiceServer struct {
	slicev1.UnimplementedSliceServiceServer
	storage storage.Storage
}

func NewSliceServiceServer(st storage.Storage) *SliceServiceServer {
	return &SliceServiceServer{storage: st}
}

func (s *SliceServiceServer) CheckoutSlice(ctx context.Context, req *slicev1.CheckoutRequest) (*slicev1.CheckoutResponse, error) {
	log.Printf("CheckoutSlice called: slice_id=%s, commit_hash=%s", req.SliceId, req.CommitHash)

	metadata, err := s.storage.GetSliceMetadata(ctx, req.SliceId)
	if err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	manifest := &slicev1.SliceManifest{CommitHash: metadata.HeadCommitHash, FileMetadata: []*slicev1.FileMetadata{}}

	return &slicev1.CheckoutResponse{Manifest: manifest, Files: []*slicev1.FileContent{}}, nil
}

func (s *SliceServiceServer) CreateChangeset(ctx context.Context, req *slicev1.CreateChangesetRequest) (*slicev1.CreateChangesetResponse, error) {
	log.Printf("CreateChangeset called: slice_id=%s, author=%s", req.SliceId, req.Author)

	if _, err := s.storage.GetSlice(ctx, req.SliceId); err != nil {
		return nil, status.Error(codes.NotFound, fmt.Sprintf("slice not found: %s", req.SliceId))
	}

	return &slicev1.CreateChangesetResponse{
		ChangesetId:   fmt.Sprintf("cs-%d", time.Now().UnixNano()),
		ChangesetHash: "stub-hash",
		Status:        slicev1.ChangesetStatus_PENDING,
	}, nil
}

func (s *SliceServiceServer) ReviewChangeset(ctx context.Context, req *slicev1.ReviewChangesetRequest) (*slicev1.ReviewChangesetResponse, error) {
	log.Printf("ReviewChangeset called: changeset_id=%s", req.ChangesetId)
	return &slicev1.ReviewChangesetResponse{ReviewStatus: slicev1.ReviewStatus_READY_FOR_MERGE, Warnings: []string{}}, nil
}

func (s *SliceServiceServer) MergeChangeset(ctx context.Context, req *slicev1.MergeChangesetRequest) (*slicev1.MergeChangesetResponse, error) {
	log.Printf("MergeChangeset called: changeset_id=%s", req.ChangesetId)
	return &slicev1.MergeChangesetResponse{
		Status:        slicev1.MergeStatus_MERGE_STATUS_SUCCESS,
		NewCommitHash: fmt.Sprintf("commit-%d", time.Now().UnixNano()),
		Conflicts:     []*slicev1.Conflict{},
	}, nil
}

func (s *SliceServiceServer) RebaseChangeset(ctx context.Context, req *slicev1.RebaseChangesetRequest) (*slicev1.RebaseChangesetResponse, error) {
	log.Printf("RebaseChangeset called: changeset_id=%s", req.ChangesetId)
	return &slicev1.RebaseChangesetResponse{
		Status:              slicev1.RebaseStatus_REBASE_STATUS_SUCCESS,
		NewBaseCommitHash:   "base-hash",
		SliceCommitsToApply: []string{},
		Conflicts:           []*slicev1.Conflict{},
	}, nil
}

func (s *SliceServiceServer) GetSliceCommits(ctx context.Context, req *slicev1.CommitHistoryRequest) (*slicev1.CommitHistoryResponse, error) {
	log.Printf("GetSliceCommits called: slice_id=%s", req.SliceId)
	return &slicev1.CommitHistoryResponse{Commits: []*slicev1.CommitInfo{}}, nil
}

func (s *SliceServiceServer) GetSliceState(ctx context.Context, req *slicev1.StateRequest) (*slicev1.StateResponse, error) {
	log.Printf("GetSliceState called: slice_id=%s", req.SliceId)

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

func (s *SliceServiceServer) ListChangesets(ctx context.Context, req *slicev1.ListChangesetsRequest) (*slicev1.ListChangesetsResponse, error) {
	log.Printf("ListChangesets called: slice_id=%s", req.SliceId)
	return &slicev1.ListChangesetsResponse{Changesets: []*slicev1.ChangesetInfo{}}, nil
}

func (s *SliceServiceServer) StreamCheckoutSlice(req *slicev1.CheckoutRequest, stream slicev1.SliceService_StreamCheckoutSliceServer) error {
	log.Printf("StreamCheckoutSlice called: slice_id=%s", req.SliceId)
	return nil
}

func (s *SliceServiceServer) StreamCreateChangeset(stream slicev1.SliceService_StreamCreateChangesetServer) error {
	log.Printf("StreamCreateChangeset called")
	return nil
}
