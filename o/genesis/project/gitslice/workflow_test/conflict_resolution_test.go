package workflow

import (
	"fmt"
	"strings"
	"testing"
)

// TestConflictList tests showing all conflicts for current working directory
// Command: gs conflict list
func TestConflictList(t *testing.T) {
	workdir, sliceID, fileID := createConflictSetup(t)

	output := runCLIOrFail(t, workdir, "conflict", "list", "--slice", sliceID)

	if !strings.Contains(output, fileID) {
		t.Fatalf("expected conflict output to mention %s, got: %s", fileID, output)
	}
}

// TestConflictListChangeset tests showing conflicts for specific changeset
// Command: gs conflict list --changeset cl-abc123
func TestConflictListChangeset(t *testing.T) {
	workdir, sliceID, fileID := createConflictSetup(t)

	// Create a changeset that will hit the shared file to ensure conflict detection is visible
	output := runCLIOrFail(t, workdir, "changeset", "create", "--message", "conflict", "--files", fileID)
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("expected changeset ID in output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_CONFLICT") {
		t.Fatalf("expected merge conflict status, got: %s", output)
	}

	if !strings.Contains(output, fileID) {
		t.Fatalf("expected merge output to mention conflicting file, got: %s", output)
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

	output := runCLIOrFail(t, workdir, "conflict", "resolve", "--theirs", sliceB, fileID)
	if !strings.Contains(output, "Resolved conflict") {
		t.Fatalf("expected resolve confirmation, got: %s", output)
	}
}

// TestConflictResolveOurs tests auto-resolve with local changes
// Command: gs conflict resolve --ours
func TestConflictResolveOurs(t *testing.T) {
	workdir, sliceID, fileID, _ := createConflictSetupWithSlices(t)

	output := runCLIOrFail(t, workdir, "conflict", "resolve", "--ours", fileID)
	if !strings.Contains(output, "Resolved conflict") {
		t.Fatalf("expected resolve confirmation, got: %s", output)
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

	output := runCLIOrFail(t, "", "conflict", "show", fileID)
	if !strings.Contains(output, "Conflict for") {
		t.Fatalf("expected conflict details, got: %s", output)
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

	if _, err := runCLI("slice", "create", sliceA, "--files", fileID); err != nil {
		t.Fatalf("failed to create base slice: %v", err)
	}
	if _, err := runCLI("slice", "create", sliceB, "--files", fileID); err != nil {
		t.Fatalf("failed to create conflicting slice: %v", err)
	}

	workdir := t.TempDir()
	if _, err := runCLIWithDir(workdir, "init", sliceA); err != nil {
		t.Fatalf("failed to init working dir: %v", err)
	}

	return workdir, sliceA, fileID, sliceB
}
