package common

import (
	"context"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/niczy/gitslice/internal/storage"
)

const GenesisMountPath = "/o/genesis/projects/gitslice"

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

// GenerateSliceID creates a new unique slice ID with a "sl-" prefix followed by a UUID.
func GenerateSliceID() string {
	return "sl-" + uuid.New().String()
}

// NormalizeSlicePath prefixes a repo-relative file path with the genesis mount path.
func NormalizeSlicePath(filePath string) string {
	slicePath := path.Join(GenesisMountPath, filePath)
	return strings.TrimPrefix(slicePath, "/")
}
