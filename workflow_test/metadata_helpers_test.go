package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func writeSliceMetadataFile(t *testing.T, dir, sliceID string) string {
	t.Helper()

	path := filepath.Join(dir, fmt.Sprintf("%s.toml", sliceID))
	content := []byte(fmt.Sprintf("slice_id = %q\n", sliceID))
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("failed to write metadata file: %v", err)
	}
	return path
}

func createSliceFromRoot(t *testing.T, sliceID, folderPath string) {
	t.Helper()

	client := newSliceClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		FolderPath:    folderPath,
		NewSliceId:    sliceID,
		Name:          sliceID,
		Description:   "test slice",
	})
	if err != nil {
		t.Fatalf("failed to create slice %s: %v", sliceID, err)
	}
}
