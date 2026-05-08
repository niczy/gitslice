package sliceservice

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/rootpromote"
	"github.com/niczy/gitslice/internal/storage"
)

const durablePromotionProjectionName = "root-promotion"

type DurablePromotionConfig struct {
	Enabled      bool
	WorkerCount  int
	ShardCount   int32
	BatchSize    int
	PollInterval time.Duration
}

func (cfg DurablePromotionConfig) normalized() DurablePromotionConfig {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.ShardCount <= 0 {
		cfg.ShardCount = mergeEventShardCount
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = rootpromote.DefaultBatchMaxSize
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	return cfg
}

func (s *sliceServiceServer) StartDurablePromotionWorkers(ctx context.Context, cfg DurablePromotionConfig) {
	cfg = cfg.normalized()
	if !cfg.Enabled {
		return
	}
	if _, ok := s.promotionStore().(storage.MergeEventProjectionBatchProcessor); !ok {
		log.Printf("durable promotion requested but promotion storage does not support merge event projection batches")
		return
	}
	s.durablePromotion = true
	for i := 0; i < cfg.WorkerCount; i++ {
		workerID := i
		go s.runDurablePromotionWorker(ctx, cfg, workerID)
	}
	log.Printf("durable promotion workers started workers=%d shards=%d batch_size=%d poll_interval=%s", cfg.WorkerCount, cfg.ShardCount, cfg.BatchSize, cfg.PollInterval)
}

func (s *sliceServiceServer) runDurablePromotionWorker(ctx context.Context, cfg DurablePromotionConfig, workerID int) {
	for {
		processed, err := s.processDurablePromotionOnce(ctx, cfg)
		if err != nil {
			log.Printf("durable promotion worker=%d failed: %v", workerID, err)
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(cfg.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *sliceServiceServer) processDurablePromotionOnce(ctx context.Context, cfg DurablePromotionConfig) (bool, error) {
	cfg = cfg.normalized()
	processor, ok := s.promotionStore().(storage.MergeEventProjectionBatchProcessor)
	if !ok {
		return false, nil
	}
	return processor.ProcessMergeEventProjectionBatch(ctx, durablePromotionProjectionName, cfg.ShardCount, cfg.BatchSize, func(processCtx context.Context, events []*models.MergeEvent) error {
		return s.promoteMergeEvents(processCtx, events)
	})
}

func (s *sliceServiceServer) promoteMergeEvents(ctx context.Context, events []*models.MergeEvent) error {
	jobs := make([]rootpromote.Job, 0, len(events))
	for _, event := range events {
		job, ok := mergeEventPromotionJob(event)
		if !ok {
			continue
		}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return nil
	}
	return s.promoteSliceBatch(ctx, jobs)
}

func mergeEventPromotionJob(event *models.MergeEvent) (rootpromote.Job, bool) {
	if event == nil {
		return rootpromote.Job{}, false
	}
	sourceSliceID := strings.TrimSpace(event.SourceSliceID)
	commitHash := strings.TrimSpace(event.SourceCommitHash)
	if sourceSliceID == "" || commitHash == "" {
		return rootpromote.Job{}, false
	}
	files := mergeEventTouchedPaths(event)
	if len(files) == 0 {
		return rootpromote.Job{}, false
	}
	commitTime := event.CreatedAt
	if commitTime.IsZero() {
		commitTime = time.Now()
	}
	shardKey := "global"
	if homeID := strings.TrimSpace(event.HomeID); homeID != "" {
		shardKey = "home:" + homeID
	}
	return rootpromote.Job{
		SliceID:    sourceSliceID,
		CommitHash: commitHash,
		Files:      files,
		CommitTime: commitTime,
		ShardKey:   shardKey,
	}, true
}

func mergeEventTouchedPaths(event *models.MergeEvent) []string {
	if event == nil {
		return nil
	}
	paths := normalizeModifiedFiles(event.TouchedPaths)
	if len(paths) > 0 {
		return paths
	}
	raw := make([]string, 0, len(event.PathUpdates))
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		raw = append(raw, update.Path)
	}
	return normalizeModifiedFiles(raw)
}
