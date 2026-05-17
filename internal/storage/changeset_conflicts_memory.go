package storage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func (s *InMemoryStorage) ReplaceChangesetConflicts(ctx context.Context, changesetID string, conflicts []*models.ChangesetConflict) error {
	_ = ctx
	changesetID = strings.TrimSpace(changesetID)
	if changesetID == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.changesets[changesetID]; !ok {
		return ErrChangesetNotFound
	}
	if len(conflicts) == 0 {
		delete(s.changesetConflicts, changesetID)
		return nil
	}

	now := time.Now()
	replacement := make(map[string]*models.ChangesetConflict, len(conflicts))
	for _, conflict := range conflicts {
		normalized := normalizeChangesetConflictForStore(changesetID, conflict, now)
		if normalized == nil {
			continue
		}
		replacement[normalized.ID] = normalized
	}
	if len(replacement) == 0 {
		delete(s.changesetConflicts, changesetID)
		return nil
	}
	s.changesetConflicts[changesetID] = replacement
	return nil
}

func (s *InMemoryStorage) ListChangesetConflicts(ctx context.Context, changesetID string) ([]*models.ChangesetConflict, error) {
	_ = ctx
	changesetID = strings.TrimSpace(changesetID)
	if changesetID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.changesets[changesetID]; !ok {
		return nil, ErrChangesetNotFound
	}
	byID := s.changesetConflicts[changesetID]
	out := make([]*models.ChangesetConflict, 0, len(byID))
	for _, conflict := range byID {
		out = append(out, cloneChangesetConflict(conflict))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].ID < out[j].ID
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func cloneChangesetConflict(conflict *models.ChangesetConflict) *models.ChangesetConflict {
	if conflict == nil {
		return nil
	}
	copy := *conflict
	if conflict.ResolvedAt != nil {
		resolvedAt := *conflict.ResolvedAt
		copy.ResolvedAt = &resolvedAt
	}
	return &copy
}
