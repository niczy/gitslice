package adminservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
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

// NewGRPCServer constructs a gRPC server for the admin service using the provided storage backend.
func NewGRPCServer(st storage.Storage) *grpc.Server {
	srv := grpc.NewServer()
	adminv1.RegisterAdminServiceServer(srv, newAdminServiceServer(st))
	return srv
}

func (s *adminServiceServer) BatchMerge(ctx context.Context, req *adminv1.BatchMergeRequest) (*adminv1.BatchMergeResponse, error) {
	log.Printf("BatchMerge called: max_slices=%v", req.MaxSlices)

	rootSlice, err := s.storage.GetRootSlice(ctx)
	if errors.Is(err, storage.ErrSliceNotFound) {
		if initErr := s.storage.InitializeRootSlice(ctx); initErr != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to initialize root slice: %v", initErr))
		}
		rootSlice, err = s.storage.GetRootSlice(ctx)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load root slice: %v", err))
	}

	conflicts, err := s.storage.ListConflicts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}
	if len(conflicts) > 0 {
		return nil, status.Error(codes.FailedPrecondition, "conflicts present; resolve before merging")
	}

	allSlices, err := s.storage.ListSlices(ctx, int(^uint(0)>>1), 0)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slices: %v", err))
	}

	// Filter out the root slice and apply the max_slices limit if provided.
	mergeCandidates := make([]*models.Slice, 0, len(allSlices))
	for _, slice := range allSlices {
		if slice.IsRoot {
			continue
		}
		mergeCandidates = append(mergeCandidates, slice)
	}

	maxSlices := req.GetMaxSlices()
	if maxSlices > 0 && int(maxSlices) < len(mergeCandidates) {
		mergeCandidates = mergeCandidates[:maxSlices]
	}

	rootMetadata, err := s.storage.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load root metadata: %v", err))
	}

	mergedFiles := make(map[string]bool)
	for _, file := range rootMetadata.ModifiedFiles {
		mergedFiles[file] = true
	}
	for _, file := range rootSlice.Files {
		mergedFiles[file] = true
	}

	mergedSliceIDs := make([]string, 0, len(mergeCandidates))
	for _, slice := range mergeCandidates {
		mergedSliceIDs = append(mergedSliceIDs, slice.ID)

		metadata, err := s.storage.GetSliceMetadata(ctx, slice.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load slice metadata: %v", err))
		}

		filesToMerge := make(map[string]bool)
		for _, fileID := range slice.Files {
			filesToMerge[fileID] = true
		}
		for _, fileID := range metadata.ModifiedFiles {
			filesToMerge[fileID] = true
		}

		for fileID := range filesToMerge {
			if err := s.storage.AddFileToSlice(ctx, fileID, rootSlice.ID); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to add file to root slice: %v", err))
			}
			if err := s.storage.RemoveFileFromSlice(ctx, fileID, slice.ID); err != nil {
				return nil, status.Error(codes.Internal, fmt.Sprintf("failed to remove file from slice: %v", err))
			}

			mergedFiles[fileID] = true
		}

		metadata.HeadCommitHash = fmt.Sprintf("merged-%s-%d", slice.ID, time.Now().UnixNano())
		metadata.ModifiedFiles = []string{}
		metadata.ModifiedFilesCount = 0

		if err := s.storage.UpdateSliceMetadata(ctx, slice.ID, metadata); err != nil {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update slice metadata: %v", err))
		}
	}

	mergedFileList := make([]string, 0, len(mergedFiles))
	for file := range mergedFiles {
		mergedFileList = append(mergedFileList, file)
	}
	sort.Strings(mergedFileList)

	commitTime := time.Now()
	globalCommitHash := fmt.Sprintf("global-%d", commitTime.UnixNano())
	rootMetadata.HeadCommitHash = globalCommitHash
	rootMetadata.ModifiedFiles = mergedFileList
	rootMetadata.ModifiedFilesCount = len(mergedFileList)

	if err := s.storage.UpdateSliceMetadata(ctx, rootSlice.ID, rootMetadata); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update root metadata: %v", err))
	}

	state, err := s.storage.GetGlobalState(ctx)
	if err != nil {
		if !errors.Is(err, storage.ErrInvalidInput) {
			return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load global state: %v", err))
		}
		state = &models.GlobalState{}
	}

	newHistory := &models.GlobalCommit{
		CommitHash:     globalCommitHash,
		Timestamp:      commitTime,
		MergedSliceIDs: mergedSliceIDs,
	}

	state.GlobalCommitHash = globalCommitHash
	state.Timestamp = commitTime
	state.History = append([]*models.GlobalCommit{newHistory}, state.History...)

	if err := s.storage.UpdateGlobalState(ctx, state); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update global state: %v", err))
	}

	return &adminv1.BatchMergeResponse{
		GlobalCommitHash: globalCommitHash,
		MergedSliceCount: int32(len(mergeCandidates)),
		MergedSliceIds:   mergedSliceIDs,
		Timestamp:        commitTime.Unix(),
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

	conflicts, err := s.storage.ListConflicts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}

	var protoConflicts []*adminv1.Conflict
	for _, conflict := range conflicts {
		if req.SliceId != "" {
			contains := false
			for _, id := range conflict.ConflictingSlices {
				if id == req.GetSliceId() {
					contains = true
					break
				}
			}
			if !contains {
				continue
			}
		}

		protoConflicts = append(protoConflicts, &adminv1.Conflict{
			FileId:              conflict.FileID,
			ConflictingSliceIds: conflict.ConflictingSlices,
		})
	}

	return &adminv1.ConflictsResponse{
		Conflicts:      protoConflicts,
		TotalConflicts: int32(len(protoConflicts)),
	}, nil
}

func (s *adminServiceServer) ResolveConflict(ctx context.Context, req *adminv1.ResolveConflictRequest) (*adminv1.ResolveConflictResponse, error) {
	log.Printf("ResolveConflict called: file_id=%s preferred_slice_id=%s", req.FileId, req.PreferredSliceId)

	if req.FileId == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id is required")
	}

	conflict, err := s.storage.ResolveConflict(ctx, req.FileId, req.PreferredSliceId)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve conflict: %v", err))
	}

	return &adminv1.ResolveConflictResponse{
		ResolvedConflict: &adminv1.Conflict{
			FileId:              conflict.FileID,
			ConflictingSliceIds: conflict.ConflictingSlices,
		},
	}, nil
}

func (s *adminServiceServer) GetGlobalState(ctx context.Context, req *adminv1.GlobalStateRequest) (*adminv1.GlobalStateResponse, error) {
	log.Printf("GetGlobalState called: include_history=%v", req.IncludeHistory)

	state, err := s.storage.GetGlobalState(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load global state: %v", err))
	}

	response := &adminv1.GlobalStateResponse{
		GlobalCommitHash: state.GlobalCommitHash,
		Timestamp:        state.Timestamp.Unix(),
		History:          []*adminv1.GlobalCommitHistory{},
	}

	if req.IncludeHistory {
		for _, commit := range state.History {
			response.History = append(response.History, &adminv1.GlobalCommitHistory{
				CommitHash:     commit.CommitHash,
				Timestamp:      commit.Timestamp.Unix(),
				MergedSliceIds: commit.MergedSliceIDs,
			})
		}
	}

	return response, nil
}

func (s *adminServiceServer) WatchConflicts(req *adminv1.WatchConflictsRequest, stream adminv1.AdminService_WatchConflictsServer) error {
	log.Printf("WatchConflicts called: slice_id=%v", req.SliceId)

	conflicts, err := s.storage.ListConflicts(stream.Context())
	if err != nil {
		return status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}

	var protoConflicts []*adminv1.Conflict
	for _, conflict := range conflicts {
		if req.SliceId != "" {
			matches := false
			for _, id := range conflict.ConflictingSlices {
				if id == req.GetSliceId() {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
		}

		protoConflicts = append(protoConflicts, &adminv1.Conflict{
			FileId:              conflict.FileID,
			ConflictingSliceIds: conflict.ConflictingSlices,
		})
	}

	if err := stream.Send(&adminv1.ConflictUpdate{NewConflicts: protoConflicts}); err != nil {
		return status.Error(codes.Unavailable, fmt.Sprintf("failed to stream conflicts: %v", err))
	}

	return nil
}
