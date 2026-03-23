package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestCheckoutSliceReturnsManifest(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-slice"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg)
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

func TestCheckoutSliceNotFound(t *testing.T) {
	workdir := t.TempDir()
	sliceArg := sliceIDArg("nonexistent-slice")

	_, err := runCLIWithDirForTest(t, workdir, "slice", "checkout", sliceArg)
	if err == nil {
		t.Fatalf("expected checkout to fail for missing slice")
	}
}

func TestCheckoutSliceWithCommitHash(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-commit"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--commit", "HEAD")
	if resp.SliceID != sliceID || resp.Commit == "" {
		t.Fatalf("expected checkout JSON output with commit, got: %+v", resp)
	}
}

func TestCheckoutEmptySlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "empty-checkout"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg)
	if resp.SliceID != sliceID || resp.FileCount != 0 {
		t.Fatalf("expected checkout JSON output, got: %+v", resp)
	}
}

func TestStreamCheckoutSlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "stream-checkout"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg)
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
	if _, err := storage.WriteSliceFileManifest(ctx, testStorage, sliceID, filePath, content); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("add file to slice: %v", err)
	}
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--files")
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
	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceIDArg(sliceID), "--files")
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
