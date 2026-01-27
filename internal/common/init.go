package common

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const genesisMountPath = "/o/genesis/projects/gitslice"

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

// populateRootSliceFromGit scans the current git repository and adds all tracked files
// to the root slice using the changeset workflow so the initialization appears in commit history.
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

	log.Printf("Found %d files in git repository, adding to root slice under %s via changeset", len(files), genesisMountPath)

	// Phase 1: Create directory and file entries in storage, collect all paths
	dirsToCreate := make(map[string]bool)
	var allModifiedFiles []string

	for _, filePath := range files {
		if filePath == "" {
			continue
		}
		slicePath := normalizeSlicePath(filePath)
		dirs := extractParentDirs(slicePath)
		for _, dir := range dirs {
			dirsToCreate[dir] = true
		}
	}

	// Create directory entries
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
		if err := st.AddFileToSlice(ctx, dirPath, sliceID); err != nil {
			log.Printf("Warning: failed to add directory %s to slice: %v", dirPath, err)
		}
		allModifiedFiles = append(allModifiedFiles, dirPath)
	}

	// Create file entries and content
	now := time.Now()
	var fileChangeRecords []*models.FileChangeRecord
	fileCount := 0
	for _, filePath := range files {
		if filePath == "" {
			continue
		}

		slicePath := normalizeSlicePath(filePath)
		fullPath := filepath.Join(repoRoot, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("Warning: failed to read file %s: %v", filePath, err)
			continue
		}

		contentHash := fmt.Sprintf("%x", len(content))

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

		if err := st.AddFileToSlice(ctx, slicePath, sliceID); err != nil {
			log.Printf("Warning: failed to add file %s to root slice: %v", slicePath, err)
			continue
		}

		fileContent := &models.FileContent{
			FileID:  slicePath,
			Path:    slicePath,
			Content: content,
			Size:    int64(len(content)),
			Hash:    contentHash,
		}
		if err := st.AddFileContent(ctx, fileContent); err != nil {
			log.Printf("Warning: failed to store content for %s: %v", slicePath, err)
			continue
		}

		allModifiedFiles = append(allModifiedFiles, slicePath)

		// Build file change record for history tracking
		fileChangeRecords = append(fileChangeRecords, &models.FileChangeRecord{
			ID:         fmt.Sprintf("genesis-%s", slicePath),
			SliceID:    sliceID,
			Path:       slicePath,
			ChangeType: models.ChangeTypeAdd,
			NewHash:    contentHash,
			Author:     "system",
			Message:    "Genesis: initialize repository files",
			Timestamp:  now,
		})

		fileCount++
	}

	// Phase 2: Create and merge a changeset so the initialization shows in history
	genesisCommitHash := fmt.Sprintf("genesis-commit-%d", now.UnixNano())
	genesisMessage := "Genesis: initialize repository files"

	cs := &models.Changeset{
		ID:            fmt.Sprintf("cs-genesis-%d", now.UnixNano()),
		Hash:          fmt.Sprintf("hash-genesis-%d", now.UnixNano()),
		SliceID:       sliceID,
		ModifiedFiles: allModifiedFiles,
		Status:        models.ChangesetStatusMerged,
		Author:        "system",
		Message:       genesisMessage,
		CreatedAt:     now,
		MergedAt:      &now,
	}

	if err := st.CreateChangeset(ctx, cs); err != nil {
		return fmt.Errorf("failed to create genesis changeset: %w", err)
	}

	// Create the commit record on the root slice
	if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{
		CommitHash: genesisCommitHash,
		ParentHash: "",
		Timestamp:  now,
		Message:    genesisMessage,
	}); err != nil {
		log.Printf("Warning: failed to add genesis commit: %v", err)
	}

	// Update slice metadata with the genesis commit
	metadata, err := st.GetSliceMetadata(ctx, sliceID)
	if err == nil {
		metadata.HeadCommitHash = genesisCommitHash
		metadata.ModifiedFiles = allModifiedFiles
		metadata.ModifiedFilesCount = len(allModifiedFiles)
		metadata.LastModified = now
		if err := st.UpdateSliceMetadata(ctx, sliceID, metadata); err != nil {
			log.Printf("Warning: failed to update root slice metadata: %v", err)
		}
	}

	// Create commit snapshot for versioned file access
	snapshotFiles := make(map[string]string)
	for _, f := range allModifiedFiles {
		snapshotFiles[f] = f
	}
	snapshot := &models.CommitSnapshot{
		CommitHash: genesisCommitHash,
		SliceID:    sliceID,
		Files:      snapshotFiles,
		Timestamp:  now,
	}
	if err := st.SaveCommitSnapshot(ctx, snapshot); err != nil {
		log.Printf("Warning: failed to save genesis commit snapshot: %v", err)
	}

	// Record file change history so genesis files appear in history queries
	// Set the commit hash on all records now that we have it
	for _, rec := range fileChangeRecords {
		rec.CommitHash = genesisCommitHash
	}
	if err := st.AddFileChanges(ctx, fileChangeRecords); err != nil {
		log.Printf("Warning: failed to record genesis file changes: %v", err)
	}

	// Update global state with genesis commit
	state, _ := st.GetGlobalState(ctx)
	if state == nil {
		state = &models.GlobalState{}
	}
	globalCommit := &models.GlobalCommit{
		CommitHash:     genesisCommitHash,
		Timestamp:      now,
		MergedSliceIDs: []string{sliceID},
	}
	state.GlobalCommitHash = genesisCommitHash
	state.Timestamp = now
	state.History = append([]*models.GlobalCommit{globalCommit}, state.History...)
	if err := st.UpdateGlobalState(ctx, state); err != nil {
		log.Printf("Warning: failed to update global state with genesis commit: %v", err)
	}

	log.Printf("Successfully populated root slice with %d directories and %d files via genesis changeset %s", len(sortedDirs), fileCount, cs.ID)
	return nil
}

// extractParentDirs returns all parent directories for a given path
// e.g. "o/genesis/projects/gitslice/internal/common/init.go"
// -> ["o", "o/genesis", "o/genesis/projects", "o/genesis/projects/gitslice", "o/genesis/projects/gitslice/internal", "o/genesis/projects/gitslice/internal/common"]
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

func normalizeSlicePath(filePath string) string {
	slicePath := path.Join(genesisMountPath, filePath)
	return strings.TrimPrefix(slicePath, "/")
}
