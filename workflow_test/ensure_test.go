package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSliceEnsureIsIdempotent(t *testing.T) {
	rootWorkdir := t.TempDir()
	_ = runCLIOrFail(t, rootWorkdir, "init", sliceIDArg("root"))

	folderPath := fmt.Sprintf("ensure-%d", time.Now().UnixNano())
	filePath := folderPath + "/README.md"
	localPath := filepath.Join(rootWorkdir, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir ensure seed path: %v", err)
	}
	if err := os.WriteFile(localPath, []byte("seed ensure folder\n"), 0o644); err != nil {
		t.Fatalf("write ensure seed file: %v", err)
	}
	createResp := runCLIJSONOrFail[changesetCreateJSON](t, rootWorkdir, "changeset", "create", "--message", "seed ensure folder", "--files", filePath)
	mergeResp := runCLIJSONOrFail[mergeJSON](t, rootWorkdir, "changeset", "merge", createResp.ChangesetID)
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected root merge success, got: %+v", mergeResp)
	}

	sliceName := fmt.Sprintf("ensure-slice-%d", time.Now().UnixNano())
	first := runCLIJSONOrFail[sliceEnsureJSON](t, rootWorkdir, "slice", "ensure", sliceName, folderPath)
	second := runCLIJSONOrFail[sliceEnsureJSON](t, rootWorkdir, "slice", "ensure", sliceName, folderPath)
	if !first.Created || second.Created || first.SliceID == "" || first.SliceID != second.SliceID || first.Slug != second.Slug {
		t.Fatalf("expected idempotent slice ensure results, got first=%+v second=%+v", first, second)
	}
}
