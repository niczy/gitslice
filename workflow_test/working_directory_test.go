package workflow

import (
	"strings"
	"testing"
)

func TestStatusShowsSliceBinding(t *testing.T) {
	workdir := t.TempDir()
	slicePath := newSlicePath(t, "status-slice")

	_ = runCLIOrFail(t, workdir, "slice", "create", slicePath)
	_ = runCLIOrFail(t, workdir, "init", slicePath)

	output := runCLIOrFail(t, workdir, "status")
	if !strings.Contains(output, slicePath) {
		t.Fatalf("expected status to mention slice binding, got: %s", output)
	}
}
