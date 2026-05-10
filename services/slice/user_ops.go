package sliceservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultUserGitImportMaxCommits = 200
	maxUserGitImportCommits        = 500
)

func sliceInfoToProto(slice *models.Slice) *slicev1.SliceInfo {
	if slice == nil {
		return nil
	}
	slug := storage.QualifiedSliceSlug(slice)
	if homeSlug, ok := homeslice.ExternalSlugForSlice(slice); ok {
		slug = homeSlug
	}
	return &slicev1.SliceInfo{
		SliceId:     slice.ID,
		Name:        slice.Name,
		Slug:        slug,
		Description: slice.Description,
		Owners:      slice.Owners,
		CreatedAt:   slice.CreatedAt.Unix(),
		UpdatedAt:   slice.UpdatedAt.Unix(),
		FileCount:   int32(len(slice.Files)),
		IsRoot:      slice.IsRoot,
		Environment: slice.Environment,
	}
}

func (s *sliceServiceServer) ListSlices(ctx context.Context, req *slicev1.ListSlicesRequest) (*slicev1.ListSlicesResponse, error) {
	log.Printf("ListSlices called: limit=%v offset=%v", req.GetLimit(), req.GetOffset())

	limit := int(req.GetLimit())
	offset := int(req.GetOffset())
	if limit <= 0 {
		limit = int(^uint(0) >> 1)
	}
	if offset < 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid offset")
	}

	username, err := s.optionalUsername(ctx)
	if err != nil {
		return nil, err
	}

	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}

	owned, err := s.storage.ListSlicesByOwner(ctx, username, int(^uint(0)>>1), 0)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slices: %v", err))
	}
	slices := make([]*models.Slice, 0, len(owned))
	for _, slice := range owned {
		if slice.IsRoot {
			continue
		}
		slices = append(slices, slice)
	}

	total := len(slices)
	if offset >= len(slices) {
		slices = []*models.Slice{}
	} else {
		end := offset + limit
		if end > len(slices) {
			end = len(slices)
		}
		slices = slices[offset:end]
	}

	response := &slicev1.ListSlicesResponse{
		Slices: make([]*slicev1.SliceInfo, 0, len(slices)),
		Total:  int32(total),
	}
	for _, slice := range slices {
		response.Slices = append(response.Slices, sliceInfoToProto(slice))
	}
	return response, nil
}

func (s *sliceServiceServer) GetGlobalState(ctx context.Context, req *slicev1.GlobalStateRequest) (*slicev1.GlobalStateResponse, error) {
	state, err := s.storage.GetGlobalState(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load global state: %v", err))
	}
	response := &slicev1.GlobalStateResponse{
		GlobalCommitHash: state.GlobalCommitHash,
		Timestamp:        state.Timestamp.Unix(),
		History:          []*slicev1.GlobalCommitHistory{},
	}
	if req.GetIncludeHistory() {
		for _, commit := range state.History {
			response.History = append(response.History, &slicev1.GlobalCommitHistory{
				CommitHash:     commit.CommitHash,
				Timestamp:      commit.Timestamp.Unix(),
				MergedSliceIds: commit.MergedSliceIDs,
			})
		}
	}
	return response, nil
}

func (s *sliceServiceServer) BatchMerge(ctx context.Context, req *slicev1.BatchMergeRequest) (*slicev1.BatchMergeResponse, error) {
	log.Printf("BatchMerge called: max_slices=%v", req.GetMaxSlices())
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}

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

	allSlices, err := s.storage.ListSlicesByOwner(ctx, username, int(^uint(0)>>1), 0)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list slices: %v", err))
	}

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

		metadata.HeadCommitHash = common.GenerateCommitID()
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
	globalCommitHash := common.GenerateCommitID()
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
	state.GlobalCommitHash = globalCommitHash
	state.Timestamp = commitTime
	state.History = append([]*models.GlobalCommit{{
		CommitHash:     globalCommitHash,
		Timestamp:      commitTime,
		MergedSliceIDs: mergedSliceIDs,
	}}, state.History...)
	if err := s.storage.UpdateGlobalState(ctx, state); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to update global state: %v", err))
	}

	return &slicev1.BatchMergeResponse{
		GlobalCommitHash: globalCommitHash,
		MergedSliceCount: int32(len(mergeCandidates)),
		MergedSliceIds:   mergedSliceIDs,
		Timestamp:        commitTime.Unix(),
	}, nil
}

func (s *sliceServiceServer) scopedDivergentConflicts(ctx context.Context, username, requestedSliceID string) ([]*models.FileConflict, error) {
	requestedSliceID = strings.TrimSpace(requestedSliceID)
	if requestedSliceID != "" {
		slice, err := s.storage.GetSlice(ctx, requestedSliceID)
		if err != nil {
			return nil, status.Error(codes.NotFound, "slice not found")
		}
		if !authz.HasSliceViewAccess(slice, username) {
			return nil, status.Error(codes.PermissionDenied, "forbidden")
		}
	}
	conflicts, err := storage.ListDivergentConflicts(ctx, s.storage)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list conflicts: %v", err))
	}
	out := make([]*models.FileConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		if conflict == nil {
			continue
		}
		if requestedSliceID != "" && !stringSliceContains(conflict.ConflictingSlices, requestedSliceID) {
			continue
		}
		allVisible := true
		for _, sliceID := range conflict.ConflictingSlices {
			slice, err := s.storage.GetSlice(ctx, sliceID)
			if err != nil || !authz.HasSliceViewAccess(slice, username) {
				allVisible = false
				break
			}
		}
		if allVisible {
			out = append(out, conflict)
		}
	}
	return out, nil
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func conflictToProto(conflict *models.FileConflict) *slicev1.Conflict {
	if conflict == nil {
		return nil
	}
	return &slicev1.Conflict{
		FileId:              conflict.FileID,
		ConflictingSliceIds: append([]string(nil), conflict.ConflictingSlices...),
		Type:                slicev1.ConflictType_CONFLICT_TYPE_CONTENT,
	}
}

func (s *sliceServiceServer) GetConflicts(ctx context.Context, req *slicev1.ConflictsRequest) (*slicev1.ConflictsResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	conflicts, err := s.scopedDivergentConflicts(ctx, username, req.GetSliceId())
	if err != nil {
		return nil, err
	}
	protoConflicts := make([]*slicev1.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		protoConflicts = append(protoConflicts, conflictToProto(conflict))
	}
	return &slicev1.ConflictsResponse{
		Conflicts:      protoConflicts,
		TotalConflicts: int32(len(protoConflicts)),
	}, nil
}

func (s *sliceServiceServer) ResolveConflict(ctx context.Context, req *slicev1.ResolveConflictRequest) (*slicev1.ResolveConflictResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	fileID := strings.TrimSpace(req.GetFileId())
	preferredSliceID := strings.TrimSpace(req.GetPreferredSliceId())
	if fileID == "" || preferredSliceID == "" {
		return nil, status.Error(codes.InvalidArgument, "file_id and preferred_slice_id are required")
	}
	conflicts, err := s.scopedDivergentConflicts(ctx, username, "")
	if err != nil {
		return nil, err
	}
	var target *models.FileConflict
	for _, conflict := range conflicts {
		if conflict != nil && conflict.FileID == fileID {
			target = conflict
			break
		}
	}
	if target == nil {
		return nil, status.Error(codes.NotFound, "conflict not found")
	}
	if !stringSliceContains(target.ConflictingSlices, preferredSliceID) {
		return nil, status.Error(codes.InvalidArgument, "preferred_slice_id must reference one of the conflicting slices")
	}
	for _, sliceID := range target.ConflictingSlices {
		slice, err := s.storage.GetSlice(ctx, sliceID)
		if err != nil {
			return nil, status.Error(codes.NotFound, "conflicting slice not found")
		}
		if !canManageSliceVisibility(slice, username) {
			return nil, status.Error(codes.PermissionDenied, "requires owner access to all conflicting slices")
		}
	}
	resolved, err := storage.NormalizeConflictToPreferred(ctx, s.storage, fileID, preferredSliceID)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidInput) {
			return nil, status.Error(codes.InvalidArgument, "preferred_slice_id must reference one of the conflicting slices")
		}
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "preferred slice is missing materialized file content")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve conflict: %v", err))
	}
	return &slicev1.ResolveConflictResponse{ResolvedConflict: conflictToProto(resolved)}, nil
}

func (s *sliceServiceServer) WatchConflicts(req *slicev1.WatchConflictsRequest, stream slicev1.SliceService_WatchConflictsServer) error {
	username, err := s.requireUsername(stream.Context())
	if err != nil {
		return err
	}
	conflicts, err := s.scopedDivergentConflicts(stream.Context(), username, req.GetSliceId())
	if err != nil {
		return err
	}
	protoConflicts := make([]*slicev1.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		protoConflicts = append(protoConflicts, conflictToProto(conflict))
	}
	if err := stream.Send(&slicev1.ConflictUpdate{NewConflicts: protoConflicts}); err != nil {
		return status.Error(codes.Unavailable, fmt.Sprintf("failed to stream conflicts: %v", err))
	}
	return nil
}

func validateUserImportURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return status.Error(codes.InvalidArgument, "repo_url must be a valid URL")
	}
	if parsed.Scheme != "https" {
		return status.Error(codes.InvalidArgument, "repo_url must use https")
	}
	return nil
}

func (s *sliceServiceServer) ImportGitRepo(ctx context.Context, req *slicev1.ImportGitRepoRequest) (*slicev1.ImportGitRepoResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	repoURL := strings.TrimSpace(req.GetRepoUrl())
	if err := validateUserImportURL(repoURL); err != nil {
		return nil, err
	}
	maxCommits := int(req.GetMaxCommits())
	if maxCommits <= 0 {
		maxCommits = defaultUserGitImportMaxCommits
	}
	if maxCommits > maxUserGitImportCommits {
		return nil, status.Errorf(codes.InvalidArgument, "max_commits must be <= %d", maxUserGitImportCommits)
	}

	homeSlice, err := homeslice.EnsureUserHomeSlice(ctx, s.storage, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to ensure home slice")
	}

	visibleMountPath := strings.TrimSpace(req.GetMountPath())
	if visibleMountPath == "" {
		visibleMountPath = homeslice.VisibleRootPath(username) + "/" + repoNameFromURL(repoURL)
	}
	storedMountPath, normalizedVisibleMountPath, err := homeslice.ResolveVisiblePath(username, visibleMountPath, false)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid mount_path: %v", err)
	}
	if storedMountPath == homeslice.RelativeRootPath(username) {
		return nil, status.Error(codes.InvalidArgument, "mount_path must be below the home directory root")
	}

	res, err := importGitRepo(ctx, s.storage, "", repoURL, strings.TrimSpace(req.GetRef()), homeSlice.ID, storedMountPath, false, req.GetFirstParent(), maxCommits, false)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("git import failed: %v", err))
	}
	return &slicev1.ImportGitRepoResponse{
		ImportedCommits: int32(res.ImportedCommits),
		HeadCommitHash:  res.HeadCommitHash,
		Warnings:        res.Warnings,
		SliceId:         homeSlice.ID,
		MountPath:       normalizedVisibleMountPath,
	}, nil
}
