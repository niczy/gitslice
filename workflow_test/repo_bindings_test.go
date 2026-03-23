package workflow

import (
	"context"
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
	if err := os.WriteFile(scriptPath, []byte("#!/usr/bin/env bash\necho repo binding\n"), 0o755); err != nil {
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

func TestRepoBindingCLIWorkflowEndToEnd(t *testing.T) {
	_, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	username := fmt.Sprintf("repocli%d", time.Now().UnixNano()%1_000_000_000)

	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", nil, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	remoteDir, sourceDir := createLocalGitRemote(t)
	boundPath := fmt.Sprintf("/%s/repos/demo-%d", username, time.Now().UnixNano())
	readmePath := boundPath + "/README.md"

	output := runCLIForUser("", "repo", "import", "--push-enabled", remoteDir, boundPath)
	if !strings.Contains(output, "Imported "+remoteDir+" into "+boundPath) {
		t.Fatalf("expected import output, got: %s", output)
	}
	if !strings.Contains(output, "Branch: main") {
		t.Fatalf("expected import branch output, got: %s", output)
	}

	output = runCLIForUser("", "repo", "list")
	if !strings.Contains(output, boundPath) || !strings.Contains(output, remoteDir) {
		t.Fatalf("expected repo list output to include binding, got: %s", output)
	}

	output = runCLIForUser("", "repo", "status", boundPath)
	if !strings.Contains(output, "Push enabled: true") {
		t.Fatalf("expected status to show push enabled, got: %s", output)
	}

	output = runCLIForUser("", "fs", "cat", readmePath)
	if output != "version 1\n" {
		t.Fatalf("unexpected imported README: %q", output)
	}

	checkoutDir := filepath.Join(t.TempDir(), "repo-checkout")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatalf("mkdir checkout: %v", err)
	}
	output = runCLIForUser(checkoutDir, "slice", "checkout", homeslice.IDForUsername(username), "--json")
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

	output = runCLIForUser("", "repo", "pull", boundPath)
	if !strings.Contains(output, "Pulled") || !strings.Contains(output, "Home commit: ") {
		t.Fatalf("expected pull output, got: %s", output)
	}

	output = runCLIForUser("", "fs", "cat", readmePath)
	if output != "version 2 from remote\n" {
		t.Fatalf("unexpected README after pull: %q", output)
	}
	output = runCLIForUser("", "fs", "cat", boundPath+"/CHANGELOG.md")
	if output != "remote changelog\n" {
		t.Fatalf("unexpected changelog after pull: %q", output)
	}

	localReadme := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(localReadme, []byte("version 3 from home slice\n"), 0o644); err != nil {
		t.Fatalf("write local README: %v", err)
	}
	output = runCLIForUser("", "fs", "write", readmePath, "-f", localReadme)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected fs write output, got: %s", output)
	}

	output = runCLIForUser("", "repo", "push", "--message", "push repo binding changes", boundPath)
	if !strings.Contains(output, "Pushed") {
		t.Fatalf("expected push output, got: %s", output)
	}

	runGitOrFail(t, sourceDir, "pull", "--ff-only", "origin", "main")
	readmeContent, err := os.ReadFile(filepath.Join(sourceDir, "README.md"))
	if err != nil {
		t.Fatalf("read source README after push: %v", err)
	}
	if string(readmeContent) != "version 3 from home slice\n" {
		t.Fatalf("unexpected README in source repo after push: %q", readmeContent)
	}

	output = runCLIForUser("", "repo", "unlink", boundPath)
	if !strings.Contains(output, "Removed repo binding: "+boundPath) {
		t.Fatalf("expected unlink output, got: %s", output)
	}
	output = runCLIForUser("", "repo", "list")
	if strings.Contains(output, boundPath) {
		t.Fatalf("expected repo binding to be removed, got: %s", output)
	}
}
