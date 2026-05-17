package storage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func (s *InMemoryStorage) CreateDirectoryMove(ctx context.Context, move *models.DirectoryMove) error {
	_ = ctx
	normalized, err := normalizeDirectoryMove(move)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.directoryMoves[normalized.MoveID]; exists {
		return ErrInvalidInput
	}
	s.directoryMoves[normalized.MoveID] = cloneDirectoryMove(normalized)
	return nil
}

func (s *InMemoryStorage) ListDirectoryMoves(ctx context.Context, homeID string) ([]*models.DirectoryMove, error) {
	_ = ctx
	homeID = strings.TrimSpace(homeID)
	if homeID == "" {
		return nil, ErrInvalidInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*models.DirectoryMove, 0)
	for _, move := range s.directoryMoves {
		if move == nil || strings.TrimSpace(move.HomeID) != homeID {
			continue
		}
		result = append(result, cloneDirectoryMove(move))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].MergeSeq == result[j].MergeSeq {
			return result[i].MoveID < result[j].MoveID
		}
		return result[i].MergeSeq < result[j].MergeSeq
	})
	return result, nil
}

func normalizeDirectoryMove(move *models.DirectoryMove) (*models.DirectoryMove, error) {
	if move == nil {
		return nil, ErrInvalidInput
	}
	normalized := *move
	normalized.MoveID = strings.TrimSpace(move.MoveID)
	normalized.HomeID = strings.TrimSpace(move.HomeID)
	normalized.SourceSliceID = strings.TrimSpace(move.SourceSliceID)
	normalized.SourceCommitHash = strings.TrimSpace(move.SourceCommitHash)
	normalized.OldPrefix = cleanRelativePath(move.OldPrefix)
	normalized.NewPrefix = cleanRelativePath(move.NewPrefix)
	normalized.BaseSubtreeDigest = strings.TrimSpace(move.BaseSubtreeDigest)
	if normalized.MoveID == "" ||
		normalized.HomeID == "" ||
		normalized.OldPrefix == "" ||
		normalized.NewPrefix == "" ||
		normalized.OldPrefix == normalized.NewPrefix ||
		normalized.BaseSubtreeVersion < 0 ||
		normalized.NewSubtreeVersion < 0 ||
		normalized.MergeSeq < 0 {
		return nil, ErrInvalidInput
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = time.Now()
	}
	return &normalized, nil
}

func cloneDirectoryMove(move *models.DirectoryMove) *models.DirectoryMove {
	if move == nil {
		return nil
	}
	clone := *move
	return &clone
}

func cloneDirectoryMoves(moves []*models.DirectoryMove) []*models.DirectoryMove {
	if moves == nil {
		return nil
	}
	out := make([]*models.DirectoryMove, 0, len(moves))
	for _, move := range moves {
		if move == nil {
			continue
		}
		out = append(out, cloneDirectoryMove(move))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
