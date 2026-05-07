package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
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

	filePath := folderPath + "/README.md"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if testStorage == nil {
		t.Fatal("expected test storage to be initialized")
	}
	rootSlice, err := testStorage.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	content := []byte("seed focused folder\n")
	mustWriteSliceManifest(t, ctx, testStorage, rootSlice.ID, filePath, content)
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(rootSlice.ID, filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: rootSlice.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, filePath, rootSlice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	workdir := t.TempDir()
	sliceName := fmt.Sprintf("focused-slice-%d", time.Now().UnixNano())
	createSliceResp := runCLIJSONOrFail[sliceCreateJSON](t, workdir, "slice", "create", sliceName, folderPath)
	if createSliceResp.SliceID == "" {
		t.Fatalf("expected created slice ID")
	}
	return createSliceResp.SliceID
}

func publishRootFolderFromWorktree(t *testing.T, folderPath, message string, populate func(rootWorkdir string)) {
	t.Helper()

	rootWorkdir := t.TempDir()
	_ = runCLIOrFail(t, rootWorkdir, "init", sliceIDArg("root_slice"))
	populate(rootWorkdir)

	rootFolder := filepath.Join(rootWorkdir, filepath.FromSlash(folderPath))
	modifiedPaths := make([]string, 0, 8)
	err := filepath.Walk(rootFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == rootFolder {
			return nil
		}
		rel, err := filepath.Rel(rootWorkdir, path)
		if err != nil {
			return err
		}
		modifiedPaths = append(modifiedPaths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk seeded root folder %s: %v", folderPath, err)
	}
	if len(modifiedPaths) == 0 {
		modifiedPaths = append(modifiedPaths, folderPath)
	}

	createResp := runCLIJSONOrFail[changesetCreateJSON](t, rootWorkdir, "changeset", "create", "--message", message, "--files", strings.Join(modifiedPaths, ","))
	if createResp.ChangesetID == "" {
		t.Fatalf("expected root changeset ID for %s, got: %+v", folderPath, createResp)
	}
	mergeResp := runCLIJSONOrFail[mergeJSON](t, rootWorkdir, "changeset", "merge", createResp.ChangesetID)
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected root merge success for %s, got: %+v", folderPath, mergeResp)
	}
}

func createFocusedSliceForFolder(t *testing.T, folderPath string) sliceCreateJSON {
	t.Helper()

	workdir := t.TempDir()
	sliceName := fmt.Sprintf("workflow-slice-%d", time.Now().UnixNano())
	createResp := runCLIJSONOrFail[sliceCreateJSON](t, workdir, "slice", "create", sliceName, folderPath)
	if createResp.SliceID == "" || createResp.Slug == "" {
		t.Fatalf("expected created focused slice for %s, got: %+v", folderPath, createResp)
	}
	return createResp
}

func checkoutFocusedSliceRef(t *testing.T, sliceRef string) string {
	t.Helper()

	checkoutDir := t.TempDir()
	checkoutResp := runCLIJSONOrFail[sliceCheckoutJSON](t, checkoutDir, "slice", "checkout", sliceRef)
	if checkoutResp.SliceID == "" {
		t.Fatalf("expected checkout for created slice, got: %+v", checkoutResp)
	}
	return checkoutDir
}

type seededWorkflowFile struct {
	content       []byte
	executable    bool
	symlinkTarget string
}

func createSeededWorkflowSlice(t *testing.T, sliceID string, files map[string]seededWorkflowFile) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)
	if testStorage == nil {
		t.Fatal("expected test storage to be initialized")
	}
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("CreateSlice(%s) failed: %v", sliceID, err)
	}

	snapshotFiles := make(map[string]string, len(files))
	for path, file := range files {
		if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
			ID:            common.GenerateEntryID(sliceID, path),
			Path:          path,
			Type:          "file",
			ParentID:      sliceID,
			Size:          int64(len(file.content)),
			Executable:    file.executable,
			SymlinkTarget: file.symlinkTarget,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", path, err)
		}
		manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, testStorage, sliceID, path, file.content, file.executable, file.symlinkTarget)
		if err != nil {
			t.Fatalf("WriteSliceFileManifestWithMetadata(%s) failed: %v", path, err)
		}
		if err := testStorage.AddFileToSlice(ctx, path, sliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", path, err)
		}
		snapshotFiles[path] = manifest.Hash
	}

	now := time.Now()
	commitHash := fmt.Sprintf("seed-%s", sliceID)
	if err := testStorage.AddSliceCommit(ctx, sliceID, &models.Commit{
		CommitHash: commitHash,
		Timestamp:  now,
		Message:    "seed local workflow slice",
	}); err != nil {
		t.Fatalf("AddSliceCommit(%s) failed: %v", sliceID, err)
	}
	if err := testStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Files:      snapshotFiles,
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot(%s) failed: %v", commitHash, err)
	}
	metadata, err := testStorage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		t.Fatalf("GetSliceMetadata(%s) failed: %v", sliceID, err)
	}
	metadata.HeadCommitHash = commitHash
	metadata.LastModified = now
	metadata.ModifiedFiles = make([]string, 0, len(snapshotFiles))
	for path := range snapshotFiles {
		metadata.ModifiedFiles = append(metadata.ModifiedFiles, path)
	}
	sort.Strings(metadata.ModifiedFiles)
	metadata.ModifiedFilesCount = len(metadata.ModifiedFiles)
	if err := testStorage.UpdateSliceMetadata(ctx, sliceID, metadata); err != nil {
		t.Fatalf("UpdateSliceMetadata(%s) failed: %v", sliceID, err)
	}
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

	doctorResp := runCLIJSONOrFail[doctorJSON](t, checkoutDir, "doctor")
	if doctorResp.Auth.Username == "" ||
		!doctorResp.Services.Admin.OK ||
		!doctorResp.Services.Slice.OK ||
		!doctorResp.Services.GlobalState.OK ||
		!doctorResp.Services.Filesystem.OK ||
		!doctorResp.Checkout.Present ||
		doctorResp.Checkout.SliceID != sliceID ||
		doctorResp.Checkout.Mode != "no-git" {
		t.Fatalf("expected doctor JSON output sections, got: %+v", doctorResp)
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

func TestSliceTreeRenameAndChangesetRebaseJSON(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceID := fmt.Sprintf("slice-json-%d", time.Now().UnixNano())
	createSeededWorkflowSlice(t, sliceID, map[string]seededWorkflowFile{
		"apps/json-workflow/README.md": {content: []byte("json workflow\n")},
		"apps/json-workflow/src/main.go": {
			content: []byte("package main\nfunc main() {}\n"),
		},
	})

	treeResp := runCLIJSONOrFail[sliceTreeJSON](t, "", "slice", "tree", "apps", "--slice", sliceIDArg(sliceID))
	if treeResp.SliceID != sliceID || treeResp.Path != "apps" {
		t.Fatalf("unexpected slice tree JSON: %+v", treeResp)
	}
	if len(treeResp.Nodes) != 1 || treeResp.Nodes[0].Name != "json-workflow" {
		t.Fatalf("expected apps/json-workflow in tree output, got: %+v", treeResp)
	}

	renameResp := runCLIJSONOrFail[sliceRenameJSON](t, "", "slice", "rename", sliceIDArg(sliceID), "Renamed JSON Slice")
	if renameResp.SliceID != sliceID || renameResp.Name != "Renamed JSON Slice" || renameResp.Status != "renamed" {
		t.Fatalf("unexpected slice rename JSON: %+v", renameResp)
	}

	createResp, err := newSliceClient(t).CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        sliceID,
		BaseCommitHash: "stale-base",
		ModifiedFiles:  []string{"apps/json-workflow/README.md"},
		Author:         "tester",
		Message:        "json rebase",
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	rebaseResp := runCLIJSONOrFail[changesetRebaseJSON](t, "", "changeset", "rebase", createResp.GetChangesetId())
	if rebaseResp.ChangesetID != createResp.GetChangesetId() {
		t.Fatalf("unexpected changeset rebase JSON: %+v", rebaseResp)
	}
	if rebaseResp.NewBaseCommitHash != "seed-"+sliceID {
		t.Fatalf("expected rebase to current head, got: %+v", rebaseResp)
	}
}

func TestSliceResourceAliasesWorkflow(t *testing.T) {
	rootResp := runCLIJSONOrFail[sliceRootJSON](t, "", "slice", "root")
	if rootResp.SliceID != "root_slice" {
		t.Fatalf("unexpected slice root response: %+v", rootResp)
	}

	sliceID := fmt.Sprintf("slice-alias-%d", time.Now().UnixNano())
	createSeededWorkflowSlice(t, sliceID, map[string]seededWorkflowFile{
		"docs/README.md": {content: []byte("alias history\n")},
	})

	bindDir := t.TempDir()
	bindResp := runCLIJSONOrFail[initJSON](t, bindDir, "slice", "bind", rootResp.SliceID)
	if bindResp.Status != "initialized" || bindResp.SliceID != rootResp.SliceID {
		t.Fatalf("unexpected slice bind response: %+v", bindResp)
	}

	historyResp := runCLIJSONOrFail[sliceHistoryJSON](t, "", "slice", "history", sliceID)
	if len(historyResp.Commits) == 0 {
		t.Fatalf("expected slice history commits, got: %+v", historyResp)
	}
}

func TestSlicePublishAndChangesetShowWorkflow(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-publish-%d", time.Now().UnixNano())

	createSliceFromRoot(t, sliceID, "")
	_ = runCLIOrFail(t, workdir, "init", sliceIDArg(sliceID))

	publishFile := filepath.Join(workdir, "publish.txt")
	if err := os.WriteFile(publishFile, []byte("publish workflow\n"), 0o644); err != nil {
		t.Fatalf("write publish workflow file: %v", err)
	}

	publishPreview := runCLIJSONOrFail[slicePublishJSON](t, workdir, "slice", "export", "--message", "publish workflow")
	changesetID := publishPreview.Changeset.ChangesetID
	if changesetID == "" || !publishPreview.ReviewOnly || publishPreview.Review.ChangesetID != changesetID || publishPreview.ReusedExisting || publishPreview.Merge != nil {
		t.Fatalf("expected unmerged publish output, got: %+v", publishPreview)
	}

	showResp := runCLIJSONOrFail[changesetReviewJSON](t, workdir, "changeset", "show")
	if showResp.ChangesetID != changesetID {
		t.Fatalf("expected tracked changeset show output, got: %+v", showResp)
	}

	restoreOutput := runCLIOrFail(t, workdir, "slice", "restore")
	if !strings.Contains(restoreOutput, "Restore complete.") &&
		!strings.Contains(restoreOutput, "Removed new paths: 1") {
		t.Fatalf("expected restore output before clean publish, got: %s", restoreOutput)
	}

	publishResp := runCLIJSONOrFail[slicePublishJSON](t, workdir, "slice", "publish")
	if !publishResp.ReusedExisting || publishResp.ReviewOnly || publishResp.Merge == nil || publishResp.Merge.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected publish to reuse and merge tracked changeset, got: %+v", publishResp)
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

func TestSliceExportThenTrackedChangesetMergeAppendsCommitAndUpdatesTree(t *testing.T) {
	sliceID := fmt.Sprintf("slice-export-merge-%d", time.Now().UnixNano())
	fileRel := filepath.ToSlash(filepath.Join("src", "app.txt"))
	initialContent := "version one\n"
	updatedContent := "version two\n"

	createSeededWorkflowSlice(t, sliceID, map[string]seededWorkflowFile{
		fileRel: {content: []byte(initialContent)},
	})

	checkoutDir := checkoutFocusedSliceRef(t, sliceID)
	beforeHistory := runCLIJSONOrFail[sliceHistoryJSON](t, checkoutDir, "slice", "history")
	if len(beforeHistory.Commits) == 0 {
		t.Fatalf("expected initial slice history, got: %+v", beforeHistory)
	}
	beforeHead := beforeHistory.Commits[0].CommitHash

	if err := os.WriteFile(filepath.Join(checkoutDir, filepath.FromSlash(fileRel)), []byte(updatedContent), 0o644); err != nil {
		t.Fatalf("update checked out file: %v", err)
	}

	createResp := runCLIJSONOrFail[changesetCreateJSON](t, checkoutDir, "changeset", "create", "--message", "export then merge workflow")
	if createResp.ChangesetID == "" {
		t.Fatalf("expected created changeset, got: %+v", createResp)
	}

	exportResp := runCLIJSONOrFail[slicePublishJSON](t, checkoutDir, "slice", "export", "--message", "export then merge workflow")
	if exportResp.Changeset.ChangesetID != createResp.ChangesetID ||
		!exportResp.ReviewOnly ||
		exportResp.Merge != nil ||
		exportResp.Review.ReviewStatus != "READY_FOR_MERGE" {
		t.Fatalf("expected export to update tracked changeset without merging, got: %+v", exportResp)
	}

	afterExportHistory := runCLIJSONOrFail[sliceHistoryJSON](t, checkoutDir, "slice", "history")
	if len(afterExportHistory.Commits) != len(beforeHistory.Commits) || afterExportHistory.Commits[0].CommitHash != beforeHead {
		t.Fatalf("expected export to leave history unchanged, before=%+v after=%+v", beforeHistory, afterExportHistory)
	}

	mergeResp := runCLIJSONOrFail[mergeJSON](t, checkoutDir, "changeset", "merge")
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" ||
		mergeResp.ChangesetID != createResp.ChangesetID ||
		mergeResp.NewCommitHash == "" ||
		mergeResp.NewCommitHash == beforeHead {
		t.Fatalf("expected tracked changeset merge to append a new commit, got: %+v", mergeResp)
	}

	afterMergeHistory := runCLIJSONOrFail[sliceHistoryJSON](t, checkoutDir, "slice", "history")
	if len(afterMergeHistory.Commits) != len(beforeHistory.Commits)+1 ||
		afterMergeHistory.Commits[0].CommitHash != mergeResp.NewCommitHash {
		t.Fatalf("expected merge to append commit %s, before=%+v after=%+v", mergeResp.NewCommitHash, beforeHistory, afterMergeHistory)
	}

	treeOutput := runCLIOrFail(t, checkoutDir, "slice", "tree", "src")
	if !strings.Contains(treeOutput, "app.txt") {
		t.Fatalf("expected updated file in slice tree, got: %s", treeOutput)
	}

	committedContent := runCLIOrFail(t, checkoutDir, "file", "cat", "--slice", sliceID, "--commit", mergeResp.NewCommitHash, "--raw", fileRel)
	if committedContent != updatedContent {
		t.Fatalf("expected committed file content %q, got %q", updatedContent, committedContent)
	}

	statusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutDir, "slice", "status")
	if statusResp.TrackedChangesetID != "" {
		t.Fatalf("expected tracked changeset to clear after merge, got: %+v", statusResp)
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

func TestComprehensiveNoGitSlicePublishAndSyncWorkflow(t *testing.T) {
	sliceID := fmt.Sprintf("comprehensive-local-%d", time.Now().UnixNano())
	readmeRel := "README.md"
	staleRel := filepath.ToSlash(filepath.Join("docs", "stale.txt"))
	newRel := filepath.ToSlash(filepath.Join("docs", "NEW.txt"))

	createSeededWorkflowSlice(t, sliceID, map[string]seededWorkflowFile{
		readmeRel: {content: []byte("comprehensive v1\n")},
		staleRel:  {content: []byte("stale file\n")},
	})

	checkoutA := checkoutFocusedSliceRef(t, sliceID)

	initialStatus := runCLIJSONOrFail[sliceStatusJSON](t, checkoutA, "slice", "status")
	if initialStatus.Mode != "no-git" || initialStatus.WorkingTree != "clean" || initialStatus.Changes.Added != 0 || initialStatus.Changes.Modified != 0 || initialStatus.Changes.Deleted != 0 {
		t.Fatalf("expected initial checkout to be clean, got: %+v", initialStatus)
	}

	readmePath := filepath.Join(checkoutA, filepath.FromSlash(readmeRel))
	stalePath := filepath.Join(checkoutA, filepath.FromSlash(staleRel))
	newPath := filepath.Join(checkoutA, filepath.FromSlash(newRel))
	if err := os.WriteFile(readmePath, []byte("comprehensive v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite README: %v", err)
	}
	if err := os.Remove(stalePath); err != nil {
		t.Fatalf("remove stale file: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("brand new\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	statusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutA, "slice", "status")
	if statusResp.WorkingTree != "dirty" || statusResp.Changes.Added != 1 || statusResp.Changes.Modified != 1 || statusResp.Changes.Deleted != 1 {
		t.Fatalf("expected mixed local changes, got: %+v", statusResp)
	}

	diffSummary := runCLIOrFail(t, checkoutA, "slice", "diff", "--summary")
	if !strings.Contains(diffSummary, "Changes: +1 ~1 -1") ||
		!strings.Contains(diffSummary, "M "+readmeRel) ||
		!strings.Contains(diffSummary, "D "+staleRel) ||
		!strings.Contains(diffSummary, "A "+newRel) {
		t.Fatalf("expected comprehensive diff summary, got: %s", diffSummary)
	}

	restorePreview := runCLIOrFail(t, checkoutA, "slice", "restore", "--dry-run", newRel)
	if !strings.Contains(restorePreview, "remove "+newRel) ||
		!strings.Contains(restorePreview, "Would restore tracked files: 0") ||
		!strings.Contains(restorePreview, "Would remove new paths: 1") {
		t.Fatalf("expected targeted restore preview, got: %s", restorePreview)
	}
	restoreOutput := runCLIOrFail(t, checkoutA, "slice", "restore", newRel)
	if !strings.Contains(restoreOutput, "Restored tracked files: 0") || !strings.Contains(restoreOutput, "Removed new paths: 1") {
		t.Fatalf("expected targeted restore output, got: %s", restoreOutput)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("expected restored new path to be removed, err=%v", err)
	}

	statusResp = runCLIJSONOrFail[sliceStatusJSON](t, checkoutA, "slice", "status")
	if statusResp.WorkingTree != "dirty" || statusResp.Changes.Added != 0 || statusResp.Changes.Modified != 1 || statusResp.Changes.Deleted != 1 {
		t.Fatalf("expected remaining modify/delete changes after partial restore, got: %+v", statusResp)
	}

	publishPreview := runCLIJSONOrFail[slicePublishJSON](t, checkoutA, "slice", "publish", "--review-only", "--message", "comprehensive no-git workflow")
	if publishPreview.Changeset.ChangesetID == "" ||
		publishPreview.Review.ChangesetID != publishPreview.Changeset.ChangesetID ||
		publishPreview.Review.ReviewStatus != "READY_FOR_MERGE" {
		t.Fatalf("expected review-only publish output, got: %+v", publishPreview)
	}
	if got := publishPreview.Review.Diff.FilesAdded + publishPreview.Review.Diff.FilesModified + publishPreview.Review.Diff.FilesDeleted; got != 2 {
		t.Fatalf("expected review-only publish to cover 2 remaining paths, got: %+v", publishPreview)
	}

	showResp := runCLIJSONOrFail[changesetReviewJSON](t, checkoutA, "changeset", "show")
	if showResp.ChangesetID != publishPreview.Changeset.ChangesetID {
		t.Fatalf("expected tracked changeset show output, got: %+v", showResp)
	}
	if got := showResp.Diff.FilesAdded + showResp.Diff.FilesModified + showResp.Diff.FilesDeleted; got != 2 {
		t.Fatalf("expected tracked changeset show to cover 2 remaining paths, got: %+v", showResp)
	}

	publishResp := runCLIJSONOrFail[slicePublishJSON](t, checkoutA, "slice", "export", "--message", "comprehensive no-git workflow")
	if !publishResp.ReviewOnly || publishResp.Merge != nil {
		t.Fatalf("expected export without merging, got: %+v", publishResp)
	}
	mergeResp := runCLIJSONOrFail[mergeJSON](t, checkoutA, "changeset", "merge")
	if mergeResp.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("expected explicit merge success, got: %+v", mergeResp)
	}
}

func TestComprehensiveNoGitMetadataWorkflow(t *testing.T) {
	sliceID := fmt.Sprintf("metadata-local-%d", time.Now().UnixNano())
	readmeRel := "README.md"
	scriptRel := filepath.ToSlash(filepath.Join("bin", "run.sh"))
	linkRel := filepath.ToSlash(filepath.Join("bin", "current"))
	linkTarget := "run.sh"

	createSeededWorkflowSlice(t, sliceID, map[string]seededWorkflowFile{
		readmeRel: {content: []byte("metadata readme\n")},
		scriptRel: {content: []byte("#!/bin/sh\necho metadata\n"), executable: true},
		linkRel:   {content: []byte(linkTarget), symlinkTarget: linkTarget},
	})

	checkoutA := checkoutFocusedSliceRef(t, sliceID)

	scriptPathA := filepath.Join(checkoutA, filepath.FromSlash(scriptRel))
	linkPathA := filepath.Join(checkoutA, filepath.FromSlash(linkRel))
	if info, err := os.Stat(scriptPathA); err != nil {
		t.Fatalf("stat checked out script: %v", err)
	} else if info.Mode().Perm() != 0o755 {
		t.Fatalf("expected executable script after checkout, got mode %o", info.Mode().Perm())
	}
	if target, err := os.Readlink(linkPathA); err != nil {
		t.Fatalf("read checked out symlink: %v", err)
	} else if target != "run.sh" {
		t.Fatalf("expected original symlink target, got %q", target)
	}

	if err := os.Chmod(scriptPathA, 0o644); err != nil {
		t.Fatalf("chmod script non-executable: %v", err)
	}
	if err := os.Remove(linkPathA); err != nil {
		t.Fatalf("remove original symlink: %v", err)
	}
	if err := os.Symlink("../README.md", linkPathA); err != nil {
		t.Fatalf("rewrite symlink target: %v", err)
	}

	statusResp := runCLIJSONOrFail[sliceStatusJSON](t, checkoutA, "slice", "status")
	if statusResp.WorkingTree != "dirty" || statusResp.Changes.Added != 0 || statusResp.Changes.Modified != 2 || statusResp.Changes.Deleted != 0 {
		t.Fatalf("expected metadata-only modifications, got: %+v", statusResp)
	}

	diffSummary := runCLIOrFail(t, checkoutA, "slice", "diff", "--stat")
	if !strings.Contains(diffSummary, "M "+scriptRel) ||
		!strings.Contains(diffSummary, "mode: non-executable") ||
		!strings.Contains(diffSummary, "M "+linkRel) ||
		!strings.Contains(diffSummary, "symlink: run.sh -> ../README.md") {
		t.Fatalf("expected metadata notes in diff stat output, got: %s", diffSummary)
	}

	restorePreview := runCLIOrFail(t, checkoutA, "slice", "restore", "--dry-run", linkRel)
	if !strings.Contains(restorePreview, "restore "+linkRel) ||
		!strings.Contains(restorePreview, "Would restore tracked files: 1") ||
		!strings.Contains(restorePreview, "Would remove new paths: 0") {
		t.Fatalf("expected targeted metadata restore preview, got: %s", restorePreview)
	}
	restoreOutput := runCLIOrFail(t, checkoutA, "slice", "restore", linkRel)
	if !strings.Contains(restoreOutput, "Restored tracked files: 1") || !strings.Contains(restoreOutput, "Removed new paths: 0") {
		t.Fatalf("expected targeted metadata restore output, got: %s", restoreOutput)
	}
	if target, err := os.Readlink(linkPathA); err != nil {
		t.Fatalf("read restored symlink: %v", err)
	} else if target != "run.sh" {
		t.Fatalf("expected restored symlink target, got %q", target)
	}

	statusResp = runCLIJSONOrFail[sliceStatusJSON](t, checkoutA, "slice", "status")
	if statusResp.WorkingTree != "dirty" || statusResp.Changes.Added != 0 || statusResp.Changes.Modified == 0 || statusResp.Changes.Deleted != 0 {
		t.Fatalf("expected restore to leave only dirty tracked metadata changes, got: %+v", statusResp)
	}

	diffSummary = runCLIOrFail(t, checkoutA, "slice", "diff", "--stat")
	if !strings.Contains(diffSummary, "M "+scriptRel) ||
		!strings.Contains(diffSummary, "mode: non-executable") ||
		strings.Contains(diffSummary, "M "+linkRel) {
		t.Fatalf("expected only executable metadata diff after symlink restore, got: %s", diffSummary)
	}

	restoreOutput = runCLIOrFail(t, checkoutA, "slice", "restore", scriptRel)
	if !strings.Contains(restoreOutput, "Restored tracked files: 1") || !strings.Contains(restoreOutput, "Removed new paths: 0") {
		t.Fatalf("expected script restore output, got: %s", restoreOutput)
	}
	if info, err := os.Stat(scriptPathA); err != nil {
		t.Fatalf("stat restored script: %v", err)
	} else if info.Mode().Perm() != 0o755 {
		t.Fatalf("expected restored script to be executable, got mode %o", info.Mode().Perm())
	}

	statusResp = runCLIJSONOrFail[sliceStatusJSON](t, checkoutA, "slice", "status")
	if statusResp.WorkingTree != "clean" || statusResp.Changes.Added != 0 || statusResp.Changes.Modified != 0 || statusResp.Changes.Deleted != 0 {
		t.Fatalf("expected metadata workflow to end clean after restoring both paths, got: %+v", statusResp)
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
	if string(restoredContent) != "seed focused folder\n" {
		t.Fatalf("expected tracked file to revert to original seeded content, got %q", string(restoredContent))
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
	if !strings.Contains(output, "Uploaded 1 file") || !strings.Contains(output, "Planning upload for 1 file") {
		t.Fatalf("expected upload output, got: %s", output)
	}

	localDownload := filepath.Join(t.TempDir(), "download")
	output = runCLIForUser("", "fs", "sync", "--direction", "pull", remoteBase, localDownload)
	if !strings.Contains(output, "Downloaded 1 file") {
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
