package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func TestSliceSearchFindsBaseAndOverlayMatches(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	sliceID := "slice-search-overlay"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}

	filePath := "docs/readme.md"
	entry := &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len("alpha base text\n")),
	}
	if err := testStorage.AddEntry(ctx, entry); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	manifestHash := mustWriteSliceManifest(t, ctx, testStorage, sliceID, filePath, []byte("alpha base text\n"))
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("add file to slice: %v", err)
	}
	setWorkflowSliceHead(t, ctx, sliceID, "slice-search-overlay-1", "", map[string]string{filePath: manifestHash})

	workdir := t.TempDir()
	runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceIDArg(sliceID))

	baseResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "alpha")
	if baseResp.Total != 1 || len(baseResp.Matches) != 1 || baseResp.Matches[0].Path != filePath {
		t.Fatalf("expected base search hit, got %+v", baseResp)
	}

	if err := os.WriteFile(filepath.Join(workdir, filePath), []byte("overlay only text\n"), 0o644); err != nil {
		t.Fatalf("write overlay file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "docs", "new.txt"), []byte("new needle text\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	regexResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "over.*only", "--regex")
	if !regexResp.Regex || regexResp.Total != 1 || regexResp.Matches[0].Path != filePath {
		t.Fatalf("expected regex overlay hit, got %+v", regexResp)
	}

	newResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "needle", "--glob", "docs/*.txt")
	if newResp.Total != 1 || newResp.Matches[0].Path != "docs/new.txt" {
		t.Fatalf("expected new file search hit, got %+v", newResp)
	}

	if err := os.Remove(filepath.Join(workdir, filePath)); err != nil {
		t.Fatalf("remove readme: %v", err)
	}
	deletedResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "alpha")
	if deletedResp.Total != 0 {
		t.Fatalf("expected deleted base file to disappear from search, got %+v", deletedResp)
	}
}

func TestSliceSearchRefreshesAfterRestoreAndSync(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	sliceID := "slice-search-restore-sync"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}

	filePath := "docs/readme.md"
	entry := &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len("sync base text\n")),
	}
	if err := testStorage.AddEntry(ctx, entry); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	initialHash := mustWriteSliceManifest(t, ctx, testStorage, sliceID, filePath, []byte("sync base text\n"))
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("add file to slice: %v", err)
	}
	setWorkflowSliceHead(t, ctx, sliceID, "slice-search-sync-1", "", map[string]string{filePath: initialHash})

	workdir := t.TempDir()
	runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceIDArg(sliceID))

	if err := os.WriteFile(filepath.Join(workdir, filePath), []byte("local transient change\n"), 0o644); err != nil {
		t.Fatalf("write local change: %v", err)
	}
	localResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "transient")
	if localResp.Total != 1 {
		t.Fatalf("expected local overlay search hit, got %+v", localResp)
	}

	runCLIOrFail(t, workdir, "slice", "restore")
	restoredResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "transient")
	if restoredResp.Total != 0 {
		t.Fatalf("expected restore to clear local overlay hit, got %+v", restoredResp)
	}

	entry.Size = int64(len("remote fresh text\n"))
	if err := testStorage.UpdateEntry(ctx, entry); err != nil {
		t.Fatalf("update entry: %v", err)
	}
	updatedHash := mustWriteSliceManifest(t, ctx, testStorage, sliceID, filePath, []byte("remote fresh text\n"))
	setWorkflowSliceHead(t, ctx, sliceID, "slice-search-sync-2", "slice-search-sync-1", map[string]string{filePath: updatedHash})
	time.Sleep(10 * time.Millisecond)

	syncResp := runCLIJSONOrFail[sliceSyncJSON](t, workdir, "slice", "sync")
	if syncResp.Commit != "slice-search-sync-2" {
		t.Fatalf("expected sync to advance to new commit, got %+v", syncResp)
	}
	remoteResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "remote fresh")
	if remoteResp.Total != 1 || remoteResp.Matches[0].Path != filePath {
		t.Fatalf("expected synced search hit, got %+v", remoteResp)
	}
	oldResp := runCLIJSONOrFail[sliceSearchJSON](t, workdir, "slice", "search", "sync base")
	if oldResp.Total != 0 {
		t.Fatalf("expected old base text to disappear after sync, got %+v", oldResp)
	}
}
