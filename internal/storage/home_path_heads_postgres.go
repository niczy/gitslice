package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

func (s *PostgresNativeStorage) UpsertHomePathHeads(ctx context.Context, heads []*models.HomePathHead) error {
	ctx = ensureCtx(ctx)
	return upsertHomePathHeads(ctx, s.pool, heads)
}

func (s *postgresNativeTxView) UpsertHomePathHeads(ctx context.Context, heads []*models.HomePathHead) error {
	ctx = ensureCtx(ctx)
	return upsertHomePathHeads(ctx, s.tx, heads)
}

func upsertHomePathHeads(ctx context.Context, exec execable, heads []*models.HomePathHead) error {
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

	homeIDs := make([]string, 0, len(normalized))
	paths := make([]string, 0, len(normalized))
	pathVersions := make([]int64, 0, len(normalized))
	contentHashes := make([]string, 0, len(normalized))
	manifestHashes := make([]string, 0, len(normalized))
	sourceSliceIDs := make([]string, 0, len(normalized))
	sourceCommitHashes := make([]string, 0, len(normalized))
	lastMergeSeqs := make([]int64, 0, len(normalized))
	deleted := make([]bool, 0, len(normalized))
	updatedAts := make([]time.Time, 0, len(normalized))
	for _, head := range normalized {
		homeIDs = append(homeIDs, head.HomeID)
		paths = append(paths, head.Path)
		pathVersions = append(pathVersions, head.PathVersion)
		contentHashes = append(contentHashes, head.ContentHash)
		manifestHashes = append(manifestHashes, head.ManifestHash)
		sourceSliceIDs = append(sourceSliceIDs, head.SourceSliceID)
		sourceCommitHashes = append(sourceCommitHashes, head.SourceCommitHash)
		lastMergeSeqs = append(lastMergeSeqs, head.LastMergeSeq)
		deleted = append(deleted, head.Deleted)
		updatedAts = append(updatedAts, head.UpdatedAt)
	}

	_, err := exec.Exec(ctx, `
		INSERT INTO home_path_heads (
			home_id, path, path_version, content_hash, manifest_hash,
			source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
		)
		SELECT home_id, path, path_version, content_hash, manifest_hash,
		       source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
		FROM unnest(
			$1::text[], $2::text[], $3::bigint[], $4::text[], $5::text[],
			$6::text[], $7::text[], $8::bigint[], $9::boolean[], $10::timestamptz[]
		) AS rows(
			home_id, path, path_version, content_hash, manifest_hash,
			source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
		)
		ON CONFLICT (home_id, path) DO UPDATE
		SET path_version = EXCLUDED.path_version,
		    content_hash = EXCLUDED.content_hash,
		    manifest_hash = EXCLUDED.manifest_hash,
		    source_slice_id = EXCLUDED.source_slice_id,
		    source_commit_hash = EXCLUDED.source_commit_hash,
		    last_merge_seq = EXCLUDED.last_merge_seq,
		    deleted = EXCLUDED.deleted,
		    updated_at = EXCLUDED.updated_at
		WHERE EXCLUDED.last_merge_seq > home_path_heads.last_merge_seq
		   OR (
		       EXCLUDED.last_merge_seq = home_path_heads.last_merge_seq
		       AND EXCLUDED.path_version >= home_path_heads.path_version
		   )
	`, homeIDs, paths, pathVersions, contentHashes, manifestHashes, sourceSliceIDs, sourceCommitHashes, lastMergeSeqs, deleted, updatedAts)
	return err
}

func (s *PostgresNativeStorage) GetHomePathHeads(ctx context.Context, homeID string, paths []string) (map[string]*models.HomePathHead, error) {
	ctx = ensureCtx(ctx)
	return getHomePathHeads(ctx, s.pool, homeID, paths)
}

func (s *postgresNativeTxView) GetHomePathHeads(ctx context.Context, homeID string, paths []string) (map[string]*models.HomePathHead, error) {
	ctx = ensureCtx(ctx)
	return getHomePathHeads(ctx, s.tx, homeID, paths)
}

func getHomePathHeads(ctx context.Context, q queryable, homeID string, paths []string) (map[string]*models.HomePathHead, error) {
	homeID = normalizeHomePathHeadHomeID(homeID)
	cleanedPaths := normalizeHomePathHeadPaths(paths)
	if homeID == "" {
		return nil, ErrInvalidInput
	}
	result := make(map[string]*models.HomePathHead, len(cleanedPaths))
	if len(cleanedPaths) == 0 {
		return result, nil
	}

	rows, err := q.Query(ctx, `
		SELECT home_id, path, path_version, content_hash, manifest_hash,
		       source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
		FROM home_path_heads
		WHERE home_id = $1 AND path = ANY($2)
	`, homeID, cleanedPaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		head, err := scanHomePathHead(rows)
		if err != nil {
			return nil, err
		}
		result[head.Path] = head
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) ListHomePathHeads(ctx context.Context, homeID string, limit int) ([]*models.HomePathHead, error) {
	ctx = ensureCtx(ctx)
	return listHomePathHeads(ctx, s.pool, homeID, limit)
}

func (s *postgresNativeTxView) ListHomePathHeads(ctx context.Context, homeID string, limit int) ([]*models.HomePathHead, error) {
	ctx = ensureCtx(ctx)
	return listHomePathHeads(ctx, s.tx, homeID, limit)
}

func listHomePathHeads(ctx context.Context, q queryable, homeID string, limit int) ([]*models.HomePathHead, error) {
	homeID = normalizeHomePathHeadHomeID(homeID)
	if homeID == "" {
		return nil, ErrInvalidInput
	}
	limit = normalizeHomePathHeadListLimit(limit)

	rows, err := q.Query(ctx, `
		SELECT home_id, path, path_version, content_hash, manifest_hash,
		       source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
		FROM home_path_heads
		WHERE home_id = $1
		ORDER BY path ASC
		LIMIT $2
	`, homeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*models.HomePathHead, 0, limit)
	for rows.Next() {
		head, err := scanHomePathHead(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, head)
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) BackfillHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadBackfillResult, error) {
	ctx = ensureCtx(ctx)
	return backfillHomePathHeads(ctx, s, s, homeID)
}

func (s *postgresNativeTxView) BackfillHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadBackfillResult, error) {
	ctx = ensureCtx(ctx)
	return backfillHomePathHeads(ctx, s, s, homeID)
}

func (s *PostgresNativeStorage) ValidateHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadValidationResult, error) {
	ctx = ensureCtx(ctx)
	return validateHomePathHeads(ctx, s, s, homeID)
}

func (s *postgresNativeTxView) ValidateHomePathHeads(ctx context.Context, homeID string) (*models.HomePathHeadValidationResult, error) {
	ctx = ensureCtx(ctx)
	return validateHomePathHeads(ctx, s, s, homeID)
}

func scanHomePathHead(row interface {
	Scan(dest ...interface{}) error
}) (*models.HomePathHead, error) {
	var head models.HomePathHead
	err := row.Scan(
		&head.HomeID,
		&head.Path,
		&head.PathVersion,
		&head.ContentHash,
		&head.ManifestHash,
		&head.SourceSliceID,
		&head.SourceCommitHash,
		&head.LastMergeSeq,
		&head.Deleted,
		&head.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	head.HomeID = normalizeHomePathHeadHomeID(head.HomeID)
	head.Path = cleanRelativePath(head.Path)
	head.ContentHash = strings.TrimSpace(head.ContentHash)
	head.ManifestHash = strings.TrimSpace(head.ManifestHash)
	head.SourceSliceID = strings.TrimSpace(head.SourceSliceID)
	head.SourceCommitHash = strings.TrimSpace(head.SourceCommitHash)
	return &head, nil
}
