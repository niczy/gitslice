package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/ids"
	"github.com/niczy/gitslice/internal/models"
)

// PostgresNativeStorage implements the Storage interface using native PostgreSQL
// tables as the source of truth for metadata. Blob payloads remain in an ObjectStore.
//
// This replaces the deprecated snapshot-based storage_state blob backend.
type PostgresNativeStorage struct {
	pool        *pgxpool.Pool
	objectStore ObjectStore
	namespace   string
}

type PostgresNativeStorageOptions struct {
	MaxConns                int32
	MinConns                int32
	MaxConnLifetime         time.Duration
	RebuildIndexesOnStartup bool
}

// PostgresPoolStats is a stable snapshot of pgx pool counters used by
// benchmark and diagnostic code without exposing pgxpool internals.
type PostgresPoolStats struct {
	AcquireCount            int64
	AcquireDuration         time.Duration
	AcquiredConns           int32
	CanceledAcquireCount    int64
	ConstructingConns       int32
	EmptyAcquireCount       int64
	EmptyAcquireWaitTime    time.Duration
	IdleConns               int32
	MaxConns                int32
	TotalConns              int32
	NewConnsCount           int64
	MaxLifetimeDestroyCount int64
	MaxIdleDestroyCount     int64
}

func nativeEntryID(sliceID, p string) string {
	if p == "" {
		// Root node uses the slice ID so callers can list root children via parentID=sliceID.
		return sliceID
	}
	return generateEntryID(sliceID, p)
}

func nativeParentID(sliceID, p string) string {
	if p == "" {
		return ""
	}
	dir := path.Dir(p)
	if dir == "." || dir == "/" || dir == "" {
		return sliceID
	}
	return nativeEntryID(sliceID, dir)
}

func collectMaterializedPaths(rawFiles []string) []string {
	paths := make([]string, 0, len(rawFiles))
	seen := make(map[string]struct{}, len(rawFiles))
	for _, raw := range rawFiles {
		p := cleanRelativePath(raw)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func computeDirSet(paths []string) map[string]bool {
	dirs := make(map[string]bool)
	for _, p := range paths {
		for _, d := range extractParentDirs(p) {
			if d == "" {
				continue
			}
			dirs[d] = true
		}
	}
	for _, p := range paths {
		prefix := p + "/"
		i := sort.SearchStrings(paths, prefix)
		if i < len(paths) && strings.HasPrefix(paths[i], prefix) {
			dirs[p] = true
		}
	}
	return dirs
}

func normalizeFileIndexIDs(fileIDs []string) []string {
	out := make([]string, 0, len(fileIDs))
	seen := make(map[string]struct{}, len(fileIDs))
	for _, fileID := range fileIDs {
		cleaned := strings.TrimSpace(fileID)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func normalizeRelativePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, filePath := range paths {
		cleaned := cleanRelativePath(filePath)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func mergeRootSliceFileIDs(filesJSON []byte, fileIDs []string) ([]byte, error) {
	var files []string
	if err := json.Unmarshal(filesJSON, &files); err != nil {
		files = []string{}
	}
	seen := make(map[string]struct{}, len(files)+len(fileIDs))
	out := make([]string, 0, len(files)+len(fileIDs))
	for _, existing := range files {
		cleaned := strings.TrimSpace(existing)
		if cleaned == "" {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	for _, fileID := range normalizeFileIndexIDs(fileIDs) {
		if _, ok := seen[fileID]; ok {
			continue
		}
		seen[fileID] = struct{}{}
		out = append(out, fileID)
	}
	sort.Strings(out)
	return json.Marshal(out)
}

func fileChangeCopyRows(changes []*models.FileChangeRecord) [][]any {
	rows := make([][]any, 0, len(changes))
	for _, change := range changes {
		if change == nil || strings.TrimSpace(change.ID) == "" {
			continue
		}
		rows = append(rows, []any{
			change.ID,
			change.SliceID,
			change.CommitHash,
			change.Path,
			change.OldPath,
			string(change.ChangeType),
			change.OldHash,
			change.NewHash,
			change.LinesAdded,
			change.LinesDeleted,
			change.Author,
			change.Message,
			change.Timestamp,
		})
	}
	return rows
}

type postgresRootPromotionFile struct {
	Manifest   *models.FileManifest
	BlockCount int
}

type postgresEntryContribution struct {
	Type string
	Size int64
}

func appendGlobalCommitsTx(ctx context.Context, exec execable, commits []*models.GlobalCommit) error {
	if len(commits) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(commits))
	timestamps := make([]time.Time, 0, len(commits))
	mergedSliceIDs := make([]string, 0, len(commits))
	for i := len(commits) - 1; i >= 0; i-- {
		commit := commits[i]
		if commit == nil || strings.TrimSpace(commit.CommitHash) == "" {
			continue
		}
		hashes = append(hashes, strings.TrimSpace(commit.CommitHash))
		timestamps = append(timestamps, commit.Timestamp)
		merged, err := json.Marshal(commit.MergedSliceIDs)
		if err != nil {
			return err
		}
		mergedSliceIDs = append(mergedSliceIDs, string(merged))
	}
	if len(hashes) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO global_commits (commit_hash, committed_at, merged_slice_ids)
		SELECT commit_hash, committed_at, merged_slice_ids::jsonb
		FROM unnest($1::text[], $2::timestamptz[], $3::text[]) WITH ORDINALITY
			AS rows(commit_hash, committed_at, merged_slice_ids, ord)
		ORDER BY ord
		ON CONFLICT (commit_hash) DO NOTHING
	`, hashes, timestamps, mergedSliceIDs)
	return err
}

func replaceGlobalCommitsTx(ctx context.Context, exec execable, commits []*models.GlobalCommit) error {
	// UpdateGlobalState has replacement semantics; root promotion uses the
	// append-only path directly when preserving the durable timeline matters.
	if _, err := exec.Exec(ctx, `DELETE FROM global_commits`); err != nil {
		return err
	}
	return appendGlobalCommitsTx(ctx, exec, commits)
}

func loadGlobalCommitHistory(ctx context.Context, q queryable) ([]*models.GlobalCommit, error) {
	rows, err := q.Query(ctx, `
		SELECT commit_hash, committed_at, merged_slice_ids
		FROM global_commits
		ORDER BY committed_at DESC, seq DESC
		LIMIT 10000
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]*models.GlobalCommit, 0)
	for rows.Next() {
		var commit models.GlobalCommit
		var mergedJSON []byte
		if err := rows.Scan(&commit.CommitHash, &commit.Timestamp, &mergedJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(mergedJSON, &commit.MergedSliceIDs); err != nil {
			commit.MergedSliceIDs = nil
		}
		history = append(history, &commit)
	}
	return history, rows.Err()
}

func decodeGlobalStateHistory(stateJSON []byte) []*models.GlobalCommit {
	var stateData struct {
		History []*models.GlobalCommit `json:"history"`
	}
	if err := json.Unmarshal(stateJSON, &stateData); err != nil {
		return []*models.GlobalCommit{}
	}
	if stateData.History == nil {
		return []*models.GlobalCommit{}
	}
	return stateData.History
}

func globalStateJSONWithoutHistory() []byte {
	stateJSON, _ := json.Marshal(struct {
		History []*models.GlobalCommit `json:"history"`
	}{History: []*models.GlobalCommit{}})
	return stateJSON
}

func aggregateImmediateParentDeltas(paths []string, oldByPath map[string]postgresEntryContribution, manifestByPath map[string]*models.FileManifest) (deltaPaths []string, deltas []int64) {
	aggregated := make(map[string]int64)
	for _, filePath := range paths {
		manifest := manifestByPath[filePath]
		if manifest == nil {
			continue
		}
		parentPath := cleanRelativePath(path.Dir(filePath))
		if parentPath == "" {
			continue
		}
		old := int64(0)
		if prev, ok := oldByPath[filePath]; ok {
			old = directoryEntryAggregateContribution(&models.DirectoryEntry{Type: prev.Type, Size: prev.Size})
		}
		next := directoryEntryAggregateContribution(&models.DirectoryEntry{Type: "file", Size: manifest.TotalSize})
		if delta := next - old; delta != 0 {
			aggregated[parentPath] += delta
		}
	}
	if len(aggregated) == 0 {
		return nil, nil
	}
	deltaPaths = make([]string, 0, len(aggregated))
	for p := range aggregated {
		deltaPaths = append(deltaPaths, p)
	}
	sort.Strings(deltaPaths)
	deltas = make([]int64, 0, len(deltaPaths))
	for _, p := range deltaPaths {
		deltas = append(deltas, aggregated[p])
	}
	return deltaPaths, deltas
}

func (s *PostgresNativeStorage) materializeDirectoryTreeTx(ctx context.Context, tx pgx.Tx, sliceID string, rawFiles []string, includeFiles bool) error {
	paths := collectMaterializedPaths(rawFiles)
	dirs := computeDirSet(paths)

	ids := make([]string, 0, len(paths)*2+1)
	entryPaths := make([]string, 0, len(paths)*2+1)
	types := make([]string, 0, len(paths)*2+1)
	parents := make([]string, 0, len(paths)*2+1)
	sizes := make([]int64, 0, len(paths)*2+1)
	enqueue := func(id, pth, typ, parent string, size int64) {
		ids = append(ids, id)
		entryPaths = append(entryPaths, pth)
		types = append(types, typ)
		parents = append(parents, parent)
		sizes = append(sizes, size)
	}

	// Root node.
	enqueue(nativeEntryID(sliceID, ""), "", "directory", "", 0)

	// Directories parents-first.
	for _, dirPath := range sortDirsByDepth(dirs) {
		dirPath = cleanRelativePath(dirPath)
		if dirPath == "" {
			continue
		}
		enqueue(nativeEntryID(sliceID, dirPath), dirPath, "directory", nativeParentID(sliceID, dirPath), 0)
	}

	// Leaf files.
	if includeFiles {
		for _, p := range paths {
			if dirs[p] {
				continue
			}
			enqueue(nativeEntryID(sliceID, p), p, "file", nativeParentID(sliceID, p), 0)
		}
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size)
		SELECT id, $1, path, type, parent_id, NULL, size
		FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::bigint[])
			AS rows(id, path, type, parent_id, size)
		ON CONFLICT (slice_id, path) DO NOTHING
	`, sliceID, ids, entryPaths, types, parents, sizes)
	return err
}

func insertFileSliceIndex(ctx context.Context, exec execable, sliceID string, fileIDs []string) error {
	cleanedIDs := normalizeFileIndexIDs(fileIDs)
	if len(cleanedIDs) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO file_slice_index (file_id, slice_id)
		SELECT file_id, $2
		FROM unnest($1::text[]) AS file_id
		ON CONFLICT DO NOTHING
	`, cleanedIDs, strings.TrimSpace(sliceID))
	return err
}

type postgresEntryAggregateState struct {
	ID   string
	Path string
	Type string
	Size int64
}

func loadEntryAggregateState(ctx context.Context, tx pgx.Tx, sliceID, entryPath string) (*postgresEntryAggregateState, error) {
	entryPath = cleanRelativePath(entryPath)
	var state postgresEntryAggregateState
	err := tx.QueryRow(ctx, `
		SELECT id, path, type, size
		FROM directory_entries
		WHERE slice_id = $1 AND path = $2
	`, sliceID, entryPath).Scan(&state.ID, &state.Path, &state.Type, &state.Size)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &state, nil
}

func loadEntryAggregateStateByID(ctx context.Context, tx pgx.Tx, entryID string) (*postgresEntryAggregateState, error) {
	var state postgresEntryAggregateState
	err := tx.QueryRow(ctx, `
		SELECT id, path, type, size
		FROM directory_entries
		WHERE id = $1
	`, entryID).Scan(&state.ID, &state.Path, &state.Type, &state.Size)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &state, nil
}

func adjustAncestorDirectorySizesTx(ctx context.Context, tx pgx.Tx, sliceID, filePath string, delta int64) error {
	if delta == 0 {
		return nil
	}
	paths := ancestorDirectoryPaths(filePath)
	if len(paths) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE directory_entries
		SET size = GREATEST(0, size + $3),
			updated_at = NOW()
		WHERE slice_id = $1 AND type = 'directory' AND path = ANY($2)
	`, sliceID, paths, delta)
	return err
}

func rebuildDirectorySizesTx(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `
		SELECT slice_id, path, type, size
		FROM directory_entries
		ORDER BY slice_id, path
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type entryRow struct {
		sliceID string
		path    string
		typ     string
		size    int64
	}
	type sliceTotals struct {
		directories []string
		totals      map[string]int64
	}

	bySlice := make(map[string]*sliceTotals)
	for rows.Next() {
		var row entryRow
		if err := rows.Scan(&row.sliceID, &row.path, &row.typ, &row.size); err != nil {
			return err
		}
		agg := bySlice[row.sliceID]
		if agg == nil {
			agg = &sliceTotals{totals: make(map[string]int64)}
			bySlice[row.sliceID] = agg
		}
		if row.typ == "directory" {
			agg.directories = append(agg.directories, row.path)
			agg.totals[row.path] = 0
			continue
		}
		if row.typ != "file" || row.size <= 0 {
			continue
		}
		for _, dirPath := range ancestorDirectoryPaths(row.path) {
			agg.totals[dirPath] += row.size
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for sliceID, agg := range bySlice {
		for _, dirPath := range agg.directories {
			if _, err := tx.Exec(ctx, `
				UPDATE directory_entries
				SET size = $3,
					updated_at = NOW()
				WHERE slice_id = $1 AND path = $2 AND type = 'directory'
			`, sliceID, dirPath, agg.totals[dirPath]); err != nil {
				return err
			}
		}
	}
	return nil
}

func pgSchemaFromNamespace(namespace string) string {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return "public"
	}

	// Preserve existing deployments: the core server uses namespace "core" but
	// historically stored native tables in the default schema.
	if ns == "core" || ns == "default" {
		return "public"
	}

	// Sanitize into a safe SQL identifier (lower_snake, max 63 chars).
	var b strings.Builder
	b.Grow(len(ns) + 3)
	b.WriteString("ns_")

	prevUnderscore := false
	for _, r := range ns {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevUnderscore = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r - 'A' + 'a')
			prevUnderscore = false
		case r >= '0' && r <= '9':
			// Identifiers can't start with a digit; prefix already covers us.
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}

	s := strings.Trim(b.String(), "_")
	if s == "" || s == "ns" {
		return "public"
	}
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// NewPostgresNativeStorage creates a new native PostgreSQL storage backend.
// It runs schema migrations on startup to ensure tables exist.
func NewPostgresNativeStorage(ctx context.Context, dsn string, objectStore ObjectStore, namespace string) (*PostgresNativeStorage, error) {
	return NewPostgresNativeStorageWithOptions(ctx, dsn, objectStore, namespace, PostgresNativeStorageOptions{})
}

func NewPostgresNativeStorageWithOptions(ctx context.Context, dsn string, objectStore ObjectStore, namespace string, options PostgresNativeStorageOptions) (*PostgresNativeStorage, error) {
	ctx = ensureCtx(ctx)
	if dsn == "" {
		return nil, ErrInvalidInput
	}
	if objectStore == nil {
		return nil, ErrInvalidInput
	}
	if namespace == "" {
		namespace = "default"
	}

	schema := pgSchemaFromNamespace(namespace)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	applyPostgresNativePoolOptions(cfg, options)
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// Keep default behavior for the public schema.
		if schema == "" || schema == "public" {
			return nil
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schema)); err != nil {
			return fmt.Errorf("create schema %s: %w", schema, err)
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %s, public", schema)); err != nil {
			return fmt.Errorf("set search_path %s: %w", schema, err)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	if err := RunMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	storage := &PostgresNativeStorage{
		pool:        pool,
		objectStore: objectStore,
		namespace:   namespace,
	}
	if options.RebuildIndexesOnStartup {
		if err := storage.RebuildIndexes(ctx); err != nil {
			pool.Close()
			return nil, fmt.Errorf("rebuild indexes: %w", err)
		}
	}

	return storage, nil
}

func applyPostgresNativePoolOptions(cfg *pgxpool.Config, options PostgresNativeStorageOptions) {
	if options.MaxConns > 0 {
		cfg.MaxConns = options.MaxConns
	}
	if options.MinConns > 0 {
		cfg.MinConns = options.MinConns
	}
	if options.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = options.MaxConnLifetime
	}
}

func (s *PostgresNativeStorage) CreateAccount(ctx context.Context, account *models.Account) error {
	ctx = ensureCtx(ctx)
	if account == nil {
		return ErrInvalidInput
	}
	accountID := strings.TrimSpace(account.AccountID)
	ownerMode := normalizeAccountOwnerMode(account.OwnerMode)
	claimState := normalizeAccountClaimState(account.ClaimState)
	claimTokenHash := strings.TrimSpace(account.ClaimTokenHash)
	if accountID == "" || !validAccountOwnerMode(ownerMode) || !validAccountClaimState(claimState) {
		return ErrInvalidInput
	}

	now := time.Now()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	account.AccountID = accountID
	account.OwnerMode = ownerMode
	account.ClaimState = claimState
	account.ClaimTokenHash = claimTokenHash

	_, err := s.pool.Exec(ctx, `
		INSERT INTO accounts (account_id, owner_mode, claim_state, claim_token_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, accountID, string(ownerMode), string(claimState), claimTokenHash, account.CreatedAt, account.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetAccount(ctx context.Context, accountID string) (*models.Account, error) {
	ctx = ensureCtx(ctx)
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, ErrInvalidInput
	}

	var account models.Account
	err := s.pool.QueryRow(ctx, `
		SELECT account_id, owner_mode, claim_state, COALESCE(claim_token_hash, ''), created_at, updated_at
		FROM accounts
		WHERE account_id = $1
	`, accountID).Scan(&account.AccountID, &account.OwnerMode, &account.ClaimState, &account.ClaimTokenHash, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &account, nil
}

func (s *PostgresNativeStorage) GetAccountByClaimTokenHash(ctx context.Context, claimTokenHash string) (*models.Account, error) {
	ctx = ensureCtx(ctx)
	claimTokenHash = strings.TrimSpace(claimTokenHash)
	if claimTokenHash == "" {
		return nil, ErrInvalidInput
	}

	var account models.Account
	err := s.pool.QueryRow(ctx, `
		SELECT account_id, owner_mode, claim_state, COALESCE(claim_token_hash, ''), created_at, updated_at
		FROM accounts
		WHERE claim_token_hash = $1
	`, claimTokenHash).Scan(&account.AccountID, &account.OwnerMode, &account.ClaimState, &account.ClaimTokenHash, &account.CreatedAt, &account.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &account, nil
}

func (s *PostgresNativeStorage) UpdateAccount(ctx context.Context, account *models.Account) error {
	ctx = ensureCtx(ctx)
	if account == nil {
		return ErrInvalidInput
	}
	accountID := strings.TrimSpace(account.AccountID)
	ownerMode := normalizeAccountOwnerMode(account.OwnerMode)
	claimState := normalizeAccountClaimState(account.ClaimState)
	claimTokenHash := strings.TrimSpace(account.ClaimTokenHash)
	if accountID == "" || !validAccountOwnerMode(ownerMode) || !validAccountClaimState(claimState) {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts
		SET owner_mode = $1, claim_state = $2, claim_token_hash = $3, updated_at = NOW()
		WHERE account_id = $4
	`, string(ownerMode), string(claimState), claimTokenHash, accountID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

// Close closes the backing Postgres pool.
func (s *PostgresNativeStorage) Close() error {
	s.pool.Close()
	return nil
}

func (s *PostgresNativeStorage) PostgresPoolStats() PostgresPoolStats {
	if s == nil || s.pool == nil {
		return PostgresPoolStats{}
	}
	stats := s.pool.Stat()
	return PostgresPoolStats{
		AcquireCount:            stats.AcquireCount(),
		AcquireDuration:         stats.AcquireDuration(),
		AcquiredConns:           stats.AcquiredConns(),
		CanceledAcquireCount:    stats.CanceledAcquireCount(),
		ConstructingConns:       stats.ConstructingConns(),
		EmptyAcquireCount:       stats.EmptyAcquireCount(),
		EmptyAcquireWaitTime:    stats.EmptyAcquireWaitTime(),
		IdleConns:               stats.IdleConns(),
		MaxConns:                stats.MaxConns(),
		TotalConns:              stats.TotalConns(),
		NewConnsCount:           stats.NewConnsCount(),
		MaxLifetimeDestroyCount: stats.MaxLifetimeDestroyCount(),
		MaxIdleDestroyCount:     stats.MaxIdleDestroyCount(),
	}
}

// BulkWrite executes a sequence of storage operations and commits them as a single
// database transaction. This is used by admin workflows like git import to avoid
// slow per-operation commits.
//
// Note: object store writes happen inside the callback and may occur before the
// DB transaction commits. This is safe for our content-addressed object store
// layout (idempotent writes).
func (s *PostgresNativeStorage) BulkWrite(ctx context.Context, fn func(st Storage) error) error {
	ctx = ensureCtx(ctx)
	if fn == nil {
		return ErrInvalidInput
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	view := &postgresNativeTxView{PostgresNativeStorage: s, tx: tx}
	if err := fn(view); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Reset clears all persisted native rows. This is an admin/ops escape hatch.
// Object store blobs are not deleted.
//
// Note: this affects the currently selected schema (search_path).
func (s *PostgresNativeStorage) Reset(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	// Keep schema_migrations so we don't need to re-run DDL after a reset.
	_, err := s.pool.Exec(ctx, `
		TRUNCATE TABLE
			agent_session_audit,
			agent_session_events,
			agent_sessions,
			team_members,
			teams,
			organization_invites,
			organization_members,
			organizations,
			repo_bindings,
			auth_sessions,
			users,
			accounts,
			file_changes,
			global_commits,
			global_state,
			file_manifests,
			commit_snapshots,
			directory_entries,
			slice_commits,
			home_path_heads,
			projection_offsets,
			merge_events,
			changeset_snapshots,
			changesets,
			file_locks,
			slice_locks,
			file_slice_index,
			slice_metadata,
			slices
		RESTART IDENTITY CASCADE
	`)
	return err
}

func (s *PostgresNativeStorage) objKey(parts ...string) string {
	if s.namespace == "" {
		return strings.Join(parts, ":")
	}
	return fmt.Sprintf("%s:%s", s.namespace, strings.Join(parts, ":"))
}

func (s *PostgresNativeStorage) searchBlobObjectKey(version uint32, searchContentHash string) string {
	return s.objKey("search_index_blobs", fmt.Sprintf("v%d", version), strings.TrimSpace(searchContentHash))
}

func (s *PostgresNativeStorage) sliceSearchArtifactObjectKey(version uint32, sliceID, commitHash string) string {
	return s.objKey("slice_search_artifacts", fmt.Sprintf("v%d", version), strings.TrimSpace(sliceID), strings.TrimSpace(commitHash))
}

func (s *PostgresNativeStorage) workspaceSearchArtifactObjectKey(version uint32, workspaceID string) string {
	return s.objKey("workspace_search_artifacts", fmt.Sprintf("v%d", version), strings.TrimSpace(workspaceID))
}

type postgresNativeTxView struct {
	*PostgresNativeStorage
	tx pgx.Tx
}

// ---- Transactional overrides used by BulkWrite (git import) ----

func (s *postgresNativeTxView) CreateAccount(ctx context.Context, account *models.Account) error {
	return s.PostgresNativeStorage.CreateAccount(ctx, account)
}

func (s *postgresNativeTxView) GetAccount(ctx context.Context, accountID string) (*models.Account, error) {
	return s.PostgresNativeStorage.GetAccount(ctx, accountID)
}

func (s *postgresNativeTxView) GetAccountByClaimTokenHash(ctx context.Context, claimTokenHash string) (*models.Account, error) {
	return s.PostgresNativeStorage.GetAccountByClaimTokenHash(ctx, claimTokenHash)
}

func (s *postgresNativeTxView) UpdateAccount(ctx context.Context, account *models.Account) error {
	return s.PostgresNativeStorage.UpdateAccount(ctx, account)
}

func (s *postgresNativeTxView) CreateAgentSession(ctx context.Context, session *models.AgentSession) error {
	return s.PostgresNativeStorage.CreateAgentSession(ctx, session)
}

func (s *postgresNativeTxView) GetAgentSession(ctx context.Context, sessionID string) (*models.AgentSession, error) {
	return s.PostgresNativeStorage.GetAgentSession(ctx, sessionID)
}

func (s *postgresNativeTxView) GetActiveAgentSessionBySlice(ctx context.Context, sliceID string) (*models.AgentSession, error) {
	return s.PostgresNativeStorage.GetActiveAgentSessionBySlice(ctx, sliceID)
}

func (s *postgresNativeTxView) ListAgentSessionsByState(ctx context.Context, states []models.AgentSessionState, limit int) ([]*models.AgentSession, error) {
	return s.PostgresNativeStorage.ListAgentSessionsByState(ctx, states, limit)
}

func (s *postgresNativeTxView) UpdateAgentSession(ctx context.Context, session *models.AgentSession) error {
	return s.PostgresNativeStorage.UpdateAgentSession(ctx, session)
}

func (s *postgresNativeTxView) AppendAgentSessionEvent(ctx context.Context, event *models.AgentSessionEvent) error {
	return s.PostgresNativeStorage.AppendAgentSessionEvent(ctx, event)
}

func (s *postgresNativeTxView) ListAgentSessionEvents(ctx context.Context, sessionID string, sinceSeq uint64, limit int) ([]*models.AgentSessionEvent, error) {
	return s.PostgresNativeStorage.ListAgentSessionEvents(ctx, sessionID, sinceSeq, limit)
}

func (s *postgresNativeTxView) AddAgentSessionAudit(ctx context.Context, audit *models.AgentSessionAudit) error {
	return s.PostgresNativeStorage.AddAgentSessionAudit(ctx, audit)
}

func (s *postgresNativeTxView) UpdateSliceVisibility(ctx context.Context, sliceID string, visibility models.Visibility) error {
	ctx = ensureCtx(ctx)
	tag, err := s.tx.Exec(ctx, `UPDATE slices SET visibility = $1, updated_at = NOW() WHERE id = $2`, string(models.NormalizeVisibility(visibility)), sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	return nil
}

func (s *postgresNativeTxView) UpdateSliceFolderMounts(ctx context.Context, sliceID string, mounts []models.SliceFolderMount, files []string) error {
	ctx = ensureCtx(ctx)
	mountsJSON, err := json.Marshal(mounts)
	if err != nil {
		return err
	}
	if mounts == nil {
		mountsJSON = []byte("[]")
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return err
	}
	if files == nil {
		filesJSON = []byte("[]")
	}
	tag, err := s.tx.Exec(ctx, `UPDATE slices SET folder_mounts = $1, files = $2, updated_at = NOW() WHERE id = $3`, mountsJSON, filesJSON, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	if _, err := s.tx.Exec(ctx, `DELETE FROM directory_entries WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	return nil
}

func (s *postgresNativeTxView) GetPathVisibilityRule(ctx context.Context, p string) (*models.PathVisibilityRule, error) {
	return s.PostgresNativeStorage.getPathVisibilityRule(ctx, s.tx, p)
}

func (s *postgresNativeTxView) ListPathVisibilityRules(ctx context.Context, pathPrefix string) ([]*models.PathVisibilityRule, error) {
	return s.PostgresNativeStorage.listPathVisibilityRules(ctx, s.tx, pathPrefix)
}

func (s *postgresNativeTxView) UpsertPathVisibilityRule(ctx context.Context, rule *models.PathVisibilityRule) error {
	return s.PostgresNativeStorage.upsertPathVisibilityRule(ctx, s.tx, rule)
}

func (s *postgresNativeTxView) DeletePathVisibilityRule(ctx context.Context, p string) error {
	return s.PostgresNativeStorage.deletePathVisibilityRule(ctx, s.tx, p)
}

func (s *postgresNativeTxView) CreateSlice(ctx context.Context, slice *models.Slice) error {
	ctx = ensureCtx(ctx)
	if slice == nil || slice.ID == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	if slice.CreatedAt.IsZero() {
		slice.CreatedAt = now
	}
	slice.Visibility = models.NormalizeVisibility(slice.Visibility)
	slice.UpdatedAt = now

	filesJSON, _ := json.Marshal(slice.Files)
	if slice.Files == nil {
		filesJSON = []byte("[]")
	}
	ownersJSON, _ := json.Marshal(slice.Owners)
	if slice.Owners == nil {
		ownersJSON = []byte("[]")
	}
	mountsJSON, _ := json.Marshal(slice.FolderMounts)
	if slice.FolderMounts == nil {
		mountsJSON = []byte("[]")
	}

	slug, err := s.allocateSliceSlug(ctx, s.tx, slice)
	if err != nil {
		return err
	}
	slice.Slug = slug

	_, err = s.tx.Exec(ctx, `
		INSERT INTO slices (id, name, slug, description, created_by, parent_id, is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14)
	`, slice.ID, slice.Name, slice.Slug, slice.Description, slice.CreatedBy,
		slice.ParentSlice, slice.IsRoot, string(slice.Visibility), filesJSON, mountsJSON, ownersJSON,
		slice.CreatedAt, slice.UpdatedAt, slice.Environment)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrSliceAlreadyExists
		}
		return err
	}

	initialCommitHash := ids.GenerateInitialCommitID(slice.ID)
	_, err = s.tx.Exec(ctx, `
		INSERT INTO slice_metadata (slice_id, head_commit_hash, modified_files, last_modified, modified_files_count)
		VALUES ($1, $2, '[]', $3, 0)
	`, slice.ID, initialCommitHash, now)
	if err != nil {
		return err
	}

	if err := insertFileSliceIndex(ctx, s.tx, slice.ID, slice.Files); err != nil {
		return err
	}

	// Materialize directory-entry tree from the slice file set.
	if err := s.materializeDirectoryTreeTx(ctx, s.tx, slice.ID, slice.Files, true); err != nil {
		return err
	}

	return nil
}

func (s *postgresNativeTxView) GetSlice(ctx context.Context, sliceID string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.tx, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE id = $1`, sliceID)
}

func (s *postgresNativeTxView) GetSliceByName(ctx context.Context, name string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.tx, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE name = $1 AND is_root = false LIMIT 1`, name)
}

func (s *postgresNativeTxView) GetSliceBySlug(ctx context.Context, slug string) (*models.Slice, error) {
	return s.getSliceBySlug(ctx, s.tx, slug)
}

func (s *postgresNativeTxView) GetSliceByOwnerAndSlug(ctx context.Context, owner, slug string) (*models.Slice, error) {
	return s.getSliceByOwnerAndSlug(ctx, s.tx, owner, slug)
}

func (s *postgresNativeTxView) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.tx, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE is_root = true LIMIT 1`)
}

func (s *postgresNativeTxView) InitializeRootSlice(ctx context.Context) error {
	ctx = ensureCtx(ctx)

	var count int
	if err := s.tx.QueryRow(ctx, `SELECT COUNT(*) FROM slices WHERE is_root = true`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	rootSlice := &models.Slice{
		ID:          ids.RootSliceID,
		Name:        "Root Slice",
		Description: "The root slice containing all files",
		Visibility:  models.VisibilityPrivate,
		Files:       []string{},
		Owners:      []string{"system"},
		CreatedBy:   "system",
		IsRoot:      true,
	}
	if err := s.CreateSlice(ctx, rootSlice); err != nil {
		return err
	}

	_, err := s.tx.Exec(ctx, `UPDATE slice_metadata SET head_commit_hash = $2 WHERE slice_id = $1`, rootSlice.ID, ids.GenerateInitialCommitID(rootSlice.ID))
	return err
}

func (s *postgresNativeTxView) GetSliceMetadata(ctx context.Context, sliceID string) (*models.SliceMetadata, error) {
	ctx = ensureCtx(ctx)

	var meta models.SliceMetadata
	var modifiedFilesJSON []byte
	err := s.tx.QueryRow(ctx, `
		SELECT slice_id, head_commit_hash, modified_files, last_modified, modified_files_count
		FROM slice_metadata WHERE slice_id = $1
	`, sliceID).Scan(&meta.SliceID, &meta.HeadCommitHash, &modifiedFilesJSON, &meta.LastModified, &meta.ModifiedFilesCount)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrSliceNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(modifiedFilesJSON, &meta.ModifiedFiles); err != nil {
		meta.ModifiedFiles = []string{}
	}
	return &meta, nil
}

func (s *postgresNativeTxView) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	ctx = ensureCtx(ctx)
	if metadata == nil {
		return ErrInvalidInput
	}
	if metadata.LastModified.IsZero() {
		metadata.LastModified = time.Now()
	}
	modifiedJSON, _ := json.Marshal(metadata.ModifiedFiles)
	if metadata.ModifiedFiles == nil {
		modifiedJSON = []byte("[]")
	}
	tag, err := s.tx.Exec(ctx, `
		UPDATE slice_metadata
		SET head_commit_hash = $1, modified_files = $2, last_modified = $3, modified_files_count = $4
		WHERE slice_id = $5
	`, metadata.HeadCommitHash, modifiedJSON, metadata.LastModified, metadata.ModifiedFilesCount, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	return nil
}

func (s *postgresNativeTxView) AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error {
	ctx = ensureCtx(ctx)
	if commit == nil {
		return ErrInvalidInput
	}
	_, err := s.tx.Exec(ctx, `
		INSERT INTO slice_commits (slice_id, commit_hash, parent_hash, message, committed_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (slice_id, commit_hash) DO NOTHING
	`, sliceID, commit.CommitHash, commit.ParentHash, commit.Message, commit.Timestamp)
	return err
}

func (s *postgresNativeTxView) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	ctx = ensureCtx(ctx)
	var isRoot bool
	var filesJSON []byte
	err := s.tx.QueryRow(ctx, `SELECT is_root, files FROM slices WHERE id = $1`, sliceID).Scan(&isRoot, &filesJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrSliceNotFound
		}
		return err
	}
	if isRoot {
		var files []string
		if err := json.Unmarshal(filesJSON, &files); err != nil {
			files = []string{}
		}
		for _, f := range files {
			if f == fileID {
				return nil
			}
		}
		files = append(files, fileID)
		sort.Strings(files)
		filesJSON, _ = json.Marshal(files)
		_, err = s.tx.Exec(ctx, `UPDATE slices SET files = $1, updated_at = NOW() WHERE id = $2`, filesJSON, sliceID)
		return err
	}
	_, err = s.tx.Exec(ctx, `
		INSERT INTO file_slice_index (file_id, slice_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, fileID, sliceID)
	return err
}

func (s *postgresNativeTxView) AddFilesToSlice(ctx context.Context, fileIDs []string, sliceID string) error {
	ctx = ensureCtx(ctx)
	cleanedIDs := normalizeFileIndexIDs(fileIDs)
	if len(cleanedIDs) == 0 {
		return nil
	}

	var isRoot bool
	var filesJSON []byte
	err := s.tx.QueryRow(ctx, `SELECT is_root, files FROM slices WHERE id = $1`, sliceID).Scan(&isRoot, &filesJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrSliceNotFound
		}
		return err
	}
	if isRoot {
		merged, err := mergeRootSliceFileIDs(filesJSON, cleanedIDs)
		if err != nil {
			return err
		}
		_, err = s.tx.Exec(ctx, `UPDATE slices SET files = $1, updated_at = NOW() WHERE id = $2`, merged, sliceID)
		return err
	}

	_, err = s.tx.Exec(ctx, `
		INSERT INTO file_slice_index (file_id, slice_id)
		SELECT file_id, $2
		FROM unnest($1::text[]) AS file_id
		ON CONFLICT DO NOTHING
	`, cleanedIDs, sliceID)
	return err
}

func (s *postgresNativeTxView) GetActiveSlicesForFiles(ctx context.Context, fileIDs []string) (map[string][]string, error) {
	ctx = ensureCtx(ctx)
	cleanedIDs := normalizeFileIndexIDs(fileIDs)
	result := make(map[string][]string, len(cleanedIDs))
	if len(cleanedIDs) == 0 {
		return result, nil
	}

	rows, err := s.tx.Query(ctx, `
		SELECT file_id, slice_id
		FROM file_slice_index
		WHERE file_id = ANY($1)
		ORDER BY file_id, slice_id
	`, cleanedIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for _, fileID := range cleanedIDs {
		result[fileID] = []string{}
	}
	for rows.Next() {
		var fileID, mappedSliceID string
		if err := rows.Scan(&fileID, &mappedSliceID); err != nil {
			return nil, err
		}
		result[fileID] = append(result[fileID], mappedSliceID)
	}
	return result, rows.Err()
}

func (s *postgresNativeTxView) RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error {
	ctx = ensureCtx(ctx)
	var isRoot bool
	var filesJSON []byte
	err := s.tx.QueryRow(ctx, `SELECT is_root, files FROM slices WHERE id = $1`, sliceID).Scan(&isRoot, &filesJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrSliceNotFound
		}
		return err
	}
	if isRoot {
		var files []string
		if err := json.Unmarshal(filesJSON, &files); err != nil {
			return err
		}
		out := files[:0]
		for _, f := range files {
			if f != fileID {
				out = append(out, f)
			}
		}
		sort.Strings(out)
		newJSON, _ := json.Marshal(out)
		_, err = s.tx.Exec(ctx, `UPDATE slices SET files = $1, updated_at = NOW() WHERE id = $2`, newJSON, sliceID)
		return err
	}
	_, err = s.tx.Exec(ctx, `DELETE FROM file_slice_index WHERE file_id = $1 AND slice_id = $2`, fileID, sliceID)
	return err
}

func (s *postgresNativeTxView) AddEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	ctx = ensureCtx(ctx)
	if entry == nil {
		return ErrInvalidInput
	}
	sliceID := inferSliceIDForEntry(entry)
	if sliceID == "" {
		return ErrInvalidInput
	}

	p := cleanRelativePath(entry.Path)
	typ := strings.TrimSpace(entry.Type)
	if typ == "" {
		typ = "file"
	}
	if p == "" {
		typ = "directory"
	}
	insertID := entry.ID
	if typ == "directory" {
		// Directory IDs must be deterministic so child-parent pointers can be computed.
		insertID = nativeEntryID(sliceID, p)
	}

	// Ensure parent directories exist.
	if err := s.materializeDirectoryTreeTx(ctx, s.tx, sliceID, []string{p}, false); err != nil {
		return err
	}
	prevState, err := loadEntryAggregateState(ctx, s.tx, sliceID, p)
	if err != nil && err != ErrEntryNotFound {
		return err
	}
	storedSize := entry.Size
	if prevState != nil && typ == "directory" && storedSize == 0 && prevState.Type == "directory" {
		storedSize = prevState.Size
	}
	oldContribution := int64(0)
	if prevState != nil {
		oldContribution = directoryEntryAggregateContribution(&models.DirectoryEntry{Type: prevState.Type, Size: prevState.Size})
	}

	_, err = s.tx.Exec(ctx, `
		INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size, is_executable, symlink_target)
		VALUES ($1, $2, $3, $4, $5, NULL, $6, $7, $8)
		ON CONFLICT (slice_id, path) DO UPDATE SET
			type = EXCLUDED.type,
			parent_id = EXCLUDED.parent_id,
			size = EXCLUDED.size,
			is_executable = EXCLUDED.is_executable,
			symlink_target = EXCLUDED.symlink_target,
			updated_at = NOW()
	`, insertID, sliceID, p, typ, nativeParentID(sliceID, p), storedSize, entry.Executable, entry.SymlinkTarget)
	if err != nil {
		return err
	}
	newContribution := directoryEntryAggregateContribution(&models.DirectoryEntry{Type: typ, Size: storedSize})
	return adjustAncestorDirectorySizesTx(ctx, s.tx, sliceID, p, newContribution-oldContribution)
}

func (s *postgresNativeTxView) UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	ctx = ensureCtx(ctx)
	if entry == nil {
		return ErrInvalidInput
	}
	var sliceID string
	if err := s.tx.QueryRow(ctx, `SELECT slice_id FROM directory_entries WHERE id = $1`, entry.ID).Scan(&sliceID); err != nil {
		if err == pgx.ErrNoRows {
			return ErrEntryNotFound
		}
		return err
	}

	p := cleanRelativePath(entry.Path)
	typ := strings.TrimSpace(entry.Type)
	if typ == "" {
		typ = "file"
	}
	if p == "" {
		typ = "directory"
	}

	// Ensure parent directories exist.
	if err := s.materializeDirectoryTreeTx(ctx, s.tx, sliceID, []string{p}, false); err != nil {
		return err
	}
	prevState, err := loadEntryAggregateStateByID(ctx, s.tx, entry.ID)
	if err != nil {
		return err
	}
	storedSize := entry.Size
	if typ == "directory" && storedSize == 0 && prevState.Type == "directory" {
		storedSize = prevState.Size
	}
	oldContribution := directoryEntryAggregateContribution(&models.DirectoryEntry{Type: prevState.Type, Size: prevState.Size})

	tag, err := s.tx.Exec(ctx, `
		UPDATE directory_entries SET path = $1, type = $2, parent_id = $3, content = NULL, size = $4, is_executable = $5, symlink_target = $6, updated_at = NOW()
		WHERE id = $7
	`, p, typ, nativeParentID(sliceID, p), storedSize, entry.Executable, entry.SymlinkTarget, entry.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	newContribution := directoryEntryAggregateContribution(&models.DirectoryEntry{Type: typ, Size: storedSize})
	if prevState.Path == p {
		return adjustAncestorDirectorySizesTx(ctx, s.tx, sliceID, p, newContribution-oldContribution)
	}
	if err := adjustAncestorDirectorySizesTx(ctx, s.tx, sliceID, prevState.Path, -oldContribution); err != nil {
		return err
	}
	return adjustAncestorDirectorySizesTx(ctx, s.tx, sliceID, p, newContribution)
}

func (s *postgresNativeTxView) DeleteEntry(ctx context.Context, entryID string) error {
	ctx = ensureCtx(ctx)
	var sliceID, entryPath, entryType string
	var entrySize int64
	if err := s.tx.QueryRow(ctx, `
		SELECT slice_id, path, type, size
		FROM directory_entries
		WHERE id = $1
	`, entryID).Scan(&sliceID, &entryPath, &entryType, &entrySize); err != nil {
		if err == pgx.ErrNoRows {
			return ErrEntryNotFound
		}
		return err
	}
	tag, err := s.tx.Exec(ctx, `DELETE FROM directory_entries WHERE id = $1`, entryID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return adjustAncestorDirectorySizesTx(ctx, s.tx, sliceID, entryPath, -directoryEntryAggregateContribution(&models.DirectoryEntry{
		Type: entryType,
		Size: entrySize,
	}))
}

func (s *postgresNativeTxView) PutBlock(ctx context.Context, hash string, data []byte) error {
	return s.PostgresNativeStorage.PutBlock(ctx, hash, data)
}

func (s *postgresNativeTxView) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	return s.PostgresNativeStorage.GetBlock(ctx, hash)
}

func (s *postgresNativeTxView) GetBlocks(ctx context.Context, hashes []string) (map[string][]byte, error) {
	return s.PostgresNativeStorage.GetBlocks(ctx, hashes)
}

func (s *postgresNativeTxView) HasBlock(ctx context.Context, hash string) (bool, error) {
	return s.PostgresNativeStorage.HasBlock(ctx, hash)
}

func (s *postgresNativeTxView) PutBlocks(ctx context.Context, blocks map[string][]byte) error {
	return s.PostgresNativeStorage.PutBlocks(ctx, blocks)
}

func (s *postgresNativeTxView) PutFileManifest(ctx context.Context, sliceID, filePath string, manifest *models.FileManifest) error {
	ctx = ensureCtx(ctx)
	if manifest == nil {
		return ErrInvalidInput
	}

	sliceID = strings.TrimSpace(sliceID)
	filePath = cleanRelativePath(filePath)
	if sliceID == "" || filePath == "" {
		return ErrInvalidInput
	}

	canonical := cloneManifest(manifest)
	canonical.Path = filePath
	if strings.TrimSpace(canonical.Hash) == "" {
		return ErrInvalidInput
	}
	if err := s.PutVersionedFileManifest(ctx, canonical); err != nil {
		return err
	}
	return s.putFileManifestReference(ctx, sliceID, filePath, canonical)
}

func (s *postgresNativeTxView) putFileManifestReference(ctx context.Context, sliceID, filePath string, manifest *models.FileManifest) error {
	return putPostgresFileManifestReference(ctx, s.tx, sliceID, filePath, manifest)
}

func (s *postgresNativeTxView) GetFileManifest(ctx context.Context, sliceID, filePath string) (*models.FileManifest, error) {
	return s.PostgresNativeStorage.getFileManifest(ctx, s.tx, sliceID, filePath)
}

func (s *postgresNativeTxView) GetFileManifestHashes(ctx context.Context, sliceID string, paths []string) (map[string]string, error) {
	ctx = ensureCtx(ctx)
	cleanedPaths := normalizeRelativePaths(paths)
	result := make(map[string]string, len(cleanedPaths))
	if len(cleanedPaths) == 0 {
		return result, nil
	}

	rows, err := s.tx.Query(ctx, `
		SELECT path, hash
		FROM file_manifests
		WHERE slice_id = $1 AND path = ANY($2)
	`, strings.TrimSpace(sliceID), cleanedPaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var filePath, hash string
		if err := rows.Scan(&filePath, &hash); err != nil {
			return nil, err
		}
		result[filePath] = strings.TrimSpace(hash)
	}
	return result, rows.Err()
}

func (s *postgresNativeTxView) DeleteFileManifest(ctx context.Context, sliceID, filePath string) error {
	ctx = ensureCtx(ctx)
	sliceID = strings.TrimSpace(sliceID)
	filePath = cleanRelativePath(filePath)
	if sliceID == "" || filePath == "" {
		return ErrInvalidInput
	}

	_, err := s.tx.Exec(ctx, `DELETE FROM file_manifests WHERE slice_id = $1 AND path = $2`, sliceID, filePath)
	if err != nil {
		return err
	}
	if err := s.objectStore.DeleteObject(ctx, s.objKey("manifests", sliceID, filePath)); err != nil && err != ErrEntryNotFound {
		return err
	}
	return nil
}

func (s *postgresNativeTxView) PutVersionedFileManifest(ctx context.Context, manifest *models.FileManifest) error {
	return s.PostgresNativeStorage.PutVersionedFileManifest(ctx, manifest)
}

func (s *postgresNativeTxView) GetVersionedFileManifest(ctx context.Context, hash string) (*models.FileManifest, error) {
	return s.PostgresNativeStorage.GetVersionedFileManifest(ctx, hash)
}

func (s *postgresNativeTxView) GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error) {
	ctx = ensureCtx(ctx)
	var cs models.CommitSnapshot
	var filesJSON []byte
	err := s.tx.QueryRow(ctx, `
		SELECT commit_hash, slice_id, files, committed_at FROM commit_snapshots WHERE commit_hash = $1
	`, commitHash).Scan(&cs.CommitHash, &cs.SliceID, &filesJSON, &cs.Timestamp)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(filesJSON, &cs.Files); err != nil {
		cs.Files = make(map[string]string)
	}
	return &cs, nil
}

func (s *postgresNativeTxView) SaveCommitSnapshot(ctx context.Context, snapshot *models.CommitSnapshot) error {
	ctx = ensureCtx(ctx)
	if snapshot == nil || snapshot.CommitHash == "" {
		return ErrInvalidInput
	}
	filesJSON, _ := json.Marshal(snapshot.Files)
	if snapshot.Files == nil {
		filesJSON = []byte("{}")
	}
	_, err := s.tx.Exec(ctx, `
		INSERT INTO commit_snapshots (commit_hash, slice_id, files, committed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (commit_hash) DO UPDATE SET slice_id = $2, files = $3, committed_at = $4
	`, snapshot.CommitHash, snapshot.SliceID, filesJSON, snapshot.Timestamp)
	return err
}

func (s *postgresNativeTxView) GetExistingEntriesByPaths(ctx context.Context, sliceID string, paths []string) (map[string]bool, error) {
	ctx = ensureCtx(ctx)
	cleanedPaths := normalizeRelativePaths(paths)
	result := make(map[string]bool, len(cleanedPaths))
	if len(cleanedPaths) == 0 {
		return result, nil
	}

	rows, err := s.tx.Query(ctx, `
		SELECT path
		FROM directory_entries
		WHERE slice_id = $1 AND path = ANY($2)
	`, strings.TrimSpace(sliceID), cleanedPaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for _, filePath := range cleanedPaths {
		result[filePath] = false
	}
	for rows.Next() {
		var filePath string
		if err := rows.Scan(&filePath); err != nil {
			return nil, err
		}
		result[filePath] = true
	}
	return result, rows.Err()
}

func (s *postgresNativeTxView) GetGlobalState(ctx context.Context) (*models.GlobalState, error) {
	ctx = ensureCtx(ctx)
	var gs models.GlobalState
	var stateJSON []byte
	err := s.tx.QueryRow(ctx, `
		SELECT global_commit_hash, updated_at, state_json FROM global_state WHERE id = true
	`).Scan(&gs.GlobalCommitHash, &gs.Timestamp, &stateJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return &models.GlobalState{
				GlobalCommitHash: ids.GenerateInitialCommitID(ids.RootSliceID),
				History:          []*models.GlobalCommit{},
			}, nil
		}
		return nil, err
	}
	history, err := loadGlobalCommitHistory(ctx, s.tx)
	if err != nil {
		history = decodeGlobalStateHistory(stateJSON)
	}
	gs.History = history
	return &gs, nil
}

func (s *postgresNativeTxView) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	ctx = ensureCtx(ctx)
	if state == nil {
		return ErrInvalidInput
	}
	if err := replaceGlobalCommitsTx(ctx, s.tx, state.History); err != nil {
		return err
	}
	stateJSON := globalStateJSONWithoutHistory()
	_, err := s.tx.Exec(ctx, `
		INSERT INTO global_state (id, global_commit_hash, updated_at, state_json)
		VALUES (true, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET global_commit_hash = $1, updated_at = $2, state_json = $3
	`, state.GlobalCommitHash, state.Timestamp, stateJSON)
	return err
}

func (s *postgresNativeTxView) AddFileChanges(ctx context.Context, changes []*models.FileChangeRecord) error {
	ctx = ensureCtx(ctx)
	if len(changes) == 0 {
		return nil
	}
	rows := fileChangeCopyRows(changes)
	if len(rows) == 0 {
		return nil
	}
	_, err := s.tx.CopyFrom(
		ctx,
		pgx.Identifier{"file_changes"},
		[]string{"id", "slice_id", "commit_hash", "path", "old_path", "change_type", "old_hash", "new_hash", "lines_added", "lines_deleted", "author", "message", "committed_at"},
		pgx.CopyFromRows(rows),
	)
	return err
}

// ============ Slice Operations ============

func (s *PostgresNativeStorage) CreateSlice(ctx context.Context, slice *models.Slice) error {
	ctx = ensureCtx(ctx)
	if slice.ID == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	slice.Visibility = models.NormalizeVisibility(slice.Visibility)
	slice.CreatedAt = now
	slice.UpdatedAt = now

	filesJSON, _ := json.Marshal(slice.Files)
	if slice.Files == nil {
		filesJSON = []byte("[]")
	}
	ownersJSON, _ := json.Marshal(slice.Owners)
	if slice.Owners == nil {
		ownersJSON = []byte("[]")
	}
	mountsJSON, _ := json.Marshal(slice.FolderMounts)
	if slice.FolderMounts == nil {
		mountsJSON = []byte("[]")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	slug, err := s.allocateSliceSlug(ctx, tx, slice)
	if err != nil {
		return err
	}
	slice.Slug = slug

	_, err = tx.Exec(ctx, `
		INSERT INTO slices (id, name, slug, description, created_by, parent_id, is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14)
	`, slice.ID, slice.Name, slice.Slug, slice.Description, slice.CreatedBy,
		slice.ParentSlice, slice.IsRoot, string(slice.Visibility), filesJSON, mountsJSON, ownersJSON,
		slice.CreatedAt, slice.UpdatedAt, slice.Environment)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrSliceAlreadyExists
		}
		return err
	}

	initialCommitHash := ids.GenerateInitialCommitID(slice.ID)
	_, err = tx.Exec(ctx, `
		INSERT INTO slice_metadata (slice_id, head_commit_hash, modified_files, last_modified, modified_files_count)
		VALUES ($1, $2, '[]', $3, 0)
	`, slice.ID, initialCommitHash, now)
	if err != nil {
		return err
	}

	// Index files from the slice definition.
	if err := insertFileSliceIndex(ctx, tx, slice.ID, slice.Files); err != nil {
		return err
	}

	// Materialize directory-entry tree from the slice file set so ListEntries can
	// list direct children without scanning all descendant paths.
	if err := s.materializeDirectoryTreeTx(ctx, tx, slice.ID, slice.Files, true); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) DeleteSlice(ctx context.Context, sliceID string) error {
	ctx = ensureCtx(ctx)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slices WHERE id = $1)`, sliceID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrSliceNotFound
	}

	if _, err := tx.Exec(ctx, `UPDATE slices SET parent_id = NULL WHERE parent_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_session_events WHERE session_id IN (SELECT session_id FROM agent_sessions WHERE slice_id = $1)`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_session_audit WHERE session_id IN (SELECT session_id FROM agent_sessions WHERE slice_id = $1)`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_sessions WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM repo_bindings WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM file_manifests WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM changesets WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM file_slice_index WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM slice_locks WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM file_locks WHERE owner_slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM directory_entries WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM file_changes WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM commit_snapshots WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM slice_commits WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM slice_metadata WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `DELETE FROM slices WHERE id = $1`, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}

	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) GetSlice(ctx context.Context, sliceID string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.pool, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE id = $1`, sliceID)
}

func (s *PostgresNativeStorage) ListSlices(ctx context.Context, limit, offset int) ([]*models.Slice, error) {
	ctx = ensureCtx(ctx)
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment
		FROM slices ORDER BY created_at LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.collectSlices(rows)
}

func (s *PostgresNativeStorage) CountSlices(ctx context.Context) (int, error) {
	ctx = ensureCtx(ctx)
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM slices`).Scan(&count)
	return count, err
}

func (s *PostgresNativeStorage) ListSlicesByOwner(ctx context.Context, owner string, limit, offset int) ([]*models.Slice, error) {
	ctx = ensureCtx(ctx)
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment
		FROM slices WHERE owners @> $1::jsonb ORDER BY created_at LIMIT $2 OFFSET $3
	`, fmt.Sprintf(`[%q]`, owner), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.collectSlices(rows)
}

func (s *PostgresNativeStorage) SearchSlices(ctx context.Context, query string, limit, offset int) ([]*models.Slice, error) {
	ctx = ensureCtx(ctx)
	pattern := "%" + query + "%"
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment
		FROM slices WHERE name ILIKE $1 OR description ILIKE $1 ORDER BY created_at LIMIT $2 OFFSET $3
	`, pattern, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.collectSlices(rows)
}

func (s *PostgresNativeStorage) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.pool, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE is_root = true LIMIT 1`)
}

func (s *PostgresNativeStorage) InitializeRootSlice(ctx context.Context) error {
	ctx = ensureCtx(ctx)

	// Check if root already exists.
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM slices WHERE is_root = true`).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	rootSlice := &models.Slice{
		ID:          ids.RootSliceID,
		Name:        "Root Slice",
		Description: "The root slice containing all files",
		Visibility:  models.VisibilityPrivate,
		Files:       []string{},
		Owners:      []string{"system"},
		CreatedBy:   "system",
		IsRoot:      true,
	}

	// Use CreateSlice but override the initial head commit hash afterward.
	if err := s.CreateSlice(ctx, rootSlice); err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		UPDATE slice_metadata SET head_commit_hash = $2 WHERE slice_id = $1
	`, rootSlice.ID, ids.GenerateInitialCommitID(rootSlice.ID))
	return err
}

func (s *PostgresNativeStorage) SetSliceFiles(ctx context.Context, sliceID string, files []string) error {
	ctx = ensureCtx(ctx)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var currentFiles []byte
	err = tx.QueryRow(ctx, `SELECT files FROM slices WHERE id = $1 FOR UPDATE`, sliceID).Scan(&currentFiles)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrSliceNotFound
		}
		return err
	}

	var existing []string
	if err := json.Unmarshal(currentFiles, &existing); err != nil {
		return err
	}
	if len(existing) > 0 {
		return ErrSliceFilesImmutable
	}

	filesJSON, _ := json.Marshal(files)
	_, err = tx.Exec(ctx, `UPDATE slices SET files = $1, updated_at = NOW() WHERE id = $2`, filesJSON, sliceID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// UpdateSliceName updates the display name of a slice.
func (s *PostgresNativeStorage) UpdateSliceName(ctx context.Context, sliceID, newName string) error {
	ctx = ensureCtx(ctx)

	tag, err := s.pool.Exec(ctx, `UPDATE slices SET name = $1, updated_at = NOW() WHERE id = $2`, newName, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) UpdateSliceVisibility(ctx context.Context, sliceID string, visibility models.Visibility) error {
	ctx = ensureCtx(ctx)

	tag, err := s.pool.Exec(ctx, `UPDATE slices SET visibility = $1, updated_at = NOW() WHERE id = $2`, string(models.NormalizeVisibility(visibility)), sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	return nil
}

// UpdateSliceEnvironment sets the default environment for a slice.
func (s *PostgresNativeStorage) UpdateSliceEnvironment(ctx context.Context, sliceID, environment string) error {
	ctx = ensureCtx(ctx)

	tag, err := s.pool.Exec(ctx, `UPDATE slices SET environment = $1, updated_at = NOW() WHERE id = $2`, environment, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) UpdateSliceFolderMounts(ctx context.Context, sliceID string, mounts []models.SliceFolderMount, files []string) error {
	ctx = ensureCtx(ctx)

	mountsJSON, err := json.Marshal(mounts)
	if err != nil {
		return err
	}
	if mounts == nil {
		mountsJSON = []byte("[]")
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return err
	}
	if files == nil {
		filesJSON = []byte("[]")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE slices SET folder_mounts = $1, files = $2, updated_at = NOW() WHERE id = $3`, mountsJSON, filesJSON, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM directory_entries WHERE slice_id = $1`, sliceID); err != nil {
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetPathVisibilityRule(ctx context.Context, p string) (*models.PathVisibilityRule, error) {
	return s.getPathVisibilityRule(ctx, s.pool, p)
}

func (s *PostgresNativeStorage) ListPathVisibilityRules(ctx context.Context, pathPrefix string) ([]*models.PathVisibilityRule, error) {
	return s.listPathVisibilityRules(ctx, s.pool, pathPrefix)
}

func (s *PostgresNativeStorage) UpsertPathVisibilityRule(ctx context.Context, rule *models.PathVisibilityRule) error {
	return s.upsertPathVisibilityRule(ctx, s.pool, rule)
}

func (s *PostgresNativeStorage) DeletePathVisibilityRule(ctx context.Context, p string) error {
	return s.deletePathVisibilityRule(ctx, s.pool, p)
}

// GetSliceByName retrieves the first non-root slice matching the given display name.
func (s *PostgresNativeStorage) GetSliceByName(ctx context.Context, name string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.pool, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE name = $1 AND is_root = false LIMIT 1`, name)
}

func (s *PostgresNativeStorage) GetSliceBySlug(ctx context.Context, slug string) (*models.Slice, error) {
	return s.getSliceBySlug(ctx, s.pool, slug)
}

func (s *PostgresNativeStorage) GetSliceByOwnerAndSlug(ctx context.Context, owner, slug string) (*models.Slice, error) {
	return s.getSliceByOwnerAndSlug(ctx, s.pool, owner, slug)
}

// ============ Metadata Operations ============

func (s *PostgresNativeStorage) GetSliceMetadata(ctx context.Context, sliceID string) (*models.SliceMetadata, error) {
	ctx = ensureCtx(ctx)

	var meta models.SliceMetadata
	var modifiedFilesJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT slice_id, head_commit_hash, modified_files, last_modified, modified_files_count
		FROM slice_metadata WHERE slice_id = $1
	`, sliceID).Scan(&meta.SliceID, &meta.HeadCommitHash, &modifiedFilesJSON, &meta.LastModified, &meta.ModifiedFilesCount)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrSliceNotFound
		}
		return nil, err
	}

	if err := json.Unmarshal(modifiedFilesJSON, &meta.ModifiedFiles); err != nil {
		meta.ModifiedFiles = []string{}
	}

	return &meta, nil
}

func (s *PostgresNativeStorage) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	ctx = ensureCtx(ctx)

	if metadata.LastModified.IsZero() {
		metadata.LastModified = time.Now()
	}

	modifiedJSON, _ := json.Marshal(metadata.ModifiedFiles)
	if metadata.ModifiedFiles == nil {
		modifiedJSON = []byte("[]")
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE slice_metadata
		SET head_commit_hash = $1, modified_files = $2, last_modified = $3, modified_files_count = $4
		WHERE slice_id = $5
	`, metadata.HeadCommitHash, modifiedJSON, metadata.LastModified, metadata.ModifiedFilesCount, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSliceNotFound
	}
	return nil
}

// ============ Commit Operations ============

func (s *PostgresNativeStorage) AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error {
	ctx = ensureCtx(ctx)

	// Verify slice exists.
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slices WHERE id = $1)`, sliceID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSliceNotFound
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO slice_commits (slice_id, commit_hash, parent_hash, message, committed_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (slice_id, commit_hash) DO NOTHING
	`, sliceID, commit.CommitHash, commit.ParentHash, commit.Message, commit.Timestamp)
	return err
}

func (s *PostgresNativeStorage) ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error) {
	ctx = ensureCtx(ctx)
	limit = normalizeSliceCommitLimit(limit)

	// Verify slice exists.
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slices WHERE id = $1)`, sliceID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrSliceNotFound
	}

	var rows pgx.Rows
	if fromCommitHash != "" {
		// Find the seq of the fromCommitHash, then get commits before it.
		rows, err = s.pool.Query(ctx, `
			SELECT commit_hash, parent_hash, message, committed_at
			FROM slice_commits
			WHERE slice_id = $1 AND seq < (
				SELECT seq FROM slice_commits WHERE slice_id = $1 AND commit_hash = $2 LIMIT 1
			)
			ORDER BY seq DESC
			LIMIT $3
		`, sliceID, fromCommitHash, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT commit_hash, parent_hash, message, committed_at
			FROM slice_commits WHERE slice_id = $1
			ORDER BY seq DESC
			LIMIT $2
		`, sliceID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commits []*models.Commit
	for rows.Next() {
		var c models.Commit
		if err := rows.Scan(&c.CommitHash, &c.ParentHash, &c.Message, &c.Timestamp); err != nil {
			return nil, err
		}
		commits = append(commits, &c)
	}
	if commits == nil {
		commits = []*models.Commit{}
	}
	return commits, rows.Err()
}

func (s *PostgresNativeStorage) GetCommitByHash(ctx context.Context, sliceID, commitHash string) (*models.Commit, error) {
	ctx = ensureCtx(ctx)

	var c models.Commit
	err := s.pool.QueryRow(ctx, `
		SELECT commit_hash, parent_hash, message, committed_at
		FROM slice_commits
		WHERE slice_id = $1 AND commit_hash = $2
		LIMIT 1
	`, sliceID, commitHash).Scan(&c.CommitHash, &c.ParentHash, &c.Message, &c.Timestamp)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}
	return &c, nil
}

// ============ File Indexing ============

func (s *PostgresNativeStorage) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	ctx = ensureCtx(ctx)

	// Check slice exists and whether it is root.
	var isRoot bool
	var filesJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT is_root, files FROM slices WHERE id = $1`, sliceID).Scan(&isRoot, &filesJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrSliceNotFound
		}
		return err
	}

	if isRoot {
		var files []string
		if err := json.Unmarshal(filesJSON, &files); err != nil {
			files = []string{}
		}
		for _, f := range files {
			if f == fileID {
				return nil // Already present
			}
		}
		files = append(files, fileID)
		updated, _ := json.Marshal(files)
		_, err = s.pool.Exec(ctx, `UPDATE slices SET files = $1, updated_at = NOW() WHERE id = $2`, updated, sliceID)
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO file_slice_index (file_id, slice_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, fileID, sliceID)
	return err
}

func (s *PostgresNativeStorage) AddFilesToSlice(ctx context.Context, fileIDs []string, sliceID string) error {
	ctx = ensureCtx(ctx)
	cleanedIDs := normalizeFileIndexIDs(fileIDs)
	if len(cleanedIDs) == 0 {
		return nil
	}

	var isRoot bool
	var filesJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT is_root, files FROM slices WHERE id = $1`, sliceID).Scan(&isRoot, &filesJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrSliceNotFound
		}
		return err
	}
	if isRoot {
		merged, err := mergeRootSliceFileIDs(filesJSON, cleanedIDs)
		if err != nil {
			return err
		}
		_, err = s.pool.Exec(ctx, `UPDATE slices SET files = $1, updated_at = NOW() WHERE id = $2`, merged, sliceID)
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO file_slice_index (file_id, slice_id)
		SELECT file_id, $2
		FROM unnest($1::text[]) AS file_id
		ON CONFLICT DO NOTHING
	`, cleanedIDs, sliceID)
	return err
}

func (s *PostgresNativeStorage) GetActiveSlicesForFile(ctx context.Context, fileID string) ([]string, error) {
	ctx = ensureCtx(ctx)

	rows, err := s.pool.Query(ctx, `SELECT slice_id FROM file_slice_index WHERE file_id = $1`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sliceIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		sliceIDs = append(sliceIDs, id)
	}
	if sliceIDs == nil {
		sliceIDs = []string{}
	}
	return sliceIDs, rows.Err()
}

func (s *PostgresNativeStorage) GetActiveSlicesForFiles(ctx context.Context, fileIDs []string) (map[string][]string, error) {
	ctx = ensureCtx(ctx)
	cleanedIDs := normalizeFileIndexIDs(fileIDs)
	result := make(map[string][]string, len(cleanedIDs))
	if len(cleanedIDs) == 0 {
		return result, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT file_id, slice_id
		FROM file_slice_index
		WHERE file_id = ANY($1)
		ORDER BY file_id, slice_id
	`, cleanedIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for _, fileID := range cleanedIDs {
		result[fileID] = []string{}
	}
	for rows.Next() {
		var fileID, mappedSliceID string
		if err := rows.Scan(&fileID, &mappedSliceID); err != nil {
			return nil, err
		}
		result[fileID] = append(result[fileID], mappedSliceID)
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error {
	ctx = ensureCtx(ctx)

	// Handle root slice: remove from the files array.
	var isRoot bool
	var filesJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT is_root, files FROM slices WHERE id = $1`, sliceID).Scan(&isRoot, &filesJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			// No-op for missing slice (matches in-memory behavior).
			_, _ = s.pool.Exec(ctx, `DELETE FROM file_slice_index WHERE file_id = $1 AND slice_id = $2`, fileID, sliceID)
			return nil
		}
		return err
	}

	if isRoot {
		var files []string
		if err := json.Unmarshal(filesJSON, &files); err != nil {
			files = []string{}
		}
		filtered := make([]string, 0, len(files))
		for _, f := range files {
			if f != fileID {
				filtered = append(filtered, f)
			}
		}
		updated, _ := json.Marshal(filtered)
		_, err = s.pool.Exec(ctx, `UPDATE slices SET files = $1, updated_at = NOW() WHERE id = $2`, updated, sliceID)
		if err != nil {
			return err
		}
	}

	_, err = s.pool.Exec(ctx, `DELETE FROM file_slice_index WHERE file_id = $1 AND slice_id = $2`, fileID, sliceID)
	return err
}

func (s *PostgresNativeStorage) ListConflicts(ctx context.Context) ([]*models.FileConflict, error) {
	ctx = ensureCtx(ctx)

	rows, err := s.pool.Query(ctx, `
		SELECT file_id, array_agg(slice_id ORDER BY slice_id)
		FROM file_slice_index
		GROUP BY file_id
		HAVING COUNT(*) >= 2
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conflicts []*models.FileConflict
	for rows.Next() {
		var fileID string
		var sliceIDs []string
		if err := rows.Scan(&fileID, &sliceIDs); err != nil {
			return nil, err
		}
		conflicts = append(conflicts, &models.FileConflict{
			FileID:            fileID,
			ConflictingSlices: sliceIDs,
		})
	}
	return conflicts, rows.Err()
}

func (s *PostgresNativeStorage) ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error) {
	ctx = ensureCtx(ctx)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Get current slices for this file.
	rows, err := tx.Query(ctx, `SELECT slice_id FROM file_slice_index WHERE file_id = $1`, fileID)
	if err != nil {
		return nil, err
	}
	var sliceIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		sliceIDs = append(sliceIDs, id)
	}
	rows.Close()

	if len(sliceIDs) == 0 {
		return &models.FileConflict{FileID: fileID, ConflictingSlices: []string{}}, tx.Commit(ctx)
	}

	// Determine which slice to keep.
	keepSlice := ""
	if preferredSliceID != "" {
		for _, id := range sliceIDs {
			if id == preferredSliceID {
				keepSlice = preferredSliceID
				break
			}
		}
		if keepSlice == "" {
			return nil, ErrInvalidInput
		}
	}
	if keepSlice == "" && len(sliceIDs) > 0 {
		sort.Strings(sliceIDs)
		keepSlice = sliceIDs[0]
	}

	// Remove all other mappings.
	_, err = tx.Exec(ctx, `DELETE FROM file_slice_index WHERE file_id = $1 AND slice_id != $2`, fileID, keepSlice)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &models.FileConflict{FileID: fileID, ConflictingSlices: []string{keepSlice}}, nil
}

// ============ Locking ============

func (s *PostgresNativeStorage) LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error {
	ctx = ensureCtx(ctx)

	// Verify slice exists.
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slices WHERE id = $1)`, sliceID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSliceNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Check for conflicting file locks.
	for _, fileID := range fileIDs {
		var owner string
		err := tx.QueryRow(ctx, `SELECT owner_slice_id FROM file_locks WHERE file_id = $1`, fileID).Scan(&owner)
		if err == nil && owner != sliceID {
			return ErrLockHeld
		}
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
	}

	// Acquire slice lock.
	tag, err := tx.Exec(ctx, `
		INSERT INTO slice_locks (slice_id) VALUES ($1)
		ON CONFLICT (slice_id) DO NOTHING
	`, sliceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLockHeld
	}

	// Acquire file locks.
	for _, fileID := range fileIDs {
		tag, err = tx.Exec(ctx, `
			INSERT INTO file_locks (file_id, owner_slice_id) VALUES ($1, $2)
			ON CONFLICT (file_id) DO NOTHING
		`, fileID, sliceID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrLockHeld
		}
	}

	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) UnlockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) {
	ctx = ensureCtx(ctx)

	_, _ = s.pool.Exec(ctx, `DELETE FROM slice_locks WHERE slice_id = $1`, sliceID)
	for _, fileID := range fileIDs {
		_, _ = s.pool.Exec(ctx, `DELETE FROM file_locks WHERE file_id = $1 AND owner_slice_id = $2`, fileID, sliceID)
	}
}

// ============ Index Maintenance ============

func (s *PostgresNativeStorage) RebuildIndexes(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := rebuildDirectorySizesTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ============ Changeset Operations ============

func (s *PostgresNativeStorage) CreateChangeset(ctx context.Context, changeset *models.Changeset) error {
	ctx = ensureCtx(ctx)
	if changeset == nil {
		return ErrInvalidInput
	}

	// Verify slice exists.
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slices WHERE id = $1)`, changeset.SliceID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrSliceNotFound
	}

	modifiedJSON, _ := json.Marshal(changeset.ModifiedFiles)
	if changeset.ModifiedFiles == nil {
		modifiedJSON = []byte("[]")
	}
	if strings.TrimSpace(changeset.ID) == "" {
		var nextID int64
		if err := s.pool.QueryRow(ctx, `SELECT nextval('changeset_id_seq')`).Scan(&nextID); err != nil {
			return err
		}
		changeset.ID = fmt.Sprintf("chg_%d", nextID)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO changesets (id, hash, slice_id, base_commit_hash, modified_files, status, author, message, created_at, merged_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, changeset.ID, changeset.Hash, changeset.SliceID, changeset.BaseCommitHash,
		modifiedJSON, int(changeset.Status), changeset.Author, changeset.Message,
		changeset.CreatedAt, changeset.MergedAt)
	return err
}

func (s *PostgresNativeStorage) GetChangeset(ctx context.Context, changesetID string) (*models.Changeset, error) {
	ctx = ensureCtx(ctx)

	var cs models.Changeset
	var modifiedJSON []byte
	var status int
	err := s.pool.QueryRow(ctx, `
		SELECT id, hash, slice_id, base_commit_hash, modified_files, status, author, message, created_at, merged_at
		FROM changesets WHERE id = $1
	`, changesetID).Scan(&cs.ID, &cs.Hash, &cs.SliceID, &cs.BaseCommitHash,
		&modifiedJSON, &status, &cs.Author, &cs.Message, &cs.CreatedAt, &cs.MergedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrChangesetNotFound
		}
		return nil, err
	}

	cs.Status = models.ChangesetStatus(status)
	if err := json.Unmarshal(modifiedJSON, &cs.ModifiedFiles); err != nil {
		cs.ModifiedFiles = []string{}
	}

	return &cs, nil
}

func (s *PostgresNativeStorage) ListChangesets(ctx context.Context, sliceID string, status *models.ChangesetStatus, limit int) ([]*models.Changeset, error) {
	ctx = ensureCtx(ctx)

	var rows pgx.Rows
	var err error

	if status != nil {
		rows, err = s.pool.Query(ctx, `
			SELECT id, hash, slice_id, base_commit_hash, modified_files, status, author, message, created_at, merged_at
			FROM changesets WHERE slice_id = $1 AND status = $2
			ORDER BY created_at DESC LIMIT $3
		`, sliceID, int(*status), limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, hash, slice_id, base_commit_hash, modified_files, status, author, message, created_at, merged_at
			FROM changesets WHERE slice_id = $1
			ORDER BY created_at DESC LIMIT $2
		`, sliceID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*models.Changeset
	for rows.Next() {
		var cs models.Changeset
		var modifiedJSON []byte
		var statusInt int
		if err := rows.Scan(&cs.ID, &cs.Hash, &cs.SliceID, &cs.BaseCommitHash,
			&modifiedJSON, &statusInt, &cs.Author, &cs.Message, &cs.CreatedAt, &cs.MergedAt); err != nil {
			return nil, err
		}
		cs.Status = models.ChangesetStatus(statusInt)
		if err := json.Unmarshal(modifiedJSON, &cs.ModifiedFiles); err != nil {
			cs.ModifiedFiles = []string{}
		}
		result = append(result, &cs)
	}
	if result == nil {
		result = []*models.Changeset{}
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) UpdateChangeset(ctx context.Context, changeset *models.Changeset) error {
	ctx = ensureCtx(ctx)

	modifiedJSON, _ := json.Marshal(changeset.ModifiedFiles)
	if changeset.ModifiedFiles == nil {
		modifiedJSON = []byte("[]")
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE changesets
		SET hash = $1, slice_id = $2, base_commit_hash = $3, modified_files = $4,
		    status = $5, author = $6, message = $7, merged_at = $8
		WHERE id = $9
	`, changeset.Hash, changeset.SliceID, changeset.BaseCommitHash, modifiedJSON,
		int(changeset.Status), changeset.Author, changeset.Message, changeset.MergedAt, changeset.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrChangesetNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) CreateChangesetSnapshot(ctx context.Context, snapshot *models.ChangesetSnapshot) error {
	ctx = ensureCtx(ctx)
	if snapshot == nil || snapshot.ID == "" || snapshot.ChangesetID == "" || snapshot.Version <= 0 {
		return ErrInvalidInput
	}

	modifiedJSON, _ := json.Marshal(snapshot.ModifiedFiles)
	if snapshot.ModifiedFiles == nil {
		modifiedJSON = []byte("[]")
	}
	var fileHashesJSON any
	if snapshot.FileHashes != nil {
		rawFileHashesJSON, _ := json.Marshal(snapshot.FileHashes)
		fileHashesJSON = rawFileHashesJSON
	}
	var basePathVersionsJSON any
	if snapshot.BasePathVersions != nil {
		rawBasePathVersionsJSON, _ := json.Marshal(snapshot.BasePathVersions)
		basePathVersionsJSON = rawBasePathVersionsJSON
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO changeset_snapshots (id, changeset_id, version, hash, base_commit_hash, modified_files, file_hashes, base_path_versions, author, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, snapshot.ID, snapshot.ChangesetID, snapshot.Version, snapshot.Hash, snapshot.BaseCommitHash,
		modifiedJSON, fileHashesJSON, basePathVersionsJSON, snapshot.Author, snapshot.Message, snapshot.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "changeset_snapshots_changeset_id_fkey") {
			return ErrChangesetNotFound
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetChangesetSnapshot(ctx context.Context, changesetID string, version int32) (*models.ChangesetSnapshot, error) {
	ctx = ensureCtx(ctx)

	var row pgx.Row
	if version <= 0 {
		row = s.pool.QueryRow(ctx, `
			SELECT id, changeset_id, version, hash, base_commit_hash, modified_files,
			       COALESCE(file_hashes, 'null'::jsonb), COALESCE(base_path_versions, 'null'::jsonb),
			       author, message, created_at
			FROM changeset_snapshots
			WHERE changeset_id = $1
			ORDER BY version DESC
			LIMIT 1
		`, changesetID)
	} else {
		row = s.pool.QueryRow(ctx, `
			SELECT id, changeset_id, version, hash, base_commit_hash, modified_files,
			       COALESCE(file_hashes, 'null'::jsonb), COALESCE(base_path_versions, 'null'::jsonb),
			       author, message, created_at
			FROM changeset_snapshots
			WHERE changeset_id = $1 AND version = $2
			LIMIT 1
		`, changesetID, version)
	}

	var snapshot models.ChangesetSnapshot
	var modifiedJSON []byte
	var fileHashesJSON []byte
	var basePathVersionsJSON []byte
	err := row.Scan(
		&snapshot.ID,
		&snapshot.ChangesetID,
		&snapshot.Version,
		&snapshot.Hash,
		&snapshot.BaseCommitHash,
		&modifiedJSON,
		&fileHashesJSON,
		&basePathVersionsJSON,
		&snapshot.Author,
		&snapshot.Message,
		&snapshot.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrChangesetNotFound
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
	return &snapshot, nil
}

func (s *PostgresNativeStorage) ListChangesetSnapshots(ctx context.Context, changesetID string, limit int) ([]*models.ChangesetSnapshot, error) {
	ctx = ensureCtx(ctx)

	if limit <= 0 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, changeset_id, version, hash, base_commit_hash, modified_files,
		       COALESCE(file_hashes, 'null'::jsonb), COALESCE(base_path_versions, 'null'::jsonb),
		       author, message, created_at
		FROM changeset_snapshots
		WHERE changeset_id = $1
		ORDER BY version DESC
		LIMIT $2
	`, changesetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*models.ChangesetSnapshot, 0)
	for rows.Next() {
		var snapshot models.ChangesetSnapshot
		var modifiedJSON []byte
		var fileHashesJSON []byte
		var basePathVersionsJSON []byte
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.ChangesetID,
			&snapshot.Version,
			&snapshot.Hash,
			&snapshot.BaseCommitHash,
			&modifiedJSON,
			&fileHashesJSON,
			&basePathVersionsJSON,
			&snapshot.Author,
			&snapshot.Message,
			&snapshot.CreatedAt,
		); err != nil {
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
		snapshotCopy := snapshot
		result = append(result, &snapshotCopy)
	}
	if result == nil {
		result = []*models.ChangesetSnapshot{}
	}
	return result, rows.Err()
}

// ============ Block-Backed File Content ============

func (s *PostgresNativeStorage) PutBlock(ctx context.Context, hash string, data []byte) error {
	ctx = ensureCtx(ctx)
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return ErrInvalidInput
	}
	return s.objectStore.PutObject(ctx, s.objKey("blocks", hash), append([]byte(nil), data...))
}

func (s *PostgresNativeStorage) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	ctx = ensureCtx(ctx)
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, ErrInvalidInput
	}
	return s.objectStore.GetObject(ctx, s.objKey("blocks", hash))
}

func (s *PostgresNativeStorage) GetBlocks(ctx context.Context, hashes []string) (map[string][]byte, error) {
	ctx = ensureCtx(ctx)
	if len(hashes) == 0 {
		return map[string][]byte{}, nil
	}

	unique := make([]string, 0, len(hashes))
	seen := make(map[string]struct{}, len(hashes))
	for _, rawHash := range hashes {
		hash := strings.TrimSpace(rawHash)
		if hash == "" {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[hash]; exists {
			continue
		}
		seen[hash] = struct{}{}
		unique = append(unique, hash)
	}

	type result struct {
		hash string
		data []byte
		err  error
	}

	workerCount := checkoutBlockFetchWorkerCount(len(unique))
	jobs := make(chan string)
	results := make(chan result, len(unique))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for hash := range jobs {
				data, err := s.objectStore.GetObject(ctx, s.objKey("blocks", hash))
				results <- result{hash: hash, data: data, err: err}
			}
		}()
	}
	for _, hash := range unique {
		jobs <- hash
	}
	close(jobs)
	wg.Wait()
	close(results)

	blocks := make(map[string][]byte, len(unique))
	for result := range results {
		if result.err != nil {
			return nil, result.err
		}
		blocks[result.hash] = result.data
	}
	return blocks, nil
}

func (s *PostgresNativeStorage) HasBlock(ctx context.Context, hash string) (bool, error) {
	ctx = ensureCtx(ctx)
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return false, ErrInvalidInput
	}
	_, err := s.objectStore.GetObject(ctx, s.objKey("blocks", hash))
	if err == nil {
		return true, nil
	}
	if err == ErrEntryNotFound {
		return false, nil
	}
	return false, err
}

func (s *PostgresNativeStorage) PutBlocks(ctx context.Context, blocks map[string][]byte) error {
	ctx = ensureCtx(ctx)
	for hash, data := range blocks {
		if err := s.PutBlock(ctx, hash, data); err != nil {
			return err
		}
	}
	return nil
}

func checkoutBlockFetchWorkerCount(jobs int) int {
	if jobs <= 0 {
		return 0
	}
	workers := runtime.GOMAXPROCS(0) * 4
	if workers < 4 {
		workers = 4
	}
	if workers > 32 {
		workers = 32
	}
	if jobs < workers {
		return jobs
	}
	return workers
}

func (s *PostgresNativeStorage) PutFileManifest(ctx context.Context, sliceID, filePath string, manifest *models.FileManifest) error {
	ctx = ensureCtx(ctx)
	if manifest == nil {
		return ErrInvalidInput
	}

	sliceID = strings.TrimSpace(sliceID)
	filePath = cleanRelativePath(filePath)
	if sliceID == "" || filePath == "" {
		return ErrInvalidInput
	}

	canonical := cloneManifest(manifest)
	canonical.Path = filePath
	if strings.TrimSpace(canonical.Hash) == "" {
		return ErrInvalidInput
	}
	if err := s.PutVersionedFileManifest(ctx, canonical); err != nil {
		return err
	}
	return s.putFileManifestReference(ctx, sliceID, filePath, canonical)
}

func (s *PostgresNativeStorage) putFileManifestReference(ctx context.Context, sliceID, filePath string, manifest *models.FileManifest) error {
	return putPostgresFileManifestReference(ctx, s.pool, sliceID, filePath, manifest)
}

func putPostgresFileManifestReference(ctx context.Context, exec execable, sliceID, filePath string, manifest *models.FileManifest) error {
	sliceID = strings.TrimSpace(sliceID)
	filePath = cleanRelativePath(filePath)
	if sliceID == "" || filePath == "" || manifest == nil {
		return ErrInvalidInput
	}
	canonical := cloneManifest(manifest)
	canonical.Path = filePath
	if strings.TrimSpace(canonical.Hash) == "" {
		return ErrInvalidInput
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO file_manifests (slice_id, path, hash, total_size, block_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (slice_id, path) DO UPDATE SET hash = $3, total_size = $4, block_count = $5, updated_at = NOW()
	`, sliceID, filePath, canonical.Hash, canonical.TotalSize, len(canonical.Blocks))
	return err
}

func (s *PostgresNativeStorage) GetFileManifest(ctx context.Context, sliceID, filePath string) (*models.FileManifest, error) {
	ctx = ensureCtx(ctx)
	return s.getFileManifest(ctx, s.pool, sliceID, filePath)
}

func (s *PostgresNativeStorage) getFileManifest(ctx context.Context, q queryable, sliceID, filePath string) (*models.FileManifest, error) {
	sliceID = strings.TrimSpace(sliceID)
	filePath = cleanRelativePath(filePath)
	if sliceID == "" || filePath == "" {
		return nil, ErrInvalidInput
	}

	var hash string
	var totalSize int64
	var blockCount int
	err := q.QueryRow(ctx, `
		SELECT hash, total_size, block_count
		FROM file_manifests
		WHERE slice_id = $1 AND path = $2
	`, sliceID, filePath).Scan(&hash, &totalSize, &blockCount)
	if err == nil {
		manifest, manifestErr := s.GetVersionedFileManifest(ctx, hash)
		if manifestErr == nil && manifest != nil {
			manifest.Path = filePath
			manifest.Hash = strings.TrimSpace(hash)
			if manifest.TotalSize == 0 && totalSize > 0 {
				manifest.TotalSize = totalSize
			}
			return manifest, nil
		}
		if manifestErr != nil && manifestErr != ErrEntryNotFound {
			return nil, manifestErr
		}
	} else if err != pgx.ErrNoRows {
		return nil, err
	}

	// Legacy fallback for namespaces written before manifests became hash-addressed.
	raw, err := s.objectStore.GetObject(ctx, s.objKey("manifests", sliceID, filePath))
	if err != nil {
		if err == ErrEntryNotFound {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}

	var manifest models.FileManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	manifest.Path = filePath
	if strings.TrimSpace(manifest.Hash) != "" {
		if err := s.PutVersionedFileManifest(ctx, &manifest); err != nil {
			return nil, err
		}
	}
	return cloneManifest(&manifest), nil
}

func (s *PostgresNativeStorage) GetFileManifestHashes(ctx context.Context, sliceID string, paths []string) (map[string]string, error) {
	ctx = ensureCtx(ctx)
	cleanedPaths := normalizeRelativePaths(paths)
	result := make(map[string]string, len(cleanedPaths))
	if len(cleanedPaths) == 0 {
		return result, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT path, hash
		FROM file_manifests
		WHERE slice_id = $1 AND path = ANY($2)
	`, strings.TrimSpace(sliceID), cleanedPaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var filePath, hash string
		if err := rows.Scan(&filePath, &hash); err != nil {
			return nil, err
		}
		result[filePath] = strings.TrimSpace(hash)
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) PromoteFilesToRoot(ctx context.Context, rootSliceID string, jobs []RootPromotionJob) error {
	return s.PromoteFilesToSlice(ctx, rootSliceID, jobs)
}

func (s *PostgresNativeStorage) PromoteFilesToSlice(ctx context.Context, targetSliceID string, jobs []RootPromotionJob) error {
	ctx = ensureCtx(ctx)
	targetSliceID = strings.TrimSpace(targetSliceID)
	if targetSliceID == "" {
		return ErrInvalidInput
	}
	if len(jobs) == 0 {
		return nil
	}

	type promotionRequest struct {
		sourceSliceID string
		filePath      string
		ordinal       int32
	}

	requests := make([]promotionRequest, 0)
	sourceSliceIDs := make([]string, 0)
	sourcePaths := make([]string, 0)
	ordinals := make([]int32, 0)
	var ordinal int32
	for _, job := range jobs {
		sourceSliceID := strings.TrimSpace(job.SliceID)
		if sourceSliceID == "" {
			continue
		}
		jobPaths := normalizeRelativePaths(job.Files)
		if len(jobPaths) == 0 {
			continue
		}
		for _, filePath := range jobPaths {
			requests = append(requests, promotionRequest{
				sourceSliceID: sourceSliceID,
				filePath:      filePath,
				ordinal:       ordinal,
			})
			sourceSliceIDs = append(sourceSliceIDs, sourceSliceID)
			sourcePaths = append(sourcePaths, filePath)
			ordinals = append(ordinals, ordinal)
			ordinal++
		}
	}
	if len(requests) == 0 {
		return nil
	}

	selected := make(map[string]postgresRootPromotionFile)
	selectedOrdinal := make(map[string]int32)
	rows, err := s.pool.Query(ctx, `
		WITH requested AS (
			SELECT source_slice_id, path, ordinal
			FROM unnest($1::text[], $2::text[], $3::int[]) AS r(source_slice_id, path, ordinal)
		),
		ranked AS (
			SELECT r.path, r.ordinal, fm.hash, fm.total_size, fm.block_count,
			       COALESCE(de.is_executable, false) AS is_executable,
			       COALESCE(de.symlink_target, '') AS symlink_target
			FROM requested r
			JOIN file_manifests fm
			  ON fm.slice_id = r.source_slice_id AND fm.path = r.path
			LEFT JOIN directory_entries de
			  ON de.slice_id = fm.slice_id AND de.path = fm.path
		),
		latest AS (
			SELECT path, ordinal, hash, total_size, block_count, is_executable, symlink_target,
			       ROW_NUMBER() OVER (PARTITION BY path ORDER BY ordinal DESC) AS rn
			FROM ranked
		)
		SELECT path, ordinal, hash, total_size, block_count, is_executable, symlink_target
		FROM latest
		WHERE rn = 1
	`, sourceSliceIDs, sourcePaths, ordinals)
	if err != nil {
		return fmt.Errorf("failed to load source manifest refs: %w", err)
	}
	for rows.Next() {
		var filePath, hash, symlinkTarget string
		var totalSize int64
		var blockCount int
		var requestOrdinal int32
		var executable bool
		if err := rows.Scan(&filePath, &requestOrdinal, &hash, &totalSize, &blockCount, &executable, &symlinkTarget); err != nil {
			rows.Close()
			return err
		}
		filePath = cleanRelativePath(filePath)
		if filePath == "" || strings.TrimSpace(hash) == "" {
			continue
		}
		selected[filePath] = postgresRootPromotionFile{
			Manifest: &models.FileManifest{
				Path:          filePath,
				Hash:          strings.TrimSpace(hash),
				TotalSize:     totalSize,
				Executable:    executable,
				SymlinkTarget: symlinkTarget,
			},
			BlockCount: blockCount,
		}
		selectedOrdinal[filePath] = requestOrdinal
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, request := range requests {
		if existingOrdinal, ok := selectedOrdinal[request.filePath]; ok && existingOrdinal >= request.ordinal {
			continue
		}
		manifest, err := s.GetFileManifest(ctx, request.sourceSliceID, request.filePath)
		if err != nil {
			if err == ErrEntryNotFound {
				continue
			}
			return fmt.Errorf("failed to load source manifest for %s in %s: %w", request.filePath, request.sourceSliceID, err)
		}
		manifest.Path = request.filePath
		selected[request.filePath] = postgresRootPromotionFile{
			Manifest:   cloneManifest(manifest),
			BlockCount: len(manifest.Blocks),
		}
		selectedOrdinal[request.filePath] = request.ordinal
	}
	if len(selected) == 0 {
		return nil
	}

	paths := make([]string, 0, len(selected))
	for filePath := range selected {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var isRoot bool
	if err := tx.QueryRow(ctx, `SELECT is_root FROM slices WHERE id = $1`, targetSliceID).Scan(&isRoot); err != nil {
		if err == pgx.ErrNoRows {
			return ErrSliceNotFound
		}
		return err
	}

	lockKeys := make([]string, 0, len(paths))
	for _, filePath := range paths {
		lockKeys = append(lockKeys, targetSliceID+":"+filePath)
	}
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended(lock_key, 0))
		FROM unnest($1::text[]) AS lock_key
		ORDER BY lock_key
	`, lockKeys); err != nil {
		return err
	}

	if err := s.materializeDirectoryTreeTx(ctx, tx, targetSliceID, paths, false); err != nil {
		return err
	}

	oldByPath := make(map[string]postgresEntryContribution, len(paths))
	rows, err = tx.Query(ctx, `
		SELECT path, type, size
		FROM directory_entries
		WHERE slice_id = $1 AND path = ANY($2)
	`, targetSliceID, paths)
	if err != nil {
		return err
	}
	for rows.Next() {
		var filePath, typ string
		var size int64
		if err := rows.Scan(&filePath, &typ, &size); err != nil {
			rows.Close()
			return err
		}
		oldByPath[filePath] = postgresEntryContribution{Type: typ, Size: size}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	ids := make([]string, 0, len(paths))
	parentIDs := make([]string, 0, len(paths))
	sizes := make([]int64, 0, len(paths))
	hashes := make([]string, 0, len(paths))
	blockCounts := make([]int, 0, len(paths))
	executables := make([]bool, 0, len(paths))
	symlinkTargets := make([]string, 0, len(paths))
	manifestByPath := make(map[string]*models.FileManifest, len(paths))
	for _, filePath := range paths {
		manifest := selected[filePath].Manifest
		if manifest == nil {
			continue
		}
		ids = append(ids, nativeEntryID(targetSliceID, filePath))
		parentIDs = append(parentIDs, nativeParentID(targetSliceID, filePath))
		sizes = append(sizes, manifest.TotalSize)
		hashes = append(hashes, strings.TrimSpace(manifest.Hash))
		blockCounts = append(blockCounts, selected[filePath].BlockCount)
		executables = append(executables, manifest.Executable)
		symlinkTargets = append(symlinkTargets, manifest.SymlinkTarget)
		manifestByPath[filePath] = manifest
	}
	if len(ids) == 0 {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO file_manifests (slice_id, path, hash, total_size, block_count)
		SELECT $1, path, hash, total_size, block_count
		FROM unnest($2::text[], $3::text[], $4::bigint[], $5::int[])
			AS rows(path, hash, total_size, block_count)
		ON CONFLICT (slice_id, path) DO UPDATE SET
			hash = EXCLUDED.hash,
			total_size = EXCLUDED.total_size,
			block_count = EXCLUDED.block_count,
			updated_at = NOW()
	`, targetSliceID, paths, hashes, sizes, blockCounts); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size, is_executable, symlink_target)
		SELECT id, $1, path, 'file', parent_id, NULL, size, is_executable, symlink_target
		FROM unnest($2::text[], $3::text[], $4::text[], $5::bigint[], $6::bool[], $7::text[])
			AS rows(id, path, parent_id, size, is_executable, symlink_target)
		ON CONFLICT (slice_id, path) DO UPDATE SET
			type = 'file',
			parent_id = EXCLUDED.parent_id,
			content = NULL,
			size = EXCLUDED.size,
			is_executable = EXCLUDED.is_executable,
			symlink_target = EXCLUDED.symlink_target,
			updated_at = NOW()
	`, targetSliceID, ids, paths, parentIDs, sizes, executables, symlinkTargets); err != nil {
		return err
	}

	if !isRoot {
		if _, err := tx.Exec(ctx, `
			INSERT INTO file_slice_index (file_id, slice_id)
			SELECT file_id, $2
			FROM unnest($1::text[]) AS file_id
			ON CONFLICT DO NOTHING
		`, paths, targetSliceID); err != nil {
			return err
		}
	}

	deltaPaths, deltas := aggregateImmediateParentDeltas(paths, oldByPath, manifestByPath)
	if len(deltaPaths) > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE directory_entries AS de
			SET size = GREATEST(0, de.size + rows.delta),
				updated_at = NOW()
			FROM unnest($2::text[], $3::bigint[]) AS rows(path, delta)
			WHERE de.slice_id = $1
			  AND de.type = 'directory'
			  AND de.path = rows.path
		`, targetSliceID, deltaPaths, deltas); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) DeleteFileManifest(ctx context.Context, sliceID, filePath string) error {
	ctx = ensureCtx(ctx)
	sliceID = strings.TrimSpace(sliceID)
	filePath = cleanRelativePath(filePath)
	if sliceID == "" || filePath == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `DELETE FROM file_manifests WHERE slice_id = $1 AND path = $2`, sliceID, filePath)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	if err := s.objectStore.DeleteObject(ctx, s.objKey("manifests", sliceID, filePath)); err != nil && err != ErrEntryNotFound {
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) PutVersionedFileManifest(ctx context.Context, manifest *models.FileManifest) error {
	ctx = ensureCtx(ctx)
	if manifest == nil || strings.TrimSpace(manifest.Hash) == "" {
		return ErrInvalidInput
	}

	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return s.objectStore.PutObject(ctx, s.objKey("versioned_manifests", strings.TrimSpace(manifest.Hash)), raw)
}

func (s *PostgresNativeStorage) GetVersionedFileManifest(ctx context.Context, hash string) (*models.FileManifest, error) {
	ctx = ensureCtx(ctx)
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, ErrInvalidInput
	}

	raw, err := s.objectStore.GetObject(ctx, s.objKey("versioned_manifests", hash))
	if err != nil {
		if err == ErrEntryNotFound {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	var manifest models.FileManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	return cloneManifest(&manifest), nil
}

func (s *PostgresNativeStorage) PutSearchIndexFileBlob(ctx context.Context, version uint32, searchContentHash string, payload []byte) error {
	ctx = ensureCtx(ctx)
	if version == 0 || strings.TrimSpace(searchContentHash) == "" {
		return ErrInvalidInput
	}
	return s.objectStore.PutObject(ctx, s.searchBlobObjectKey(version, searchContentHash), append([]byte(nil), payload...))
}

func (s *PostgresNativeStorage) GetSearchIndexFileBlob(ctx context.Context, version uint32, searchContentHash string) ([]byte, error) {
	ctx = ensureCtx(ctx)
	if version == 0 || strings.TrimSpace(searchContentHash) == "" {
		return nil, ErrInvalidInput
	}
	return s.objectStore.GetObject(ctx, s.searchBlobObjectKey(version, searchContentHash))
}

func (s *PostgresNativeStorage) PutSliceSearchArtifact(ctx context.Context, sliceID, commitHash string, version uint32, payload []byte) error {
	ctx = ensureCtx(ctx)
	if version == 0 || strings.TrimSpace(sliceID) == "" || strings.TrimSpace(commitHash) == "" {
		return ErrInvalidInput
	}
	return s.objectStore.PutObject(ctx, s.sliceSearchArtifactObjectKey(version, sliceID, commitHash), append([]byte(nil), payload...))
}

func (s *PostgresNativeStorage) GetSliceSearchArtifact(ctx context.Context, sliceID, commitHash string, version uint32) ([]byte, error) {
	ctx = ensureCtx(ctx)
	if version == 0 || strings.TrimSpace(sliceID) == "" || strings.TrimSpace(commitHash) == "" {
		return nil, ErrInvalidInput
	}
	return s.objectStore.GetObject(ctx, s.sliceSearchArtifactObjectKey(version, sliceID, commitHash))
}

func (s *PostgresNativeStorage) PutWorkspaceSearchArtifact(ctx context.Context, workspaceID string, version uint32, payload []byte) error {
	ctx = ensureCtx(ctx)
	if version == 0 || strings.TrimSpace(workspaceID) == "" {
		return ErrInvalidInput
	}
	return s.objectStore.PutObject(ctx, s.workspaceSearchArtifactObjectKey(version, workspaceID), append([]byte(nil), payload...))
}

func (s *PostgresNativeStorage) GetWorkspaceSearchArtifact(ctx context.Context, workspaceID string, version uint32) ([]byte, error) {
	ctx = ensureCtx(ctx)
	if version == 0 || strings.TrimSpace(workspaceID) == "" {
		return nil, ErrInvalidInput
	}
	return s.objectStore.GetObject(ctx, s.workspaceSearchArtifactObjectKey(version, workspaceID))
}

func (s *PostgresNativeStorage) DeleteWorkspaceSearchArtifact(ctx context.Context, workspaceID string, version uint32) error {
	ctx = ensureCtx(ctx)
	if version == 0 || strings.TrimSpace(workspaceID) == "" {
		return ErrInvalidInput
	}
	return s.objectStore.DeleteObject(ctx, s.workspaceSearchArtifactObjectKey(version, workspaceID))
}

// ============ Directory Entries ============

func (s *PostgresNativeStorage) AddEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	ctx = ensureCtx(ctx)
	if entry.ID == "" {
		return ErrInvalidInput
	}

	sliceID := inferSliceIDForEntry(entry)
	if sliceID == "" {
		return ErrInvalidInput
	}

	p := cleanRelativePath(entry.Path)
	typ := strings.TrimSpace(entry.Type)
	if typ == "" {
		typ = "file"
	}
	if p == "" {
		typ = "directory"
	}
	insertID := entry.ID
	if typ == "directory" {
		// Directory IDs must be deterministic so child-parent pointers can be computed.
		insertID = nativeEntryID(sliceID, p)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Ensure parent directories exist.
	if err := s.materializeDirectoryTreeTx(ctx, tx, sliceID, []string{p}, false); err != nil {
		return err
	}
	prevState, err := loadEntryAggregateState(ctx, tx, sliceID, p)
	if err != nil && err != ErrEntryNotFound {
		return err
	}
	storedSize := entry.Size
	if prevState != nil && typ == "directory" && storedSize == 0 && prevState.Type == "directory" {
		storedSize = prevState.Size
	}
	oldContribution := int64(0)
	if prevState != nil {
		oldContribution = directoryEntryAggregateContribution(&models.DirectoryEntry{Type: prevState.Type, Size: prevState.Size})
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size, is_executable, symlink_target)
		VALUES ($1, $2, $3, $4, $5, NULL, $6, $7, $8)
		ON CONFLICT (slice_id, path) DO UPDATE SET
			type = EXCLUDED.type,
			parent_id = EXCLUDED.parent_id,
			size = EXCLUDED.size,
			is_executable = EXCLUDED.is_executable,
			symlink_target = EXCLUDED.symlink_target,
			updated_at = NOW()
	`, insertID, sliceID, p, typ, nativeParentID(sliceID, p), storedSize, entry.Executable, entry.SymlinkTarget)
	if err != nil {
		return err
	}
	newContribution := directoryEntryAggregateContribution(&models.DirectoryEntry{Type: typ, Size: storedSize})
	if err := adjustAncestorDirectorySizesTx(ctx, tx, sliceID, p, newContribution-oldContribution); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) GetEntry(ctx context.Context, entryID string) (*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)

	var e models.DirectoryEntry
	err := s.pool.QueryRow(ctx, `
		SELECT id, path, type, parent_id, content, size, is_executable, symlink_target,
			COALESCE((
				SELECT fm.hash
				FROM file_manifests fm
				WHERE fm.slice_id = directory_entries.slice_id AND fm.path = directory_entries.path
				LIMIT 1
			), '')
		FROM directory_entries
		WHERE id = $1
	`, entryID).Scan(&e.ID, &e.Path, &e.Type, &e.ParentID, &e.Content, &e.Size, &e.Executable, &e.SymlinkTarget, &e.Hash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (s *PostgresNativeStorage) GetEntryByPath(ctx context.Context, sliceID, path string) (*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)

	var e models.DirectoryEntry
	err := s.pool.QueryRow(ctx, `
		SELECT id, path, type, parent_id, content, size, is_executable, symlink_target,
			COALESCE((
				SELECT fm.hash
				FROM file_manifests fm
				WHERE fm.slice_id = directory_entries.slice_id AND fm.path = directory_entries.path
				LIMIT 1
			), '')
		FROM directory_entries
		WHERE slice_id = $1 AND path = $2
	`, sliceID, path).Scan(&e.ID, &e.Path, &e.Type, &e.ParentID, &e.Content, &e.Size, &e.Executable, &e.SymlinkTarget, &e.Hash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (s *PostgresNativeStorage) GetExistingEntriesByPaths(ctx context.Context, sliceID string, paths []string) (map[string]bool, error) {
	ctx = ensureCtx(ctx)
	cleanedPaths := normalizeRelativePaths(paths)
	result := make(map[string]bool, len(cleanedPaths))
	if len(cleanedPaths) == 0 {
		return result, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT path
		FROM directory_entries
		WHERE slice_id = $1 AND path = ANY($2)
	`, strings.TrimSpace(sliceID), cleanedPaths)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for _, filePath := range cleanedPaths {
		result[filePath] = false
	}
	for rows.Next() {
		var filePath string
		if err := rows.Scan(&filePath); err != nil {
			return nil, err
		}
		result[filePath] = true
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) ListEntriesByPathPrefixes(ctx context.Context, sliceID string, prefixes []string) ([]*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)
	cleanedPrefixes := normalizeRelativePaths(prefixes)
	if len(cleanedPrefixes) == 0 {
		return nil, nil
	}
	likePatterns := make([]string, 0, len(cleanedPrefixes))
	for _, prefix := range cleanedPrefixes {
		likePatterns = append(likePatterns, postgresLikeDescendantPattern(prefix))
	}

	rows, err := s.pool.Query(ctx, `
		SELECT de.id, de.path, de.type, de.parent_id, de.content, de.size, de.is_executable, de.symlink_target,
			COALESCE(fm.hash, '')
		FROM directory_entries de
		JOIN unnest($2::text[], $3::text[]) AS prefix(path_prefix, descendant_pattern)
			ON de.path = prefix.path_prefix OR de.path LIKE prefix.descendant_pattern ESCAPE '\'
		LEFT JOIN file_manifests fm
			ON fm.slice_id = de.slice_id AND fm.path = de.path
		WHERE de.slice_id = $1
		ORDER BY de.path
	`, strings.TrimSpace(sliceID), cleanedPrefixes, likePatterns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]*models.DirectoryEntry, 0)
	seen := make(map[string]struct{})
	for rows.Next() {
		var e models.DirectoryEntry
		if err := rows.Scan(&e.ID, &e.Path, &e.Type, &e.ParentID, &e.Content, &e.Size, &e.Executable, &e.SymlinkTarget, &e.Hash); err != nil {
			return nil, err
		}
		key := e.ID + "\x00" + e.Path
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, &e)
	}
	return result, rows.Err()
}

func postgresLikeDescendantPattern(prefix string) string {
	var b strings.Builder
	for _, r := range cleanRelativePath(prefix) {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteString("/%")
	return b.String()
}

func (s *PostgresNativeStorage) ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)

	rows, err := s.pool.Query(ctx, `
		SELECT id, path, type, parent_id, content, size, is_executable, symlink_target,
			COALESCE((
				SELECT fm.hash
				FROM file_manifests fm
				WHERE fm.slice_id = directory_entries.slice_id AND fm.path = directory_entries.path
				LIMIT 1
			), '')
		FROM directory_entries
		WHERE slice_id = $1 AND parent_id = $2
		ORDER BY path
	`, sliceID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*models.DirectoryEntry
	for rows.Next() {
		var e models.DirectoryEntry
		if err := rows.Scan(&e.ID, &e.Path, &e.Type, &e.ParentID, &e.Content, &e.Size, &e.Executable, &e.SymlinkTarget, &e.Hash); err != nil {
			return nil, err
		}
		result = append(result, &e)
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	ctx = ensureCtx(ctx)

	var sliceID string
	if err := s.pool.QueryRow(ctx, `SELECT slice_id FROM directory_entries WHERE id = $1`, entry.ID).Scan(&sliceID); err != nil {
		if err == pgx.ErrNoRows {
			return ErrEntryNotFound
		}
		return err
	}

	p := cleanRelativePath(entry.Path)
	typ := strings.TrimSpace(entry.Type)
	if typ == "" {
		typ = "file"
	}
	if p == "" {
		typ = "directory"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Ensure parent directories exist.
	if err := s.materializeDirectoryTreeTx(ctx, tx, sliceID, []string{p}, false); err != nil {
		return err
	}
	prevState, err := loadEntryAggregateStateByID(ctx, tx, entry.ID)
	if err != nil {
		return err
	}
	storedSize := entry.Size
	if typ == "directory" && storedSize == 0 && prevState.Type == "directory" {
		storedSize = prevState.Size
	}
	oldContribution := directoryEntryAggregateContribution(&models.DirectoryEntry{Type: prevState.Type, Size: prevState.Size})

	tag, err := tx.Exec(ctx, `
		UPDATE directory_entries SET path = $1, type = $2, parent_id = $3, content = NULL, size = $4, is_executable = $5, symlink_target = $6, updated_at = NOW()
		WHERE id = $7
	`, p, typ, nativeParentID(sliceID, p), storedSize, entry.Executable, entry.SymlinkTarget, entry.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	newContribution := directoryEntryAggregateContribution(&models.DirectoryEntry{Type: typ, Size: storedSize})
	if prevState.Path == p {
		if err := adjustAncestorDirectorySizesTx(ctx, tx, sliceID, p, newContribution-oldContribution); err != nil {
			return err
		}
	} else {
		if err := adjustAncestorDirectorySizesTx(ctx, tx, sliceID, prevState.Path, -oldContribution); err != nil {
			return err
		}
		if err := adjustAncestorDirectorySizesTx(ctx, tx, sliceID, p, newContribution); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) DeleteEntry(ctx context.Context, entryID string) error {
	ctx = ensureCtx(ctx)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var sliceID, entryPath, entryType string
	var entrySize int64
	if err := tx.QueryRow(ctx, `
		SELECT slice_id, path, type, size
		FROM directory_entries
		WHERE id = $1
	`, entryID).Scan(&sliceID, &entryPath, &entryType, &entrySize); err != nil {
		if err == pgx.ErrNoRows {
			return ErrEntryNotFound
		}
		return err
	}

	tag, err := tx.Exec(ctx, `DELETE FROM directory_entries WHERE id = $1`, entryID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	if err := adjustAncestorDirectorySizesTx(ctx, tx, sliceID, entryPath, -directoryEntryAggregateContribution(&models.DirectoryEntry{
		Type: entryType,
		Size: entrySize,
	})); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ============ Global State ============

func (s *PostgresNativeStorage) GetGlobalState(ctx context.Context) (*models.GlobalState, error) {
	ctx = ensureCtx(ctx)

	var gs models.GlobalState
	var stateJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT global_commit_hash, updated_at, state_json FROM global_state WHERE id = true
	`).Scan(&gs.GlobalCommitHash, &gs.Timestamp, &stateJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			// Return default state.
			return &models.GlobalState{
				GlobalCommitHash: ids.GenerateInitialCommitID(ids.RootSliceID),
				History:          []*models.GlobalCommit{},
			}, nil
		}
		return nil, err
	}

	history, err := loadGlobalCommitHistory(ctx, s.pool)
	if err != nil {
		history = decodeGlobalStateHistory(stateJSON)
	}
	gs.History = history

	return &gs, nil
}

func (s *PostgresNativeStorage) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	ctx = ensureCtx(ctx)

	if state == nil {
		return ErrInvalidInput
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := replaceGlobalCommitsTx(ctx, tx, state.History); err != nil {
		return err
	}
	stateJSON := globalStateJSONWithoutHistory()

	if _, err := tx.Exec(ctx, `
		INSERT INTO global_state (id, global_commit_hash, updated_at, state_json)
		VALUES (true, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET global_commit_hash = $1, updated_at = $2, state_json = $3
	`, state.GlobalCommitHash, state.Timestamp, stateJSON); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) UpdateRootPromotionState(ctx context.Context, rootSliceID string, latestCommitHash string, latestTime time.Time, latestFiles []string, commits []*models.GlobalCommit) error {
	ctx = ensureCtx(ctx)
	rootSliceID = strings.TrimSpace(rootSliceID)
	latestCommitHash = strings.TrimSpace(latestCommitHash)
	if rootSliceID == "" || latestCommitHash == "" {
		return ErrInvalidInput
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO global_state (id, global_commit_hash, updated_at, state_json)
		VALUES (true, $1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`, latestCommitHash, latestTime, globalStateJSONWithoutHistory()); err != nil {
		return err
	}
	if err := appendGlobalCommitsTx(ctx, tx, commits); err != nil {
		return err
	}
	initialCommitHash := ids.GenerateInitialCommitID(ids.RootSliceID)
	if _, err := tx.Exec(ctx, `
		UPDATE global_state
		SET global_commit_hash = $1,
			updated_at = $2,
			state_json = $3
		WHERE id = true
		  AND (global_commit_hash = '' OR global_commit_hash = $4 OR updated_at <= $2)
	`, latestCommitHash, latestTime, globalStateJSONWithoutHistory(), initialCommitHash); err != nil {
		return err
	}

	cleanedFiles := normalizeRelativePaths(latestFiles)
	modifiedJSON, _ := json.Marshal(cleanedFiles)
	tag, err := tx.Exec(ctx, `
		UPDATE slice_metadata
		SET head_commit_hash = $1,
			modified_files = $2,
			last_modified = $3,
			modified_files_count = $4
		WHERE slice_id = $5
		  AND (head_commit_hash = '' OR head_commit_hash = $6 OR last_modified <= $3)
	`, latestCommitHash, modifiedJSON, latestTime, len(cleanedFiles), rootSliceID, initialCommitHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM slice_metadata WHERE slice_id = $1)`, rootSliceID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrSliceNotFound
		}
	}
	return tx.Commit(ctx)
}

// ============ Health Check ============

func (s *PostgresNativeStorage) Ping(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	if err := s.PingMetadata(ctx); err != nil {
		return err
	}

	key := s.objKey("healthcheck")
	if err := s.objectStore.PutObject(ctx, key, []byte("ok")); err != nil {
		return err
	}
	_, err := s.objectStore.GetObject(ctx, key)
	_ = s.objectStore.DeleteObject(ctx, key)
	return err
}

func (s *PostgresNativeStorage) PingMetadata(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	return s.pool.Ping(ctx)
}

// ============ Commit Snapshots ============

func (s *PostgresNativeStorage) GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error) {
	ctx = ensureCtx(ctx)

	var cs models.CommitSnapshot
	var filesJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT commit_hash, slice_id, files, committed_at FROM commit_snapshots WHERE commit_hash = $1
	`, commitHash).Scan(&cs.CommitHash, &cs.SliceID, &filesJSON, &cs.Timestamp)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrCommitNotFound
		}
		return nil, err
	}

	if err := json.Unmarshal(filesJSON, &cs.Files); err != nil {
		cs.Files = make(map[string]string)
	}

	return &cs, nil
}

func (s *PostgresNativeStorage) SaveCommitSnapshot(ctx context.Context, snapshot *models.CommitSnapshot) error {
	ctx = ensureCtx(ctx)
	if snapshot.CommitHash == "" {
		return ErrInvalidInput
	}

	filesJSON, _ := json.Marshal(snapshot.Files)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO commit_snapshots (commit_hash, slice_id, files, committed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (commit_hash) DO UPDATE SET slice_id = $2, files = $3, committed_at = $4
	`, snapshot.CommitHash, snapshot.SliceID, filesJSON, snapshot.Timestamp)
	return err
}

func (s *PostgresNativeStorage) GetFileAtCommit(ctx context.Context, commitHash, path string) (*models.FileContent, error) {
	ctx = ensureCtx(ctx)

	snapshot, err := s.GetCommitSnapshot(ctx, commitHash)
	if err != nil {
		return nil, err
	}

	contentHash, exists := snapshot.Files[path]
	if !exists {
		return nil, ErrEntryNotFound
	}

	content, err := ReadVersionedFileContent(ctx, s, contentHash)
	if err != nil {
		return nil, err
	}
	content.Path = path
	content.FileID = path
	return content, nil
}

func (s *PostgresNativeStorage) ListFilesAtCommit(ctx context.Context, commitHash, pathPrefix string) ([]string, error) {
	ctx = ensureCtx(ctx)

	snapshot, err := s.GetCommitSnapshot(ctx, commitHash)
	if err != nil {
		return nil, err
	}

	var files []string
	for path := range snapshot.Files {
		if pathPrefix == "" || strings.HasPrefix(path, pathPrefix) {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

// ============ File Change History ============

func (s *PostgresNativeStorage) AddFileChange(ctx context.Context, change *models.FileChangeRecord) error {
	ctx = ensureCtx(ctx)
	if change.ID == "" || change.Path == "" || change.CommitHash == "" {
		return ErrInvalidInput
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO file_changes (id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			slice_id = EXCLUDED.slice_id,
			commit_hash = EXCLUDED.commit_hash,
			path = EXCLUDED.path,
			old_path = EXCLUDED.old_path,
			change_type = EXCLUDED.change_type,
			old_hash = EXCLUDED.old_hash,
			new_hash = EXCLUDED.new_hash,
			lines_added = EXCLUDED.lines_added,
			lines_deleted = EXCLUDED.lines_deleted,
			author = EXCLUDED.author,
			message = EXCLUDED.message,
			committed_at = EXCLUDED.committed_at
	`, change.ID, change.SliceID, change.CommitHash, change.Path, change.OldPath,
		string(change.ChangeType), change.OldHash, change.NewHash,
		change.LinesAdded, change.LinesDeleted, change.Author, change.Message, change.Timestamp)
	return err
}

func (s *PostgresNativeStorage) AddFileChanges(ctx context.Context, changes []*models.FileChangeRecord) error {
	ctx = ensureCtx(ctx)
	rows := fileChangeCopyRows(changes)
	if len(rows) == 0 {
		return nil
	}
	_, err := s.pool.CopyFrom(
		ctx,
		pgx.Identifier{"file_changes"},
		[]string{"id", "slice_id", "commit_hash", "path", "old_path", "change_type", "old_hash", "new_hash", "lines_added", "lines_deleted", "author", "message", "committed_at"},
		pgx.CopyFromRows(rows),
	)
	return err
}

func (s *PostgresNativeStorage) GetFileHistory(ctx context.Context, sliceID, path string, limit int, fromCommit string) ([]*models.FileChangeRecord, error) {
	ctx = ensureCtx(ctx)

	var rows pgx.Rows
	var err error

	if fromCommit != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at
			FROM file_changes
			WHERE slice_id = $1 AND path = $2 AND committed_at < (
				SELECT committed_at FROM file_changes WHERE slice_id = $1 AND path = $2 AND commit_hash = $3 LIMIT 1
			)
			ORDER BY committed_at DESC
			LIMIT $4
		`, sliceID, path, fromCommit, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at
			FROM file_changes WHERE slice_id = $1 AND path = $2
			ORDER BY committed_at DESC
			LIMIT $3
		`, sliceID, path, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.collectFileChanges(rows)
}

func (s *PostgresNativeStorage) GetDirectoryHistory(ctx context.Context, sliceID, pathPrefix string, limit int, fromCommit string) ([]*models.FileChangeRecord, error) {
	ctx = ensureCtx(ctx)

	if pathPrefix != "" && !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	var rows pgx.Rows
	var err error

	if fromCommit != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at
			FROM file_changes
			WHERE slice_id = $1 AND path LIKE $2 AND committed_at < (
				SELECT MIN(committed_at) FROM file_changes WHERE commit_hash = $3
			)
			ORDER BY committed_at DESC
			LIMIT $4
		`, sliceID, pathPrefix+"%", fromCommit, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at
			FROM file_changes WHERE slice_id = $1 AND path LIKE $2
			ORDER BY committed_at DESC
			LIMIT $3
		`, sliceID, pathPrefix+"%", limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.collectFileChanges(rows)
}

func (s *PostgresNativeStorage) GetCommitChanges(ctx context.Context, commitHash string) ([]*models.FileChangeRecord, error) {
	ctx = ensureCtx(ctx)

	rows, err := s.pool.Query(ctx, `
		SELECT id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at
		FROM file_changes WHERE commit_hash = $1 ORDER BY path
	`, commitHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.collectFileChanges(rows)
}

func (s *PostgresNativeStorage) QueryFileHistory(ctx context.Context, query *models.FileHistoryQuery) (*models.FileHistoryResult, error) {
	ctx = ensureCtx(ctx)

	var conditions []string
	var args []interface{}
	argIdx := 1

	if query.SliceID != "" {
		conditions = append(conditions, fmt.Sprintf("slice_id = $%d", argIdx))
		args = append(args, query.SliceID)
		argIdx++
	}
	if query.Path != "" {
		conditions = append(conditions, fmt.Sprintf("path = $%d", argIdx))
		args = append(args, query.Path)
		argIdx++
	}
	if query.PathPrefix != "" {
		prefix := query.PathPrefix
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		conditions = append(conditions, fmt.Sprintf("path LIKE $%d", argIdx))
		args = append(args, prefix+"%")
		argIdx++
	}
	if len(query.ChangeTypes) > 0 {
		types := make([]string, len(query.ChangeTypes))
		for i, ct := range query.ChangeTypes {
			types[i] = string(ct)
		}
		conditions = append(conditions, fmt.Sprintf("change_type = ANY($%d)", argIdx))
		args = append(args, types)
		argIdx++
	}
	if query.Author != "" {
		conditions = append(conditions, fmt.Sprintf("author = $%d", argIdx))
		args = append(args, query.Author)
		argIdx++
	}
	if query.FromTimestamp != nil {
		conditions = append(conditions, fmt.Sprintf("committed_at >= $%d", argIdx))
		args = append(args, *query.FromTimestamp)
		argIdx++
	}
	if query.ToTimestamp != nil {
		conditions = append(conditions, fmt.Sprintf("committed_at <= $%d", argIdx))
		args = append(args, *query.ToTimestamp)
		argIdx++
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Get total count.
	var totalCount int
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM file_changes %s", where)
	err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&totalCount)
	if err != nil {
		return nil, err
	}

	// Get paginated results.
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := query.Offset

	dataSQL := fmt.Sprintf(`
		SELECT id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at
		FROM file_changes %s ORDER BY committed_at DESC LIMIT $%d OFFSET $%d
	`, where, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, dataSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	changes, err := s.collectFileChanges(rows)
	if err != nil {
		return nil, err
	}

	hasMore := offset+len(changes) < totalCount

	return &models.FileHistoryResult{
		Changes:    changes,
		TotalCount: totalCount,
		HasMore:    hasMore,
	}, nil
}

func (s *PostgresNativeStorage) GetDirectorySummary(ctx context.Context, sliceID, pathPrefix string) (*models.DirectoryChangeSummary, error) {
	ctx = ensureCtx(ctx)

	if pathPrefix != "" && !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	// Count total changes and unique files.
	var totalChanges int
	var filesChanged int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT path)
		FROM file_changes WHERE slice_id = $1 AND path LIKE $2
	`, sliceID, pathPrefix+"%").Scan(&totalChanges, &filesChanged)
	if err != nil {
		return nil, err
	}

	if totalChanges == 0 {
		return &models.DirectoryChangeSummary{
			Path:          pathPrefix,
			TotalChanges:  0,
			FilesChanged:  0,
			ChangesByType: make(map[models.ChangeType]int),
		}, nil
	}

	// Get changes by type.
	changesByType := make(map[models.ChangeType]int)
	rows, err := s.pool.Query(ctx, `
		SELECT change_type, COUNT(*)
		FROM file_changes WHERE slice_id = $1 AND path LIKE $2
		GROUP BY change_type
	`, sliceID, pathPrefix+"%")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ct string
		var count int
		if err := rows.Scan(&ct, &count); err != nil {
			rows.Close()
			return nil, err
		}
		changesByType[models.ChangeType(ct)] = count
	}
	rows.Close()

	// Get last change.
	var lastChange models.FileChangeRecord
	var changeType string
	err = s.pool.QueryRow(ctx, `
		SELECT id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at
		FROM file_changes WHERE slice_id = $1 AND path LIKE $2
		ORDER BY committed_at DESC LIMIT 1
	`, sliceID, pathPrefix+"%").Scan(
		&lastChange.ID, &lastChange.SliceID, &lastChange.CommitHash, &lastChange.Path,
		&lastChange.OldPath, &changeType, &lastChange.OldHash, &lastChange.NewHash,
		&lastChange.LinesAdded, &lastChange.LinesDeleted, &lastChange.Author,
		&lastChange.Message, &lastChange.Timestamp)
	if err != nil {
		return nil, err
	}
	lastChange.ChangeType = models.ChangeType(changeType)

	return &models.DirectoryChangeSummary{
		Path:          pathPrefix,
		TotalChanges:  totalChanges,
		FilesChanged:  filesChanged,
		LastChange:    &lastChange,
		ChangesByType: changesByType,
	}, nil
}

// ============ Account Operations ============

func (s *PostgresNativeStorage) EnsureUser(ctx context.Context, username string) (*models.User, error) {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}
	if existing, err := s.GetUser(ctx, username); err == nil {
		return existing, nil
	} else if err != ErrEntryNotFound {
		return nil, err
	}
	rootPath := rootPathForSlug(username)

	var slugTaken bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE slug = $1)`, username).Scan(&slugTaken); err != nil {
		return nil, err
	}
	if slugTaken {
		return nil, ErrEntryExists
	}

	now := time.Now()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (username, account_id, name, primary_email, password_hash, auth_source, clerk_user_id, root_path, created_at, updated_at)
		VALUES ($1, NULL, '', '', '', '', '', $2, $3, $4)
		ON CONFLICT (username) DO NOTHING
	`, username, rootPath, now, now)
	if err != nil {
		return nil, err
	}

	return s.GetUser(ctx, username)
}

func (s *PostgresNativeStorage) GetUser(ctx context.Context, username string) (*models.User, error) {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT username, COALESCE(account_id, ''), COALESCE(name, ''), COALESCE(primary_email, ''), COALESCE(password_hash, ''), COALESCE(auth_source, ''), COALESCE(clerk_user_id, ''), COALESCE(root_path, ''), created_at, updated_at
		FROM users WHERE username = $1
	`, username).
		Scan(&u.Username, &u.AccountID, &u.Name, &u.PrimaryEmail, &u.PasswordHash, &u.AuthSource, &u.ClerkUserID, &u.RootPath, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	u.PrimaryEmail = strings.ToLower(strings.TrimSpace(u.PrimaryEmail))
	if u.RootPath == "" {
		u.RootPath = rootPathForSlug(u.Username)
	}
	return &u, nil
}

func (s *PostgresNativeStorage) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	ctx = ensureCtx(ctx)
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, ErrInvalidInput
	}

	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT username, COALESCE(account_id, ''), COALESCE(name, ''), COALESCE(primary_email, ''), COALESCE(password_hash, ''), COALESCE(auth_source, ''), COALESCE(clerk_user_id, ''), COALESCE(root_path, ''), created_at, updated_at
		FROM users
		WHERE lower(primary_email) = $1 AND primary_email <> ''
		LIMIT 1
	`, email).
		Scan(&u.Username, &u.AccountID, &u.Name, &u.PrimaryEmail, &u.PasswordHash, &u.AuthSource, &u.ClerkUserID, &u.RootPath, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	u.PrimaryEmail = strings.ToLower(strings.TrimSpace(u.PrimaryEmail))
	if u.RootPath == "" {
		u.RootPath = rootPathForSlug(u.Username)
	}
	return &u, nil
}

func (s *PostgresNativeStorage) GetUserByClerkUserID(ctx context.Context, clerkUserID string) (*models.User, error) {
	ctx = ensureCtx(ctx)
	clerkUserID = strings.TrimSpace(clerkUserID)
	if clerkUserID == "" {
		return nil, ErrInvalidInput
	}

	var u models.User
	err := s.pool.QueryRow(ctx, `
		SELECT username, COALESCE(account_id, ''), COALESCE(name, ''), COALESCE(primary_email, ''), COALESCE(password_hash, ''), COALESCE(auth_source, ''), COALESCE(clerk_user_id, ''), COALESCE(root_path, ''), created_at, updated_at
		FROM users
		WHERE clerk_user_id = $1 AND clerk_user_id <> ''
		LIMIT 1
	`, clerkUserID).
		Scan(&u.Username, &u.AccountID, &u.Name, &u.PrimaryEmail, &u.PasswordHash, &u.AuthSource, &u.ClerkUserID, &u.RootPath, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	u.PrimaryEmail = strings.ToLower(strings.TrimSpace(u.PrimaryEmail))
	if u.RootPath == "" {
		u.RootPath = rootPathForSlug(u.Username)
	}
	return &u, nil
}

func (s *PostgresNativeStorage) ListUsers(ctx context.Context, limit, offset int) ([]*models.User, error) {
	ctx = ensureCtx(ctx)
	if offset < 0 {
		return nil, ErrInvalidInput
	}

	query := `
		SELECT username, COALESCE(account_id, ''), COALESCE(name, ''), COALESCE(primary_email, ''), COALESCE(password_hash, ''), COALESCE(auth_source, ''), COALESCE(clerk_user_id, ''), COALESCE(root_path, ''), created_at, updated_at
		FROM users
		ORDER BY username ASC
	`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	} else {
		query += ` OFFSET $1`
		args = append(args, offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]*models.User, 0)
	for rows.Next() {
		var user models.User
		if err := rows.Scan(&user.Username, &user.AccountID, &user.Name, &user.PrimaryEmail, &user.PasswordHash, &user.AuthSource, &user.ClerkUserID, &user.RootPath, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, err
		}
		user.PrimaryEmail = strings.ToLower(strings.TrimSpace(user.PrimaryEmail))
		if user.RootPath == "" {
			user.RootPath = rootPathForSlug(user.Username)
		}
		userCopy := user
		users = append(users, &userCopy)
	}
	return users, rows.Err()
}

func (s *PostgresNativeStorage) CreateUser(ctx context.Context, user *models.User) error {
	ctx = ensureCtx(ctx)
	if user == nil {
		return ErrInvalidInput
	}
	username := strings.TrimSpace(user.Username)
	if !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}
	rootPath := rootPathForSlug(username)
	var slugTaken bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE slug = $1)`, username).Scan(&slugTaken); err != nil {
		return err
	}
	if slugTaken {
		return ErrEntryExists
	}
	email := strings.ToLower(strings.TrimSpace(user.PrimaryEmail))
	accountID := strings.TrimSpace(user.AccountID)
	authSource := strings.TrimSpace(user.AuthSource)
	clerkUserID := strings.TrimSpace(user.ClerkUserID)
	now := time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now
	user.RootPath = rootPath

	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (username, account_id, name, primary_email, password_hash, auth_source, clerk_user_id, root_path, created_at, updated_at)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, $9, $10)
	`, username, accountID, user.Name, email, user.PasswordHash, authSource, clerkUserID, rootPath, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return ErrEntryNotFound
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) UpdateUser(ctx context.Context, user *models.User) error {
	ctx = ensureCtx(ctx)
	if user == nil {
		return ErrInvalidInput
	}
	username := strings.TrimSpace(user.Username)
	if !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}
	email := strings.ToLower(strings.TrimSpace(user.PrimaryEmail))
	accountID := strings.TrimSpace(user.AccountID)
	authSource := strings.TrimSpace(user.AuthSource)
	clerkUserID := strings.TrimSpace(user.ClerkUserID)
	now := time.Now()

	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		SET account_id = NULLIF($1, ''), name = $2, primary_email = $3, password_hash = $4, auth_source = $5, clerk_user_id = $6, updated_at = $7
		WHERE username = $8
	`, accountID, user.Name, email, user.PasswordHash, authSource, clerkUserID, now, username)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return ErrEntryNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) DeleteUser(ctx context.Context, username string) error {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var primaryEmail, accountID string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(primary_email, ''), COALESCE(account_id, '') FROM users WHERE username = $1`, username).Scan(&primaryEmail, &accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEntryNotFound
		}
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM auth_sessions WHERE username = $1`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM agent_key_challenges
		WHERE username = $1
		   OR agent_key_id IN (SELECT key_id FROM agent_keys WHERE username = $1)
	`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_keys WHERE username = $1`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM device_authorizations WHERE username = $1`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM repo_bindings WHERE owner_username = $1`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM team_members WHERE username = $1`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE username = $1`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organization_invites WHERE created_by = $1 OR lower(target_email) = lower($2)`, username, strings.TrimSpace(primaryEmail)); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE username = $1`, username)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	if accountID != "" {
		if _, err := tx.Exec(ctx, `
			DELETE FROM accounts
			WHERE account_id = $1
			  AND NOT EXISTS (SELECT 1 FROM users WHERE account_id = $1)
		`, accountID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) CreateAuthSession(ctx context.Context, session *models.AuthSession) error {
	ctx = ensureCtx(ctx)
	if session == nil || session.SessionID == "" || session.Username == "" || session.Token == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(session.Username) {
		return ErrInvalidInput
	}

	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = session.CreatedAt
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, username, agent_key_id, token, refresh_token, device_info, created_at, last_seen_at, access_token_expires_at, refresh_token_expires_at, revoked_at
		) VALUES (
			$1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6, $7, $8, $9, $10, NULL
		)
	`, session.SessionID, session.Username, strings.TrimSpace(session.AgentKeyID), session.Token, strings.TrimSpace(session.RefreshToken), session.DeviceInfo, session.CreatedAt, session.LastSeenAt, session.AccessTokenExpiresAt, session.RefreshTokenExpiresAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetAuthSession(ctx context.Context, sessionID string) (*models.AuthSession, error) {
	ctx = ensureCtx(ctx)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrInvalidInput
	}

	var session models.AuthSession
	var accessTokenExpiresAt *time.Time
	var refreshTokenExpiresAt *time.Time
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, username, COALESCE(agent_key_id, ''), token, COALESCE(refresh_token, ''), COALESCE(device_info, ''), created_at, last_seen_at, access_token_expires_at, refresh_token_expires_at, revoked_at
		FROM auth_sessions
		WHERE session_id = $1 AND revoked_at IS NULL
		LIMIT 1
	`, sessionID).Scan(
		&session.SessionID,
		&session.Username,
		&session.AgentKeyID,
		&session.Token,
		&session.RefreshToken,
		&session.DeviceInfo,
		&session.CreatedAt,
		&session.LastSeenAt,
		&accessTokenExpiresAt,
		&refreshTokenExpiresAt,
		&revokedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	session.AccessTokenExpiresAt = accessTokenExpiresAt
	session.RefreshTokenExpiresAt = refreshTokenExpiresAt
	session.RevokedAt = revokedAt
	return &session, nil
}

func (s *PostgresNativeStorage) GetAuthSessionByToken(ctx context.Context, token string) (*models.AuthSession, error) {
	ctx = ensureCtx(ctx)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidInput
	}

	var session models.AuthSession
	var accessTokenExpiresAt *time.Time
	var refreshTokenExpiresAt *time.Time
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, username, COALESCE(agent_key_id, ''), token, COALESCE(refresh_token, ''), COALESCE(device_info, ''), created_at, last_seen_at, access_token_expires_at, refresh_token_expires_at, revoked_at
		FROM auth_sessions
		WHERE token = $1 AND revoked_at IS NULL AND (access_token_expires_at IS NULL OR access_token_expires_at > NOW())
		LIMIT 1
	`, token).Scan(
		&session.SessionID,
		&session.Username,
		&session.AgentKeyID,
		&session.Token,
		&session.RefreshToken,
		&session.DeviceInfo,
		&session.CreatedAt,
		&session.LastSeenAt,
		&accessTokenExpiresAt,
		&refreshTokenExpiresAt,
		&revokedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	session.AccessTokenExpiresAt = accessTokenExpiresAt
	session.RefreshTokenExpiresAt = refreshTokenExpiresAt
	session.RevokedAt = revokedAt
	return &session, nil
}

func (s *PostgresNativeStorage) GetAuthSessionByRefreshToken(ctx context.Context, refreshToken string) (*models.AuthSession, error) {
	ctx = ensureCtx(ctx)
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, ErrInvalidInput
	}

	var session models.AuthSession
	var accessTokenExpiresAt *time.Time
	var refreshTokenExpiresAt *time.Time
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, username, COALESCE(agent_key_id, ''), token, COALESCE(refresh_token, ''), COALESCE(device_info, ''), created_at, last_seen_at, access_token_expires_at, refresh_token_expires_at, revoked_at
		FROM auth_sessions
		WHERE refresh_token = $1 AND revoked_at IS NULL AND (refresh_token_expires_at IS NULL OR refresh_token_expires_at > NOW())
		LIMIT 1
	`, refreshToken).Scan(
		&session.SessionID,
		&session.Username,
		&session.AgentKeyID,
		&session.Token,
		&session.RefreshToken,
		&session.DeviceInfo,
		&session.CreatedAt,
		&session.LastSeenAt,
		&accessTokenExpiresAt,
		&refreshTokenExpiresAt,
		&revokedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	session.AccessTokenExpiresAt = accessTokenExpiresAt
	session.RefreshTokenExpiresAt = refreshTokenExpiresAt
	session.RevokedAt = revokedAt
	return &session, nil
}

func (s *PostgresNativeStorage) ListAuthSessionsByUser(ctx context.Context, username string) ([]*models.AuthSession, error) {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	rows, err := s.pool.Query(ctx, `
		SELECT session_id, username, COALESCE(agent_key_id, ''), token, COALESCE(refresh_token, ''), COALESCE(device_info, ''), created_at, last_seen_at, access_token_expires_at, refresh_token_expires_at, revoked_at
		FROM auth_sessions
		WHERE username = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*models.AuthSession, 0)
	for rows.Next() {
		var session models.AuthSession
		var accessTokenExpiresAt *time.Time
		var refreshTokenExpiresAt *time.Time
		var revokedAt *time.Time
		if err := rows.Scan(
			&session.SessionID,
			&session.Username,
			&session.AgentKeyID,
			&session.Token,
			&session.RefreshToken,
			&session.DeviceInfo,
			&session.CreatedAt,
			&session.LastSeenAt,
			&accessTokenExpiresAt,
			&refreshTokenExpiresAt,
			&revokedAt,
		); err != nil {
			return nil, err
		}
		session.AccessTokenExpiresAt = accessTokenExpiresAt
		session.RefreshTokenExpiresAt = refreshTokenExpiresAt
		session.RevokedAt = revokedAt
		sessionCopy := session
		out = append(out, &sessionCopy)
	}
	if out == nil {
		out = []*models.AuthSession{}
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) UpdateAuthSessionTokens(ctx context.Context, sessionID, accessToken string, accessTokenExpiresAt *time.Time, refreshToken string, refreshTokenExpiresAt *time.Time) error {
	ctx = ensureCtx(ctx)
	sessionID = strings.TrimSpace(sessionID)
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	if sessionID == "" || accessToken == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET token = $1,
		    access_token_expires_at = $2,
		    refresh_token = NULLIF($3, ''),
		    refresh_token_expires_at = $4
		WHERE session_id = $5 AND revoked_at IS NULL
	`, accessToken, accessTokenExpiresAt, refreshToken, refreshTokenExpiresAt, sessionID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) TouchAuthSession(ctx context.Context, sessionID string, at time.Time) error {
	ctx = ensureCtx(ctx)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrInvalidInput
	}
	if at.IsZero() {
		at = time.Now()
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET last_seen_at = $1
		WHERE session_id = $2 AND revoked_at IS NULL
	`, at, sessionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) RevokeAuthSession(ctx context.Context, username, sessionID string) error {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	sessionID = strings.TrimSpace(sessionID)
	if !auth.ValidateUsername(username) || sessionID == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = NOW()
		WHERE session_id = $1 AND username = $2 AND revoked_at IS NULL
	`, sessionID, username)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) RevokeAuthSessionByToken(ctx context.Context, token string) error {
	ctx = ensureCtx(ctx)
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = NOW()
		WHERE token = $1 AND revoked_at IS NULL
	`, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) RevokeAuthSessionsByAgentKey(ctx context.Context, username, agentKeyID string) (int, error) {
	username = strings.TrimSpace(username)
	agentKeyID = strings.TrimSpace(agentKeyID)
	if !auth.ValidateUsername(username) || agentKeyID == "" {
		return 0, ErrInvalidInput
	}

	commandTag, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $3
		WHERE username = $1
		  AND agent_key_id = $2
		  AND revoked_at IS NULL
	`, username, agentKeyID, time.Now())
	if err != nil {
		return 0, err
	}
	return int(commandTag.RowsAffected()), nil
}

func (s *PostgresNativeStorage) CreateAgentKey(ctx context.Context, key *models.AgentKey) error {
	ctx = ensureCtx(ctx)
	if key == nil {
		return ErrInvalidInput
	}
	keyID := strings.TrimSpace(key.KeyID)
	username := strings.TrimSpace(key.Username)
	fingerprint := strings.TrimSpace(key.Fingerprint)
	algorithm := strings.TrimSpace(key.Algorithm)
	if keyID == "" || !auth.ValidateUsername(username) || fingerprint == "" || algorithm == "" || len(key.PublicKey) == 0 {
		return ErrInvalidInput
	}

	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, username).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrEntryNotFound
	}

	now := time.Now()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = key.CreatedAt
	}
	state := key.State
	if state == "" {
		state = models.AgentKeyStateActive
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_keys (
			key_id, username, name, algorithm, public_key, fingerprint, state, created_at, updated_at, last_used_at, revoked_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL
		)
	`, keyID, username, strings.TrimSpace(key.Name), algorithm, key.PublicKey, fingerprint, string(state), key.CreatedAt, key.UpdatedAt, key.LastUsedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetAgentKey(ctx context.Context, keyID string) (*models.AgentKey, error) {
	ctx = ensureCtx(ctx)
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, ErrInvalidInput
	}

	var key models.AgentKey
	var lastUsedAt *time.Time
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT key_id, username, name, algorithm, public_key, fingerprint, state, created_at, updated_at, last_used_at, revoked_at
		FROM agent_keys
		WHERE key_id = $1
		LIMIT 1
	`, keyID).Scan(
		&key.KeyID,
		&key.Username,
		&key.Name,
		&key.Algorithm,
		&key.PublicKey,
		&key.Fingerprint,
		&key.State,
		&key.CreatedAt,
		&key.UpdatedAt,
		&lastUsedAt,
		&revokedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	key.LastUsedAt = lastUsedAt
	key.RevokedAt = revokedAt
	return &key, nil
}

func (s *PostgresNativeStorage) GetAgentKeyByFingerprint(ctx context.Context, fingerprint string) (*models.AgentKey, error) {
	ctx = ensureCtx(ctx)
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return nil, ErrInvalidInput
	}

	var key models.AgentKey
	var lastUsedAt *time.Time
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT key_id, username, name, algorithm, public_key, fingerprint, state, created_at, updated_at, last_used_at, revoked_at
		FROM agent_keys
		WHERE fingerprint = $1
		LIMIT 1
	`, fingerprint).Scan(
		&key.KeyID,
		&key.Username,
		&key.Name,
		&key.Algorithm,
		&key.PublicKey,
		&key.Fingerprint,
		&key.State,
		&key.CreatedAt,
		&key.UpdatedAt,
		&lastUsedAt,
		&revokedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	key.LastUsedAt = lastUsedAt
	key.RevokedAt = revokedAt
	return &key, nil
}

func (s *PostgresNativeStorage) ListAgentKeysByUser(ctx context.Context, username string) ([]*models.AgentKey, error) {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	rows, err := s.pool.Query(ctx, `
		SELECT key_id, username, name, algorithm, public_key, fingerprint, state, created_at, updated_at, last_used_at, revoked_at
		FROM agent_keys
		WHERE username = $1
		ORDER BY created_at ASC
	`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*models.AgentKey, 0)
	for rows.Next() {
		var key models.AgentKey
		var lastUsedAt *time.Time
		var revokedAt *time.Time
		if err := rows.Scan(
			&key.KeyID,
			&key.Username,
			&key.Name,
			&key.Algorithm,
			&key.PublicKey,
			&key.Fingerprint,
			&key.State,
			&key.CreatedAt,
			&key.UpdatedAt,
			&lastUsedAt,
			&revokedAt,
		); err != nil {
			return nil, err
		}
		key.LastUsedAt = lastUsedAt
		key.RevokedAt = revokedAt
		out = append(out, &key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []*models.AgentKey{}
	}
	return out, nil
}

func (s *PostgresNativeStorage) TouchAgentKey(ctx context.Context, keyID string, at time.Time) error {
	ctx = ensureCtx(ctx)
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return ErrInvalidInput
	}
	if at.IsZero() {
		at = time.Now()
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_keys
		SET last_used_at = $1, updated_at = $1
		WHERE key_id = $2
	`, at, keyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) RevokeAgentKey(ctx context.Context, username, keyID string, revokedAt time.Time) error {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	keyID = strings.TrimSpace(keyID)
	if !auth.ValidateUsername(username) || keyID == "" {
		return ErrInvalidInput
	}
	if revokedAt.IsZero() {
		revokedAt = time.Now()
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_keys
		SET state = $1, revoked_at = $2, updated_at = $2
		WHERE key_id = $3 AND username = $4
	`, string(models.AgentKeyStateRevoked), revokedAt, keyID, username)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) CreateAgentKeyChallenge(ctx context.Context, challenge *models.AgentKeyChallenge) error {
	ctx = ensureCtx(ctx)
	if challenge == nil {
		return ErrInvalidInput
	}
	challengeID := strings.TrimSpace(challenge.ChallengeID)
	agentKeyID := strings.TrimSpace(challenge.AgentKeyID)
	username := strings.TrimSpace(challenge.Username)
	if challengeID == "" || agentKeyID == "" || !auth.ValidateUsername(username) || len(challenge.Challenge) == 0 {
		return ErrInvalidInput
	}

	now := time.Now()
	if challenge.CreatedAt.IsZero() {
		challenge.CreatedAt = now
	}
	if challenge.ExpiresAt.IsZero() {
		challenge.ExpiresAt = now.Add(time.Minute)
	}

	var keyUsername string
	err := s.pool.QueryRow(ctx, `SELECT username FROM agent_keys WHERE key_id = $1`, agentKeyID).Scan(&keyUsername)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrEntryNotFound
		}
		return err
	}
	if keyUsername != username {
		return ErrInvalidInput
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO agent_key_challenges (
			challenge_id, agent_key_id, username, challenge, device_info, created_at, expires_at, used_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, NULL
		)
	`, challengeID, agentKeyID, username, challenge.Challenge, challenge.DeviceInfo, challenge.CreatedAt, challenge.ExpiresAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetAgentKeyChallenge(ctx context.Context, challengeID string) (*models.AgentKeyChallenge, error) {
	ctx = ensureCtx(ctx)
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return nil, ErrInvalidInput
	}

	var challenge models.AgentKeyChallenge
	var usedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT challenge_id, agent_key_id, username, challenge, COALESCE(device_info, ''), created_at, expires_at, used_at
		FROM agent_key_challenges
		WHERE challenge_id = $1
		LIMIT 1
	`, challengeID).Scan(
		&challenge.ChallengeID,
		&challenge.AgentKeyID,
		&challenge.Username,
		&challenge.Challenge,
		&challenge.DeviceInfo,
		&challenge.CreatedAt,
		&challenge.ExpiresAt,
		&usedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	challenge.UsedAt = usedAt
	return &challenge, nil
}

func (s *PostgresNativeStorage) MarkAgentKeyChallengeUsed(ctx context.Context, challengeID string, usedAt time.Time) error {
	ctx = ensureCtx(ctx)
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return ErrInvalidInput
	}
	if usedAt.IsZero() {
		usedAt = time.Now()
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_key_challenges
		SET used_at = $1
		WHERE challenge_id = $2 AND used_at IS NULL
	`, usedAt, challengeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var existing bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_key_challenges WHERE challenge_id = $1)`, challengeID).Scan(&existing); err != nil {
			return err
		}
		if existing {
			return ErrEntryExists
		}
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) CreateDeviceAuthorization(ctx context.Context, authorization *models.DeviceAuthorization) error {
	ctx = ensureCtx(ctx)
	if authorization == nil {
		return ErrInvalidInput
	}
	deviceCode := strings.TrimSpace(authorization.DeviceCode)
	userCode := strings.TrimSpace(authorization.UserCode)
	if deviceCode == "" || userCode == "" {
		return ErrInvalidInput
	}
	if authorization.Status == "" {
		authorization.Status = models.DeviceAuthorizationPending
	}
	now := time.Now()
	if authorization.CreatedAt.IsZero() {
		authorization.CreatedAt = now
	}
	if authorization.ExpiresAt.IsZero() {
		authorization.ExpiresAt = now.Add(10 * time.Minute)
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_authorizations (
			device_code, user_code, username, session_id, device_info, status, created_at, expires_at, approved_at, denied_at
		) VALUES (
			$1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $8, $9, $10
		)
	`, deviceCode, userCode, strings.TrimSpace(authorization.Username), strings.TrimSpace(authorization.SessionID), authorization.DeviceInfo, string(authorization.Status), authorization.CreatedAt, authorization.ExpiresAt, authorization.ApprovedAt, authorization.DeniedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetDeviceAuthorizationByDeviceCode(ctx context.Context, deviceCode string) (*models.DeviceAuthorization, error) {
	ctx = ensureCtx(ctx)
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return nil, ErrInvalidInput
	}

	var authorization models.DeviceAuthorization
	var username *string
	var sessionID *string
	var approvedAt *time.Time
	var deniedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT device_code, user_code, username, session_id, COALESCE(device_info, ''), status, created_at, expires_at, approved_at, denied_at
		FROM device_authorizations
		WHERE device_code = $1
		LIMIT 1
	`, deviceCode).Scan(
		&authorization.DeviceCode,
		&authorization.UserCode,
		&username,
		&sessionID,
		&authorization.DeviceInfo,
		&authorization.Status,
		&authorization.CreatedAt,
		&authorization.ExpiresAt,
		&approvedAt,
		&deniedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	if username != nil {
		authorization.Username = *username
	}
	if sessionID != nil {
		authorization.SessionID = *sessionID
	}
	authorization.ApprovedAt = approvedAt
	authorization.DeniedAt = deniedAt
	return &authorization, nil
}

func (s *PostgresNativeStorage) GetDeviceAuthorizationByUserCode(ctx context.Context, userCode string) (*models.DeviceAuthorization, error) {
	ctx = ensureCtx(ctx)
	userCode = strings.TrimSpace(userCode)
	if userCode == "" {
		return nil, ErrInvalidInput
	}

	var authorization models.DeviceAuthorization
	var username *string
	var sessionID *string
	var approvedAt *time.Time
	var deniedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT device_code, user_code, username, session_id, COALESCE(device_info, ''), status, created_at, expires_at, approved_at, denied_at
		FROM device_authorizations
		WHERE user_code = $1
		LIMIT 1
	`, userCode).Scan(
		&authorization.DeviceCode,
		&authorization.UserCode,
		&username,
		&sessionID,
		&authorization.DeviceInfo,
		&authorization.Status,
		&authorization.CreatedAt,
		&authorization.ExpiresAt,
		&approvedAt,
		&deniedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	if username != nil {
		authorization.Username = *username
	}
	if sessionID != nil {
		authorization.SessionID = *sessionID
	}
	authorization.ApprovedAt = approvedAt
	authorization.DeniedAt = deniedAt
	return &authorization, nil
}

func (s *PostgresNativeStorage) UpdateDeviceAuthorization(ctx context.Context, authorization *models.DeviceAuthorization) error {
	ctx = ensureCtx(ctx)
	if authorization == nil {
		return ErrInvalidInput
	}
	deviceCode := strings.TrimSpace(authorization.DeviceCode)
	userCode := strings.TrimSpace(authorization.UserCode)
	if deviceCode == "" || userCode == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE device_authorizations
		SET user_code = $1,
		    username = NULLIF($2, ''),
		    session_id = NULLIF($3, ''),
		    device_info = $4,
		    status = $5,
		    created_at = $6,
		    expires_at = $7,
		    approved_at = $8,
		    denied_at = $9
		WHERE device_code = $10
	`, userCode, strings.TrimSpace(authorization.Username), strings.TrimSpace(authorization.SessionID), authorization.DeviceInfo, string(authorization.Status), authorization.CreatedAt, authorization.ExpiresAt, authorization.ApprovedAt, authorization.DeniedAt, deviceCode)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) CreateOrganization(ctx context.Context, org *models.Organization) error {
	ctx = ensureCtx(ctx)
	if org == nil {
		return ErrInvalidInput
	}
	slug := strings.TrimSpace(org.Slug)
	name := strings.TrimSpace(org.Name)
	createdBy := strings.TrimSpace(org.CreatedBy)
	if slug == "" || name == "" || createdBy == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(slug) || !auth.ValidateUsername(createdBy) {
		return ErrInvalidInput
	}
	org.Slug = slug
	org.Name = name
	org.CreatedBy = createdBy
	org.RootPath = rootPathForSlug(org.Slug)

	var slugTaken bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, org.Slug).Scan(&slugTaken); err != nil {
		return err
	}
	if slugTaken {
		return ErrEntryExists
	}

	now := time.Now()
	if org.CreatedAt.IsZero() {
		org.CreatedAt = now
	}
	org.UpdatedAt = now

	_, err := s.pool.Exec(ctx, `
		INSERT INTO organizations (slug, name, created_by, root_path, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, org.Slug, org.Name, org.CreatedBy, org.RootPath, org.CreatedAt, org.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetOrganization(ctx context.Context, orgSlug string) (*models.Organization, error) {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	if orgSlug == "" {
		return nil, ErrInvalidInput
	}

	var org models.Organization
	err := s.pool.QueryRow(ctx, `
		SELECT slug, name, created_by, COALESCE(root_path, ''), created_at, updated_at
		FROM organizations
		WHERE slug = $1
	`, orgSlug).
		Scan(&org.Slug, &org.Name, &org.CreatedBy, &org.RootPath, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	if org.RootPath == "" {
		org.RootPath = rootPathForSlug(org.Slug)
	}
	return &org, nil
}

func (s *PostgresNativeStorage) UpdateOrganization(ctx context.Context, org *models.Organization) error {
	ctx = ensureCtx(ctx)
	if org == nil {
		return ErrInvalidInput
	}
	slug := strings.TrimSpace(org.Slug)
	name := strings.TrimSpace(org.Name)
	if slug == "" || name == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE organizations
		SET name = $1, updated_at = NOW()
		WHERE slug = $2
	`, name, slug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) DeleteOrganization(ctx context.Context, orgSlug string) error {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	if orgSlug == "" {
		return ErrInvalidInput
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE org_slug = $1`, orgSlug); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organization_invites WHERE org_slug = $1`, orgSlug); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM organizations WHERE slug = $1`, orgSlug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	ctx = ensureCtx(ctx)
	if member == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(member.OrgSlug)
	username := strings.TrimSpace(member.Username)
	role := normalizeOrganizationRole(member.Role)
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) || !validOrganizationRole(role) {
		return ErrInvalidInput
	}

	// Verify org exists.
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE slug = $1)`, orgSlug).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrEntryNotFound
	}
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username = $1)`, username).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrEntryNotFound
	}

	now := time.Now()
	if member.CreatedAt.IsZero() {
		member.CreatedAt = now
	}
	member.UpdatedAt = now
	member.OrgSlug = orgSlug
	member.Username = username
	member.Role = role

	_, err = s.pool.Exec(ctx, `
		INSERT INTO organization_members (org_slug, username, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`, member.OrgSlug, member.Username, string(member.Role), member.CreatedAt, member.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetOrganizationMember(ctx context.Context, orgSlug, username string) (*models.OrganizationMember, error) {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	var member models.OrganizationMember
	err := s.pool.QueryRow(ctx, `
		SELECT org_slug, username, role, created_at, updated_at
		FROM organization_members
		WHERE org_slug = $1 AND username = $2
	`, orgSlug, username).Scan(&member.OrgSlug, &member.Username, &member.Role, &member.CreatedAt, &member.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &member, nil
}

func (s *PostgresNativeStorage) ListOrganizationMembers(ctx context.Context, orgSlug string) ([]*models.OrganizationMember, error) {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	if !auth.ValidateUsername(orgSlug) {
		return nil, ErrInvalidInput
	}

	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE slug = $1)`, orgSlug).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrEntryNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT org_slug, username, role, created_at, updated_at
		FROM organization_members
		WHERE org_slug = $1
		ORDER BY username
	`, orgSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := make([]*models.OrganizationMember, 0)
	for rows.Next() {
		var member models.OrganizationMember
		if err := rows.Scan(&member.OrgSlug, &member.Username, &member.Role, &member.CreatedAt, &member.UpdatedAt); err != nil {
			return nil, err
		}
		members = append(members, &member)
	}
	if members == nil {
		members = []*models.OrganizationMember{}
	}
	return members, rows.Err()
}

func (s *PostgresNativeStorage) UpdateOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	ctx = ensureCtx(ctx)
	if member == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(member.OrgSlug)
	username := strings.TrimSpace(member.Username)
	role := normalizeOrganizationRole(member.Role)
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) || !validOrganizationRole(role) {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE organization_members
		SET role = $1, updated_at = NOW()
		WHERE org_slug = $2 AND username = $3
	`, string(role), orgSlug, username)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	member.OrgSlug = orgSlug
	member.Username = username
	member.Role = role
	return nil
}

func (s *PostgresNativeStorage) RemoveOrganizationMember(ctx context.Context, orgSlug, username string) error {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE org_slug = $1 AND username = $2`, orgSlug, username)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM team_members tm
		USING teams t
		WHERE tm.team_id = t.team_id AND t.org_slug = $1 AND tm.username = $2
	`, orgSlug, username); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) CreateOrganizationInvite(ctx context.Context, invite *models.OrganizationInvite) error {
	ctx = ensureCtx(ctx)
	if invite == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(invite.OrgSlug)
	inviteID := strings.TrimSpace(invite.InviteID)
	targetEmail := normalizeEmail(invite.TargetEmail)
	role := normalizeOrganizationRole(invite.Role)
	status := normalizeOrganizationInviteStatus(invite.Status)
	createdBy := strings.TrimSpace(invite.CreatedBy)

	if !auth.ValidateUsername(orgSlug) || inviteID == "" || !validateEmail(targetEmail) || !auth.ValidateUsername(createdBy) || !validOrganizationRole(role) {
		return ErrInvalidInput
	}
	if status == "" {
		status = models.OrganizationInvitePending
	}
	if !validOrganizationInviteStatus(status) {
		return ErrInvalidInput
	}

	now := time.Now()
	if invite.CreatedAt.IsZero() {
		invite.CreatedAt = now
	}
	invite.UpdatedAt = now
	invite.OrgSlug = orgSlug
	invite.InviteID = inviteID
	invite.TargetEmail = targetEmail
	invite.Role = role
	invite.Status = status
	invite.CreatedBy = createdBy

	_, err := s.pool.Exec(ctx, `
		INSERT INTO organization_invites (invite_id, org_slug, target_email, role, status, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, invite.InviteID, invite.OrgSlug, invite.TargetEmail, string(invite.Role), string(invite.Status), invite.CreatedBy, invite.CreatedAt, invite.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return ErrEntryNotFound
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetOrganizationInvite(ctx context.Context, orgSlug, inviteID string) (*models.OrganizationInvite, error) {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	inviteID = strings.TrimSpace(inviteID)
	if !auth.ValidateUsername(orgSlug) || inviteID == "" {
		return nil, ErrInvalidInput
	}

	var invite models.OrganizationInvite
	err := s.pool.QueryRow(ctx, `
		SELECT invite_id, org_slug, target_email, role, status, created_by, created_at, updated_at
		FROM organization_invites
		WHERE org_slug = $1 AND invite_id = $2
	`, orgSlug, inviteID).Scan(
		&invite.InviteID,
		&invite.OrgSlug,
		&invite.TargetEmail,
		&invite.Role,
		&invite.Status,
		&invite.CreatedBy,
		&invite.CreatedAt,
		&invite.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	invite.TargetEmail = normalizeEmail(invite.TargetEmail)
	return &invite, nil
}

func (s *PostgresNativeStorage) UpdateOrganizationInvite(ctx context.Context, invite *models.OrganizationInvite) error {
	ctx = ensureCtx(ctx)
	if invite == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(invite.OrgSlug)
	inviteID := strings.TrimSpace(invite.InviteID)
	targetEmail := normalizeEmail(invite.TargetEmail)
	role := normalizeOrganizationRole(invite.Role)
	status := normalizeOrganizationInviteStatus(invite.Status)
	createdBy := strings.TrimSpace(invite.CreatedBy)
	if !auth.ValidateUsername(orgSlug) || inviteID == "" || !validateEmail(targetEmail) || !auth.ValidateUsername(createdBy) || !validOrganizationRole(role) || !validOrganizationInviteStatus(status) {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE organization_invites
		SET target_email = $1, role = $2, status = $3, created_by = $4, updated_at = NOW()
		WHERE invite_id = $5 AND org_slug = $6
	`, targetEmail, string(role), string(status), createdBy, inviteID, orgSlug)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return ErrEntryNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	invite.OrgSlug = orgSlug
	invite.InviteID = inviteID
	invite.TargetEmail = targetEmail
	invite.Role = role
	invite.Status = status
	invite.CreatedBy = createdBy
	invite.UpdatedAt = time.Now()
	return nil
}

func (s *PostgresNativeStorage) CreateTeam(ctx context.Context, team *models.Team) error {
	ctx = ensureCtx(ctx)
	if team == nil {
		return ErrInvalidInput
	}
	teamID := strings.TrimSpace(team.TeamID)
	orgSlug := strings.TrimSpace(team.OrgSlug)
	name := strings.TrimSpace(team.Name)
	createdBy := strings.TrimSpace(team.CreatedBy)
	if teamID == "" || !auth.ValidateUsername(orgSlug) || name == "" || !auth.ValidateUsername(createdBy) {
		return ErrInvalidInput
	}

	now := time.Now()
	if team.CreatedAt.IsZero() {
		team.CreatedAt = now
	}
	team.UpdatedAt = now
	team.TeamID = teamID
	team.OrgSlug = orgSlug
	team.Name = name
	team.CreatedBy = createdBy

	_, err := s.pool.Exec(ctx, `
		INSERT INTO teams (team_id, org_slug, name, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, team.TeamID, team.OrgSlug, team.Name, team.CreatedBy, team.CreatedAt, team.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		if strings.Contains(err.Error(), "violates foreign key constraint") {
			return ErrEntryNotFound
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetTeam(ctx context.Context, teamID string) (*models.Team, error) {
	ctx = ensureCtx(ctx)
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return nil, ErrInvalidInput
	}

	var team models.Team
	err := s.pool.QueryRow(ctx, `
		SELECT team_id, org_slug, name, created_by, created_at, updated_at
		FROM teams
		WHERE team_id = $1
	`, teamID).Scan(&team.TeamID, &team.OrgSlug, &team.Name, &team.CreatedBy, &team.CreatedAt, &team.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &team, nil
}

func (s *PostgresNativeStorage) ListTeams(ctx context.Context, orgSlug string) ([]*models.Team, error) {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	if !auth.ValidateUsername(orgSlug) {
		return nil, ErrInvalidInput
	}

	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE slug = $1)`, orgSlug).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrEntryNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT team_id, org_slug, name, created_by, created_at, updated_at
		FROM teams
		WHERE org_slug = $1
		ORDER BY team_id
	`, orgSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := make([]*models.Team, 0)
	for rows.Next() {
		var team models.Team
		if err := rows.Scan(&team.TeamID, &team.OrgSlug, &team.Name, &team.CreatedBy, &team.CreatedAt, &team.UpdatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, &team)
	}
	if teams == nil {
		teams = []*models.Team{}
	}
	return teams, rows.Err()
}

func (s *PostgresNativeStorage) UpdateTeam(ctx context.Context, team *models.Team) error {
	ctx = ensureCtx(ctx)
	if team == nil {
		return ErrInvalidInput
	}
	teamID := strings.TrimSpace(team.TeamID)
	name := strings.TrimSpace(team.Name)
	if teamID == "" || name == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE teams
		SET name = $1, updated_at = NOW()
		WHERE team_id = $2
	`, name, teamID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	team.Name = name
	team.TeamID = teamID
	team.UpdatedAt = time.Now()
	return nil
}

func (s *PostgresNativeStorage) DeleteTeam(ctx context.Context, orgSlug, teamID string) error {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	teamID = strings.TrimSpace(teamID)
	if !auth.ValidateUsername(orgSlug) || teamID == "" {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `DELETE FROM teams WHERE team_id = $1 AND org_slug = $2`, teamID, orgSlug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) AddTeamMember(ctx context.Context, member *models.TeamMember) error {
	ctx = ensureCtx(ctx)
	if member == nil {
		return ErrInvalidInput
	}
	teamID := strings.TrimSpace(member.TeamID)
	username := strings.TrimSpace(member.Username)
	if teamID == "" || !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	now := time.Now()
	if member.AddedAt.IsZero() {
		member.AddedAt = now
	}
	member.TeamID = teamID
	member.Username = username

	_, err := s.pool.Exec(ctx, `
		INSERT INTO team_members (team_id, username, added_at)
		SELECT t.team_id, $2, $3
		FROM teams t
		INNER JOIN organization_members om ON om.org_slug = t.org_slug AND om.username = $2
		WHERE t.team_id = $1
	`, teamID, username, member.AddedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}

	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM team_members WHERE team_id = $1 AND username = $2)`, teamID, username).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) DeleteTeamMember(ctx context.Context, orgSlug, teamID, username string) error {
	ctx = ensureCtx(ctx)
	orgSlug = strings.TrimSpace(orgSlug)
	teamID = strings.TrimSpace(teamID)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(orgSlug) || teamID == "" || !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `
		DELETE FROM team_members tm
		USING teams t
		WHERE tm.team_id = t.team_id
		  AND t.org_slug = $1
		  AND tm.team_id = $2
		  AND tm.username = $3
	`, orgSlug, teamID, username)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) ListOrganizationsForUser(ctx context.Context, username string) ([]*models.Organization, error) {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	rows, err := s.pool.Query(ctx, `
		SELECT o.slug, o.name, o.created_by, COALESCE(o.root_path, ''), o.created_at, o.updated_at
		FROM organizations o
		INNER JOIN organization_members m ON o.slug = m.org_slug
		WHERE m.username = $1
		ORDER BY o.slug
	`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []*models.Organization
	for rows.Next() {
		var org models.Organization
		if err := rows.Scan(&org.Slug, &org.Name, &org.CreatedBy, &org.RootPath, &org.CreatedAt, &org.UpdatedAt); err != nil {
			return nil, err
		}
		if org.RootPath == "" {
			org.RootPath = rootPathForSlug(org.Slug)
		}
		orgs = append(orgs, &org)
	}
	if orgs == nil {
		orgs = []*models.Organization{}
	}
	return orgs, rows.Err()
}

// ============ Agent Session Operations ============

func (s *PostgresNativeStorage) CreateAgentSession(ctx context.Context, session *models.AgentSession) error {
	ctx = ensureCtx(ctx)
	if session == nil ||
		session.SessionID == "" ||
		session.SliceID == "" ||
		session.UserID == "" ||
		session.Provider == "" ||
		session.E2BTemplateID == "" ||
		session.State == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_sessions (
			session_id, slice_id, environment_name, agent_type, user_id, state, provider, e2b_template_id, e2b_sandbox_id, e2b_region,
			idle_timeout_sec, ttl_sec, runtime_provider, runtime_session_id, runtime_status, runtime_error_code,
			runtime_endpoint, created_at, updated_at, started_at, last_activity_at, stopped_at, failure_code, failure_message
		) VALUES (
			$1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''),
			$11, $12, NULLIF($13, ''), NULLIF($14, ''), NULLIF($15, ''), NULLIF($16, ''),
			NULLIF($17, ''), $18, $19, $20, $21, $22, NULLIF($23, ''), NULLIF($24, '')
		)
	`,
		session.SessionID, session.SliceID, session.EnvironmentName, session.AgentType, session.UserID, string(session.State), session.Provider,
		session.E2BTemplateID, session.E2BSandboxID, session.E2BRegion,
		session.IdleTimeoutSec, session.TTLSec, session.RuntimeProvider, session.RuntimeSessionID, session.RuntimeStatus, session.RuntimeErrorCode,
		session.RuntimeEndpoint, session.CreatedAt, session.UpdatedAt, session.StartedAt, session.LastActivityAt, session.StoppedAt,
		session.FailureCode, session.FailureMessage,
	)
	if err != nil {
		if strings.Contains(err.Error(), "idx_agent_sessions_active_per_slice") ||
			strings.Contains(err.Error(), "duplicate key") ||
			strings.Contains(err.Error(), "unique constraint") {
			return ErrAgentSessionConflict
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetAgentSession(ctx context.Context, sessionID string) (*models.AgentSession, error) {
	ctx = ensureCtx(ctx)

	var session models.AgentSession
	var startedAt, lastActivityAt, stoppedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, slice_id, COALESCE(environment_name, ''), COALESCE(agent_type, ''), user_id, state, provider, e2b_template_id, COALESCE(e2b_sandbox_id, ''),
		       COALESCE(e2b_region, ''), idle_timeout_sec, ttl_sec,
		       COALESCE(runtime_provider, ''), COALESCE(runtime_session_id, ''), COALESCE(runtime_status, ''), COALESCE(runtime_error_code, ''),
		       COALESCE(runtime_endpoint, ''),
		       created_at, updated_at, started_at, last_activity_at, stopped_at,
		       COALESCE(failure_code, ''), COALESCE(failure_message, '')
		FROM agent_sessions
		WHERE session_id = $1
	`, sessionID).Scan(
		&session.SessionID, &session.SliceID, &session.EnvironmentName, &session.AgentType, &session.UserID, &session.State, &session.Provider,
		&session.E2BTemplateID, &session.E2BSandboxID, &session.E2BRegion, &session.IdleTimeoutSec, &session.TTLSec,
		&session.RuntimeProvider, &session.RuntimeSessionID, &session.RuntimeStatus, &session.RuntimeErrorCode,
		&session.RuntimeEndpoint, &session.CreatedAt, &session.UpdatedAt, &startedAt, &lastActivityAt,
		&stoppedAt, &session.FailureCode, &session.FailureMessage,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrAgentSessionNotFound
		}
		return nil, err
	}
	session.StartedAt = startedAt
	session.LastActivityAt = lastActivityAt
	session.StoppedAt = stoppedAt
	return &session, nil
}

func (s *PostgresNativeStorage) GetActiveAgentSessionBySlice(ctx context.Context, sliceID string) (*models.AgentSession, error) {
	ctx = ensureCtx(ctx)

	var sessionID string
	err := s.pool.QueryRow(ctx, `
		SELECT session_id
		FROM agent_sessions
		WHERE slice_id = $1 AND state IN ('creating', 'starting', 'running', 'idle', 'stopping')
		ORDER BY created_at DESC
		LIMIT 1
	`, sliceID).Scan(&sessionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrAgentSessionNotFound
		}
		return nil, err
	}
	return s.GetAgentSession(ctx, sessionID)
}

func (s *PostgresNativeStorage) ListAgentSessionsByState(ctx context.Context, states []models.AgentSessionState, limit int) ([]*models.AgentSession, error) {
	ctx = ensureCtx(ctx)
	if len(states) == 0 {
		return []*models.AgentSession{}, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	stateVals := make([]string, 0, len(states))
	for _, state := range states {
		if strings.TrimSpace(string(state)) == "" {
			continue
		}
		stateVals = append(stateVals, string(state))
	}
	if len(stateVals) == 0 {
		return []*models.AgentSession{}, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT session_id
		FROM agent_sessions
		WHERE state = ANY($1)
		ORDER BY updated_at DESC
		LIMIT $2
	`, stateVals, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*models.AgentSession, 0, limit)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		session, err := s.GetAgentSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) UpdateAgentSession(ctx context.Context, session *models.AgentSession) error {
	ctx = ensureCtx(ctx)
	if session == nil || session.SessionID == "" || session.SliceID == "" || session.UserID == "" {
		return ErrInvalidInput
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = time.Now()
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_sessions
		SET slice_id = $1,
		    environment_name = $2,
		    agent_type = NULLIF($3, ''),
		    user_id = $4,
		    state = $5,
		    provider = $6,
		    e2b_template_id = $7,
		    e2b_sandbox_id = NULLIF($8, ''),
		    e2b_region = NULLIF($9, ''),
		    idle_timeout_sec = $10,
		    ttl_sec = $11,
		    runtime_provider = NULLIF($12, ''),
		    runtime_session_id = NULLIF($13, ''),
		    runtime_status = NULLIF($14, ''),
		    runtime_error_code = NULLIF($15, ''),
		    runtime_endpoint = NULLIF($16, ''),
		    updated_at = $17,
		    started_at = $18,
		    last_activity_at = $19,
		    stopped_at = $20,
		    failure_code = NULLIF($21, ''),
		    failure_message = NULLIF($22, '')
		WHERE session_id = $23
	`,
		session.SliceID, session.EnvironmentName, session.AgentType, session.UserID, string(session.State), session.Provider, session.E2BTemplateID,
		session.E2BSandboxID, session.E2BRegion, session.IdleTimeoutSec, session.TTLSec,
		session.RuntimeProvider, session.RuntimeSessionID, session.RuntimeStatus, session.RuntimeErrorCode, session.RuntimeEndpoint,
		session.UpdatedAt, session.StartedAt, session.LastActivityAt, session.StoppedAt,
		session.FailureCode, session.FailureMessage, session.SessionID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "idx_agent_sessions_active_per_slice") ||
			strings.Contains(err.Error(), "duplicate key") ||
			strings.Contains(err.Error(), "unique constraint") {
			return ErrAgentSessionConflict
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAgentSessionNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) AppendAgentSessionEvent(ctx context.Context, event *models.AgentSessionEvent) error {
	ctx = ensureCtx(ctx)
	if event == nil || event.SessionID == "" || event.Stream == "" || event.Type == "" {
		return ErrInvalidInput
	}
	if event.Seq > uint64(^uint64(0)>>1) {
		return ErrInvalidInput
	}
	if event.TS.IsZero() {
		event.TS = time.Now()
	}
	payload := []byte("{}")
	if len(event.Payload) > 0 {
		payload = event.Payload
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_session_events (session_id, seq, ts, stream, type, payload_json)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, event.SessionID, int64(event.Seq), event.TS, event.Stream, event.Type, payload)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrAgentSessionConflict
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) ListAgentSessionEvents(ctx context.Context, sessionID string, sinceSeq uint64, limit int) ([]*models.AgentSessionEvent, error) {
	ctx = ensureCtx(ctx)
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT session_id, seq, ts, stream, type, payload_json
		FROM agent_session_events
		WHERE session_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3
	`, sessionID, int64(sinceSeq), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]*models.AgentSessionEvent, 0, limit)
	for rows.Next() {
		var event models.AgentSessionEvent
		var seq int64
		var payload []byte
		if err := rows.Scan(&event.SessionID, &seq, &event.TS, &event.Stream, &event.Type, &payload); err != nil {
			return nil, err
		}
		if seq < 0 {
			return nil, ErrInvalidInput
		}
		event.Seq = uint64(seq)
		if payload == nil {
			payload = []byte("{}")
		}
		event.Payload = payload
		events = append(events, &event)
	}
	return events, rows.Err()
}

func (s *PostgresNativeStorage) AddAgentSessionAudit(ctx context.Context, audit *models.AgentSessionAudit) error {
	ctx = ensureCtx(ctx)
	if audit == nil || audit.SessionID == "" || audit.Action == "" {
		return ErrInvalidInput
	}
	if audit.CreatedAt.IsZero() {
		audit.CreatedAt = time.Now()
	}
	metadata := []byte("{}")
	if len(audit.Metadata) > 0 {
		metadata = audit.Metadata
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_session_audit (session_id, actor_user_id, action, metadata_json, created_at)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5)
		RETURNING id
	`, audit.SessionID, audit.ActorUserID, audit.Action, metadata, audit.CreatedAt).Scan(&audit.ID)
	if err != nil {
		return err
	}
	return nil
}

// ============ Internal Helpers ============

// queryable abstracts *pgxpool.Pool and pgx.Tx for shared scan methods.
type queryable interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
}

type execable interface {
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}

func (s *PostgresNativeStorage) sliceSlugExists(ctx context.Context, q queryable, owner, slug string) (bool, error) {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM slices WHERE created_by = $1 AND slug = $2)`, strings.TrimSpace(owner), strings.TrimSpace(slug)).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *PostgresNativeStorage) allocateSliceSlug(ctx context.Context, q queryable, slice *models.Slice) (string, error) {
	if slice == nil {
		return "", ErrInvalidInput
	}
	explicit := strings.TrimSpace(slice.Slug)
	if explicit != "" {
		explicit, err := normalizeStoredSliceSlug(slice, explicit)
		if err != nil {
			return "", err
		}
		exists, err := s.sliceSlugExists(ctx, q, slice.CreatedBy, explicit)
		if err != nil {
			return "", err
		}
		if exists {
			return "", ErrSliceAlreadyExists
		}
		return explicit, nil
	}

	for attempt := 1; ; attempt++ {
		candidate := sliceSlugCandidate(slice, attempt)
		exists, err := s.sliceSlugExists(ctx, q, slice.CreatedBy, candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
}

func (s *PostgresNativeStorage) hydrateRootSliceFiles(ctx context.Context, q queryable, sl *models.Slice) error {
	if sl == nil || !sl.IsRoot || strings.TrimSpace(sl.ID) == "" {
		return nil
	}
	rows, err := q.Query(ctx, `
		SELECT path
		FROM directory_entries
		WHERE slice_id = $1
		  AND type = 'file'
		ORDER BY path
	`, sl.ID)
	if err != nil {
		return err
	}
	defer rows.Close()

	files := make([]string, 0)
	for rows.Next() {
		var filePath string
		if err := rows.Scan(&filePath); err != nil {
			return err
		}
		if filePath = cleanRelativePath(filePath); filePath != "" {
			files = append(files, filePath)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(files) > 0 {
		seen := make(map[string]struct{}, len(sl.Files)+len(files))
		merged := make([]string, 0, len(sl.Files)+len(files))
		for _, existing := range sl.Files {
			cleaned := cleanRelativePath(existing)
			if cleaned == "" {
				continue
			}
			if _, ok := seen[cleaned]; ok {
				continue
			}
			seen[cleaned] = struct{}{}
			merged = append(merged, cleaned)
		}
		for _, filePath := range files {
			if _, ok := seen[filePath]; ok {
				continue
			}
			seen[filePath] = struct{}{}
			merged = append(merged, filePath)
		}
		sort.Strings(merged)
		sl.Files = merged
	}
	return nil
}

func (s *PostgresNativeStorage) scanSlice(ctx context.Context, q queryable, sql string, args ...interface{}) (*models.Slice, error) {
	var sl models.Slice
	var filesJSON, ownersJSON, mountsJSON []byte
	var parentID *string
	var visibility string

	err := q.QueryRow(ctx, sql, args...).Scan(
		&sl.ID, &sl.Name, &sl.Slug, &sl.Description, &sl.CreatedBy, &parentID,
		&sl.IsRoot, &visibility, &filesJSON, &mountsJSON, &ownersJSON,
		&sl.CreatedAt, &sl.UpdatedAt, &sl.Environment,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrSliceNotFound
		}
		return nil, err
	}

	if parentID != nil {
		sl.ParentSlice = *parentID
	}
	sl.Visibility = models.NormalizeVisibility(models.Visibility(visibility))
	if err := json.Unmarshal(filesJSON, &sl.Files); err != nil {
		sl.Files = []string{}
	}
	if err := json.Unmarshal(ownersJSON, &sl.Owners); err != nil {
		sl.Owners = []string{}
	}
	if err := json.Unmarshal(mountsJSON, &sl.FolderMounts); err != nil {
		sl.FolderMounts = nil
	}
	if strings.TrimSpace(sl.Slug) == "" {
		sl.Slug = defaultSliceSlug(&sl)
	}
	if err := s.hydrateRootSliceFiles(ctx, q, &sl); err != nil {
		return nil, err
	}

	return &sl, nil
}

func (s *PostgresNativeStorage) collectSlices(rows pgx.Rows) ([]*models.Slice, error) {
	var result []*models.Slice
	for rows.Next() {
		var sl models.Slice
		var filesJSON, ownersJSON, mountsJSON []byte
		var parentID *string
		var visibility string
		if err := rows.Scan(
			&sl.ID, &sl.Name, &sl.Slug, &sl.Description, &sl.CreatedBy, &parentID,
			&sl.IsRoot, &visibility, &filesJSON, &mountsJSON, &ownersJSON,
			&sl.CreatedAt, &sl.UpdatedAt, &sl.Environment,
		); err != nil {
			return nil, err
		}
		if parentID != nil {
			sl.ParentSlice = *parentID
		}
		sl.Visibility = models.NormalizeVisibility(models.Visibility(visibility))
		if err := json.Unmarshal(filesJSON, &sl.Files); err != nil {
			sl.Files = []string{}
		}
		if err := json.Unmarshal(ownersJSON, &sl.Owners); err != nil {
			sl.Owners = []string{}
		}
		if err := json.Unmarshal(mountsJSON, &sl.FolderMounts); err != nil {
			sl.FolderMounts = nil
		}
		if strings.TrimSpace(sl.Slug) == "" {
			sl.Slug = defaultSliceSlug(&sl)
		}
		result = append(result, &sl)
	}
	if result == nil {
		result = []*models.Slice{}
	}
	return result, rows.Err()
}

func (s *PostgresNativeStorage) getSliceByOwnerAndSlug(ctx context.Context, q queryable, owner, slug string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, q, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE created_by = $1 AND slug = $2 LIMIT 1`, strings.TrimSpace(owner), strings.TrimSpace(slug))
}

func (s *PostgresNativeStorage) getSliceBySlug(ctx context.Context, q queryable, slug string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	slug = strings.TrimSpace(slug)
	if owner, local, ok := SplitQualifiedSliceRef(slug); ok {
		return s.getSliceByOwnerAndSlug(ctx, q, owner, local)
	}
	rows, err := q.Query(ctx, `SELECT id, name, slug, description, created_by, COALESCE(parent_id, ''), is_root, visibility, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE slug = $1 ORDER BY id LIMIT 2`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	slices, err := s.collectSlices(rows)
	if err != nil {
		return nil, err
	}
	if len(slices) == 0 {
		return nil, ErrSliceNotFound
	}
	if len(slices) > 1 {
		return nil, ErrSliceNotFound
	}
	return slices[0], nil
}

func (s *PostgresNativeStorage) getPathVisibilityRule(ctx context.Context, q queryable, p string) (*models.PathVisibilityRule, error) {
	ctx = ensureCtx(ctx)
	pathKey := normalizeVisibilityPath(p)
	if pathKey == "" {
		return nil, ErrInvalidInput
	}

	var rule models.PathVisibilityRule
	err := q.QueryRow(ctx, `
		SELECT path, entry_type, visibility, updated_by, updated_at
		FROM path_visibility
		WHERE path = $1
	`, pathKey).Scan(&rule.Path, &rule.EntryType, &rule.Visibility, &rule.UpdatedBy, &rule.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return copyPathVisibilityRule(&rule), nil
}

func (s *PostgresNativeStorage) listPathVisibilityRules(ctx context.Context, q queryable, pathPrefix string) ([]*models.PathVisibilityRule, error) {
	ctx = ensureCtx(ctx)
	prefix := normalizeVisibilityPath(pathPrefix)

	sql := `
		SELECT path, entry_type, visibility, updated_by, updated_at
		FROM path_visibility
	`
	args := []interface{}{}
	if prefix != "" {
		sql += ` WHERE path = $1 OR path LIKE $1 || '/%'`
		args = append(args, prefix)
	}
	sql += ` ORDER BY path`

	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]*models.PathVisibilityRule, 0)
	for rows.Next() {
		var rule models.PathVisibilityRule
		if err := rows.Scan(&rule.Path, &rule.EntryType, &rule.Visibility, &rule.UpdatedBy, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, copyPathVisibilityRule(&rule))
	}
	if rules == nil {
		rules = []*models.PathVisibilityRule{}
	}
	return rules, rows.Err()
}

func (s *PostgresNativeStorage) upsertPathVisibilityRule(ctx context.Context, q execable, rule *models.PathVisibilityRule) error {
	ctx = ensureCtx(ctx)
	if rule == nil {
		return ErrInvalidInput
	}
	pathKey := normalizeVisibilityPath(rule.Path)
	if pathKey == "" {
		return ErrInvalidInput
	}

	updatedAt := rule.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}

	_, err := q.Exec(ctx, `
		INSERT INTO path_visibility (path, entry_type, visibility, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (path) DO UPDATE SET
			entry_type = EXCLUDED.entry_type,
			visibility = EXCLUDED.visibility,
			updated_by = EXCLUDED.updated_by,
			updated_at = EXCLUDED.updated_at
	`, pathKey, string(rule.EntryType), string(models.NormalizeVisibility(rule.Visibility)), rule.UpdatedBy, updatedAt)
	return err
}

func (s *PostgresNativeStorage) deletePathVisibilityRule(ctx context.Context, q execable, p string) error {
	ctx = ensureCtx(ctx)
	pathKey := normalizeVisibilityPath(p)
	if pathKey == "" {
		return ErrInvalidInput
	}
	tag, err := q.Exec(ctx, `DELETE FROM path_visibility WHERE path = $1`, pathKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) collectFileChanges(rows pgx.Rows) ([]*models.FileChangeRecord, error) {
	var result []*models.FileChangeRecord
	for rows.Next() {
		var fc models.FileChangeRecord
		var changeType string
		if err := rows.Scan(
			&fc.ID, &fc.SliceID, &fc.CommitHash, &fc.Path, &fc.OldPath,
			&changeType, &fc.OldHash, &fc.NewHash,
			&fc.LinesAdded, &fc.LinesDeleted, &fc.Author, &fc.Message, &fc.Timestamp,
		); err != nil {
			return nil, err
		}
		fc.ChangeType = models.ChangeType(changeType)
		result = append(result, &fc)
	}
	if result == nil {
		result = []*models.FileChangeRecord{}
	}
	return result, rows.Err()
}
