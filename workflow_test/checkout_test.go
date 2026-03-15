package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestCheckoutSliceReturnsManifest(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-slice"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceArg)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestCheckoutSliceNotFound(t *testing.T) {
	workdir := t.TempDir()
	sliceArg := sliceIDArg("nonexistent-slice")

	_, err := runCLIWithDir(workdir, "slice", "checkout", sliceArg)
	if err == nil {
		t.Fatalf("expected checkout to fail for missing slice")
	}
}

func TestCheckoutSliceWithCommitHash(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "checkout-commit"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceArg, "--commit", "HEAD")
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestCheckoutEmptySlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "empty-checkout"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceArg)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestStreamCheckoutSlice(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "stream-checkout"

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceArg)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
}

func TestCheckoutSliceHonorsFileMetadata(t *testing.T) {
	if testStorage == nil {
		t.Fatalf("test storage is not initialized")
	}

	ctx := withTestUser(context.Background())
	sliceID := "checkout-metadata-slice"
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{testUsername},
		CreatedBy: testUsername,
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
	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceIDArg(sliceID))
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
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
