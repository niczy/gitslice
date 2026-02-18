package benchmarksuite

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

// TestFileServiceReadLoad measures and validates concurrent read performance
// on the file service (ListEntries, GetFileHistory, GetDirectoryHistory).
//
// The test first writes a known dataset by merging changesets, then fans out
// many concurrent readers and checks every response for correctness.
func TestFileServiceReadLoad(t *testing.T) {
	const (
		// Number of slices / files to populate before reading.
		writeSlices = 20
		// Number of concurrent read goroutines.
		readWorkers = 40
		// Read iterations per worker.
		readsPerWorker = 50
	)

	ctx := userCtx(context.Background())
	ts := time.Now().UnixNano()

	type writeRecord struct {
		sliceID    string
		fileID     string
		commitHash string
	}
	written := make([]writeRecord, writeSlices)

	// ----- Write phase -----
	t.Log("File service read load: write phase...")
	for i := 0; i < writeSlices; i++ {
		sid := fmt.Sprintf("fsread-s%d-%d", i, ts)
		fid := fmt.Sprintf("fsread/%d-%d/file.go", i, ts)

		_, err := benchSliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
			ParentSliceId: "root_slice",
			NewSliceId:    sid,
			Name:          sid,
		})
		if err != nil {
			t.Fatalf("write: CreateSliceFromFolder(%s): %v", sid, err)
		}

		csResp, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
			SliceId:       sid,
			ModifiedFiles: []string{fid},
			Author:        "fsread-test",
			Message:       fmt.Sprintf("file read test write %d", i),
		})
		if err != nil {
			t.Fatalf("write: CreateChangeset(%s): %v", sid, err)
		}

		mergeResp, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
			ChangesetId: csResp.ChangesetId,
		})
		if err != nil {
			t.Fatalf("write: MergeChangeset(%s): %v", sid, err)
		}
		if mergeResp.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
			t.Fatalf("write: expected SUCCESS for %s, got %v", sid, mergeResp.Status)
		}
		written[i] = writeRecord{sliceID: sid, fileID: fid, commitHash: mergeResp.NewCommitHash}
	}
	t.Logf("File service read load: wrote %d slices", writeSlices)

	// ----- Read phase -----
	t.Logf("File service read load: read phase (%d workers × %d reads)...", readWorkers, readsPerWorker)

	var (
		listEntriesOK  int64
		fileHistoryOK  int64
		dirHistoryOK   int64
		readErrors     int64
	)

	var wg sync.WaitGroup
	for w := 0; w < readWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			rctx := userCtx(context.Background())
			for iter := 0; iter < readsPerWorker; iter++ {
				rec := written[(workerID+iter)%writeSlices]

				// 1. ListEntries for the slice.
				_, err := benchFileClient.ListEntries(rctx, &filev1.ListEntriesRequest{
					Version: &filev1.ListEntriesRequest_SliceVersion{
						SliceVersion: &filev1.SliceVersion{SliceId: rec.sliceID},
					},
				})
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
				} else {
					atomic.AddInt64(&listEntriesOK, 1)
				}

				// 2. GetFileHistory for the file written by this slice.
				// Must specify SliceId so the server queries the correct slice's history.
				histResp, err := benchFileClient.GetFileHistory(rctx, &filev1.GetFileHistoryRequest{
					Path:    rec.fileID,
					SliceId: rec.sliceID,
				})
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
				} else if len(histResp.Changes) == 0 {
					atomic.AddInt64(&readErrors, 1)
				} else {
					atomic.AddInt64(&fileHistoryOK, 1)
				}

				// 3. GetDirectoryHistory for the parent directory.
				dirPath := fmt.Sprintf("fsread/%d-%d", (workerID+iter)%writeSlices, ts)
				_, err = benchFileClient.GetDirectoryHistory(rctx, &filev1.GetDirectoryHistoryRequest{
					Path:  dirPath,
					Limit: 10,
				})
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
				} else {
					atomic.AddInt64(&dirHistoryOK, 1)
				}
			}
		}(w)
	}

	readStart := time.Now()
	wg.Wait()
	readElapsed := time.Since(readStart).Seconds()

	totalReads := int64(readWorkers) * int64(readsPerWorker) * 3
	t.Logf("=== File Service Read Load Results ===")
	t.Logf("Workers:              %d", readWorkers)
	t.Logf("Reads per worker:     %d × 3 RPCs = %d total RPC calls", readsPerWorker, totalReads)
	t.Logf("Elapsed:              %.2f s", readElapsed)
	t.Logf("Throughput:           %.0f RPC/s", float64(totalReads)/readElapsed)
	t.Logf("")
	t.Logf("ListEntries OK:       %d", listEntriesOK)
	t.Logf("GetFileHistory OK:    %d", fileHistoryOK)
	t.Logf("GetDirHistory OK:     %d", dirHistoryOK)
	t.Logf("Read errors:          %d", readErrors)

	if readErrors > 0 {
		t.Errorf("INTEGRITY FAIL: %d read errors out of %d total read calls", readErrors, totalReads)
	}
}

// TestFileServiceCommitChangesConsistency creates a slice, merges multiple
// changesets, then verifies that GetCommitChanges returns the correct modified
// files for each commit.
func TestFileServiceCommitChangesConsistency(t *testing.T) {
	ctx := userCtx(context.Background())
	ts := time.Now().UnixNano()

	sid := fmt.Sprintf("cc-slice-%d", ts)
	files := []string{
		fmt.Sprintf("cc/%d/a.go", ts),
		fmt.Sprintf("cc/%d/b.go", ts),
		fmt.Sprintf("cc/%d/c.go", ts),
	}

	_, err := benchSliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		NewSliceId:    sid,
		Name:          sid,
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder: %v", err)
	}

	type commitRecord struct {
		hash  string
		files []string
	}
	var commits []commitRecord

	for i, f := range files {
		csResp, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
			SliceId:       sid,
			ModifiedFiles: []string{f},
			Author:        "cc-test",
			Message:       fmt.Sprintf("commit %d: add %s", i, f),
		})
		if err != nil {
			t.Fatalf("CreateChangeset(%d): %v", i, err)
		}

		mergeResp, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
			ChangesetId: csResp.ChangesetId,
		})
		if err != nil {
			t.Fatalf("MergeChangeset(%d): %v", i, err)
		}
		if mergeResp.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
			t.Fatalf("expected SUCCESS for commit %d, got %v", i, mergeResp.Status)
		}
		commits = append(commits, commitRecord{
			hash:  mergeResp.NewCommitHash,
			files: []string{f},
		})
	}

	// Verify GetCommitChanges for each commit.
	for i, c := range commits {
		changesResp, err := benchFileClient.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
			CommitHash: c.hash,
		})
		if err != nil {
			t.Errorf("commit %d: GetCommitChanges(%s): %v", i, c.hash, err)
			continue
		}
		if len(changesResp.Changes) == 0 {
			t.Errorf("commit %d: expected at least one change, got none", i)
			continue
		}

		// Verify the expected file appears in the change records.
		found := false
		for _, ch := range changesResp.Changes {
			if ch.Path == c.files[0] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("commit %d: file %q not found in changes %v", i, c.files[0], changesResp.Changes)
		}
	}

	// Verify file history shows commits for each file, scoped to the slice.
	for _, f := range files {
		histResp, err := benchFileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
			Path:    f,
			SliceId: sid,
			Limit:   5,
		})
		if err != nil {
			t.Errorf("GetFileHistory(%q): %v", f, err)
			continue
		}
		if len(histResp.Changes) == 0 {
			t.Errorf("file %q: expected history entries, got none", f)
		}
	}
}
