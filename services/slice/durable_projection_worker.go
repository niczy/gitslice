package sliceservice

import (
	"context"
	"log"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type DurableProjectionConfig struct {
	Enabled      bool
	WorkerCount  int
	ShardCount   int32
	BatchSize    int
	PollInterval time.Duration
}

func (cfg DurableProjectionConfig) normalized() DurableProjectionConfig {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 1
	}
	if cfg.ShardCount <= 0 {
		cfg.ShardCount = mergeEventShardCount
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultProjectionBatchMaxSize
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	return cfg
}

func (s *sliceServiceServer) StartDurableProjectionWorkers(ctx context.Context, cfg DurableProjectionConfig) {
	cfg = cfg.normalized()
	if !cfg.Enabled {
		return
	}
	if _, ok := s.projectionStore().(storage.MergeEventProjectionBatchProcessor); !ok {
		log.Printf("durable projection requested but projection storage does not support merge event projection batches")
		return
	}
	s.durableProjection = true
	for i := 0; i < cfg.WorkerCount; i++ {
		workerID := i
		go s.runDurableHistoryProjectionWorker(ctx, cfg, workerID)
	}
	log.Printf("durable history projection workers started workers=%d shards=%d batch_size=%d poll_interval=%s", cfg.WorkerCount, cfg.ShardCount, cfg.BatchSize, cfg.PollInterval)
}

func (s *sliceServiceServer) EnableDurableProjectionModeForTesting() {
	s.durableProjection = true
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
