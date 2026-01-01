package workflow_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatus tests showing current directory's slice binding
// Command: gs status
func TestStatus(t *testing.T) {
	testSliceID := "status-slice"
	if err := createTestSlice(testSliceID); err != nil {
		t.Fatalf("Failed to create slice: %v", err)
	}

	tmpDir := t.TempDir()
	if _, err := runCLIInDir(tmpDir, "init", testSliceID); err != nil {
		t.Fatalf("Failed to initialize slice: %v", err)
	}

	output, err := runCLIInDir(tmpDir, "status")
	if err != nil {
		t.Fatalf("status command failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, testSliceID) {
		t.Fatalf("Expected status to include slice id %s, got: %s", testSliceID, output)
	}
}

// TestStatusDetailed tests showing working directory state
// Command: gs status --detailed
func TestStatusDetailed(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestStatusConflicts tests showing conflicts in current working directory
// Command: gs status --conflicts
func TestStatusConflicts(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestStatusUncommitted tests showing uncommitted files
// Command: gs status --uncommitted
func TestStatusUncommitted(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestStatusSliceID tests displaying current slice ID
// Expected: Shows slice ID from .gs/config
func TestStatusSliceID(t *testing.T) {
	testSliceID := "status-slice-id"
	if err := createTestSlice(testSliceID); err != nil {
		t.Fatalf("Failed to create slice: %v", err)
	}

	tmpDir := t.TempDir()
	if _, err := runCLIInDir(tmpDir, "init", testSliceID); err != nil {
		t.Fatalf("Failed to initialize slice: %v", err)
	}

	output, err := runCLIInDir(tmpDir, "status")
	if err != nil {
		t.Fatalf("status command failed: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(output, testSliceID) {
		t.Fatalf("Expected status to include slice id %s, got: %s", testSliceID, output)
	}
}

// TestStatusHeadCommit tests displaying current slice head commit
// Expected: Shows latest commit hash
func TestStatusHeadCommit(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestStatusActiveChangeset tests displaying active changeset
// Expected: Shows active changeset ID if exists
func TestStatusActiveChangeset(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestStatusUncommittedCount tests counting uncommitted files
// Expected: Shows number of uncommitted files
func TestStatusUncommittedCount(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestStatusWorkingDirectoryState tests showing working directory state
// Expected: Shows clean/dirty state
func TestStatusWorkingDirectoryState(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestStatusNotInitialized tests showing error for non-initialized directory
// Expected: Error message suggesting gs init
func TestStatusNotInitialized(t *testing.T) {
	tmpDir := t.TempDir()
	output, _ := runCLIInDir(tmpDir, "status")

	if !strings.Contains(output, "Not in a gitslice directory") {
		t.Fatalf("expected helpful message for uninitialized dir, got: %s", output)
	}
}

// TestDirectorySliceBinding tests one directory = one slice principle
// Expected: Each directory bound to exactly one slice
func TestDirectorySliceBinding(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestDirectorySwitching tests switching slices by changing directory
// Command: cd ../billing-service; gs log
// Expected: Shows different slice history
func TestDirectorySwitching(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestGsConfigStructure tests .gs directory structure
// Expected: Contains config, current_changeset, state files
func TestGsConfigStructure(t *testing.T) {
	testSliceID := "config-structure"
	if err := createTestSlice(testSliceID); err != nil {
		t.Fatalf("Failed to create slice: %v", err)
	}

	tmpDir := t.TempDir()
	if _, err := runCLIInDir(tmpDir, "init", testSliceID); err != nil {
		t.Fatalf("Failed to initialize slice: %v", err)
	}

	gsDir := filepath.Join(tmpDir, ".gs")
	if _, err := os.Stat(gsDir); err != nil {
		t.Fatalf("expected .gs directory to exist: %v", err)
	}

	configPath := filepath.Join(gsDir, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected to read config: %v", err)
	}

	if string(data) != testSliceID {
		t.Fatalf("expected config to contain slice id %s, got %s", testSliceID, string(data))
	}
}

// TestWorkingDirectoryClean tests clean working directory state
// Expected: No uncommitted changes
func TestWorkingDirectoryClean(t *testing.T) {
	t.Skip("Implementation not ready yet")
}

// TestWorkingDirectoryDirty tests dirty working directory state
// Expected: Has uncommitted changes
func TestWorkingDirectoryDirty(t *testing.T) {
	t.Skip("Implementation not ready yet")
}
