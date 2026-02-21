package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niczy/gitslice/internal/auth"
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

func (s *PostgresNativeStorage) materializeDirectoryTreeTx(ctx context.Context, tx pgx.Tx, sliceID string, rawFiles []string, includeFiles bool) error {
	paths := collectMaterializedPaths(rawFiles)
	dirs := computeDirSet(paths)

	batch := &pgx.Batch{}
	enqueue := func(id, pth, typ, parent string, size int64) {
		batch.Queue(`
			INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size)
			VALUES ($1,$2,$3,$4,$5,NULL,$6)
			ON CONFLICT (slice_id, path) DO UPDATE SET
				type = EXCLUDED.type,
				parent_id = EXCLUDED.parent_id,
				size = EXCLUDED.size,
				updated_at = NOW()
		`, id, sliceID, pth, typ, parent, size)
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

	res := tx.SendBatch(ctx, batch)
	defer res.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := res.Exec(); err != nil {
			return err
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

	return &PostgresNativeStorage{
		pool:        pool,
		objectStore: objectStore,
		namespace:   namespace,
	}, nil
}

// Close closes the backing Postgres pool.
func (s *PostgresNativeStorage) Close() error {
	s.pool.Close()
	return nil
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
			organization_members,
			organizations,
			auth_sessions,
			users,
			file_changes,
			global_state,
			versioned_content,
			commit_snapshots,
			file_contents,
			directory_entries,
			slice_commits,
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

type postgresNativeTxView struct {
	*PostgresNativeStorage
	tx pgx.Tx
}

// ---- Transactional overrides used by BulkWrite (git import) ----

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

func (s *postgresNativeTxView) CreateSlice(ctx context.Context, slice *models.Slice) error {
	ctx = ensureCtx(ctx)
	if slice == nil || slice.ID == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	if slice.CreatedAt.IsZero() {
		slice.CreatedAt = now
	}
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

	_, err := s.tx.Exec(ctx, `
		INSERT INTO slices (id, name, description, created_by, parent_id, is_root, files, folder_mounts, owners, created_at, updated_at, environment)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12)
	`, slice.ID, slice.Name, slice.Description, slice.CreatedBy,
		slice.ParentSlice, slice.IsRoot, filesJSON, mountsJSON, ownersJSON,
		slice.CreatedAt, slice.UpdatedAt, slice.Environment)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrSliceAlreadyExists
		}
		return err
	}

	initialCommitHash := fmt.Sprintf("init-%s", slice.ID)
	_, err = s.tx.Exec(ctx, `
		INSERT INTO slice_metadata (slice_id, head_commit_hash, modified_files, last_modified, modified_files_count)
		VALUES ($1, $2, '[]', $3, 0)
	`, slice.ID, initialCommitHash, now)
	if err != nil {
		return err
	}

	for _, fileID := range slice.Files {
		_, err = s.tx.Exec(ctx, `
			INSERT INTO file_slice_index (file_id, slice_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, fileID, slice.ID)
		if err != nil {
			return err
		}
	}

	// Materialize directory-entry tree from the slice file set.
	if err := s.materializeDirectoryTreeTx(ctx, s.tx, slice.ID, slice.Files, true); err != nil {
		return err
	}

	return nil
}

func (s *postgresNativeTxView) GetSlice(ctx context.Context, sliceID string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.tx, `SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE id = $1`, sliceID)
}

func (s *postgresNativeTxView) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.tx, `SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE is_root = true LIMIT 1`)
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
		ID:          "root_slice",
		Name:        "Root Slice",
		Description: "The root slice containing all files",
		Files:       []string{},
		Owners:      []string{"system"},
		CreatedBy:   "system",
		IsRoot:      true,
	}
	if err := s.CreateSlice(ctx, rootSlice); err != nil {
		return err
	}

	_, err := s.tx.Exec(ctx, `UPDATE slice_metadata SET head_commit_hash = 'root-initial' WHERE slice_id = $1`, rootSlice.ID)
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

	_, err := s.tx.Exec(ctx, `
		INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size)
		VALUES ($1, $2, $3, $4, $5, NULL, $6)
		ON CONFLICT (slice_id, path) DO UPDATE SET
			type = EXCLUDED.type,
			parent_id = EXCLUDED.parent_id,
			size = EXCLUDED.size,
			updated_at = NOW()
	`, insertID, sliceID, p, typ, nativeParentID(sliceID, p), entry.Size)
	return err
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

	tag, err := s.tx.Exec(ctx, `
		UPDATE directory_entries SET path = $1, type = $2, parent_id = $3, content = NULL, size = $4, updated_at = NOW()
		WHERE id = $5
	`, p, typ, nativeParentID(sliceID, p), entry.Size, entry.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *postgresNativeTxView) DeleteEntry(ctx context.Context, entryID string) error {
	ctx = ensureCtx(ctx)
	tag, err := s.tx.Exec(ctx, `DELETE FROM directory_entries WHERE id = $1`, entryID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *postgresNativeTxView) AddFileContent(ctx context.Context, content *models.FileContent) error {
	ctx = ensureCtx(ctx)
	if content == nil || content.FileID == "" {
		return ErrInvalidInput
	}

	// Store blob in object store (idempotent, content-addressed).
	raw, err := json.Marshal(content)
	if err != nil {
		return err
	}
	if err := s.objectStore.PutObject(ctx, s.objKey("file_content", content.FileID), raw); err != nil {
		return err
	}
	if content.Hash != "" {
		if err := s.objectStore.PutObject(ctx, s.objKey("versioned_content", content.Hash), raw); err != nil {
			return err
		}
	}

	_, err = s.tx.Exec(ctx, `
		INSERT INTO file_contents (file_id, path, size, hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (file_id) DO UPDATE SET path = $2, size = $3, hash = $4
	`, content.FileID, content.Path, content.Size, content.Hash)
	if err != nil {
		return err
	}
	if content.Hash != "" {
		_, err = s.tx.Exec(ctx, `
			INSERT INTO versioned_content (content_hash, file_id, path, size)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (content_hash) DO NOTHING
		`, content.Hash, content.FileID, content.Path, content.Size)
		if err != nil {
			return err
		}
	}
	return nil
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
				GlobalCommitHash: "global-init",
				History:          []*models.GlobalCommit{},
			}, nil
		}
		return nil, err
	}
	var stateData struct {
		History []*models.GlobalCommit `json:"history"`
	}
	if err := json.Unmarshal(stateJSON, &stateData); err == nil {
		gs.History = stateData.History
	}
	if gs.History == nil {
		gs.History = []*models.GlobalCommit{}
	}
	return &gs, nil
}

func (s *postgresNativeTxView) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	ctx = ensureCtx(ctx)
	if state == nil {
		return ErrInvalidInput
	}
	stateData := struct {
		History []*models.GlobalCommit `json:"history"`
	}{
		History: state.History,
	}
	stateJSON, _ := json.Marshal(stateData)
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
	for _, change := range changes {
		if change == nil || change.ID == "" {
			continue
		}
		_, err := s.tx.Exec(ctx, `
			INSERT INTO file_changes (id, slice_id, commit_hash, path, old_path, change_type, old_hash, new_hash, lines_added, lines_deleted, author, message, committed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT (id) DO NOTHING
		`, change.ID, change.SliceID, change.CommitHash, change.Path, change.OldPath, string(change.ChangeType),
			change.OldHash, change.NewHash, change.LinesAdded, change.LinesDeleted, change.Author, change.Message, change.Timestamp)
		if err != nil {
			return err
		}
	}
	return nil
}

// ============ Slice Operations ============

func (s *PostgresNativeStorage) CreateSlice(ctx context.Context, slice *models.Slice) error {
	ctx = ensureCtx(ctx)
	if slice.ID == "" {
		return ErrInvalidInput
	}

	now := time.Now()
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

	_, err = tx.Exec(ctx, `
		INSERT INTO slices (id, name, description, created_by, parent_id, is_root, files, folder_mounts, owners, created_at, updated_at, environment)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12)
	`, slice.ID, slice.Name, slice.Description, slice.CreatedBy,
		slice.ParentSlice, slice.IsRoot, filesJSON, mountsJSON, ownersJSON,
		slice.CreatedAt, slice.UpdatedAt, slice.Environment)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrSliceAlreadyExists
		}
		return err
	}

	initialCommitHash := fmt.Sprintf("init-%s", slice.ID)
	_, err = tx.Exec(ctx, `
		INSERT INTO slice_metadata (slice_id, head_commit_hash, modified_files, last_modified, modified_files_count)
		VALUES ($1, $2, '[]', $3, 0)
	`, slice.ID, initialCommitHash, now)
	if err != nil {
		return err
	}

	// Index files from the slice definition.
	for _, fileID := range slice.Files {
		_, err = tx.Exec(ctx, `
			INSERT INTO file_slice_index (file_id, slice_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, fileID, slice.ID)
		if err != nil {
			return err
		}
	}

	// Materialize directory-entry tree from the slice file set so ListEntries can
	// list direct children without scanning all descendant paths.
	if err := s.materializeDirectoryTreeTx(ctx, tx, slice.ID, slice.Files, true); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) GetSlice(ctx context.Context, sliceID string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.pool, `SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE id = $1`, sliceID)
}

func (s *PostgresNativeStorage) ListSlices(ctx context.Context, limit, offset int) ([]*models.Slice, error) {
	ctx = ensureCtx(ctx)
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment
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
		SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment
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
		SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment
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
	return s.scanSlice(ctx, s.pool, `SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE is_root = true LIMIT 1`)
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
		ID:          "root_slice",
		Name:        "Root Slice",
		Description: "The root slice containing all files",
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
		UPDATE slice_metadata SET head_commit_hash = 'root-initial' WHERE slice_id = $1
	`, rootSlice.ID)
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

// GetSliceByName retrieves the first non-root slice matching the given display name.
func (s *PostgresNativeStorage) GetSliceByName(ctx context.Context, name string) (*models.Slice, error) {
	ctx = ensureCtx(ctx)
	return s.scanSlice(ctx, s.pool, `SELECT id, name, description, created_by, COALESCE(parent_id, ''), is_root, files, folder_mounts, owners, created_at, updated_at, environment FROM slices WHERE name = $1 AND is_root = false LIMIT 1`, name)
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
	`, sliceID, commit.CommitHash, commit.ParentHash, commit.Message, commit.Timestamp)
	return err
}

func (s *PostgresNativeStorage) ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error) {
	ctx = ensureCtx(ctx)

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
	_, err = tx.Exec(ctx, `
		INSERT INTO slice_locks (slice_id) VALUES ($1)
		ON CONFLICT (slice_id) DO NOTHING
	`, sliceID)
	if err != nil {
		return err
	}

	// Acquire file locks.
	for _, fileID := range fileIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO file_locks (file_id, owner_slice_id) VALUES ($1, $2)
			ON CONFLICT (file_id) DO UPDATE SET owner_slice_id = $2
		`, fileID, sliceID)
		if err != nil {
			return err
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
	return nil
}

// ============ Changeset Operations ============

func (s *PostgresNativeStorage) CreateChangeset(ctx context.Context, changeset *models.Changeset) error {
	ctx = ensureCtx(ctx)

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

	_, err := s.pool.Exec(ctx, `
		INSERT INTO changeset_snapshots (id, changeset_id, version, hash, base_commit_hash, modified_files, author, message, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, snapshot.ID, snapshot.ChangesetID, snapshot.Version, snapshot.Hash, snapshot.BaseCommitHash,
		modifiedJSON, snapshot.Author, snapshot.Message, snapshot.CreatedAt)
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
			SELECT id, changeset_id, version, hash, base_commit_hash, modified_files, author, message, created_at
			FROM changeset_snapshots
			WHERE changeset_id = $1
			ORDER BY version DESC
			LIMIT 1
		`, changesetID)
	} else {
		row = s.pool.QueryRow(ctx, `
			SELECT id, changeset_id, version, hash, base_commit_hash, modified_files, author, message, created_at
			FROM changeset_snapshots
			WHERE changeset_id = $1 AND version = $2
			LIMIT 1
		`, changesetID, version)
	}

	var snapshot models.ChangesetSnapshot
	var modifiedJSON []byte
	err := row.Scan(
		&snapshot.ID,
		&snapshot.ChangesetID,
		&snapshot.Version,
		&snapshot.Hash,
		&snapshot.BaseCommitHash,
		&modifiedJSON,
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
	return &snapshot, nil
}

func (s *PostgresNativeStorage) ListChangesetSnapshots(ctx context.Context, changesetID string, limit int) ([]*models.ChangesetSnapshot, error) {
	ctx = ensureCtx(ctx)

	if limit <= 0 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, changeset_id, version, hash, base_commit_hash, modified_files, author, message, created_at
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
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.ChangesetID,
			&snapshot.Version,
			&snapshot.Hash,
			&snapshot.BaseCommitHash,
			&modifiedJSON,
			&snapshot.Author,
			&snapshot.Message,
			&snapshot.CreatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(modifiedJSON, &snapshot.ModifiedFiles); err != nil {
			snapshot.ModifiedFiles = []string{}
		}
		snapshotCopy := snapshot
		result = append(result, &snapshotCopy)
	}
	if result == nil {
		result = []*models.ChangesetSnapshot{}
	}
	return result, rows.Err()
}

// ============ File Content ============

func (s *PostgresNativeStorage) GetSliceFiles(ctx context.Context, sliceID string) ([]*models.FileContent, error) {
	ctx = ensureCtx(ctx)

	// Get the file list from the slice.
	var filesJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT files FROM slices WHERE id = $1`, sliceID).Scan(&filesJSON)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrSliceNotFound
		}
		return nil, err
	}

	var fileIDs []string
	if err := json.Unmarshal(filesJSON, &fileIDs); err != nil {
		return nil, err
	}

	var files []*models.FileContent
	for _, fileID := range fileIDs {
		// Try to hydrate from object store.
		raw, err := s.objectStore.GetObject(ctx, s.objKey("file_content", fileID))
		if err == nil {
			var fc models.FileContent
			if err := json.Unmarshal(raw, &fc); err == nil {
				files = append(files, &fc)
				continue
			}
		}

		// Fall back to metadata from file_contents table.
		var fc models.FileContent
		err = s.pool.QueryRow(ctx, `
			SELECT file_id, path, size, hash FROM file_contents WHERE file_id = $1
		`, fileID).Scan(&fc.FileID, &fc.Path, &fc.Size, &fc.Hash)
		if err == nil {
			files = append(files, &fc)
		}
	}
	return files, nil
}

func (s *PostgresNativeStorage) GetSliceFileByPath(ctx context.Context, sliceID, path string) (*models.FileContent, error) {
	ctx = ensureCtx(ctx)

	// Look up the directory entry.
	var entryID string
	var entrySize int64
	err := s.pool.QueryRow(ctx, `
		SELECT id, size FROM directory_entries WHERE slice_id = $1 AND path = $2
	`, sliceID, path).Scan(&entryID, &entrySize)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}

	// Try object store hydration using path as file ID.
	for _, candidateID := range []string{path, entryID} {
		if candidateID == "" {
			continue
		}
		raw, err := s.objectStore.GetObject(ctx, s.objKey("file_content", candidateID))
		if err != nil {
			continue
		}
		var fc models.FileContent
		if err := json.Unmarshal(raw, &fc); err != nil {
			continue
		}
		if fc.FileID == "" {
			fc.FileID = candidateID
		}
		if fc.Path == "" {
			fc.Path = path
		}
		if fc.Size == 0 {
			fc.Size = entrySize
		}
		return &fc, nil
	}

	// Fall back to file_contents table metadata.
	var fc models.FileContent
	err = s.pool.QueryRow(ctx, `
		SELECT file_id, path, size, hash FROM file_contents WHERE file_id = $1
	`, path).Scan(&fc.FileID, &fc.Path, &fc.Size, &fc.Hash)
	if err == nil {
		return &fc, nil
	}

	// Try by entry ID.
	err = s.pool.QueryRow(ctx, `
		SELECT file_id, path, size, hash FROM file_contents WHERE file_id = $1
	`, entryID).Scan(&fc.FileID, &fc.Path, &fc.Size, &fc.Hash)
	if err == nil {
		if fc.Path == "" {
			fc.Path = path
		}
		return &fc, nil
	}

	// Last resort: return metadata-only entry.
	return &models.FileContent{
		FileID: path,
		Path:   path,
		Size:   entrySize,
	}, nil
}

func (s *PostgresNativeStorage) AddFileContent(ctx context.Context, content *models.FileContent) error {
	ctx = ensureCtx(ctx)
	if content == nil || content.FileID == "" {
		return ErrInvalidInput
	}

	// Store blob in object store.
	raw, err := json.Marshal(content)
	if err != nil {
		return err
	}
	if err := s.objectStore.PutObject(ctx, s.objKey("file_content", content.FileID), raw); err != nil {
		return err
	}
	if content.Hash != "" {
		if err := s.objectStore.PutObject(ctx, s.objKey("versioned_content", content.Hash), raw); err != nil {
			return err
		}
	}

	// Upsert metadata in DB.
	_, err = s.pool.Exec(ctx, `
		INSERT INTO file_contents (file_id, path, size, hash)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (file_id) DO UPDATE SET path = $2, size = $3, hash = $4
	`, content.FileID, content.Path, content.Size, content.Hash)
	if err != nil {
		return err
	}

	if content.Hash != "" {
		_, err = s.pool.Exec(ctx, `
			INSERT INTO versioned_content (content_hash, file_id, path, size)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (content_hash) DO NOTHING
		`, content.Hash, content.FileID, content.Path, content.Size)
		if err != nil {
			return err
		}
	}

	return nil
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

	_, err = tx.Exec(ctx, `
		INSERT INTO directory_entries (id, slice_id, path, type, parent_id, content, size)
		VALUES ($1, $2, $3, $4, $5, NULL, $6)
		ON CONFLICT (slice_id, path) DO UPDATE SET
			type = EXCLUDED.type,
			parent_id = EXCLUDED.parent_id,
			size = EXCLUDED.size,
			updated_at = NOW()
	`, insertID, sliceID, p, typ, nativeParentID(sliceID, p), entry.Size)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) GetEntry(ctx context.Context, entryID string) (*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)

	var e models.DirectoryEntry
	err := s.pool.QueryRow(ctx, `
		SELECT id, path, type, parent_id, content, size FROM directory_entries WHERE id = $1
	`, entryID).Scan(&e.ID, &e.Path, &e.Type, &e.ParentID, &e.Content, &e.Size)
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
		SELECT id, path, type, parent_id, content, size FROM directory_entries
		WHERE slice_id = $1 AND path = $2
	`, sliceID, path).Scan(&e.ID, &e.Path, &e.Type, &e.ParentID, &e.Content, &e.Size)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (s *PostgresNativeStorage) ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error) {
	ctx = ensureCtx(ctx)

	rows, err := s.pool.Query(ctx, `
		SELECT id, path, type, parent_id, content, size
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
		if err := rows.Scan(&e.ID, &e.Path, &e.Type, &e.ParentID, &e.Content, &e.Size); err != nil {
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

	tag, err := tx.Exec(ctx, `
		UPDATE directory_entries SET path = $1, type = $2, parent_id = $3, content = NULL, size = $4, updated_at = NOW()
		WHERE id = $5
	`, p, typ, nativeParentID(sliceID, p), entry.Size, entry.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) DeleteEntry(ctx context.Context, entryID string) error {
	ctx = ensureCtx(ctx)

	tag, err := s.pool.Exec(ctx, `DELETE FROM directory_entries WHERE id = $1`, entryID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
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
				GlobalCommitHash: "global-init",
				History:          []*models.GlobalCommit{},
			}, nil
		}
		return nil, err
	}

	// Decode history from state_json.
	var stateData struct {
		History []*models.GlobalCommit `json:"history"`
	}
	if err := json.Unmarshal(stateJSON, &stateData); err == nil {
		gs.History = stateData.History
	}
	if gs.History == nil {
		gs.History = []*models.GlobalCommit{}
	}

	return &gs, nil
}

func (s *PostgresNativeStorage) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	ctx = ensureCtx(ctx)

	stateData := struct {
		History []*models.GlobalCommit `json:"history"`
	}{
		History: state.History,
	}
	stateJSON, _ := json.Marshal(stateData)

	_, err := s.pool.Exec(ctx, `
		INSERT INTO global_state (id, global_commit_hash, updated_at, state_json)
		VALUES (true, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET global_commit_hash = $1, updated_at = $2, state_json = $3
	`, state.GlobalCommitHash, state.Timestamp, stateJSON)
	return err
}

// ============ Health Check ============

func (s *PostgresNativeStorage) Ping(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	if err := s.pool.Ping(ctx); err != nil {
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

func (s *PostgresNativeStorage) GetFileContentByHash(ctx context.Context, contentHash string) (*models.FileContent, error) {
	ctx = ensureCtx(ctx)

	// Try object store.
	raw, err := s.objectStore.GetObject(ctx, s.objKey("versioned_content", contentHash))
	if err == nil {
		var content models.FileContent
		if err := json.Unmarshal(raw, &content); err == nil {
			return &content, nil
		}
	}

	// Try versioned_content table metadata.
	var fc models.FileContent
	err = s.pool.QueryRow(ctx, `
		SELECT file_id, path, size FROM versioned_content WHERE content_hash = $1
	`, contentHash).Scan(&fc.FileID, &fc.Path, &fc.Size)
	if err == nil {
		fc.Hash = contentHash
		return &fc, nil
	}

	return nil, ErrEntryNotFound
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

	content, err := s.GetFileContentByHash(ctx, contentHash)
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
	`, change.ID, change.SliceID, change.CommitHash, change.Path, change.OldPath,
		string(change.ChangeType), change.OldHash, change.NewHash,
		change.LinesAdded, change.LinesDeleted, change.Author, change.Message, change.Timestamp)
	return err
}

func (s *PostgresNativeStorage) AddFileChanges(ctx context.Context, changes []*models.FileChangeRecord) error {
	ctx = ensureCtx(ctx)
	for _, change := range changes {
		if err := s.AddFileChange(ctx, change); err != nil {
			return err
		}
	}
	return nil
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

	now := time.Now()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (username, name, primary_email, password_hash, created_at, updated_at)
		VALUES ($1, '', '', '', $2, $3)
		ON CONFLICT (username) DO NOTHING
	`, username, now, now)
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
		SELECT username, COALESCE(name, ''), COALESCE(primary_email, ''), COALESCE(password_hash, ''), created_at, updated_at
		FROM users WHERE username = $1
	`, username).
		Scan(&u.Username, &u.Name, &u.PrimaryEmail, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	u.PrimaryEmail = strings.ToLower(strings.TrimSpace(u.PrimaryEmail))
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
		SELECT username, COALESCE(name, ''), COALESCE(primary_email, ''), COALESCE(password_hash, ''), created_at, updated_at
		FROM users
		WHERE lower(primary_email) = $1 AND primary_email <> ''
		LIMIT 1
	`, email).
		Scan(&u.Username, &u.Name, &u.PrimaryEmail, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	u.PrimaryEmail = strings.ToLower(strings.TrimSpace(u.PrimaryEmail))
	return &u, nil
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
	email := strings.ToLower(strings.TrimSpace(user.PrimaryEmail))
	now := time.Now()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	user.UpdatedAt = now

	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (username, name, primary_email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, username, user.Name, email, user.PasswordHash, user.CreatedAt, user.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
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
	now := time.Now()

	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		SET name = $1, primary_email = $2, password_hash = $3, updated_at = $4
		WHERE username = $5
	`, user.Name, email, user.PasswordHash, now, username)
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
			session_id, username, token, device_info, created_at, last_seen_at, revoked_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, NULL
		)
	`, session.SessionID, session.Username, session.Token, session.DeviceInfo, session.CreatedAt, session.LastSeenAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetAuthSessionByToken(ctx context.Context, token string) (*models.AuthSession, error) {
	ctx = ensureCtx(ctx)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidInput
	}

	var session models.AuthSession
	var revokedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT session_id, username, token, COALESCE(device_info, ''), created_at, last_seen_at, revoked_at
		FROM auth_sessions
		WHERE token = $1 AND revoked_at IS NULL
		LIMIT 1
	`, token).Scan(
		&session.SessionID,
		&session.Username,
		&session.Token,
		&session.DeviceInfo,
		&session.CreatedAt,
		&session.LastSeenAt,
		&revokedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
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
		SELECT session_id, username, token, COALESCE(device_info, ''), created_at, last_seen_at, revoked_at
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
		var revokedAt *time.Time
		if err := rows.Scan(
			&session.SessionID,
			&session.Username,
			&session.Token,
			&session.DeviceInfo,
			&session.CreatedAt,
			&session.LastSeenAt,
			&revokedAt,
		); err != nil {
			return nil, err
		}
		session.RevokedAt = revokedAt
		sessionCopy := session
		out = append(out, &sessionCopy)
	}
	if out == nil {
		out = []*models.AuthSession{}
	}
	return out, rows.Err()
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

func (s *PostgresNativeStorage) CreateOrganization(ctx context.Context, org *models.Organization) error {
	ctx = ensureCtx(ctx)
	if org == nil || org.Slug == "" || org.Name == "" || org.CreatedBy == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(org.CreatedBy) {
		return ErrInvalidInput
	}

	now := time.Now()
	if org.CreatedAt.IsZero() {
		org.CreatedAt = now
	}
	org.UpdatedAt = now

	_, err := s.pool.Exec(ctx, `
		INSERT INTO organizations (slug, name, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`, org.Slug, org.Name, org.CreatedBy, org.CreatedAt, org.UpdatedAt)
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
	err := s.pool.QueryRow(ctx, `SELECT slug, name, created_by, created_at, updated_at FROM organizations WHERE slug = $1`, orgSlug).
		Scan(&org.Slug, &org.Name, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &org, nil
}

func (s *PostgresNativeStorage) AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	ctx = ensureCtx(ctx)
	if member == nil || member.OrgSlug == "" || member.Username == "" || member.Role == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(member.Username) {
		return ErrInvalidInput
	}

	// Verify org exists.
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE slug = $1)`, member.OrgSlug).Scan(&exists)
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

func (s *PostgresNativeStorage) ListOrganizationsForUser(ctx context.Context, username string) ([]*models.Organization, error) {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	rows, err := s.pool.Query(ctx, `
		SELECT o.slug, o.name, o.created_by, o.created_at, o.updated_at
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
		if err := rows.Scan(&org.Slug, &org.Name, &org.CreatedBy, &org.CreatedAt, &org.UpdatedAt); err != nil {
			return nil, err
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
			session_id, slice_id, environment_name, user_id, state, provider, e2b_template_id, e2b_sandbox_id, e2b_region,
			idle_timeout_sec, ttl_sec, runtime_endpoint, created_at, updated_at, started_at, last_activity_at,
			stopped_at, failure_code, failure_message
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''),
			$10, $11, NULLIF($12, ''), $13, $14, $15, $16, $17, NULLIF($18, ''), NULLIF($19, '')
		)
	`,
		session.SessionID, session.SliceID, session.EnvironmentName, session.UserID, string(session.State), session.Provider,
		session.E2BTemplateID, session.E2BSandboxID, session.E2BRegion,
		session.IdleTimeoutSec, session.TTLSec, session.RuntimeEndpoint,
		session.CreatedAt, session.UpdatedAt, session.StartedAt, session.LastActivityAt, session.StoppedAt,
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
		SELECT session_id, slice_id, COALESCE(environment_name, ''), user_id, state, provider, e2b_template_id, COALESCE(e2b_sandbox_id, ''),
		       COALESCE(e2b_region, ''), idle_timeout_sec, ttl_sec, COALESCE(runtime_endpoint, ''),
		       created_at, updated_at, started_at, last_activity_at, stopped_at,
		       COALESCE(failure_code, ''), COALESCE(failure_message, '')
		FROM agent_sessions
		WHERE session_id = $1
	`, sessionID).Scan(
		&session.SessionID, &session.SliceID, &session.EnvironmentName, &session.UserID, &session.State, &session.Provider,
		&session.E2BTemplateID, &session.E2BSandboxID, &session.E2BRegion, &session.IdleTimeoutSec, &session.TTLSec,
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
		    user_id = $3,
		    state = $4,
		    provider = $5,
		    e2b_template_id = $6,
		    e2b_sandbox_id = NULLIF($7, ''),
		    e2b_region = NULLIF($8, ''),
		    idle_timeout_sec = $9,
		    ttl_sec = $10,
		    runtime_endpoint = NULLIF($11, ''),
		    updated_at = $12,
		    started_at = $13,
		    last_activity_at = $14,
		    stopped_at = $15,
		    failure_code = NULLIF($16, ''),
		    failure_message = NULLIF($17, '')
		WHERE session_id = $18
	`,
		session.SliceID, session.EnvironmentName, session.UserID, string(session.State), session.Provider, session.E2BTemplateID,
		session.E2BSandboxID, session.E2BRegion, session.IdleTimeoutSec, session.TTLSec, session.RuntimeEndpoint,
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
}

func (s *PostgresNativeStorage) scanSlice(ctx context.Context, q queryable, sql string, args ...interface{}) (*models.Slice, error) {
	var sl models.Slice
	var filesJSON, ownersJSON, mountsJSON []byte
	var parentID *string

	err := q.QueryRow(ctx, sql, args...).Scan(
		&sl.ID, &sl.Name, &sl.Description, &sl.CreatedBy, &parentID,
		&sl.IsRoot, &filesJSON, &mountsJSON, &ownersJSON,
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
	if err := json.Unmarshal(filesJSON, &sl.Files); err != nil {
		sl.Files = []string{}
	}
	if err := json.Unmarshal(ownersJSON, &sl.Owners); err != nil {
		sl.Owners = []string{}
	}
	if err := json.Unmarshal(mountsJSON, &sl.FolderMounts); err != nil {
		sl.FolderMounts = nil
	}

	return &sl, nil
}

func (s *PostgresNativeStorage) collectSlices(rows pgx.Rows) ([]*models.Slice, error) {
	var result []*models.Slice
	for rows.Next() {
		var sl models.Slice
		var filesJSON, ownersJSON, mountsJSON []byte
		var parentID *string
		if err := rows.Scan(
			&sl.ID, &sl.Name, &sl.Description, &sl.CreatedBy, &parentID,
			&sl.IsRoot, &filesJSON, &mountsJSON, &ownersJSON,
			&sl.CreatedAt, &sl.UpdatedAt, &sl.Environment,
		); err != nil {
			return nil, err
		}
		if parentID != nil {
			sl.ParentSlice = *parentID
		}
		if err := json.Unmarshal(filesJSON, &sl.Files); err != nil {
			sl.Files = []string{}
		}
		if err := json.Unmarshal(ownersJSON, &sl.Owners); err != nil {
			sl.Owners = []string{}
		}
		if err := json.Unmarshal(mountsJSON, &sl.FolderMounts); err != nil {
			sl.FolderMounts = nil
		}
		result = append(result, &sl)
	}
	if result == nil {
		result = []*models.Slice{}
	}
	return result, rows.Err()
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
