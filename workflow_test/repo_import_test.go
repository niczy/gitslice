package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/homeslice"
)

func createLocalGitRemote(t *testing.T) (string, string) {
	t.Helper()

	baseDir := t.TempDir()
	remoteDir := filepath.Join(baseDir, "remote.git")
	sourceDir := filepath.Join(baseDir, "source")

	runGitOrFail(t, "", "init", "--bare", "--initial-branch=main", remoteDir)
	runGitOrFail(t, "", "clone", remoteDir, sourceDir)
	runGitOrFail(t, sourceDir, "config", "user.name", "repo-test")
	runGitOrFail(t, sourceDir, "config", "user.email", "repo-test@example.com")

	readmePath := filepath.Join(sourceDir, "README.md")
	scriptPath := filepath.Join(sourceDir, "bin", "tool.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(readmePath, []byte("version 1\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\necho repo import\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.Symlink("README.md", filepath.Join(sourceDir, "README.link")); err != nil {
		t.Fatalf("write symlink: %v", err)
	}

	runGitOrFail(t, sourceDir, "add", "-A")
	runGitOrFail(t, sourceDir, "commit", "-m", "initial import")
	runGitOrFail(t, sourceDir, "push", "-u", "origin", "main")

	return remoteDir, sourceDir
}

func TestRepoImportCLIWorkflowEndToEnd(t *testing.T) {
	username := fmt.Sprintf("repoimport%d", time.Now().UnixNano()%1_000_000_000)
	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir CLI home: %v", err)
	}
	env := map[string]string{"HOME": homeDir}

	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}
	runCLIJSONForUser := func(workdir string, args ...string) string {
		t.Helper()
		args = append(args, "--json")
		return runCLIForUser(workdir, args...)
	}

	remoteDir, sourceDir := createLocalGitRemote(t)
	boundPath := fmt.Sprintf("/%s/repos/demo-%d", username, time.Now().UnixNano())
	readmePath := boundPath + "/README.md"

	importOutput := runCLIJSONForUser("", "repo", "import", remoteDir, boundPath)
	var importResp repoImportJSON
	if err := json.Unmarshal([]byte(importOutput), &importResp); err != nil {
		t.Fatalf("decode repo import JSON: %v\nOutput:\n%s", err, importOutput)
	}
	if importResp.Path != boundPath || importResp.RepoURL != remoteDir || importResp.Branch != "main" {
		t.Fatalf("expected import output, got: %+v", importResp)
	}

	output := runCLIForUser("", "fs", "cat", readmePath)
	if output != "version 1\n" {
		t.Fatalf("unexpected imported README: %q", output)
	}

	checkoutDir := filepath.Join(t.TempDir(), "repo-checkout")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatalf("mkdir checkout: %v", err)
	}
	output = runCLIForUser(checkoutDir, "slice", "checkout", homeslice.IDForUsername(username), "--here", "--json")
	var checkoutResp sliceCheckoutJSON
	if err := json.Unmarshal([]byte(output), &checkoutResp); err != nil {
		t.Fatalf("decode checkout JSON: %v\nOutput:\n%s", err, output)
	}
	if checkoutResp.SliceID != homeslice.IDForUsername(username) {
		t.Fatalf("expected home slice checkout output, got: %+v", checkoutResp)
	}

	scriptInfo, err := os.Lstat(filepath.Join(checkoutDir, username, "repos", filepath.Base(boundPath), "bin", "tool.sh"))
	if err != nil {
		t.Fatalf("lstat checked out executable: %v", err)
	}
	if scriptInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected checked out script to be executable, mode=%v", scriptInfo.Mode())
	}
	linkTarget, err := os.Readlink(filepath.Join(checkoutDir, username, "repos", filepath.Base(boundPath), "README.link"))
	if err != nil {
		t.Fatalf("read checked out symlink: %v", err)
	}
	if linkTarget != "README.md" {
		t.Fatalf("unexpected symlink target: %q", linkTarget)
	}

	if err := os.WriteFile(filepath.Join(sourceDir, "README.md"), []byte("version 2 from remote\n"), 0o644); err != nil {
		t.Fatalf("rewrite remote README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "CHANGELOG.md"), []byte("remote changelog\n"), 0o644); err != nil {
		t.Fatalf("write changelog: %v", err)
	}
	runGitOrFail(t, sourceDir, "add", "-A")
	runGitOrFail(t, sourceDir, "commit", "-m", "remote update")
	runGitOrFail(t, sourceDir, "push", "origin", "main")

	failedOutput, err := runCLIWithDirInputEnvLegacyUser("", "", env, true, username, "repo", "import", remoteDir, boundPath, "--json")
	if err == nil {
		t.Fatalf("expected repo import without --force to fail for populated path, got:\n%s", failedOutput)
	}
	if !strings.Contains(failedOutput, "target path already contains files") {
		t.Fatalf("expected populated path error, got:\n%s", failedOutput)
	}

	forceOutput := runCLIJSONForUser("", "repo", "import", "--force", remoteDir, boundPath)
	var forceResp repoImportJSON
	if err := json.Unmarshal([]byte(forceOutput), &forceResp); err != nil {
		t.Fatalf("decode force repo import JSON: %v\nOutput:\n%s", err, forceOutput)
	}
	if forceResp.Path != boundPath || forceResp.CommitHash == "" {
		t.Fatalf("expected force import output, got: %+v", forceResp)
	}

	output = runCLIForUser("", "fs", "cat", readmePath)
	if output != "version 2 from remote\n" {
		t.Fatalf("unexpected README after force import: %q", output)
	}
	output = runCLIForUser("", "fs", "cat", boundPath+"/CHANGELOG.md")
	if output != "remote changelog\n" {
		t.Fatalf("unexpected changelog after force import: %q", output)
	}
}

func TestDetachedRepoImportJobWorkflow(t *testing.T) {
	username := fmt.Sprintf("repojob%d", time.Now().UnixNano()%1_000_000_000)
	env := workflowProcessEnv(t, nil)
	runCLIJSONForUser := func(workdir string, args ...string) string {
		t.Helper()
		args = append(args, "--json")
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	remoteDir, _ := createLocalGitRemote(t)
	boundPath := fmt.Sprintf("/%s/repos/detached-%d", username, time.Now().UnixNano())

	startOutput := runCLIJSONForUser("", "repo", "import", "--detach", remoteDir, boundPath)
	var started jobJSON
	if err := json.Unmarshal([]byte(startOutput), &started); err != nil {
		t.Fatalf("decode detached job JSON: %v\nOutput:\n%s", err, startOutput)
	}
	if started.JobID == "" || started.Kind != "repo import" {
		t.Fatalf("unexpected detached job start output: %+v", started)
	}

	waitOutput := runCLIJSONForUser("", "jobs", "wait", started.JobID, "--timeout", "10s")
	var completed jobJSON
	if err := json.Unmarshal([]byte(waitOutput), &completed); err != nil {
		t.Fatalf("decode completed job JSON: %v\nOutput:\n%s", err, waitOutput)
	}
	if completed.Status != "succeeded" || completed.ExitCode != 0 {
		t.Fatalf("expected successful detached job, got: %+v", completed)
	}
	if len(completed.Result) == 0 {
		t.Fatalf("expected detached job result JSON, got: %+v", completed)
	}

	var importResp repoImportJSON
	if err := json.Unmarshal(completed.Result, &importResp); err != nil {
		t.Fatalf("decode repo import job result: %v\nResult:\n%s", err, string(completed.Result))
	}
	if importResp.Path != boundPath || importResp.RepoURL != remoteDir {
		t.Fatalf("unexpected repo import job result: %+v", importResp)
	}

	getOutput := runCLIJSONForUser("", "jobs", "get", started.JobID)
	var fetched jobJSON
	if err := json.Unmarshal([]byte(getOutput), &fetched); err != nil {
		t.Fatalf("decode jobs get JSON: %v\nOutput:\n%s", err, getOutput)
	}
	if fetched.JobID != started.JobID || fetched.Status != "succeeded" {
		t.Fatalf("unexpected jobs get output: %+v", fetched)
	}

	logsOutput := runCLIJSONForUser("", "jobs", "logs", started.JobID)
	var logs jobLogsJSON
	if err := json.Unmarshal([]byte(logsOutput), &logs); err != nil {
		t.Fatalf("decode jobs logs JSON: %v\nOutput:\n%s", err, logsOutput)
	}
	if logs.JobID != started.JobID || !strings.Contains(logs.Stdout, "\"repo_url\"") {
		t.Fatalf("unexpected jobs logs output: %+v", logs)
	}

	listOutput := runCLIJSONForUser("", "jobs", "list")
	var list jobsListJSON
	if err := json.Unmarshal([]byte(listOutput), &list); err != nil {
		t.Fatalf("decode jobs list JSON: %v\nOutput:\n%s", err, listOutput)
	}
	found := false
	for _, job := range list.Jobs {
		if job.JobID == started.JobID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected jobs list to include %s, got: %+v", started.JobID, list)
	}

	output, err := runCLIWithDirInputEnvLegacyUser("", "", env, true, username, "fs", "cat", boundPath+"/README.md")
	if err != nil {
		t.Fatalf("CLI cat after detached import failed: %v\nOutput:\n%s", err, output)
	}
	if output != "version 1\n" {
		t.Fatalf("unexpected imported README after detached import: %q", output)
	}
}

func TestFilesystemEnsureDirIsIdempotent(t *testing.T) {
	username := fmt.Sprintf("ensuredir%d", time.Now().UnixNano()%1_000_000_000)
	env := workflowProcessEnv(t, nil)
	runCLIJSONForUser := func(workdir string, args ...string) string {
		t.Helper()
		args = append(args, "--json")
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	dirPath := fmt.Sprintf("/%s/ensure-dir-%d", username, time.Now().UnixNano())
	var created filesystemActionJSON
	if err := json.Unmarshal([]byte(runCLIJSONForUser("", "fs", "ensure-dir", dirPath)), &created); err != nil {
		t.Fatalf("decode ensure-dir create JSON: %v", err)
	}
	if created.Action != "ensure-dir" || created.Status != "created" {
		t.Fatalf("unexpected ensure-dir create response: %+v", created)
	}
	var existing filesystemActionJSON
	if err := json.Unmarshal([]byte(runCLIJSONForUser("", "fs", "ensure-dir", dirPath)), &existing); err != nil {
		t.Fatalf("decode ensure-dir existing JSON: %v", err)
	}
	if existing.Status != "exists" {
		t.Fatalf("unexpected ensure-dir existing response: %+v", existing)
	}
}
