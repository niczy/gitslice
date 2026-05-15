package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSlicePublishReturnsJSONErrorWhenClean(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("agent-error-clean-%d", time.Now().UnixNano())

	createSliceFromRoot(t, sliceID, "")
	_ = runCLIOrFail(t, workdir, "init", sliceIDArg(sliceID))

	errResp := runCLIJSONErrorOrFail[cliErrorJSON](t, workdir, "slice", "publish", "--review-only")
	if errResp.ErrorCode != "NO_LOCAL_CHANGES" || errResp.SuggestedAction == "" {
		t.Fatalf("expected structured no-local-changes error, got: %+v", errResp)
	}
}

func TestSliceStatusReturnsJSONErrorOutsideCheckout(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir workdir: %v", err)
	}

	errResp := runCLIJSONErrorOrFail[cliErrorJSON](t, workdir, "slice", "status")
	if errResp.ErrorCode != "SLICE_NOT_BOUND" {
		t.Fatalf("expected slice-not-bound error, got: %+v", errResp)
	}
}
