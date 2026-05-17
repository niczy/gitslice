package sliceservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *sliceServiceServer) ListChangesetConflicts(ctx context.Context, req *slicev1.ListChangesetConflictsRequest) (*slicev1.ListChangesetConflictsResponse, error) {
	username, err := s.requireUsername(ctx)
	if err != nil {
		return nil, err
	}
	changesetID := strings.TrimSpace(req.GetChangesetId())
	if changesetID == "" {
		return nil, status.Error(codes.InvalidArgument, "changeset_id is required")
	}
	cs, err := s.storage.GetChangeset(ctx, changesetID)
	if err != nil {
		if errors.Is(err, storage.ErrChangesetNotFound) {
			return nil, status.Error(codes.NotFound, "changeset not found")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to load changeset: %v", err))
	}
	slice, err := s.storage.GetSlice(ctx, cs.SliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "slice not found")
	}
	if !s.hasSliceViewAccess(ctx, slice, username) {
		return nil, status.Error(codes.PermissionDenied, "not authorized for slice")
	}

	store, ok := s.storage.(storage.ChangesetConflictStore)
	if !ok {
		return &slicev1.ListChangesetConflictsResponse{ChangesetId: changesetID}, nil
	}
	conflicts, err := store.ListChangesetConflicts(ctx, changesetID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to list changeset conflicts: %v", err))
	}
	return &slicev1.ListChangesetConflictsResponse{
		ChangesetId:    changesetID,
		Conflicts:      changesetConflictsToProto(conflicts),
		TotalConflicts: int32(len(conflicts)),
	}, nil
}

func (s *sliceServiceServer) changesetConflictArtifacts(ctx context.Context, cs *models.Changeset, drifts []changesetPathHeadDrift) []*models.ChangesetConflict {
	if cs == nil || len(drifts) == 0 {
		return nil
	}
	conflicts := make([]*models.ChangesetConflict, 0, len(drifts))
	for _, drift := range drifts {
		if strings.TrimSpace(drift.Path) == "" {
			continue
		}
		message := fmt.Sprintf(
			"path %s changed from version %d to %d. Sync the changeset before merging.",
			drift.Path,
			drift.BaseVersion,
			drift.CurrentVersion,
		)
		conflicts = append(conflicts, &models.ChangesetConflict{
			ID:             common.GenerateChangesetConflictID(cs.ID, drift.Path),
			ChangesetID:    cs.ID,
			SliceID:        cs.SliceID,
			Path:           drift.Path,
			Type:           models.ChangesetConflictTypeStaleBase,
			Message:        message,
			BaseVersion:    drift.BaseVersion,
			CurrentVersion: drift.CurrentVersion,
			BaseHash:       drift.BaseHash,
			OursHash:       drift.OursHash,
			TheirsHash:     drift.CurrentHash,
			Patch:          s.buildConflictPatch(ctx, drift.Path, drift.CurrentHash, drift.OursHash),
		})
	}
	return conflicts
}

func (s *sliceServiceServer) staleBaseMergeResponseFromDrifts(ctx context.Context, cs *models.Changeset, drifts []changesetPathHeadDrift) (*slicev1.MergeChangesetResponse, error) {
	if cs == nil {
		return nil, status.Error(codes.Internal, "missing changeset for stale-base response")
	}
	conflicts := s.changesetConflictArtifacts(ctx, cs, drifts)
	if err := s.replaceChangesetConflictArtifacts(ctx, cs.ID, conflicts); err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to store changeset conflicts: %v", err))
	}
	return &slicev1.MergeChangesetResponse{
		Status:      slicev1.MergeStatus_MERGE_STATUS_STALE_BASE,
		ChangesetId: cs.ID,
		Message:     pathHeadDriftMergeMessage(drifts),
		Conflicts:   changesetConflictsToProto(conflicts),
	}, nil
}

func (s *sliceServiceServer) replaceChangesetConflictArtifacts(ctx context.Context, changesetID string, conflicts []*models.ChangesetConflict) error {
	store, ok := s.storage.(storage.ChangesetConflictStore)
	if !ok {
		return nil
	}
	return store.ReplaceChangesetConflicts(ctx, changesetID, conflicts)
}

func (s *sliceServiceServer) buildConflictPatch(ctx context.Context, filePath, theirsHash, oursHash string) string {
	beforeLines, beforeOK := s.loadDiffLinesFromHash(ctx, theirsHash)
	if !beforeOK {
		return ""
	}
	afterLines, afterOK := s.loadDiffLinesFromHash(ctx, oursHash)
	if !afterOK {
		return ""
	}
	return buildUnifiedPatchFromLines(filePath, filePath, beforeLines, afterLines)
}

func changesetConflictsToProto(conflicts []*models.ChangesetConflict) []*slicev1.Conflict {
	out := make([]*slicev1.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		if proto := changesetConflictToProto(conflict); proto != nil {
			out = append(out, proto)
		}
	}
	return out
}

func changesetConflictToProto(conflict *models.ChangesetConflict) *slicev1.Conflict {
	if conflict == nil {
		return nil
	}
	return &slicev1.Conflict{
		FileId:         conflict.Path,
		Type:           changesetConflictTypeToProto(conflict.Type),
		Message:        conflict.Message,
		ConflictId:     conflict.ID,
		ChangesetId:    conflict.ChangesetID,
		Path:           conflict.Path,
		BaseVersion:    conflict.BaseVersion,
		CurrentVersion: conflict.CurrentVersion,
		BaseHash:       conflict.BaseHash,
		OursHash:       conflict.OursHash,
		TheirsHash:     conflict.TheirsHash,
		Patch:          conflict.Patch,
		Resolved:       conflict.Resolved,
	}
}

func changesetConflictTypeToProto(conflictType string) slicev1.ConflictType {
	switch strings.TrimSpace(conflictType) {
	case models.ChangesetConflictTypeContent:
		return slicev1.ConflictType_CONFLICT_TYPE_CONTENT
	default:
		return slicev1.ConflictType_CONFLICT_TYPE_STALE_BASE
	}
}
