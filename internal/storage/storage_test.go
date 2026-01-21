package storage

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/niczy/gitslice/internal/models"
	"github.com/redis/go-redis/v9"
)

func TestStorageCompliance(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		factory func(t *testing.T) Storage
	}{
		{
			name: "in-memory",
			factory: func(t *testing.T) Storage {
				t.Helper()
				return NewInMemoryStorage()
			},
		},
		{
			name: "redis",
			factory: func(t *testing.T) Storage {
				t.Helper()
				mr := miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				store := NewInMemoryObjectStore()
				t.Cleanup(func() {
					_ = client.Close()
					mr.Close()
				})
				return NewRedisStorage(client, store, "test")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runStorageContract(ctx, t, tc.factory(t))
		})
	}
}

func runStorageContract(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	// Create primary slice
	slice := &models.Slice{ID: "slice-1", Name: "Alpha", Description: "First", Files: []string{"file-1"}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	// Verify retrieval
	fetched, err := st.GetSlice(ctx, slice.ID)
	if err != nil || fetched.ID != slice.ID {
		t.Fatalf("GetSlice mismatch: %v", err)
	}

	// Metadata round trip
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = "commit-1"
	meta.ModifiedFiles = []string{"file-1"}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	// Commit history
	commit := &models.Commit{CommitHash: "commit-1", ParentHash: "", Message: "init", Timestamp: time.Now()}
	if err := st.AddSliceCommit(ctx, slice.ID, commit); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	commits, err := st.ListSliceCommits(ctx, slice.ID, 10, "")
	if err != nil || len(commits) != 1 || commits[0].CommitHash != commit.CommitHash {
		t.Fatalf("ListSliceCommits mismatch: %v len=%d", err, len(commits))
	}

	// File indexing and conflicts
	if err := st.AddFileToSlice(ctx, "file-1", slice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	slice2 := &models.Slice{ID: "slice-2", Name: "Beta", Description: "Second", Files: []string{"file-1"}, Owners: []string{"bob"}, CreatedBy: "bob"}
	if err := st.CreateSlice(ctx, slice2); err != nil {
		t.Fatalf("CreateSlice second failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "file-1", slice2.ID); err != nil {
		t.Fatalf("AddFileToSlice second failed: %v", err)
	}

	conflicts, err := st.ListConflicts(ctx)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("ListConflicts unexpected: %v len=%d", err, len(conflicts))
	}
	resolved, err := st.ResolveConflict(ctx, "file-1", slice.ID)
	if err != nil {
		t.Fatalf("ResolveConflict failed: %v", err)
	}
	if len(resolved.ConflictingSlices) != 1 || resolved.ConflictingSlices[0] != slice.ID {
		t.Fatalf("ResolveConflict result mismatch: %+v", resolved)
	}

	// Locking
	if err := st.LockSliceAndFiles(ctx, slice.ID, []string{"file-1"}); err != nil {
		t.Fatalf("LockSliceAndFiles failed: %v", err)
	}
	if err := st.LockSliceAndFiles(ctx, slice2.ID, []string{"file-1"}); err != ErrLockHeld {
		t.Fatalf("expected ErrLockHeld, got %v", err)
	}
	st.UnlockSliceAndFiles(ctx, slice.ID, []string{"file-1"})
	if err := st.LockSliceAndFiles(ctx, slice2.ID, []string{"file-1"}); err != nil {
		t.Fatalf("Lock after unlock failed: %v", err)
	}
	st.UnlockSliceAndFiles(ctx, slice2.ID, []string{"file-1"})

	// Changesets
	cs := &models.Changeset{ID: "cs-1", Hash: "h1", SliceID: slice.ID, ModifiedFiles: []string{"file-1"}, Status: models.ChangesetStatusPending, Author: "alice", Message: "msg", CreatedAt: time.Now()}
	if err := st.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	fetchedCS, err := st.GetChangeset(ctx, cs.ID)
	if err != nil || fetchedCS.ID != cs.ID {
		t.Fatalf("GetChangeset mismatch: %v", err)
	}
	pending := models.ChangesetStatusPending
	listed, err := st.ListChangesets(ctx, slice.ID, &pending, 5)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListChangesets unexpected: %v len=%d", err, len(listed))
	}
	cs.Status = models.ChangesetStatusMerged
	if err := st.UpdateChangeset(ctx, cs); err != nil {
		t.Fatalf("UpdateChangeset failed: %v", err)
	}

	// Entries
	entry := &models.DirectoryEntry{ID: "entry-1", Path: "app/main.go", Type: "file", ParentID: slice.ID, Content: []byte("code"), Size: 4}
	if err := st.AddEntry(ctx, entry); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	gotEntry, err := st.GetEntry(ctx, entry.ID)
	if err != nil || gotEntry.Path != entry.Path {
		t.Fatalf("GetEntry mismatch: %v", err)
	}
	byPath, err := st.GetEntryByPath(ctx, slice.ID, entry.Path)
	if err != nil || byPath.ID != entry.ID {
		t.Fatalf("GetEntryByPath mismatch: %v", err)
	}
	entries, err := st.ListEntries(ctx, slice.ID, slice.ID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListEntries unexpected: %v len=%d", err, len(entries))
	}
	entry.Size = 8
	if err := st.UpdateEntry(ctx, entry); err != nil {
		t.Fatalf("UpdateEntry failed: %v", err)
	}
	if err := st.DeleteEntry(ctx, entry.ID); err != nil {
		t.Fatalf("DeleteEntry failed: %v", err)
	}

	// Global state
	state := &models.GlobalState{GlobalCommitHash: "global-1", Timestamp: time.Now(), History: []*models.GlobalCommit{{CommitHash: "global-1", Timestamp: time.Now()}}}
	if err := st.UpdateGlobalState(ctx, state); err != nil {
		t.Fatalf("UpdateGlobalState failed: %v", err)
	}
	storedState, err := st.GetGlobalState(ctx)
	if err != nil || storedState.GlobalCommitHash != state.GlobalCommitHash {
		t.Fatalf("GetGlobalState mismatch: %v", err)
	}

	// Root slice init
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	if _, err := st.GetRootSlice(ctx); err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}

	// Basic health
	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestRedisStorageRebuildIndexes(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	store := NewInMemoryObjectStore()
	rs := NewRedisStorage(client, store, "rebuild")
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})

	slice1 := &models.Slice{ID: "slice-1", Name: "Alpha", Files: []string{"file-1"}}
	slice2 := &models.Slice{ID: "slice-2", Name: "Beta", Files: []string{"file-1", "file-2"}}
	if err := rs.CreateSlice(ctx, slice1); err != nil {
		t.Fatalf("CreateSlice 1 failed: %v", err)
	}
	if err := rs.CreateSlice(ctx, slice2); err != nil {
		t.Fatalf("CreateSlice 2 failed: %v", err)
	}

	cs := &models.Changeset{ID: "cs-rebuild", Hash: "h", SliceID: slice1.ID, ModifiedFiles: []string{"file-1"}, Status: models.ChangesetStatusPending}
	if err := rs.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	entry := &models.DirectoryEntry{ID: "entry-1", Path: "app/main.go", Type: "file", ParentID: slice1.ID, Content: []byte("hi"), Size: 2}
	if err := rs.AddEntry(ctx, entry); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := rs.UpdateGlobalState(ctx, &models.GlobalState{GlobalCommitHash: "gc1", Timestamp: time.Now()}); err != nil {
		t.Fatalf("UpdateGlobalState failed: %v", err)
	}

	mr.FlushAll()

	if err := rs.RebuildIndexes(ctx); err != nil {
		t.Fatalf("RebuildIndexes failed: %v", err)
	}

	slices, err := rs.ListSlices(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListSlices failed: %v", err)
	}
	if len(slices) != 2 {
		t.Fatalf("expected 2 slices after rebuild, got %d", len(slices))
	}

	mapped, err := rs.GetActiveSlicesForFile(ctx, "file-1")
	if err != nil {
		t.Fatalf("GetActiveSlicesForFile failed: %v", err)
	}
	if len(mapped) != 2 {
		t.Fatalf("expected file-1 to map to 2 slices after rebuild, got %d", len(mapped))
	}

	restoredCS, err := rs.GetChangeset(ctx, cs.ID)
	if err != nil || restoredCS.ID != cs.ID {
		t.Fatalf("expected changeset restored after rebuild: %v", err)
	}
	restoredEntry, err := rs.GetEntry(ctx, entry.ID)
	if err != nil || restoredEntry.Path != entry.Path {
		t.Fatalf("expected entry restored after rebuild: %v", err)
	}
	restoredState, err := rs.GetGlobalState(ctx)
	if err != nil || restoredState.GlobalCommitHash != "gc1" {
		t.Fatalf("expected global state restored, got %#v err=%v", restoredState, err)
	}
}
