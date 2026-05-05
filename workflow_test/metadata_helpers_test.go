package workflow

import (
	"context"
	"testing"
	"time"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func sliceIDArg(sliceID string) string {
	return sliceID
}

func createSliceFromRoot(t *testing.T, sliceID, folderPath string) {
	t.Helper()

	client := newSliceClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	_, err := client.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		FolderPaths:   []string{folderPath},
		NewSliceId:    sliceID,
		Name:          sliceID,
		Description:   "test slice",
	})
	if err != nil {
		t.Fatalf("failed to create slice %s: %v", sliceID, err)
	}
}
