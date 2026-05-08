package common

import (
	"context"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/ids"
	"github.com/niczy/gitslice/internal/storage"
)

const GenesisMountPath = "/o/genesis/projects/gitslice"
const RootSliceID = ids.RootSliceID
const ChangesetIDPrefix = ids.ChangesetIDPrefix
const CommitIDPrefix = ids.CommitIDPrefix
const ChangesetVersionIDPrefix = ids.ChangesetVersionIDPrefix
const ChangesetSnapshotIDPrefix = ids.ChangesetSnapshotIDPrefix
const FileChangeIDPrefix = ids.FileChangeIDPrefix

// EnsureRootSliceInitialized initializes the root slice if it doesn't exist.
// It returns an error only if initialization fails critically.
// This function is idempotent and safe to call multiple times.
func EnsureRootSliceInitialized(ctx context.Context, st storage.Storage) error {
	// Check if root slice already exists
	_, err := st.GetRootSlice(ctx)
	if err == nil {
		return nil
	}

	// Root slice doesn't exist, initialize it
	if err := st.InitializeRootSlice(ctx); err != nil {
		return fmt.Errorf("failed to initialize root slice: %w", err)
	}

	log.Println("Root slice initialized successfully")
	return nil
}

// ExtractParentDirs returns all parent directories for a given path
// e.g. "o/genesis/projects/gitslice/internal/common/init.go"
// -> ["o", "o/genesis", "o/genesis/projects", ...]
func ExtractParentDirs(filePath string) []string {
	var dirs []string
	parts := strings.Split(filePath, "/")

	for i := 0; i < len(parts)-1; i++ {
		dirPath := strings.Join(parts[:i+1], "/")
		dirs = append(dirs, dirPath)
	}

	return dirs
}

// SortDirsByDepth sorts directories so parents come before children
func SortDirsByDepth(dirs map[string]bool) []string {
	var sorted []string
	for dir := range dirs {
		sorted = append(sorted, dir)
	}

	sort.Slice(sorted, func(i, j int) bool {
		depthI := strings.Count(sorted[i], "/")
		depthJ := strings.Count(sorted[j], "/")
		if depthI != depthJ {
			return depthI < depthJ
		}
		return sorted[i] < sorted[j]
	})

	return sorted
}

// GenerateEntryID creates a unique ID for a directory entry
func GenerateEntryID(sliceID, path string) string {
	return fmt.Sprintf("%s:%s", sliceID, path)
}

// GenerateSliceID creates a new opaque custom-slice ID.
func GenerateSliceID() string {
	return ids.GenerateSliceID()
}

// GenerateCommitID creates an opaque synthetic commit ID.
func GenerateCommitID() string {
	return ids.GenerateCommitID()
}

// GenerateInitialCommitID creates a deterministic initial commit ID for a slice.
func GenerateInitialCommitID(sliceID string) string {
	return ids.GenerateInitialCommitID(sliceID)
}

// IsInitialCommitID reports whether an ID is a deterministic slice-initial marker.
func IsInitialCommitID(commitID string) bool {
	return ids.IsInitialCommitID(commitID)
}

// GenerateChangesetVersionHash creates an opaque version marker for changeset contents.
func GenerateChangesetVersionHash() string {
	return ids.GenerateChangesetVersionHash()
}

// GenerateChangesetSnapshotID creates a deterministic ID for one changeset version.
func GenerateChangesetSnapshotID(changesetID string, version int64) string {
	return ids.GenerateChangesetSnapshotID(changesetID, version)
}

// GenerateFileChangeID creates a stable private ID for a path within one commit.
func GenerateFileChangeID(commitID, filePath string) string {
	return ids.GenerateFileChangeID(commitID, filePath)
}

// GenerateMergeEventID creates an opaque durable merge event ID.
func GenerateMergeEventID() string {
	return ids.GenerateMergeEventID()
}

// NormalizeSlicePath prefixes a repo-relative file path with the genesis mount path.
func NormalizeSlicePath(filePath string) string {
	slicePath := path.Join(GenesisMountPath, filePath)
	return strings.TrimPrefix(slicePath, "/")
}
