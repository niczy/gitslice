package sliceservice

import (
	"context"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const maxProjectionWait = 30 * time.Second

func (s *sliceServiceServer) GetProjectionStatus(ctx context.Context, req *slicev1.GetProjectionStatusRequest) (*slicev1.ProjectionStatus, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "projection status request is required")
	}
	projectionName := strings.TrimSpace(req.GetProjectionName())
	if projectionName == "" || req.GetShardId() < 0 || req.GetMergeSeq() < 0 {
		return nil, status.Error(codes.InvalidArgument, "projection_name, shard_id, and merge_seq are required")
	}
	wait := time.Duration(req.GetWaitMs()) * time.Millisecond
	if wait < 0 {
		wait = 0
	}
	if wait > maxProjectionWait {
		wait = maxProjectionWait
	}
	deadline := time.Now().Add(wait)
	for {
		projection, err := s.projectionStatus(ctx, projectionName, req.GetShardId(), req.GetMergeSeq())
		if err != nil {
			return nil, err
		}
		if projection.GetState() == slicev1.ProjectionState_PROJECTION_STATE_CAUGHT_UP || wait == 0 || !time.Now().Before(deadline) {
			return projection, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *sliceServiceServer) mergeProjectionStatuses(ctx context.Context, event *models.MergeEvent) []*slicev1.ProjectionStatus {
	if event == nil {
		return nil
	}
	projectionNames := []string{historyProjectionName}
	projections := make([]*slicev1.ProjectionStatus, 0, len(projectionNames))
	for _, projectionName := range projectionNames {
		projections = append(projections, &slicev1.ProjectionStatus{
			ProjectionName: projectionName,
			ShardId:        event.ShardID,
			RequestedSeq:   event.MergeSeq,
			State:          slicev1.ProjectionState_PROJECTION_STATE_PENDING,
		})
	}
	return projections
}

func (s *sliceServiceServer) projectionStatus(ctx context.Context, projectionName string, shardID int32, requestedSeq int64) (*slicev1.ProjectionStatus, error) {
	projectionName = strings.TrimSpace(projectionName)
	if projectionName == "" || shardID < 0 || requestedSeq < 0 {
		return nil, status.Error(codes.InvalidArgument, "projection_name, shard_id, and merge_seq are required")
	}
	eventStore, ok := s.projectionStore().(storage.MergeEventStore)
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "projection status is not supported by storage")
	}
	offset, err := eventStore.GetProjectionOffset(ctx, projectionName, shardID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	appliedSeq := int64(0)
	if offset != nil {
		appliedSeq = offset.MergeSeq
	}
	state := slicev1.ProjectionState_PROJECTION_STATE_PENDING
	if appliedSeq >= requestedSeq {
		state = slicev1.ProjectionState_PROJECTION_STATE_CAUGHT_UP
	}
	return &slicev1.ProjectionStatus{
		ProjectionName: projectionName,
		ShardId:        shardID,
		RequestedSeq:   requestedSeq,
		AppliedSeq:     appliedSeq,
		State:          state,
	}, nil
}

func mergeEventHomeIDFromEvent(event *models.MergeEvent) string {
	if event == nil {
		return ""
	}
	return strings.TrimSpace(event.HomeID)
}

func mergeEventShardFromEvent(event *models.MergeEvent) int32 {
	if event == nil {
		return 0
	}
	return event.ShardID
}

func mergeEventSeqFromEvent(event *models.MergeEvent) int64 {
	if event == nil {
		return 0
	}
	return event.MergeSeq
}
