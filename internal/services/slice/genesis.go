package sliceservice

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

// PopulateGenesisFromGit scans the current git repository and adds all tracked files
// to the root slice by creating and merging a changeset through the service API,
// so the initialization appears in commit history.
func (s *sliceServiceServer) PopulateGenesisFromGit(ctx context.Context) error {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "1" || os.Getenv("SKIP_GIT_POPULATION") == "1" {
		log.Println("Skipping git population (test environment detected)")
		return nil
	}

	// Check if root slice already has files (idempotent)
	rootSlice, err := s.storage.GetRootSlice(ctx)
	if err != nil {
		return fmt.Errorf("root slice not initialized: %w", err)
	}
	if len(rootSlice.Files) > 0 {
		log.Println("Root slice already populated, skipping genesis")
		return nil
	}

	sliceID := rootSlice.ID

	// Discover git repo and tracked files
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("not in a git repository: %w", err)
	}
	repoRoot := strings.TrimSpace(string(output))

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

	log.Printf("Found %d files in git repository, populating root slice via changeset", len(files))

	// Phase 1: Write directory and file entries + content into storage.
	// The changeset/merge API handles commit history; storage writes are needed
	// for the actual data (entries, content) which the API doesn't upload yet.
	dirsToCreate := make(map[string]bool)
	var allModifiedFiles []string

	for _, fp := range files {
		if fp == "" {
			continue
		}
		slicePath := common.NormalizeSlicePath(fp)
		for _, dir := range common.ExtractParentDirs(slicePath) {
			dirsToCreate[dir] = true
		}
	}

	sortedDirs := common.SortDirsByDepth(dirsToCreate)
	for _, dirPath := range sortedDirs {
		dirEntry := &models.DirectoryEntry{
			ID:       common.GenerateEntryID(sliceID, dirPath),
			Path:     dirPath,
			Type:     "directory",
			ParentID: sliceID,
			Size:     0,
		}
		if err := s.storage.AddEntry(ctx, dirEntry); err != nil {
			log.Printf("Warning: failed to add directory entry %s: %v", dirPath, err)
			continue
		}
		if err := s.storage.AddFileToSlice(ctx, dirPath, sliceID); err != nil {
			log.Printf("Warning: failed to add directory %s to slice: %v", dirPath, err)
		}
		allModifiedFiles = append(allModifiedFiles, dirPath)
	}

	fileCount := 0
	for _, fp := range files {
		if fp == "" {
			continue
		}

		slicePath := common.NormalizeSlicePath(fp)
		fullPath := filepath.Join(repoRoot, fp)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("Warning: failed to read file %s: %v", fp, err)
			continue
		}

		contentHash := fmt.Sprintf("%x", len(content))

		fileEntry := &models.DirectoryEntry{
			ID:       common.GenerateEntryID(sliceID, slicePath),
			Path:     slicePath,
			Type:     "file",
			ParentID: sliceID,
			Content:  content,
			Size:     int64(len(content)),
		}
		if err := s.storage.AddEntry(ctx, fileEntry); err != nil {
			log.Printf("Warning: failed to add file entry %s: %v", slicePath, err)
			continue
		}
		if err := s.storage.AddFileToSlice(ctx, slicePath, sliceID); err != nil {
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
		if err := s.storage.AddFileContent(ctx, fileContent); err != nil {
			log.Printf("Warning: failed to store content for %s: %v", slicePath, err)
			continue
		}

		allModifiedFiles = append(allModifiedFiles, slicePath)
		fileCount++
	}

	if err := s.storage.SetSliceFiles(ctx, sliceID, allModifiedFiles); err != nil && err != storage.ErrSliceFilesImmutable {
		return fmt.Errorf("failed to set root slice files: %w", err)
	}

	// Phase 2: Create and merge a changeset via the service's own RPC methods.
	csResp, err := s.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       sliceID,
		ModifiedFiles: allModifiedFiles,
		Author:        "system",
		Message:       "Genesis: initialize repository files",
	})
	if err != nil {
		return fmt.Errorf("failed to create genesis changeset: %w", err)
	}

	mergeResp, err := s.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: csResp.ChangesetId,
	})
	if err != nil {
		return fmt.Errorf("failed to merge genesis changeset: %w", err)
	}

	if mergeResp.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		return fmt.Errorf("genesis merge failed with status: %s", mergeResp.Status.String())
	}

	log.Printf("Successfully populated root slice with %d directories and %d files via genesis changeset %s (commit %s)",
		len(sortedDirs), fileCount, csResp.ChangesetId, mergeResp.NewCommitHash)

	if err := s.ensureDefaultSlices(ctx, sliceID); err != nil {
		return err
	}
	return nil
}

func (s *sliceServiceServer) ensureDefaultSlices(ctx context.Context, rootSliceID string) error {
	defaults := []struct {
		ID          string
		Name        string
		FolderPath  string
		Description string
	}{
		{
			ID:          "gs_cli",
			Name:        "gs_cli",
			FolderPath:  "gs_cli",
			Description: "Slice for /gitslice/gs_cli",
		},
		{
			ID:          "slice_service",
			Name:        "slice_service",
			FolderPath:  "slice_service",
			Description: "Slice for /gitslice/slice_service",
		},
	}

	for _, def := range defaults {
		if _, err := s.storage.GetSlice(ctx, def.ID); err == nil {
			continue
		} else if err != storage.ErrSliceNotFound {
			return fmt.Errorf("failed to check slice %s: %w", def.ID, err)
		}

		resp, err := s.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
			ParentSliceId: rootSliceID,
			FolderPath:    common.NormalizeSlicePath(def.FolderPath),
			NewSliceId:    def.ID,
			Name:          def.Name,
			Description:   def.Description,
		})
		if err != nil {
			return fmt.Errorf("failed to create slice %s: %w", def.ID, err)
		}

		log.Printf("Created slice %s with %d files from %s", def.ID, len(resp.Files), def.FolderPath)
	}

	return nil
}
