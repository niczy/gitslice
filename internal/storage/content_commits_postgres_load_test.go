package storage

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niczy/gitslice/internal/models"
)

func TestPostgresContentCommitListLoad(t *testing.T) {
	if os.Getenv("RUN_POSTGRES_CONTENT_COMMIT_LOAD") != "1" {
		t.Skip("set RUN_POSTGRES_CONTENT_COMMIT_LOAD=1 to run the Postgres content commit load test")
	}
	dsn := strings.TrimSpace(os.Getenv("BENCHMARK_POSTGRES_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	}
	if dsn == "" {
		t.Fatal("BENCHMARK_POSTGRES_DSN or TEST_POSTGRES_DSN is required")
	}

	commitCount := loadTestIntEnv(t, "CONTENT_COMMIT_LOAD_COMMITS", 100_000)
	batchSize := loadTestIntEnv(t, "CONTENT_COMMIT_LOAD_BATCH", 10_000)
	queryLimit := loadTestIntEnv(t, "CONTENT_COMMIT_LOAD_LIMIT", 100)
	queryRuns := loadTestIntEnv(t, "CONTENT_COMMIT_LOAD_QUERY_RUNS", 3)
	if commitCount < queryLimit {
		t.Fatalf("CONTENT_COMMIT_LOAD_COMMITS must be >= CONTENT_COMMIT_LOAD_LIMIT")
	}

	ctx := context.Background()
	namespace := fmt.Sprintf("content-commit-load-%d", time.Now().UnixNano())
	schema := pgSchemaFromNamespace(namespace)
	st, err := NewPostgresNativeStorageWithOptions(ctx, dsn, NewInMemoryObjectStore(), namespace, PostgresNativeStorageOptions{
		MaxConns: int32(loadTestIntEnv(t, "CONTENT_COMMIT_LOAD_MAX_CONNS", 16)),
	})
	if err != nil {
		t.Fatalf("NewPostgresNativeStorageWithOptions failed: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
		dropPostgresLoadSchema(t, dsn, schema)
	})

	homeID := "load-home"
	rootDir := "repo"
	viewSliceID := "load-view"
	sourceSliceID := "load-source"
	now := time.Now().UTC().Add(-time.Duration(commitCount) * time.Millisecond)

	for _, sl := range []*models.Slice{
		{
			ID:          viewSliceID,
			Name:        "Load View",
			Description: "content commit load view",
			CreatedBy:   homeID,
			Owners:      []string{homeID},
			Visibility:  models.VisibilityPrivate,
		},
		{
			ID:          sourceSliceID,
			Name:        "Load Source",
			Description: "content commit load source",
			CreatedBy:   homeID,
			Owners:      []string{homeID},
			Visibility:  models.VisibilityPrivate,
		},
	} {
		if err := st.CreateSlice(ctx, sl); err != nil {
			t.Fatalf("CreateSlice(%s) failed: %v", sl.ID, err)
		}
	}

	insertStart := time.Now()
	insertedRows := seedContentCommitLoadRows(t, ctx, st, homeID, rootDir, sourceSliceID, commitCount, batchSize, now)
	insertElapsed := time.Since(insertStart)

	var projectedRows, scopedRows int64
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM content_commit_dirs`).Scan(&projectedRows); err != nil {
		t.Fatalf("count projected rows failed: %v", err)
	}
	if err := st.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM content_commit_dirs
		WHERE home_id = $1 AND dir_path = $2
	`, homeID, rootDir).Scan(&scopedRows); err != nil {
		t.Fatalf("count scoped rows failed: %v", err)
	}
	if projectedRows != int64(insertedRows) {
		t.Fatalf("expected %d projected rows, got %d", insertedRows, projectedRows)
	}
	if scopedRows != int64(commitCount) {
		t.Fatalf("expected %d scoped rows, got %d", commitCount, scopedRows)
	}
	if os.Getenv("CONTENT_COMMIT_LOAD_ANALYZE") != "0" {
		analyzeStart := time.Now()
		if _, err := st.pool.Exec(ctx, `ANALYZE content_commit_dirs`); err != nil {
			t.Fatalf("analyze content_commit_dirs failed: %v", err)
		}
		if _, err := st.pool.Exec(ctx, `ANALYZE slice_commits`); err != nil {
			t.Fatalf("analyze slice_commits failed: %v", err)
		}
		t.Logf("analyzed benchmark tables in %s", time.Since(analyzeStart).Round(time.Millisecond))
	}

	scope := []ContentCommitScope{{HomeID: homeID, DirPath: rootDir}}
	firstPage, firstLatencies := measureContentCommitLoadQuery(t, ctx, st, viewSliceID, scope, queryLimit, "", queryRuns)
	if len(firstPage) != queryLimit {
		t.Fatalf("expected first page length %d, got %d", queryLimit, len(firstPage))
	}
	expectedNewest := loadCommitHash(commitCount - 1)
	if firstPage[0].CommitHash != expectedNewest {
		t.Fatalf("expected newest commit %s, got %s", expectedNewest, firstPage[0].CommitHash)
	}

	nextCursor := firstPage[len(firstPage)-1].CommitHash
	_, nextLatencies := measureContentCommitLoadQuery(t, ctx, st, viewSliceID, scope, queryLimit, nextCursor, queryRuns)

	deepCursor := loadCommitHash(commitCount / 2)
	_, deepLatencies := measureContentCommitLoadQuery(t, ctx, st, viewSliceID, scope, queryLimit, deepCursor, queryRuns)

	t.Logf("=== Postgres Content Commit Load Results ===")
	t.Logf("Commits/files seeded:     %d", commitCount)
	t.Logf("Projected rows seeded:    %d", projectedRows)
	t.Logf("Scoped rows queried:      %d", scopedRows)
	t.Logf("Insert elapsed:           %s", insertElapsed.Round(time.Millisecond))
	t.Logf("Insert throughput:        %.1f commits/sec, %.1f projection rows/sec", float64(commitCount)/insertElapsed.Seconds(), float64(projectedRows)/insertElapsed.Seconds())
	t.Logf("List limit:               %d", queryLimit)
	t.Logf("First page latency:       %s", formatLoadLatencies(firstLatencies))
	t.Logf("Next page latency:        %s", formatLoadLatencies(nextLatencies))
	t.Logf("Deep cursor latency:      %s", formatLoadLatencies(deepLatencies))

	if os.Getenv("CONTENT_COMMIT_LOAD_EXPLAIN") == "1" {
		plan, err := explainContentCommitLoadQuery(ctx, st, viewSliceID, scope, queryLimit, deepCursor)
		if err != nil {
			t.Fatalf("explain failed: %v", err)
		}
		t.Logf("Deep cursor EXPLAIN ANALYZE:\n%s", strings.Join(plan, "\n"))
	}
}

func seedContentCommitLoadRows(t *testing.T, ctx context.Context, st *PostgresNativeStorage, homeID, rootDir, sourceSliceID string, commitCount, batchSize int, baseTime time.Time) int {
	t.Helper()
	columns := []string{
		"home_id",
		"dir_path",
		"commit_hash",
		"source_slice_id",
		"parent_hash",
		"message",
		"author",
		"committed_at",
		"merge_seq",
	}
	inserted := 0
	for start := 0; start < commitCount; start += batchSize {
		end := start + batchSize
		if end > commitCount {
			end = commitCount
		}
		rows := make([][]any, 0, (end-start)*2)
		for i := start; i < end; i++ {
			commitHash := loadCommitHash(i)
			parentHash := ""
			if i > 0 {
				parentHash = loadCommitHash(i - 1)
			}
			committedAt := baseTime.Add(time.Duration(i) * time.Millisecond)
			filePath := fmt.Sprintf("%s/file-%09d.txt", rootDir, i)
			for _, dirPath := range []string{rootDir, filePath} {
				rows = append(rows, []any{
					homeID,
					dirPath,
					commitHash,
					sourceSliceID,
					parentHash,
					"load commit",
					"load",
					committedAt,
					int64(i + 1),
				})
			}
		}
		copied, err := st.pool.CopyFrom(ctx, pgx.Identifier{"content_commit_dirs"}, columns, pgx.CopyFromRows(rows))
		if err != nil {
			t.Fatalf("CopyFrom content_commit_dirs failed at commit %d: %v", start, err)
		}
		inserted += int(copied)
		t.Logf("seeded %d/%d commits (%d projection rows)", end, commitCount, inserted)
	}
	return inserted
}

func measureContentCommitLoadQuery(t *testing.T, ctx context.Context, st *PostgresNativeStorage, sliceID string, scopes []ContentCommitScope, limit int, cursor string, runs int) ([]*models.Commit, []time.Duration) {
	t.Helper()
	latencies := make([]time.Duration, 0, runs)
	var commits []*models.Commit
	for i := 0; i < runs; i++ {
		start := time.Now()
		var err error
		commits, err = st.ListSliceContentCommits(ctx, sliceID, scopes, limit, cursor)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("ListSliceContentCommits(cursor=%q) failed: %v", cursor, err)
		}
		latencies = append(latencies, elapsed)
	}
	return commits, latencies
}

func explainContentCommitLoadQuery(ctx context.Context, st *PostgresNativeStorage, sliceID string, scopes []ContentCommitScope, limit int, fromCommitHash string) ([]string, error) {
	normalizedScopes := normalizeContentCommitScopes(scopes)
	if len(normalizedScopes) == 0 {
		return nil, fmt.Errorf("no scopes to explain")
	}
	scope := normalizedScopes[0]
	cursor, ok, err := st.findContentCommitListCursor(ctx, sliceID, normalizedScopes, fromCommitHash, nil)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("cursor %q not found", fromCommitHash)
	}
	rows, err := st.pool.Query(ctx, `
		EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
		SELECT commit_hash, parent_hash, message, committed_at
		FROM content_commit_dirs
		WHERE home_id = $1 AND dir_path = $2
		  AND (committed_at, commit_hash) < ($3, $4)
		ORDER BY committed_at DESC, commit_hash DESC
		LIMIT $5
	`, scope.HomeID, scope.DirPath, cursor.CommittedAt, cursor.CommitHash, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		plan = append(plan, line)
	}
	return plan, rows.Err()
}

func loadCommitHash(i int) string {
	return fmt.Sprintf("load-c-%09d", i)
}

func loadTestIntEnv(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s must be a positive integer, got %q", key, raw)
	}
	return n
}

func formatLoadLatencies(latencies []time.Duration) string {
	if len(latencies) == 0 {
		return "n/a"
	}
	var min, max, total time.Duration
	min = latencies[0]
	for _, latency := range latencies {
		if latency < min {
			min = latency
		}
		if latency > max {
			max = latency
		}
		total += latency
	}
	avg := total / time.Duration(len(latencies))
	return fmt.Sprintf("min=%s avg=%s max=%s runs=%d", min.Round(time.Microsecond), avg.Round(time.Microsecond), max.Round(time.Microsecond), len(latencies))
}

func dropPostgresLoadSchema(t *testing.T, dsn string, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Logf("cleanup: connect failed: %v", err)
		return
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)); err != nil {
		t.Logf("cleanup: drop schema %s failed: %v", schema, err)
	}
}
