package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilesystemRegexSearchCLI(t *testing.T) {
	username := fmt.Sprintf("fssearch%d", time.Now().UnixNano()%1_000_000_000)

	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", nil, true, username, args...)
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

	searchOutput := runCLIForUser("", "fs", "search", "alpha.*gamma", "--regex", "--glob", fmt.Sprintf("/%s/**/*.md", username), "--json")
	var searchResp fsSearchJSON
	if err := json.Unmarshal([]byte(searchOutput), &searchResp); err != nil {
		t.Fatalf("decode fs search JSON: %v\nOutput:\n%s", err, searchOutput)
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
