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
	if err := st.AddFileToSlice(ctx, "file-2", slice.ID); err != nil {
		t.Fatalf("AddFileToSlice new file failed: %v", err)
	}
	afterAdd, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice after AddFileToSlice failed: %v", err)
	}
	if len(afterAdd.Files) != 1 || afterAdd.Files[0] != "file-1" {
		t.Fatalf("slice files should be immutable, got: %#v", afterAdd.Files)
	}
	slice2 := &models.Slice{ID: "slice-2", Name: "Beta", Description: "Second", Files: []string{"file-1"}, Owners: []string{"bob"}, CreatedBy: "bob"}
	if err := st.CreateSlice(ctx, slice2); err != nil {
		t.Fatalf("CreateSlice second failed: %v", err)
	}
	count, err := st.CountSlices(ctx)
	if err != nil {
		t.Fatalf("CountSlices failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 slices, got %d", count)
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

	emptySlice := &models.Slice{ID: "slice-empty", Name: "Empty", Description: "Empty", Files: []string{}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, emptySlice); err != nil {
		t.Fatalf("CreateSlice empty failed: %v", err)
	}
	if err := st.SetSliceFiles(ctx, emptySlice.ID, []string{"file-9"}); err != nil {
		t.Fatalf("SetSliceFiles failed: %v", err)
	}
	emptyFetched, err := st.GetSlice(ctx, emptySlice.ID)
	if err != nil {
		t.Fatalf("GetSlice after SetSliceFiles failed: %v", err)
	}
	if len(emptyFetched.Files) != 1 || emptyFetched.Files[0] != "file-9" {
		t.Fatalf("SetSliceFiles mismatch: %#v", emptyFetched.Files)
	}
	if err := st.SetSliceFiles(ctx, emptySlice.ID, []string{"file-10"}); err != ErrSliceFilesImmutable {
		t.Fatalf("expected ErrSliceFilesImmutable, got %v", err)
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

func TestFileChangeHistory(t *testing.T) {
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
				return NewRedisStorage(client, store, "test-history")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFileChangeHistoryTests(ctx, t, tc.factory(t))
		})
	}
}

func runFileChangeHistoryTests(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	// Setup: Create a slice first
	slice := &models.Slice{ID: "slice-history", Name: "History Test", Description: "For testing file history"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	baseTime := time.Now().Add(-time.Hour)

	// Test 1: AddFileChange and GetFileHistory
	t.Run("AddFileChange", func(t *testing.T) {
		change1 := &models.FileChangeRecord{
			ID:         "change-1",
			SliceID:    slice.ID,
			CommitHash: "commit-abc",
			Path:       "src/main.go",
			ChangeType: models.ChangeTypeAdd,
			NewHash:    "hash123",
			LinesAdded: 50,
			Author:     "alice",
			Message:    "Initial commit",
			Timestamp:  baseTime,
		}
		if err := st.AddFileChange(ctx, change1); err != nil {
			t.Fatalf("AddFileChange failed: %v", err)
		}

		change2 := &models.FileChangeRecord{
			ID:           "change-2",
			SliceID:      slice.ID,
			CommitHash:   "commit-def",
			Path:         "src/main.go",
			ChangeType:   models.ChangeTypeModify,
			OldHash:      "hash123",
			NewHash:      "hash456",
			LinesAdded:   10,
			LinesDeleted: 5,
			Author:       "bob",
			Message:      "Fix bug",
			Timestamp:    baseTime.Add(10 * time.Minute),
		}
		if err := st.AddFileChange(ctx, change2); err != nil {
			t.Fatalf("AddFileChange second failed: %v", err)
		}

		// Verify GetFileHistory returns changes in order (newest first)
		history, err := st.GetFileHistory(ctx, slice.ID, "src/main.go", 10, "")
		if err != nil {
			t.Fatalf("GetFileHistory failed: %v", err)
		}
		if len(history) != 2 {
			t.Fatalf("expected 2 changes, got %d", len(history))
		}
		// Newest first
		if history[0].ID != "change-2" {
			t.Errorf("expected newest change first, got %s", history[0].ID)
		}
		if history[1].ID != "change-1" {
			t.Errorf("expected oldest change second, got %s", history[1].ID)
		}
	})

	// Test 2: AddFileChanges batch
	t.Run("AddFileChanges batch", func(t *testing.T) {
		changes := []*models.FileChangeRecord{
			{
				ID:         "change-3",
				SliceID:    slice.ID,
				CommitHash: "commit-ghi",
				Path:       "src/utils/helper.go",
				ChangeType: models.ChangeTypeAdd,
				NewHash:    "hashutil1",
				Author:     "charlie",
				Message:    "Add helper",
				Timestamp:  baseTime.Add(20 * time.Minute),
			},
			{
				ID:         "change-4",
				SliceID:    slice.ID,
				CommitHash: "commit-ghi",
				Path:       "src/utils/config.go",
				ChangeType: models.ChangeTypeAdd,
				NewHash:    "hashutil2",
				Author:     "charlie",
				Message:    "Add helper",
				Timestamp:  baseTime.Add(20 * time.Minute),
			},
		}
		if err := st.AddFileChanges(ctx, changes); err != nil {
			t.Fatalf("AddFileChanges failed: %v", err)
		}

		// Verify both were added
		history1, _ := st.GetFileHistory(ctx, slice.ID, "src/utils/helper.go", 10, "")
		history2, _ := st.GetFileHistory(ctx, slice.ID, "src/utils/config.go", 10, "")
		if len(history1) != 1 || len(history2) != 1 {
			t.Errorf("expected 1 change each, got %d and %d", len(history1), len(history2))
		}
	})

	// Test 3: GetDirectoryHistory
	t.Run("GetDirectoryHistory", func(t *testing.T) {
		// Get all changes under src/utils/
		history, err := st.GetDirectoryHistory(ctx, slice.ID, "src/utils", 10, "")
		if err != nil {
			t.Fatalf("GetDirectoryHistory failed: %v", err)
		}
		if len(history) != 2 {
			t.Errorf("expected 2 changes under src/utils/, got %d", len(history))
		}

		// Get all changes under src/
		historyAll, err := st.GetDirectoryHistory(ctx, slice.ID, "src", 10, "")
		if err != nil {
			t.Fatalf("GetDirectoryHistory src/ failed: %v", err)
		}
		if len(historyAll) != 4 {
			t.Errorf("expected 4 changes under src/, got %d", len(historyAll))
		}
	})

	// Test 4: GetCommitChanges
	t.Run("GetCommitChanges", func(t *testing.T) {
		changes, err := st.GetCommitChanges(ctx, "commit-ghi")
		if err != nil {
			t.Fatalf("GetCommitChanges failed: %v", err)
		}
		if len(changes) != 2 {
			t.Errorf("expected 2 changes in commit-ghi, got %d", len(changes))
		}

		// Non-existent commit should return empty
		empty, err := st.GetCommitChanges(ctx, "nonexistent")
		if err != nil {
			t.Fatalf("GetCommitChanges nonexistent failed: %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("expected 0 changes for nonexistent commit, got %d", len(empty))
		}
	})

	// Test 5: Pagination with fromCommit
	t.Run("Pagination", func(t *testing.T) {
		// Get first page
		page1, err := st.GetFileHistory(ctx, slice.ID, "src/main.go", 1, "")
		if err != nil {
			t.Fatalf("GetFileHistory page1 failed: %v", err)
		}
		if len(page1) != 1 {
			t.Fatalf("expected 1 change in page1, got %d", len(page1))
		}

		// Get second page using fromCommit
		page2, err := st.GetFileHistory(ctx, slice.ID, "src/main.go", 1, page1[0].CommitHash)
		if err != nil {
			t.Fatalf("GetFileHistory page2 failed: %v", err)
		}
		if len(page2) != 1 {
			t.Fatalf("expected 1 change in page2, got %d", len(page2))
		}
		if page2[0].ID == page1[0].ID {
			t.Error("page2 should have different change than page1")
		}
	})

	// Test 6: QueryFileHistory with filters
	t.Run("QueryFileHistory", func(t *testing.T) {
		// Query by author
		result, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID: slice.ID,
			Author:  "alice",
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by author failed: %v", err)
		}
		if len(result.Changes) != 1 {
			t.Errorf("expected 1 change by alice, got %d", len(result.Changes))
		}

		// Query by change type
		result2, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID:     slice.ID,
			ChangeTypes: []models.ChangeType{models.ChangeTypeAdd},
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by type failed: %v", err)
		}
		if len(result2.Changes) != 3 {
			t.Errorf("expected 3 add changes, got %d", len(result2.Changes))
		}

		// Query by path prefix
		result3, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID:    slice.ID,
			PathPrefix: "src/utils",
			Limit:      10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by prefix failed: %v", err)
		}
		if len(result3.Changes) != 2 {
			t.Errorf("expected 2 changes under src/utils, got %d", len(result3.Changes))
		}

		// Query with time filter
		midTime := baseTime.Add(15 * time.Minute)
		result4, err := st.QueryFileHistory(ctx, &models.FileHistoryQuery{
			SliceID:       slice.ID,
			FromTimestamp: &midTime,
			Limit:         10,
		})
		if err != nil {
			t.Fatalf("QueryFileHistory by time failed: %v", err)
		}
		if len(result4.Changes) != 2 {
			t.Errorf("expected 2 changes after midTime, got %d", len(result4.Changes))
		}
	})

	// Test 7: GetDirectorySummary
	t.Run("GetDirectorySummary", func(t *testing.T) {
		summary, err := st.GetDirectorySummary(ctx, slice.ID, "src")
		if err != nil {
			t.Fatalf("GetDirectorySummary failed: %v", err)
		}
		if summary.TotalChanges != 4 {
			t.Errorf("expected 4 total changes, got %d", summary.TotalChanges)
		}
		if summary.FilesChanged != 3 {
			t.Errorf("expected 3 unique files, got %d", summary.FilesChanged)
		}
		if summary.LastChange == nil {
			t.Error("expected LastChange to be set")
		}
		if summary.ChangesByType[models.ChangeTypeAdd] != 3 {
			t.Errorf("expected 3 add changes, got %d", summary.ChangesByType[models.ChangeTypeAdd])
		}
		if summary.ChangesByType[models.ChangeTypeModify] != 1 {
			t.Errorf("expected 1 modify change, got %d", summary.ChangesByType[models.ChangeTypeModify])
		}
	})

	// Test 8: Empty results
	t.Run("EmptyResults", func(t *testing.T) {
		history, err := st.GetFileHistory(ctx, slice.ID, "nonexistent/path.go", 10, "")
		if err != nil {
			t.Fatalf("GetFileHistory nonexistent failed: %v", err)
		}
		if len(history) != 0 {
			t.Errorf("expected 0 changes for nonexistent path, got %d", len(history))
		}

		summary, err := st.GetDirectorySummary(ctx, slice.ID, "nonexistent")
		if err != nil {
			t.Fatalf("GetDirectorySummary nonexistent failed: %v", err)
		}
		if summary.TotalChanges != 0 {
			t.Errorf("expected 0 changes for nonexistent dir, got %d", summary.TotalChanges)
		}
	})

	// Test 9: Invalid input
	t.Run("InvalidInput", func(t *testing.T) {
		invalidChange := &models.FileChangeRecord{
			ID:   "", // Missing ID
			Path: "test.go",
		}
		if err := st.AddFileChange(ctx, invalidChange); err != ErrInvalidInput {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}

		invalidChange2 := &models.FileChangeRecord{
			ID:   "valid-id",
			Path: "", // Missing path
		}
		if err := st.AddFileChange(ctx, invalidChange2); err != ErrInvalidInput {
			t.Errorf("expected ErrInvalidInput for missing path, got %v", err)
		}
	})
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
