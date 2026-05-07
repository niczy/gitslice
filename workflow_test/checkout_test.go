package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
)

func TestCheckoutSliceReturnsManifest(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-slice"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--here")
	if resp.SliceID != sliceID || resp.FileCount != 0 {
		t.Fatalf("expected checkout JSON output, got: %+v", resp)
	}
	if len(resp.Files) != 0 {
		t.Fatalf("expected default checkout JSON to omit per-file listing, got: %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected default checkout to skip git metadata, err=%v", err)
	}
}

func TestCheckoutSliceDefaultsToSliceNameDirectory(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	sliceID := fmt.Sprintf("checkout-default-dir-%d", time.Now().UnixNano())
	sliceSlug := "api-cross-a-20260504232313"
	qualifiedRef := "nicholas/" + sliceSlug
	filePath := filepath.ToSlash(filepath.Join("fixtures", sliceID, "README.md"))
	content := []byte("default checkout directory\n")

	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "API Cross A",
		Slug:      sliceSlug,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: "nicholas",
	}); err != nil {
		t.Fatalf("CreateSlice(%s) failed: %v", sliceID, err)
	}
	manifest, err := storage.WriteSliceFileManifest(ctx, testStorage, sliceID, filePath, content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest(%s) failed: %v", filePath, err)
	}
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(content)),
		Hash:     manifest.Hash,
	}); err != nil {
		t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
	}
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
	}
	now := time.Now()
	commitHash := "seed-" + sliceID
	if err := testStorage.AddSliceCommit(ctx, sliceID, &models.Commit{
		CommitHash: commitHash,
		Timestamp:  now,
		Message:    "seed default checkout directory",
	}); err != nil {
		t.Fatalf("AddSliceCommit(%s) failed: %v", sliceID, err)
	}
	if err := testStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Files:      map[string]string{filePath: manifest.Hash},
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot(%s) failed: %v", commitHash, err)
	}
	metadata, err := testStorage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		t.Fatalf("GetSliceMetadata(%s) failed: %v", sliceID, err)
	}
	metadata.HeadCommitHash = commitHash
	metadata.ModifiedFiles = []string{filePath}
	metadata.ModifiedFilesCount = 1
	metadata.LastModified = now
	if err := testStorage.UpdateSliceMetadata(ctx, sliceID, metadata); err != nil {
		t.Fatalf("UpdateSliceMetadata(%s) failed: %v", sliceID, err)
	}

	parentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(parentDir, "unrelated.txt"), []byte("keep parent usable\n"), 0o644); err != nil {
		t.Fatalf("write unrelated parent file: %v", err)
	}
	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, parentDir, "slice", "checkout", qualifiedRef)
	if resp.SliceID != sliceID || resp.Path != sliceSlug {
		t.Fatalf("expected checkout into %q, got: %+v", sliceSlug, resp)
	}

	checkoutDir := filepath.Join(parentDir, sliceSlug)
	checkedOutContent, err := os.ReadFile(filepath.Join(checkoutDir, filePath))
	if err != nil {
		t.Fatalf("read checked out file: %v", err)
	}
	if string(checkedOutContent) != string(content) {
		t.Fatalf("expected checked out content %q, got %q", string(content), string(checkedOutContent))
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, ".gs", "config")); err != nil {
		t.Fatalf("expected checkout metadata under target directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parentDir, filePath)); !os.IsNotExist(err) {
		t.Fatalf("expected checkout not to write files into parent dir, err=%v", err)
	}
}

func TestCheckoutSliceNotFound(t *testing.T) {
	workdir := t.TempDir()
	sliceArg := sliceIDArg("nonexistent-slice")

	_, err := runCLIWithDirForTest(t, workdir, "slice", "checkout", sliceArg, "--here")
	if err == nil {
		t.Fatalf("expected checkout to fail for missing slice")
	}
}

func TestCheckoutSliceWithCommitHash(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-commit"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--here", "--commit", "HEAD")
	if resp.SliceID != sliceID || resp.Commit == "" {
		t.Fatalf("expected checkout JSON output with commit, got: %+v", resp)
	}
}

func TestCheckoutEmptySlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "empty-checkout"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--here")
	if resp.SliceID != sliceID || resp.FileCount != 0 {
		t.Fatalf("expected checkout JSON output, got: %+v", resp)
	}
}

func TestStreamCheckoutSlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "stream-checkout"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--here")
	if resp.SliceID != sliceID || resp.FileCount != 0 {
		t.Fatalf("expected checkout JSON output, got: %+v", resp)
	}
}

func TestCheckoutSlicePrintsFilesWhenRequested(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	workdir := t.TempDir()
	sliceID := "checkout-files-flag"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}
	filePath := "pkg/readme.txt"
	content := []byte("hello\n")
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	manifest, err := storage.WriteSliceFileManifest(ctx, testStorage, sliceID, filePath, content)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("add file to slice: %v", err)
	}
	setWorkflowSliceHead(t, ctx, sliceID, "checkout-search-artifact-1", "", map[string]string{
		filePath: manifest.Hash,
	})
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--here", "--files")
	if resp.SliceID != sliceID || resp.FileCount != 1 || len(resp.Files) != 1 {
		t.Fatalf("expected verbose checkout JSON output, got: %+v", resp)
	}
	if resp.Files[0].Path != "pkg/readme.txt" || resp.Files[0].Size != int64(len(content)) {
		t.Fatalf("expected file listing in verbose checkout output, got: %+v", resp)
	}
}

func TestCheckoutSliceHonorsFileMetadata(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	sliceID := "checkout-metadata-slice"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}

	scriptPath := "bin/run.sh"
	scriptContent := []byte("#!/bin/sh\necho integration\n")
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:         sliceID + ":" + scriptPath,
		Path:       scriptPath,
		Type:       "file",
		ParentID:   sliceID,
		Size:       int64(len(scriptContent)),
		Executable: true,
	}); err != nil {
		t.Fatalf("add script entry: %v", err)
	}
	if _, err := storage.WriteSliceFileManifestWithMetadata(ctx, testStorage, sliceID, scriptPath, scriptContent, true, ""); err != nil {
		t.Fatalf("write script manifest: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, scriptPath, sliceID); err != nil {
		t.Fatalf("add script to slice: %v", err)
	}

	linkPath := "bin/current"
	linkTarget := "bin/run.sh"
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:            sliceID + ":" + linkPath,
		Path:          linkPath,
		Type:          "file",
		ParentID:      sliceID,
		Size:          int64(len(linkTarget)),
		SymlinkTarget: linkTarget,
	}); err != nil {
		t.Fatalf("add symlink entry: %v", err)
	}
	if _, err := storage.WriteSliceFileManifestWithMetadata(ctx, testStorage, sliceID, linkPath, []byte(linkTarget), false, linkTarget); err != nil {
		t.Fatalf("write symlink manifest: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, linkPath, sliceID); err != nil {
		t.Fatalf("add symlink to slice: %v", err)
	}

	workdir := t.TempDir()
	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceIDArg(sliceID), "--here", "--files")
	if resp.SliceID != sliceID || resp.FileCount != 2 || len(resp.Files) != 2 {
		t.Fatalf("expected checkout JSON output, got: %+v", resp)
	}

	scriptInfo, err := os.Stat(filepath.Join(workdir, scriptPath))
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if scriptInfo.Mode().Perm() != 0o755 {
		t.Fatalf("expected executable mode 0755, got %o", scriptInfo.Mode().Perm())
	}
	if got, err := os.Readlink(filepath.Join(workdir, linkPath)); err != nil {
		t.Fatalf("readlink: %v", err)
	} else if got != linkTarget {
		t.Fatalf("expected symlink target %q, got %q", linkTarget, got)
	}
}

func TestCheckoutPersistsSliceSearchArtifact(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	sliceID := "checkout-search-artifact"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}

	filePath := "docs/readme.md"
	content := []byte("alpha beta gamma\n")
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	manifest, err := storage.WriteSliceFileManifest(ctx, testStorage, sliceID, filePath, content)
	if err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("add file to slice: %v", err)
	}
	setWorkflowSliceHead(t, ctx, sliceID, "checkout-search-artifact-local-1", "", map[string]string{
		filePath: manifest.Hash,
	})

	workdir := t.TempDir()
	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceIDArg(sliceID), "--here")
	meta := readSearchArtifactMetadata(t, workdir)
	if meta.Source != "downloaded" {
		t.Fatalf("expected downloaded search artifact, got %+v", meta)
	}
	if meta.CommitHash != resp.Commit || meta.Version != searchindex.CurrentArtifactVersion {
		t.Fatalf("unexpected search artifact metadata: %+v", meta)
	}
	artifact := readBaseSearchArtifact(t, workdir)
	if artifact.CommitHash != resp.Commit || len(artifact.Files) != 1 || artifact.Files[0].Path != filePath {
		t.Fatalf("unexpected search artifact: %+v", artifact)
	}
}

func TestCheckoutSearchArtifactFallsBackToLocalBuild(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	sliceID := "checkout-search-artifact-local"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}

	filePath := "docs/readme.md"
	content := []byte("delta epsilon zeta\n")
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       sliceID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: sliceID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	if _, err := storage.WriteSliceFileManifest(ctx, testStorage, sliceID, filePath, content); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("add file to slice: %v", err)
	}

	workdir := t.TempDir()
	env := map[string]string{"GS_DISABLE_SLICE_SEARCH_ARTIFACT_DOWNLOAD": "1"}
	resp := runCLIJSONWithEnvOrFail[sliceCheckoutJSON](t, workdir, env, "slice", "checkout", sliceIDArg(sliceID), "--here")
	meta := readSearchArtifactMetadata(t, workdir)
	if meta.Source != "rebuilt_local" {
		t.Fatalf("expected rebuilt_local search artifact, got %+v", meta)
	}
	artifact := readBaseSearchArtifact(t, workdir)
	if artifact.CommitHash != resp.Commit || len(artifact.Files) != 1 || artifact.Files[0].Path != filePath {
		t.Fatalf("unexpected fallback artifact: %+v", artifact)
	}
}

func TestSliceSyncReplacesSliceSearchArtifactWhenCommitChanges(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withWorkflowUser(t, context.Background())
	sliceID := "sync-search-artifact"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}

	filePath := "docs/readme.md"
	setHead := func(commitHash, parentHash string, content []byte) {
		t.Helper()
		entry, err := testStorage.GetEntryByPath(ctx, sliceID, filePath)
		if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			t.Fatalf("get entry: %v", err)
		}
		if entry == nil {
			entry = &models.DirectoryEntry{
				ID:       sliceID + ":" + filePath,
				Path:     filePath,
				Type:     "file",
				ParentID: sliceID,
			}
			if err := testStorage.AddEntry(ctx, entry); err != nil {
				t.Fatalf("add entry: %v", err)
			}
		}
		entry.Size = int64(len(content))
		if err := testStorage.UpdateEntry(ctx, entry); err != nil {
			t.Fatalf("update entry: %v", err)
		}
		manifestHash := mustWriteSliceManifest(t, ctx, testStorage, sliceID, filePath, content)
		if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
			t.Fatalf("add file to slice: %v", err)
		}

		now := time.Now()
		if err := testStorage.AddSliceCommit(ctx, sliceID, &models.Commit{
			CommitHash: commitHash,
			ParentHash: parentHash,
			Message:    "seed " + commitHash,
			Timestamp:  now,
		}); err != nil {
			t.Fatalf("add commit: %v", err)
		}
		if err := testStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
			CommitHash: commitHash,
			SliceID:    sliceID,
			Files: map[string]string{
				filePath: manifestHash,
			},
			Timestamp: now,
		}); err != nil {
			t.Fatalf("save snapshot: %v", err)
		}
		meta, err := testStorage.GetSliceMetadata(ctx, sliceID)
		if err != nil {
			t.Fatalf("get slice metadata: %v", err)
		}
		meta.HeadCommitHash = commitHash
		meta.LastModified = now
		meta.ModifiedFiles = []string{filePath}
		meta.ModifiedFilesCount = 1
		if err := testStorage.UpdateSliceMetadata(ctx, sliceID, meta); err != nil {
			t.Fatalf("update slice metadata: %v", err)
		}
	}

	setHead("sync-search-artifact-1", "", []byte("alpha beta gamma\n"))

	workdir := t.TempDir()
	checkoutResp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceIDArg(sliceID), "--here")
	firstMeta := readSearchArtifactMetadata(t, workdir)
	firstArtifact := readBaseSearchArtifact(t, workdir)
	if firstMeta.CommitHash != checkoutResp.Commit || firstArtifact.CommitHash != checkoutResp.Commit {
		t.Fatalf("unexpected initial artifact state: meta=%+v artifact=%+v", firstMeta, firstArtifact)
	}
	if firstArtifact.Files[0].SearchContentHash != searchindex.SearchContentHash([]byte("alpha beta gamma\n")) {
		t.Fatalf("unexpected initial search hash: %+v", firstArtifact.Files[0])
	}

	setHead("sync-search-artifact-2", "sync-search-artifact-1", []byte("updated searchable text\n"))

	syncResp := runCLIJSONOrFail[sliceSyncJSON](t, workdir, "slice", "sync")
	secondMeta := readSearchArtifactMetadata(t, workdir)
	secondArtifact := readBaseSearchArtifact(t, workdir)
	if syncResp.Commit != "sync-search-artifact-2" || secondMeta.CommitHash != syncResp.Commit || secondArtifact.CommitHash != syncResp.Commit {
		t.Fatalf("expected synced artifact to advance to commit %q, got sync=%+v meta=%+v artifact=%+v", "sync-search-artifact-2", syncResp, secondMeta, secondArtifact)
	}
	if secondArtifact.Files[0].SearchContentHash != searchindex.SearchContentHash([]byte("updated searchable text\n")) {
		t.Fatalf("unexpected updated search hash: %+v", secondArtifact.Files[0])
	}
}

func readSearchArtifactMetadata(t *testing.T, workdir string) struct {
	Version    uint32 `json:"version"`
	SliceID    string `json:"slice_id"`
	CommitHash string `json:"commit_hash"`
	Source     string `json:"source"`
} {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(workdir, ".gs", "search", "metadata.json"))
	if err != nil {
		t.Fatalf("read search artifact metadata: %v", err)
	}
	var meta struct {
		Version    uint32 `json:"version"`
		SliceID    string `json:"slice_id"`
		CommitHash string `json:"commit_hash"`
		Source     string `json:"source"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal search artifact metadata: %v", err)
	}
	return meta
}

func readBaseSearchArtifact(t *testing.T, workdir string) *searchindex.SliceArtifact {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(workdir, ".gs", "search", "base.artifact"))
	if err != nil {
		t.Fatalf("read base search artifact: %v", err)
	}
	artifact, err := searchindex.DecodeSliceArtifact(raw)
	if err != nil {
		t.Fatalf("decode base search artifact: %v", err)
	}
	return artifact
}

func setWorkflowSliceHead(t *testing.T, ctx context.Context, sliceID, commitHash, parentHash string, files map[string]string) {
	t.Helper()

	now := time.Now()
	if err := testStorage.AddSliceCommit(ctx, sliceID, &models.Commit{
		CommitHash: commitHash,
		ParentHash: parentHash,
		Message:    "seed " + commitHash,
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("add slice commit: %v", err)
	}
	if err := testStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    sliceID,
		Files:      files,
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("save commit snapshot: %v", err)
	}
	meta, err := testStorage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		t.Fatalf("get slice metadata: %v", err)
	}
	meta.HeadCommitHash = commitHash
	meta.LastModified = now
	meta.ModifiedFiles = make([]string, 0, len(files))
	for path := range files {
		meta.ModifiedFiles = append(meta.ModifiedFiles, path)
	}
	meta.ModifiedFilesCount = len(meta.ModifiedFiles)
	if err := testStorage.UpdateSliceMetadata(ctx, sliceID, meta); err != nil {
		t.Fatalf("update slice metadata: %v", err)
	}
}
