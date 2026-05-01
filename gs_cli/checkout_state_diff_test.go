package gscli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/storage"
)

func addTestDirectoryRecords(t *testing.T, workdir string, index *localCheckoutIndex, dirs ...string) {
	t.Helper()
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		normalized := normalizeTrackedDirectoryPath(dir)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		record, _, err := currentCheckoutDirectorySnapshot(workdir, normalized)
		if err != nil {
			t.Fatalf("snapshot dir %q: %v", normalized, err)
		}
		index.Directories = append(index.Directories, record)
	}
}

func TestCheckoutTrackedFileMatchesRecreatedSameContent(t *testing.T) {
	workdir := t.TempDir()
	path := filepath.Join(workdir, "README.md")
	content := []byte("same content\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write original file: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat original file: %v", err)
	}
	tracked := checkoutTrackedFile{
		Path: "README.md",
		Hash: storage.HashFileManifestContent(content, false, ""),
	}
	populateTrackedFileLocalMetadata(&tracked, info)

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove original file: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("recreate file: %v", err)
	}
	recreatedInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat recreated file: %v", err)
	}

	matches, err := checkoutTrackedFileMatches(path, recreatedInfo, tracked)
	if err != nil {
		t.Fatalf("checkoutTrackedFileMatches failed: %v", err)
	}
	if !matches {
		t.Fatalf("expected recreated file with same content and mode to match checkout index")
	}
}

func TestDetectNoGitModifiedFiles(t *testing.T) {
	workdir := t.TempDir()

	scriptContent := []byte("#!/bin/sh\necho ok\n")
	linkTarget := "bin/tool.sh"
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
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

	addTestDirectoryRecords(t, workdir, index, "", "bin", "docs")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	modified, err := detectNoGitModifiedFiles(workdir, index)
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
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
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
	addTestDirectoryRecords(t, workdir, index, "")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}

	touchedAt := info.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(fullPath, touchedAt, touchedAt); err != nil {
		t.Fatalf("touch file: %v", err)
	}

	modified, err := detectNoGitModifiedFiles(workdir, index)
	if err != nil {
		t.Fatalf("detect modified files: %v", err)
	}
	if len(modified) != 0 {
		t.Fatalf("expected no modified files after touching unchanged content, got %#v", modified)
	}
}

func TestCheckoutIndicesEqualContentIgnoresLocalMetadata(t *testing.T) {
	a := &localCheckoutIndex{
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
	b := &localCheckoutIndex{
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

	if !checkoutIndicesEqualContent(a, b) {
		t.Fatal("expected checkoutIndicesEqualContent to ignore local file metadata")
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
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
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
	addTestDirectoryRecords(t, workdir, index, "", "docs")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}

	if err := os.WriteFile(targetPath, []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("rewrite guide: %v", err)
	}
	touchedAt := fileInfo.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(targetPath, touchedAt, touchedAt); err != nil {
		t.Fatalf("touch guide: %v", err)
	}

	modified, err := detectNoGitModifiedFiles(workdir, index)
	if err != nil {
		t.Fatalf("detect modified files: %v", err)
	}

	want := []string{"docs/guide.md"}
	if !reflect.DeepEqual(modified, want) {
		t.Fatalf("unexpected modified files:\n got %#v\nwant %#v", modified, want)
	}
}

func TestDetectNoGitModifiedFilesFindsNestedNewFilesUnderStableTrackedAncestors(t *testing.T) {
	workdir := t.TempDir()
	srcDir := filepath.Join(workdir, "apps", "web", "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	mainPath := filepath.Join(srcDir, "main.ts")
	mainContent := []byte("console.log('ready')\n")
	if err := os.WriteFile(mainPath, mainContent, 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	mainInfo, err := os.Lstat(mainPath)
	if err != nil {
		t.Fatalf("stat main: %v", err)
	}

	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    "slice-test",
		CommitHash: "commit-1",
		Files: []checkoutTrackedFile{
			{
				Path:                 "apps/web/src/main.ts",
				Hash:                 storage.HashFileManifestContent(mainContent, false, ""),
				Size:                 mainInfo.Size(),
				ModifiedTimeUnixNano: mainInfo.ModTime().UnixNano(),
				ChangeTimeUnixNano:   fileChangeTimeUnixNano(mainInfo),
			},
		},
	}
	addTestDirectoryRecords(t, workdir, index, "", "apps", "apps/web", "apps/web/src")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "new.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	modified, err := detectNoGitModifiedFiles(workdir, index)
	if err != nil {
		t.Fatalf("detect modified files: %v", err)
	}

	want := []string{"apps/web/src/new.ts"}
	if !reflect.DeepEqual(modified, want) {
		t.Fatalf("unexpected modified files:\n got %#v\nwant %#v", modified, want)
	}
}

func TestCheckoutIndexBinaryRoundTrip(t *testing.T) {
	workdir := t.TempDir()
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    "slice-test",
		CommitHash: "commit-42",
		Files: []checkoutTrackedFile{
			{
				Path:                 "README.md",
				Hash:                 "hash-readme",
				Executable:           false,
				SymlinkTarget:        "",
				Size:                 12,
				ModifiedTimeUnixNano: 101,
				ChangeTimeUnixNano:   202,
				Device:               303,
				Inode:                404,
			},
			{
				Path:                 "bin/tool",
				Hash:                 "hash-tool",
				Executable:           true,
				SymlinkTarget:        "",
				Size:                 99,
				ModifiedTimeUnixNano: 505,
				ChangeTimeUnixNano:   606,
				Device:               707,
				Inode:                808,
			},
		},
		Directories: []checkoutTrackedDirectory{
			{
				Path:                 "",
				ModifiedTimeUnixNano: 11,
				ChangeTimeUnixNano:   22,
				Device:               33,
				Inode:                44,
				ChildCount:           2,
				ChildNameFingerprint: 55,
			},
			{
				Path:                 "bin",
				ModifiedTimeUnixNano: 66,
				ChangeTimeUnixNano:   77,
				Device:               88,
				Inode:                99,
				ChildCount:           1,
				ChildNameFingerprint: 111,
			},
		},
	}

	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}

	roundTrip, err := readCheckoutIndex(workdir)
	if err != nil {
		t.Fatalf("read checkout index: %v", err)
	}

	if !reflect.DeepEqual(roundTrip, index) {
		t.Fatalf("unexpected round-trip index:\n got %#v\nwant %#v", roundTrip, index)
	}
}
