package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/niczy/gitslice/internal/searchindex"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func TestBuildLocalSliceSearchArtifactSkipsSymlinksAndBinaryFiles(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll docs failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, "bin"), 0o755); err != nil {
		t.Fatalf("MkdirAll bin failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "docs", "readme.md"), []byte("alpha beta gamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile readme failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "docs", "data.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatalf("WriteFile data.bin failed: %v", err)
	}
	if err := os.Symlink("docs/readme.md", filepath.Join(workdir, "bin", "current")); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	artifact, err := buildLocalSliceSearchArtifact(workdir, "slice-1", &slicev1.SliceManifest{
		CommitHash: "commit-1",
		FileMetadata: []*slicev1.FileMetadata{
			{Path: "docs/readme.md"},
			{Path: "docs/data.bin"},
			{Path: "bin/current", SymlinkTarget: "docs/readme.md"},
		},
	})
	if err != nil {
		t.Fatalf("buildLocalSliceSearchArtifact failed: %v", err)
	}
	if artifact.SliceID != "slice-1" || artifact.CommitHash != "commit-1" {
		t.Fatalf("unexpected artifact identity: %+v", artifact)
	}
	if got := len(artifact.Files); got != 1 {
		t.Fatalf("expected 1 indexed file, got %d", got)
	}
	if artifact.Files[0].Path != "docs/readme.md" {
		t.Fatalf("expected only readme to be indexed, got %+v", artifact.Files)
	}
	if artifact.Files[0].SearchContentHash != searchindex.SearchContentHash([]byte("alpha beta gamma\n")) {
		t.Fatalf("unexpected search content hash: %+v", artifact.Files[0])
	}
}

func TestEnsureLocalSliceSearchArtifactBuildsAndPersistsLocally(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll docs failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "docs", "readme.md"), []byte("alpha beta gamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile readme failed: %v", err)
	}

	t.Setenv("GS_DISABLE_SLICE_SEARCH_ARTIFACT_DOWNLOAD", "1")
	manifest := &slicev1.SliceManifest{
		CommitHash: "commit-1",
		FileMetadata: []*slicev1.FileMetadata{
			{Path: "docs/readme.md"},
		},
	}
	if err := ensureLocalSliceSearchArtifact(context.Background(), nil, workdir, "slice-1", manifest); err != nil {
		t.Fatalf("ensureLocalSliceSearchArtifact failed: %v", err)
	}

	meta, err := readLocalSliceSearchArtifactMetadata(workdir)
	if err != nil {
		t.Fatalf("readLocalSliceSearchArtifactMetadata failed: %v", err)
	}
	if meta.Source != "rebuilt_local" || meta.CommitHash != "commit-1" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}

	raw, err := os.ReadFile(localSearchArtifactBaseFilePath(workdir))
	if err != nil {
		t.Fatalf("ReadFile artifact failed: %v", err)
	}
	artifact, err := searchindex.DecodeSliceArtifact(raw)
	if err != nil {
		t.Fatalf("DecodeSliceArtifact failed: %v", err)
	}
	if len(artifact.Files) != 1 || artifact.Files[0].Path != "docs/readme.md" {
		t.Fatalf("unexpected artifact files: %+v", artifact.Files)
	}
	if info, err := os.Stat(localSearchArtifactOverlayDirPath(workdir)); err != nil {
		t.Fatalf("Stat overlay dir failed: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("expected overlay path to be a directory")
	}
}
