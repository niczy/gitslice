package workflow

import (
	"strings"
	"testing"
)

// TestChangesetWorkflow exercises basic changeset lifecycle using the CLI against
// the in-memory services bootstrapped in TestMain.
func TestChangesetWorkflow(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "changeset-workflow"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)

	output := runCLIOrFail(t, workdir, "init", metadataPath)
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("expected init output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "test change", "--files", "a.txt,b.txt")
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("expected changeset ID in output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "review", changesetID)
	if !strings.Contains(output, changesetID) {
		t.Fatalf("expected review output to mention changeset, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "Merge status") {
		t.Fatalf("expected merge status, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "list", "--status", "merged")
	if !strings.Contains(output, changesetID) {
		t.Fatalf("expected merged changeset in list, got: %s", output)
	}
}
