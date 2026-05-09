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

	"github.com/niczy/gitslice/internal/models"
	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

type mergeAcceptanceRecord struct {
	userIndex   int
	sliceID     string
	fileID      string
	homeID      string
	changesetID string
	commitHash  string
}

type mergeAcceptanceResult struct {
	userIndex  int
	mergeMs    float64
	status     slicev1.MergeStatus
	commitHash string
	err        error
}

// TestMergeAcceptanceThroughput pre-creates ready-to-merge changesets, then
// measures only the concurrent MergeChangeset acceptance path.
func TestMergeAcceptanceThroughput(t *testing.T) {
	numUsers, err := benchmarkUserCountFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	numWorkers := benchmarkWorkerCountFromEnv(t)
	skippedInShortMode(t, numUsers)

	prefix := fmt.Sprintf("mergebench-%d", time.Now().UnixNano())
	t.Logf("Preparing merge acceptance benchmark: %d ready changesets, %d workers", numUsers, numWorkers)

	records := prepareMergeAcceptanceChangesets(t, prefix, numUsers, numWorkers)
	t.Logf("Prepared %d changesets; starting timed merge acceptance phase", len(records))

	tasks := make(chan mergeAcceptanceRecord, numWorkers*2)
	results := make(chan mergeAcceptanceResult, numWorkers*4)

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rec := range tasks {
				results <- runMergeAcceptance(rec)
			}
		}()
	}

	foregroundPoolObserver := startBenchmarkPostgresPoolObserver()
	start := time.Now()

	go func() {
		for _, rec := range records {
			tasks <- rec
		}
		close(tasks)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	allResults := make([]mergeAcceptanceResult, 0, len(records))
	for result := range results {
		allResults = append(allResults, result)
	}
	elapsed := time.Since(start).Seconds()
	foregroundPoolReport, foregroundPoolStatsOK := foregroundPoolObserver.stopAndReport()

	var successCount, conflictCount, errorCount int64
	latencies := make([]float64, 0, len(allResults))
	commitsByIndex := make(map[int]string, len(allResults))
	for _, result := range allResults {
		if result.err != nil {
			atomic.AddInt64(&errorCount, 1)
			continue
		}
		switch result.status {
		case slicev1.MergeStatus_MERGE_STATUS_SUCCESS:
			atomic.AddInt64(&successCount, 1)
			latencies = append(latencies, result.mergeMs)
			commitsByIndex[result.userIndex] = result.commitHash
		case slicev1.MergeStatus_MERGE_STATUS_CONFLICT:
			atomic.AddInt64(&conflictCount, 1)
		default:
			atomic.AddInt64(&errorCount, 1)
		}
	}
	sort.Float64s(latencies)
	throughput := float64(successCount) / elapsed

	t.Logf("=== Merge Acceptance Results ===")
	t.Logf("Changesets prepared: %d", len(records))
	t.Logf("Workers:             %d", numWorkers)
	t.Logf("Elapsed:             %.2f s (merge acceptance only)", elapsed)
	t.Logf("Merge throughput:    %.1f merges/sec", throughput)
	t.Logf("")
	t.Logf("Outcomes:")
	t.Logf("  Successful merges: %d", successCount)
	t.Logf("  Conflicts:         %d", conflictCount)
	t.Logf("  Errors:            %d", errorCount)
	if len(latencies) > 0 {
		t.Logf("")
		t.Logf("MergeChangeset latency (ms):")
		t.Logf("  P50:  %.2f", percentile(latencies, 50))
		t.Logf("  P95:  %.2f", percentile(latencies, 95))
		t.Logf("  P99:  %.2f", percentile(latencies, 99))
		t.Logf("  Max:  %.2f", latencies[len(latencies)-1])
	}
	logBenchmarkPostgresPoolReport(t, "Merge acceptance", foregroundPoolReport, foregroundPoolStatsOK)

	drainPoolObserver := startBenchmarkPromotionPostgresPoolObserver()
	promotionDrainElapsed, promotionDrainObserved, promotionDrainErr := drainBenchmarkPromotions(30 * time.Second)
	drainPoolReport, drainPoolStatsOK := drainPoolObserver.stopAndReport()
	if promotionDrainObserved {
		if promotionDrainErr != nil {
			t.Logf("Promotion drain elapsed: %.2f s (error: %v)", promotionDrainElapsed.Seconds(), promotionDrainErr)
		} else {
			t.Logf("Promotion drain elapsed: %.2f s", promotionDrainElapsed.Seconds())
		}
	} else {
		t.Logf("Promotion drain elapsed: unavailable")
	}
	logBenchmarkPostgresPoolReport(t, "Promotion drain", drainPoolReport, drainPoolStatsOK)

	if errorCount > 0 {
		t.Errorf("INTEGRITY FAIL: %d merges encountered errors", errorCount)
	}
	if conflictCount > 0 {
		t.Errorf("INTEGRITY FAIL: unexpected conflicts detected (%d). Each prepared changeset owns a unique file.", conflictCount)
	}
	if int(successCount) != len(records) {
		t.Errorf("INTEGRITY FAIL: expected %d successful merges, got %d", len(records), successCount)
	}

	runMergeAcceptanceIntegritySample(t, records, commitsByIndex)
}

func benchmarkWorkerCountFromEnv(t *testing.T) int {
	t.Helper()
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
	return numWorkers
}

func prepareMergeAcceptanceChangesets(t *testing.T, prefix string, numUsers, numWorkers int) []mergeAcceptanceRecord {
	t.Helper()
	tasks := make(chan int, numWorkers*2)
	records := make([]mergeAcceptanceRecord, numUsers)
	var errorsCount int64

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range tasks {
				rec, err := prepareMergeAcceptanceChangeset(prefix, i)
				if err != nil {
					atomic.AddInt64(&errorsCount, 1)
					t.Logf("prepare %d failed: %v", i, err)
					continue
				}
				records[i] = rec
			}
		}()
	}
	for i := 0; i < numUsers; i++ {
		tasks <- i
	}
	close(tasks)
	wg.Wait()

	if errorsCount > 0 {
		t.Fatalf("failed to prepare %d/%d changesets", errorsCount, numUsers)
	}
	return records
}

func prepareMergeAcceptanceChangeset(prefix string, i int) (mergeAcceptanceRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = userCtxForIndex(ctx, i)

	sid := fmt.Sprintf("%s-slice-%07d", prefix, i)
	folder := fmt.Sprintf("%s-%07d", prefix, i)
	fid := fmt.Sprintf("%s/%s-main.go", folder, prefix)

	if err := seedBenchmarkRootDirectory(ctx, benchStorage, folder); err != nil {
		return mergeAcceptanceRecord{}, fmt.Errorf("seed root folder: %w", err)
	}

	if _, err := benchSliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root",
		NewSliceId:    sid,
		Name:          fmt.Sprintf("Merge Bench %d", i),
		Description:   "merge acceptance benchmark slice",
		FolderPaths:   []string{folder},
	}); err != nil {
		return mergeAcceptanceRecord{}, fmt.Errorf("CreateSliceFromFolder: %w", err)
	}

	csResp, err := benchSliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       sid,
		ModifiedFiles: []string{fid},
		Author:        "merge-bench",
		Message:       fmt.Sprintf("merge acceptance change %d", i),
		FileContents: []*slicev1.FileContentChange{{
			Path:    fid,
			Content: []byte(fmt.Sprintf("package main\n\nconst mergeBenchUser = %d\n", i)),
		}},
	})
	if err != nil {
		return mergeAcceptanceRecord{}, fmt.Errorf("CreateChangeset: %w", err)
	}
	return mergeAcceptanceRecord{
		userIndex:   i,
		sliceID:     sid,
		fileID:      fid,
		homeID:      folder,
		changesetID: csResp.GetChangesetId(),
	}, nil
}

func runMergeAcceptance(rec mergeAcceptanceRecord) mergeAcceptanceResult {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = userCtxForIndex(ctx, rec.userIndex)

	start := time.Now()
	mergeResp, err := benchSliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: rec.changesetID,
	})
	result := mergeAcceptanceResult{
		userIndex: rec.userIndex,
		mergeMs:   float64(time.Since(start).Microseconds()) / 1000.0,
	}
	if err != nil {
		result.err = fmt.Errorf("MergeChangeset: %w", err)
		return result
	}
	result.status = mergeResp.GetStatus()
	result.commitHash = mergeResp.GetNewCommitHash()
	return result
}

func runMergeAcceptanceIntegritySample(t *testing.T, records []mergeAcceptanceRecord, commitsByIndex map[int]string) {
	t.Helper()
	if len(records) == 0 {
		return
	}
	sampleSize := len(records) / 100
	if sampleSize < 10 {
		sampleSize = 10
	}
	if sampleSize > len(records) {
		sampleSize = len(records)
	}
	t.Logf("Running merge acceptance integrity checks on %d changesets (1%%)...", sampleSize)

	perm := rand.Perm(len(records))
	integrityErrors := 0
	for _, idx := range perm[:sampleSize] {
		rec := records[idx]
		ctx := userCtxForIndex(context.Background(), rec.userIndex)
		commitHash := commitsByIndex[rec.userIndex]
		if commitHash == "" {
			t.Logf("  [user %d] missing commit hash from merge result", rec.userIndex)
			integrityErrors++
			continue
		}

		stateResp, err := benchSliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: rec.sliceID})
		if err != nil {
			t.Logf("  [user %d] GetSliceState failed: %v", rec.userIndex, err)
			integrityErrors++
			continue
		}
		if stateResp.GetLatestCommitHash() != commitHash {
			t.Logf("  [user %d] state commit mismatch: state=%s merge=%s", rec.userIndex, stateResp.GetLatestCommitHash(), commitHash)
			integrityErrors++
			continue
		}

		eventStore, eventOK := benchStorage.(interface {
			GetMergeEventByChangeset(context.Context, string) (*models.MergeEvent, error)
		})
		headStore, headOK := benchStorage.(interface {
			GetHomePathHeads(context.Context, string, []string) (map[string]*models.HomePathHead, error)
		})
		if eventOK {
			event, err := eventStore.GetMergeEventByChangeset(ctx, rec.changesetID)
			if err != nil {
				t.Logf("  [user %d] GetMergeEventByChangeset failed: %v", rec.userIndex, err)
				integrityErrors++
				continue
			}
			if event.SourceCommitHash != commitHash || len(event.PathUpdates) != 1 || event.PathUpdates[0].Path != rec.fileID {
				t.Logf("  [user %d] merge event mismatch: merge=%s event=%#v", rec.userIndex, commitHash, event)
				integrityErrors++
				continue
			}
		}
		if headOK {
			heads, err := headStore.GetHomePathHeads(ctx, rec.homeID, []string{rec.fileID})
			if err != nil {
				t.Logf("  [user %d] GetHomePathHeads failed: %v", rec.userIndex, err)
				integrityErrors++
				continue
			}
			head := heads[rec.fileID]
			if head == nil || head.SourceCommitHash != commitHash || head.PathVersion <= 0 {
				t.Logf("  [user %d] path head mismatch: merge=%s head=%#v", rec.userIndex, commitHash, head)
				integrityErrors++
				continue
			}
		}

		if !eventOK && !headOK {
			fileHistResp, err := benchFileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
				Path:    rec.fileID,
				SliceId: rec.sliceID,
			})
			if err != nil {
				t.Logf("  [user %d] GetFileHistory failed: %v", rec.userIndex, err)
				integrityErrors++
				continue
			}
			if len(fileHistResp.GetChanges()) == 0 {
				t.Logf("  [user %d] expected file history for %q, got none", rec.userIndex, rec.fileID)
				integrityErrors++
			}
		}
	}

	if integrityErrors > 0 {
		t.Errorf("INTEGRITY FAIL: %d/%d sampled merge acceptance records failed integrity checks", integrityErrors, sampleSize)
		return
	}
	t.Logf("Integrity OK: all %d sampled merge acceptance records passed", sampleSize)
}
