package workflow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	filev1 "github.com/niczy/gitslice/proto/file"
)

func TestFileHistoryRPCIntegration(t *testing.T) {
	if testStorage == nil {
		t.Fatal("expected test storage to be initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a unique slice for this test
	sliceID := fmt.Sprintf("slice-history-rpc-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:    sliceID,
		Name:  "History RPC Test",
		Files: []string{"src/main.go", "src/utils/helper.go", "docs/readme.md"},
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	baseTime := time.Now().Add(-time.Hour)

	// Add file change history
	changes := []*models.FileChangeRecord{
		{
			ID:         fmt.Sprintf("change-1-%d", time.Now().UnixNano()),
			SliceID:    sliceID,
			CommitHash: "commit-abc123",
			Path:       "src/main.go",
			ChangeType: models.ChangeTypeAdd,
			NewHash:    "hash1",
			LinesAdded: 100,
			Author:     "alice",
			Message:    "Initial commit",
			Timestamp:  baseTime,
		},
		{
			ID:           fmt.Sprintf("change-2-%d", time.Now().UnixNano()),
			SliceID:      sliceID,
			CommitHash:   "commit-def456",
			Path:         "src/main.go",
			ChangeType:   models.ChangeTypeModify,
			OldHash:      "hash1",
			NewHash:      "hash2",
			LinesAdded:   20,
			LinesDeleted: 5,
			Author:       "bob",
			Message:      "Fix bug in main",
			Timestamp:    baseTime.Add(10 * time.Minute),
		},
		{
			ID:         fmt.Sprintf("change-3-%d", time.Now().UnixNano()),
			SliceID:    sliceID,
			CommitHash: "commit-def456",
			Path:       "src/utils/helper.go",
			ChangeType: models.ChangeTypeAdd,
			NewHash:    "hash3",
			LinesAdded: 50,
			Author:     "bob",
			Message:    "Fix bug in main",
			Timestamp:  baseTime.Add(10 * time.Minute),
		},
		{
			ID:         fmt.Sprintf("change-4-%d", time.Now().UnixNano()),
			SliceID:    sliceID,
			CommitHash: "commit-ghi789",
			Path:       "docs/readme.md",
			ChangeType: models.ChangeTypeAdd,
			NewHash:    "hash4",
			LinesAdded: 30,
			Author:     "charlie",
			Message:    "Add documentation",
			Timestamp:  baseTime.Add(20 * time.Minute),
		},
	}

	if err := testStorage.AddFileChanges(ctx, changes); err != nil {
		t.Fatalf("failed to add file changes: %v", err)
	}

	fileClient := newFileClient(t)

	// Test 1: GetFileHistory
	t.Run("GetFileHistory", func(t *testing.T) {
		resp, err := fileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
			Path:    "src/main.go",
			SliceId: sliceID,
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("GetFileHistory failed: %v", err)
		}

		if len(resp.Changes) != 2 {
			t.Fatalf("expected 2 changes for src/main.go, got %d", len(resp.Changes))
		}

		// Verify newest first
		if resp.Changes[0].Author != "bob" {
			t.Errorf("expected newest change by bob, got %s", resp.Changes[0].Author)
		}
		if resp.Changes[1].Author != "alice" {
			t.Errorf("expected oldest change by alice, got %s", resp.Changes[1].Author)
		}

		// Verify change types
		if resp.Changes[0].ChangeType != filev1.ChangeType_CHANGE_TYPE_MODIFY {
			t.Errorf("expected modify change type, got %v", resp.Changes[0].ChangeType)
		}
		if resp.Changes[1].ChangeType != filev1.ChangeType_CHANGE_TYPE_ADD {
			t.Errorf("expected add change type, got %v", resp.Changes[1].ChangeType)
		}
	})

	// Test 2: GetFileHistory with pagination
	t.Run("GetFileHistoryPagination", func(t *testing.T) {
		// Get first page
		page1, err := fileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
			Path:    "src/main.go",
			SliceId: sliceID,
			Limit:   1,
		})
		if err != nil {
			t.Fatalf("GetFileHistory page1 failed: %v", err)
		}

		if len(page1.Changes) != 1 {
			t.Fatalf("expected 1 change in page1, got %d", len(page1.Changes))
		}
		if !page1.HasMore {
			t.Error("expected HasMore=true for page1")
		}

		// Get second page
		page2, err := fileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
			Path:       "src/main.go",
			SliceId:    sliceID,
			Limit:      1,
			FromCommit: page1.NextCommit,
		})
		if err != nil {
			t.Fatalf("GetFileHistory page2 failed: %v", err)
		}

		if len(page2.Changes) != 1 {
			t.Fatalf("expected 1 change in page2, got %d", len(page2.Changes))
		}
		if page2.Changes[0].Id == page1.Changes[0].Id {
			t.Error("page2 should have different change than page1")
		}
	})

	// Test 3: GetDirectoryHistory
	t.Run("GetDirectoryHistory", func(t *testing.T) {
		resp, err := fileClient.GetDirectoryHistory(ctx, &filev1.GetDirectoryHistoryRequest{
			Path:    "src",
			SliceId: sliceID,
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("GetDirectoryHistory failed: %v", err)
		}

		if len(resp.Changes) != 3 {
			t.Fatalf("expected 3 changes under src/, got %d", len(resp.Changes))
		}

		// Verify summary
		if resp.Summary == nil {
			t.Fatal("expected summary in response")
		}
		if resp.Summary.TotalChanges != 3 {
			t.Errorf("expected 3 total changes in summary, got %d", resp.Summary.TotalChanges)
		}
		if resp.Summary.FilesChanged != 2 {
			t.Errorf("expected 2 unique files in summary, got %d", resp.Summary.FilesChanged)
		}
	})

	// Test 4: GetDirectoryHistory for root
	t.Run("GetDirectoryHistoryRoot", func(t *testing.T) {
		resp, err := fileClient.GetDirectoryHistory(ctx, &filev1.GetDirectoryHistoryRequest{
			Path:    "",
			SliceId: sliceID,
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("GetDirectoryHistory root failed: %v", err)
		}

		if len(resp.Changes) != 4 {
			t.Fatalf("expected 4 changes under root, got %d", len(resp.Changes))
		}
	})

	// Test 5: GetCommitChanges
	t.Run("GetCommitChanges", func(t *testing.T) {
		resp, err := fileClient.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
			CommitHash: "commit-def456",
		})
		if err != nil {
			t.Fatalf("GetCommitChanges failed: %v", err)
		}

		if len(resp.Changes) != 2 {
			t.Fatalf("expected 2 changes in commit-def456, got %d", len(resp.Changes))
		}
		if resp.CommitHash != "commit-def456" {
			t.Errorf("expected commit hash commit-def456, got %s", resp.CommitHash)
		}

		// Verify stats
		if resp.FilesModified != 1 {
			t.Errorf("expected 1 modified file, got %d", resp.FilesModified)
		}
		if resp.FilesAdded != 1 {
			t.Errorf("expected 1 added file, got %d", resp.FilesAdded)
		}
	})

	// Test 6: GetCommitChanges for non-existent commit
	t.Run("GetCommitChangesEmpty", func(t *testing.T) {
		resp, err := fileClient.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
			CommitHash: "nonexistent-commit",
		})
		if err != nil {
			t.Fatalf("GetCommitChanges nonexistent failed: %v", err)
		}

		if len(resp.Changes) != 0 {
			t.Errorf("expected 0 changes for nonexistent commit, got %d", len(resp.Changes))
		}
	})

	// Test 7: GetFileHistory for non-existent file
	t.Run("GetFileHistoryEmpty", func(t *testing.T) {
		resp, err := fileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
			Path:    "nonexistent/file.go",
			SliceId: sliceID,
			Limit:   10,
		})
		if err != nil {
			t.Fatalf("GetFileHistory nonexistent failed: %v", err)
		}

		if len(resp.Changes) != 0 {
			t.Errorf("expected 0 changes for nonexistent file, got %d", len(resp.Changes))
		}
	})

	// Test 8: Validation errors
	t.Run("ValidationErrors", func(t *testing.T) {
		// GetFileHistory without path
		_, err := fileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
			SliceId: sliceID,
		})
		if err == nil {
			t.Error("expected error for GetFileHistory without path")
		}

		// GetCommitChanges without commit_hash
		_, err = fileClient.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{})
		if err == nil {
			t.Error("expected error for GetCommitChanges without commit_hash")
		}
	})
}

func TestFileHistoryWithDefaultSlice(t *testing.T) {
	if testStorage == nil {
		t.Fatal("expected test storage to be initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get root slice
	rootSlice, err := testStorage.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to get root slice: %v", err)
	}

	// Add a change to root slice
	change := &models.FileChangeRecord{
		ID:         fmt.Sprintf("root-change-%d", time.Now().UnixNano()),
		SliceID:    rootSlice.ID,
		CommitHash: fmt.Sprintf("root-commit-%d", time.Now().UnixNano()),
		Path:       fmt.Sprintf("root-file-%d.go", time.Now().UnixNano()),
		ChangeType: models.ChangeTypeAdd,
		NewHash:    "roothash",
		Author:     "root-author",
		Message:    "Root slice change",
		Timestamp:  time.Now(),
	}
	if err := testStorage.AddFileChange(ctx, change); err != nil {
		t.Fatalf("failed to add root change: %v", err)
	}

	fileClient := newFileClient(t)

	// Test: GetFileHistory without specifying slice_id should use root slice
	resp, err := fileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
		Path:  change.Path,
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("GetFileHistory without slice_id failed: %v", err)
	}

	if len(resp.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(resp.Changes))
	}
	if resp.Changes[0].Author != "root-author" {
		t.Errorf("expected author root-author, got %s", resp.Changes[0].Author)
	}
}
