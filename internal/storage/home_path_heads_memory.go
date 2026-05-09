package storage

import (
	"context"
	"sort"

	"github.com/niczy/gitslice/internal/models"
)

func (s *InMemoryStorage) UpsertHomePathHeads(ctx context.Context, heads []*models.HomePathHead) error {
	_ = ctx
	normalized := make([]*models.HomePathHead, 0, len(heads))
	for _, head := range heads {
		if head == nil {
			continue
		}
		cleaned, err := normalizeHomePathHead(head)
		if err != nil {
			return err
		}
		normalized = append(normalized, cleaned)
	}
	if len(normalized) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, head := range normalized {
		key := homePathHeadKey(head.HomeID, head.Path)
		stored := cloneHomePathHead(head)
		if existing := s.homePathHeads[key]; existing != nil {
			if stored.PathVersion < existing.PathVersion {
				stored.PathVersion = existing.PathVersion
			}
			if stored.LastMergeSeq < existing.LastMergeSeq {
				stored.LastMergeSeq = existing.LastMergeSeq
			}
		}
		s.homePathHeads[key] = stored
	}
	return nil
}

func (s *InMemoryStorage) GetHomePathHeads(ctx context.Context, homeID string, paths []string) (map[string]*models.HomePathHead, error) {
	_ = ctx
	homeID = normalizeHomePathHeadHomeID(homeID)
	cleanedPaths := normalizeHomePathHeadPaths(paths)
	if homeID == "" {
		return nil, ErrInvalidInput
	}
	result := make(map[string]*models.HomePathHead, len(cleanedPaths))
	if len(cleanedPaths) == 0 {
		return result, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, filePath := range cleanedPaths {
		if head := s.homePathHeads[homePathHeadKey(homeID, filePath)]; head != nil {
			result[filePath] = cloneHomePathHead(head)
		}
	}
	return result, nil
}

func (s *InMemoryStorage) ListHomePathHeads(ctx context.Context, homeID string, limit int) ([]*models.HomePathHead, error) {
	_ = ctx
	homeID = normalizeHomePathHeadHomeID(homeID)
	if homeID == "" {
		return nil, ErrInvalidInput
	}
	limit = normalizeHomePathHeadListLimit(limit)

	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*models.HomePathHead, 0, limit)
	for _, head := range s.homePathHeads {
		if head == nil || head.HomeID != homeID {
			continue
		}
		result = append(result, cloneHomePathHead(head))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *InMemoryStorage) BackfillHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadBackfillResult, error) {
	return backfillHomePathHeads(ctx, s, s, homeID)
}

func (s *InMemoryStorage) ValidateHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadValidationResult, error) {
	return validateHomePathHeads(ctx, s, s, homeID)
}
