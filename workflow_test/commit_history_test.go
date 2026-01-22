package workflow

import (
	"strings"
	"testing"
)

func TestCommitLogAndShow(t *testing.T) {
	workdir := t.TempDir()
	slicePath := newSlicePath(t, "commit-history")

	_ = runCLIOrFail(t, workdir, "slice", "create", slicePath, "--description", "history slice")
	_ = runCLIOrFail(t, workdir, "init", slicePath)

	output := runCLIOrFail(t, workdir, "log", slicePath)
	if !strings.Contains(output, "Commit history") {
		t.Fatalf("expected log output to include commit history heading, got: %s", output)
	}
}
