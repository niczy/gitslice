package filesystemservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/rootpromote"
	"github.com/niczy/gitslice/internal/storage"
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
	return rootpromote.WithGlobalLock(func() error {
		if err := common.EnsureRootSliceInitialized(ctx, s.storage); err != nil {
			return err
		}
		rootSlice, err := s.storage.GetRootSlice(ctx)
		if err != nil {
			return fmt.Errorf("failed to load root slice: %w", err)
		}

		for _, job := range latestPromotionJobs(batch) {
			if !homeslice.IsHomeSliceID(job.SliceID) {
				continue
			}
			if _, err := homeslice.SyncHomeSliceToRoot(ctx, s.storage, job.SliceID); err != nil {
				return fmt.Errorf("failed to sync %s into root: %w", job.SliceID, err)
			}
		}

		state, err := s.storage.GetGlobalState(ctx)
		if err != nil && !errors.Is(err, storage.ErrInvalidInput) {
			return fmt.Errorf("failed to load global state: %w", err)
		}
		if state == nil {
			state = &models.GlobalState{}
		}

		history := make([]*models.GlobalCommit, 0, len(batch))
		for i := len(batch) - 1; i >= 0; i-- {
			job := batch[i]
			history = append(history, &models.GlobalCommit{
				CommitHash:     job.CommitHash,
				Timestamp:      job.CommitTime,
				MergedSliceIDs: []string{job.SliceID},
			})
		}
		latest := batch[len(batch)-1]
		state.GlobalCommitHash = latest.CommitHash
		state.Timestamp = latest.CommitTime
		state.History = append(history, state.History...)
		if err := s.storage.UpdateGlobalState(ctx, state); err != nil {
			return fmt.Errorf("failed to update global state: %w", err)
		}

		rootMetadata, err := s.storage.GetSliceMetadata(ctx, rootSlice.ID)
		if err != nil {
			return fmt.Errorf("failed to load root metadata: %w", err)
		}
		rootMetadata.HeadCommitHash = state.GlobalCommitHash
		rootMetadata.ModifiedFiles = normalizePromotionPaths(latest.Files)
		rootMetadata.ModifiedFilesCount = len(rootMetadata.ModifiedFiles)
		rootMetadata.LastModified = state.Timestamp
		if err := s.storage.UpdateSliceMetadata(ctx, rootSlice.ID, rootMetadata); err != nil {
			return fmt.Errorf("failed to update root metadata: %w", err)
		}
		return nil
	})
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
