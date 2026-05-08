package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSliceEnsureIsIdempotent(t *testing.T) {
	rootWorkdir := t.TempDir()
	_ = runCLIOrFail(t, rootWorkdir, "init", sliceIDArg("root"))

	folderPath := fmt.Sprintf("ensure-%d", time.Now().UnixNano())
	filePath := folderPath + "/README.md"
	localPath := filepath.Join(rootWorkdir, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir ensure seed path: %v", err)
	}
	if err := os.WriteFile(localPath, []byte("seed ensure folder\n"), 0o644); err != nil {
		t.Fatalf("write ensure seed file: %v", err)
	}
	createResp := runCLIJSONOrFail[changesetCreateJSON](t, rootWorkdir, "changeset", "create", "--message", "seed ensure folder", "--files", filePath)
	mergeResp := runCLIJSONOrFail[mergeJSON](t, rootWorkdir, "changeset", "merge", createResp.ChangesetID)
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected root merge success, got: %+v", mergeResp)
	}

	sliceName := fmt.Sprintf("ensure-slice-%d", time.Now().UnixNano())
	first := runCLIJSONOrFail[sliceEnsureJSON](t, rootWorkdir, "slice", "ensure", sliceName, folderPath)
	second := runCLIJSONOrFail[sliceEnsureJSON](t, rootWorkdir, "slice", "ensure", sliceName, folderPath)
	if !first.Created || second.Created || first.SliceID == "" || first.SliceID != second.SliceID || first.Slug != second.Slug {
		t.Fatalf("expected idempotent slice ensure results, got first=%+v second=%+v", first, second)
	}
}

func TestRepoEnsureIsIdempotent(t *testing.T) {
	username := fmt.Sprintf("repoensure%d", time.Now().UnixNano()%1_000_000_000)
	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", nil, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}
	runCLIJSONForUser := func(workdir string, args ...string) repoEnsureJSON {
		t.Helper()
		output := runCLIForUser(workdir, append(args, "--json")...)
		var resp repoEnsureJSON
		if err := json.Unmarshal([]byte(output), &resp); err != nil {
			t.Fatalf("decode repo ensure JSON: %v\nOutput:\n%s", err, output)
		}
		return resp
	}

	remoteDir, _ := createLocalGitRemote(t)
	boundPath := fmt.Sprintf("/%s/repos/ensure-%d", username, time.Now().UnixNano())

	first := runCLIJSONForUser("", "repo", "ensure", "--push-enabled", remoteDir, boundPath)
	second := runCLIJSONForUser("", "repo", "ensure", "--push-enabled", remoteDir, boundPath)
	if !first.Created || second.Created || first.Binding.Path != boundPath || first.Binding.Path != second.Binding.Path {
		t.Fatalf("expected idempotent repo ensure results, got first=%+v second=%+v", first, second)
	}
}
