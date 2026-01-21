package workflow

import (
	"strings"
	"testing"
)

func TestStatusShowsSliceBinding(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "status-slice"

	_ = runCLIOrFail(t, workdir, "slice", "create", sliceID)
	_ = runCLIOrFail(t, workdir, "init", sliceID)

	output := runCLIOrFail(t, workdir, "status")
	if !strings.Contains(output, sliceID) {
		t.Fatalf("expected status to mention slice binding, got: %s", output)
	}
}
