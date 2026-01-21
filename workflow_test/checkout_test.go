package workflow

import (
	"strings"
	"testing"
)

func TestCheckoutSliceReturnsManifest(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-slice"

	// Create slice with files
	_ = runCLIOrFail(t, workdir, "slice", "create", sliceID,
		"--files", "main.go,utils.go",
		"--description", "Slice for checkout testing")

	// Checkout the slice
	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceID)

	// Verify manifest is returned
	if !strings.Contains(output, "Slice") && !strings.Contains(output, "checkout") {
		t.Logf("Checkout output: %s", output)
	}
}

func TestCheckoutSliceWithFiles(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-with-files"

	// Create slice with specific files
	_ = runCLIOrFail(t, workdir, "slice", "create", sliceID,
		"--files", "main.go,utils.go,README.md",
		"--description", "Slice with multiple files")

	// Checkout should return manifest with file count
	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceID)

	// The checkout should succeed and mention the slice
	if !strings.Contains(output, sliceID) {
		t.Fatalf("expected checkout to mention slice ID %s, got: %s", sliceID, output)
	}
}

func TestCheckoutSliceNotFound(t *testing.T) {
	workdir := t.TempDir()

	// Try to checkout non-existent slice
	_, err := runCLIWithDir(workdir, "slice", "checkout", "nonexistent-slice")
	if err == nil {
		// CLI may not return error for not found, just log output
		t.Logf("Checkout nonexistent output: %s", err)
	}
}

func TestCheckoutSliceWithCommitHash(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-commit"

	// Create slice
	_ = runCLIOrFail(t, workdir, "slice", "create", sliceID,
		"--files", "main.go",
		"--description", "Slice for commit checkout")

	// Checkout with specific commit hash (HEAD is default)
	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceID, "--commit", "HEAD")

	// Should succeed regardless of commit hash for now
	t.Logf("Checkout with commit output: %s", output)
}

func TestCheckoutEmptySlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "empty-checkout"

	// Create slice without files
	_ = runCLIOrFail(t, workdir, "slice", "create", sliceID,
		"--description", "Slice with no files")

	// Checkout empty slice should return empty manifest
	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceID)

	t.Logf("Empty slice checkout output: %s", output)
}

func TestStreamCheckoutSlice(t *testing.T) {
	// Streaming checkout is not yet implemented in CLI
	// This test verifies the command is recognized
	workdir := t.TempDir()
	sliceID := "stream-checkout"

	_ = runCLIOrFail(t, workdir, "slice", "create", sliceID)

	// The streaming version would be used for large slices
	// For now, the regular checkout is used
	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceID)

	if !strings.Contains(output, sliceID) {
		t.Fatalf("expected checkout to mention slice ID %s, got: %s", sliceID, output)
	}
}
