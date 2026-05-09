package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

const mergeEventSequenceAdvisoryLockClass int32 = 93242

func (s *PostgresNativeStorage) NextMergeEventSequence(ctx context.Context, shardID int32) (int64, error) {
	ctx = ensureCtx(ctx)
	if shardID < 0 {
		return 0, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	seq, err := nextMergeEventSequence(ctx, tx, tx, shardID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *postgresNativeTxView) NextMergeEventSequence(ctx context.Context, shardID int32) (int64, error) {
	ctx = ensureCtx(ctx)
	return nextMergeEventSequence(ctx, s.tx, s.tx, shardID)
}

func nextMergeEventSequence(ctx context.Context, q queryable, exec execable, shardID int32) (int64, error) {
	if shardID < 0 {
		return 0, ErrInvalidInput
	}
	var seq int64
	if err := q.QueryRow(ctx, `
		INSERT INTO merge_event_shard_sequences (shard_id, next_seq)
		VALUES ($1, 2)
		ON CONFLICT (shard_id) DO UPDATE
		SET next_seq = merge_event_shard_sequences.next_seq + 1
		RETURNING next_seq - 1
	`, shardID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *PostgresNativeStorage) AppendMergeEvent(ctx context.Context, event *models.MergeEvent) error {
	ctx = ensureCtx(ctx)
	return appendMergeEvent(ctx, s.pool, event)
}

func (s *postgresNativeTxView) AppendMergeEvent(ctx context.Context, event *models.MergeEvent) error {
	ctx = ensureCtx(ctx)
	return appendMergeEvent(ctx, s.tx, event)
}

func (s *PostgresNativeStorage) AppendMergeEventWithPathHeadCAS(ctx context.Context, event *models.MergeEvent) error {
	ctx = ensureCtx(ctx)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := appendMergeEventWithPathHeadCAS(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *postgresNativeTxView) AppendMergeEventWithPathHeadCAS(ctx context.Context, event *models.MergeEvent) error {
	ctx = ensureCtx(ctx)
	return appendMergeEventWithPathHeadCAS(ctx, s.tx, event)
}

func appendMergeEvent(ctx context.Context, exec execable, event *models.MergeEvent) error {
	normalized, err := normalizeMergeEvent(event)
	if err != nil {
		return err
	}
	touchedPaths, err := json.Marshal(normalized.TouchedPaths)
	if err != nil {
		return fmt.Errorf("marshal touched paths: %w", err)
	}
	pathUpdates, err := json.Marshal(normalized.PathUpdates)
	if err != nil {
		return fmt.Errorf("marshal path updates: %w", err)
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO merge_events (
			home_id, shard_id, merge_seq, event_id, changeset_id,
			source_slice_id, source_commit_hash, author, message,
			touched_paths, path_updates, forced, force_reason, forced_by, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb, $12, $13, $14, $15)
	`,
		normalized.HomeID,
		normalized.ShardID,
		normalized.MergeSeq,
		normalized.EventID,
		normalized.ChangesetID,
		normalized.SourceSliceID,
		normalized.SourceCommitHash,
		normalized.Author,
		normalized.Message,
		touchedPaths,
		pathUpdates,
		normalized.Forced,
		normalized.ForceReason,
		normalized.ForcedBy,
		normalized.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrMergeEventConflict
		}
		return err
	}
	return nil
}

func appendMergeEventWithPathHeadCAS(ctx context.Context, exec execable, event *models.MergeEvent) error {
	normalized, err := normalizeMergeEvent(event)
	if err != nil {
		return err
	}
	heads, err := homePathHeadsFromMergeEvent(normalized)
	if err != nil {
		return err
	}
	if len(heads) != len(normalized.PathUpdates) {
		return ErrInvalidInput
	}
	for _, update := range normalized.PathUpdates {
		if update == nil {
			return ErrInvalidInput
		}
		path := cleanRelativePath(update.Path)
		if path == "" || update.BaseVersion < 0 || update.NewVersion <= update.BaseVersion {
			return ErrInvalidInput
		}
		if update.BaseVersion == 0 {
			head := headsByPath(heads)[path]
			if head == nil {
				return ErrInvalidInput
			}
			tag, err := exec.Exec(ctx, `
				INSERT INTO home_path_heads (
					home_id, path, path_version, content_hash, manifest_hash,
					source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (home_id, path) DO NOTHING
			`,
				head.HomeID,
				head.Path,
				head.PathVersion,
				head.ContentHash,
				head.ManifestHash,
				head.SourceSliceID,
				head.SourceCommitHash,
				head.LastMergeSeq,
				head.Deleted,
				head.UpdatedAt,
			)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return ErrHomePathHeadConflict
			}
			continue
		}

		head := headsByPath(heads)[path]
		if head == nil {
			return ErrInvalidInput
		}
		tag, err := exec.Exec(ctx, `
			UPDATE home_path_heads
			SET path_version = $1,
			    content_hash = $2,
			    manifest_hash = $3,
			    source_slice_id = $4,
			    source_commit_hash = $5,
			    last_merge_seq = $6,
			    deleted = $7,
			    updated_at = $8
			WHERE home_id = $9
			  AND path = $10
			  AND path_version = $11
		`,
			head.PathVersion,
			head.ContentHash,
			head.ManifestHash,
			head.SourceSliceID,
			head.SourceCommitHash,
			head.LastMergeSeq,
			head.Deleted,
			head.UpdatedAt,
			head.HomeID,
			head.Path,
			update.BaseVersion,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrHomePathHeadConflict
		}
	}
	return appendMergeEvent(ctx, exec, normalized)
}

func headsByPath(heads []*models.HomePathHead) map[string]*models.HomePathHead {
	out := make(map[string]*models.HomePathHead, len(heads))
	for _, head := range heads {
		if head != nil {
			out[head.Path] = head
		}
	}
	return out
}

func (s *PostgresNativeStorage) GetMergeEventByChangeset(ctx context.Context, changesetID string) (*models.MergeEvent, error) {
	ctx = ensureCtx(ctx)
	return getMergeEventByChangeset(ctx, s.pool, changesetID)
}

func (s *postgresNativeTxView) GetMergeEventByChangeset(ctx context.Context, changesetID string) (*models.MergeEvent, error) {
	ctx = ensureCtx(ctx)
	return getMergeEventByChangeset(ctx, s.tx, changesetID)
}

func getMergeEventByChangeset(ctx context.Context, q queryable, changesetID string) (*models.MergeEvent, error) {
	changesetID = strings.TrimSpace(changesetID)
	if changesetID == "" {
		return nil, ErrInvalidInput
	}
	event, err := scanMergeEvent(q.QueryRow(ctx, `
		SELECT home_id, shard_id, merge_seq, event_id, changeset_id,
		       source_slice_id, source_commit_hash, author, message,
		       touched_paths, path_updates, forced, force_reason, forced_by, created_at
		FROM merge_events
		WHERE changeset_id = $1
	`, changesetID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMergeEventNotFound
		}
		return nil, err
	}
	return event, nil
}

func (s *PostgresNativeStorage) ListMergeEvents(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error) {
	ctx = ensureCtx(ctx)
	return listMergeEvents(ctx, s.pool, shardID, afterSeq, limit)
}

func (s *postgresNativeTxView) ListMergeEvents(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error) {
	ctx = ensureCtx(ctx)
	return listMergeEvents(ctx, s.tx, shardID, afterSeq, limit)
}

func listMergeEvents(ctx context.Context, q queryable, shardID int32, afterSeq int64, limit int) ([]*models.MergeEvent, error) {
	if shardID < 0 || afterSeq < 0 {
		return nil, ErrInvalidInput
	}
	limit = normalizeMergeEventListLimit(limit)
	rows, err := q.Query(ctx, `
		SELECT home_id, shard_id, merge_seq, event_id, changeset_id,
		       source_slice_id, source_commit_hash, author, message,
		       touched_paths, path_updates, forced, force_reason, forced_by, created_at
		FROM merge_events
		WHERE shard_id = $1 AND merge_seq > $2
		ORDER BY merge_seq ASC
		LIMIT $3
	`, shardID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]*models.MergeEvent, 0, limit)
	for rows.Next() {
		event, err := scanMergeEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *PostgresNativeStorage) UpdateProjectionOffset(ctx context.Context, offset *models.ProjectionOffset) error {
	ctx = ensureCtx(ctx)
	return updateProjectionOffset(ctx, s.pool, offset)
}

func (s *postgresNativeTxView) UpdateProjectionOffset(ctx context.Context, offset *models.ProjectionOffset) error {
	ctx = ensureCtx(ctx)
	return updateProjectionOffset(ctx, s.tx, offset)
}

func updateProjectionOffset(ctx context.Context, exec execable, offset *models.ProjectionOffset) error {
	normalized, err := normalizeProjectionOffset(offset)
	if err != nil {
		return err
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO projection_offsets (projection_name, shard_id, merge_seq, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (projection_name, shard_id) DO UPDATE
		SET merge_seq = GREATEST(projection_offsets.merge_seq, EXCLUDED.merge_seq),
		    updated_at = CASE
		        WHEN EXCLUDED.merge_seq >= projection_offsets.merge_seq THEN EXCLUDED.updated_at
		        ELSE projection_offsets.updated_at
		    END
	`, normalized.ProjectionName, normalized.ShardID, normalized.MergeSeq, normalized.UpdatedAt)
	return err
}

func (s *PostgresNativeStorage) GetProjectionOffset(ctx context.Context, projectionName string, shardID int32) (*models.ProjectionOffset, error) {
	ctx = ensureCtx(ctx)
	return getProjectionOffset(ctx, s.pool, projectionName, shardID)
}

func (s *postgresNativeTxView) GetProjectionOffset(ctx context.Context, projectionName string, shardID int32) (*models.ProjectionOffset, error) {
	ctx = ensureCtx(ctx)
	return getProjectionOffset(ctx, s.tx, projectionName, shardID)
}

func getProjectionOffset(ctx context.Context, q queryable, projectionName string, shardID int32) (*models.ProjectionOffset, error) {
	projectionName = strings.TrimSpace(projectionName)
	if projectionName == "" || shardID < 0 {
		return nil, ErrInvalidInput
	}
	var offset models.ProjectionOffset
	err := q.QueryRow(ctx, `
		SELECT projection_name, shard_id, merge_seq, updated_at
		FROM projection_offsets
		WHERE projection_name = $1 AND shard_id = $2
	`, projectionName, shardID).Scan(&offset.ProjectionName, &offset.ShardID, &offset.MergeSeq, &offset.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &models.ProjectionOffset{ProjectionName: projectionName, ShardID: shardID}, nil
		}
		return nil, err
	}
	return &offset, nil
}

func (s *PostgresNativeStorage) ProcessMergeEventProjectionBatch(ctx context.Context, projectionName string, shardCount int32, limit int, fn func(context.Context, []*models.MergeEvent) error) (bool, error) {
	ctx = ensureCtx(ctx)
	projectionName = normalizeProjectionName(projectionName)
	if projectionName == "" || shardCount <= 0 || fn == nil {
		return false, ErrInvalidInput
	}
	limit = normalizeMergeEventListLimit(limit)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO projection_offsets (projection_name, shard_id, merge_seq, updated_at)
		SELECT $1, shards.shard_id, 0, NOW()
		FROM generate_series(0, $2::int - 1) AS shards(shard_id)
		WHERE EXISTS (
		    SELECT 1
		    FROM merge_events e
		    WHERE e.shard_id = shards.shard_id
		)
		ON CONFLICT (projection_name, shard_id) DO NOTHING
	`, projectionName, shardCount); err != nil {
		return false, err
	}

	var shardID int32
	var afterSeq int64
	err = tx.QueryRow(ctx, `
		SELECT po.shard_id, po.merge_seq
		FROM projection_offsets po
		WHERE po.projection_name = $1
		  AND po.shard_id >= 0
		  AND po.shard_id < $2
		  AND EXISTS (
		      SELECT 1
		      FROM merge_events e
		      WHERE e.shard_id = po.shard_id
		        AND e.merge_seq > po.merge_seq
		  )
		ORDER BY po.updated_at ASC, po.shard_id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`, projectionName, shardCount).Scan(&shardID, &afterSeq)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
			return false, nil
		}
		return false, err
	}

	events, err := listMergeEvents(ctx, tx, shardID, afterSeq, limit)
	if err != nil {
		return false, err
	}
	if len(events) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}

	if err := fn(ctx, events); err != nil {
		return true, err
	}
	latestSeq := events[len(events)-1].MergeSeq
	if err := updateProjectionOffset(ctx, tx, &models.ProjectionOffset{
		ProjectionName: projectionName,
		ShardID:        shardID,
		MergeSeq:       latestSeq,
		UpdatedAt:      time.Now(),
	}); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, err
	}
	return true, nil
}

type mergeEventScanner interface {
	Scan(dest ...interface{}) error
}

func scanMergeEvent(row mergeEventScanner) (*models.MergeEvent, error) {
	var event models.MergeEvent
	var touchedPathsJSON []byte
	var pathUpdatesJSON []byte
	if err := row.Scan(
		&event.HomeID,
		&event.ShardID,
		&event.MergeSeq,
		&event.EventID,
		&event.ChangesetID,
		&event.SourceSliceID,
		&event.SourceCommitHash,
		&event.Author,
		&event.Message,
		&touchedPathsJSON,
		&pathUpdatesJSON,
		&event.Forced,
		&event.ForceReason,
		&event.ForcedBy,
		&event.CreatedAt,
	); err != nil {
		return nil, err
	}
	if len(touchedPathsJSON) > 0 {
		if err := json.Unmarshal(touchedPathsJSON, &event.TouchedPaths); err != nil {
			return nil, fmt.Errorf("unmarshal touched paths: %w", err)
		}
	}
	if len(pathUpdatesJSON) > 0 {
		if err := json.Unmarshal(pathUpdatesJSON, &event.PathUpdates); err != nil {
			return nil, fmt.Errorf("unmarshal path updates: %w", err)
		}
	}
	return &event, nil
}

func normalizeMergeEvent(event *models.MergeEvent) (*models.MergeEvent, error) {
	if event == nil ||
		strings.TrimSpace(event.HomeID) == "" ||
		event.ShardID < 0 ||
		event.MergeSeq <= 0 ||
		strings.TrimSpace(event.EventID) == "" ||
		strings.TrimSpace(event.ChangesetID) == "" ||
		strings.TrimSpace(event.SourceSliceID) == "" ||
		strings.TrimSpace(event.SourceCommitHash) == "" ||
		strings.TrimSpace(event.Author) == "" {
		return nil, ErrInvalidInput
	}
	normalized := *event
	normalized.HomeID = strings.TrimSpace(event.HomeID)
	normalized.EventID = strings.TrimSpace(event.EventID)
	normalized.ChangesetID = strings.TrimSpace(event.ChangesetID)
	normalized.SourceSliceID = strings.TrimSpace(event.SourceSliceID)
	normalized.SourceCommitHash = strings.TrimSpace(event.SourceCommitHash)
	normalized.Author = strings.TrimSpace(event.Author)
	normalized.ForceReason = strings.TrimSpace(event.ForceReason)
	normalized.ForcedBy = strings.TrimSpace(event.ForcedBy)
	normalized.TouchedPaths = append([]string(nil), event.TouchedPaths...)
	normalized.PathUpdates = cloneMergePathUpdates(event.PathUpdates)
	if normalized.TouchedPaths == nil {
		normalized.TouchedPaths = []string{}
	}
	if normalized.PathUpdates == nil {
		normalized.PathUpdates = []*models.MergePathUpdate{}
	}
	if normalized.CreatedAt.IsZero() {
		normalized.CreatedAt = time.Now()
	}
	return &normalized, nil
}

func normalizeProjectionOffset(offset *models.ProjectionOffset) (*models.ProjectionOffset, error) {
	if offset == nil ||
		normalizeProjectionName(offset.ProjectionName) == "" ||
		offset.ShardID < 0 ||
		offset.MergeSeq < 0 {
		return nil, ErrInvalidInput
	}
	normalized := *offset
	normalized.ProjectionName = normalizeProjectionName(offset.ProjectionName)
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = time.Now()
	}
	return &normalized, nil
}

func normalizeProjectionName(projectionName string) string {
	return strings.TrimSpace(projectionName)
}

func cloneMergeEvent(event *models.MergeEvent) *models.MergeEvent {
	if event == nil {
		return nil
	}
	clone := *event
	clone.TouchedPaths = append([]string(nil), event.TouchedPaths...)
	clone.PathUpdates = cloneMergePathUpdates(event.PathUpdates)
	return &clone
}

func cloneMergePathUpdates(updates []*models.MergePathUpdate) []*models.MergePathUpdate {
	if updates == nil {
		return nil
	}
	out := make([]*models.MergePathUpdate, 0, len(updates))
	for _, update := range updates {
		if update == nil {
			continue
		}
		clone := *update
		out = append(out, &clone)
	}
	return out
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint")
}
