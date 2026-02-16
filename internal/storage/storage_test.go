package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
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
	}
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		cases = append(cases, struct {
			name    string
			factory func(t *testing.T) Storage
		}{
			name: "postgres-native",
			factory: func(t *testing.T) Storage {
				t.Helper()
				st, err := NewPostgresNativeStorage(ctx, dsn, NewInMemoryObjectStore(), fmt.Sprintf("test-native-%d", time.Now().UnixNano()))
				if err != nil {
					t.Fatalf("NewPostgresNativeStorage failed: %v", err)
				}
				t.Cleanup(func() { _ = st.Close() })
				return st
			},
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runStorageContract(ctx, t, tc.factory(t))
		})
	}
}

func runStorageContract(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	slice1ID := fmt.Sprintf("slice-1-%s", suffix)
	slice2ID := fmt.Sprintf("slice-2-%s", suffix)
	emptySliceID := fmt.Sprintf("slice-empty-%s", suffix)
	file1ID := fmt.Sprintf("file-1-%s", suffix)
	file2ID := fmt.Sprintf("file-2-%s", suffix)
	file9ID := fmt.Sprintf("file-9-%s", suffix)
	file10ID := fmt.Sprintf("file-10-%s", suffix)
	entry1ID := fmt.Sprintf("entry-1-%s", suffix)
	changeset1ID := fmt.Sprintf("cs-1-%s", suffix)
	commit1Hash := fmt.Sprintf("commit-1-%s", suffix)

	// Create primary slice
	slice := &models.Slice{ID: slice1ID, Name: "Alpha", Description: "First", Files: []string{file1ID}, Owners: []string{"alice"}, CreatedBy: "alice"}
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
	if meta.HeadCommitHash != fmt.Sprintf("init-%s", slice.ID) {
		t.Fatalf("unexpected initial head commit hash: %s", meta.HeadCommitHash)
	}
	meta.HeadCommitHash = commit1Hash
	meta.ModifiedFiles = []string{file1ID}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, slice.ID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	// Commit history
	commit := &models.Commit{CommitHash: commit1Hash, ParentHash: "", Message: "init", Timestamp: time.Now()}
	if err := st.AddSliceCommit(ctx, slice.ID, commit); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	commits, err := st.ListSliceCommits(ctx, slice.ID, 10, "")
	if err != nil || len(commits) != 1 || commits[0].CommitHash != commit.CommitHash {
		t.Fatalf("ListSliceCommits mismatch: %v len=%d", err, len(commits))
	}

	// File indexing and conflicts
	if err := st.AddFileToSlice(ctx, file1ID, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, file2ID, slice.ID); err != nil {
		t.Fatalf("AddFileToSlice new file failed: %v", err)
	}
	afterAdd, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice after AddFileToSlice failed: %v", err)
	}
	if len(afterAdd.Files) != 1 || afterAdd.Files[0] != file1ID {
		t.Fatalf("slice files should be immutable, got: %#v", afterAdd.Files)
	}
	slice2 := &models.Slice{ID: slice2ID, Name: "Beta", Description: "Second", Files: []string{file1ID}, Owners: []string{"bob"}, CreatedBy: "bob"}
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
	if err := st.AddFileToSlice(ctx, file1ID, slice2.ID); err != nil {
		t.Fatalf("AddFileToSlice second failed: %v", err)
	}
	if err := st.RemoveFileFromSlice(ctx, "file-unknown", "slice-missing"); err != nil {
		t.Fatalf("RemoveFileFromSlice for missing slice should be no-op: %v", err)
	}

	conflicts, err := st.ListConflicts(ctx)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("ListConflicts unexpected: %v len=%d", err, len(conflicts))
	}
	resolved, err := st.ResolveConflict(ctx, file1ID, slice.ID)
	if err != nil {
		t.Fatalf("ResolveConflict failed: %v", err)
	}
	if len(resolved.ConflictingSlices) != 1 || resolved.ConflictingSlices[0] != slice.ID {
		t.Fatalf("ResolveConflict result mismatch: %+v", resolved)
	}

	// Locking
	if err := st.LockSliceAndFiles(ctx, slice.ID, []string{file1ID}); err != nil {
		t.Fatalf("LockSliceAndFiles failed: %v", err)
	}
	if err := st.LockSliceAndFiles(ctx, slice2.ID, []string{file1ID}); err != ErrLockHeld {
		t.Fatalf("expected ErrLockHeld, got %v", err)
	}
	st.UnlockSliceAndFiles(ctx, slice.ID, []string{file1ID})
	if err := st.LockSliceAndFiles(ctx, slice2.ID, []string{file1ID}); err != nil {
		t.Fatalf("Lock after unlock failed: %v", err)
	}
	st.UnlockSliceAndFiles(ctx, slice2.ID, []string{file1ID})

	// Changesets
	cs := &models.Changeset{ID: changeset1ID, Hash: "h1", SliceID: slice.ID, ModifiedFiles: []string{file1ID}, Status: models.ChangesetStatusPending, Author: "alice", Message: "msg", CreatedAt: time.Now()}
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

	emptySlice := &models.Slice{ID: emptySliceID, Name: "Empty", Description: "Empty", Files: []string{}, Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, emptySlice); err != nil {
		t.Fatalf("CreateSlice empty failed: %v", err)
	}
	if err := st.SetSliceFiles(ctx, emptySlice.ID, []string{file9ID}); err != nil {
		t.Fatalf("SetSliceFiles failed: %v", err)
	}
	emptyFetched, err := st.GetSlice(ctx, emptySlice.ID)
	if err != nil {
		t.Fatalf("GetSlice after SetSliceFiles failed: %v", err)
	}
	if len(emptyFetched.Files) != 1 || emptyFetched.Files[0] != file9ID {
		t.Fatalf("SetSliceFiles mismatch: %#v", emptyFetched.Files)
	}
	if err := st.SetSliceFiles(ctx, emptySlice.ID, []string{file10ID}); err != ErrSliceFilesImmutable {
		t.Fatalf("expected ErrSliceFilesImmutable, got %v", err)
	}

	// UpdateSliceName
	if err := st.UpdateSliceName(ctx, slice.ID, "Renamed"); err != nil {
		t.Fatalf("UpdateSliceName failed: %v", err)
	}
	renamedSlice, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice after rename failed: %v", err)
	}
	if renamedSlice.Name != "Renamed" {
		t.Fatalf("expected name %q, got %q", "Renamed", renamedSlice.Name)
	}
	if err := st.UpdateSliceName(ctx, "nonexistent-slice-"+suffix, "X"); err != ErrSliceNotFound {
		t.Fatalf("expected ErrSliceNotFound, got %v", err)
	}

	// GetSliceByName
	found, err := st.GetSliceByName(ctx, "Renamed")
	if err != nil {
		t.Fatalf("GetSliceByName failed: %v", err)
	}
	if found.ID != slice.ID {
		t.Fatalf("GetSliceByName returned wrong slice: %s", found.ID)
	}
	_, err = st.GetSliceByName(ctx, "nonexistent-name-"+suffix)
	if err != ErrSliceNotFound {
		t.Fatalf("expected ErrSliceNotFound for unknown name, got %v", err)
	}

	// Entries
	entry := &models.DirectoryEntry{ID: entry1ID, Path: fmt.Sprintf("app/%s/main.go", suffix), Type: "file", ParentID: slice.ID, Content: []byte("code"), Size: 4}
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
	if err != nil {
		t.Fatalf("ListEntries root unexpected: %v", err)
	}
	var appDir *models.DirectoryEntry
	for _, e := range entries {
		if e != nil && e.Path == "app" && e.Type == "directory" {
			appDir = e
			break
		}
	}
	if appDir == nil {
		t.Fatalf("expected directory entry \"app\" at root, got %#v", entries)
	}
	l1, err := st.ListEntries(ctx, slice.ID, appDir.ID)
	if err != nil || len(l1) != 1 || l1[0].Type != "directory" || l1[0].Path != fmt.Sprintf("app/%s", suffix) {
		t.Fatalf("ListEntries app unexpected: %v got=%#v", err, l1)
	}
	l2, err := st.ListEntries(ctx, slice.ID, l1[0].ID)
	if err != nil || len(l2) != 1 || l2[0].Type != "file" || l2[0].Path != entry.Path || l2[0].ID != entry.ID {
		t.Fatalf("ListEntries app/<suffix> unexpected: %v got=%#v", err, l2)
	}
	entry.Size = 8
	if err := st.UpdateEntry(ctx, entry); err != nil {
		t.Fatalf("UpdateEntry failed: %v", err)
	}
	if err := st.DeleteEntry(ctx, entry.ID); err != nil {
		t.Fatalf("DeleteEntry failed: %v", err)
	}

	// Global state
	state := &models.GlobalState{GlobalCommitHash: "global-1-" + suffix, Timestamp: time.Now(), History: []*models.GlobalCommit{{CommitHash: "global-1-" + suffix, Timestamp: time.Now()}}}
	if err := st.UpdateGlobalState(ctx, state); err != nil {
		t.Fatalf("UpdateGlobalState failed: %v", err)
	}
	storedState, err := st.GetGlobalState(ctx)
	if err != nil || storedState.GlobalCommitHash != state.GlobalCommitHash {
		t.Fatalf("GetGlobalState mismatch: %v", err)
	}
	replaced := &models.GlobalState{GlobalCommitHash: "global-2-" + suffix, Timestamp: time.Now(), History: []*models.GlobalCommit{{CommitHash: "global-2-" + suffix, Timestamp: time.Now()}}}
	if err := st.UpdateGlobalState(ctx, replaced); err != nil {
		t.Fatalf("UpdateGlobalState replacement failed: %v", err)
	}
	replacedState, err := st.GetGlobalState(ctx)
	if err != nil {
		t.Fatalf("GetGlobalState replacement failed: %v", err)
	}
	if replacedState.GlobalCommitHash != ("global-2-"+suffix) || len(replacedState.History) != 1 || replacedState.History[0].CommitHash != ("global-2-"+suffix) {
		t.Fatalf("global state should be replaced, got: %#v", replacedState)
	}

	// Versioned content + snapshot lookup
	content := &models.FileContent{
		FileID:  "versioned-file-" + suffix,
		Path:    "src/versioned-" + suffix + ".go",
		Content: []byte("package main"),
		Size:    int64(len("package main")),
		Hash:    "hash-versioned-1-" + suffix,
	}
	if err := st.AddFileContent(ctx, content); err != nil {
		t.Fatalf("AddFileContent versioned failed: %v", err)
	}
	snapshot := &models.CommitSnapshot{
		CommitHash: "commit-snapshot-1-" + suffix,
		SliceID:    slice.ID,
		Files: map[string]string{
			content.Path: content.Hash,
		},
		Timestamp: time.Now(),
	}
	if err := st.SaveCommitSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}
	versioned, err := st.GetFileAtCommit(ctx, snapshot.CommitHash, content.Path)
	if err != nil {
		t.Fatalf("GetFileAtCommit failed: %v", err)
	}
	if versioned.Hash != content.Hash {
		t.Fatalf("GetFileAtCommit hash mismatch: got %s want %s", versioned.Hash, content.Hash)
	}
	filesAtCommit, err := st.ListFilesAtCommit(ctx, snapshot.CommitHash, "src/")
	if err != nil {
		t.Fatalf("ListFilesAtCommit failed: %v", err)
	}
	if len(filesAtCommit) != 1 || filesAtCommit[0] != content.Path {
		t.Fatalf("ListFilesAtCommit mismatch: %#v", filesAtCommit)
	}

	// Root slice init
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "root-file", rootSlice.ID); err != nil {
		t.Fatalf("AddFileToSlice root failed: %v", err)
	}
	if err := st.RemoveFileFromSlice(ctx, "root-file", rootSlice.ID); err != nil {
		t.Fatalf("RemoveFileFromSlice root failed: %v", err)
	}
	rootAfterRemove, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice after remove failed: %v", err)
	}
	for _, fileID := range rootAfterRemove.Files {
		if fileID == "root-file" {
			t.Fatalf("root slice should not keep removed file, got %#v", rootAfterRemove.Files)
		}
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
	}
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		cases = append(cases, struct {
			name    string
			factory func(t *testing.T) Storage
		}{
			name: "postgres-native-history",
			factory: func(t *testing.T) Storage {
				t.Helper()
				st, err := NewPostgresNativeStorage(ctx, dsn, NewInMemoryObjectStore(), fmt.Sprintf("test-native-history-%d", time.Now().UnixNano()))
				if err != nil {
					t.Fatalf("NewPostgresNativeStorage failed: %v", err)
				}
				t.Cleanup(func() { _ = st.Close() })
				return st
			},
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFileChangeHistoryTests(ctx, t, tc.factory(t))
		})
	}
}

func runFileChangeHistoryTests(ctx context.Context, t *testing.T, st Storage) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// Setup: Create a slice first
	slice := &models.Slice{ID: "slice-history-" + suffix, Name: "History Test", Description: "For testing file history"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	baseTime := time.Now().Add(-time.Hour)

	// Test 1: AddFileChange and GetFileHistory
	t.Run("AddFileChange", func(t *testing.T) {
		change1 := &models.FileChangeRecord{
			ID:         "change-1-" + suffix,
			SliceID:    slice.ID,
			CommitHash: "commit-abc-" + suffix,
			Path:       "src/main.go",
			ChangeType: models.ChangeTypeAdd,
			NewHash:    "hash123-" + suffix,
			LinesAdded: 50,
			Author:     "alice",
			Message:    "Initial commit",
			Timestamp:  baseTime,
		}
		if err := st.AddFileChange(ctx, change1); err != nil {
			t.Fatalf("AddFileChange failed: %v", err)
		}

		change2 := &models.FileChangeRecord{
			ID:           "change-2-" + suffix,
			SliceID:      slice.ID,
			CommitHash:   "commit-def-" + suffix,
			Path:         "src/main.go",
			ChangeType:   models.ChangeTypeModify,
			OldHash:      "hash123-" + suffix,
			NewHash:      "hash456-" + suffix,
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
		if history[0].ID != ("change-2-" + suffix) {
			t.Errorf("expected newest change first, got %s", history[0].ID)
		}
		if history[1].ID != ("change-1-" + suffix) {
			t.Errorf("expected oldest change second, got %s", history[1].ID)
		}
	})

	// Test 2: AddFileChanges batch
	t.Run("AddFileChanges batch", func(t *testing.T) {
		commitGHI := "commit-ghi-" + suffix
		changes := []*models.FileChangeRecord{
			{
				ID:         "change-3-" + suffix,
				SliceID:    slice.ID,
				CommitHash: commitGHI,
				Path:       "src/utils/helper.go",
				ChangeType: models.ChangeTypeAdd,
				NewHash:    "hashutil1-" + suffix,
				Author:     "charlie",
				Message:    "Add helper",
				Timestamp:  baseTime.Add(20 * time.Minute),
			},
			{
				ID:         "change-4-" + suffix,
				SliceID:    slice.ID,
				CommitHash: commitGHI,
				Path:       "src/utils/config.go",
				ChangeType: models.ChangeTypeAdd,
				NewHash:    "hashutil2-" + suffix,
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
		changes, err := st.GetCommitChanges(ctx, "commit-ghi-"+suffix)
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

func TestPostgresNativeStoragePersistsAcrossRestart(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres persistence test")
	}

	ctx := context.Background()
	store := NewInMemoryObjectStore()
	namespace := fmt.Sprintf("restart-%d", time.Now().UnixNano())
	rs, err := NewPostgresNativeStorage(ctx, dsn, store, namespace)
	t.Cleanup(func() {
		_ = rs.Close()
	})
	if err != nil {
		t.Fatalf("NewPostgresNativeStorage failed: %v", err)
	}

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
	if err := rs.AddFileContent(ctx, &models.FileContent{
		FileID:  entry.Path,
		Path:    entry.Path,
		Content: []byte("hi"),
		Size:    2,
		Hash:    "hash-main-go",
	}); err != nil {
		t.Fatalf("AddFileContent failed: %v", err)
	}
	if err := rs.UpdateGlobalState(ctx, &models.GlobalState{GlobalCommitHash: "gc1", Timestamp: time.Now()}); err != nil {
		t.Fatalf("UpdateGlobalState failed: %v", err)
	}
	if err := rs.AddFileToSlice(ctx, "file-3", slice1.ID); err != nil {
		t.Fatalf("AddFileToSlice file-3 failed: %v", err)
	}

	if err := rs.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	rs, err = NewPostgresNativeStorage(ctx, dsn, store, namespace)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
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
	mappedFile3, err := rs.GetActiveSlicesForFile(ctx, "file-3")
	if err != nil {
		t.Fatalf("GetActiveSlicesForFile file-3 failed: %v", err)
	}
	if len(mappedFile3) != 1 || mappedFile3[0] != slice1.ID {
		t.Fatalf("expected file-3 to map to %s after rebuild, got %#v", slice1.ID, mappedFile3)
	}

	restoredCS, err := rs.GetChangeset(ctx, cs.ID)
	if err != nil || restoredCS.ID != cs.ID {
		t.Fatalf("expected changeset restored after rebuild: %v", err)
	}
	restoredEntry, err := rs.GetEntry(ctx, entry.ID)
	if err != nil || restoredEntry.Path != entry.Path {
		t.Fatalf("expected entry restored after rebuild: %v", err)
	}
	restoredFile, err := rs.GetSliceFileByPath(ctx, slice1.ID, entry.Path)
	if err != nil {
		t.Fatalf("expected file content restored after rebuild: %v", err)
	}
	if got := string(restoredFile.Content); got != "hi" {
		t.Fatalf("expected restored file content hi, got %q", got)
	}
	if restoredFile.Hash != "hash-main-go" {
		t.Fatalf("expected restored file hash hash-main-go, got %q", restoredFile.Hash)
	}
	restoredState, err := rs.GetGlobalState(ctx)
	if err != nil || restoredState.GlobalCommitHash != "gc1" {
		t.Fatalf("expected global state restored, got %#v err=%v", restoredState, err)
	}
}
