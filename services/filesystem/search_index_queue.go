package filesystemservice

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
)

const (
	defaultWorkspaceSearchIndexQueueSize = 8192
	defaultWorkspaceSearchIndexTimeout   = 10 * time.Minute
	searchProjectionName                 = "search-index"
)

type workspaceSearchIndexJob struct {
	workspaceID string
	commitHash  string
}

type workspaceSearchIndexQueue struct {
	once         sync.Once
	jobs         chan workspaceSearchIndexJob
	wg           sync.WaitGroup
	batchWindow  time.Duration
	batchMaxSize int
	process      func(context.Context, []workspaceSearchIndexJob) error
}

func newWorkspaceSearchIndexQueue(batchWindow time.Duration, batchMaxSize int, process func(context.Context, []workspaceSearchIndexJob) error) *workspaceSearchIndexQueue {
	if batchWindow <= 0 {
		batchWindow = 100 * time.Millisecond
	}
	if batchMaxSize <= 0 {
		batchMaxSize = 256
	}
	return &workspaceSearchIndexQueue{
		jobs:         make(chan workspaceSearchIndexJob, defaultWorkspaceSearchIndexQueueSize),
		batchWindow:  batchWindow,
		batchMaxSize: batchMaxSize,
		process:      process,
	}
}

func (q *workspaceSearchIndexQueue) Enqueue(job workspaceSearchIndexJob) bool {
	if q == nil || q.process == nil {
		return false
	}
	if strings.TrimSpace(job.workspaceID) == "" || strings.TrimSpace(job.commitHash) == "" {
		return true
	}

	q.once.Do(func() {
		go q.run()
	})

	q.wg.Add(1)
	select {
	case q.jobs <- cloneWorkspaceSearchIndexJob(job):
		return true
	default:
		q.wg.Done()
		return false
	}
}

func (q *workspaceSearchIndexQueue) Wait(ctx context.Context) error {
	if q == nil {
		return nil
	}

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *workspaceSearchIndexQueue) run() {
	for {
		current, ok := <-q.jobs
		if !ok {
			return
		}

		batch := []workspaceSearchIndexJob{current}
		timer := time.NewTimer(q.batchWindow)
		collecting := true
		for collecting && len(batch) < q.batchMaxSize {
			select {
			case nextJob, open := <-q.jobs:
				if !open {
					collecting = false
					break
				}
				batch = append(batch, nextJob)
			case <-timer.C:
				collecting = false
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		_ = q.process(context.Background(), batch)
		for range batch {
			q.wg.Done()
		}
	}
}

func cloneWorkspaceSearchIndexJob(job workspaceSearchIndexJob) workspaceSearchIndexJob {
	return workspaceSearchIndexJob{
		workspaceID: strings.TrimSpace(job.workspaceID),
		commitHash:  strings.TrimSpace(job.commitHash),
	}
}

func (s *filesystemServiceServer) enqueueWorkspaceSearchIndex(workspaceID, commitHash string) {
	job := workspaceSearchIndexJob{
		workspaceID: strings.TrimSpace(workspaceID),
		commitHash:  strings.TrimSpace(commitHash),
	}
	if job.workspaceID == "" || job.commitHash == "" {
		return
	}
	if ok := s.workspaceSearchIndexQueue().Enqueue(job); !ok {
		log.Printf("filesystem: dropped search index job for workspace %s commit %s because queue is full", job.workspaceID, job.commitHash)
	}
}

func (s *filesystemServiceServer) waitForQueuedSearchIndexing(ctx context.Context) error {
	return s.workspaceSearchIndexQueue().Wait(ctx)
}

func (s *filesystemServiceServer) workspaceSearchIndexQueue() *workspaceSearchIndexQueue {
	s.searchIndexQueueMu.Lock()
	defer s.searchIndexQueueMu.Unlock()
	if s.searchIndexQueue != nil {
		return s.searchIndexQueue
	}
	s.searchIndexQueue = newWorkspaceSearchIndexQueue(s.searchIndexBatchWindow, s.searchIndexBatchMaxSize, func(ctx context.Context, batch []workspaceSearchIndexJob) error {
		if err := s.indexWorkspaceSearchBatch(ctx, batch); err != nil {
			workspaceID := ""
			if len(batch) > 0 {
				workspaceID = batch[0].workspaceID
			}
			log.Printf("filesystem: failed to index %d queued workspace commits for %s: %v", len(batch), workspaceID, err)
			return err
		}
		return nil
	})
	return s.searchIndexQueue
}

func (s *filesystemServiceServer) indexWorkspaceSearchBatch(ctx context.Context, batch []workspaceSearchIndexJob) error {
	var firstErr error
	for _, job := range latestWorkspaceSearchIndexJobs(batch) {
		if err := s.indexWorkspaceSearchCommit(ctx, job.workspaceID, job.commitHash); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			log.Printf("filesystem: failed to refresh search artifact for commit %s in %s: %v", job.commitHash, job.workspaceID, err)
		}
	}
	return firstErr
}

func latestWorkspaceSearchIndexJobs(batch []workspaceSearchIndexJob) []workspaceSearchIndexJob {
	if len(batch) == 0 {
		return nil
	}
	latestByWorkspace := make(map[string]workspaceSearchIndexJob, len(batch))
	order := make([]string, 0, len(batch))
	for _, job := range batch {
		workspaceID := strings.TrimSpace(job.workspaceID)
		commitHash := strings.TrimSpace(job.commitHash)
		if workspaceID == "" || commitHash == "" {
			continue
		}
		if _, seen := latestByWorkspace[workspaceID]; !seen {
			order = append(order, workspaceID)
		}
		latestByWorkspace[workspaceID] = workspaceSearchIndexJob{
			workspaceID: workspaceID,
			commitHash:  commitHash,
		}
	}

	result := make([]workspaceSearchIndexJob, 0, len(order))
	for _, workspaceID := range order {
		result = append(result, latestByWorkspace[workspaceID])
	}
	return result
}

func (s *filesystemServiceServer) indexWorkspaceSearchCommit(ctx context.Context, workspaceID, commitHash string) error {
	ctx, cancel := context.WithTimeout(ctx, s.searchIndexBuildTimeout)
	defer cancel()

	current, err := s.workspaceCommitIsCurrent(ctx, workspaceID, commitHash)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}

	artifact, err := storage.BuildAndStoreSliceSearchArtifact(ctx, s.storage, workspaceID, commitHash)
	if err != nil {
		return err
	}

	current, err = s.workspaceCommitIsCurrent(ctx, workspaceID, commitHash)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	if err := storage.StoreWorkspaceSearchArtifact(ctx, s.storage, workspaceID, artifact); err != nil {
		return fmt.Errorf("store workspace search artifact: %w", err)
	}
	if err := s.updateSearchProjectionOffsetForCommit(ctx, commitHash); err != nil {
		if current, currentErr := s.workspaceCommitIsCurrent(ctx, workspaceID, commitHash); currentErr != nil {
			log.Printf("filesystem: failed to check current commit after projection offset failure workspace=%s commit=%s: %v", workspaceID, commitHash, currentErr)
		} else if current {
			if deleteErr := s.storage.DeleteWorkspaceSearchArtifact(ctx, workspaceID, searchindex.CurrentArtifactVersion); deleteErr != nil {
				log.Printf("filesystem: failed to delete search artifact after projection offset failure workspace=%s commit=%s: %v", workspaceID, commitHash, deleteErr)
			}
		}
		return fmt.Errorf("update search projection offset: %w", err)
	}
	return nil
}

func (s *filesystemServiceServer) updateSearchProjectionOffsetForCommit(ctx context.Context, commitHash string) error {
	eventStore, ok := s.storage.(storage.MergeEventStore)
	if !ok {
		return nil
	}
	event, err := eventStore.GetMergeEventBySourceCommitHash(ctx, strings.TrimSpace(commitHash))
	if err != nil {
		if errors.Is(err, storage.ErrMergeEventNotFound) {
			return nil
		}
		return err
	}
	if event == nil || event.ShardID < 0 || event.MergeSeq <= 0 {
		return nil
	}
	return eventStore.UpdateProjectionOffset(ctx, &models.ProjectionOffset{
		ProjectionName: searchProjectionName,
		ShardID:        event.ShardID,
		MergeSeq:       event.MergeSeq,
		UpdatedAt:      time.Now(),
	})
}

func (s *filesystemServiceServer) workspaceCommitIsCurrent(ctx context.Context, workspaceID, commitHash string) (bool, error) {
	meta, err := s.storage.GetSliceMetadata(ctx, strings.TrimSpace(workspaceID))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(meta.HeadCommitHash) == strings.TrimSpace(commitHash), nil
}
