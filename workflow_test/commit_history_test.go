package workflow

import (
	"strings"
	"testing"
)

func TestCommitLogAndShow(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "commit-history"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)
	_ = runCLIOrFail(t, workdir, "init", sliceArg)

	output := runCLIOrFail(t, workdir, "log", sliceArg)
	if !strings.Contains(output, "Commit history") {
		t.Fatalf("expected log output to include commit history heading, got: %s", output)
	}
}
