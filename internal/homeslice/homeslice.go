package homeslice

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const idPrefix = "home."

type BackfillResult struct {
	Username          string
	HomeSliceID       string
	Created           bool
	Seeded            bool
	FilesCopied       int
	DirectoriesCopied int
}

// IDForUsername returns the deterministic home-slice ID for a user.
func IDForUsername(username string) string {
	return idPrefix + strings.TrimSpace(username)
}

// VisibleRootPath returns the absolute user-visible home path for a user.
func VisibleRootPath(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	return "/" + username
}

// RelativeRootPath returns the stored relative path used for a user's top-level directory.
func RelativeRootPath(username string) string {
	return strings.TrimPrefix(VisibleRootPath(username), "/")
}

// EnsureUserHomeSlice provisions the user's deterministic home slice and reserves the
// user's top-level directory in both the home slice and the root slice.
func EnsureUserHomeSlice(ctx context.Context, st storage.Storage, username string) (*models.Slice, error) {
	if st == nil {
		return nil, fmt.Errorf("storage is nil")
	}

	user, err := st.EnsureUser(ctx, strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		return nil, err
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		return nil, err
	}

	homeSlice, _, err := ensureHomeSliceRecord(ctx, st, user)
	if err != nil {
		return nil, err
	}
	if err := ensureInitialHeadArtifacts(ctx, st, homeSlice, "create home slice"); err != nil {
		return nil, err
	}

	rootPath := strings.TrimPrefix(strings.TrimSpace(user.RootPath), "/")
	if rootPath == "" {
		rootPath = RelativeRootPath(user.Username)
	}
	if _, err := ensureDirectory(ctx, st, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if _, err := ensureDirectory(ctx, st, rootSlice.ID, rootPath); err != nil {
		return nil, err
	}
	return homeSlice, nil
}

func BackfillUserHomeSlice(ctx context.Context, st storage.Storage, username string) (*BackfillResult, error) {
	if st == nil {
		return nil, fmt.Errorf("storage is nil")
	}

	user, err := st.GetUser(ctx, strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		return nil, err
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		return nil, err
	}

	homeSlice, created, err := ensureHomeSliceRecord(ctx, st, user)
	if err != nil {
		return nil, err
	}
	if err := ensureInitialHeadArtifacts(ctx, st, homeSlice, "create home slice"); err != nil {
		return nil, err
	}

	rootPath := strings.TrimPrefix(strings.TrimSpace(user.RootPath), "/")
	if rootPath == "" {
		rootPath = RelativeRootPath(user.Username)
	}
	if _, err := ensureDirectory(ctx, st, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if _, err := ensureDirectory(ctx, st, rootSlice.ID, rootPath); err != nil {
		return nil, err
	}

	filesCopied, directoriesCopied, err := copyRootSubtreeToHomeSlice(ctx, st, rootSlice.ID, homeSlice.ID, rootPath)
	if err != nil {
		return nil, err
	}
	if filesCopied > 0 || directoriesCopied > 0 {
		if err := recordSliceStateCommit(ctx, st, homeSlice, "backfill home slice from root"); err != nil {
			return nil, err
		}
	}

	return &BackfillResult{
		Username:          user.Username,
		HomeSliceID:       homeSlice.ID,
		Created:           created,
		Seeded:            filesCopied > 0 || directoriesCopied > 0,
		FilesCopied:       filesCopied,
		DirectoriesCopied: directoriesCopied,
	}, nil
}

func ensureHomeSliceRecord(ctx context.Context, st storage.Storage, user *models.User) (*models.Slice, bool, error) {
	if user == nil {
		return nil, false, fmt.Errorf("user is nil")
	}

	sliceID := IDForUsername(user.Username)
	slice, err := st.GetSlice(ctx, sliceID)
	if err == nil {
		return slice, false, nil
	}
	if err != storage.ErrSliceNotFound {
		return nil, false, err
	}

	now := time.Now()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          sliceID,
		Name:        user.Username,
		Description: fmt.Sprintf("Home slice for %s", user.Username),
		Visibility:  models.VisibilityPrivate,
		Files:       []string{},
		Owners:      []string{user.Username},
		CreatedBy:   user.Username,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil && err != storage.ErrSliceAlreadyExists {
		return nil, false, err
	}
	slice, err = st.GetSlice(ctx, sliceID)
	return slice, true, err
}

func ensureInitialHeadArtifacts(ctx context.Context, st storage.Storage, slice *models.Slice, message string) error {
	if slice == nil {
		return fmt.Errorf("slice is nil")
	}

	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		return err
	}

	expectedHead := "init-" + slice.ID
	if strings.TrimSpace(meta.HeadCommitHash) != expectedHead {
		return nil
	}

	createdAt := slice.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	if _, err := st.GetCommitByHash(ctx, slice.ID, expectedHead); err != nil {
		if err != storage.ErrCommitNotFound {
			return err
		}
		if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
			CommitHash: expectedHead,
			ParentHash: "",
			Timestamp:  createdAt,
			Message:    strings.TrimSpace(message),
		}); err != nil && !isDuplicateWrite(err) {
			return err
		}
	}

	if _, err := st.GetCommitSnapshot(ctx, expectedHead); err != nil {
		if err != storage.ErrCommitNotFound && err != storage.ErrEntryNotFound {
			return err
		}
		if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
			CommitHash: expectedHead,
			SliceID:    slice.ID,
			Files:      map[string]string{},
			Timestamp:  createdAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func ensureDirectory(ctx context.Context, st storage.Storage, sliceID, dirPath string) (bool, error) {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" {
		return false, nil
	}

	entry, err := st.GetEntryByPath(ctx, sliceID, dirPath)
	if err == nil {
		if entry.Type != "directory" {
			return false, fmt.Errorf("path %q already exists as %s in slice %s", dirPath, entry.Type, sliceID)
		}
		return false, nil
	}
	if err != storage.ErrEntryNotFound {
		return false, err
	}

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, dirPath),
		Path:     dirPath,
		Type:     "directory",
		ParentID: sliceID,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func copyRootSubtreeToHomeSlice(ctx context.Context, st storage.Storage, rootSliceID, homeSliceID, rootPath string) (int, int, error) {
	entries, err := collectSubtreeEntries(ctx, st, rootSliceID, rootPath)
	if err != nil {
		return 0, 0, err
	}

	filesCopied := 0
	directoriesCopied := 0
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		switch entry.Type {
		case "directory":
			created, err := ensureDirectory(ctx, st, homeSliceID, entry.Path)
			if err != nil {
				return 0, 0, err
			}
			if created {
				directoriesCopied++
			}
		default:
			copied, err := copyFile(ctx, st, rootSliceID, homeSliceID, entry.Path)
			if err != nil {
				return 0, 0, err
			}
			if copied {
				filesCopied++
			}
		}
	}
	return filesCopied, directoriesCopied, nil
}

func collectSubtreeEntries(ctx context.Context, st storage.Storage, sliceID, rootPath string) ([]*models.DirectoryEntry, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, nil
	}

	rootEntry, err := st.GetEntryByPath(ctx, sliceID, rootPath)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, nil
		}
		return nil, err
	}

	entries := []*models.DirectoryEntry{rootEntry}
	queue := []string{rootEntry.ID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		children, err := st.ListEntries(ctx, sliceID, parentID)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child == nil {
				continue
			}
			entries = append(entries, child)
			if child.Type == "directory" {
				queue = append(queue, child.ID)
			}
		}
	}
	return entries, nil
}

func copyFile(ctx context.Context, st storage.Storage, sourceSliceID, targetSliceID, filePath string) (bool, error) {
	if entry, err := st.GetEntryByPath(ctx, targetSliceID, filePath); err == nil {
		if entry.Type != "file" {
			return false, fmt.Errorf("path %q already exists as %s in slice %s", filePath, entry.Type, targetSliceID)
		}
		if _, err := storage.ReadSliceFileContent(ctx, st, targetSliceID, filePath); err == nil {
			return false, nil
		} else if err != storage.ErrEntryNotFound {
			return false, err
		}
	} else if err != storage.ErrEntryNotFound {
		return false, err
	}

	sourceFile, err := storage.ReadSliceFileContent(ctx, st, sourceSliceID, filePath)
	if err != nil {
		return false, err
	}
	sourceEntry, err := st.GetEntryByPath(ctx, sourceSliceID, filePath)
	if err != nil {
		return false, err
	}

	manifest, err := storage.WriteSliceFileManifestWithMetadata(
		ctx,
		st,
		targetSliceID,
		filePath,
		append([]byte(nil), sourceFile.Content...),
		sourceEntry.Executable,
		sourceEntry.SymlinkTarget,
	)
	if err != nil {
		return false, err
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:            common.GenerateEntryID(targetSliceID, filePath),
		Path:          filePath,
		Type:          "file",
		ParentID:      targetSliceID,
		Size:          sourceFile.Size,
		Hash:          manifest.Hash,
		Executable:    sourceEntry.Executable,
		SymlinkTarget: sourceEntry.SymlinkTarget,
	}); err != nil {
		return false, err
	}
	if err := st.AddFileToSlice(ctx, filePath, targetSliceID); err != nil {
		return false, err
	}
	return true, nil
}

func recordSliceStateCommit(ctx context.Context, st storage.Storage, slice *models.Slice, message string) error {
	if slice == nil {
		return fmt.Errorf("slice is nil")
	}

	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		return err
	}

	entries, err := collectSliceEntries(ctx, st, slice.ID)
	if err != nil {
		return err
	}

	snapshotFiles := make(map[string]string, len(entries))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		paths = append(paths, entry.Path)
		if entry.Type != "file" {
			continue
		}
		manifest, err := st.GetFileManifest(ctx, slice.ID, entry.Path)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				continue
			}
			return err
		}
		if strings.TrimSpace(manifest.Hash) == "" {
			continue
		}
		snapshotFiles[entry.Path] = strings.TrimSpace(manifest.Hash)
	}

	now := time.Now()
	commitHash := fmt.Sprintf("home-backfill-%d", now.UnixNano())
	if err := st.AddSliceCommit(ctx, slice.ID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: meta.HeadCommitHash,
		Timestamp:  now,
		Message:    strings.TrimSpace(message),
	}); err != nil {
		return err
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Files:      snapshotFiles,
		Timestamp:  now,
	}); err != nil {
		return err
	}
	return st.UpdateSliceMetadata(ctx, slice.ID, &models.SliceMetadata{
		SliceID:            slice.ID,
		HeadCommitHash:     commitHash,
		ModifiedFiles:      paths,
		LastModified:       now,
		ModifiedFilesCount: len(paths),
	})
}

func collectSliceEntries(ctx context.Context, st storage.Storage, sliceID string) ([]*models.DirectoryEntry, error) {
	rootChildren, err := st.ListEntries(ctx, sliceID, sliceID)
	if err != nil {
		return nil, err
	}

	entries := make([]*models.DirectoryEntry, 0, len(rootChildren))
	queue := make([]string, 0, len(rootChildren))
	for _, child := range rootChildren {
		if child == nil || strings.TrimSpace(child.Path) == "" {
			continue
		}
		entries = append(entries, child)
		if child.Type == "directory" {
			queue = append(queue, child.ID)
		}
	}

	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		children, err := st.ListEntries(ctx, sliceID, parentID)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child == nil || strings.TrimSpace(child.Path) == "" {
				continue
			}
			entries = append(entries, child)
			if child.Type == "directory" {
				queue = append(queue, child.ID)
			}
		}
	}
	return entries, nil
}

func collectSlicePaths(ctx context.Context, st storage.Storage, sliceID string) ([]string, error) {
	entries, err := collectSliceEntries(ctx, st, sliceID)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		paths = append(paths, entry.Path)
	}
	return paths, nil
}

func isDuplicateWrite(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate key") || strings.Contains(text, "unique constraint")
}
