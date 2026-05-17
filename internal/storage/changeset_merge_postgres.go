package storage

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/ids"
	"github.com/niczy/gitslice/internal/models"
)

const postgresMergeEventShardCount = 1024

func (s *PostgresNativeStorage) AcceptChangesetMerge(ctx context.Context, req *AcceptChangesetMergeRequest) (*AcceptChangesetMergeResult, error) {
	ctx = ensureCtx(ctx)
	if req == nil || req.Changeset == nil || strings.TrimSpace(req.Changeset.ID) == "" {
		return nil, ErrInvalidInput
	}

	cs := req.Changeset
	paths := normalizeRelativePaths(req.ModifiedFiles)
	if len(paths) == 0 {
		return nil, ErrInvalidInput
	}
	homeID := strings.TrimSpace(req.HomeID)
	if homeID == "" {
		homeID = "global"
	}
	if req.ShardID < 0 || strings.TrimSpace(req.CommitHash) == "" {
		return nil, ErrInvalidInput
	}
	mergedAt := req.MergedAt
	if mergedAt.IsZero() {
		mergedAt = time.Now()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	snapshot, err := getLatestChangesetSnapshotForMerge(ctx, tx, cs.ID)
	if err != nil {
		return nil, err
	}
	if !changesetSnapshotCoversFastMerge(snapshot, paths) {
		return nil, ErrMergeFastPathUnsupported
	}

	parentHash, err := lockMergeSliceMetadata(ctx, tx, cs.SliceID)
	if err != nil {
		return nil, err
	}
	commitHash := strings.TrimSpace(req.CommitHash)
	pathUpdates, err := buildFastMergePathUpdates(cs, snapshot, paths, commitHash, parentHash)
	if err != nil {
		return nil, err
	}

	author := strings.TrimSpace(cs.Author)
	if author == "" && req.SourceSlice != nil {
		author = strings.TrimSpace(req.SourceSlice.CreatedBy)
		for _, owner := range req.SourceSlice.Owners {
			if author != "" {
				break
			}
			author = strings.TrimSpace(owner)
		}
	}
	if author == "" {
		author = "system"
	}

	event := &models.MergeEvent{
		HomeID:           homeID,
		ShardID:          req.ShardID,
		EventID:          ids.GenerateMergeEventID(),
		ChangesetID:      cs.ID,
		SourceSliceID:    cs.SliceID,
		SourceCommitHash: commitHash,
		Author:           author,
		Message:          cs.Message,
		TouchedPaths:     paths,
		PathUpdates:      pathUpdates,
		CreatedAt:        mergedAt,
	}
	if err := acceptFastChangesetMergeStatement(ctx, tx, cs, event); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	cs.ModifiedFiles = paths
	cs.Status = models.ChangesetStatusMerged
	cs.MergedAt = &mergedAt
	return &AcceptChangesetMergeResult{
		Changeset:   cs,
		SourceSlice: req.SourceSlice,
		Event:       event,
		ParentHash:  parentHash,
		CommitHash:  commitHash,
		MergedAt:    mergedAt,
	}, nil
}

func (s *PostgresNativeStorage) AcceptChangesetMergeByID(ctx context.Context, changesetID string, username string, commitHash string, mergedAt time.Time) (*AcceptChangesetMergeResult, error) {
	ctx = ensureCtx(ctx)
	changesetID = strings.TrimSpace(changesetID)
	username = strings.TrimSpace(username)
	commitHash = strings.TrimSpace(commitHash)
	if changesetID == "" || username == "" || commitHash == "" {
		return nil, ErrInvalidInput
	}
	if mergedAt.IsZero() {
		mergedAt = time.Now()
	}

	eventID := ids.GenerateMergeEventID()
	var found bool
	var authorized bool
	var basicSupported bool
	var supported bool
	var cs models.Changeset
	var sourceSlice models.Slice
	var statusValue int
	var visibility string
	var parentID string
	var ownersJSON []byte
	var modifiedJSON []byte
	var touchedPathsJSON []byte
	var pathUpdatesJSON []byte
	var parentHash string
	var homeID string
	var shardID int32
	var mergeSeq int64
	var appliedCount int64
	var updateCount int64
	var changesetRows int64
	var metadataRows int64
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		WITH input AS (
			SELECT
				$1::text AS changeset_id,
				$2::text AS username,
				$3::text AS commit_hash,
				$4::timestamptz AS merged_at,
				$5::text AS event_id,
				$6::int AS merged_status,
				$7::text AS revert_prefix,
				$8::int AS shard_count
		),
		loaded AS (
			SELECT
				i.*,
				c.id AS c_id,
				COALESCE(c.hash, '') AS c_hash,
				COALESCE(c.slice_id, '') AS c_slice_id,
				COALESCE(c.base_commit_hash, '') AS c_base_commit_hash,
				COALESCE(c.modified_files, '[]'::jsonb) AS c_modified_files,
				COALESCE(c.status, 0) AS c_status,
				COALESCE(c.author, '') AS c_author,
				COALESCE(c.message, '') AS c_message,
				COALESCE(c.created_at, i.merged_at) AS c_created_at,
				c.merged_at AS c_merged_at,
				COALESCE(s.id, '') AS s_id,
				COALESCE(s.name, '') AS s_name,
				COALESCE(s.slug, '') AS s_slug,
				COALESCE(s.description, '') AS s_description,
				COALESCE(s.created_by, '') AS s_created_by,
				COALESCE(s.parent_id, '') AS s_parent_id,
				COALESCE(s.is_root, false) AS s_is_root,
				COALESCE(s.visibility, '') AS s_visibility,
				COALESCE(s.owners, '[]'::jsonb) AS s_owners,
				COALESCE(s.created_at, i.merged_at) AS s_created_at,
				COALESCE(s.updated_at, i.merged_at) AS s_updated_at,
				COALESCE(s.environment, '') AS s_environment,
				COALESCE(sm.head_commit_hash, '') AS parent_hash,
				COALESCE(snap.id, '') AS snapshot_id,
				COALESCE(snap.file_hashes, '{}'::jsonb) AS file_hashes,
				COALESCE(snap.base_path_versions, '{}'::jsonb) AS base_path_versions,
				COALESCE(snap.rename_sources, '{}'::jsonb) AS rename_sources,
				COALESCE(snap.directory_moves, '[]'::jsonb) AS directory_moves
			FROM input i
			LEFT JOIN changesets c ON c.id = i.changeset_id
			LEFT JOIN slices s ON s.id = c.slice_id
			LEFT JOIN slice_metadata sm ON sm.slice_id = s.id
			LEFT JOIN LATERAL (
				SELECT id, file_hashes, base_path_versions, rename_sources, directory_moves
				FROM changeset_snapshots
				WHERE changeset_id = c.id
				ORDER BY version DESC
				LIMIT 1
			) snap ON true
		),
		paths AS (
			SELECT DISTINCT trim(both '/' FROM value) AS path
			FROM loaded l
			CROSS JOIN LATERAL jsonb_array_elements_text(l.c_modified_files) AS raw(value)
			WHERE trim(both '/' FROM value) <> ''
		),
		path_stats AS (
			SELECT
				COUNT(*)::bigint AS path_count,
				COUNT(DISTINCT NULLIF(split_part(path, '/', 1), ''))::bigint AS distinct_roots,
				MIN(NULLIF(split_part(path, '/', 1), '')) AS common_root,
				COALESCE(bool_or(path = '.gitslice/config.yaml' OR path LIKE '%/.gitslice/config.yaml'), false) AS touches_config
			FROM paths
		),
		home_prepared AS (
			SELECT
				l.*,
				ps.path_count,
				(l.c_id IS NOT NULL) AS found,
				(l.s_is_root OR l.s_created_by = l.username OR l.s_owners ? l.username) AS authorized,
				CASE
					WHEN l.s_id LIKE 'home\_%' ESCAPE '\' THEN NULLIF(substr(l.s_id, 6), '')
					WHEN ps.distinct_roots = 1 THEN ps.common_root
					WHEN NULLIF(trim(l.s_created_by), '') IS NOT NULL THEN trim(l.s_created_by)
					WHEN jsonb_array_length(l.s_owners) > 0 THEN l.s_owners ->> 0
					WHEN NULLIF(trim(l.c_slice_id), '') IS NOT NULL THEN trim(l.c_slice_id)
					ELSE 'global'
				END AS home_id,
				(
					l.c_id IS NOT NULL
					AND (l.s_is_root OR l.s_created_by = l.username OR l.s_owners ? l.username)
					AND ps.path_count > 0
					AND l.snapshot_id <> ''
					AND jsonb_object_length(l.rename_sources) = 0
					AND jsonb_array_length(l.directory_moves) = 0
					AND l.c_hash NOT LIKE l.revert_prefix || '%'
					AND NOT ps.touches_config
				) AS basic_supported
			FROM loaded l
			CROSS JOIN path_stats ps
		),
		prepared AS (
			SELECT
				hp.*,
				mod(abs(hashtextextended(COALESCE(NULLIF(hp.home_id, ''), 'global'), 0)), hp.shard_count::bigint)::int AS shard_id
			FROM home_prepared hp
		),
		updates AS (
			SELECT
				p.path,
				(prepared.base_path_versions ->> p.path)::bigint AS base_version,
				((prepared.base_path_versions ->> p.path)::bigint + 1) AS new_version,
				COALESCE(prepared.file_hashes ->> p.path, '') AS manifest_hash
			FROM prepared
			JOIN paths p ON prepared.basic_supported
			WHERE prepared.base_path_versions ? p.path
		),
		update_stats AS (
			SELECT
				COUNT(*)::bigint AS update_count,
				COALESCE(SUM(CASE WHEN base_version < 0 THEN 1 ELSE 0 END), 0)::bigint AS bad_base_count
			FROM updates
		),
		ready AS (
			SELECT
				prepared.*,
				(
					prepared.basic_supported
					AND us.update_count = prepared.path_count
					AND us.bad_base_count = 0
				) AS supported
			FROM prepared
			CROSS JOIN update_stats us
		),
		path_payload AS (
			SELECT
				COALESCE(jsonb_agg(u.path ORDER BY u.path) FILTER (WHERE u.path IS NOT NULL), '[]'::jsonb) AS touched_paths,
				COALESCE(jsonb_agg(
					jsonb_build_object(
						'path', u.path,
						'base_version', u.base_version,
						'new_version', u.new_version,
						'content_hash', u.manifest_hash,
						'manifest_hash', u.manifest_hash,
						'source_slice_id', r.c_slice_id,
						'source_commit_hash', r.commit_hash,
						'parent_commit_hash', r.parent_hash
					) || CASE WHEN u.manifest_hash = '' THEN '{"deleted":true}'::jsonb ELSE '{}'::jsonb END
					ORDER BY u.path
				) FILTER (WHERE u.path IS NOT NULL), '[]'::jsonb) AS path_updates
			FROM ready r
			LEFT JOIN updates u ON r.supported
			GROUP BY r.c_slice_id, r.commit_hash, r.parent_hash
		),
		seq AS (
			INSERT INTO merge_event_shard_sequences (shard_id, next_seq)
			SELECT r.shard_id, 2
			FROM ready r
			WHERE r.supported
			ON CONFLICT (shard_id) DO UPDATE
			SET next_seq = merge_event_shard_sequences.next_seq + 1
			RETURNING next_seq - 1 AS merge_seq
		),
		ins_heads AS (
			INSERT INTO home_path_heads (
				home_id, path, entry_type, path_version, content_hash, manifest_hash,
				source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
			)
			SELECT r.home_id, u.path, 'file', u.new_version, u.manifest_hash, u.manifest_hash,
			       r.c_slice_id, r.commit_hash, seq.merge_seq, u.manifest_hash = '', r.merged_at
			FROM ready r
			CROSS JOIN seq
			JOIN updates u ON r.supported
			WHERE u.base_version = 0
			ON CONFLICT (home_id, path) DO NOTHING
			RETURNING path
		),
		upd_heads AS (
			UPDATE home_path_heads h
			SET entry_type = 'file',
			    path_version = u.new_version,
			    content_hash = u.manifest_hash,
			    manifest_hash = u.manifest_hash,
			    source_slice_id = r.c_slice_id,
			    source_commit_hash = r.commit_hash,
			    last_merge_seq = seq.merge_seq,
			    deleted = u.manifest_hash = '',
			    updated_at = r.merged_at
			FROM ready r
			CROSS JOIN seq
			JOIN updates u ON r.supported
			WHERE u.base_version > 0
			  AND h.home_id = r.home_id
			  AND h.path = u.path
			  AND h.path_version = u.base_version
			RETURNING h.path
		),
		applied AS (
			SELECT path FROM ins_heads
			UNION ALL
			SELECT path FROM upd_heads
		),
		counts AS (
			SELECT
				(SELECT COUNT(*) FROM applied)::bigint AS applied_count,
				(SELECT COUNT(*) FROM updates)::bigint AS update_count
		),
		event_insert AS (
			INSERT INTO merge_events (
				home_id, shard_id, merge_seq, event_id, changeset_id,
				source_slice_id, source_commit_hash, author, message,
				touched_paths, path_updates, created_at
			)
			SELECT r.home_id, r.shard_id, seq.merge_seq, r.event_id, r.c_id,
			       r.c_slice_id, r.commit_hash, COALESCE(NULLIF(r.c_author, ''), r.username),
			       r.c_message, pp.touched_paths, pp.path_updates, r.merged_at
			FROM ready r
			CROSS JOIN seq
			CROSS JOIN counts c
			CROSS JOIN path_payload pp
			WHERE r.supported
			  AND c.applied_count = c.update_count
			  AND c.update_count = r.path_count
			RETURNING merge_seq
		),
		content_commit_dir_rows AS (
			SELECT DISTINCT
				r.home_id,
				array_to_string((parts.path_parts)[1:g.idx], '/') AS dir_path,
				r.commit_hash,
				r.c_slice_id AS source_slice_id,
				r.parent_hash,
				r.c_message,
				COALESCE(NULLIF(r.c_author, ''), r.username) AS author,
				r.merged_at,
				seq.merge_seq
			FROM ready r
			CROSS JOIN seq
			CROSS JOIN counts c
			JOIN updates u ON r.supported
			CROSS JOIN LATERAL (SELECT string_to_array(u.path, '/') AS path_parts) AS parts
			CROSS JOIN LATERAL generate_series(1, array_length(parts.path_parts, 1)) AS g(idx)
			WHERE EXISTS (SELECT 1 FROM event_insert)
			  AND c.applied_count = c.update_count
			  AND c.update_count = r.path_count
		),
		content_commit_insert AS (
			INSERT INTO content_commit_dirs (
				home_id, dir_path, commit_hash, source_slice_id, parent_hash,
				message, author, committed_at, merge_seq
			)
			SELECT home_id, dir_path, commit_hash, source_slice_id, parent_hash,
			       c_message, author, merged_at, merge_seq
			FROM content_commit_dir_rows
			WHERE dir_path <> ''
			ON CONFLICT (home_id, dir_path, commit_hash) DO UPDATE SET
				source_slice_id = EXCLUDED.source_slice_id,
				parent_hash = EXCLUDED.parent_hash,
				message = EXCLUDED.message,
				author = EXCLUDED.author,
				committed_at = EXCLUDED.committed_at,
				merge_seq = EXCLUDED.merge_seq
			RETURNING 1
		),
		changeset_update AS (
			UPDATE changesets c
			SET modified_files = pp.touched_paths,
			    status = r.merged_status,
			    author = COALESCE(NULLIF(r.c_author, ''), r.username),
			    message = r.c_message,
			    merged_at = r.merged_at
			FROM ready r
			CROSS JOIN path_payload pp
			WHERE c.id = r.c_id
			  AND EXISTS (SELECT 1 FROM event_insert)
			RETURNING c.id
		),
		metadata_update AS (
			UPDATE slice_metadata sm
			SET head_commit_hash = r.commit_hash,
			    modified_files = pp.touched_paths,
			    last_modified = r.merged_at,
			    modified_files_count = r.path_count
			FROM ready r
			CROSS JOIN path_payload pp
			WHERE sm.slice_id = r.c_slice_id
			  AND EXISTS (SELECT 1 FROM event_insert)
			RETURNING sm.slice_id
		)
		SELECT
			r.found,
			r.authorized,
			r.basic_supported,
			r.supported,
			r.c_id,
			r.c_hash,
			r.c_slice_id,
			r.c_base_commit_hash,
			r.c_modified_files,
			r.c_status,
			COALESCE(NULLIF(r.c_author, ''), r.username),
			r.c_message,
			r.c_created_at,
			r.c_merged_at,
			r.s_id,
			r.s_name,
			r.s_slug,
			r.s_description,
			r.s_created_by,
			r.s_parent_id,
			r.s_is_root,
			r.s_visibility,
			r.s_owners,
			r.s_created_at,
			r.s_updated_at,
			r.s_environment,
			r.parent_hash,
			COALESCE(NULLIF(r.home_id, ''), 'global'),
			r.shard_id,
			pp.touched_paths,
			pp.path_updates,
			COALESCE((SELECT merge_seq FROM event_insert), 0),
			(SELECT applied_count FROM counts),
			(SELECT update_count FROM counts),
			(SELECT COUNT(*) FROM changeset_update),
			(SELECT COUNT(*) FROM metadata_update)
		FROM ready r
		CROSS JOIN path_payload pp
	`, changesetID, username, commitHash, mergedAt, eventID, int(models.ChangesetStatusMerged), "chgver_revert~", postgresMergeEventShardCount).Scan(
		&found,
		&authorized,
		&basicSupported,
		&supported,
		&cs.ID,
		&cs.Hash,
		&cs.SliceID,
		&cs.BaseCommitHash,
		&modifiedJSON,
		&statusValue,
		&cs.Author,
		&cs.Message,
		&cs.CreatedAt,
		&cs.MergedAt,
		&sourceSlice.ID,
		&sourceSlice.Name,
		&sourceSlice.Slug,
		&sourceSlice.Description,
		&sourceSlice.CreatedBy,
		&parentID,
		&sourceSlice.IsRoot,
		&visibility,
		&ownersJSON,
		&sourceSlice.CreatedAt,
		&sourceSlice.UpdatedAt,
		&sourceSlice.Environment,
		&parentHash,
		&homeID,
		&shardID,
		&touchedPathsJSON,
		&pathUpdatesJSON,
		&mergeSeq,
		&appliedCount,
		&updateCount,
		&changesetRows,
		&metadataRows,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrChangesetNotFound
		}
		if isUniqueViolation(err) {
			return nil, ErrMergeEventConflict
		}
		return nil, err
	}
	if !found {
		return nil, ErrChangesetNotFound
	}
	if !authorized {
		return nil, ErrPermissionDenied
	}
	if !basicSupported || !supported {
		return nil, ErrMergeFastPathUnsupported
	}
	if mergeSeq <= 0 || appliedCount != updateCount {
		return nil, ErrHomePathHeadConflict
	}
	if changesetRows != 1 {
		return nil, ErrChangesetNotFound
	}
	if metadataRows != 1 {
		return nil, ErrSliceNotFound
	}
	cs.Status = models.ChangesetStatus(statusValue)
	if err := json.Unmarshal(modifiedJSON, &cs.ModifiedFiles); err != nil {
		cs.ModifiedFiles = []string{}
	}
	cs.Status = models.ChangesetStatusMerged
	cs.MergedAt = &mergedAt
	sourceSlice.ParentSlice = parentID
	sourceSlice.Visibility = models.NormalizeVisibility(models.Visibility(visibility))
	if err := json.Unmarshal(ownersJSON, &sourceSlice.Owners); err != nil {
		sourceSlice.Owners = []string{}
	}
	paths := []string{}
	if err := json.Unmarshal(touchedPathsJSON, &paths); err != nil {
		paths = []string{}
	}
	pathUpdates := []*models.MergePathUpdate{}
	if err := json.Unmarshal(pathUpdatesJSON, &pathUpdates); err != nil {
		pathUpdates = []*models.MergePathUpdate{}
	}
	event := &models.MergeEvent{
		HomeID:           homeID,
		ShardID:          shardID,
		MergeSeq:         mergeSeq,
		EventID:          eventID,
		ChangesetID:      cs.ID,
		SourceSliceID:    cs.SliceID,
		SourceCommitHash: commitHash,
		Author:           cs.Author,
		Message:          cs.Message,
		TouchedPaths:     paths,
		PathUpdates:      pathUpdates,
		CreatedAt:        mergedAt,
	}
	heads, err := homePathHeadsFromMergeEvent(event)
	if err != nil {
		return nil, err
	}
	if err := refreshPathHeadChildren(ctx, tx, heads); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &AcceptChangesetMergeResult{
		Changeset:   &cs,
		SourceSlice: &sourceSlice,
		Event:       event,
		ParentHash:  parentHash,
		CommitHash:  commitHash,
		MergedAt:    mergedAt,
	}, nil
}

func loadFastMergeInputsForUpdate(ctx context.Context, q queryable, changesetID string) (*models.Changeset, *models.Slice, string, error) {
	var cs models.Changeset
	var sl models.Slice
	var modifiedJSON []byte
	var status int
	var ownersJSON []byte
	var parentID *string
	var visibility string
	var parentHash string
	err := q.QueryRow(ctx, `
		SELECT c.id, c.hash, c.slice_id, c.base_commit_hash, c.modified_files,
		       c.status, c.author, c.message, c.created_at, c.merged_at,
		       s.id, s.name, s.slug, s.description, s.created_by,
		       COALESCE(s.parent_id, ''), s.is_root, s.visibility, s.owners,
		       s.created_at, s.updated_at, s.environment,
		       sm.head_commit_hash
		FROM changesets c
		JOIN slices s ON s.id = c.slice_id
		JOIN slice_metadata sm ON sm.slice_id = s.id
		WHERE c.id = $1
		FOR UPDATE OF c, sm
	`, changesetID).Scan(
		&cs.ID, &cs.Hash, &cs.SliceID, &cs.BaseCommitHash, &modifiedJSON,
		&status, &cs.Author, &cs.Message, &cs.CreatedAt, &cs.MergedAt,
		&sl.ID, &sl.Name, &sl.Slug, &sl.Description, &sl.CreatedBy,
		&parentID, &sl.IsRoot, &visibility, &ownersJSON,
		&sl.CreatedAt, &sl.UpdatedAt, &sl.Environment,
		&parentHash,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, "", ErrChangesetNotFound
		}
		return nil, nil, "", err
	}
	cs.Status = models.ChangesetStatus(status)
	if err := json.Unmarshal(modifiedJSON, &cs.ModifiedFiles); err != nil {
		cs.ModifiedFiles = []string{}
	}
	if parentID != nil {
		sl.ParentSlice = *parentID
	}
	sl.Visibility = models.NormalizeVisibility(models.Visibility(visibility))
	if err := json.Unmarshal(ownersJSON, &sl.Owners); err != nil {
		sl.Owners = []string{}
	}
	return &cs, &sl, strings.TrimSpace(parentHash), nil
}

func getLatestChangesetSnapshotForMerge(ctx context.Context, q queryable, changesetID string) (*models.ChangesetSnapshot, error) {
	var snapshot models.ChangesetSnapshot
	var modifiedJSON []byte
	var fileHashesJSON []byte
	var basePathVersionsJSON []byte
	var renameSourcesJSON []byte
	var directoryMovesJSON []byte
	err := q.QueryRow(ctx, `
		SELECT id, changeset_id, version, hash, base_commit_hash, modified_files,
		       COALESCE(file_hashes, 'null'::jsonb), COALESCE(base_path_versions, 'null'::jsonb),
		       COALESCE(rename_sources, 'null'::jsonb), COALESCE(directory_moves, 'null'::jsonb), author, message, created_at
		FROM changeset_snapshots
		WHERE changeset_id = $1
		ORDER BY version DESC
		LIMIT 1
	`, strings.TrimSpace(changesetID)).Scan(
		&snapshot.ID,
		&snapshot.ChangesetID,
		&snapshot.Version,
		&snapshot.Hash,
		&snapshot.BaseCommitHash,
		&modifiedJSON,
		&fileHashesJSON,
		&basePathVersionsJSON,
		&renameSourcesJSON,
		&directoryMovesJSON,
		&snapshot.Author,
		&snapshot.Message,
		&snapshot.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMergeFastPathUnsupported
		}
		return nil, err
	}
	if err := json.Unmarshal(modifiedJSON, &snapshot.ModifiedFiles); err != nil {
		snapshot.ModifiedFiles = []string{}
	}
	if err := json.Unmarshal(fileHashesJSON, &snapshot.FileHashes); err != nil {
		snapshot.FileHashes = map[string]string{}
	}
	if err := json.Unmarshal(basePathVersionsJSON, &snapshot.BasePathVersions); err != nil {
		snapshot.BasePathVersions = map[string]int64{}
	}
	if err := json.Unmarshal(renameSourcesJSON, &snapshot.RenameSources); err != nil {
		snapshot.RenameSources = map[string]string{}
	}
	if err := json.Unmarshal(directoryMovesJSON, &snapshot.DirectoryMoves); err != nil {
		snapshot.DirectoryMoves = nil
	}
	return &snapshot, nil
}

func changesetSnapshotCoversFastMerge(snapshot *models.ChangesetSnapshot, paths []string) bool {
	if snapshot == nil || len(paths) == 0 || len(snapshot.BasePathVersions) == 0 {
		return false
	}
	if len(snapshot.RenameSources) > 0 {
		return false
	}
	if len(snapshot.DirectoryMoves) > 0 {
		return false
	}
	for _, path := range paths {
		if _, ok := snapshot.BasePathVersions[path]; !ok {
			return false
		}
	}
	return true
}

func lockMergeSliceMetadata(ctx context.Context, q queryable, sliceID string) (string, error) {
	var parentHash string
	if err := q.QueryRow(ctx, `
		SELECT head_commit_hash
		FROM slice_metadata
		WHERE slice_id = $1
		FOR UPDATE
	`, strings.TrimSpace(sliceID)).Scan(&parentHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrSliceNotFound
		}
		return "", err
	}
	return strings.TrimSpace(parentHash), nil
}

func buildFastMergePathUpdates(cs *models.Changeset, snapshot *models.ChangesetSnapshot, paths []string, commitHash, parentHash string) ([]*models.MergePathUpdate, error) {
	if cs == nil || snapshot == nil {
		return nil, ErrInvalidInput
	}
	updates := make([]*models.MergePathUpdate, 0, len(paths))
	for _, path := range paths {
		baseVersion, ok := snapshot.BasePathVersions[path]
		if !ok || baseVersion < 0 {
			return nil, ErrMergeFastPathUnsupported
		}
		manifestHash := strings.TrimSpace(snapshot.FileHashes[path])
		updates = append(updates, &models.MergePathUpdate{
			Path:             path,
			EntryType:        "file",
			BaseVersion:      baseVersion,
			NewVersion:       baseVersion + 1,
			ContentHash:      manifestHash,
			ManifestHash:     manifestHash,
			SourceSliceID:    cs.SliceID,
			SourceCommitHash: strings.TrimSpace(commitHash),
			ParentCommitHash: strings.TrimSpace(parentHash),
			Deleted:          manifestHash == "",
		})
	}
	return updates, nil
}

func acceptFastChangesetMergeStatement(ctx context.Context, q interface {
	queryable
	execable
}, cs *models.Changeset, event *models.MergeEvent) error {
	if cs == nil || event == nil || len(event.PathUpdates) == 0 {
		return ErrInvalidInput
	}

	normalizable := *event
	normalizable.MergeSeq = 1
	normalized, err := normalizeMergeEvent(&normalizable)
	if err != nil {
		return err
	}
	normalized.MergeSeq = 0
	normalized.TouchedPaths = normalizeRelativePaths(normalized.TouchedPaths)
	if len(normalized.TouchedPaths) == 0 || len(normalized.PathUpdates) != len(normalized.TouchedPaths) {
		return ErrInvalidInput
	}

	paths := make([]string, 0, len(normalized.PathUpdates))
	baseVersions := make([]int64, 0, len(normalized.PathUpdates))
	newVersions := make([]int64, 0, len(normalized.PathUpdates))
	contentHashes := make([]string, 0, len(normalized.PathUpdates))
	manifestHashes := make([]string, 0, len(normalized.PathUpdates))
	deleted := make([]bool, 0, len(normalized.PathUpdates))
	for _, update := range normalized.PathUpdates {
		if update == nil {
			return ErrInvalidInput
		}
		path := cleanRelativePath(update.Path)
		if path == "" || update.BaseVersion < 0 || update.NewVersion <= update.BaseVersion {
			return ErrInvalidInput
		}
		update.Path = path
		paths = append(paths, path)
		baseVersions = append(baseVersions, update.BaseVersion)
		newVersions = append(newVersions, update.NewVersion)
		contentHashes = append(contentHashes, strings.TrimSpace(update.ContentHash))
		manifestHashes = append(manifestHashes, strings.TrimSpace(update.ManifestHash))
		deleted = append(deleted, update.Deleted)
	}

	touchedPathsJSON, err := json.Marshal(normalized.TouchedPaths)
	if err != nil {
		return err
	}
	pathUpdatesJSON, err := json.Marshal(normalized.PathUpdates)
	if err != nil {
		return err
	}

	var mergeSeq int64
	var appliedCount int64
	var updateCount int64
	var changesetRows int64
	var metadataRows int64
	err = q.QueryRow(ctx, `
		WITH seq AS (
			INSERT INTO merge_event_shard_sequences (shard_id, next_seq)
			VALUES ($1, 2)
			ON CONFLICT (shard_id) DO UPDATE
			SET next_seq = merge_event_shard_sequences.next_seq + 1
			RETURNING next_seq - 1 AS merge_seq
		),
		updates AS (
			SELECT path, base_version, new_version, content_hash, manifest_hash, deleted
			FROM unnest($2::text[], $3::bigint[], $4::bigint[], $5::text[], $6::text[], $7::boolean[])
				AS u(path, base_version, new_version, content_hash, manifest_hash, deleted)
		),
		ins_heads AS (
			INSERT INTO home_path_heads (
				home_id, path, entry_type, path_version, content_hash, manifest_hash,
				source_slice_id, source_commit_hash, last_merge_seq, deleted, updated_at
			)
			SELECT $8, u.path, 'file', u.new_version, u.content_hash, u.manifest_hash,
			       $11, $12, seq.merge_seq, u.deleted, $17
			FROM updates u
			CROSS JOIN seq
			WHERE u.base_version = 0
			ON CONFLICT (home_id, path) DO NOTHING
			RETURNING path
		),
		upd_heads AS (
			UPDATE home_path_heads h
			SET entry_type = 'file',
			    path_version = u.new_version,
			    content_hash = u.content_hash,
			    manifest_hash = u.manifest_hash,
			    source_slice_id = $11,
			    source_commit_hash = $12,
			    last_merge_seq = seq.merge_seq,
			    deleted = u.deleted,
			    updated_at = $17
			FROM updates u
			CROSS JOIN seq
			WHERE u.base_version > 0
			  AND h.home_id = $8
			  AND h.path = u.path
			  AND h.path_version = u.base_version
			RETURNING h.path
		),
		applied AS (
			SELECT path FROM ins_heads
			UNION ALL
			SELECT path FROM upd_heads
		),
		counts AS (
			SELECT
				(SELECT COUNT(*) FROM applied) AS applied_count,
				(SELECT COUNT(*) FROM updates) AS update_count
		),
		event_insert AS (
			INSERT INTO merge_events (
				home_id, shard_id, merge_seq, event_id, changeset_id,
				source_slice_id, source_commit_hash, author, message,
				touched_paths, path_updates, created_at
			)
			SELECT $8, $1, seq.merge_seq, $9, $10,
			       $11, $12, $13, $14, $15::jsonb, $16::jsonb, $17
			FROM seq
			CROSS JOIN counts
			WHERE counts.applied_count = counts.update_count
			RETURNING merge_seq
		),
		content_commit_dir_rows AS (
			SELECT DISTINCT
				$8 AS home_id,
				array_to_string((parts.path_parts)[1:g.idx], '/') AS dir_path,
				$12 AS commit_hash,
				$11 AS source_slice_id,
				$20 AS parent_hash,
				$14 AS message,
				$13 AS author,
				$17 AS committed_at,
				seq.merge_seq
			FROM updates u
			CROSS JOIN seq
			CROSS JOIN counts
			CROSS JOIN LATERAL (SELECT string_to_array(u.path, '/') AS path_parts) AS parts
			CROSS JOIN LATERAL generate_series(1, array_length(parts.path_parts, 1)) AS g(idx)
			WHERE EXISTS (SELECT 1 FROM event_insert)
			  AND counts.applied_count = counts.update_count
		),
		content_commit_insert AS (
			INSERT INTO content_commit_dirs (
				home_id, dir_path, commit_hash, source_slice_id, parent_hash,
				message, author, committed_at, merge_seq
			)
			SELECT home_id, dir_path, commit_hash, source_slice_id, parent_hash,
			       message, author, committed_at, merge_seq
			FROM content_commit_dir_rows
			WHERE dir_path <> ''
			ON CONFLICT (home_id, dir_path, commit_hash) DO UPDATE SET
				source_slice_id = EXCLUDED.source_slice_id,
				parent_hash = EXCLUDED.parent_hash,
				message = EXCLUDED.message,
				author = EXCLUDED.author,
				committed_at = EXCLUDED.committed_at,
				merge_seq = EXCLUDED.merge_seq
			RETURNING 1
		),
		changeset_update AS (
			UPDATE changesets
			SET modified_files = $15::jsonb,
			    status = $18,
			    author = $13,
			    message = $14,
			    merged_at = $17
			WHERE id = $10
			  AND EXISTS (SELECT 1 FROM event_insert)
			RETURNING id
		),
		metadata_update AS (
			UPDATE slice_metadata
			SET head_commit_hash = $12,
			    modified_files = $15::jsonb,
			    last_modified = $17,
			    modified_files_count = $19
			WHERE slice_id = $11
			  AND EXISTS (SELECT 1 FROM event_insert)
			RETURNING slice_id
		)
		SELECT
			COALESCE((SELECT merge_seq FROM event_insert), 0),
			(SELECT applied_count FROM counts),
			(SELECT update_count FROM counts),
			(SELECT COUNT(*) FROM changeset_update),
			(SELECT COUNT(*) FROM metadata_update)
	`,
		normalized.ShardID,
		paths,
		baseVersions,
		newVersions,
		contentHashes,
		manifestHashes,
		deleted,
		normalized.HomeID,
		normalized.EventID,
		normalized.ChangesetID,
		normalized.SourceSliceID,
		normalized.SourceCommitHash,
		normalized.Author,
		normalized.Message,
		touchedPathsJSON,
		pathUpdatesJSON,
		normalized.CreatedAt,
		int(models.ChangesetStatusMerged),
		len(normalized.TouchedPaths),
		firstMergeEventParentHash(normalized),
	).Scan(&mergeSeq, &appliedCount, &updateCount, &changesetRows, &metadataRows)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrMergeEventConflict
		}
		return err
	}
	if mergeSeq <= 0 || appliedCount != updateCount {
		return ErrHomePathHeadConflict
	}
	if changesetRows != 1 {
		return ErrChangesetNotFound
	}
	if metadataRows != 1 {
		return ErrSliceNotFound
	}

	normalized.MergeSeq = mergeSeq
	heads, err := homePathHeadsFromMergeEvent(normalized)
	if err != nil {
		return err
	}
	if err := refreshPathHeadChildren(ctx, q, heads); err != nil {
		return err
	}

	event.HomeID = normalized.HomeID
	event.ShardID = normalized.ShardID
	event.MergeSeq = mergeSeq
	event.EventID = normalized.EventID
	event.ChangesetID = normalized.ChangesetID
	event.SourceSliceID = normalized.SourceSliceID
	event.SourceCommitHash = normalized.SourceCommitHash
	event.Author = normalized.Author
	event.Message = normalized.Message
	event.TouchedPaths = normalized.TouchedPaths
	event.PathUpdates = normalized.PathUpdates
	event.CreatedAt = normalized.CreatedAt
	return nil
}

func markChangesetMergedFast(ctx context.Context, exec execable, cs *models.Changeset, paths []string, mergedAt time.Time) error {
	modifiedJSON, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	tag, err := exec.Exec(ctx, `
		UPDATE changesets
		SET modified_files = $1,
		    status = $2,
		    author = $3,
		    message = $4,
		    merged_at = $5
		WHERE id = $6
	`, modifiedJSON, int(models.ChangesetStatusMerged), cs.Author, cs.Message, mergedAt, cs.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrChangesetNotFound
	}
	return nil
}

func updateSliceMetadataFast(ctx context.Context, exec execable, sliceID, headCommit string, paths []string, mergedAt time.Time) error {
	modifiedJSON, err := json.Marshal(paths)
	if err != nil {
		return err
	}
	tag, err := exec.Exec(ctx, `
		UPDATE slice_metadata
		SET head_commit_hash = $1,
		    modified_files = $2,
		    last_modified = $3,
		    modified_files_count = $4
		WHERE slice_id = $5
	`, strings.TrimSpace(headCommit), modifiedJSON, mergedAt, len(paths), strings.TrimSpace(sliceID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	return nil
}

func fastMergeSliceViewAccess(slice *models.Slice, username string) bool {
	if slice == nil {
		return false
	}
	if slice.IsRoot {
		return true
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	if strings.TrimSpace(slice.CreatedBy) == username {
		return true
	}
	for _, owner := range slice.Owners {
		if strings.TrimSpace(owner) == username {
			return true
		}
	}
	return false
}

func fastMergeUnsupportedHash(hash string) bool {
	return strings.HasPrefix(strings.TrimSpace(hash), "chgver_revert~")
}

func fastMergeTouchesConfig(paths []string) bool {
	for _, rawPath := range paths {
		trimmed := strings.Trim(strings.TrimSpace(rawPath), "/")
		if trimmed == ".gitslice/config.yaml" || strings.HasSuffix(trimmed, "/.gitslice/config.yaml") {
			return true
		}
	}
	return false
}

func fastMergeHomeID(sourceSlice *models.Slice, cs *models.Changeset, paths []string) string {
	if sourceSlice != nil {
		if username := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sourceSlice.ID), "home_")); username != "" && strings.HasPrefix(strings.TrimSpace(sourceSlice.ID), "home_") {
			return username
		}
	}
	if homeRoot := fastMergeCommonHomeRoot(paths); homeRoot != "" {
		return homeRoot
	}
	if sourceSlice != nil {
		if createdBy := strings.TrimSpace(sourceSlice.CreatedBy); createdBy != "" {
			return createdBy
		}
		for _, owner := range sourceSlice.Owners {
			if owner = strings.TrimSpace(owner); owner != "" {
				return owner
			}
		}
	}
	if cs != nil && strings.TrimSpace(cs.SliceID) != "" {
		return strings.TrimSpace(cs.SliceID)
	}
	return "global"
}

func fastMergeCommonHomeRoot(paths []string) string {
	root := ""
	for _, path := range normalizeRelativePaths(paths) {
		part, _, _ := strings.Cut(path, "/")
		if part == "" {
			continue
		}
		if root == "" {
			root = part
			continue
		}
		if root != part {
			return ""
		}
	}
	return root
}

func fastMergeShardID(homeID string) int32 {
	homeID = strings.TrimSpace(homeID)
	if homeID == "" {
		homeID = "global"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(homeID))
	return int32(h.Sum32() % postgresMergeEventShardCount)
}
