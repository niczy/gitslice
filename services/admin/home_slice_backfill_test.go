package adminservice

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
)

func TestBackfillHomeSlicesCopiesExistingRootSubtree(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)

	if _, err := st.EnsureUser(ctx, "adminuser"); err != nil {
		t.Fatalf("EnsureUser(adminuser) failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "legacyuser",
		Name:         "Legacy User",
		PrimaryEmail: "legacy@example.com",
		PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser(legacyuser) failed: %v", err)
	}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("EnsureRootSliceInitialized failed: %v", err)
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	const filePath = "legacyuser/readme.md"
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  filePath,
		Path:    filePath,
		Content: []byte("legacy"),
		Size:    int64(len("legacy")),
		Hash:    "legacy-hash",
	}); err != nil {
		t.Fatalf("AddFileContent failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(rootSlice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: rootSlice.ID,
		Size:     int64(len("legacy")),
		Hash:     "legacy-hash",
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, rootSlice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	resp, err := svc.BackfillHomeSlices(withAdminUser(ctx, "adminuser"), &adminv1.BackfillHomeSlicesRequest{
		Usernames: []string{"legacyuser"},
	})
	if err != nil {
		t.Fatalf("BackfillHomeSlices failed: %v", err)
	}
	if resp.GetProcessed() != 1 || resp.GetCreated() != 1 || resp.GetSeeded() != 1 {
		t.Fatalf("unexpected response counts: %#v", resp)
	}
	if len(resp.GetResults()) != 1 {
		t.Fatalf("expected one result, got %#v", resp)
	}
	result := resp.GetResults()[0]
	if result.GetHomeSliceId() != "home.legacyuser" || result.GetFilesCopied() != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}

	copied, err := st.GetSliceFileByPath(ctx, "home.legacyuser", filePath)
	if err != nil {
		t.Fatalf("GetSliceFileByPath failed: %v", err)
	}
	if string(copied.Content) != "legacy" {
		t.Fatalf("unexpected copied content: %q", string(copied.Content))
	}

	second, err := svc.BackfillHomeSlices(withAdminUser(ctx, "adminuser"), &adminv1.BackfillHomeSlicesRequest{
		Usernames: []string{"legacyuser"},
	})
	if err != nil {
		t.Fatalf("second BackfillHomeSlices failed: %v", err)
	}
	if second.GetSeeded() != 0 || second.GetResults()[0].GetFilesCopied() != 0 {
		t.Fatalf("expected idempotent second backfill, got %#v", second)
	}
}
