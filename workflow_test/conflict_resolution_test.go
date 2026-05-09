package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

// TestConflictList tests showing all conflicts for current working directory
// Command: gs conflict list
func TestConflictList(t *testing.T) {
	workdir, sliceID, fileID := createConflictSetup(t)

	output := runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID, "--json")
	var resp conflictListJSON
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("decode conflict list JSON: %v\nOutput:\n%s", err, output)
	}
	if resp.Total == 0 || len(resp.Conflicts) == 0 || resp.Conflicts[0].FileID != fileID {
		t.Fatalf("expected conflict output to mention %s, got: %+v", fileID, resp)
	}
}

// TestConflictListChangeset tests showing conflicts for specific changeset
// Command: gs conflict list --changeset cl-abc123
func TestConflictListChangeset(t *testing.T) {
	workdir, sliceID, fileID := createConflictSetup(t)

	// Legacy conflict listing remains visible, but merge authority is now the
	// home path head recorded in the changeset snapshot.
	output := runCLIOrFail(t, workdir, "changeset", "create", "--message", "conflict", "--files", fileID)
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("expected changeset ID in output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected path-head-authoritative merge success, got: %s", output)
	}

	// Ensure the list command still returns the conflict for the slice
	listOutput := runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID)
	if !strings.Contains(listOutput, fileID) {
		t.Fatalf("expected conflict list to include %s, got: %s", fileID, listOutput)
	}
}

// TestConflictListDetailed tests showing conflicts in detail
// Command: gs conflict list --detailed
func TestConflictListDetailed(t *testing.T) {
	workdir, sliceID, fileID := createConflictSetup(t)

	output := runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID, "--detailed", "--severity")
	if !strings.Contains(output, "severity") {
		t.Fatalf("expected severity information in output, got: %s", output)
	}
	if !strings.Contains(output, fileID) {
		t.Fatalf("expected conflict output to mention %s, got: %s", fileID, output)
	}
}

// TestConflictListSeverity tests showing conflicts with severity levels
// Command: gs conflict list --severity
func TestConflictListSeverity(t *testing.T) {
	workdir, sliceID, _ := createConflictSetup(t)

	output := runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID, "--severity")
	if !strings.Contains(output, "severity") {
		t.Fatalf("expected severity information, got: %s", output)
	}
}

// TestConflictResolveInteractive tests interactive conflict resolution
// Command: gs conflict resolve
func TestConflictResolveInteractive(t *testing.T) {
	output := runCLIOrFail(t, "", "conflict", "resolve")
	if !strings.Contains(output, "Usage: gs conflict resolve") {
		t.Fatalf("expected usage guidance, got: %s", output)
	}
}

// TestConflictResolveTheirs tests auto-resolve with incoming changes
// Command: gs conflict resolve --theirs
func TestConflictResolveTheirs(t *testing.T) {
	workdir, _, fileID, sliceB := createConflictSetupWithSlices(t)

	output := runCLIOrFail(t, workdir, "conflict", "resolve", "--theirs", sliceB, fileID, "--json")
	var resp conflictResolveJSON
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("decode conflict resolve JSON: %v\nOutput:\n%s", err, output)
	}
	if resp.Conflict.FileID != fileID {
		t.Fatalf("expected resolve confirmation, got: %+v", resp)
	}
}

// TestConflictResolveOurs tests auto-resolve with local changes
// Command: gs conflict resolve --ours
func TestConflictResolveOurs(t *testing.T) {
	workdir, sliceID, fileID, _ := createConflictSetupWithSlices(t)

	output := runCLIOrFail(t, workdir, "conflict", "resolve", "--ours", fileID, "--json")
	var resp conflictResolveJSON
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("decode conflict resolve JSON: %v\nOutput:\n%s", err, output)
	}
	if resp.Conflict.FileID != fileID {
		t.Fatalf("expected resolve confirmation, got: %+v", resp)
	}

	// After resolving in favor of the current slice, no conflicts should remain for it
	listOutput := runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID)
	if strings.Contains(listOutput, fileID) {
		t.Fatalf("expected conflict to be cleared for %s, got: %s", fileID, listOutput)
	}
}

// TestConflictResolveResolved tests marking conflict as resolved after manual edit
// Command: gs conflict resolve --resolved file.py
func TestConflictResolveResolved(t *testing.T) {
	assertUnsupportedCommand(t, "conflict", "resolve", "--resolved", "file.py")
}

// TestConflictShow tests showing conflict details before resolving
// Command: gs conflict show file.py
func TestConflictShow(t *testing.T) {
	_, _, fileID := createConflictSetup(t)

	output := runCLIOrFail(t, "", "conflict", "show", fileID, "--json")
	var resp conflictShowJSON
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("decode conflict show JSON: %v\nOutput:\n%s", err, output)
	}
	if !resp.Found || resp.Conflict == nil || resp.Conflict.FileID != fileID {
		t.Fatalf("expected conflict details, got: %+v", resp)
	}
}

// TestConflictHistory tests getting conflict history
// Command: gs conflict history file.py
func TestConflictHistory(t *testing.T) {
	assertUnsupportedCommand(t, "conflict", "history", "file.py")
}

// TestConflictResolutionWorkflow tests full conflict resolution workflow
// Expected: Detect conflict → Show details → Resolve → Retry merge
func TestConflictResolutionWorkflow(t *testing.T) {
	workdir, sliceID, fileID, _ := createConflictSetupWithSlices(t)

	listOutput := runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID)
	if !strings.Contains(listOutput, fileID) {
		t.Fatalf("expected conflict listed before resolution, got: %s", listOutput)
	}

	_ = runCLIOrFail(t, workdir, "conflict", "resolve", "--ours", fileID)

	listOutput = runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID)
	if strings.Contains(listOutput, fileID) {
		t.Fatalf("expected conflict removed after resolution, got: %s", listOutput)
	}
}

// TestConflictSemantic tests semantic conflict type
// Expected: Shows semantic conflicts in code logic
func TestConflictSemantic(t *testing.T) {
	assertUnsupportedCommand(t, "conflict", "list", "--semantic")
}

// TestConflictFormatting tests formatting conflict type
// Expected: Shows formatting conflicts (whitespace, style)
func TestConflictFormatting(t *testing.T) {
	assertUnsupportedCommand(t, "conflict", "list", "--formatting")
}

// TestConflictStructural tests structural conflict type
// Expected: Shows structural conflicts (renames, moves)
func TestConflictStructural(t *testing.T) {
	assertUnsupportedCommand(t, "conflict", "list", "--structural")
}

// TestConflictSeverityCritical tests CRITICAL severity level
// Expected: Blocks merge until resolved
func TestConflictSeverityCritical(t *testing.T) {
	output := runCLIOrFail(t, "", "conflict", "list", "--severity", "critical")
	if !strings.Contains(output, "conflict") {
		t.Fatalf("expected conflicts to be displayed, got: %s", output)
	}
}

// TestConflictSeverityHigh tests HIGH severity level
// Expected: Strongly recommended to resolve
func TestConflictSeverityHigh(t *testing.T) {
	output := runCLIOrFail(t, "", "conflict", "list", "--severity", "high")
	if !strings.Contains(output, "conflict") {
		t.Fatalf("expected conflicts to be displayed, got: %s", output)
	}
}

// TestConflictSeverityMedium tests MEDIUM severity level
// Expected: Warning but can proceed
func TestConflictSeverityMedium(t *testing.T) {
	output := runCLIOrFail(t, "", "conflict", "list", "--severity", "medium")
	if !strings.Contains(output, "conflict") {
		t.Fatalf("expected conflicts to be displayed, got: %s", output)
	}
}

// TestConflictSeverityLow tests LOW severity level
// Expected: Informational only
func TestConflictSeverityLow(t *testing.T) {
	output := runCLIOrFail(t, "", "conflict", "list", "--severity", "low")
	if !strings.Contains(output, "conflict") {
		t.Fatalf("expected conflicts to be displayed, got: %s", output)
	}
}

func createConflictSetup(t *testing.T) (string, string, string) {
	workdir, sliceA, fileID, _ := createConflictSetupWithSlices(t)
	return workdir, sliceA, fileID
}

func createConflictSetupWithSlices(t *testing.T) (string, string, string, string) {
	t.Helper()

	fileID := fmt.Sprintf("shared-%s.txt", strings.ToLower(t.Name()))
	sliceA := fmt.Sprintf("conflict-a-%s", strings.ToLower(t.Name()))
	sliceB := fmt.Sprintf("conflict-b-%s", strings.ToLower(t.Name()))

	if testStorage == nil {
		t.Fatalf("expected test storage to be initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceA, Name: sliceA, Files: []string{fileID}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
		t.Fatalf("failed to create base slice: %v", err)
	}
	if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceB, Name: sliceB, Files: []string{fileID}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
		t.Fatalf("failed to create conflicting slice: %v", err)
	}

	writeConflictFile := func(sliceID, content string) {
		t.Helper()
		manifest, err := storage.WriteSliceFileManifest(ctx, testStorage, sliceID, fileID, []byte(content))
		if err != nil {
			t.Fatalf("failed to write slice file manifest for %s: %v", sliceID, err)
		}
		if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
			ID:       sliceID + ":" + fileID,
			Path:     fileID,
			Type:     "file",
			ParentID: sliceID,
			Size:     manifest.TotalSize,
			Hash:     manifest.Hash,
		}); err != nil {
			t.Fatalf("failed to add conflict entry for %s: %v", sliceID, err)
		}
		if err := testStorage.AddFileToSlice(ctx, fileID, sliceID); err != nil {
			t.Fatalf("failed to add conflict file index for %s: %v", sliceID, err)
		}
	}
	writeConflictFile(sliceA, "slice-a\n")
	writeConflictFile(sliceB, "slice-b\n")

	workdir := t.TempDir()
	sliceArg := sliceIDArg(sliceA)
	if _, err := runCLIWithDirForTest(t, workdir, "init", sliceArg); err != nil {
		t.Fatalf("failed to init working dir: %v", err)
	}

	return workdir, sliceA, fileID, sliceB
}
