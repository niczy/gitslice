package main

import (
	"context"
	"log"
	"net"

	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
)

type sliceServiceServer struct {
	slicev1.UnimplementedSliceServiceServer
}

func (s *sliceServiceServer) CheckoutSlice(ctx context.Context, req *slicev1.CheckoutRequest) (*slicev1.CheckoutResponse, error) {
	log.Printf("CheckoutSlice called: slice_id=%s, commit_hash=%s", req.SliceId, req.CommitHash)
	return &slicev1.CheckoutResponse{}, nil
}

func (s *sliceServiceServer) CreateChangeset(ctx context.Context, req *slicev1.CreateChangesetRequest) (*slicev1.CreateChangesetResponse, error) {
	log.Printf("CreateChangeset called: slice_id=%s, author=%s", req.SliceId, req.Author)
	return &slicev1.CreateChangesetResponse{}, nil
}

func (s *sliceServiceServer) ReviewChangeset(ctx context.Context, req *slicev1.ReviewChangesetRequest) (*slicev1.ReviewChangesetResponse, error) {
	log.Printf("ReviewChangeset called: changeset_id=%s", req.ChangesetId)
	return &slicev1.ReviewChangesetResponse{}, nil
}

func (s *sliceServiceServer) MergeChangeset(ctx context.Context, req *slicev1.MergeChangesetRequest) (*slicev1.MergeChangesetResponse, error) {
	log.Printf("MergeChangeset called: changeset_id=%s", req.ChangesetId)
	return &slicev1.MergeChangesetResponse{}, nil
}

func (s *sliceServiceServer) RebaseChangeset(ctx context.Context, req *slicev1.RebaseChangesetRequest) (*slicev1.RebaseChangesetResponse, error) {
	log.Printf("RebaseChangeset called: changeset_id=%s", req.ChangesetId)
	return &slicev1.RebaseChangesetResponse{}, nil
}

func (s *sliceServiceServer) GetSliceCommits(ctx context.Context, req *slicev1.CommitHistoryRequest) (*slicev1.CommitHistoryResponse, error) {
	log.Printf("GetSliceCommits called: slice_id=%s", req.SliceId)
	return &slicev1.CommitHistoryResponse{}, nil
}

func (s *sliceServiceServer) GetSliceState(ctx context.Context, req *slicev1.StateRequest) (*slicev1.StateResponse, error) {
	log.Printf("GetSliceState called: slice_id=%s", req.SliceId)
	return &slicev1.StateResponse{}, nil
}

func (s *sliceServiceServer) ListChangesets(ctx context.Context, req *slicev1.ListChangesetsRequest) (*slicev1.ListChangesetsResponse, error) {
	log.Printf("ListChangesets called: slice_id=%s", req.SliceId)
	return &slicev1.ListChangesetsResponse{}, nil
}

func (s *sliceServiceServer) StreamCheckoutSlice(req *slicev1.CheckoutRequest, stream slicev1.SliceService_StreamCheckoutSliceServer) error {
	log.Printf("StreamCheckoutSlice called: slice_id=%s", req.SliceId)
	return nil
}

func (s *sliceServiceServer) StreamCreateChangeset(stream slicev1.SliceService_StreamCreateChangesetServer) error {
	log.Printf("StreamCreateChangeset called")
	return nil
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	slicev1.RegisterSliceServiceServer(s, &sliceServiceServer{})

	log.Println("SliceService server listening on :50051")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
