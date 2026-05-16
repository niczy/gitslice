package benchmarksuite

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

// TestConflictDetection verifies that when two folder views both try to merge a
// changeset touching the same file, exactly one succeeds and the other receives
// MERGE_STATUS_STALE_BASE. With path-head authority, the second writer must
// rebase on the latest tracked-folder head rather than relying on the removed
// root ownership projection.
func TestConflictDetection(t *testing.T) {
	ctx := userCtx(context.Background())
	ts := time.Now().UnixNano()

	sharedFile := fmt.Sprintf("conflict/shared-%d.go", ts)
	sliceA := fmt.Sprintf("conflict-a-%d", ts)
	sliceB := fmt.Sprintf("conflict-b-%d", ts)

	// Create two independent slices from root.
	for _, sid := range []string{sliceA, sliceB} {
		_, err := benchSliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
			ParentSliceId: "root",
			NewSliceId:    sid,
			Name:          sid,
			FolderPaths:   []string{"conflict"},
		})
		if err != nil {
			t.Fatalf("CreateSliceFromFolder(%s): %v", sid, err)
		}
	}
	seedBenchmarkSliceFileState(t, ctx, sliceB, sharedFile, []byte("slice B content\n"))

	// Both slices create a changeset touching the same file.
	csA, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       sliceA,
		ModifiedFiles: []string{sharedFile},
		Author:        "conflict-test",
		Message:       "change from A",
	})
	if err != nil {
		t.Fatalf("CreateChangeset(A): %v", err)
	}

	csB, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       sliceB,
		ModifiedFiles: []string{sharedFile},
		Author:        "conflict-test",
		Message:       "change from B",
	})
	if err != nil {
		t.Fatalf("CreateChangeset(B): %v", err)
	}

	// Merge A first – must succeed.
	mergeA, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: csA.ChangesetId,
	})
	if err != nil {
		t.Fatalf("MergeChangeset(A): %v", err)
	}
	if mergeA.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected A to merge with SUCCESS, got %v", mergeA.Status)
	}

	// Merge B – must be rejected because A advanced the tracked folder head.
	mergeB, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: csB.ChangesetId,
	})
	if err != nil {
		t.Fatalf("MergeChangeset(B): %v", err)
	}
	if mergeB.Status != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("expected B to fail with STALE_BASE, got %v", mergeB.Status)
	}
	if mergeB.Message == "" {
		t.Fatal("expected stale-base response to include a message")
	}
}

// TestConflictResolution verifies that after a conflict is resolved via the
// admin service, both slices share normalized content and can merge.
//
// The setup directly pre-registers both slices as owners of the same file,
// which is the correct way to model the "two teams claim the same directory"
// scenario. The admin then resolves the conflict in favour of B by normalizing
// A to B's content.
func TestConflictResolution(t *testing.T) {
	ctx := userCtx(context.Background())
	ts := time.Now().UnixNano()

	sharedFile := fmt.Sprintf("resolve/shared-%d.go", ts)
	sliceA := fmt.Sprintf("resolve-a-%d", ts)
	sliceB := fmt.Sprintf("resolve-b-%d", ts)

	// Pre-register both slices as co-owners of sharedFile.
	// This populates the file index so ResolveConflict can choose between them.
	for _, sid := range []string{sliceA, sliceB} {
		if err := benchStorage.CreateSlice(ctx, &models.Slice{
			ID:        sid,
			Name:      sid,
			Files:     []string{sharedFile},
			Owners:    []string{benchmarkAdminUsername},
			CreatedBy: benchmarkAdminUsername,
		}); err != nil {
			t.Fatalf("CreateSlice(%s): %v", sid, err)
		}
	}
	seedBenchmarkSliceFileState(t, ctx, sliceA, sharedFile, []byte("slice A content\n"))
	seedBenchmarkSliceFileState(t, ctx, sliceB, sharedFile, []byte("slice B content\n"))

	// Confirm the conflict is visible via the slice API.
	conflictsResp, err := benchSliceClient.GetConflicts(ctx, &slicev1.ConflictsRequest{})
	if err != nil {
		t.Fatalf("GetConflicts: %v", err)
	}
	foundConflict := false
	for _, c := range conflictsResp.Conflicts {
		if c.FileId == sharedFile {
			foundConflict = true
			break
		}
	}
	if !foundConflict {
		t.Fatalf("expected conflict for %q in GetConflicts (total=%d)", sharedFile, conflictsResp.TotalConflicts)
	}

	// Resolve: prefer B.
	_, err = benchSliceClient.ResolveConflict(ctx, &slicev1.ResolveConflictRequest{
		FileId:           sharedFile,
		PreferredSliceId: sliceB,
	})
	if err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}

	// B should now merge successfully (it is the sole owner of the file).
	csB, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       sliceB,
		ModifiedFiles: []string{sharedFile},
		Message:       "change from B after resolution",
	})
	if err != nil {
		t.Fatalf("CreateChangeset(B): %v", err)
	}
	mergeB, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: csB.ChangesetId})
	if err != nil {
		t.Fatalf("MergeChangeset(B): %v", err)
	}
	if mergeB.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected B to succeed after resolution, got %v", mergeB.Status)
	}

	// A is no longer content-conflicted because resolution normalized all owners
	// to B's content.
	csA, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       sliceA,
		ModifiedFiles: []string{sharedFile},
		Message:       "change from A after normalization",
	})
	if err != nil {
		t.Fatalf("CreateChangeset(A): %v", err)
	}
	mergeA, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: csA.ChangesetId})
	if err != nil {
		t.Fatalf("MergeChangeset(A): %v", err)
	}
	if mergeA.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected A to succeed after conflict normalization, got %v", mergeA.Status)
	}
}

// TestConflictDetectionUnderLoad creates many slices that all claim a single
// shared file, then merges all changesets concurrently. Exactly one should
// succeed; the rest should be rejected by tracked-folder stale-base checks.
func TestConflictDetectionUnderLoad(t *testing.T) {
	const numSlices = 20
	ctx := userCtx(context.Background())
	ts := time.Now().UnixNano()

	hotFile := fmt.Sprintf("hotfile/shared-%d.go", ts)

	type csEntry struct {
		sliceID     string
		changesetID string
	}
	entries := make([]csEntry, numSlices)

	for i := 0; i < numSlices; i++ {
		sid := fmt.Sprintf("hotfile-s%d-%d", i, ts)
		entries[i].sliceID = sid

		_, err := benchSliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
			ParentSliceId: "root",
			NewSliceId:    sid,
			Name:          sid,
			FolderPaths:   []string{"hotfile"},
		})
		if err != nil {
			t.Fatalf("CreateSliceFromFolder(%s): %v", sid, err)
		}
		seedBenchmarkSliceFileState(t, ctx, sid, hotFile, []byte(fmt.Sprintf("hot change from slice %d\n", i)))

		csResp, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
			SliceId:       sid,
			ModifiedFiles: []string{hotFile},
			Message:       fmt.Sprintf("hot change from slice %d", i),
		})
		if err != nil {
			t.Fatalf("CreateChangeset(%s): %v", sid, err)
		}
		entries[i].changesetID = csResp.ChangesetId
	}

	// Merge all changesets concurrently.
	type result struct {
		idx    int
		status slicev1.MergeStatus
		err    error
	}
	results := make(chan result, numSlices)
	var wg sync.WaitGroup

	for i, e := range entries {
		wg.Add(1)
		go func(idx int, csID string) {
			defer wg.Done()
			resp, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
				ChangesetId: csID,
			})
			if err != nil {
				results <- result{idx: idx, err: err}
				return
			}
			results <- result{idx: idx, status: resp.Status}
		}(i, e.changesetID)
	}

	wg.Wait()
	close(results)

	successCount := 0
	conflictCount := 0
	staleBaseCount := 0
	locked := make([]result, 0)
	errorCount := 0
	for r := range results {
		if r.err != nil {
			t.Logf("merge %d error: %v", r.idx, r.err)
			errorCount++
			continue
		}
		switch r.status {
		case slicev1.MergeStatus_MERGE_STATUS_SUCCESS:
			successCount++
		case slicev1.MergeStatus_MERGE_STATUS_CONFLICT:
			conflictCount++
		case slicev1.MergeStatus_MERGE_STATUS_STALE_BASE:
			staleBaseCount++
		case slicev1.MergeStatus_MERGE_STATUS_LOCKED:
			locked = append(locked, r)
		default:
			t.Logf("merge %d unexpected status: %v", r.idx, r.status)
			errorCount++
		}
	}

	for _, r := range locked {
		resp, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
			ChangesetId: entries[r.idx].changesetID,
		})
		if err != nil {
			t.Logf("retry merge %d error: %v", r.idx, err)
			errorCount++
			continue
		}
		switch resp.GetStatus() {
		case slicev1.MergeStatus_MERGE_STATUS_SUCCESS:
			successCount++
		case slicev1.MergeStatus_MERGE_STATUS_CONFLICT:
			conflictCount++
		case slicev1.MergeStatus_MERGE_STATUS_STALE_BASE:
			staleBaseCount++
		default:
			t.Logf("retry merge %d unexpected status: %v", r.idx, resp.GetStatus())
			errorCount++
		}
	}

	t.Logf("Hot-file concurrent merge results: success=%d conflict=%d stale_base=%d locked=%d error=%d",
		successCount, conflictCount, staleBaseCount, len(locked), errorCount)

	if errorCount > 0 {
		t.Errorf("unexpected errors during concurrent hot-file merges: %d", errorCount)
	}
	if successCount != 1 {
		t.Errorf("expected exactly 1 successful merge for hot file, got %d", successCount)
	}
	if conflictCount+staleBaseCount != numSlices-1 {
		t.Errorf("expected %d rejected merges, got conflicts=%d stale_base=%d",
			numSlices-1, conflictCount, staleBaseCount)
	}
}

func seedBenchmarkSliceFileState(t *testing.T, ctx context.Context, sliceID, filePath string, content []byte) {
	t.Helper()
	manifest, err := storage.WriteSliceFileManifest(ctx, benchStorage, sliceID, filePath, content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest(%s, %s): %v", sliceID, filePath, err)
	}
	if err := benchStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     manifest.TotalSize,
		Hash:     manifest.Hash,
	}); err != nil {
		t.Fatalf("AddEntry(%s, %s): %v", sliceID, filePath, err)
	}
	if err := benchStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("AddFileToSlice(%s, %s): %v", sliceID, filePath, err)
	}
}
