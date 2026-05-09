package sliceservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const historyProjectionName = "history-projection"

type HistoryProjectionBackfillResult struct {
	Batches int
	Events  int
}

func (s *sliceServiceServer) enqueueHistoryProjection(ctx context.Context, event *models.MergeEvent) {
	if event == nil || s.durablePromotion {
		return
	}
	cloned := cloneHistoryMergeEvent(event)
	s.historyProjectionWG.Add(1)
	go func() {
		defer s.historyProjectionWG.Done()
		bg := context.Background()
		if err := s.projectMergeEventHistoryBatch(bg, []*models.MergeEvent{cloned}); err != nil {
			log.Printf("failed to project merge history for changeset %s: %v", cloned.ChangesetID, err)
			return
		}
		if err := s.updateHistoryProjectionOffsets(bg, []*models.MergeEvent{cloned}); err != nil {
			log.Printf("failed to update history projection offset for changeset %s: %v", cloned.ChangesetID, err)
		}
	}()
}

func (s *sliceServiceServer) waitForQueuedHistoryProjections(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.historyProjectionWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *sliceServiceServer) runDurableHistoryProjectionWorker(ctx context.Context, cfg DurablePromotionConfig, workerID int) {
	for {
		processed, err := s.processDurableHistoryProjectionOnce(ctx, cfg)
		if err != nil {
			log.Printf("durable history projection worker=%d failed: %v", workerID, err)
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(cfg.normalized().PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *sliceServiceServer) processDurableHistoryProjectionOnce(ctx context.Context, cfg DurablePromotionConfig) (bool, error) {
	processed, _, err := s.processDurableHistoryProjectionBatch(ctx, cfg)
	return processed, err
}

func (s *sliceServiceServer) processDurableHistoryProjectionBatch(ctx context.Context, cfg DurablePromotionConfig) (bool, int, error) {
	cfg = cfg.normalized()
	processor, ok := s.promotionStore().(storage.MergeEventProjectionBatchProcessor)
	if !ok {
		return false, 0, nil
	}
	eventCount := 0
	processed, err := processor.ProcessMergeEventProjectionBatch(ctx, historyProjectionName, cfg.ShardCount, cfg.BatchSize, func(processCtx context.Context, events []*models.MergeEvent) error {
		eventCount = len(events)
		return s.projectMergeEventHistoryBatch(processCtx, events)
	})
	return processed, eventCount, err
}

func (s *sliceServiceServer) BackfillHistoryProjection(ctx context.Context, cfg DurablePromotionConfig, maxBatches int) (HistoryProjectionBackfillResult, error) {
	var result HistoryProjectionBackfillResult
	for maxBatches <= 0 || result.Batches < maxBatches {
		processed, events, err := s.processDurableHistoryProjectionBatch(ctx, cfg)
		if err != nil {
			return result, err
		}
		if !processed {
			return result, nil
		}
		result.Batches++
		result.Events += events
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *sliceServiceServer) projectMergeEventHistoryBatch(ctx context.Context, events []*models.MergeEvent) error {
	st := s.promotionStore()
	for _, event := range events {
		if err := s.projectMergeEventHistory(ctx, st, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *sliceServiceServer) projectMergeEventHistory(ctx context.Context, st storage.Storage, event *models.MergeEvent) error {
	if event == nil {
		return nil
	}
	sliceID := strings.TrimSpace(event.SourceSliceID)
	commitHash := strings.TrimSpace(event.SourceCommitHash)
	if sliceID == "" || commitHash == "" {
		return nil
	}
	commitTime := event.CreatedAt
	if commitTime.IsZero() {
		commitTime = time.Now()
	}
	parentHash := s.mergeEventParentCommitHash(ctx, st, event)
	if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: parentHash,
		Timestamp:  commitTime,
		Message:    event.Message,
	}); err != nil {
		return fmt.Errorf("project slice commit %s: %w", commitHash, err)
	}
	parentFiles, err := s.createCommitSnapshotFromMergeEvent(ctx, st, event, parentHash, commitTime)
	if err != nil {
		return err
	}
	changes, err := s.buildFileChangeRecordsFromMergeEvent(ctx, st, event, parentFiles, commitTime)
	if err != nil {
		return err
	}
	if err := addFileChangesIdempotently(ctx, st, changes); err != nil {
		return fmt.Errorf("project file changes for commit %s: %w", commitHash, err)
	}
	return nil
}

func (s *sliceServiceServer) createCommitSnapshotFromMergeEvent(ctx context.Context, st storage.Storage, event *models.MergeEvent, parentHash string, timestamp time.Time) (map[string]string, error) {
	parentFiles := make(map[string]string)
	if parentHash != "" {
		parentSnapshot, err := st.GetCommitSnapshot(ctx, parentHash)
		if err != nil {
			if !errors.Is(err, storage.ErrCommitNotFound) {
				return nil, fmt.Errorf("load parent snapshot %s: %w", parentHash, err)
			}
		} else if parentSnapshot != nil {
			for filePath, contentHash := range parentSnapshot.Files {
				cleanedPath := cleanDiffPath(filePath)
				cleanedHash := strings.TrimSpace(contentHash)
				if cleanedPath == "" || !isUsableContentHash(cleanedPath, cleanedHash) {
					continue
				}
				parentFiles[cleanedPath] = cleanedHash
			}
		}
	}

	files := make(map[string]string, len(parentFiles)+len(event.PathUpdates))
	for filePath, contentHash := range parentFiles {
		files[filePath] = contentHash
	}
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		filePath := cleanDiffPath(update.Path)
		if filePath == "" {
			continue
		}
		if update.Deleted {
			delete(files, filePath)
			continue
		}
		hash := mergePathUpdateHash(update)
		if hash == "" {
			continue
		}
		files[filePath] = hash
	}

	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: strings.TrimSpace(event.SourceCommitHash),
		SliceID:    strings.TrimSpace(event.SourceSliceID),
		Files:      files,
		Timestamp:  timestamp,
	}); err != nil {
		return nil, fmt.Errorf("project commit snapshot %s: %w", event.SourceCommitHash, err)
	}
	return parentFiles, nil
}

func (s *sliceServiceServer) buildFileChangeRecordsFromMergeEvent(ctx context.Context, st storage.Storage, event *models.MergeEvent, parentFiles map[string]string, timestamp time.Time) ([]*models.FileChangeRecord, error) {
	updates := mergeEventPathUpdatesByPath(event)
	paths := mergeEventTouchedPaths(event)
	if len(paths) == 0 && len(updates) > 0 {
		for filePath := range updates {
			paths = append(paths, filePath)
		}
	}

	changes := make([]*models.FileChangeRecord, 0, len(paths))
	for _, rawPath := range paths {
		filePath := cleanDiffPath(rawPath)
		if filePath == "" {
			continue
		}
		update := updates[filePath]
		oldHash := strings.TrimSpace(parentFiles[filePath])
		if oldHash == "" {
			oldHash = previousKnownFileHashExcluding(ctx, st, event.SourceSliceID, filePath, event.SourceCommitHash)
		}
		newHash := ""
		deleted := false
		if update != nil {
			newHash = mergePathUpdateHash(update)
			deleted = update.Deleted
		}

		changeType := models.ChangeTypeModify
		linesAdded := 0
		linesDeleted := 0
		switch {
		case oldHash == "" && newHash != "" && !deleted:
			changeType = models.ChangeTypeAdd
			if fileContent, readErr := storage.ReadVersionedFileContent(ctx, st, newHash); readErr == nil && fileContent != nil && len(fileContent.Content) > 0 {
				linesAdded = countTextLines(fileContent.Content)
			}
		case oldHash != "" && (newHash == "" || deleted):
			changeType = models.ChangeTypeDelete
			if previousContent, hashErr := storage.ReadVersionedFileContent(ctx, st, oldHash); hashErr == nil && previousContent != nil {
				linesDeleted = countTextLines(previousContent.Content)
			}
			newHash = ""
		default:
			changeType = models.ChangeTypeModify
		}

		changes = append(changes, &models.FileChangeRecord{
			ID:           common.GenerateFileChangeID(event.SourceCommitHash, filePath),
			SliceID:      strings.TrimSpace(event.SourceSliceID),
			CommitHash:   strings.TrimSpace(event.SourceCommitHash),
			Path:         filePath,
			ChangeType:   changeType,
			OldHash:      oldHash,
			NewHash:      newHash,
			LinesAdded:   linesAdded,
			LinesDeleted: linesDeleted,
			Author:       strings.TrimSpace(event.Author),
			Message:      event.Message,
			Timestamp:    timestamp,
		})
	}
	return changes, nil
}

func previousKnownFileHashExcluding(ctx context.Context, st storage.Storage, sliceID, filePath, excludedCommit string) string {
	cleanedPath := cleanDiffPath(filePath)
	if cleanedPath == "" {
		return ""
	}
	history, err := st.GetFileHistory(ctx, strings.TrimSpace(sliceID), cleanedPath, 64, "")
	if err != nil {
		return ""
	}
	excludedCommit = strings.TrimSpace(excludedCommit)
	for _, item := range history {
		if item == nil || strings.TrimSpace(item.CommitHash) == excludedCommit {
			continue
		}
		candidate := strings.TrimSpace(item.NewHash)
		if isUsableContentHash(cleanedPath, candidate) {
			return candidate
		}
	}
	return ""
}

func addFileChangesIdempotently(ctx context.Context, st storage.Storage, changes []*models.FileChangeRecord) error {
	for _, change := range changes {
		if change == nil {
			continue
		}
		if err := st.AddFileChange(ctx, change); err != nil {
			return err
		}
	}
	return nil
}

func (s *sliceServiceServer) updateHistoryProjectionOffsets(ctx context.Context, events []*models.MergeEvent) error {
	eventStore, ok := s.promotionStore().(storage.MergeEventStore)
	if !ok {
		return nil
	}
	latestByShard := make(map[int32]int64)
	for _, event := range events {
		if event == nil {
			continue
		}
		if event.MergeSeq > latestByShard[event.ShardID] {
			latestByShard[event.ShardID] = event.MergeSeq
		}
	}
	for shardID, seq := range latestByShard {
		if err := eventStore.UpdateProjectionOffset(ctx, &models.ProjectionOffset{
			ProjectionName: historyProjectionName,
			ShardID:        shardID,
			MergeSeq:       seq,
			UpdatedAt:      time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *sliceServiceServer) mergeEventParentCommitHash(ctx context.Context, st storage.Storage, event *models.MergeEvent) string {
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		if parentHash := strings.TrimSpace(update.ParentCommitHash); parentHash != "" {
			return parentHash
		}
	}
	commit, err := st.GetCommitByHash(ctx, strings.TrimSpace(event.SourceSliceID), strings.TrimSpace(event.SourceCommitHash))
	if err == nil && commit != nil {
		return strings.TrimSpace(commit.ParentHash)
	}
	return ""
}

func mergePathUpdateHash(update *models.MergePathUpdate) string {
	if update == nil {
		return ""
	}
	if hash := strings.TrimSpace(update.ManifestHash); hash != "" {
		return hash
	}
	return strings.TrimSpace(update.ContentHash)
}

func mergeEventPathUpdatesByPath(event *models.MergeEvent) map[string]*models.MergePathUpdate {
	updates := make(map[string]*models.MergePathUpdate)
	if event == nil {
		return updates
	}
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		filePath := cleanDiffPath(update.Path)
		if filePath == "" {
			continue
		}
		updates[filePath] = update
	}
	return updates
}

func cloneHistoryMergeEvent(event *models.MergeEvent) *models.MergeEvent {
	if event == nil {
		return nil
	}
	clone := *event
	clone.TouchedPaths = append([]string(nil), event.TouchedPaths...)
	clone.PathUpdates = make([]*models.MergePathUpdate, 0, len(event.PathUpdates))
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		updateClone := *update
		clone.PathUpdates = append(clone.PathUpdates, &updateClone)
	}
	return &clone
}
