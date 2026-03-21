package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createFocusedSliceFromPublishedFolder(t *testing.T, folderPath string) string {
	t.Helper()

	rootWorkdir := t.TempDir()
	_ = runCLIOrFail(t, rootWorkdir, "init", sliceIDArg("root_slice"))

	filePath := folderPath + "/README.md"
	output := runCLIOrFail(t, rootWorkdir, "changeset", "create", "--message", "seed focused folder", "--files", filePath)
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract root changeset ID from output: %s", output)
	}
	output = runCLIOrFail(t, rootWorkdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected root merge success, got: %s", output)
	}

	sliceName := fmt.Sprintf("focused-slice-%d", time.Now().UnixNano())
	output = runCLIOrFail(t, rootWorkdir, "slice", "create", sliceName, folderPath)
	sliceID := extractCreatedSliceID(output)
	if sliceID == "" {
		t.Fatalf("expected created slice ID, got: %s", output)
	}
	return sliceID
}

func TestSliceWorkflowCommands(t *testing.T) {
	rootWorkdir := t.TempDir()
	rootSliceArg := sliceIDArg("root_slice")
	_ = runCLIOrFail(t, rootWorkdir, "init", rootSliceArg)

	folderPath := fmt.Sprintf("apps/workflow-%d", time.Now().UnixNano())
	filePath := folderPath + "/README.md"
	output := runCLIOrFail(t, rootWorkdir, "changeset", "create", "--message", "seed workflow folder", "--files", filePath)
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract changeset ID from output: %s", output)
	}
	output = runCLIOrFail(t, rootWorkdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected merge success, got: %s", output)
	}

	sliceName := fmt.Sprintf("workflow-slice-%d", time.Now().UnixNano())
	output = runCLIOrFail(t, rootWorkdir, "slice", "create", sliceName, folderPath)
	sliceID := extractCreatedSliceID(output)
	sliceSlug := extractCreatedSliceSlug(output)
	if sliceID == "" || sliceSlug == "" {
		t.Fatalf("expected slice ID and slug in output, got: %s", output)
	}

	output = runCLIOrFail(t, "", "slice", "list")
	if !strings.Contains(output, sliceID) || !strings.Contains(output, sliceSlug) {
		t.Fatalf("expected slice list to include created slice, got: %s", output)
	}

	checkoutDir := t.TempDir()
	output = runCLIOrFail(t, checkoutDir, "slice", "checkout", sliceSlug, "--git")
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "tree")
	if !strings.Contains(output, "README.md") {
		t.Fatalf("expected slice tree output to include README.md, got: %s", output)
	}

	trackedFiles := strings.Fields(runGitOrFail(t, checkoutDir, "ls-files"))
	if len(trackedFiles) == 0 {
		t.Fatal("expected tracked files after checkout")
	}
	localFile := filepath.Join(checkoutDir, trackedFiles[0])
	if err := os.WriteFile(localFile, []byte("updated locally\n"), 0o644); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	output = runCLIOrFail(t, checkoutDir, "slice", "diff", "--name-only")
	if !strings.Contains(output, trackedFiles[0]) {
		t.Fatalf("expected slice diff to include %s, got: %s", trackedFiles[0], output)
	}
	output = runCLIOrFail(t, checkoutDir, "slice", "status")
	if !strings.Contains(output, "Mode: git") || !strings.Contains(output, "Working tree: dirty") || !strings.Contains(output, "Changes: +0 ~1 -0") {
		t.Fatalf("expected slice status to show git dirty state, got: %s", output)
	}
	output = runCLIOrFail(t, checkoutDir, "status")
	if !strings.Contains(output, "Mode: git") || !strings.Contains(output, "Slice: "+sliceID) {
		t.Fatalf("expected top-level status alias to show slice status, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "doctor")
	if !strings.Contains(output, "Auth:") || !strings.Contains(output, "Checkout:") {
		t.Fatalf("expected doctor output sections, got: %s", output)
	}

	output = runCLIOrFail(t, "", "slice", "delete", sliceSlug)
	if !strings.Contains(output, "Deleted slice: "+sliceID) {
		t.Fatalf("expected delete output, got: %s", output)
	}

	output = runCLIOrFail(t, "", "slice", "list")
	if strings.Contains(output, sliceID) {
		t.Fatalf("expected deleted slice to be absent from list output, got: %s", output)
	}
}

func TestSlicePublishAndChangesetShowWorkflow(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-publish-%d", time.Now().UnixNano())

	createSliceFromRoot(t, sliceID, "")
	_ = runCLIOrFail(t, workdir, "init", sliceIDArg(sliceID))

	output := runCLIOrFail(t, workdir, "slice", "publish", "--review-only", "--message", "publish workflow", "--files", "publish.txt")
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("expected changeset ID from publish output, got: %s", output)
	}
	if !strings.Contains(output, "Changeset: "+changesetID) {
		t.Fatalf("expected review output in publish command, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "show")
	if !strings.Contains(output, "Changeset: "+changesetID) {
		t.Fatalf("expected tracked changeset show output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "slice", "publish", "--files", "publish.txt")
	if !strings.Contains(output, "Merge status: MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected publish merge success, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "list", "--status", "merged")
	if !strings.Contains(output, changesetID) {
		t.Fatalf("expected merged changeset in list output, got: %s", output)
	}
}

func TestChangesetCreateWorksWithoutGitCheckout(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-create-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)

	checkoutDir := t.TempDir()
	output := runCLIOrFail(t, checkoutDir, "slice", "checkout", sliceIDArg(sliceID))
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected default checkout to skip git metadata, err=%v", err)
	}

	targetPath := filepath.Join(checkoutDir, folderPath, "README.md")
	original, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read tracked file: %v", err)
	}
	if err := os.WriteFile(targetPath, append([]byte("// no-git changeset create\n"), original...), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}

	output = runCLIOrFail(t, checkoutDir, "changeset", "create", "--message", "no-git create")
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("expected changeset ID, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "changeset", "show")
	if !strings.Contains(output, "Changeset: "+changesetID) || !strings.Contains(output, "Files: +1 ~0 -0") {
		t.Fatalf("expected changeset show to include modified file, got: %s", output)
	}
}

func TestSlicePublishWorksWithoutGitCheckout(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-publish-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)

	checkoutDir := t.TempDir()
	output := runCLIOrFail(t, checkoutDir, "slice", "checkout", sliceIDArg(sliceID))
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected default checkout to skip git metadata, err=%v", err)
	}

	targetPath := filepath.Join(checkoutDir, folderPath, "README.md")
	original, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read tracked file: %v", err)
	}
	if err := os.WriteFile(targetPath, append([]byte("// no-git slice publish\n"), original...), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	newPath := filepath.Join(checkoutDir, folderPath, "NEW.txt")
	if err := os.WriteFile(newPath, []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "status")
	if !strings.Contains(output, "Mode: no-git") || !strings.Contains(output, "Working tree: dirty") || !strings.Contains(output, "Changes: +1 ~1 -0") {
		t.Fatalf("expected no-git slice status to show local changes, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "publish", "--review-only", "--message", "no-git publish")
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("expected changeset ID from publish output, got: %s", output)
	}
	if !strings.Contains(output, "Changeset: "+changesetID) || !strings.Contains(output, "Status: READY_FOR_MERGE") {
		t.Fatalf("expected publish review output to include modified file, got: %s", output)
	}
}

func TestFilesystemSyncCommand(t *testing.T) {
	username := fmt.Sprintf("fssync%d", time.Now().UnixNano())
	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	localUpload := filepath.Join(t.TempDir(), "upload")
	if err := os.MkdirAll(filepath.Join(localUpload, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir upload dir: %v", err)
	}
	content := "hello from fs sync\n"
	if err := os.WriteFile(filepath.Join(localUpload, "nested", "README.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write upload file: %v", err)
	}

	remoteBase := fmt.Sprintf("/%s/fs-sync-%d", username, time.Now().UnixNano())
	output := runCLIForUser("", "fs", "sync", "--direction", "push", localUpload, remoteBase)
	if !strings.Contains(output, "Uploaded 1 files") {
		t.Fatalf("expected upload output, got: %s", output)
	}

	localDownload := filepath.Join(t.TempDir(), "download")
	output = runCLIForUser("", "fs", "sync", "--direction", "pull", remoteBase, localDownload)
	if !strings.Contains(output, "Downloaded 1 files") {
		t.Fatalf("expected download output, got: %s", output)
	}

	downloaded, err := os.ReadFile(filepath.Join(localDownload, "nested", "README.md"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(downloaded) != content {
		t.Fatalf("downloaded content mismatch: got %q want %q", string(downloaded), content)
	}
}
