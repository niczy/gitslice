package homeslice

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/storage"
)

func TestEnsureUserHomeSliceProvisionsRootAndHomeSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	slice, err := EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	if slice.ID != "home.alice" {
		t.Fatalf("unexpected slice id: %q", slice.ID)
	}

	if _, err := st.GetSlice(ctx, "home.alice"); err != nil {
		t.Fatalf("home slice not found: %v", err)
	}
	if _, err := st.GetEntryByPath(ctx, "home.alice", "alice"); err != nil {
		t.Fatalf("home slice directory not found: %v", err)
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if _, err := st.GetEntryByPath(ctx, rootSlice.ID, "alice"); err != nil {
		t.Fatalf("root slice directory not found: %v", err)
	}

	meta, err := st.GetSliceMetadata(ctx, "home.alice")
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	expectedHead := "init-home.alice"
	if meta.HeadCommitHash != expectedHead {
		t.Fatalf("unexpected head commit: %q", meta.HeadCommitHash)
	}
	if _, err := st.GetCommitByHash(ctx, "home.alice", expectedHead); err != nil {
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

	commits, err := st.ListSliceCommits(ctx, "home.alice", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits failed: %v", err)
	}
	if len(commits) != 1 {
		t.Fatalf("expected one initial commit, got %d", len(commits))
	}
	if commits[0].CommitHash != "init-home.alice" {
		t.Fatalf("unexpected commit hash: %q", commits[0].CommitHash)
	}
}
