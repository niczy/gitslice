package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func TestMaterializeSliceCheckoutHonorsMetadata(t *testing.T) {
	workdir := t.TempDir()

	scriptV1 := []byte("#!/bin/sh\necho one\n")
	linkV1 := "bin/run.sh"
	respV1 := &slicev1.CheckoutResponse{
		Manifest: &slicev1.SliceManifest{
			CommitHash: "commit-1",
			FileMetadata: []*slicev1.FileMetadata{
				{
					FileId:     "bin/run.sh",
					Path:       "bin/run.sh",
					Size:       int64(len(scriptV1)),
					Hash:       storage.HashFileManifestContent(scriptV1, true, ""),
					Executable: true,
				},
				{
					FileId:        "bin/current",
					Path:          "bin/current",
					Size:          int64(len(linkV1)),
					Hash:          storage.HashFileManifestContent([]byte(linkV1), false, linkV1),
					SymlinkTarget: linkV1,
				},
			},
		},
		Files: []*slicev1.FileContent{
			{FileId: "bin/run.sh", Content: scriptV1},
			{FileId: "bin/current", Content: []byte(linkV1)},
		},
	}

	if _, err := materializeSliceCheckout(workdir, respV1, nil, false); err != nil {
		t.Fatalf("materialize v1 failed: %v", err)
	}

	scriptPath := filepath.Join(workdir, "bin", "run.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("expected executable mode 0755, got %o", info.Mode().Perm())
	}
	linkPath := filepath.Join(workdir, "bin", "current")
	if got, err := os.Readlink(linkPath); err != nil {
		t.Fatalf("readlink v1: %v", err)
	} else if got != linkV1 {
		t.Fatalf("expected link target %q, got %q", linkV1, got)
	}

	scriptV2 := []byte("#!/bin/sh\necho two\n")
	linkV2 := "bin/other.sh"
	respV2 := &slicev1.CheckoutResponse{
		Manifest: &slicev1.SliceManifest{
			CommitHash: "commit-2",
			FileMetadata: []*slicev1.FileMetadata{
				{
					FileId: "bin/run.sh",
					Path:   "bin/run.sh",
					Size:   int64(len(scriptV2)),
					Hash:   storage.HashFileManifestContent(scriptV2, false, ""),
				},
				{
					FileId:        "bin/current",
					Path:          "bin/current",
					Size:          int64(len(linkV2)),
					Hash:          storage.HashFileManifestContent([]byte(linkV2), false, linkV2),
					SymlinkTarget: linkV2,
				},
			},
		},
		Files: []*slicev1.FileContent{
			{FileId: "bin/run.sh", Content: scriptV2},
			{FileId: "bin/current", Content: []byte(linkV2)},
		},
	}

	if _, err := materializeSliceCheckout(workdir, respV2, nil, false); err != nil {
		t.Fatalf("materialize v2 failed: %v", err)
	}

	info, err = os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat script v2: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("expected non-executable mode 0644, got %o", info.Mode().Perm())
	}
	if content, err := os.ReadFile(scriptPath); err != nil {
		t.Fatalf("read script v2: %v", err)
	} else if string(content) != string(scriptV2) {
		t.Fatalf("unexpected script content %q", string(content))
	}
	if got, err := os.Readlink(linkPath); err != nil {
		t.Fatalf("readlink v2: %v", err)
	} else if got != linkV2 {
		t.Fatalf("expected updated link target %q, got %q", linkV2, got)
	}
}
