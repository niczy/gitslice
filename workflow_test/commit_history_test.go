package workflow

import (
	"strings"
	"testing"
)

func TestCommitLogAndShow(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "commit-history"

	createSliceFromRoot(t, sliceID, "")
	metadataPath := writeSliceMetadataFile(t, t.TempDir(), sliceID)
	_ = runCLIOrFail(t, workdir, "init", metadataPath)

	output := runCLIOrFail(t, workdir, "log", metadataPath)
	if !strings.Contains(output, "Commit history") {
		t.Fatalf("expected log output to include commit history heading, got: %s", output)
	}
}
