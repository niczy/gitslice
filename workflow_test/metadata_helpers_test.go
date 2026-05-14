package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
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

	if strings.TrimSpace(folderPath) == "" {
		if testStorage == nil {
			t.Fatal("expected test storage to be initialized")
		}
		if err := testStorage.CreateSlice(ctx, &models.Slice{
			ID:          sliceID,
			Name:        sliceID,
			Description: "test slice",
			Visibility:  models.VisibilityPrivate,
			Owners:      []string{workflowUsername(t)},
			CreatedBy:   workflowUsername(t),
		}); err != nil {
			t.Fatalf("failed to create empty slice %s: %v", sliceID, err)
		}
		return
	}

	_, err := client.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
		FolderPaths:   []string{folderPath},
		NewSliceId:    sliceID,
		Name:          sliceID,
		Description:   "test slice",
	})
	if err != nil {
		t.Fatalf("failed to create slice %s: %v", sliceID, err)
	}
}
