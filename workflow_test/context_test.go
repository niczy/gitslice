package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContextJSONShowsCheckoutAndTrackedChangeset(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("context-slice-%d", time.Now().UnixNano())

	createSliceFromRoot(t, sliceID, "")
	_ = runCLIOrFail(t, workdir, "init", sliceIDArg(sliceID))

	contextResp := runCLIJSONOrFail[contextJSON](t, workdir, "context")
	if contextResp.Auth.Username == "" ||
		!contextResp.Checkout.Present ||
		contextResp.Checkout.SliceID != sliceID ||
		contextResp.Checkout.Mode != "no-git" ||
		contextResp.HomeSliceID == "" {
		t.Fatalf("unexpected initial context output: %+v", contextResp)
	}

	filePath := filepath.Join(workdir, "context.txt")
	if err := os.WriteFile(filePath, []byte("context change\n"), 0o644); err != nil {
		t.Fatalf("write context file: %v", err)
	}
	createResp := runCLIJSONOrFail[changesetCreateJSON](t, workdir, "changeset", "create", "--message", "context changes")
	if createResp.ChangesetID == "" {
		t.Fatalf("expected changeset create response, got: %+v", createResp)
	}

	contextResp = runCLIJSONOrFail[contextJSON](t, workdir, "context")
	if contextResp.Checkout.WorkingTree != "dirty" ||
		contextResp.TrackedChange.ChangesetID != createResp.ChangesetID ||
		!contextResp.TrackedChange.Present {
		t.Fatalf("unexpected dirty context output: %+v", contextResp)
	}
}

func TestContextJSONShowsNoCheckoutOutsideBoundDirectory(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir plain workdir: %v", err)
	}

	contextResp := runCLIJSONOrFail[contextJSON](t, workdir, "context")
	if contextResp.Auth.Username == "" || contextResp.Checkout.Present || contextResp.HomeSliceID == "" {
		t.Fatalf("unexpected context output outside checkout: %+v", contextResp)
	}
}
