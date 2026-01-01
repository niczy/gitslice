package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type adminServiceServer struct {
	adminv1.UnimplementedAdminServiceServer
	storage storage.Storage
}

func newAdminServiceServer(st storage.Storage) *adminServiceServer {
	return &adminServiceServer{
		storage: st,
	}
}

func (s *adminServiceServer) BatchMerge(ctx context.Context, req *adminv1.BatchMergeRequest) (*adminv1.BatchMergeResponse, error) {
	log.Printf("BatchMerge called: max_slices=%v", req.MaxSlices)

	// TODO: Implement batch merge logic
	return &adminv1.BatchMergeResponse{
		GlobalCommitHash: fmt.Sprintf("global-%d", time.Now().UnixNano()),
		MergedSliceCount: 0,
		MergedSliceIds:   []string{},
		Timestamp:        time.Now().Unix(),
	}, nil
}

func (s *adminServiceServer) CreateSlice(ctx context.Context, req *adminv1.CreateSliceRequest) (*adminv1.CreateSliceResponse, error) {
	log.Printf("CreateSlice called: slice_id=%s, name=%s", req.SliceId, req.Name)

	// Validate input
	if req.SliceId == "" {
		return nil, status.Error(codes.InvalidArgument, "slice_id is required")
	}

	// Create slice model
	slice := &models.Slice{
		ID:          req.SliceId,
		Name:        req.Name,
		Description: req.Description,
		Files:       req.Files,
		Owners:      req.Owners,
		CreatedBy:   req.CreatedBy,
	}

	// Store slice
	if err := s.storage.CreateSlice(ctx, slice); err != nil {
		return nil, status.Error(codes.AlreadyExists, fmt.Sprintf("slice already exists: %s", req.SliceId))
	}

	return &adminv1.CreateSliceResponse{
		SliceId: req.SliceId,
		Status:  "created",
	}, nil
}

func (s *adminServiceServer) ListSlices(ctx context.Context, req *adminv1.ListSlicesRequest) (*adminv1.ListSlicesResponse, error) {
	log.Printf("ListSlices called: limit=%d, offset=%d", req.Limit, req.Offset)

	// List slices from storage
	slices, err := s.storage.ListSlices(ctx, int(req.Limit), int(req.Offset))
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slices: %v", err))
	}

	// Convert to protobuf format
	sliceInfos := make([]*adminv1.SliceInfo, 0, len(slices))
	for _, slice := range slices {
		metadata, _ := s.storage.GetSliceMetadata(ctx, slice.ID)
		sliceInfos = append(sliceInfos, &adminv1.SliceInfo{
			SliceId:            slice.ID,
			LatestCommitHash:   metadata.HeadCommitHash,
			ModifiedFilesCount: int32(metadata.ModifiedFilesCount),
			LastModified:       metadata.LastModified.Unix(),
		})
	}

	return &adminv1.ListSlicesResponse{
		Slices: sliceInfos,
	}, nil
}

func (s *adminServiceServer) GetConflicts(ctx context.Context, req *adminv1.ConflictsRequest) (*adminv1.ConflictsResponse, error) {
	log.Printf("GetConflicts called: slice_id=%v", req.SliceId)

	// TODO: Implement conflict detection
	return &adminv1.ConflictsResponse{
		Conflicts:      []*adminv1.Conflict{},
		TotalConflicts: 0,
	}, nil
}

func (s *adminServiceServer) GetGlobalState(ctx context.Context, req *adminv1.GlobalStateRequest) (*adminv1.GlobalStateResponse, error) {
	log.Printf("GetGlobalState called: include_history=%v", req.IncludeHistory)

	// TODO: Implement global state
	history := []*adminv1.GlobalCommitHistory{}
	if req.IncludeHistory {
		history = append(history, &adminv1.GlobalCommitHistory{
			CommitHash:     "global-init",
			Timestamp:      time.Now().Unix(),
			MergedSliceIds: []string{},
		})
	}

	return &adminv1.GlobalStateResponse{
		GlobalCommitHash: "global-init",
		Timestamp:        time.Now().Unix(),
		History:          history,
	}, nil
}

func (s *adminServiceServer) WatchConflicts(req *adminv1.WatchConflictsRequest, stream adminv1.AdminService_WatchConflictsServer) error {
	log.Printf("WatchConflicts called: slice_id=%v", req.SliceId)

	// TODO: Implement conflict streaming
	return nil
}

func main() {
	// Initialize storage
	st := storage.NewInMemoryStorage()

	lis, err := net.Listen("tcp", ":50052")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	adminv1.RegisterAdminServiceServer(s, newAdminServiceServer(st))

	log.Println("AdminService server listening on :50052")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
