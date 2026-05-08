package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func (s *InMemoryStorage) NextMergeEventSequence(ctx context.Context, shardID int32) (int64, error) {
	_ = ctx
	if shardID < 0 {
		return 0, ErrInvalidInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var maxSeq int64
	for _, event := range s.mergeEventsByShard[shardID] {
		if event.MergeSeq > maxSeq {
			maxSeq = event.MergeSeq
		}
	}
	return maxSeq + 1, nil
}

func (s *InMemoryStorage) AppendMergeEvent(ctx context.Context, event *models.MergeEvent) error {
	_ = ctx
	normalized, err := normalizeMergeEvent(event)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.mergeEventsByID[normalized.EventID]; exists {
		return ErrMergeEventConflict
	}
	if _, exists := s.mergeEventsByChangeset[normalized.ChangesetID]; exists {
		return ErrMergeEventConflict
	}
	for _, existing := range s.mergeEventsByShard[normalized.ShardID] {
		if existing.MergeSeq == normalized.MergeSeq {
			return ErrMergeEventConflict
		}
	}

	stored := cloneMergeEvent(normalized)
	events := append(s.mergeEventsByShard[normalized.ShardID], stored)
	for i := len(events) - 1; i > 0 && events[i-1].MergeSeq > events[i].MergeSeq; i-- {
		events[i-1], events[i] = events[i], events[i-1]
	}
	s.mergeEventsByShard[normalized.ShardID] = events
	s.mergeEventsByChangeset[normalized.ChangesetID] = stored
	s.mergeEventsByID[normalized.EventID] = stored
	return nil
}

func (s *InMemoryStorage) GetMergeEventByChangeset(ctx context.Context, changesetID string) (*models.MergeEvent, error) {
	_ = ctx
	if changesetID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	event, exists := s.mergeEventsByChangeset[changesetID]
	if !exists {
		return nil, ErrMergeEventNotFound
	}
	return cloneMergeEvent(event), nil
}

func (s *InMemoryStorage) ListMergeEvents(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error) {
	_ = ctx
	if shardID < 0 || afterSeq < 0 {
		return nil, ErrInvalidInput
	}
	limit = normalizeMergeEventListLimit(limit)

	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.mergeEventsByShard[shardID]
	out := make([]*models.MergeEvent, 0, limit)
	for _, event := range events {
		if event.MergeSeq <= afterSeq {
			continue
		}
		out = append(out, cloneMergeEvent(event))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *InMemoryStorage) UpdateProjectionOffset(ctx context.Context, offset *models.ProjectionOffset) error {
	_ = ctx
	normalized, err := normalizeProjectionOffset(offset)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := projectionOffsetKey(normalized.ProjectionName, normalized.ShardID)
	existing := s.projectionOffsets[key]
	if existing != nil && existing.MergeSeq > normalized.MergeSeq {
		return nil
	}
	stored := *normalized
	s.projectionOffsets[key] = &stored
	return nil
}

func (s *InMemoryStorage) GetProjectionOffset(ctx context.Context, projectionName string, shardID int32) (*models.ProjectionOffset, error) {
	_ = ctx
	normalized, err := normalizeProjectionOffset(&models.ProjectionOffset{ProjectionName: projectionName, ShardID: shardID})
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key := projectionOffsetKey(normalized.ProjectionName, normalized.ShardID)
	existing := s.projectionOffsets[key]
	if existing == nil {
		return &models.ProjectionOffset{ProjectionName: normalized.ProjectionName, ShardID: normalized.ShardID}, nil
	}
	clone := *existing
	return &clone, nil
}

func (s *InMemoryStorage) ProcessMergeEventProjectionBatch(ctx context.Context, projectionName string, shardCount int32, limit int, fn func(context.Context, []*models.MergeEvent) error) (bool, error) {
	if fn == nil || shardCount <= 0 {
		return false, ErrInvalidInput
	}
	projectionName = normalizeProjectionName(projectionName)
	if projectionName == "" {
		return false, ErrInvalidInput
	}
	limit = normalizeMergeEventListLimit(limit)

	var events []*models.MergeEvent
	var shardID int32
	var latestSeq int64

	s.mu.RLock()
	for candidateShard := int32(0); candidateShard < shardCount; candidateShard++ {
		key := projectionOffsetKey(projectionName, candidateShard)
		var afterSeq int64
		if offset := s.projectionOffsets[key]; offset != nil {
			afterSeq = offset.MergeSeq
		}
		for _, event := range s.mergeEventsByShard[candidateShard] {
			if event.MergeSeq <= afterSeq {
				continue
			}
			events = append(events, cloneMergeEvent(event))
			latestSeq = event.MergeSeq
			if len(events) >= limit {
				break
			}
		}
		if len(events) > 0 {
			shardID = candidateShard
			break
		}
	}
	s.mu.RUnlock()

	if len(events) == 0 {
		return false, nil
	}
	if err := fn(ctx, events); err != nil {
		return true, err
	}
	return true, s.UpdateProjectionOffset(ctx, &models.ProjectionOffset{
		ProjectionName: projectionName,
		ShardID:        shardID,
		MergeSeq:       latestSeq,
		UpdatedAt:      time.Now(),
	})
}

func projectionOffsetKey(projectionName string, shardID int32) string {
	return fmt.Sprintf("%s:%d", projectionName, shardID)
}
