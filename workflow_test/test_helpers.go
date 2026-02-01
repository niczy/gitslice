package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// assertUnsupportedCommand ensures that a CLI invocation either errors or clearly
// reports that the command is not implemented. This keeps the new workflow
// tests meaningful even for commands that do not have full server-side
// support yet.
func assertUnsupportedCommand(t *testing.T, args ...string) {
	t.Helper()

	output, err := runCLI(args...)
	if err != nil {
		return
	}

	if !strings.Contains(output, "Unknown") && !strings.Contains(output, "not implemented") {
		t.Fatalf("expected command %v to be unsupported, got output: %s", args, output)
	}
}

// setupTempGitRepo creates a temporary git repository with known files and
// returns the directory path. The repo is cleaned up automatically when the
// test finishes.
func setupTempGitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	git("init")
	git("config", "user.email", "test@test.com")
	git("config", "user.name", "Test")

	// Create known files
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatalf("failed to write README.md: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to write src/main.go: %v", err)
	}

	git("add", ".")
	git("commit", "-m", "initial")

	return dir
}
