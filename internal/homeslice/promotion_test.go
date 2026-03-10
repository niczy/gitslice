package homeslice

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestSyncHomeSliceToRootMirrorsSubtree(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root: %v", err)
	}
	home, err := EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("ensure home: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("get root: %v", err)
	}

	writeHomeFile(t, ctx, st, home.ID, "alice/docs/readme.md", "home v1\n")
	writeHomeFile(t, ctx, st, home.ID, "alice/src/main.py", "print('hi')\n")
	if _, err := ensureDirectory(ctx, st, home.ID, "alice/docs/guides"); err != nil {
		t.Fatalf("ensure dir: %v", err)
	}

	if _, err := SyncHomeSliceToRoot(ctx, st, home.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}

	rootReadme, err := st.GetSliceFileByPath(ctx, root.ID, "alice/docs/readme.md")
	if err != nil {
		t.Fatalf("root readme: %v", err)
	}
	if got := string(rootReadme.Content); got != "home v1\n" {
		t.Fatalf("unexpected root content: %q", got)
	}

	if err := st.DeleteEntry(ctx, common.GenerateEntryID(home.ID, "alice/src/main.py")); err != nil {
		t.Fatalf("delete home entry: %v", err)
	}
	if err := st.RemoveFileFromSlice(ctx, "alice/src/main.py", home.ID); err != nil {
		t.Fatalf("remove home file from slice: %v", err)
	}
	writeHomeFile(t, ctx, st, home.ID, "alice/docs/readme.md", "home v2\n")

	if _, err := SyncHomeSliceToRoot(ctx, st, home.ID); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	rootReadme, err = st.GetSliceFileByPath(ctx, root.ID, "alice/docs/readme.md")
	if err != nil {
		t.Fatalf("updated root readme: %v", err)
	}
	if got := string(rootReadme.Content); got != "home v2\n" {
		t.Fatalf("unexpected updated root content: %q", got)
	}
	if _, err := st.GetEntryByPath(ctx, root.ID, "alice/src/main.py"); err != storage.ErrEntryNotFound {
		t.Fatalf("expected root file removal, got %v", err)
	}
}

func writeHomeFile(t *testing.T, ctx context.Context, st storage.Storage, sliceID, filePath, content string) {
	t.Helper()

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("add entry %s: %v", filePath, err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  filePath,
		Path:    filePath,
		Content: []byte(content),
		Size:    int64(len(content)),
		Hash:    "",
	}); err != nil {
		t.Fatalf("add content %s: %v", filePath, err)
	}
	if err := st.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("add file to slice %s: %v", filePath, err)
	}
}
