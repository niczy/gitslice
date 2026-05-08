package storage

import (
	"context"
	"fmt"

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

func projectionOffsetKey(projectionName string, shardID int32) string {
	return fmt.Sprintf("%s:%d", projectionName, shardID)
}
