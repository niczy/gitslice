package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/niczy/gitslice/internal/storage"
)

func TestDirtyTrackerEventPaths(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "apps", "web", "new.ts")

	got := dirtyTrackerEventPaths(root, eventPath, fsnotify.Create)
	want := []string{"apps/web", "apps/web/new.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected dirty tracker event paths:\n got %#v\nwant %#v", got, want)
	}
}

func TestCollectNoGitWorkingTreeStatusFromCandidates(t *testing.T) {
	workdir := t.TempDir()
	docsDir := filepath.Join(workdir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}

	readmePath := filepath.Join(workdir, "README.md")
	guidePath := filepath.Join(docsDir, "guide.md")
	if err := os.WriteFile(readmePath, []byte("ready\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(guidePath, []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write guide: %v", err)
	}

	readmeInfo, err := os.Lstat(readmePath)
	if err != nil {
		t.Fatalf("stat README: %v", err)
	}
	guideInfo, err := os.Lstat(guidePath)
	if err != nil {
		t.Fatalf("stat guide: %v", err)
	}

	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    "slice-test",
		CommitHash: "commit-1",
		Files: []checkoutTrackedFile{
			{
				Path:                 "README.md",
				Hash:                 storage.HashFileManifestContent([]byte("ready\n"), false, ""),
				Size:                 readmeInfo.Size(),
				ModifiedTimeUnixNano: readmeInfo.ModTime().UnixNano(),
				ChangeTimeUnixNano:   fileChangeTimeUnixNano(readmeInfo),
			},
			{
				Path:                 "docs/guide.md",
				Hash:                 storage.HashFileManifestContent([]byte("v1\n"), false, ""),
				Size:                 guideInfo.Size(),
				ModifiedTimeUnixNano: guideInfo.ModTime().UnixNano(),
				ChangeTimeUnixNano:   fileChangeTimeUnixNano(guideInfo),
			},
		},
	}
	addTestDirectoryRecords(t, workdir, index, "", "docs")

	if err := os.WriteFile(guidePath, []byte("version-two\n"), 0o644); err != nil {
		t.Fatalf("rewrite guide: %v", err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "new.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	lookup := newCheckoutIndexLookup(index)
	entries, remaining, err := collectNoGitWorkingTreeStatusFromCandidates(workdir, lookup, []string{"docs/guide.md", "docs"})
	if err != nil {
		t.Fatalf("collect from candidates: %v", err)
	}

	gotEntries := collectWorkingTreeStatusPaths(entries)
	wantEntries := []string{"docs/guide.md", "docs/new.md"}
	if !reflect.DeepEqual(gotEntries, wantEntries) {
		t.Fatalf("unexpected candidate status paths:\n got %#v\nwant %#v", gotEntries, wantEntries)
	}
	if !reflect.DeepEqual(remaining, wantEntries) {
		t.Fatalf("unexpected remaining dirty paths:\n got %#v\nwant %#v", remaining, wantEntries)
	}
}
