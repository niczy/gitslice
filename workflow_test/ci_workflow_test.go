package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/storage"
)

type runnerTokenJSON struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type runnerStartJSON struct {
	RunnerID      string `json:"runner_id"`
	CompletedJobs int    `json:"completed_jobs"`
	DurationMS    int64  `json:"duration_ms"`
}

func TestPathScopedCIWorkflowBlocksFailsAndPassesMerge(t *testing.T) {
	username := workflowUsername(t)
	projectDir := "ci-workflow-" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	remoteRoot := "/" + username + "/" + projectDir
	remoteFile := remoteRoot + "/app/app.txt"

	writeRemoteFileFromString(t, remoteRoot+"/app/.gs-ci.yaml", `
version: 1
name: app
watch:
  - "*.txt"
jobs:
  unit:
    required: true
    commands:
      - grep ready app.txt
`)
	writeRemoteFileFromString(t, remoteFile, "baseline\n")
	writeRemoteFileFromString(t, "/"+username+"/.gitslice/ci.yaml", `
version: 1
triggers:
  changeset_export: true
  merge_requested: true
defaults:
  runner_pool: default
  shell: bash
  timeout_seconds: 30
runner_pools:
  default:
    executor: shell
merge_policy:
  require_success: true
  missing_manifest: block
  allow_force_merge: true
`)

	tokenResp := runCLIJSONOrFail[runnerTokenJSON](t, "", "runner", "token", "create", "--name", "ci-e2e", "--pool", "default", "--ttl", "30m")
	if tokenResp.Token == "" || tokenResp.ExpiresAt == "" {
		t.Fatalf("unexpected runner token response: %+v", tokenResp)
	}
	runCLIOrFail(t, "", "runner", "enroll", "--token", tokenResp.Token, "--executor", "shell")
	drainRunnerQueue(t, 5)

	checkoutDir := t.TempDir()
	_ = runCLIJSONOrFail[sliceCheckoutJSON](t, checkoutDir, "slice", "checkout", homeslice.IDForUsername(username), "--here")
	localFile := filepath.Join(checkoutDir, username, projectDir, "app", "app.txt")
	if err := os.WriteFile(localFile, []byte("fail\n"), 0o600); err != nil {
		t.Fatalf("write failing local file: %v", err)
	}

	exportFail := runCLIJSONOrFail[slicePublishJSON](t, checkoutDir, "slice", "export", "--message", "ci failing export", "--files", strings.TrimPrefix(remoteFile, "/"))
	if exportFail.Changeset.ChangesetID == "" || !exportFail.ReviewOnly {
		t.Fatalf("unexpected failing export response: %+v", exportFail)
	}
	output, err := runCLIWithDirInputEnvLegacyUser(checkoutDir, "", workflowProcessEnv(t, nil), true, username, "changeset", "merge")
	if err == nil {
		t.Fatalf("expected merge to block before CI executes\nOutput:\n%s", output)
	}
	if !strings.Contains(output, "CI required checks") {
		t.Fatalf("expected CI gate error before runner, got:\n%s", output)
	}

	statusAfterFailRun := runRunnerUntilCIStatus(t, checkoutDir, "failed")
	output, err = runCLIWithDirInputEnvLegacyUser(checkoutDir, "", workflowProcessEnv(t, nil), true, username, "changeset", "merge")
	if err == nil {
		t.Fatalf("expected merge to block after failing CI\nOutput:\n%s", output)
	}
	if !strings.Contains(output, "CI required checks failed") {
		t.Fatalf("expected failed CI gate error, got:\n%s\nCI status after runner:\n%s", output, statusAfterFailRun)
	}

	if err := os.WriteFile(localFile, []byte("ready\n"), 0o600); err != nil {
		t.Fatalf("write passing local file: %v", err)
	}
	exportPass := runCLIJSONOrFail[slicePublishJSON](t, checkoutDir, "slice", "export", "--message", "ci passing export", "--files", strings.TrimPrefix(remoteFile, "/"))
	if exportPass.Changeset.ChangesetID != exportFail.Changeset.ChangesetID || exportPass.ReusedExisting {
		t.Fatalf("expected export to append the tracked changeset, got fail=%+v pass=%+v", exportFail, exportPass)
	}
	_ = runRunnerUntilCIStatus(t, checkoutDir, "passed")
	mergeResp := runCLIJSONOrFail[mergeJSON](t, checkoutDir, "changeset", "merge", "--wait")
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" || mergeResp.NewCommitHash == "" {
		t.Fatalf("unexpected merge response: %+v", mergeResp)
	}

	if got := runCLIOrFail(t, "", "fs", "cat", remoteFile); got != "ready\n" {
		t.Fatalf("home file content = %q, want ready", got)
	}
	assertHomeSliceCheckoutFileContent(t, username, projectDir, "ready\n")
}

func writeRemoteFileFromString(t *testing.T, remotePath, content string) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(tmp, []byte(strings.TrimPrefix(content, "\n")), 0o600); err != nil {
		t.Fatalf("write temp input: %v", err)
	}
	output := runCLIOrFail(t, "", "fs", "write", remotePath, "-f", tmp)
	commitHash := extractFilesystemCommitHash(output)
	waitForRemoteHomeHead(t, remotePath, commitHash)
	waitForRemoteHomeProjection(t, remotePath)
}

func waitForRemoteHomeHead(t *testing.T, remotePath, commitHash string) {
	t.Helper()
	if commitHash == "" || testStorage == nil {
		return
	}
	parts := strings.Split(strings.TrimPrefix(remotePath, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return
	}
	homeID := homeslice.IDForUsername(parts[0])
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitForCondition(3*time.Second, 25*time.Millisecond, func() (bool, error) {
		metadata, err := testStorage.GetSliceMetadata(ctx, homeID)
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(metadata.HeadCommitHash) == commitHash, nil
	}); err != nil {
		t.Fatalf("expected home %s head to reach %s after fs write %s: %v", homeID, commitHash, remotePath, err)
	}
}

func waitForRemoteHomeProjection(t *testing.T, remotePath string) {
	t.Helper()
	if testStorage == nil {
		return
	}
	rootPath := strings.TrimPrefix(remotePath, "/")
	if strings.TrimSpace(rootPath) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitForCondition(3*time.Second, 25*time.Millisecond, func() (bool, error) {
		if _, err := storage.ReadSliceFileContent(ctx, testStorage, "root", rootPath); err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}); err != nil {
		t.Fatalf("expected root path-head projection for %s: %v", remotePath, err)
	}
}

func drainRunnerQueue(t *testing.T, maxJobs int) {
	t.Helper()
	for i := 0; i < maxJobs; i++ {
		resp := runCLIJSONOrFail[runnerStartJSON](t, "", "runner", "start", "--once", "--workdir", t.TempDir())
		if resp.CompletedJobs == 0 {
			return
		}
	}
	t.Fatalf("runner queue did not drain after %d jobs", maxJobs)
}

func runRunnerUntilCIStatus(t *testing.T, checkoutDir, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastStatus string
	for time.Now().Before(deadline) {
		lastStatus = runCLIOrFail(t, checkoutDir, "ci", "status")
		if strings.Contains(lastStatus, "app/unit  "+want) {
			return lastStatus
		}
		resp := runCLIJSONOrFail[runnerStartJSON](t, "", "runner", "start", "--once", "--workdir", t.TempDir())
		if resp.CompletedJobs == 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Fatalf("CI status did not become %q before timeout; last status:\n%s", want, lastStatus)
	return lastStatus
}

func assertHomeSliceCheckoutFileContent(t *testing.T, username, projectDir, want string) {
	t.Helper()
	checkoutDir := t.TempDir()
	_ = runCLIJSONOrFail[sliceCheckoutJSON](t, checkoutDir, "slice", "checkout", homeslice.IDForUsername(username), "--here")
	data, err := os.ReadFile(filepath.Join(checkoutDir, username, projectDir, "app", "app.txt"))
	if err != nil {
		t.Fatalf("read home checkout file: %v", err)
	}
	if string(data) != want {
		t.Fatalf("home checkout file content = %q, want %q", data, want)
	}
}
