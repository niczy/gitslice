package workflow

import (
	"strings"
	"testing"
)

func TestCommitLogAndShow(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "commit-history"

	_ = runCLIOrFail(t, workdir, "slice", "create", sliceID, "--description", "history slice")
	_ = runCLIOrFail(t, workdir, "init", sliceID)

	output := runCLIOrFail(t, workdir, "log", sliceID)
	if !strings.Contains(output, "Commit history") {
		t.Fatalf("expected log output to include commit history heading, got: %s", output)
	}
}
