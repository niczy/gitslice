package common

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

	// Add each file to the root slice under "genesis" directory
	for _, filePath := range files {
		if filePath == "" {
			continue
		}

		// Prefix path with "genesis/" for organization
		slicePath := "genesis/" + filePath

		// Add file path to slice
		if err := st.AddFileToSlice(ctx, slicePath, sliceID); err != nil {
			log.Printf("Warning: failed to add file %s to root slice: %v", slicePath, err)
			continue
		}

		// Read file content from actual git repo location
		fullPath := filepath.Join(repoRoot, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("Warning: failed to read file %s: %v", filePath, err)
			continue
		}

		fileContent := &models.FileContent{
			FileID:  slicePath,
			Path:    slicePath,
			Content: content,
			Size:    int64(len(content)),
			Hash:    fmt.Sprintf("%x", len(content)), // Simple hash for now
		}

		if err := st.AddFileContent(ctx, fileContent); err != nil {
			log.Printf("Warning: failed to store content for %s: %v", slicePath, err)
			continue
		}
	}

	log.Printf("Successfully populated root slice with %d files", len(files))
	return nil
}
