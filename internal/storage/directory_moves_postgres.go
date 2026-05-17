package storage

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

func (s *PostgresNativeStorage) CreateDirectoryMove(ctx context.Context, move *models.DirectoryMove) error {
	ctx = ensureCtx(ctx)
	return createDirectoryMove(ctx, s.pool, move)
}

func (s *postgresNativeTxView) CreateDirectoryMove(ctx context.Context, move *models.DirectoryMove) error {
	ctx = ensureCtx(ctx)
	return createDirectoryMove(ctx, s.tx, move)
}

func createDirectoryMove(ctx context.Context, exec execable, move *models.DirectoryMove) error {
	normalized, err := normalizeDirectoryMove(move)
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO directory_moves (
			move_id, home_id, source_slice_id, source_commit_hash,
			old_prefix, new_prefix, base_subtree_version, base_subtree_digest,
			new_subtree_version, merge_seq, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`,
		normalized.MoveID,
		normalized.HomeID,
		normalized.SourceSliceID,
		normalized.SourceCommitHash,
		normalized.OldPrefix,
		normalized.NewPrefix,
		normalized.BaseSubtreeVersion,
		normalized.BaseSubtreeDigest,
		normalized.NewSubtreeVersion,
		normalized.MergeSeq,
		normalized.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrInvalidInput
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) ListDirectoryMoves(ctx context.Context, homeID string) ([]*models.DirectoryMove, error) {
	ctx = ensureCtx(ctx)
	return listDirectoryMoves(ctx, s.pool, homeID)
}

func (s *postgresNativeTxView) ListDirectoryMoves(ctx context.Context, homeID string) ([]*models.DirectoryMove, error) {
	ctx = ensureCtx(ctx)
	return listDirectoryMoves(ctx, s.tx, homeID)
}

func listDirectoryMoves(ctx context.Context, q queryable, homeID string) ([]*models.DirectoryMove, error) {
	homeID = strings.TrimSpace(homeID)
	if homeID == "" {
		return nil, ErrInvalidInput
	}
	rows, err := q.Query(ctx, `
		SELECT move_id, home_id, source_slice_id, source_commit_hash,
		       old_prefix, new_prefix, base_subtree_version, base_subtree_digest,
		       new_subtree_version, merge_seq, created_at
		FROM directory_moves
		WHERE home_id = $1
		ORDER BY merge_seq ASC, move_id ASC
	`, homeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*models.DirectoryMove, 0)
	for rows.Next() {
		var move models.DirectoryMove
		if err := rows.Scan(
			&move.MoveID,
			&move.HomeID,
			&move.SourceSliceID,
			&move.SourceCommitHash,
			&move.OldPrefix,
			&move.NewPrefix,
			&move.BaseSubtreeVersion,
			&move.BaseSubtreeDigest,
			&move.NewSubtreeVersion,
			&move.MergeSeq,
			&move.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, &move)
	}
	if err := rows.Err(); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []*models.DirectoryMove{}, nil
		}
		return nil, err
	}
	return result, nil
}
