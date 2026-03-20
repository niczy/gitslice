package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitStagePathsStagesIgnoredFiles(t *testing.T) {
	repoDir := t.TempDir()

	if _, err := runGitCommand(repoDir, "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoDir, ".gitignore"), []byte("package-lock.json\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "package-lock.json"), []byte("{\"lock\":true}\n"), 0o644); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	if err := gitStagePaths(repoDir, []string{".gitignore", "package-lock.json"}); err != nil {
		t.Fatalf("gitStagePaths failed: %v", err)
	}

	output, err := runGitCommand(repoDir, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatalf("git diff --cached --name-only failed: %v", err)
	}

	if !strings.Contains(output, ".gitignore") {
		t.Fatalf("expected .gitignore to be staged, got %q", output)
	}
	if !strings.Contains(output, "package-lock.json") {
		t.Fatalf("expected ignored file to be staged, got %q", output)
	}
}
