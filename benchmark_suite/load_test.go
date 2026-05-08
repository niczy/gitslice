package benchmarksuite

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

// userResult captures the outcome and timings for one simulated user session.
type userResult struct {
	userIndex      int
	createSliceMs  float64
	createChangeMs float64
	mergeMs        float64
	totalMs        float64
	mergeStatus    slicev1.MergeStatus
	err            error
}

// loadMetrics aggregates results across all simulated users.
type loadMetrics struct {
	totalUsers    int
	successCount  int64
	conflictCount int64
	errorCount    int64
	elapsedSec    float64

	createSliceLatencies  []float64
	createChangeLatencies []float64
	mergeLatencies        []float64
	totalLatencies        []float64
}

func (m *loadMetrics) record(r userResult) {
	if r.err != nil {
		atomic.AddInt64(&m.errorCount, 1)
		return
	}
	switch r.mergeStatus {
	case slicev1.MergeStatus_MERGE_STATUS_SUCCESS:
		atomic.AddInt64(&m.successCount, 1)
	case slicev1.MergeStatus_MERGE_STATUS_CONFLICT:
		atomic.AddInt64(&m.conflictCount, 1)
	default:
		atomic.AddInt64(&m.errorCount, 1)
	}
}

// TestSimulate100kUsers exercises the slice and file services under concurrent
// load, simulating up to 100 000 independent users each performing the full
// changeset workflow:
//
//	CreateSliceFromFolder → CreateChangeset → MergeChangeset
//
// Configuration via environment variables:
//
//	BENCHMARK_USERS   – total simulated users (default 100 000)
//	BENCHMARK_WORKERS – number of concurrent goroutines (default 2×NumCPU)
//
// After the load phase, the test verifies system integrity: every successful
// merge is reflected in the slice's commit history and state.
func TestSimulate100kUsers(t *testing.T) {
	numUsers, err := benchmarkUserCountFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	numWorkers := runtime.NumCPU() * 2
	if numWorkers < 4 {
		numWorkers = 4
	}
	if v := os.Getenv("BENCHMARK_WORKERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			t.Fatalf("BENCHMARK_WORKERS must be a positive integer, got %q", v)
		}
		numWorkers = n
	}

	skippedInShortMode(t, numUsers)

	t.Logf("Starting load test: %d users, %d workers", numUsers, numWorkers)

	tasks := make(chan int, numWorkers*2)
	results := make(chan userResult, numWorkers*4)

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range tasks {
				results <- runUserSession(i)
			}
		}()
	}

	// Feed tasks in the background so we can start collecting results immediately.
	go func() {
		for i := 0; i < numUsers; i++ {
			tasks <- i
		}
		close(tasks)
	}()

	// Close results once all workers finish.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all results.
	allResults := make([]userResult, 0, numUsers)
	start := time.Now()
	for r := range results {
		allResults = append(allResults, r)
	}
	elapsed := time.Since(start).Seconds()

	// Build metrics.
	var m loadMetrics
	m.totalUsers = numUsers
	m.elapsedSec = elapsed
	for _, r := range allResults {
		m.record(r)
		if r.err == nil {
			m.createSliceLatencies = append(m.createSliceLatencies, r.createSliceMs)
			m.createChangeLatencies = append(m.createChangeLatencies, r.createChangeMs)
			m.mergeLatencies = append(m.mergeLatencies, r.mergeMs)
			m.totalLatencies = append(m.totalLatencies, r.totalMs)
		}
	}

	// Sort for percentile calculations.
	sort.Float64s(m.createSliceLatencies)
	sort.Float64s(m.createChangeLatencies)
	sort.Float64s(m.mergeLatencies)
	sort.Float64s(m.totalLatencies)

	throughput := float64(m.successCount) / elapsed

	t.Logf("=== Load Test Results ===")
	t.Logf("Users simulated:   %d", numUsers)
	t.Logf("Workers:           %d", numWorkers)
	t.Logf("Elapsed:           %.2f s", elapsed)
	t.Logf("Throughput:        %.1f users/sec", throughput)
	t.Logf("")
	t.Logf("Outcomes:")
	t.Logf("  Successful merges: %d", m.successCount)
	t.Logf("  Conflicts:         %d", m.conflictCount)
	t.Logf("  Errors:            %d", m.errorCount)
	t.Logf("")
	if len(m.totalLatencies) > 0 {
		t.Logf("End-to-end latency per user (ms):")
		t.Logf("  P50:  %.2f", percentile(m.totalLatencies, 50))
		t.Logf("  P95:  %.2f", percentile(m.totalLatencies, 95))
		t.Logf("  P99:  %.2f", percentile(m.totalLatencies, 99))
		t.Logf("  Max:  %.2f", m.totalLatencies[len(m.totalLatencies)-1])
		t.Logf("")
		t.Logf("CreateSliceFromFolder latency (ms):")
		t.Logf("  P50:  %.2f", percentile(m.createSliceLatencies, 50))
		t.Logf("  P95:  %.2f", percentile(m.createSliceLatencies, 95))
		t.Logf("  P99:  %.2f", percentile(m.createSliceLatencies, 99))
		t.Logf("")
		t.Logf("CreateChangeset latency (ms):")
		t.Logf("  P50:  %.2f", percentile(m.createChangeLatencies, 50))
		t.Logf("  P95:  %.2f", percentile(m.createChangeLatencies, 95))
		t.Logf("  P99:  %.2f", percentile(m.createChangeLatencies, 99))
		t.Logf("")
		t.Logf("MergeChangeset latency (ms):")
		t.Logf("  P50:  %.2f", percentile(m.mergeLatencies, 50))
		t.Logf("  P95:  %.2f", percentile(m.mergeLatencies, 95))
		t.Logf("  P99:  %.2f", percentile(m.mergeLatencies, 99))
	}

	// Integrity assertions.
	if m.errorCount > 0 {
		t.Errorf("INTEGRITY FAIL: %d users encountered errors", m.errorCount)
	}
	if m.conflictCount > 0 {
		// Each user operates on unique files so zero conflicts are expected.
		t.Errorf("INTEGRITY FAIL: unexpected conflicts detected (%d). Each user owns unique files.", m.conflictCount)
	}
	if int(m.successCount) != numUsers {
		t.Errorf("INTEGRITY FAIL: expected %d successful merges, got %d", numUsers, m.successCount)
	}

	// Sample-based integrity: verify a random 1% of users have consistent state.
	sampleSize := numUsers / 100
	if sampleSize < 10 {
		sampleSize = 10
	}
	if sampleSize > numUsers {
		sampleSize = numUsers
	}
	t.Logf("Running sample integrity checks on %d users (1%%)...", sampleSize)

	perm := rand.Perm(numUsers)
	integrityErrors := 0

	for _, i := range perm[:sampleSize] {
		ctx := userCtxForIndex(context.Background(), i)
		sid := sliceID(i)
		fid := fileID(i)

		// Verify slice state has a non-empty commit hash.
		stateResp, err := benchSliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sid})
		if err != nil {
			t.Logf("  [user %d] GetSliceState failed: %v", i, err)
			integrityErrors++
			continue
		}
		if stateResp.LatestCommitHash == "" {
			t.Logf("  [user %d] expected non-empty commit hash, got empty", i)
			integrityErrors++
			continue
		}

		// Verify the slice has at least one commit in its history.
		histResp, err := benchSliceClient.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{
			SliceId: sid,
			Limit:   5,
		})
		if err != nil {
			t.Logf("  [user %d] GetSliceCommits failed: %v", i, err)
			integrityErrors++
			continue
		}
		if len(histResp.Commits) == 0 {
			t.Logf("  [user %d] expected at least one commit, got zero", i)
			integrityErrors++
			continue
		}
		if histResp.Commits[0].CommitHash != stateResp.LatestCommitHash {
			t.Logf("  [user %d] latest commit mismatch: state=%s history[0]=%s",
				i, stateResp.LatestCommitHash, histResp.Commits[0].CommitHash)
			integrityErrors++
			continue
		}

		// Verify the file's history is accessible via FileService.
		// Must specify SliceId so the server queries the correct slice's history.
		fileHistResp, err := benchFileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
			Path:    fid,
			SliceId: sid,
		})
		if err != nil {
			t.Logf("  [user %d] GetFileHistory failed: %v", i, err)
			integrityErrors++
			continue
		}
		if len(fileHistResp.Changes) == 0 {
			t.Logf("  [user %d] expected file history for %q, got none", i, fid)
			integrityErrors++
		}
	}

	if integrityErrors > 0 {
		t.Errorf("INTEGRITY FAIL: %d/%d sampled users failed integrity checks", integrityErrors, sampleSize)
	} else {
		t.Logf("Integrity OK: all %d sampled users passed", sampleSize)
	}
}

// runUserSession performs the full changeset workflow for user index i and
// returns timing and outcome information.
func runUserSession(i int) userResult {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = userCtxForIndex(ctx, i)

	r := userResult{userIndex: i}
	sid := sliceID(i)
	fid := fileID(i)

	totalStart := time.Now()

	// 1. Create slice.
	t0 := time.Now()
	_, err := benchSliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
		NewSliceId:    sid,
		Name:          fmt.Sprintf("Bench User %d", i),
		Description:   "benchmark slice",
		FolderPaths:   []string{benchmarkFolderPath(i)},
	})
	r.createSliceMs = float64(time.Since(t0).Microseconds()) / 1000.0
	if err != nil {
		r.err = fmt.Errorf("CreateSliceFromFolder: %w", err)
		return r
	}

	// 2. Create changeset.
	t1 := time.Now()
	csResp, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       sid,
		ModifiedFiles: []string{fid},
		Author:        "bench-worker",
		Message:       fmt.Sprintf("bench change for user %d", i),
		FileContents: []*slicev1.FileContentChange{{
			Path:    fid,
			Content: []byte(fmt.Sprintf("package main\n\nconst benchUser = %d\n", i)),
		}},
	})
	r.createChangeMs = float64(time.Since(t1).Microseconds()) / 1000.0
	if err != nil {
		r.err = fmt.Errorf("CreateChangeset: %w", err)
		return r
	}

	// 3. Merge changeset.
	t2 := time.Now()
	mergeResp, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: csResp.ChangesetId,
	})
	r.mergeMs = float64(time.Since(t2).Microseconds()) / 1000.0
	if err != nil {
		r.err = fmt.Errorf("MergeChangeset: %w", err)
		return r
	}

	r.totalMs = float64(time.Since(totalStart).Microseconds()) / 1000.0
	r.mergeStatus = mergeResp.Status
	return r
}

// TestIntegrity performs a focused integrity check: it creates a small set of
// slices with known data, merges all changesets, then verifies that each
// slice's state, commit history, changeset status, and file history are
// self-consistent.
func TestIntegrity(t *testing.T) {
	const integrityUsers = 50

	ctx := userCtx(context.Background())

	type record struct {
		sid        string
		fid        string
		csID       string
		commitHash string
	}
	records := make([]record, integrityUsers)

	// Use a unique prefix to avoid collision with the load test or other test runs.
	prefix := fmt.Sprintf("integ-%d", time.Now().UnixNano())

	t.Run("Setup", func(t *testing.T) {
		for i := 0; i < integrityUsers; i++ {
			sid := fmt.Sprintf("%s-u%04d", prefix, i)
			fid := fmt.Sprintf("integrity/%s/file.go", sid)
			records[i] = record{sid: sid, fid: fid}

			_, err := benchSliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
				ParentSliceId: "root",
				NewSliceId:    sid,
				Name:          sid,
				FolderPaths:   []string{"integrity"},
			})
			if err != nil {
				t.Fatalf("user %d: CreateSliceFromFolder: %v", i, err)
			}

			csResp, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
				SliceId:       sid,
				ModifiedFiles: []string{fid},
				Author:        "integrity-test",
				Message:       fmt.Sprintf("integrity changeset %d", i),
			})
			if err != nil {
				t.Fatalf("user %d: CreateChangeset: %v", i, err)
			}
			records[i].csID = csResp.ChangesetId

			mergeResp, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
				ChangesetId: csResp.ChangesetId,
			})
			if err != nil {
				t.Fatalf("user %d: MergeChangeset: %v", i, err)
			}
			if mergeResp.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
				t.Fatalf("user %d: expected SUCCESS, got %v", i, mergeResp.Status)
			}
			records[i].commitHash = mergeResp.NewCommitHash
		}
	})

	t.Run("SliceStateConsistency", func(t *testing.T) {
		for i, rec := range records {
			stateResp, err := benchSliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: rec.sid})
			if err != nil {
				t.Errorf("user %d: GetSliceState: %v", i, err)
				continue
			}
			if stateResp.LatestCommitHash != rec.commitHash {
				t.Errorf("user %d: state commit=%q want %q", i, stateResp.LatestCommitHash, rec.commitHash)
			}
		}
	})

	t.Run("CommitHistoryConsistency", func(t *testing.T) {
		for i, rec := range records {
			histResp, err := benchSliceClient.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{
				SliceId: rec.sid,
				Limit:   10,
			})
			if err != nil {
				t.Errorf("user %d: GetSliceCommits: %v", i, err)
				continue
			}
			if len(histResp.Commits) == 0 {
				t.Errorf("user %d: no commits in history", i)
				continue
			}
			if histResp.Commits[0].CommitHash != rec.commitHash {
				t.Errorf("user %d: history[0]=%q want %q",
					i, histResp.Commits[0].CommitHash, rec.commitHash)
			}
		}
	})

	t.Run("ChangesetStatusMerged", func(t *testing.T) {
		for i, rec := range records {
			// Filter by MERGED status explicitly; the default proto value (PENDING=0)
			// would cause the server to return only pending changesets.
			listResp, err := benchSliceClient.ListChangesets(ctx, &slicev1.ListChangesetsRequest{
				SliceId:      rec.sid,
				StatusFilter: slicev1.ChangesetStatus_MERGED,
			})
			if err != nil {
				t.Errorf("user %d: ListChangesets: %v", i, err)
				continue
			}
			found := false
			for _, cs := range listResp.Changesets {
				if cs.ChangesetId == rec.csID {
					found = true
					if cs.Status != slicev1.ChangesetStatus_MERGED {
						t.Errorf("user %d: changeset status=%v want MERGED", i, cs.Status)
					}
				}
			}
			if !found {
				t.Errorf("user %d: changeset %q not found in merged list", i, rec.csID)
			}
		}
	})

	t.Run("FileHistoryConsistency", func(t *testing.T) {
		for i, rec := range records {
			// Must specify SliceId; without it the server defaults to root
			// which does not own the file changes we recorded.
			histResp, err := benchFileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
				Path:    rec.fid,
				SliceId: rec.sid,
			})
			if err != nil {
				t.Errorf("user %d: GetFileHistory(%q): %v", i, rec.fid, err)
				continue
			}
			if len(histResp.Changes) == 0 {
				t.Errorf("user %d: expected file history for %q, got none", i, rec.fid)
			}
		}
	})
}
