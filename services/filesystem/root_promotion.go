package filesystemservice

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/rootpromote"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	sliceservice "github.com/niczy/gitslice/services/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *filesystemServiceServer) enqueueHomeSlicePromotion(ctx context.Context, workspaceID, commitHash string, files []string, commitTime time.Time) error {
	return s.rootPromotionQueue().Enqueue(ctx, rootpromote.Job{
		SliceID:    strings.TrimSpace(workspaceID),
		CommitHash: strings.TrimSpace(commitHash),
		Files:      normalizePromotionPaths(files),
		CommitTime: commitTime,
	})
}

func (s *filesystemServiceServer) waitForQueuedPromotions(ctx context.Context) error {
	return s.rootPromotionQueue().Wait(ctx)
}

func (s *filesystemServiceServer) rootPromotionQueue() *rootpromote.Queue {
	s.promotionQueueMu.Lock()
	defer s.promotionQueueMu.Unlock()
	if s.promotionQueue != nil {
		return s.promotionQueue
	}
	s.promotionQueue = rootpromote.New(s.promotionBatchWindow, s.promotionBatchMaxSize, func(ctx context.Context, batch []rootpromote.Job) error {
		if err := s.promoteHomeSliceBatch(ctx, batch); err != nil {
			sliceID := ""
			if len(batch) > 0 {
				sliceID = batch[0].SliceID
			}
			log.Printf("failed to promote %d queued home-slice commits for slice %s: %v", len(batch), sliceID, err)
			return err
		}
		return nil
	})
	return s.promotionQueue
}

func (s *filesystemServiceServer) promoteHomeSliceBatch(ctx context.Context, batch []rootpromote.Job) error {
	if len(batch) == 0 {
		return nil
	}
	if err := common.EnsureRootSliceInitialized(ctx, s.storage); err != nil {
		return err
	}

	sliceSvc := sliceservice.NewInternalService(s.storage)
	mergedAny := false
	for _, job := range latestPromotionJobs(batch) {
		if !homeslice.IsHomeSliceID(job.SliceID) {
			continue
		}
		modifiedPaths, err := homeslice.PendingPromotionPaths(ctx, s.storage, job.SliceID)
		if err != nil {
			return fmt.Errorf("failed to compute pending promotion paths for %s: %w", job.SliceID, err)
		}
		if len(modifiedPaths) == 0 {
			continue
		}

		authCtx, err := promotionAuthContext(ctx, job.SliceID)
		if err != nil {
			return err
		}
		createResp, err := sliceSvc.CreateChangeset(authCtx, &slicev1.CreateChangesetRequest{
			SliceId:        job.SliceID,
			BaseCommitHash: s.promotionBaseCommitHash(ctx),
			ModifiedFiles:  modifiedPaths,
			Message:        s.promotionChangesetMessage(ctx, job),
		})
		if err != nil {
			return fmt.Errorf("failed to create promotion changeset for %s: %w", job.SliceID, err)
		}

		mergeResp, err := sliceSvc.MergeChangesetUsingCurrentHead(authCtx, createResp.GetChangesetId())
		if err != nil {
			return fmt.Errorf("failed to merge promotion changeset for %s: %w", job.SliceID, err)
		}
		if mergeResp.GetStatus() == slicev1.MergeStatus_MERGE_STATUS_CONFLICT {
			return status.Errorf(codes.Aborted, "home slice promotion conflicts for %s: %s", job.SliceID, summarizePromotionConflicts(mergeResp.GetConflicts()))
		}
		mergedAny = true
	}

	if !mergedAny {
		return nil
	}
	return sliceSvc.WaitForQueuedPromotions(ctx)
}

func latestPromotionJobs(batch []rootpromote.Job) []rootpromote.Job {
	if len(batch) == 0 {
		return nil
	}
	latestBySlice := make(map[string]rootpromote.Job, len(batch))
	order := make([]string, 0, len(batch))
	for _, job := range batch {
		sliceID := strings.TrimSpace(job.SliceID)
		if sliceID == "" {
			continue
		}
		if _, seen := latestBySlice[sliceID]; !seen {
			order = append(order, sliceID)
		}
		latestBySlice[sliceID] = job
	}
	sort.Strings(order)
	result := make([]rootpromote.Job, 0, len(order))
	for _, sliceID := range order {
		result = append(result, latestBySlice[sliceID])
	}
	return result
}

func normalizePromotionPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, raw := range paths {
		cleaned := strings.TrimSpace(raw)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		result = append(result, cleaned)
	}
	sort.Strings(result)
	return result
}

func promotionAuthContext(ctx context.Context, sliceID string) (context.Context, error) {
	username := homeslice.UsernameFromSliceID(sliceID)
	if username == "" {
		return nil, fmt.Errorf("slice %q is not a home slice", sliceID)
	}
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "User "+username)), nil
}

func (s *filesystemServiceServer) promotionBaseCommitHash(ctx context.Context) string {
	state, err := s.storage.GetGlobalState(ctx)
	if err == nil && state != nil && strings.TrimSpace(state.GlobalCommitHash) != "" {
		return strings.TrimSpace(state.GlobalCommitHash)
	}
	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return ""
	}
	meta, err := s.storage.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(meta.HeadCommitHash)
}

func (s *filesystemServiceServer) promotionChangesetMessage(ctx context.Context, job rootpromote.Job) string {
	commit, err := s.storage.GetCommitByHash(ctx, job.SliceID, job.CommitHash)
	if err == nil {
		if message := strings.TrimSpace(commit.Message); message != "" {
			return message
		}
	}
	if message := strings.TrimSpace(job.CommitHash); message != "" {
		return "publish " + message
	}
	return "publish home slice"
}

func summarizePromotionConflicts(conflicts []*slicev1.Conflict) string {
	if len(conflicts) == 0 {
		return "unknown conflict"
	}
	parts := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		if conflict == nil {
			continue
		}
		fileID := strings.TrimSpace(conflict.GetFileId())
		if fileID == "" {
			fileID = "<unknown>"
		}
		targets := append([]string(nil), conflict.GetConflictingSliceIds()...)
		sort.Strings(targets)
		parts = append(parts, fmt.Sprintf("%s [%s]", fileID, strings.Join(targets, ", ")))
	}
	if len(parts) == 0 {
		return "unknown conflict"
	}
	return strings.Join(parts, "; ")
}
