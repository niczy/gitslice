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
	"google.golang.org/grpc/metadata"
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
		info, err := os.Lstat(fullPath)
		if err != nil {
			log.Printf("Warning: failed to stat file %s: %v", fp, err)
			continue
		}
		var (
			content       []byte
			executable    bool
			symlinkTarget string
		)
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(fullPath)
			if err != nil {
				log.Printf("Warning: failed to read symlink %s: %v", fp, err)
				continue
			}
			symlinkTarget = target
			content = []byte(target)
		} else {
			content, err = os.ReadFile(fullPath)
			if err != nil {
				log.Printf("Warning: failed to read file %s: %v", fp, err)
				continue
			}
			executable = info.Mode()&0o111 != 0
		}

		fileEntry := &models.DirectoryEntry{
			ID:            common.GenerateEntryID(sliceID, slicePath),
			Path:          slicePath,
			Type:          "file",
			ParentID:      sliceID,
			Content:       content,
			Size:          int64(len(content)),
			Executable:    executable,
			SymlinkTarget: symlinkTarget,
		}
		if err := s.storage.AddEntry(ctx, fileEntry); err != nil {
			log.Printf("Warning: failed to add file entry %s: %v", slicePath, err)
			continue
		}
		if err := s.storage.AddFileToSlice(ctx, slicePath, sliceID); err != nil {
			log.Printf("Warning: failed to add file %s to root slice: %v", slicePath, err)
			continue
		}
		if _, err := storage.WriteSliceFileManifestWithMetadata(ctx, s.storage, sliceID, slicePath, content, executable, symlinkTarget); err != nil {
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
	// These are internal calls (not coming from gRPC), so attach fake auth.
	authCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "User system"))
	csResp, err := s.CreateChangeset(authCtx, &slicev1.CreateChangesetRequest{
		SliceId:       sliceID,
		ModifiedFiles: allModifiedFiles,
		Author:        "system",
		Message:       "Genesis: initialize repository files",
	})
	if err != nil {
		return fmt.Errorf("failed to create genesis changeset: %w", err)
	}

	mergeResp, err := s.MergeChangeset(authCtx, &slicev1.MergeChangesetRequest{
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
	return nil
}
