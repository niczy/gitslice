package workflow

import (
	"fmt"
	"testing"
	"time"
)

func TestSliceEnsureIsIdempotent(t *testing.T) {
	rootWorkdir := t.TempDir()

	folderPath := fmt.Sprintf("ensure-%d", time.Now().UnixNano())
	seedWorkflowHomeFile(t, folderPath, "README.md", []byte("seed ensure folder\n"))

	sliceName := fmt.Sprintf("ensure-slice-%d", time.Now().UnixNano())
	first := runCLIJSONOrFail[sliceEnsureJSON](t, rootWorkdir, "slice", "ensure", sliceName, folderPath)
	second := runCLIJSONOrFail[sliceEnsureJSON](t, rootWorkdir, "slice", "ensure", sliceName, folderPath)
	if !first.Created || second.Created || first.SliceID == "" || first.SliceID != second.SliceID || first.Slug != second.Slug {
		t.Fatalf("expected idempotent slice ensure results, got first=%+v second=%+v", first, second)
	}
}
