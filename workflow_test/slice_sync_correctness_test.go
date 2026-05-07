package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func updateWorkflowSliceHead(t *testing.T, sliceID, commitHash string, files map[string]seededWorkflowFile) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	if testStorage == nil {
		t.Fatal("expected test storage to be initialized")
	}

	metadata, err := testStorage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		t.Fatalf("GetSliceMetadata(%s) failed: %v", sliceID, err)
	}
	parentHash := strings.TrimSpace(metadata.HeadCommitHash)

	existingPaths := map[string]struct{}{}
	if parentHash != "" {
		snapshot, err := testStorage.GetCommitSnapshot(ctx, parentHash)
		if err != nil {
			t.Fatalf("GetCommitSnapshot(%s) failed: %v", parentHash, err)
		}
		for path := range snapshot.Files {
			existingPaths[path] = struct{}{}
		}
	}

	for path := range existingPaths {
		if _, ok := files[path]; ok {
			continue
		}
		entry, err := testStorage.GetEntryByPath(ctx, sliceID, path)
		if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			t.Fatalf("GetEntryByPath(%s) failed: %v", path, err)
		}
		if entry != nil {
			if err := testStorage.DeleteEntry(ctx, entry.ID); err != nil {
				t.Fatalf("DeleteEntry(%s) failed: %v", path, err)
			}
		}
		if err := testStorage.DeleteFileManifest(ctx, sliceID, path); err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			t.Fatalf("DeleteFileManifest(%s) failed: %v", path, err)
		}
		if err := testStorage.RemoveFileFromSlice(ctx, path, sliceID); err != nil {
			t.Fatalf("RemoveFileFromSlice(%s) failed: %v", path, err)
		}
	}

	snapshotFiles := make(map[string]string, len(files))
	paths := make([]string, 0, len(files))
	for path, file := range files {
		content := append([]byte(nil), file.content...)
		if file.symlinkTarget != "" && len(content) == 0 {
			content = []byte(file.symlinkTarget)
		}

		entry, err := testStorage.GetEntryByPath(ctx, sliceID, path)
		if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			t.Fatalf("GetEntryByPath(%s) failed: %v", path, err)
		}
		if entry == nil {
			entry = &models.DirectoryEntry{
				ID:       common.GenerateEntryID(sliceID, path),
				Path:     path,
				Type:     "file",
				ParentID: sliceID,
			}
			if err := testStorage.AddEntry(ctx, entry); err != nil {
				t.Fatalf("AddEntry(%s) failed: %v", path, err)
			}
		}

		entry.Size = int64(len(content))
		entry.Executable = file.executable
		entry.SymlinkTarget = file.symlinkTarget
		if err := testStorage.UpdateEntry(ctx, entry); err != nil {
			t.Fatalf("UpdateEntry(%s) failed: %v", path, err)
		}

		manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, testStorage, sliceID, path, content, file.executable, file.symlinkTarget)
		if err != nil {
			t.Fatalf("WriteSliceFileManifestWithMetadata(%s) failed: %v", path, err)
		}
		if err := testStorage.AddFileToSlice(ctx, path, sliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", path, err)
		}

		snapshotFiles[path] = manifest.Hash
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if err := testStorage.SetSliceFiles(ctx, sliceID, paths); err != nil {
		t.Fatalf("SetSliceFiles(%s) failed: %v", sliceID, err)
	}

	now := time.Now()
	if err := testStorage.AddSliceCommit(ctx, sliceID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: parentHash,
		Timestamp:  now,
		Message:    "update slice for sync workflow",
	}); err != nil {
		t.Fatalf("AddSliceCommit(%s) failed: %v", commitHash, err)
	}
	if err := testStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Files:      snapshotFiles,
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot(%s) failed: %v", commitHash, err)
	}

	metadata.HeadCommitHash = commitHash
	metadata.LastModified = now
	metadata.ModifiedFiles = append(metadata.ModifiedFiles[:0], paths...)
	metadata.ModifiedFilesCount = len(paths)
	if err := testStorage.UpdateSliceMetadata(ctx, sliceID, metadata); err != nil {
		t.Fatalf("UpdateSliceMetadata(%s) failed: %v", sliceID, err)
	}

	return commitHash
}

func assertExecutableMode(t *testing.T, path string, want bool) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	got := info.Mode().Perm()&0o111 != 0
	if got != want {
		t.Fatalf("unexpected executable mode for %s: got=%v mode=%#o want=%v", path, got, info.Mode().Perm(), want)
	}
}

func assertSymlinkTarget(t *testing.T, path, want string) {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink, mode=%v", path, info.Mode())
	}
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	if got != want {
		t.Fatalf("unexpected symlink target for %s: got=%q want=%q", path, got, want)
	}
}

func TestSliceSyncUpdatesRemoteMetadataAndStatus(t *testing.T) {
	folderPath := fmt.Sprintf("apps/sync-metadata-%d", time.Now().UnixNano())
	sliceID := fmt.Sprintf("slice-sync-metadata-%d", time.Now().UnixNano())
	readmePath := filepath.ToSlash(filepath.Join(folderPath, "README.md"))
	changelogPath := filepath.ToSlash(filepath.Join(folderPath, "CHANGELOG.md"))
	scriptRelPath := filepath.ToSlash(filepath.Join(folderPath, "bin", "tool.sh"))
	linkRelPath := filepath.ToSlash(filepath.Join(folderPath, "docs", "current"))
	referencePath := filepath.ToSlash(filepath.Join(folderPath, "docs", "reference.txt"))
	createSeededWorkflowSlice(t, sliceID, map[string]seededWorkflowFile{
		readmePath:    {content: []byte("root readme\n")},
		scriptRelPath: {content: []byte("#!/bin/sh\necho v1\n"), executable: true},
		linkRelPath:   {content: []byte("../README.md"), symlinkTarget: "../README.md"},
		referencePath: {content: []byte("reference\n")},
	})

	checkoutDir := t.TempDir()
	t.Cleanup(func() {
		stopDirtyTrackerForTest(t, checkoutDir)
	})
	checkoutResp := runCLIJSONWithEnvOrFail[sliceCheckoutJSON](t, checkoutDir, nil, "slice", "checkout", sliceIDArg(sliceID), "--here")
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	scriptPath := filepath.Join(checkoutDir, filepath.FromSlash(scriptRelPath))
	linkPath := filepath.Join(checkoutDir, filepath.FromSlash(linkRelPath))
	assertExecutableMode(t, scriptPath, true)
	assertSymlinkTarget(t, linkPath, "../README.md")

	initialStatus := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, nil, "slice", "status", "--remote")
	if !initialStatus.RemoteQueried || initialStatus.SyncStatus != "current" || initialStatus.WorkingTree != "clean" || initialStatus.RemoteHead == "" {
		t.Fatalf("expected clean current remote status, got: %+v", initialStatus)
	}

	nextCommit := updateWorkflowSliceHead(t, sliceID, fmt.Sprintf("sync-meta-%d", time.Now().UnixNano()), map[string]seededWorkflowFile{
		readmePath:    {content: []byte("root readme\n")},
		changelogPath: {content: []byte("synced changelog\n")},
		scriptRelPath: {content: []byte("#!/bin/sh\necho v2\n"), executable: false},
		linkRelPath:   {content: []byte("../CHANGELOG.md"), symlinkTarget: "../CHANGELOG.md"},
		referencePath: {content: []byte("reference updated\n")},
	})

	behindStatus := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, nil, "slice", "status", "--remote")
	if !behindStatus.RemoteQueried || behindStatus.RemoteHead != nextCommit || behindStatus.SyncStatus != "behind_remote_head" || behindStatus.WorkingTree != "clean" {
		t.Fatalf("expected behind-remote clean status, got: %+v", behindStatus)
	}

	syncResp := runCLIJSONWithEnvOrFail[sliceSyncJSON](t, checkoutDir, nil, "slice", "sync")
	if syncResp.SliceID != sliceID || syncResp.Status != "updated" || syncResp.Commit != nextCommit {
		t.Fatalf("expected updated sync output, got: %+v", syncResp)
	}

	assertExecutableMode(t, scriptPath, false)
	assertSymlinkTarget(t, linkPath, "../CHANGELOG.md")

	changelog, err := os.ReadFile(filepath.Join(checkoutDir, filepath.FromSlash(changelogPath)))
	if err != nil {
		t.Fatalf("read synced changelog: %v", err)
	}
	if string(changelog) != "synced changelog\n" {
		t.Fatalf("unexpected synced changelog content: %q", string(changelog))
	}

	reference, err := os.ReadFile(filepath.Join(checkoutDir, filepath.FromSlash(referencePath)))
	if err != nil {
		t.Fatalf("read synced reference: %v", err)
	}
	if string(reference) != "reference updated\n" {
		t.Fatalf("unexpected synced reference content: %q", string(reference))
	}

	finalStatus := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, nil, "slice", "status", "--remote")
	if !finalStatus.RemoteQueried || finalStatus.RemoteHead != nextCommit || finalStatus.SyncStatus != "current" || finalStatus.WorkingTree != "clean" {
		t.Fatalf("expected current clean status after sync, got: %+v", finalStatus)
	}
	if finalStatus.Changes.Added != 0 || finalStatus.Changes.Modified != 0 || finalStatus.Changes.Deleted != 0 {
		t.Fatalf("expected no remaining local changes after sync, got: %+v", finalStatus)
	}
}

func TestSliceSyncFailsOnDirtyCheckoutAndPreservesLocalChanges(t *testing.T) {
	sliceID := fmt.Sprintf("slice-sync-dirty-%d", time.Now().UnixNano())
	readmePath := filepath.ToSlash(filepath.Join(fmt.Sprintf("apps/sync-dirty-%d", time.Now().UnixNano()), "docs", "readme.md"))
	createSeededWorkflowSlice(t, sliceID, map[string]seededWorkflowFile{
		readmePath: {content: []byte("remote v1\n")},
	})

	checkoutDir := t.TempDir()
	t.Cleanup(func() {
		stopDirtyTrackerForTest(t, checkoutDir)
	})
	checkoutResp := runCLIJSONWithEnvOrFail[sliceCheckoutJSON](t, checkoutDir, nil, "slice", "checkout", sliceIDArg(sliceID), "--here")
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	nextCommit := updateWorkflowSliceHead(t, sliceID, fmt.Sprintf("sync-dirty-%d", time.Now().UnixNano()), map[string]seededWorkflowFile{
		readmePath: {content: []byte("remote v2\n")},
	})

	localReadme := filepath.Join(checkoutDir, filepath.FromSlash(readmePath))
	if err := os.WriteFile(localReadme, []byte("local dirty\n"), 0o644); err != nil {
		t.Fatalf("write local dirty file: %v", err)
	}

	statusResp := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, nil, "slice", "status")
	if statusResp.WorkingTree != "dirty" || statusResp.Changes.Modified != 1 || statusResp.SyncStatus != "skipped" {
		t.Fatalf("expected local dirty status before sync, got: %+v", statusResp)
	}
	remoteStatus := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, nil, "slice", "status", "--remote")
	if !remoteStatus.RemoteQueried || remoteStatus.SyncStatus != "behind_remote_head" || remoteStatus.RemoteHead != nextCommit || remoteStatus.WorkingTree != "dirty" {
		t.Fatalf("expected dirty checkout to be behind remote head, got: %+v", remoteStatus)
	}

	stdout, stderr, err := runCLIWithDirInputEnvLegacyUserStreams(
		checkoutDir,
		"",
		workflowProcessEnv(t, nil),
		true,
		workflowUsername(t),
		"slice",
		"sync",
		"--json",
	)
	errResp := assertCLIJSONError(t, stdout, stderr, err, 5, "WORKING_TREE_DIRTY")
	if errResp.SuggestedAction != "gs slice diff" {
		t.Fatalf("expected sync error to suggest diff, got: %+v", errResp)
	}

	got, err := os.ReadFile(localReadme)
	if err != nil {
		t.Fatalf("read local dirty file after failed sync: %v", err)
	}
	if string(got) != "local dirty\n" {
		t.Fatalf("expected failed sync to preserve local content, got %q", string(got))
	}

	restoreOutput := runCLIWithEnvOrFail(t, checkoutDir, nil, "slice", "restore", readmePath)
	if !strings.Contains(restoreOutput, "Restored tracked files: 1") {
		t.Fatalf("expected targeted restore output, got: %s", restoreOutput)
	}

	cleanBehindStatus := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, nil, "slice", "status", "--remote")
	if cleanBehindStatus.WorkingTree != "clean" || cleanBehindStatus.SyncStatus != "behind_remote_head" || cleanBehindStatus.RemoteHead != nextCommit {
		t.Fatalf("expected clean but behind status after restore, got: %+v", cleanBehindStatus)
	}

	syncResp := runCLIJSONWithEnvOrFail[sliceSyncJSON](t, checkoutDir, nil, "slice", "sync")
	if syncResp.SliceID != sliceID || syncResp.Status != "updated" || syncResp.Commit != nextCommit {
		t.Fatalf("expected successful sync after restore, got: %+v", syncResp)
	}

	got, err = os.ReadFile(localReadme)
	if err != nil {
		t.Fatalf("read synced file: %v", err)
	}
	if string(got) != "remote v2\n" {
		t.Fatalf("expected synced content after restore, got %q", string(got))
	}

	finalStatus := runCLIJSONWithEnvOrFail[sliceStatusJSON](t, checkoutDir, nil, "slice", "status", "--remote")
	if finalStatus.WorkingTree != "clean" || finalStatus.SyncStatus != "current" || finalStatus.RemoteHead != nextCommit {
		t.Fatalf("expected current clean status after sync, got: %+v", finalStatus)
	}
}
