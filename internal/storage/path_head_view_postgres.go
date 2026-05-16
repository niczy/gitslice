package storage

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/ids"
	"github.com/niczy/gitslice/internal/models"
)

func postgresPathHeadViewMode(ctx context.Context, q queryable, sliceID string) (homeID string, root bool, ok bool, err error) {
	sliceID = strings.TrimSpace(sliceID)
	if homeID, ok := homePathHeadHomeIDFromSliceID(sliceID); ok {
		return homeID, false, true, nil
	}
	if sliceID == ids.RootSliceID {
		return "", true, true, nil
	}
	if sliceID == "" {
		return "", false, false, nil
	}
	var isRoot bool
	if err := q.QueryRow(ctx, `SELECT is_root FROM slices WHERE id = $1`, sliceID).Scan(&isRoot); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, false, nil
		}
		return "", false, false, err
	}
	if isRoot {
		return "", true, true, nil
	}
	return "", false, false, nil
}

func (s *PostgresNativeStorage) getPathHeadFileManifest(ctx context.Context, q queryable, sliceID, filePath string) (*models.FileManifest, bool, error) {
	homeID, root, ok, err := postgresPathHeadViewMode(ctx, q, sliceID)
	if err != nil || !ok {
		return nil, ok, err
	}
	filePath = cleanRelativePath(filePath)
	if filePath == "" {
		return nil, true, ErrEntryNotFound
	}
	if root {
		homeID, ok = pathHeadViewHomeIDForRootPath(filePath)
		if !ok {
			return nil, true, ErrEntryNotFound
		}
	}

	var manifestHash string
	err = q.QueryRow(ctx, `
		SELECT manifest_hash
		FROM home_path_heads
		WHERE home_id = $1
		  AND path = $2
		  AND deleted = false
		  AND COALESCE(NULLIF(entry_type, ''), 'file') = 'file'
	`, homeID, filePath).Scan(&manifestHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, true, ErrEntryNotFound
		}
		return nil, true, err
	}
	manifestHash = strings.TrimSpace(manifestHash)
	if manifestHash == "" {
		return nil, true, ErrEntryNotFound
	}
	manifest, err := s.GetVersionedFileManifest(ctx, manifestHash)
	if err != nil {
		return nil, true, err
	}
	return cloneManifestForPath(manifest, filePath, manifestHash), true, nil
}

func (s *PostgresNativeStorage) getPathHeadEntry(ctx context.Context, q queryable, sliceID, filePath string) (*models.DirectoryEntry, bool, error) {
	homeID, root, ok, err := postgresPathHeadViewMode(ctx, q, sliceID)
	if err != nil || !ok {
		return nil, ok, err
	}
	filePath = cleanRelativePath(filePath)
	if filePath == "" {
		return pathHeadViewEntry(sliceID, "", homePathHeadEntryTypeDirectory, "", nil), true, nil
	}
	if root {
		homeID, ok = pathHeadViewHomeIDForRootPath(filePath)
		if !ok {
			return nil, true, ErrEntryNotFound
		}
	}

	var entryType, manifestHash string
	err = q.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(entry_type, ''), 'file'), manifest_hash
		FROM home_path_heads
		WHERE home_id = $1 AND path = $2 AND deleted = false
	`, homeID, filePath).Scan(&entryType, &manifestHash)
	if err == nil {
		entryType = normalizeHomePathHeadEntryType(entryType)
		if entryType == homePathHeadEntryTypeFile {
			manifest, manifestErr := s.GetVersionedFileManifest(ctx, strings.TrimSpace(manifestHash))
			if manifestErr != nil && !errors.Is(manifestErr, ErrEntryNotFound) {
				return nil, true, manifestErr
			}
			return pathHeadViewEntry(sliceID, filePath, entryType, manifestHash, manifest), true, nil
		}
		return pathHeadViewEntry(sliceID, filePath, entryType, "", nil), true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, true, err
	}

	var exists bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM path_head_children
			WHERE home_id = $1 AND dir_path = $2
		)
	`, homeID, filePath).Scan(&exists); err != nil {
		return nil, true, err
	}
	if exists {
		return pathHeadViewEntry(sliceID, filePath, homePathHeadEntryTypeDirectory, "", nil), true, nil
	}
	return nil, true, ErrEntryNotFound
}

func (s *PostgresNativeStorage) listPathHeadEntries(ctx context.Context, q queryable, sliceID, parentID string) ([]*models.DirectoryEntry, bool, bool, error) {
	homeID, root, ok, err := postgresPathHeadViewMode(ctx, q, sliceID)
	if err != nil || !ok {
		return nil, ok, false, err
	}
	parentPath, ok := pathHeadViewParentPath(sliceID, parentID)
	if !ok {
		return nil, true, false, nil
	}
	parentExists := false
	if root && parentPath != "" {
		pathHomeID, ok := pathHeadViewHomeIDForRootPath(parentPath)
		if !ok {
			return nil, true, false, nil
		}
		homeID = pathHomeID
	}
	if parentPath != "" {
		if err := q.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM path_head_children
				WHERE home_id = $1 AND dir_path = $2
			)
		`, homeID, parentPath).Scan(&parentExists); err != nil {
			return nil, true, false, err
		}
	}

	rows, err := q.Query(ctx, `
		SELECT child_path, entry_type, manifest_hash
		FROM path_head_children
		WHERE ($1::boolean OR home_id = $2)
		  AND dir_path = $3
		ORDER BY child_path
	`, root, homeID, parentPath)
	if err != nil {
		return nil, true, false, err
	}
	defer rows.Close()

	entries := make([]*models.DirectoryEntry, 0)
	for rows.Next() {
		var childPath, entryType, manifestHash string
		if err := rows.Scan(&childPath, &entryType, &manifestHash); err != nil {
			return nil, true, false, err
		}
		entryType = normalizeHomePathHeadEntryType(entryType)
		var manifest *models.FileManifest
		if entryType == homePathHeadEntryTypeFile && strings.TrimSpace(manifestHash) != "" {
			manifest, err = s.GetVersionedFileManifest(ctx, strings.TrimSpace(manifestHash))
			if err != nil && !errors.Is(err, ErrEntryNotFound) {
				return nil, true, false, err
			}
		}
		entries = append(entries, pathHeadViewEntry(sliceID, childPath, entryType, manifestHash, manifest))
	}
	if err := rows.Err(); err != nil {
		return nil, true, false, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, true, parentExists, nil
}
