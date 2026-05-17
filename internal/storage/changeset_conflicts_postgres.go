package storage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func (s *PostgresNativeStorage) ReplaceChangesetConflicts(ctx context.Context, changesetID string, conflicts []*models.ChangesetConflict) error {
	ctx = ensureCtx(ctx)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := replaceChangesetConflicts(ctx, tx, changesetID, conflicts); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *postgresNativeTxView) ReplaceChangesetConflicts(ctx context.Context, changesetID string, conflicts []*models.ChangesetConflict) error {
	ctx = ensureCtx(ctx)
	return replaceChangesetConflicts(ctx, s.tx, changesetID, conflicts)
}

func replaceChangesetConflicts(ctx context.Context, exec interface {
	queryable
	execable
}, changesetID string, conflicts []*models.ChangesetConflict) error {
	changesetID = strings.TrimSpace(changesetID)
	if changesetID == "" {
		return ErrInvalidInput
	}
	var exists bool
	if err := exec.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM changesets WHERE id = $1)`, changesetID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrChangesetNotFound
	}

	tag, err := exec.Exec(ctx, `DELETE FROM changeset_conflicts WHERE changeset_id = $1`, changesetID)
	if err != nil {
		return err
	}
	_ = tag
	if len(conflicts) == 0 {
		return nil
	}

	now := time.Now()
	for _, conflict := range conflicts {
		normalized := normalizeChangesetConflictForStore(changesetID, conflict, now)
		if normalized == nil {
			continue
		}
		var resolvedAt any
		if normalized.ResolvedAt != nil {
			resolvedAt = *normalized.ResolvedAt
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO changeset_conflicts (
				id, changeset_id, slice_id, path, type, message,
				base_version, current_version, base_hash, ours_hash, theirs_hash,
				patch, resolved, created_at, updated_at, resolved_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		`,
			normalized.ID,
			normalized.ChangesetID,
			normalized.SliceID,
			normalized.Path,
			normalized.Type,
			normalized.Message,
			normalized.BaseVersion,
			normalized.CurrentVersion,
			normalized.BaseHash,
			normalized.OursHash,
			normalized.TheirsHash,
			normalized.Patch,
			normalized.Resolved,
			normalized.CreatedAt,
			normalized.UpdatedAt,
			resolvedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresNativeStorage) ListChangesetConflicts(ctx context.Context, changesetID string) ([]*models.ChangesetConflict, error) {
	ctx = ensureCtx(ctx)
	return listChangesetConflicts(ctx, s.pool, changesetID)
}

func (s *postgresNativeTxView) ListChangesetConflicts(ctx context.Context, changesetID string) ([]*models.ChangesetConflict, error) {
	ctx = ensureCtx(ctx)
	return listChangesetConflicts(ctx, s.tx, changesetID)
}

func listChangesetConflicts(ctx context.Context, q queryable, changesetID string) ([]*models.ChangesetConflict, error) {
	changesetID = strings.TrimSpace(changesetID)
	if changesetID == "" {
		return nil, ErrInvalidInput
	}
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM changesets WHERE id = $1)`, changesetID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrChangesetNotFound
	}

	rows, err := q.Query(ctx, `
		SELECT id, changeset_id, slice_id, path, type, message,
		       base_version, current_version, base_hash, ours_hash, theirs_hash,
		       patch, resolved, created_at, updated_at, resolved_at
		FROM changeset_conflicts
		WHERE changeset_id = $1
		ORDER BY path ASC, id ASC
	`, changesetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*models.ChangesetConflict, 0)
	for rows.Next() {
		var conflict models.ChangesetConflict
		var resolvedAt *time.Time
		if err := rows.Scan(
			&conflict.ID,
			&conflict.ChangesetID,
			&conflict.SliceID,
			&conflict.Path,
			&conflict.Type,
			&conflict.Message,
			&conflict.BaseVersion,
			&conflict.CurrentVersion,
			&conflict.BaseHash,
			&conflict.OursHash,
			&conflict.TheirsHash,
			&conflict.Patch,
			&conflict.Resolved,
			&conflict.CreatedAt,
			&conflict.UpdatedAt,
			&resolvedAt,
		); err != nil {
			return nil, err
		}
		conflict.ResolvedAt = resolvedAt
		out = append(out, &conflict)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].ID < out[j].ID
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}
