package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/storage"
)

func TestDetectNoGitModifiedFiles(t *testing.T) {
	workdir := t.TempDir()

	scriptContent := []byte("#!/bin/sh\necho ok\n")
	linkTarget := "bin/tool.sh"
	state := &localCheckoutState{
		SliceID:    "slice-test",
		CommitHash: "commit-1",
		Files: []checkoutTrackedFile{
			{
				Path: "README.md",
				Hash: storage.HashFileManifestContent([]byte("v1\n"), false, ""),
			},
			{
				Path:       "bin/tool.sh",
				Hash:       storage.HashFileManifestContent(scriptContent, true, ""),
				Executable: true,
			},
			{
				Path:          "bin/current",
				Hash:          storage.HashFileManifestContent([]byte(linkTarget), false, linkTarget),
				SymlinkTarget: linkTarget,
			},
			{
				Path: "docs/stale.txt",
				Hash: storage.HashFileManifestContent([]byte("gone\n"), false, ""),
			},
		},
	}
	if err := writeCheckoutState(workdir, state); err != nil {
		t.Fatalf("write checkout state: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(workdir, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "bin", "tool.sh"), scriptContent, 0o755); err != nil {
		t.Fatalf("write tool: %v", err)
	}
	if err := os.Symlink("bin/other.sh", filepath.Join(workdir, "bin", "current")); err != nil {
		t.Fatalf("write symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir .gs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".gs", "ignored.txt"), []byte("ignore\n"), 0o644); err != nil {
		t.Fatalf("write .gs ignored: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".git", "ignored.txt"), []byte("ignore\n"), 0o644); err != nil {
		t.Fatalf("write .git ignored: %v", err)
	}

	modified, err := detectNoGitModifiedFiles(workdir, state)
	if err != nil {
		t.Fatalf("detect modified files: %v", err)
	}

	want := []string{"README.md", "bin/current", "docs/stale.txt", "new.txt"}
	if !reflect.DeepEqual(modified, want) {
		t.Fatalf("unexpected modified files:\n got %#v\nwant %#v", modified, want)
	}
}

func TestDetectNoGitModifiedFilesTouchedButUnchanged(t *testing.T) {
	workdir := t.TempDir()
	fullPath := filepath.Join(workdir, "README.md")
	content := []byte("same\n")
	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	info, err := os.Lstat(fullPath)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	state := &localCheckoutState{
		SliceID:    "slice-test",
		CommitHash: "commit-1",
		Files: []checkoutTrackedFile{
			{
				Path:                 "README.md",
				Hash:                 storage.HashFileManifestContent(content, false, ""),
				Size:                 info.Size(),
				ModifiedTimeUnixNano: info.ModTime().UnixNano(),
				ChangeTimeUnixNano:   fileChangeTimeUnixNano(info),
			},
		},
	}
	if err := writeCheckoutState(workdir, state); err != nil {
		t.Fatalf("write checkout state: %v", err)
	}

	touchedAt := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(fullPath, touchedAt, touchedAt); err != nil {
		t.Fatalf("touch file: %v", err)
	}

	modified, err := detectNoGitModifiedFiles(workdir, state)
	if err != nil {
		t.Fatalf("detect modified files: %v", err)
	}
	if len(modified) != 0 {
		t.Fatalf("expected no modified files after touching unchanged content, got %#v", modified)
	}
}

func TestCheckoutStatesEqualContentIgnoresLocalMetadata(t *testing.T) {
	a := &localCheckoutState{
		SliceID: "slice-test",
		Files: []checkoutTrackedFile{
			{
				Path:                 "README.md",
				Hash:                 "hash-1",
				Size:                 12,
				ModifiedTimeUnixNano: 10,
			},
		},
	}
	b := &localCheckoutState{
		SliceID: "slice-test",
		Files: []checkoutTrackedFile{
			{
				Path:                 "README.md",
				Hash:                 "hash-1",
				Size:                 99,
				ModifiedTimeUnixNano: 999,
			},
		},
	}

	if !checkoutStatesEqualContent(a, b) {
		t.Fatal("expected checkoutStatesEqualContent to ignore local file metadata")
	}
}

func TestDetectNoGitModifiedFilesScansTrackedFilesInsideUnchangedDirectories(t *testing.T) {
	workdir := t.TempDir()
	docsDir := filepath.Join(workdir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	targetPath := filepath.Join(docsDir, "guide.md")
	originalContent := []byte("v1\n")
	if err := os.WriteFile(targetPath, originalContent, 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}

	fileInfo, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatalf("stat guide: %v", err)
	}
	state := &localCheckoutState{
		SliceID:    "slice-test",
		CommitHash: "commit-1",
		Files: []checkoutTrackedFile{
			{
				Path:                 "docs/guide.md",
				Hash:                 storage.HashFileManifestContent(originalContent, false, ""),
				Size:                 fileInfo.Size(),
				ModifiedTimeUnixNano: fileInfo.ModTime().UnixNano(),
				ChangeTimeUnixNano:   fileChangeTimeUnixNano(fileInfo),
			},
		},
	}
	if err := writeCheckoutState(workdir, state); err != nil {
		t.Fatalf("write checkout state: %v", err)
	}

	if err := os.WriteFile(targetPath, []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite guide: %v", err)
	}
	touchedAt := fileInfo.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(targetPath, touchedAt, touchedAt); err != nil {
		t.Fatalf("touch guide: %v", err)
	}

	modified, err := detectNoGitModifiedFiles(workdir, state)
	if err != nil {
		t.Fatalf("detect modified files: %v", err)
	}

	want := []string{"docs/guide.md"}
	if !reflect.DeepEqual(modified, want) {
		t.Fatalf("unexpected modified files:\n got %#v\nwant %#v", modified, want)
	}
}
