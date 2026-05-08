package homeslice

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func mustWriteSliceManifest(tb testing.TB, ctx context.Context, st storage.Storage, sliceID, filePath string, content []byte) string {
	tb.Helper()
	manifest, err := storage.WriteSliceFileManifest(ctx, st, sliceID, filePath, content)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	return manifest.Hash
}

func TestEnsureUserHomeSliceProvisionsRootAndHomeSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	slice, err := EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	if slice.ID != "home_alice" {
		t.Fatalf("unexpected slice id: %q", slice.ID)
	}

	if _, err := st.GetSlice(ctx, "home_alice"); err != nil {
		t.Fatalf("home slice not found: %v", err)
	}
	if _, err := st.GetEntryByPath(ctx, "home_alice", "alice"); err != nil {
		t.Fatalf("home slice directory not found: %v", err)
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if _, err := st.GetEntryByPath(ctx, rootSlice.ID, "alice"); err != nil {
		t.Fatalf("root slice directory not found: %v", err)
	}

	meta, err := st.GetSliceMetadata(ctx, "home_alice")
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	expectedHead := common.GenerateInitialCommitID("home_alice")
	if meta.HeadCommitHash != expectedHead {
		t.Fatalf("unexpected head commit: %q", meta.HeadCommitHash)
	}
	if _, err := st.GetCommitByHash(ctx, "home_alice", expectedHead); err != nil {
		t.Fatalf("initial commit not found: %v", err)
	}
	if snapshot, err := st.GetCommitSnapshot(ctx, expectedHead); err != nil {
		t.Fatalf("initial snapshot not found: %v", err)
	} else if len(snapshot.Files) != 0 {
		t.Fatalf("expected empty initial snapshot, got %#v", snapshot.Files)
	}
}

func TestEnsureUserHomeSliceIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	if _, err := EnsureUserHomeSlice(ctx, st, "alice"); err != nil {
		t.Fatalf("first EnsureUserHomeSlice failed: %v", err)
	}
	if _, err := EnsureUserHomeSlice(ctx, st, "alice"); err != nil {
		t.Fatalf("second EnsureUserHomeSlice failed: %v", err)
	}

	commits, err := st.ListSliceCommits(ctx, "home_alice", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits failed: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected one initial commit, got %d", len(commits))
	}
	if commits[0].CommitHash != common.GenerateInitialCommitID("home_alice") {
		t.Fatalf("unexpected commit hash: %q", commits[0].CommitHash)
	}
}

func TestBackfillUserHomeSliceCopiesRootFilesOnce(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("EnsureRootSliceInitialized failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "legacyuser",
		Name:         "Legacy User",
		PrimaryEmail: "legacy@example.com",
		PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	const filePath = "legacyuser/readme.md"
	hash := mustWriteSliceManifest(t, ctx, st, rootSlice.ID, filePath, []byte("legacy"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(rootSlice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: rootSlice.ID,
		Size:     int64(len("legacy")),
		Hash:     hash,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, rootSlice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	result, err := BackfillUserHomeSlice(ctx, st, "legacyuser")
	if err != nil {
		t.Fatalf("BackfillUserHomeSlice failed: %v", err)
	}
	if !result.Created || !result.Seeded || result.FilesCopied != 1 {
		t.Fatalf("unexpected backfill result: %#v", result)
	}

	copied, err := storage.ReadSliceFileContent(ctx, st, "home_legacyuser", filePath)
	if err != nil {
		t.Fatalf("ReadSliceFileContent failed: %v", err)
	}
	if string(copied.Content) != "legacy" {
		t.Fatalf("unexpected copied content: %q", string(copied.Content))
	}

	commits, err := st.ListSliceCommits(ctx, "home_legacyuser", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits failed: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected initial + backfill commits, got %d", len(commits))
	}

	second, err := BackfillUserHomeSlice(ctx, st, "legacyuser")
	if err != nil {
		t.Fatalf("second BackfillUserHomeSlice failed: %v", err)
	}
	if second.Seeded || second.FilesCopied != 0 || second.DirectoriesCopied != 0 {
		t.Fatalf("expected idempotent second backfill, got %#v", second)
	}
}
