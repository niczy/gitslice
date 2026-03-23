package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	perfFixtureDirCount     = 12
	perfFixtureFilesPerDir  = 25
	perfCheckoutColdBudget  = 12 * time.Second
	perfCheckoutWarmBudget  = 6 * time.Second
	perfStatusBudget        = 2 * time.Second
	perfPublishReviewBudget = 5 * time.Second
)

func requireWorkflowPerfTests(t *testing.T) {
	t.Helper()
	if os.Getenv("RUN_WORKFLOW_PERF_TESTS") != "1" {
		t.Skip("set RUN_WORKFLOW_PERF_TESTS=1 to run workflow perf regression tests")
	}
}

func workflowPerfBudget(t *testing.T, envName string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(envName))
	if raw == "" {
		return fallback
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		t.Fatalf("invalid %s=%q: expected positive integer milliseconds", envName, raw)
	}
	return time.Duration(ms) * time.Millisecond
}

func timeCLI(t *testing.T, workdir string, args ...string) (string, time.Duration) {
	t.Helper()
	start := time.Now()
	output := runCLIOrFail(t, workdir, args...)
	return output, time.Since(start)
}

func buildPerfFixtureSlice(t *testing.T, basePath string) string {
	t.Helper()

	rootWorkdir := t.TempDir()
	_ = runCLIOrFail(t, rootWorkdir, "init", sliceIDArg("root_slice"))

	paths := make([]string, 0, perfFixtureDirCount*perfFixtureFilesPerDir)
	for dirIdx := 0; dirIdx < perfFixtureDirCount; dirIdx++ {
		dirPath := fmt.Sprintf("%s/dir-%02d", basePath, dirIdx)
		for fileIdx := 0; fileIdx < perfFixtureFilesPerDir; fileIdx++ {
			paths = append(paths, fmt.Sprintf("%s/file-%03d.txt", dirPath, fileIdx))
		}
	}

	createResp := runCLIJSONOrFail[changesetCreateJSON](
		t,
		rootWorkdir,
		"changeset",
		"create",
		"--message", "seed workflow perf fixture",
		"--files", strings.Join(paths, ","),
	)
	if createResp.ChangesetID == "" {
		t.Fatalf("expected changeset ID while seeding perf fixture")
	}

	mergeResp := runCLIJSONOrFail[mergeJSON](t, rootWorkdir, "changeset", "merge", createResp.ChangesetID)
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected seed merge success, got %+v", mergeResp)
	}

	sliceResp := runCLIJSONOrFail[sliceCreateJSON](
		t,
		rootWorkdir,
		"slice",
		"create",
		fmt.Sprintf("perf-slice-%d", time.Now().UnixNano()),
		basePath,
	)
	if sliceResp.SliceID == "" {
		t.Fatalf("expected perf slice ID")
	}
	return sliceResp.SliceID
}

func TestPerfSliceCheckoutWarmCache(t *testing.T) {
	requireWorkflowPerfTests(t)

	sliceID := buildPerfFixtureSlice(t, fmt.Sprintf("apps/perf-checkout-%d", time.Now().UnixNano()))
	coldBudget := workflowPerfBudget(t, "WORKFLOW_PERF_CHECKOUT_COLD_MAX_MS", perfCheckoutColdBudget)
	warmBudget := workflowPerfBudget(t, "WORKFLOW_PERF_CHECKOUT_WARM_MAX_MS", perfCheckoutWarmBudget)

	firstDir := t.TempDir()
	_, coldDuration := timeCLI(t, firstDir, "slice", "checkout", sliceIDArg(sliceID))

	secondDir := t.TempDir()
	_, warmDuration := timeCLI(t, secondDir, "slice", "checkout", sliceIDArg(sliceID))

	t.Logf("slice checkout perf: cold=%s warm=%s", coldDuration, warmDuration)
	if coldDuration > coldBudget {
		t.Fatalf("cold checkout exceeded budget: got %s want <= %s", coldDuration, coldBudget)
	}
	if warmDuration > warmBudget {
		t.Fatalf("warm checkout exceeded budget: got %s want <= %s", warmDuration, warmBudget)
	}
}

func TestPerfSliceStatusNoGit(t *testing.T) {
	requireWorkflowPerfTests(t)

	basePath := fmt.Sprintf("apps/perf-status-%d", time.Now().UnixNano())
	sliceID := buildPerfFixtureSlice(t, basePath)
	statusBudget := workflowPerfBudget(t, "WORKFLOW_PERF_STATUS_MAX_MS", perfStatusBudget)

	checkoutDir := t.TempDir()
	_ = runCLIOrFail(t, checkoutDir, "slice", "checkout", sliceIDArg(sliceID))

	start := time.Now()
	statusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutDir, "slice", "status")
	duration := time.Since(start)

	t.Logf("slice status perf: duration=%s paths=%d", duration, statusResp.PathCount)
	if statusResp.WorkingTree != "clean" || statusResp.PathCount != 0 {
		t.Fatalf("expected clean status output, got %+v", statusResp)
	}
	if duration > statusBudget {
		t.Fatalf("slice status exceeded budget: got %s want <= %s", duration, statusBudget)
	}
}

func TestPerfSlicePublishReviewNoGit(t *testing.T) {
	requireWorkflowPerfTests(t)

	basePath := fmt.Sprintf("apps/perf-publish-%d", time.Now().UnixNano())
	sliceID := buildPerfFixtureSlice(t, basePath)
	publishBudget := workflowPerfBudget(t, "WORKFLOW_PERF_PUBLISH_MAX_MS", perfPublishReviewBudget)

	checkoutDir := t.TempDir()
	_ = runCLIOrFail(t, checkoutDir, "slice", "checkout", sliceIDArg(sliceID))

	targetFile := filepath.Join(checkoutDir, basePath, "dir-00", "file-000.txt")
	if err := os.WriteFile(targetFile, []byte("perf publish review change\n"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	newFile := filepath.Join(checkoutDir, basePath, "dir-00", "new-file.txt")
	if err := os.WriteFile(newFile, []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	start := time.Now()
	publishResp := runCLIJSONOrFail[slicePublishJSON](t, checkoutDir, "slice", "publish", "--review-only", "--message", "workflow perf publish")
	duration := time.Since(start)

	t.Logf(
		"slice publish perf: duration=%s files_added=%d files_modified=%d",
		duration,
		publishResp.Review.Diff.FilesAdded,
		publishResp.Review.Diff.FilesModified,
	)
	if publishResp.Changeset.ChangesetID == "" || !publishResp.ReviewOnly {
		t.Fatalf("expected review-only publish output, got %+v", publishResp)
	}
	if duration > publishBudget {
		t.Fatalf("slice publish review exceeded budget: got %s want <= %s", duration, publishBudget)
	}
}
