package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type dirtyTrackerState struct {
	PID int `json:"pid"`
}

func stopDirtyTrackerForTest(t *testing.T, checkoutDir string) {
	t.Helper()

	statePath := filepath.Join(checkoutDir, ".gs", "dirty_state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read dirty tracker state: %v", err)
	}

	var state dirtyTrackerState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode dirty tracker state: %v", err)
	}
	if state.PID <= 0 {
		return
	}
	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		t.Fatalf("stop dirty tracker pid %d: %v", state.PID, err)
	}
	_ = waitForCondition(2*time.Second, 25*time.Millisecond, func() (bool, error) {
		err := syscall.Kill(state.PID, 0)
		return err == syscall.ESRCH, nil
	})
}

func createFocusedSliceFromPublishedFolder(t *testing.T, folderPath string) string {
	t.Helper()

	rootWorkdir := t.TempDir()
	_ = runCLIOrFail(t, rootWorkdir, "init", sliceIDArg("root_slice"))

	filePath := folderPath + "/README.md"
	localPath := filepath.Join(rootWorkdir, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir focused folder seed path: %v", err)
	}
	if err := os.WriteFile(localPath, []byte("seed focused folder\n"), 0o644); err != nil {
		t.Fatalf("write focused folder seed file: %v", err)
	}
	createResp := runCLIJSONOrFail[changesetCreateJSON](t, rootWorkdir, "changeset", "create", "--message", "seed focused folder", "--files", filePath)
	if createResp.ChangesetID == "" {
		t.Fatalf("expected root changeset ID")
	}
	mergeResp := runCLIJSONOrFail[mergeJSON](t, rootWorkdir, "changeset", "merge", createResp.ChangesetID)
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected root merge success, got: %+v", mergeResp)
	}

	sliceName := fmt.Sprintf("focused-slice-%d", time.Now().UnixNano())
	createSliceResp := runCLIJSONOrFail[sliceCreateJSON](t, rootWorkdir, "slice", "create", sliceName, folderPath)
	if createSliceResp.SliceID == "" {
		t.Fatalf("expected created slice ID")
	}
	return createSliceResp.SliceID
}

func TestSliceWorkflowCommands(t *testing.T) {
	rootWorkdir := t.TempDir()
	rootSliceArg := sliceIDArg("root_slice")
	_ = runCLIOrFail(t, rootWorkdir, "init", rootSliceArg)

	folderPath := fmt.Sprintf("apps/workflow-%d", time.Now().UnixNano())
	filePath := folderPath + "/README.md"
	localPath := filepath.Join(rootWorkdir, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow seed path: %v", err)
	}
	if err := os.WriteFile(localPath, []byte("seed workflow folder\n"), 0o644); err != nil {
		t.Fatalf("write workflow seed file: %v", err)
	}
	createResp := runCLIJSONOrFail[changesetCreateJSON](t, rootWorkdir, "changeset", "create", "--message", "seed workflow folder", "--files", filePath)
	if createResp.ChangesetID == "" {
		t.Fatalf("failed to create changeset")
	}
	mergeResp := runCLIJSONOrFail[mergeJSON](t, rootWorkdir, "changeset", "merge", createResp.ChangesetID)
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected merge success, got: %+v", mergeResp)
	}

	sliceName := fmt.Sprintf("workflow-slice-%d", time.Now().UnixNano())
	sliceCreateResp := runCLIJSONOrFail[sliceCreateJSON](t, rootWorkdir, "slice", "create", sliceName, folderPath)
	if sliceCreateResp.SliceID == "" || sliceCreateResp.Slug == "" {
		t.Fatalf("expected slice ID and slug in output, got: %+v", sliceCreateResp)
	}
	sliceID := sliceCreateResp.SliceID
	sliceSlug := sliceCreateResp.Slug

	listResp := runCLIJSONOrFail[sliceListJSON](t, "", "slice", "list")
	foundSlice := false
	for _, slice := range listResp.Slices {
		if slice.SliceID == sliceID && slice.Slug == sliceSlug {
			foundSlice = true
			break
		}
	}
	if !foundSlice {
		t.Fatalf("expected slice list to include created slice, got: %+v", listResp)
	}

	checkoutDir := t.TempDir()
	checkoutResp := runCLIJSONOrFail[sliceCheckoutJSON](t, checkoutDir, "slice", "checkout", sliceSlug)
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	output := runCLIOrFail(t, checkoutDir, "slice", "tree")
	if !strings.Contains(output, "README.md") {
		t.Fatalf("expected slice tree output to include README.md, got: %s", output)
	}

	localFile := filepath.Join(checkoutDir, folderPath, "README.md")
	if err := os.WriteFile(localFile, []byte("updated locally\n"), 0o644); err != nil {
		t.Fatalf("write local file: %v", err)
	}
	output = runCLIOrFail(t, checkoutDir, "slice", "diff", "--name-only")
	if !strings.Contains(output, filepath.ToSlash(filepath.Join(folderPath, "README.md"))) {
		t.Fatalf("expected slice diff to include README, got: %s", output)
	}
	statusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutDir, "slice", "status")
	if statusResp.Mode != "no-git" ||
		statusResp.WorkingTree != "dirty" ||
		statusResp.Changes.Added != 0 ||
		statusResp.Changes.Modified != 1 ||
		statusResp.Changes.Deleted != 0 ||
		statusResp.SyncStatus != "skipped" {
		t.Fatalf("expected slice status to show no-git dirty state, got: %+v", statusResp)
	}
	remoteStatusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutDir, "slice", "status", "--remote")
	if !remoteStatusResp.RemoteQueried || remoteStatusResp.SyncStatus == "skipped" || remoteStatusResp.RemoteHead == "" {
		t.Fatalf("expected slice status --remote to include remote metadata, got: %+v", remoteStatusResp)
	}
	topStatusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutDir, "status")
	if topStatusResp.Mode != "no-git" || topStatusResp.SliceID != sliceID {
		t.Fatalf("expected top-level status alias to show slice status, got: %+v", topStatusResp)
	}

	output = runCLIOrFail(t, checkoutDir, "doctor")
	if !strings.Contains(output, "Auth:") || !strings.Contains(output, "Checkout:") {
		t.Fatalf("expected doctor output sections, got: %s", output)
	}

	deleteResp := runCLIJSONOrFail[sliceDeleteJSON](t, "", "slice", "delete", sliceSlug)
	if deleteResp.SliceID != sliceID || deleteResp.Status == "" {
		t.Fatalf("expected delete output, got: %+v", deleteResp)
	}

	listResp = runCLIJSONOrFail[sliceListJSON](t, "", "slice", "list")
	for _, slice := range listResp.Slices {
		if slice.SliceID == sliceID {
			t.Fatalf("expected deleted slice to be absent from list output, got: %+v", listResp)
		}
	}
}

func TestSlicePublishAndChangesetShowWorkflow(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-publish-%d", time.Now().UnixNano())

	createSliceFromRoot(t, sliceID, "")
	_ = runCLIOrFail(t, workdir, "init", sliceIDArg(sliceID))

	publishPreview := runCLIJSONOrFail[slicePublishJSON](t, workdir, "slice", "publish", "--review-only", "--message", "publish workflow", "--files", "publish.txt")
	changesetID := publishPreview.Changeset.ChangesetID
	if changesetID == "" || !publishPreview.ReviewOnly || publishPreview.Review.ChangesetID != changesetID {
		t.Fatalf("expected review output in publish command, got: %+v", publishPreview)
	}

	showResp := runCLIJSONOrFail[changesetReviewJSON](t, workdir, "changeset", "show")
	if showResp.ChangesetID != changesetID {
		t.Fatalf("expected tracked changeset show output, got: %+v", showResp)
	}

	publishResp := runCLIJSONOrFail[slicePublishJSON](t, workdir, "slice", "publish", "--files", "publish.txt")
	if publishResp.Merge == nil || publishResp.Merge.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected publish merge success, got: %+v", publishResp)
	}

	mergedList := runCLIJSONOrFail[changesetListJSON](t, workdir, "changeset", "list", "--status", "merged")
	foundMerged := false
	for _, changeset := range mergedList.Changesets {
		if changeset.ChangesetID == changesetID {
			foundMerged = true
			break
		}
	}
	if !foundMerged {
		t.Fatalf("expected merged changeset in list output, got: %+v", mergedList)
	}
}

func TestChangesetCreateWorksWithoutGitCheckout(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-create-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)

	checkoutDir := t.TempDir()
	checkoutResp := runCLIJSONOrFail[sliceCheckoutJSON](t, checkoutDir, "slice", "checkout", sliceIDArg(sliceID))
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
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

	createResp := runCLIJSONOrFail[changesetCreateJSON](t, checkoutDir, "changeset", "create", "--message", "no-git create")
	if createResp.ChangesetID == "" {
		t.Fatalf("expected changeset ID, got: %+v", createResp)
	}

	showResp := runCLIJSONOrFail[changesetReviewJSON](t, checkoutDir, "changeset", "show")
	if showResp.ChangesetID != createResp.ChangesetID || showResp.Diff.FilesAdded != 1 || showResp.Diff.FilesModified != 0 || showResp.Diff.FilesDeleted != 0 {
		t.Fatalf("expected changeset show to include modified file, got: %+v", showResp)
	}
}

func TestSlicePublishWorksWithoutGitCheckout(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-publish-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)

	checkoutDir := t.TempDir()
	checkoutResp := runCLIJSONOrFail[sliceCheckoutJSON](t, checkoutDir, "slice", "checkout", sliceIDArg(sliceID))
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
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

	statusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutDir, "slice", "status")
	if statusResp.Mode != "no-git" ||
		statusResp.WorkingTree != "dirty" ||
		statusResp.Changes.Added != 1 ||
		statusResp.Changes.Modified != 1 ||
		statusResp.Changes.Deleted != 0 ||
		statusResp.SyncStatus != "skipped" {
		t.Fatalf("expected no-git slice status to show local changes, got: %+v", statusResp)
	}

	publishResp := runCLIJSONOrFail[slicePublishJSON](t, checkoutDir, "slice", "publish", "--review-only", "--message", "no-git publish")
	if publishResp.Changeset.ChangesetID == "" ||
		publishResp.Review.ChangesetID != publishResp.Changeset.ChangesetID ||
		publishResp.Review.ReviewStatus != "READY_FOR_MERGE" {
		t.Fatalf("expected publish review output to include modified file, got: %+v", publishResp)
	}
}

func TestSliceDiffAndRestoreWorkWithoutGitCheckout(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-restore-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)

	checkoutDir := t.TempDir()
	checkoutResp := runCLIJSONOrFail[sliceCheckoutJSON](t, checkoutDir, "slice", "checkout", sliceIDArg(sliceID))
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	targetPath := filepath.Join(checkoutDir, folderPath, "README.md")
	if err := os.WriteFile(targetPath, []byte("updated locally\n"), 0o644); err != nil {
		t.Fatalf("rewrite tracked file: %v", err)
	}
	newPath := filepath.Join(checkoutDir, folderPath, "NEW.txt")
	if err := os.WriteFile(newPath, []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	output := runCLIOrFail(t, checkoutDir, "slice", "diff", "--name-only")
	if !strings.Contains(output, filepath.ToSlash(filepath.Join(folderPath, "README.md"))) ||
		!strings.Contains(output, filepath.ToSlash(filepath.Join(folderPath, "NEW.txt"))) {
		t.Fatalf("expected no-git diff --name-only to include tracked delete and new file, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "diff", "--summary")
	if !strings.Contains(output, "Changes: +1 ~1 -0") ||
		!strings.Contains(output, "M "+filepath.ToSlash(filepath.Join(folderPath, "README.md"))) ||
		!strings.Contains(output, "A "+filepath.ToSlash(filepath.Join(folderPath, "NEW.txt"))) {
		t.Fatalf("expected no-git diff --summary output, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "diff")
	if !strings.Contains(output, "M "+filepath.ToSlash(filepath.Join(folderPath, "README.md"))) ||
		!strings.Contains(output, "A "+filepath.ToSlash(filepath.Join(folderPath, "NEW.txt"))) {
		t.Fatalf("expected no-git diff to include modify/add entries, got: %s", output)
	}
	if !strings.Contains(output, "--- a/"+filepath.ToSlash(filepath.Join(folderPath, "README.md"))) ||
		!strings.Contains(output, "+++ b/"+filepath.ToSlash(filepath.Join(folderPath, "README.md"))) ||
		!strings.Contains(output, "--- /dev/null") ||
		!strings.Contains(output, "+++ b/"+filepath.ToSlash(filepath.Join(folderPath, "NEW.txt"))) {
		t.Fatalf("expected no-git diff to include unified patches, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "restore", "--dry-run")
	if !strings.Contains(output, "Planned restore:") ||
		!strings.Contains(output, "restore "+filepath.ToSlash(filepath.Join(folderPath, "README.md"))) ||
		!strings.Contains(output, "remove "+filepath.ToSlash(filepath.Join(folderPath, "NEW.txt"))) ||
		!strings.Contains(output, "Would restore tracked files: 1") ||
		!strings.Contains(output, "Would remove new paths: 1") {
		t.Fatalf("expected no-git restore --dry-run output, got: %s", output)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "restore")
	if !strings.Contains(output, "Restored tracked files: 1") || !strings.Contains(output, "Removed new paths: 1") {
		t.Fatalf("expected no-git restore output, got: %s", output)
	}

	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("expected tracked file to be restored, err=%v", err)
	}
	restoredContent, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read restored tracked file: %v", err)
	}
	if string(restoredContent) != "" {
		t.Fatalf("expected tracked file to revert to original empty content, got %q", string(restoredContent))
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("expected new file to be removed, err=%v", err)
	}

	output = runCLIOrFail(t, checkoutDir, "slice", "status")
	if !strings.Contains(output, "Working tree: clean") || !strings.Contains(output, "Changes: +0 ~0 -0") {
		t.Fatalf("expected restored no-git checkout to be clean, got: %s", output)
	}
}

func TestNoGitCheckoutStartsDirtyTracker(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-tracker-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)
	env := map[string]string{"GS_DISABLE_DIRTY_TRACKER": "0"}

	checkoutDir := t.TempDir()
	t.Cleanup(func() {
		stopDirtyTrackerForTest(t, checkoutDir)
	})
	output, err := runCLIWithDirInputEnvLegacyUser(checkoutDir, "", workflowProcessEnv(t, env), true, workflowUsername(t), "slice", "checkout", sliceIDArg(sliceID), "--json")
	if err != nil {
		t.Fatalf("checkout with dirty tracker failed: %v\nOutput:\n%s", err, output)
	}
	var checkoutResp sliceCheckoutJSON
	if err := json.Unmarshal([]byte(output), &checkoutResp); err != nil {
		t.Fatalf("decode checkout JSON: %v\nOutput:\n%s", err, output)
	}
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	statePath := filepath.Join(checkoutDir, ".gs", "dirty_state.json")
	pathsPath := filepath.Join(checkoutDir, ".gs", "dirty_paths.json")
	if err := waitForCondition(3*time.Second, 50*time.Millisecond, func() (bool, error) {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		return strings.Contains(string(raw), `"status":"active"`), nil
	}); err != nil {
		t.Fatalf("dirty tracker never became active: %v", err)
	}

	targetPath := filepath.Join(checkoutDir, folderPath, "README.md")
	if err := os.WriteFile(targetPath, []byte("updated locally\n"), 0o644); err != nil {
		t.Fatalf("rewrite tracked file: %v", err)
	}
	newPath := filepath.Join(checkoutDir, folderPath, "NEW.txt")
	if err := os.WriteFile(newPath, []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	if err := waitForCondition(3*time.Second, 50*time.Millisecond, func() (bool, error) {
		raw, err := os.ReadFile(pathsPath)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		content := string(raw)
		return strings.Contains(content, filepath.ToSlash(filepath.Join(folderPath, "README.md"))) &&
			strings.Contains(content, filepath.ToSlash(filepath.Join(folderPath, "NEW.txt"))), nil
	}); err != nil {
		t.Fatalf("dirty tracker never recorded local changes: %v", err)
	}

	output, err = runCLIWithDirInputEnvLegacyUser(checkoutDir, "", workflowProcessEnv(t, env), true, workflowUsername(t), "slice", "restore")
	if err != nil {
		t.Fatalf("restore with dirty tracker failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Restored tracked files: 1") || !strings.Contains(output, "Removed new paths: 1") {
		t.Fatalf("expected restore output, got: %s", output)
	}

	if err := waitForCondition(3*time.Second, 50*time.Millisecond, func() (bool, error) {
		raw, err := os.ReadFile(pathsPath)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		return strings.TrimSpace(string(raw)) == "[]", nil
	}); err != nil {
		t.Fatalf("dirty tracker did not clear after restore: %v", err)
	}
}

func TestNoGitStatusAndChangesetCreateWorkWithDirtyTracker(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-tracker-create-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)
	env := map[string]string{"GS_DISABLE_DIRTY_TRACKER": "0"}

	checkoutDir := t.TempDir()
	t.Cleanup(func() {
		stopDirtyTrackerForTest(t, checkoutDir)
	})
	checkoutResp := runCLIJSONWithEnvOrFail[sliceCheckoutJSON](t, checkoutDir, env, "slice", "checkout", sliceIDArg(sliceID))
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	statePath := filepath.Join(checkoutDir, ".gs", "dirty_state.json")
	pathsPath := filepath.Join(checkoutDir, ".gs", "dirty_paths.json")
	if err := waitForCondition(3*time.Second, 50*time.Millisecond, func() (bool, error) {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		return strings.Contains(string(raw), `"status":"active"`), nil
	}); err != nil {
		t.Fatalf("dirty tracker never became active: %v", err)
	}

	targetPath := filepath.Join(checkoutDir, folderPath, "README.md")
	if err := os.WriteFile(targetPath, []byte("updated locally\n"), 0o644); err != nil {
		t.Fatalf("rewrite tracked file: %v", err)
	}
	newPath := filepath.Join(checkoutDir, folderPath, "NEW.txt")
	if err := os.WriteFile(newPath, []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	if err := waitForCondition(3*time.Second, 50*time.Millisecond, func() (bool, error) {
		raw, err := os.ReadFile(pathsPath)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		content := string(raw)
		return strings.Contains(content, filepath.ToSlash(filepath.Join(folderPath, "README.md"))) &&
			strings.Contains(content, filepath.ToSlash(filepath.Join(folderPath, "NEW.txt"))), nil
	}); err != nil {
		t.Fatalf("dirty tracker never recorded local changes: %v", err)
	}

	statusResp := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, env, "slice", "status")
	if statusResp.Mode != "no-git" ||
		statusResp.WorkingTree != "dirty" ||
		statusResp.Changes.Added != 1 ||
		statusResp.Changes.Modified != 1 ||
		statusResp.Changes.Deleted != 0 {
		t.Fatalf("expected dirty tracker status to reflect changes, got: %+v", statusResp)
	}

	createResp := runCLIJSONWithEnvOrFail[changesetCreateJSON](t, checkoutDir, env, "changeset", "create", "--message", "dirty tracker create")
	if createResp.ChangesetID == "" {
		t.Fatalf("expected changeset create output, got: %+v", createResp)
	}

	showResp := runCLIJSONWithEnvOrFail[changesetReviewJSON](t, checkoutDir, env, "changeset", "show")
	if showResp.ChangesetID != createResp.ChangesetID || showResp.Diff.FilesDeleted != 0 || showResp.Diff.FilesAdded+showResp.Diff.FilesModified != 2 {
		t.Fatalf("expected dirty tracker review output, got: %+v", showResp)
	}
}

func TestNoGitStatusFallsBackWhenDirtyTrackerStops(t *testing.T) {
	folderPath := fmt.Sprintf("apps/nogit-tracker-fallback-%d", time.Now().UnixNano())
	sliceID := createFocusedSliceFromPublishedFolder(t, folderPath)
	env := map[string]string{"GS_DISABLE_DIRTY_TRACKER": "0"}

	checkoutDir := t.TempDir()
	checkoutResp := runCLIJSONWithEnvOrFail[sliceCheckoutJSON](t, checkoutDir, env, "slice", "checkout", sliceIDArg(sliceID))
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	statePath := filepath.Join(checkoutDir, ".gs", "dirty_state.json")
	if err := waitForCondition(3*time.Second, 50*time.Millisecond, func() (bool, error) {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		return strings.Contains(string(raw), `"status":"active"`), nil
	}); err != nil {
		t.Fatalf("dirty tracker never became active: %v", err)
	}

	stopDirtyTrackerForTest(t, checkoutDir)

	targetPath := filepath.Join(checkoutDir, folderPath, "README.md")
	if err := os.WriteFile(targetPath, []byte("updated after tracker stopped\n"), 0o644); err != nil {
		t.Fatalf("rewrite tracked file: %v", err)
	}

	statusResp := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, env, "slice", "status")
	if statusResp.WorkingTree != "dirty" || statusResp.Changes.Modified != 1 {
		t.Fatalf("expected fallback status to detect modified file, got: %+v", statusResp)
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
