package workflow

import (
	"strings"
	"testing"
)

func TestStatusShowsSliceBinding(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "status-slice"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)
	_ = runCLIOrFail(t, workdir, "init", sliceArg)

	output := runCLIOrFail(t, workdir, "status")
	if !strings.Contains(output, sliceID) {
		t.Fatalf("expected status to mention slice binding, got: %s", output)
	}
}
