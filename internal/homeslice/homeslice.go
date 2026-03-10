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

	homeSlice, err := ensureHomeSliceRecord(ctx, st, user)
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
	if err := ensureDirectory(ctx, st, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if err := ensureDirectory(ctx, st, rootSlice.ID, rootPath); err != nil {
		return nil, err
	}
	return homeSlice, nil
}

func ensureHomeSliceRecord(ctx context.Context, st storage.Storage, user *models.User) (*models.Slice, error) {
	if user == nil {
		return nil, fmt.Errorf("user is nil")
	}

	sliceID := IDForUsername(user.Username)
	slice, err := st.GetSlice(ctx, sliceID)
	if err == nil {
		return slice, nil
	}
	if err != storage.ErrSliceNotFound {
		return nil, err
	}

	now := time.Now()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          sliceID,
		Name:        user.Username,
		Description: fmt.Sprintf("Home slice for %s", user.Username),
		Files:       []string{},
		Owners:      []string{user.Username},
		CreatedBy:   user.Username,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil && err != storage.ErrSliceAlreadyExists {
		return nil, err
	}
	return st.GetSlice(ctx, sliceID)
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

func ensureDirectory(ctx context.Context, st storage.Storage, sliceID, dirPath string) error {
	dirPath = strings.TrimSpace(dirPath)
	if dirPath == "" {
		return nil
	}

	entry, err := st.GetEntryByPath(ctx, sliceID, dirPath)
	if err == nil {
		if entry.Type != "directory" {
			return fmt.Errorf("path %q already exists as %s in slice %s", dirPath, entry.Type, sliceID)
		}
		return nil
	}
	if err != storage.ErrEntryNotFound {
		return err
	}

	return st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, dirPath),
		Path:     dirPath,
		Type:     "directory",
		ParentID: sliceID,
	})
}

func isDuplicateWrite(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate key") || strings.Contains(text, "unique constraint")
}
