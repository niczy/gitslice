package adminservice

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
)

func mustWriteSliceManifest(tb testing.TB, ctx context.Context, st storage.Storage, sliceID, filePath string, content []byte) string {
	tb.Helper()
	manifest, err := storage.WriteSliceFileManifest(ctx, st, sliceID, filePath, content)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	return manifest.Hash
}

func TestBackfillHomeSlicesUsesPathHeadRootView(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	t.Setenv("AUTH_SECRET", "test-auth-secret")
	t.Setenv("ADMIN_USER_EMAILS", "admin@example.com")

	if err := st.CreateUser(ctx, &models.User{
		Username:     "adminuser",
		Name:         "Admin User",
		PrimaryEmail: "admin@example.com",
		ClerkUserID:  "user_admin",
		PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser(adminuser) failed: %v", err)
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

	const filePath = "legacyuser/readme.md"
	source := &models.Slice{ID: "source-legacyuser", Name: "source-legacyuser", Owners: []string{"legacyuser"}, CreatedBy: "legacyuser"}
	if err := st.CreateSlice(ctx, source); err != nil {
		t.Fatalf("CreateSlice(source) failed: %v", err)
	}
	hash := mustWriteSliceManifest(t, ctx, st, source.ID, filePath, []byte("legacy"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(source.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: source.ID,
		Size:     int64(len("legacy")),
		Hash:     hash,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "legacyuser",
		Path:             filePath,
		EntryType:        "file",
		PathVersion:      1,
		SourceSliceID:    source.ID,
		SourceCommitHash: "source-commit",
		ManifestHash:     hash,
		ContentHash:      hash,
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	adminCtx := withClerkAdminSession(t, ctx, "user_admin", "admin@example.com")
	resp, err := svc.BackfillHomeSlices(adminCtx, &adminv1.BackfillHomeSlicesRequest{
		Usernames: []string{"legacyuser"},
	})
	if err != nil {
		t.Fatalf("BackfillHomeSlices failed: %v", err)
	}
	if resp.GetProcessed() != 1 || resp.GetCreated() != 1 || resp.GetSeeded() != 0 {
		t.Fatalf("unexpected response counts: %#v", resp)
	}
	if len(resp.GetResults()) != 1 {
		t.Fatalf("expected one result, got %#v", resp)
	}
	result := resp.GetResults()[0]
	if result.GetHomeSliceId() != "home_legacyuser" || result.GetFilesCopied() != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}

	copied, err := storage.ReadSliceFileContent(ctx, st, "home_legacyuser", filePath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent failed: %v", err)
	}
	if string(copied.Content) != "legacy" {
		t.Fatalf("unexpected copied content: %q", string(copied.Content))
	}

	second, err := svc.BackfillHomeSlices(adminCtx, &adminv1.BackfillHomeSlicesRequest{
		Usernames: []string{"legacyuser"},
	})
	if err != nil {
		t.Fatalf("second BackfillHomeSlices failed: %v", err)
	}
	if second.GetSeeded() != 0 || second.GetResults()[0].GetFilesCopied() != 0 {
		t.Fatalf("expected idempotent second backfill, got %#v", second)
	}
}
