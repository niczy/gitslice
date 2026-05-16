package storage

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

func insertContentCommitDirs(ctx context.Context, exec execable, event *models.MergeEvent) error {
	rows := contentCommitDirRowsFromMergeEvent(event)
	if len(rows) == 0 {
		return nil
	}

	homeIDs := make([]string, 0, len(rows))
	dirPaths := make([]string, 0, len(rows))
	commitHashes := make([]string, 0, len(rows))
	sourceSliceIDs := make([]string, 0, len(rows))
	parentHashes := make([]string, 0, len(rows))
	messages := make([]string, 0, len(rows))
	authors := make([]string, 0, len(rows))
	committedAts := make([]time.Time, 0, len(rows))
	mergeSeqs := make([]int64, 0, len(rows))
	for _, row := range rows {
		homeIDs = append(homeIDs, row.HomeID)
		dirPaths = append(dirPaths, row.DirPath)
		commitHashes = append(commitHashes, row.CommitHash)
		sourceSliceIDs = append(sourceSliceIDs, row.SourceSliceID)
		parentHashes = append(parentHashes, row.ParentHash)
		messages = append(messages, row.Message)
		authors = append(authors, row.Author)
		committedAts = append(committedAts, row.CommittedAt)
		mergeSeqs = append(mergeSeqs, row.MergeSeq)
	}

	_, err := exec.Exec(ctx, `
		INSERT INTO content_commit_dirs (
			home_id, dir_path, commit_hash, source_slice_id, parent_hash,
			message, author, committed_at, merge_seq
		)
		SELECT home_id, dir_path, commit_hash, source_slice_id, parent_hash,
		       message, author, committed_at, merge_seq
		FROM unnest(
			$1::text[], $2::text[], $3::text[], $4::text[], $5::text[],
			$6::text[], $7::text[], $8::timestamptz[], $9::bigint[]
		) AS r(home_id, dir_path, commit_hash, source_slice_id, parent_hash,
		       message, author, committed_at, merge_seq)
		ON CONFLICT (home_id, dir_path, commit_hash) DO UPDATE SET
			source_slice_id = EXCLUDED.source_slice_id,
			parent_hash = EXCLUDED.parent_hash,
			message = EXCLUDED.message,
			author = EXCLUDED.author,
			committed_at = EXCLUDED.committed_at,
			merge_seq = EXCLUDED.merge_seq
	`, homeIDs, dirPaths, commitHashes, sourceSliceIDs, parentHashes, messages, authors, committedAts, mergeSeqs)
	return err
}

func (s *PostgresNativeStorage) ListSliceContentCommits(ctx context.Context, sliceID string, scopes []ContentCommitScope, limit int, fromCommitHash string) ([]*models.Commit, error) {
	ctx = ensureCtx(ctx)
	sliceID = strings.TrimSpace(sliceID)
	if sliceID == "" {
		return nil, ErrInvalidInput
	}
	limit = normalizeSliceCommitLimit(limit)

	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slices WHERE id = $1)`, sliceID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrSliceNotFound
	}

	normalizedScopes := normalizeContentCommitScopes(scopes)
	fromCommitHash = strings.TrimSpace(fromCommitHash)
	var initialCursor *contentCommitListCursor
	if fromCommitHash != "" {
		cursor, ok, err := s.findContentCommitListCursor(ctx, sliceID, normalizedScopes, fromCommitHash)
		if err != nil {
			return nil, err
		}
		if !ok {
			return []*models.Commit{}, nil
		}
		initialCursor = cursor
	}

	pageSize := limit
	if pageSize < defaultSliceCommitListLimit {
		pageSize = defaultSliceCommitListLimit
	}
	streams := make([]*postgresContentCommitStream, 0, len(normalizedScopes)+1)
	streams = append(streams, newPostgresContentCommitStream(initialCursor, pageSize, func(ctx context.Context, after *contentCommitListCursor, pageLimit int) ([]*models.Commit, error) {
		return s.listSliceCommitPageByTime(ctx, sliceID, after, pageLimit)
	}))
	for _, scope := range normalizedScopes {
		scope := scope
		streams = append(streams, newPostgresContentCommitStream(initialCursor, pageSize, func(ctx context.Context, after *contentCommitListCursor, pageLimit int) ([]*models.Commit, error) {
			return s.listContentCommitDirPage(ctx, scope.HomeID, scope.DirPath, after, pageLimit)
		}))
	}

	seen := make(map[string]struct{}, limit)
	commits := make([]*models.Commit, 0, limit)
	for len(commits) < limit {
		for _, stream := range streams {
			if len(stream.buffer) == 0 && !stream.exhausted {
				if err := stream.fetch(ctx); err != nil {
					return nil, err
				}
			}
		}

		bestStream := -1
		for i, stream := range streams {
			if len(stream.buffer) == 0 {
				continue
			}
			if bestStream == -1 || contentCommitListNewer(stream.buffer[0], streams[bestStream].buffer[0]) {
				bestStream = i
			}
		}
		if bestStream == -1 {
			break
		}

		next := streams[bestStream].pop()
		if _, ok := seen[next.CommitHash]; ok {
			continue
		}
		seen[next.CommitHash] = struct{}{}
		commits = append(commits, next)
	}
	if commits == nil {
		commits = []*models.Commit{}
	}
	return commits, nil
}

type contentCommitListCursor struct {
	CommittedAt time.Time
	CommitHash  string
}

type postgresContentCommitStream struct {
	after     *contentCommitListCursor
	pageLimit int
	fetchPage func(context.Context, *contentCommitListCursor, int) ([]*models.Commit, error)
	buffer    []*models.Commit
	exhausted bool
}

func newPostgresContentCommitStream(after *contentCommitListCursor, pageLimit int, fetchPage func(context.Context, *contentCommitListCursor, int) ([]*models.Commit, error)) *postgresContentCommitStream {
	return &postgresContentCommitStream{
		after:     after,
		pageLimit: pageLimit,
		fetchPage: fetchPage,
	}
}

func (s *postgresContentCommitStream) fetch(ctx context.Context) error {
	if s.exhausted {
		return nil
	}
	commits, err := s.fetchPage(ctx, s.after, s.pageLimit)
	if err != nil {
		return err
	}
	s.buffer = commits
	if len(commits) < s.pageLimit {
		s.exhausted = true
	}
	if len(commits) > 0 {
		last := commits[len(commits)-1]
		s.after = &contentCommitListCursor{
			CommittedAt: last.Timestamp,
			CommitHash:  last.CommitHash,
		}
	}
	return nil
}

func (s *postgresContentCommitStream) pop() *models.Commit {
	next := s.buffer[0]
	s.buffer = s.buffer[1:]
	return next
}

func (s *PostgresNativeStorage) findContentCommitListCursor(ctx context.Context, sliceID string, scopes []ContentCommitScope, commitHash string) (*contentCommitListCursor, bool, error) {
	homeIDs := make([]string, 0, len(scopes))
	dirPaths := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		homeIDs = append(homeIDs, scope.HomeID)
		dirPaths = append(dirPaths, scope.DirPath)
	}
	var cursor contentCommitListCursor
	err := s.pool.QueryRow(ctx, `
		WITH scope_rows AS (
			SELECT home_id, dir_path
			FROM unnest($2::text[], $3::text[]) AS s(home_id, dir_path)
		),
		combined AS (
			SELECT committed_at, commit_hash
			FROM slice_commits
			WHERE slice_id = $1 AND commit_hash = $4
			UNION ALL
			SELECT c.committed_at, c.commit_hash
			FROM content_commit_dirs c
			JOIN scope_rows s ON s.home_id = c.home_id AND s.dir_path = c.dir_path
			WHERE c.commit_hash = $4
		)
		SELECT committed_at, commit_hash
		FROM combined
		ORDER BY committed_at DESC, commit_hash DESC
		LIMIT 1
	`, sliceID, homeIDs, dirPaths, commitHash).Scan(&cursor.CommittedAt, &cursor.CommitHash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &cursor, true, nil
}

func (s *PostgresNativeStorage) listSliceCommitPageByTime(ctx context.Context, sliceID string, after *contentCommitListCursor, limit int) ([]*models.Commit, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = s.pool.Query(ctx, `
			SELECT commit_hash, parent_hash, message, committed_at
			FROM slice_commits
			WHERE slice_id = $1
			ORDER BY committed_at DESC, commit_hash DESC
			LIMIT $2
		`, sliceID, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT commit_hash, parent_hash, message, committed_at
			FROM slice_commits
			WHERE slice_id = $1
			  AND (committed_at, commit_hash) < ($2, $3)
			ORDER BY committed_at DESC, commit_hash DESC
			LIMIT $4
		`, sliceID, after.CommittedAt, after.CommitHash, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectContentCommitRows(rows, limit)
}

func (s *PostgresNativeStorage) listContentCommitDirPage(ctx context.Context, homeID string, dirPath string, after *contentCommitListCursor, limit int) ([]*models.Commit, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = s.pool.Query(ctx, `
			SELECT commit_hash, parent_hash, message, committed_at
			FROM content_commit_dirs
			WHERE home_id = $1 AND dir_path = $2
			ORDER BY committed_at DESC, commit_hash DESC
			LIMIT $3
		`, homeID, dirPath, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT commit_hash, parent_hash, message, committed_at
			FROM content_commit_dirs
			WHERE home_id = $1 AND dir_path = $2
			  AND (committed_at, commit_hash) < ($3, $4)
			ORDER BY committed_at DESC, commit_hash DESC
			LIMIT $5
		`, homeID, dirPath, after.CommittedAt, after.CommitHash, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectContentCommitRows(rows, limit)
}

func collectContentCommitRows(rows pgx.Rows, limit int) ([]*models.Commit, error) {
	commits := make([]*models.Commit, 0, limit)
	for rows.Next() {
		var commit models.Commit
		if err := rows.Scan(&commit.CommitHash, &commit.ParentHash, &commit.Message, &commit.Timestamp); err != nil {
			return nil, err
		}
		commits = append(commits, &commit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return commits, nil
}

func contentCommitListNewer(a *models.Commit, b *models.Commit) bool {
	if a.Timestamp.Equal(b.Timestamp) {
		return a.CommitHash > b.CommitHash
	}
	return a.Timestamp.After(b.Timestamp)
}
