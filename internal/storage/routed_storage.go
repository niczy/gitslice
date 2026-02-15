package storage

import (
	"context"
	"fmt"

	"github.com/niczy/gitslice/internal/models"
)

// NativeReadStorage routes selected read-heavy APIs to a native storage backend
// while keeping writes on the embedded snapshot backend (the embedded Storage).
//
// This is an incremental migration tool: if native tables are incomplete, reads
// fall back to the snapshot backend to preserve correctness.
type NativeReadStorage struct {
	Storage // snapshot (source of truth for writes in this mode)
	native  Storage
}

func NewNativeReadStorage(snapshot Storage, native Storage) *NativeReadStorage {
	if snapshot == nil || native == nil {
		return nil
	}
	return &NativeReadStorage{Storage: snapshot, native: native}
}

func (s *NativeReadStorage) GetEntryByPath(ctx context.Context, sliceID, path string) (*models.DirectoryEntry, error) {
	if s == nil || s.native == nil {
		return s.Storage.GetEntryByPath(ctx, sliceID, path)
	}
	e, err := s.native.GetEntryByPath(ctx, sliceID, path)
	if err == nil && e != nil {
		return e, nil
	}
	if err != nil && err != ErrEntryNotFound {
		// If native errors, fall back to snapshot to keep the system usable.
		if fallback, ferr := s.Storage.GetEntryByPath(ctx, sliceID, path); ferr == nil {
			return fallback, nil
		}
	}
	return s.Storage.GetEntryByPath(ctx, sliceID, path)
}

func (s *NativeReadStorage) ListEntries(ctx context.Context, sliceID, parentID string) ([]*models.DirectoryEntry, error) {
	if s == nil || s.native == nil {
		return s.Storage.ListEntries(ctx, sliceID, parentID)
	}
	entries, err := s.native.ListEntries(ctx, sliceID, parentID)
	if err == nil && len(entries) > 0 {
		return entries, nil
	}
	if err != nil && err != ErrEntryNotFound {
		if fallback, ferr := s.Storage.ListEntries(ctx, sliceID, parentID); ferr == nil {
			return fallback, nil
		}
	}
	return s.Storage.ListEntries(ctx, sliceID, parentID)
}

func (s *NativeReadStorage) GetSliceFileByPath(ctx context.Context, sliceID, path string) (*models.FileContent, error) {
	if s == nil || s.native == nil {
		return s.Storage.GetSliceFileByPath(ctx, sliceID, path)
	}
	fc, err := s.native.GetSliceFileByPath(ctx, sliceID, path)
	if err == nil && fc != nil {
		return fc, nil
	}
	if err != nil && err != ErrEntryNotFound {
		if fallback, ferr := s.Storage.GetSliceFileByPath(ctx, sliceID, path); ferr == nil {
			return fallback, nil
		}
	}
	return s.Storage.GetSliceFileByPath(ctx, sliceID, path)
}

func (s *NativeReadStorage) GetFileAtCommit(ctx context.Context, commitHash, path string) (*models.FileContent, error) {
	if s == nil || s.native == nil {
		return s.Storage.GetFileAtCommit(ctx, commitHash, path)
	}
	fc, err := s.native.GetFileAtCommit(ctx, commitHash, path)
	if err == nil && fc != nil {
		return fc, nil
	}
	if err != nil && err != ErrCommitNotFound && err != ErrEntryNotFound {
		if fallback, ferr := s.Storage.GetFileAtCommit(ctx, commitHash, path); ferr == nil {
			return fallback, nil
		}
	}
	return s.Storage.GetFileAtCommit(ctx, commitHash, path)
}

func (s *NativeReadStorage) GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error) {
	if s == nil || s.native == nil {
		return s.Storage.GetCommitSnapshot(ctx, commitHash)
	}
	snap, err := s.native.GetCommitSnapshot(ctx, commitHash)
	if err == nil && snap != nil {
		return snap, nil
	}
	if err != nil && err != ErrCommitNotFound {
		if fallback, ferr := s.Storage.GetCommitSnapshot(ctx, commitHash); ferr == nil {
			return fallback, nil
		}
	}
	return s.Storage.GetCommitSnapshot(ctx, commitHash)
}

func (s *NativeReadStorage) ListFilesAtCommit(ctx context.Context, commitHash, pathPrefix string) ([]string, error) {
	if s == nil || s.native == nil {
		return s.Storage.ListFilesAtCommit(ctx, commitHash, pathPrefix)
	}
	files, err := s.native.ListFilesAtCommit(ctx, commitHash, pathPrefix)
	if err == nil && len(files) > 0 {
		return files, nil
	}
	if err != nil && err != ErrCommitNotFound {
		if fallback, ferr := s.Storage.ListFilesAtCommit(ctx, commitHash, pathPrefix); ferr == nil {
			return fallback, nil
		}
	}
	return s.Storage.ListFilesAtCommit(ctx, commitHash, pathPrefix)
}

// DualWriteStorage routes reads to the embedded storage (typically snapshot) and
// mirrors selected mutations to a secondary backend (typically native).
//
// This is intentionally strict: if the secondary write fails, the method returns
// an error. There is no cross-backend transaction, so callers should treat this
// mode as a migration tool and prefer idempotent writes.
type DualWriteStorage struct {
	Storage   // primary (reads, and primary writes)
	secondary Storage
}

func NewDualWriteStorage(primary Storage, secondary Storage) *DualWriteStorage {
	if primary == nil || secondary == nil {
		return nil
	}
	return &DualWriteStorage{Storage: primary, secondary: secondary}
}

func (s *DualWriteStorage) CreateSlice(ctx context.Context, slice *models.Slice) error {
	if err := s.Storage.CreateSlice(ctx, slice); err != nil {
		return err
	}
	if err := s.secondary.CreateSlice(ctx, slice); err != nil {
		return fmt.Errorf("dual_write secondary CreateSlice: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	if err := s.Storage.UpdateSliceMetadata(ctx, sliceID, metadata); err != nil {
		return err
	}
	if err := s.secondary.UpdateSliceMetadata(ctx, sliceID, metadata); err != nil {
		return fmt.Errorf("dual_write secondary UpdateSliceMetadata: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) SetSliceFiles(ctx context.Context, sliceID string, files []string) error {
	if err := s.Storage.SetSliceFiles(ctx, sliceID, files); err != nil {
		return err
	}
	if err := s.secondary.SetSliceFiles(ctx, sliceID, files); err != nil {
		return fmt.Errorf("dual_write secondary SetSliceFiles: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) InitializeRootSlice(ctx context.Context) error {
	if err := s.Storage.InitializeRootSlice(ctx); err != nil {
		return err
	}
	if err := s.secondary.InitializeRootSlice(ctx); err != nil {
		return fmt.Errorf("dual_write secondary InitializeRootSlice: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) AddSliceCommit(ctx context.Context, sliceID string, commit *models.Commit) error {
	if err := s.Storage.AddSliceCommit(ctx, sliceID, commit); err != nil {
		return err
	}
	if err := s.secondary.AddSliceCommit(ctx, sliceID, commit); err != nil {
		return fmt.Errorf("dual_write secondary AddSliceCommit: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	if err := s.Storage.AddFileToSlice(ctx, fileID, sliceID); err != nil {
		return err
	}
	if err := s.secondary.AddFileToSlice(ctx, fileID, sliceID); err != nil {
		return fmt.Errorf("dual_write secondary AddFileToSlice: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) RemoveFileFromSlice(ctx context.Context, fileID, sliceID string) error {
	if err := s.Storage.RemoveFileFromSlice(ctx, fileID, sliceID); err != nil {
		return err
	}
	if err := s.secondary.RemoveFileFromSlice(ctx, fileID, sliceID); err != nil {
		return fmt.Errorf("dual_write secondary RemoveFileFromSlice: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) ResolveConflict(ctx context.Context, fileID, preferredSliceID string) (*models.FileConflict, error) {
	conflict, err := s.Storage.ResolveConflict(ctx, fileID, preferredSliceID)
	if err != nil {
		return nil, err
	}
	if _, err := s.secondary.ResolveConflict(ctx, fileID, preferredSliceID); err != nil {
		return nil, fmt.Errorf("dual_write secondary ResolveConflict: %w", err)
	}
	return conflict, nil
}

func (s *DualWriteStorage) LockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) error {
	if err := s.Storage.LockSliceAndFiles(ctx, sliceID, fileIDs); err != nil {
		return err
	}
	if err := s.secondary.LockSliceAndFiles(ctx, sliceID, fileIDs); err != nil {
		return fmt.Errorf("dual_write secondary LockSliceAndFiles: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) UnlockSliceAndFiles(ctx context.Context, sliceID string, fileIDs []string) {
	s.Storage.UnlockSliceAndFiles(ctx, sliceID, fileIDs)
	s.secondary.UnlockSliceAndFiles(ctx, sliceID, fileIDs)
}

func (s *DualWriteStorage) RebuildIndexes(ctx context.Context) error {
	if err := s.Storage.RebuildIndexes(ctx); err != nil {
		return err
	}
	if err := s.secondary.RebuildIndexes(ctx); err != nil {
		return fmt.Errorf("dual_write secondary RebuildIndexes: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) CreateChangeset(ctx context.Context, changeset *models.Changeset) error {
	if err := s.Storage.CreateChangeset(ctx, changeset); err != nil {
		return err
	}
	if err := s.secondary.CreateChangeset(ctx, changeset); err != nil {
		return fmt.Errorf("dual_write secondary CreateChangeset: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) UpdateChangeset(ctx context.Context, changeset *models.Changeset) error {
	if err := s.Storage.UpdateChangeset(ctx, changeset); err != nil {
		return err
	}
	if err := s.secondary.UpdateChangeset(ctx, changeset); err != nil {
		return fmt.Errorf("dual_write secondary UpdateChangeset: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) AddFileContent(ctx context.Context, content *models.FileContent) error {
	if err := s.Storage.AddFileContent(ctx, content); err != nil {
		return err
	}
	if err := s.secondary.AddFileContent(ctx, content); err != nil {
		return fmt.Errorf("dual_write secondary AddFileContent: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) AddEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	if err := s.Storage.AddEntry(ctx, entry); err != nil {
		return err
	}
	if err := s.secondary.AddEntry(ctx, entry); err != nil {
		return fmt.Errorf("dual_write secondary AddEntry: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) UpdateEntry(ctx context.Context, entry *models.DirectoryEntry) error {
	if err := s.Storage.UpdateEntry(ctx, entry); err != nil {
		return err
	}
	if err := s.secondary.UpdateEntry(ctx, entry); err != nil {
		return fmt.Errorf("dual_write secondary UpdateEntry: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) DeleteEntry(ctx context.Context, entryID string) error {
	if err := s.Storage.DeleteEntry(ctx, entryID); err != nil {
		return err
	}
	if err := s.secondary.DeleteEntry(ctx, entryID); err != nil {
		return fmt.Errorf("dual_write secondary DeleteEntry: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	if err := s.Storage.UpdateGlobalState(ctx, state); err != nil {
		return err
	}
	if err := s.secondary.UpdateGlobalState(ctx, state); err != nil {
		return fmt.Errorf("dual_write secondary UpdateGlobalState: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) SaveCommitSnapshot(ctx context.Context, snapshot *models.CommitSnapshot) error {
	if err := s.Storage.SaveCommitSnapshot(ctx, snapshot); err != nil {
		return err
	}
	if err := s.secondary.SaveCommitSnapshot(ctx, snapshot); err != nil {
		return fmt.Errorf("dual_write secondary SaveCommitSnapshot: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) AddFileChange(ctx context.Context, change *models.FileChangeRecord) error {
	if err := s.Storage.AddFileChange(ctx, change); err != nil {
		return err
	}
	if err := s.secondary.AddFileChange(ctx, change); err != nil {
		return fmt.Errorf("dual_write secondary AddFileChange: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) AddFileChanges(ctx context.Context, changes []*models.FileChangeRecord) error {
	if err := s.Storage.AddFileChanges(ctx, changes); err != nil {
		return err
	}
	if err := s.secondary.AddFileChanges(ctx, changes); err != nil {
		return fmt.Errorf("dual_write secondary AddFileChanges: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) EnsureUser(ctx context.Context, username string) (*models.User, error) {
	user, err := s.Storage.EnsureUser(ctx, username)
	if err != nil {
		return nil, err
	}
	if _, err := s.secondary.EnsureUser(ctx, username); err != nil {
		return nil, fmt.Errorf("dual_write secondary EnsureUser: %w", err)
	}
	return user, nil
}

func (s *DualWriteStorage) CreateOrganization(ctx context.Context, org *models.Organization) error {
	if err := s.Storage.CreateOrganization(ctx, org); err != nil {
		return err
	}
	if err := s.secondary.CreateOrganization(ctx, org); err != nil {
		return fmt.Errorf("dual_write secondary CreateOrganization: %w", err)
	}
	return nil
}

func (s *DualWriteStorage) AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	if err := s.Storage.AddOrganizationMember(ctx, member); err != nil {
		return err
	}
	if err := s.secondary.AddOrganizationMember(ctx, member); err != nil {
		return fmt.Errorf("dual_write secondary AddOrganizationMember: %w", err)
	}
	return nil
}
