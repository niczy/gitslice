package common

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

// EnsureRootSliceInitialized initializes the root slice if it doesn't exist.
// It returns an error only if initialization fails critically.
// This function is idempotent and safe to call multiple times.
func EnsureRootSliceInitialized(ctx context.Context, st storage.Storage) error {
	// Check if root slice already exists
	rootSlice, err := st.GetRootSlice(ctx)
	if err == nil {
		// Root slice already exists, check if it needs to be populated
		if len(rootSlice.Files) == 0 {
			log.Println("Root slice exists but is empty, attempting to populate from git repository")
			if err := populateRootSliceFromGit(ctx, st, rootSlice.ID); err != nil {
				log.Printf("Warning: failed to populate root slice: %v", err)
			}
		}
		return nil
	}

	// Root slice doesn't exist, initialize it
	if err := st.InitializeRootSlice(ctx); err != nil {
		return fmt.Errorf("failed to initialize root slice: %w", err)
	}

	log.Println("Root slice initialized successfully")

	// Try to populate it with files from git repository
	if err := populateRootSliceFromGit(ctx, st, "root_slice"); err != nil {
		log.Printf("Warning: failed to populate root slice: %v", err)
	}

	return nil
}

// populateRootSliceFromGit scans the current git repository and adds all tracked files to the root slice
func populateRootSliceFromGit(ctx context.Context, st storage.Storage, sliceID string) error {
	// Skip git population during tests
	if os.Getenv("RUN_INTEGRATION_TESTS") == "1" || os.Getenv("SKIP_GIT_POPULATION") == "1" {
		log.Println("Skipping git population (test environment detected)")
		return nil
	}

	// Check if we're in a git repository
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}

	repoRoot := strings.TrimSpace(string(output))

	// Get list of all tracked files in the repository
	cmd = exec.Command("git", "ls-files")
	cmd.Dir = repoRoot
	output, err = cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list git files: %w", err)
	}

	files := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(files) == 0 || (len(files) == 1 && files[0] == "") {
		log.Println("No files found in git repository")
		return nil
	}

	log.Printf("Found %d files in git repository, adding to root slice under genesis/", len(files))

	// Collect all unique directories we need to create
	dirsToCreate := make(map[string]bool)
	dirEntries := make(map[string]*models.DirectoryEntry)

	// Process each file and extract parent directories
	for _, filePath := range files {
		if filePath == "" {
			continue
		}

		// Prefix path with "genesis/" for organization
		slicePath := "genesis/" + filePath

		// Extract all parent directories
		dirs := extractParentDirs(slicePath)
		for _, dir := range dirs {
			dirsToCreate[dir] = true
		}
	}

	// Create directory entries in order (parent before child)
	sortedDirs := sortDirsByDepth(dirsToCreate)
	for _, dirPath := range sortedDirs {
		dirEntry := &models.DirectoryEntry{
			ID:       generateEntryID(sliceID, dirPath),
			Path:     dirPath,
			Type:     "directory",
			ParentID: sliceID,
			Size:     0,
		}

		if err := st.AddEntry(ctx, dirEntry); err != nil {
			log.Printf("Warning: failed to add directory entry %s: %v", dirPath, err)
			continue
		}

		dirEntries[dirPath] = dirEntry

		if err := st.AddFileToSlice(ctx, dirPath, sliceID); err != nil {
			log.Printf("Warning: failed to add directory %s to slice: %v", dirPath, err)
		}
	}

	// Now create file entries
	fileCount := 0
	for _, filePath := range files {
		if filePath == "" {
			continue
		}

		// Prefix path with "genesis/" for organization
		slicePath := "genesis/" + filePath

		// Read file content from actual git repo location
		fullPath := filepath.Join(repoRoot, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("Warning: failed to read file %s: %v", filePath, err)
			continue
		}

		// Create directory entry for the file
		fileEntry := &models.DirectoryEntry{
			ID:       generateEntryID(sliceID, slicePath),
			Path:     slicePath,
			Type:     "file",
			ParentID: sliceID,
			Content:  content,
			Size:     int64(len(content)),
		}

		if err := st.AddEntry(ctx, fileEntry); err != nil {
			log.Printf("Warning: failed to add file entry %s: %v", slicePath, err)
			continue
		}

		// Add file path to slice
		if err := st.AddFileToSlice(ctx, slicePath, sliceID); err != nil {
			log.Printf("Warning: failed to add file %s to root slice: %v", slicePath, err)
			continue
		}

		// Also store in file content store for compatibility
		fileContent := &models.FileContent{
			FileID:  slicePath,
			Path:    slicePath,
			Content: content,
			Size:    int64(len(content)),
			Hash:    fmt.Sprintf("%x", len(content)),
		}

		if err := st.AddFileContent(ctx, fileContent); err != nil {
			log.Printf("Warning: failed to store content for %s: %v", slicePath, err)
			continue
		}

		fileCount++
	}

	log.Printf("Successfully populated root slice with %d directories and %d files", len(sortedDirs), fileCount)
	return nil
}

// extractParentDirs returns all parent directories for a given path
// e.g. "genesis/internal/common/init.go" -> ["genesis", "genesis/internal", "genesis/internal/common"]
func extractParentDirs(filePath string) []string {
	var dirs []string
	parts := strings.Split(filePath, "/")

	// Build up each parent directory path
	for i := 0; i < len(parts)-1; i++ {
		dirPath := strings.Join(parts[:i+1], "/")
		dirs = append(dirs, dirPath)
	}

	return dirs
}

// sortDirsByDepth sorts directories so parents come before children
func sortDirsByDepth(dirs map[string]bool) []string {
	var sorted []string
	for dir := range dirs {
		sorted = append(sorted, dir)
	}

	// Sort by depth (number of slashes) and then alphabetically
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

// generateEntryID creates a unique ID for a directory entry
func generateEntryID(sliceID, path string) string {
	return fmt.Sprintf("%s:%s", sliceID, path)
}
