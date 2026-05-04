package gitlayer

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

func (h *Handler) enqueueHomeSlicePromotion(ctx context.Context, sliceID, commitHash string, files []string, commitTime time.Time) error {
	sliceID = strings.TrimSpace(sliceID)
	if !homeslice.IsHomeSliceID(sliceID) {
		return nil
	}
	return h.rootPromotionQueue().Enqueue(ctx, rootpromote.Job{
		SliceID:    sliceID,
		CommitHash: strings.TrimSpace(commitHash),
		Files:      normalizeGitPromotionPaths(files),
		CommitTime: commitTime,
	})
}

func (h *Handler) waitForQueuedPromotions(ctx context.Context) error {
	return h.rootPromotionQueue().Wait(ctx)
}

func (h *Handler) rootPromotionQueue() *rootpromote.Queue {
	h.promotionQueueMu.Lock()
	defer h.promotionQueueMu.Unlock()
	if h.promotionQueue != nil {
		return h.promotionQueue
	}
	h.promotionQueue = rootpromote.New(h.promotionBatchWindow, h.promotionBatchMaxSize, func(ctx context.Context, batch []rootpromote.Job) error {
		if err := h.promoteHomeSliceBatch(ctx, batch); err != nil {
			sliceID := ""
			if len(batch) > 0 {
				sliceID = batch[0].SliceID
			}
			log.Printf("gitlayer: failed to promote %d queued home-slice commits for slice %s: %v", len(batch), sliceID, err)
			return err
		}
		return nil
	})
	return h.promotionQueue
}

func (h *Handler) promoteHomeSliceBatch(ctx context.Context, batch []rootpromote.Job) error {
	if len(batch) == 0 {
		return nil
	}
	if err := common.EnsureRootSliceInitialized(ctx, h.st); err != nil {
		return err
	}

	sliceSvc := sliceservice.NewInternalService(h.st)
	mergedAny := false
	for _, job := range latestGitHomePromotionJobs(batch) {
		modifiedPaths, err := homeslice.PendingPromotionPaths(ctx, h.st, job.SliceID)
		if err != nil {
			return fmt.Errorf("failed to compute pending promotion paths for %s: %w", job.SliceID, err)
		}
		if len(modifiedPaths) == 0 {
			continue
		}

		authCtx, err := gitPromotionAuthContext(ctx, job.SliceID)
		if err != nil {
			return err
		}
		createResp, err := sliceSvc.CreateChangeset(authCtx, &slicev1.CreateChangesetRequest{
			SliceId:        job.SliceID,
			BaseCommitHash: h.promotionBaseCommitHash(ctx),
			ModifiedFiles:  modifiedPaths,
			Message:        h.promotionChangesetMessage(ctx, job),
		})
		if err != nil {
			return fmt.Errorf("failed to create promotion changeset for %s: %w", job.SliceID, err)
		}

		mergeResp, err := sliceSvc.MergeChangesetUsingCurrentHead(authCtx, createResp.GetChangesetId())
		if err != nil {
			return fmt.Errorf("failed to merge promotion changeset for %s: %w", job.SliceID, err)
		}
		if mergeResp.GetStatus() == slicev1.MergeStatus_MERGE_STATUS_CONFLICT {
			return status.Errorf(codes.Aborted, "home slice promotion conflicts for %s: %s", job.SliceID, summarizeGitPromotionConflicts(mergeResp.GetConflicts()))
		}
		mergedAny = true
	}
	if !mergedAny {
		return nil
	}
	return sliceSvc.WaitForQueuedPromotions(ctx)
}

func latestGitHomePromotionJobs(batch []rootpromote.Job) []rootpromote.Job {
	latestBySlice := make(map[string]rootpromote.Job, len(batch))
	order := make([]string, 0, len(batch))
	for _, job := range batch {
		sliceID := strings.TrimSpace(job.SliceID)
		if !homeslice.IsHomeSliceID(sliceID) {
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

func normalizeGitPromotionPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, raw := range paths {
		cleaned := common.CleanRelativePath(raw)
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

func gitPromotionAuthContext(ctx context.Context, sliceID string) (context.Context, error) {
	username := homeslice.UsernameFromSliceID(sliceID)
	if username == "" {
		return nil, fmt.Errorf("slice %q is not a home slice", sliceID)
	}
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "User "+username)), nil
}

func (h *Handler) promotionBaseCommitHash(ctx context.Context) string {
	state, err := h.st.GetGlobalState(ctx)
	if err == nil && state != nil && strings.TrimSpace(state.GlobalCommitHash) != "" {
		return strings.TrimSpace(state.GlobalCommitHash)
	}
	rootSlice, err := h.st.GetRootSlice(ctx)
	if err != nil {
		return ""
	}
	meta, err := h.st.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(meta.HeadCommitHash)
}

func (h *Handler) promotionChangesetMessage(ctx context.Context, job rootpromote.Job) string {
	commit, err := h.st.GetCommitByHash(ctx, job.SliceID, job.CommitHash)
	if err == nil && commit != nil {
		if message := strings.TrimSpace(commit.Message); message != "" {
			return message
		}
	}
	if message := strings.TrimSpace(job.CommitHash); message != "" {
		return "publish " + message
	}
	return "publish home slice"
}

func summarizeGitPromotionConflicts(conflicts []*slicev1.Conflict) string {
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
