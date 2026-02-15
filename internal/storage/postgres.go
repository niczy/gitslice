package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niczy/gitslice/internal/models"
)

// PostgresStorage persists storage state in PostgreSQL and uses ObjectStore for blob payloads.
//
// The implementation intentionally reuses the in-memory storage semantics by snapshotting
// the full state to Postgres. This keeps behavioral compatibility while making state durable.
type PostgresStorage struct {
	pool        *pgxpool.Pool
	objectStore ObjectStore
	namespace   string
	mu          sync.Mutex
	mem         *InMemoryStorage
	loaded      bool
}

type postgresSnapshot struct {
	LockedSlices        map[string]bool                     `json:"locked_slices"`
	FileLocks           map[string]string                   `json:"file_locks"`
	Slices              map[string]*models.Slice            `json:"slices"`
	SliceMetadata       map[string]*models.SliceMetadata    `json:"slice_metadata"`
	FileIndex           map[string]map[string]bool          `json:"file_index"`
	FileContents        map[string]*models.FileContent      `json:"file_contents"`
	Entries             map[string]*models.DirectoryEntry   `json:"entries"`
	EntriesByPath       map[string]string                   `json:"entries_by_path"`
	EntriesBySlice      map[string][]string                 `json:"entries_by_slice"`
	Changesets          map[string]*models.Changeset        `json:"changesets"`
	SliceChangesets     map[string][]string                 `json:"slice_changesets"`
	SliceCommits        map[string][]*models.Commit         `json:"slice_commits"`
	GlobalState         *models.GlobalState                 `json:"global_state"`
	CommitSnapshots     map[string]*models.CommitSnapshot   `json:"commit_snapshots"`
	VersionedContent    map[string]*models.FileContent      `json:"versioned_content"`
	FileChanges         map[string]*models.FileChangeRecord `json:"file_changes"`
	FileChangesByPath   map[string][]string                 `json:"file_changes_by_path"`
	FileChangesByCommit map[string][]string                 `json:"file_changes_by_commit"`
	FileChangesByDir    map[string][]string                 `json:"file_changes_by_dir"`

	Users      map[string]*models.User                          `json:"users"`
	Orgs       map[string]*models.Organization                  `json:"orgs"`
	OrgMembers map[string]map[string]*models.OrganizationMember `json:"org_members"`
	UserOrgs   map[string]map[string]bool                       `json:"user_orgs"`
}

const createStorageStateTableSQL = `
CREATE TABLE IF NOT EXISTS storage_state (
	namespace TEXT PRIMARY KEY,
	payload TEXT NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

const migrateStorageStateSQL = `
DO $$
BEGIN
	IF EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'storage_state' AND column_name = 'payload' AND data_type = 'jsonb'
	) THEN
		ALTER TABLE storage_state ALTER COLUMN payload TYPE TEXT USING payload::TEXT;
	END IF;
END $$;
`

func ensureCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// NewPostgresStorage constructs a PostgreSQL-backed storage implementation.
func NewPostgresStorage(ctx context.Context, dsn string, objectStore ObjectStore, namespace string) (*PostgresStorage, error) {
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

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, createStorageStateTableSQL); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, migrateStorageStateSQL); err != nil {
		pool.Close()
		return nil, err
	}

	st := &PostgresStorage{
		pool:        pool,
		objectStore: objectStore,
		namespace:   namespace,
		mem:         NewInMemoryStorage(),
	}

	if err := st.loadLocked(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return st, nil
}

// Close closes the backing Postgres pool.
func (s *PostgresStorage) Close() error {
	s.pool.Close()
	return nil
}

// Reset clears the persisted snapshot for this storage namespace and resets in-memory state.
//
// This is intentionally not part of the Storage interface; it's an admin/ops escape hatch.
// Object store blobs are not deleted.
func (s *PostgresStorage) Reset(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx = ensureCtx(ctx)
	_, err := s.pool.Exec(ctx, `DELETE FROM storage_state WHERE namespace=$1`, s.namespace)
	if err != nil {
		return err
	}
	s.mem = NewInMemoryStorage()
	s.loaded = true
	return s.persistLocked(ctx)
}

// BulkWrite applies multiple in-memory mutations and persists a single snapshot.
//
// This is an escape hatch for admin workflows that would otherwise perform
// thousands of tiny writes (each persisting the full snapshot).
func (s *PostgresStorage) BulkWrite(ctx context.Context, fn func(mem *InMemoryStorage) error) error {
	if fn == nil {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ctx = ensureCtx(ctx)
	if err := s.loadLocked(ctx); err != nil {
		return err
	}
	before, err := s.exportSnapshotLocked()
	if err != nil {
		return err
	}

	if err := fn(s.mem); err != nil {
		return err
	}

	// Externalize file content to the object store so the snapshot stays compact.
	for _, fc := range s.mem.fileContents {
		if fc != nil && len(fc.Content) > 0 && fc.FileID != "" {
			raw, jerr := json.Marshal(fc)
			if jerr == nil {
				_ = s.objectStore.PutObject(ctx, s.key("file_content", fc.FileID), raw)
				if fc.Hash != "" {
					_ = s.objectStore.PutObject(ctx, s.key("versioned_content", fc.Hash), raw)
				}
			}
		}
	}

	if err := s.persistLocked(ctx); err != nil {
		s.applySnapshotLocked(before)
		return err
	}
	return nil
}

func (s *PostgresStorage) key(parts ...string) string {
	if s.namespace == "" {
		return strings.Join(parts, ":")
	}
	return fmt.Sprintf("%s:%s", s.namespace, strings.Join(parts, ":"))
}

func (s *PostgresStorage) withRead(ctx context.Context, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx = ensureCtx(ctx)
	if err := s.loadLocked(ctx); err != nil {
		return err
	}
	return fn()
}

func (s *PostgresStorage) withWrite(ctx context.Context, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx = ensureCtx(ctx)
	if err := s.loadLocked(ctx); err != nil {
		return err
	}
	before, err := s.exportSnapshotLocked()
	if err != nil {
		return err
	}

	if err := fn(); err != nil {
		return err
	}

	if err := s.persistLocked(ctx); err != nil {
		s.applySnapshotLocked(before)
		return err
	}

	return nil
}

func (s *PostgresStorage) loadLocked(ctx context.Context) error {
	if s.loaded {
		return nil
	}

	var payload []byte
	err := s.pool.QueryRow(ctx, `SELECT payload FROM storage_state WHERE namespace = $1`, s.namespace).Scan(&payload)
	if err != nil {
		if err == pgx.ErrNoRows {
			if err := s.persistLocked(ctx); err != nil {
				return err
			}
			s.loaded = true
			return nil
		}
		return err
	}

	var snap postgresSnapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return err
	}

	s.applySnapshotLocked(&snap)
	s.loaded = true
	return nil
}

func (s *PostgresStorage) persistLocked(ctx context.Context) error {
	snap, err := s.exportSnapshotLocked()
	if err != nil {
		return err
	}

	payload, err := json.Marshal(snap)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO storage_state (namespace, payload, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (namespace)
		DO UPDATE SET payload = EXCLUDED.payload, updated_at = EXCLUDED.updated_at
	`, s.namespace, payload)
	return err
}

func (s *PostgresStorage) exportSnapshotLocked() (*postgresSnapshot, error) {
	snap := &postgresSnapshot{
		LockedSlices:        s.mem.lockedSlices,
		FileLocks:           s.mem.fileLocks,
		Slices:              s.mem.slices,
		SliceMetadata:       s.mem.sliceMetadata,
		FileIndex:           s.mem.fileIndex,
		FileContents:        s.mem.fileContents,
		Entries:             s.mem.entries,
		EntriesByPath:       s.mem.entriesByPath,
		EntriesBySlice:      s.mem.entriesBySlice,
		Changesets:          s.mem.changesets,
		SliceChangesets:     s.mem.sliceChangesets,
		SliceCommits:        s.mem.sliceCommits,
		GlobalState:         s.mem.globalState,
		CommitSnapshots:     s.mem.commitSnapshots,
		VersionedContent:    s.mem.versionedContent,
		FileChanges:         s.mem.fileChanges,
		FileChangesByPath:   s.mem.fileChangesByPath,
		FileChangesByCommit: s.mem.fileChangesByCommit,
		FileChangesByDir:    s.mem.fileChangesByDir,
		Users:               s.mem.users,
		Orgs:                s.mem.orgs,
		OrgMembers:          s.mem.orgMembers,
		UserOrgs:            s.mem.userOrgs,
	}

	// Round-trip through JSON to produce a deep copy.
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	var copied postgresSnapshot
	if err := json.Unmarshal(raw, &copied); err != nil {
		return nil, err
	}
	normalizeSnapshot(&copied)

	// Strip large binary content from entries and file contents to keep the
	// snapshot compact. File content is stored separately in the object store.
	for k, entry := range copied.Entries {
		if entry != nil && len(entry.Content) > 0 {
			entry.Content = nil
			copied.Entries[k] = entry
		}
	}
	for k, fc := range copied.FileContents {
		if fc != nil && len(fc.Content) > 0 {
			fc.Content = nil
			copied.FileContents[k] = fc
		}
	}
	for k, vc := range copied.VersionedContent {
		if vc != nil && len(vc.Content) > 0 {
			vc.Content = nil
			copied.VersionedContent[k] = vc
		}
	}

	return &copied, nil
}

func (s *PostgresStorage) applySnapshotLocked(snap *postgresSnapshot) {
	normalizeSnapshot(snap)

	s.mem.lockedSlices = snap.LockedSlices
	s.mem.fileLocks = snap.FileLocks
	s.mem.slices = snap.Slices
	s.mem.sliceMetadata = snap.SliceMetadata
	s.mem.fileIndex = snap.FileIndex
	s.mem.fileContents = snap.FileContents
	s.mem.entries = snap.Entries
	s.mem.entriesByPath = snap.EntriesByPath
	s.mem.entriesBySlice = snap.EntriesBySlice
	s.mem.changesets = snap.Changesets
	s.mem.sliceChangesets = snap.SliceChangesets
	s.mem.sliceCommits = snap.SliceCommits
	s.mem.globalState = snap.GlobalState
	s.mem.commitSnapshots = snap.CommitSnapshots
	s.mem.versionedContent = snap.VersionedContent
	s.mem.fileChanges = snap.FileChanges
	s.mem.fileChangesByPath = snap.FileChangesByPath
	s.mem.fileChangesByCommit = snap.FileChangesByCommit
	s.mem.fileChangesByDir = snap.FileChangesByDir
	s.mem.users = snap.Users
	s.mem.orgs = snap.Orgs
	s.mem.orgMembers = snap.OrgMembers
	s.mem.userOrgs = snap.UserOrgs
}

func normalizeSnapshot(snap *postgresSnapshot) {
	if snap.LockedSlices == nil {
		snap.LockedSlices = make(map[string]bool)
	}
	if snap.FileLocks == nil {
		snap.FileLocks = make(map[string]string)
	}
	if snap.Slices == nil {
		snap.Slices = make(map[string]*models.Slice)
	}
	if snap.SliceMetadata == nil {
		snap.SliceMetadata = make(map[string]*models.SliceMetadata)
	}
	if snap.FileIndex == nil {
		snap.FileIndex = make(map[string]map[string]bool)
	}
	if snap.FileContents == nil {
		snap.FileContents = make(map[string]*models.FileContent)
	}
	if snap.Entries == nil {
		snap.Entries = make(map[string]*models.DirectoryEntry)
	}
	if snap.EntriesByPath == nil {
		snap.EntriesByPath = make(map[string]string)
	}
	if snap.EntriesBySlice == nil {
		snap.EntriesBySlice = make(map[string][]string)
	}
	if snap.Changesets == nil {
		snap.Changesets = make(map[string]*models.Changeset)
	}
	if snap.SliceChangesets == nil {
		snap.SliceChangesets = make(map[string][]string)
	}
	if snap.SliceCommits == nil {
		snap.SliceCommits = make(map[string][]*models.Commit)
	}
	if snap.GlobalState == nil {
		snap.GlobalState = &models.GlobalState{
			GlobalCommitHash: "global-init",
			History:          []*models.GlobalCommit{},
		}
	}
	if snap.CommitSnapshots == nil {
		snap.CommitSnapshots = make(map[string]*models.CommitSnapshot)
	}
	if snap.VersionedContent == nil {
		snap.VersionedContent = make(map[string]*models.FileContent)
	}
	if snap.FileChanges == nil {
		snap.FileChanges = make(map[string]*models.FileChangeRecord)
	}
	if snap.FileChangesByPath == nil {
		snap.FileChangesByPath = make(map[string][]string)
	}
	if snap.FileChangesByCommit == nil {
		snap.FileChangesByCommit = make(map[string][]string)
	}
	if snap.FileChangesByDir == nil {
		snap.FileChangesByDir = make(map[string][]string)
	}
	if snap.Users == nil {
		snap.Users = make(map[string]*models.User)
	}
	if snap.Orgs == nil {
		snap.Orgs = make(map[string]*models.Organization)
	}
	if snap.OrgMembers == nil {
		snap.OrgMembers = make(map[string]map[string]*models.OrganizationMember)
	}
	if snap.UserOrgs == nil {
		snap.UserOrgs = make(map[string]map[string]bool)
	}
}

func (s *PostgresStorage) LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error {
	return s.withWrite(ctx, func() error {
		return s.mem.LockSliceAndFiles(ctx, sliceID, fileIDs)
	})
}

func (s *PostgresStorage) UnlockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) {
	_ = s.withWrite(ctx, func() error {
		s.mem.UnlockSliceAndFiles(ctx, sliceID, fileIDs)
		return nil
	})
}

func (s *PostgresStorage) CreateSlice(ctx context.Context, slice *models.Slice) error {
	return s.withWrite(ctx, func() error {
		return s.mem.CreateSlice(ctx, slice)
	})
}

func (s *PostgresStorage) GetSlice(ctx context.Context, sliceID string) (*models.Slice, error) {
	var out *models.Slice
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetSlice(ctx, sliceID)
		return err
	})
	return out, err
}

func (s *PostgresStorage) ListSlices(ctx context.Context, limit, offset int) ([]*models.Slice, error) {
	var out []*models.Slice
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListSlices(ctx, limit, offset)
		return err
	})
	return out, err
}

func (s *PostgresStorage) CountSlices(ctx context.Context) (int, error) {
	var out int
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.CountSlices(ctx)
		return err
	})
	return out, err
}

func (s *PostgresStorage) ListSlicesByOwner(ctx context.Context, owner string, limit, offset int) ([]*models.Slice, error) {
	var out []*models.Slice
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListSlicesByOwner(ctx, owner, limit, offset)
		return err
	})
	return out, err
}

func (s *PostgresStorage) SearchSlices(ctx context.Context, query string, limit, offset int) ([]*models.Slice, error) {
	var out []*models.Slice
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.SearchSlices(ctx, query, limit, offset)
		return err
	})
	return out, err
}

func (s *PostgresStorage) EnsureUser(ctx context.Context, username string) (*models.User, error) {
	// Fast path: if the user already exists, avoid persisting a full snapshot.
	var existing *models.User
	readErr := s.withRead(ctx, func() error {
		u, err := s.mem.GetUser(ctx, username)
		if err != nil {
			return err
		}
		existing = u
		return nil
	})
	if readErr == nil && existing != nil {
		return existing, nil
	}
	if readErr != nil && !errors.Is(readErr, ErrEntryNotFound) && !errors.Is(readErr, ErrInvalidInput) {
		return nil, readErr
	}

	var out *models.User
	err := s.withWrite(ctx, func() error {
		var err error
		out, err = s.mem.EnsureUser(ctx, username)
		return err
	})
	return out, err
}

func (s *PostgresStorage) GetUser(ctx context.Context, username string) (*models.User, error) {
	var out *models.User
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetUser(ctx, username)
		return err
	})
	return out, err
}

func (s *PostgresStorage) CreateOrganization(ctx context.Context, org *models.Organization) error {
	return s.withWrite(ctx, func() error {
		return s.mem.CreateOrganization(ctx, org)
	})
}

func (s *PostgresStorage) GetOrganization(ctx context.Context, orgSlug string) (*models.Organization, error) {
	var out *models.Organization
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetOrganization(ctx, orgSlug)
		return err
	})
	return out, err
}

func (s *PostgresStorage) AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	return s.withWrite(ctx, func() error {
		return s.mem.AddOrganizationMember(ctx, member)
	})
}

func (s *PostgresStorage) ListOrganizationsForUser(ctx context.Context, username string) ([]*models.Organization, error) {
	var out []*models.Organization
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListOrganizationsForUser(ctx, username)
		return err
	})
	return out, err
}

func (s *PostgresStorage) GetSliceMetadata(ctx context.Context, sliceID string) (*models.SliceMetadata, error) {
	var out *models.SliceMetadata
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetSliceMetadata(ctx, sliceID)
		return err
	})
	return out, err
}

func (s *PostgresStorage) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	return s.withWrite(ctx, func() error {
		return s.mem.UpdateSliceMetadata(ctx, sliceID, metadata)
	})
}

func (s *PostgresStorage) SetSliceFiles(ctx context.Context, sliceID string, files []string) error {
	return s.withWrite(ctx, func() error {
		return s.mem.SetSliceFiles(ctx, sliceID, files)
	})
}

func (s *PostgresStorage) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	var out *models.Slice
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetRootSlice(ctx)
		return err
	})
	return out, err
}

func (s *PostgresStorage) InitializeRootSlice(ctx context.Context) error {
	return s.withWrite(ctx, func() error {
		return s.mem.InitializeRootSlice(ctx)
	})
}

func (s *PostgresStorage) AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error {
	return s.withWrite(ctx, func() error {
		return s.mem.AddSliceCommit(ctx, sliceID, commit)
	})
}

func (s *PostgresStorage) ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error) {
	var out []*models.Commit
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListSliceCommits(ctx, sliceID, limit, fromCommitHash)
		return err
	})
	return out, err
}

func (s *PostgresStorage) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	return s.withWrite(ctx, func() error {
		return s.mem.AddFileToSlice(ctx, fileID, sliceID)
	})
}

func (s *PostgresStorage) GetActiveSlicesForFile(ctx context.Context, fileID string) ([]string, error) {
	var out []string
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetActiveSlicesForFile(ctx, fileID)
		return err
	})
	return out, err
}

func (s *PostgresStorage) RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error {
	return s.withWrite(ctx, func() error {
		return s.mem.RemoveFileFromSlice(ctx, fileID, sliceID)
	})
}

func (s *PostgresStorage) ListConflicts(ctx context.Context) ([]*models.FileConflict, error) {
	var out []*models.FileConflict
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListConflicts(ctx)
		return err
	})
	return out, err
}

func (s *PostgresStorage) ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error) {
	var out *models.FileConflict
	err := s.withWrite(ctx, func() error {
		var err error
		out, err = s.mem.ResolveConflict(ctx, fileID, preferredSliceID)
		return err
	})
	return out, err
}

func (s *PostgresStorage) RebuildIndexes(ctx context.Context) error {
	return s.withRead(ctx, func() error {
		return nil
	})
}

func (s *PostgresStorage) CreateChangeset(ctx context.Context, changeset *models.Changeset) error {
	return s.withWrite(ctx, func() error {
		return s.mem.CreateChangeset(ctx, changeset)
	})
}

func (s *PostgresStorage) GetChangeset(ctx context.Context, changesetID string) (*models.Changeset, error) {
	var out *models.Changeset
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetChangeset(ctx, changesetID)
		return err
	})
	return out, err
}

func (s *PostgresStorage) ListChangesets(ctx context.Context, sliceID string, status *models.ChangesetStatus, limit int) ([]*models.Changeset, error) {
	var out []*models.Changeset
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListChangesets(ctx, sliceID, status, limit)
		return err
	})
	return out, err
}

func (s *PostgresStorage) UpdateChangeset(ctx context.Context, changeset *models.Changeset) error {
	return s.withWrite(ctx, func() error {
		return s.mem.UpdateChangeset(ctx, changeset)
	})
}

func (s *PostgresStorage) GetSliceFiles(ctx context.Context, sliceID string) ([]*models.FileContent, error) {
	var out []*models.FileContent
	err := s.withRead(ctx, func() error {
		files, err := s.mem.GetSliceFiles(ctx, sliceID)
		if err != nil {
			return err
		}

		hydrated := make([]*models.FileContent, 0, len(files))
		for _, file := range files {
			if file == nil {
				continue
			}
			contentCopy := *file
			raw, err := s.objectStore.GetObject(ctx, s.key("file_content", file.FileID))
			if err == nil {
				var stored models.FileContent
				if err := json.Unmarshal(raw, &stored); err == nil {
					contentCopy = stored
				}
			}
			hydrated = append(hydrated, &contentCopy)
		}
		out = hydrated
		return nil
	})
	return out, err
}

func (s *PostgresStorage) GetSliceFileByPath(ctx context.Context, sliceID, path string) (*models.FileContent, error) {
	var out *models.FileContent
	err := s.withRead(ctx, func() error {
		entry, err := s.mem.GetEntryByPath(ctx, sliceID, path)
		if err != nil {
			return err
		}

		// Postgres snapshots intentionally strip large content bytes from in-memory
		// entries. Rehydrate from object storage when possible.
		candidateIDs := []string{entry.Path}
		if entry.ID != "" && entry.ID != entry.Path {
			candidateIDs = append(candidateIDs, entry.ID)
		}
		for _, fileID := range candidateIDs {
			if fileID == "" {
				continue
			}
			raw, err := s.objectStore.GetObject(ctx, s.key("file_content", fileID))
			if err != nil {
				continue
			}
			var hydrated models.FileContent
			if err := json.Unmarshal(raw, &hydrated); err != nil {
				continue
			}
			if hydrated.FileID == "" {
				hydrated.FileID = fileID
			}
			if hydrated.Path == "" {
				hydrated.Path = entry.Path
			}
			if hydrated.Size == 0 {
				hydrated.Size = entry.Size
			}
			out = &hydrated
			return nil
		}

		// Fall back to in-memory metadata when blob hydration fails.
		file := &models.FileContent{
			FileID:  entry.Path,
			Path:    entry.Path,
			Content: append([]byte(nil), entry.Content...),
			Size:    entry.Size,
		}
		if fc, ok := s.mem.fileContents[entry.Path]; ok && fc != nil {
			file.Hash = fc.Hash
			if file.Size == 0 {
				file.Size = fc.Size
			}
			if len(file.Content) == 0 {
				file.Content = append([]byte(nil), fc.Content...)
			}
		}
		if file.Hash == "" {
			if fc, ok := s.mem.fileContents[entry.ID]; ok && fc != nil {
				file.Hash = fc.Hash
				if file.Size == 0 {
					file.Size = fc.Size
				}
				if len(file.Content) == 0 {
					file.Content = append([]byte(nil), fc.Content...)
				}
			}
		}
		if file.FileID == "" {
			file.FileID = entry.ID
		}
		out = file
		return nil
	})
	return out, err
}

func (s *PostgresStorage) AddFileContent(ctx context.Context, content *models.FileContent) error {
	ctx = ensureCtx(ctx)
	if content == nil || content.FileID == "" {
		return ErrInvalidInput
	}

	return s.withWrite(ctx, func() error {
		raw, err := json.Marshal(content)
		if err != nil {
			return err
		}
		if err := s.objectStore.PutObject(ctx, s.key("file_content", content.FileID), raw); err != nil {
			return err
		}
		if content.Hash != "" {
			if err := s.objectStore.PutObject(ctx, s.key("versioned_content", content.Hash), raw); err != nil {
				return err
			}
		}

		// Keep in-memory snapshot lightweight: blobs live in the object store.
		thin := *content
		thin.Content = nil
		return s.mem.AddFileContent(ctx, &thin)
	})
}

func (s *PostgresStorage) AddEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	return s.withWrite(ctx, func() error {
		return s.mem.AddEntry(ctx, entry)
	})
}

func (s *PostgresStorage) GetEntry(ctx context.Context, entryID string) (*models.DirectoryEntry, error) {
	var out *models.DirectoryEntry
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetEntry(ctx, entryID)
		return err
	})
	return out, err
}

func (s *PostgresStorage) GetEntryByPath(ctx context.Context, sliceID, path string) (*models.DirectoryEntry, error) {
	var out *models.DirectoryEntry
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetEntryByPath(ctx, sliceID, path)
		return err
	})
	return out, err
}

func (s *PostgresStorage) ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error) {
	var out []*models.DirectoryEntry
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListEntries(ctx, sliceID, parentID)
		return err
	})
	return out, err
}

func (s *PostgresStorage) UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	return s.withWrite(ctx, func() error {
		return s.mem.UpdateEntry(ctx, entry)
	})
}

func (s *PostgresStorage) DeleteEntry(ctx context.Context, entryID string) error {
	return s.withWrite(ctx, func() error {
		return s.mem.DeleteEntry(ctx, entryID)
	})
}

func (s *PostgresStorage) GetGlobalState(ctx context.Context) (*models.GlobalState, error) {
	var out *models.GlobalState
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetGlobalState(ctx)
		return err
	})
	return out, err
}

func (s *PostgresStorage) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	return s.withWrite(ctx, func() error {
		return s.mem.UpdateGlobalState(ctx, state)
	})
}

func (s *PostgresStorage) Ping(ctx context.Context) error {
	ctx = ensureCtx(ctx)
	if err := s.pool.Ping(ctx); err != nil {
		return err
	}

	const probeKey = "healthcheck"
	key := s.key(probeKey)
	if err := s.objectStore.PutObject(ctx, key, []byte("ok")); err != nil {
		return err
	}
	_, err := s.objectStore.GetObject(ctx, key)
	_ = s.objectStore.DeleteObject(ctx, key)
	return err
}

func (s *PostgresStorage) GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error) {
	var out *models.CommitSnapshot
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetCommitSnapshot(ctx, commitHash)
		return err
	})
	return out, err
}

func (s *PostgresStorage) SaveCommitSnapshot(ctx context.Context, snapshot *models.CommitSnapshot) error {
	return s.withWrite(ctx, func() error {
		return s.mem.SaveCommitSnapshot(ctx, snapshot)
	})
}

func (s *PostgresStorage) GetFileAtCommit(ctx context.Context, commitHash, path string) (*models.FileContent, error) {
	var out *models.FileContent
	err := s.withRead(ctx, func() error {
		snapshot, err := s.mem.GetCommitSnapshot(ctx, commitHash)
		if err != nil {
			return err
		}

		contentHash, exists := snapshot.Files[path]
		if !exists {
			return ErrEntryNotFound
		}

		raw, err := s.objectStore.GetObject(ctx, s.key("versioned_content", contentHash))
		if err == nil {
			var content models.FileContent
			if err := json.Unmarshal(raw, &content); err != nil {
				return err
			}
			out = &content
			return nil
		}

		out, err = s.mem.GetFileAtCommit(ctx, commitHash, path)
		return err
	})
	return out, err
}

func (s *PostgresStorage) ListFilesAtCommit(ctx context.Context, commitHash, pathPrefix string) ([]string, error) {
	var out []string
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.ListFilesAtCommit(ctx, commitHash, pathPrefix)
		if err != nil {
			return err
		}
		sort.Strings(out)
		return nil
	})
	return out, err
}

func (s *PostgresStorage) AddFileChange(ctx context.Context, change *models.FileChangeRecord) error {
	return s.withWrite(ctx, func() error {
		return s.mem.AddFileChange(ctx, change)
	})
}

func (s *PostgresStorage) AddFileChanges(ctx context.Context, changes []*models.FileChangeRecord) error {
	return s.withWrite(ctx, func() error {
		return s.mem.AddFileChanges(ctx, changes)
	})
}

func (s *PostgresStorage) GetFileHistory(ctx context.Context, sliceID, path string, limit int, fromCommit string) ([]*models.FileChangeRecord, error) {
	var out []*models.FileChangeRecord
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetFileHistory(ctx, sliceID, path, limit, fromCommit)
		return err
	})
	return out, err
}

func (s *PostgresStorage) GetDirectoryHistory(ctx context.Context, sliceID, pathPrefix string, limit int, fromCommit string) ([]*models.FileChangeRecord, error) {
	var out []*models.FileChangeRecord
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetDirectoryHistory(ctx, sliceID, pathPrefix, limit, fromCommit)
		return err
	})
	return out, err
}

func (s *PostgresStorage) GetCommitChanges(ctx context.Context, commitHash string) ([]*models.FileChangeRecord, error) {
	var out []*models.FileChangeRecord
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetCommitChanges(ctx, commitHash)
		return err
	})
	return out, err
}

func (s *PostgresStorage) QueryFileHistory(ctx context.Context, query *models.FileHistoryQuery) (*models.FileHistoryResult, error) {
	var out *models.FileHistoryResult
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.QueryFileHistory(ctx, query)
		return err
	})
	return out, err
}

func (s *PostgresStorage) GetDirectorySummary(ctx context.Context, sliceID, pathPrefix string) (*models.DirectoryChangeSummary, error) {
	var out *models.DirectoryChangeSummary
	err := s.withRead(ctx, func() error {
		var err error
		out, err = s.mem.GetDirectorySummary(ctx, sliceID, pathPrefix)
		return err
	})
	return out, err
}
