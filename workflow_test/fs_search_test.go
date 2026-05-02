package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilesystemRegexSearchCLI(t *testing.T) {
	username := fmt.Sprintf("fssearch%d", time.Now().UnixNano()%1_000_000_000)
	env := map[string]string{"HOME": t.TempDir()}

	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	writeWorkspaceFile := func(path, content string) {
		t.Helper()
		localPath := filepath.Join(t.TempDir(), filepath.Base(path))
		if err := os.WriteFile(localPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write temp file %s: %v", localPath, err)
		}
		runCLIForUser("", "fs", "write", path, "-f", localPath)
	}

	readmePath := fmt.Sprintf("/%s/docs/README.md", username)
	notesPath := fmt.Sprintf("/%s/notes/todo.txt", username)
	writeWorkspaceFile(readmePath, "alpha beta gamma\n")
	writeWorkspaceFile(notesPath, "alpha beta notes\n")

	var searchResp fsSearchJSON
	var searchOutput string
	if err := waitForCondition(3*time.Second, 50*time.Millisecond, func() (bool, error) {
		var err error
		searchOutput, err = runCLIWithDirInputEnvLegacyUser("", "", env, true, username, "fs", "search", "alpha.*gamma", "--regex", "--glob", fmt.Sprintf("/%s/**/*.md", username), "--json")
		if err != nil {
			if strings.Contains(searchOutput, "search index is not ready") {
				return false, nil
			}
			return false, fmt.Errorf("CLI command failed: %w\nOutput:\n%s", err, searchOutput)
		}
		if err := json.Unmarshal([]byte(searchOutput), &searchResp); err != nil {
			return false, fmt.Errorf("decode fs search JSON: %w\nOutput:\n%s", err, searchOutput)
		}
		return true, nil
	}); err != nil {
		t.Fatalf("fs search did not become ready: %v", err)
	}
	if !searchResp.Regex || searchResp.Total != 1 {
		t.Fatalf("expected one regex match, got: %+v", searchResp)
	}
	if len(searchResp.Matches) != 1 || searchResp.Matches[0].Path != readmePath {
		t.Fatalf("unexpected regex search matches: %+v", searchResp.Matches)
	}
	if searchResp.Matches[0].Line != "alpha beta gamma" {
		t.Fatalf("unexpected regex search line: %+v", searchResp.Matches[0])
	}
}
