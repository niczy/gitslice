package searchindex

import (
	"reflect"
	"testing"
)

func TestBuildSliceArtifactProducesSortedFilesAndPostings(t *testing.T) {
	artifact := BuildSliceArtifact("slice-1", "commit-1", []ArtifactInputFile{
		{
			Path:              "zeta.txt",
			SearchContentHash: "hash-zeta",
			NGrams:            []string{"abc", "zzz"},
		},
		{
			Path:              "alpha.txt",
			SearchContentHash: "hash-alpha",
			NGrams:            []string{"abc", "def", "abc"},
		},
		{
			Path:              "skip.txt",
			SearchContentHash: "",
			NGrams:            []string{"nope"},
		},
	})

	if got := len(artifact.Files); got != 2 {
		t.Fatalf("expected 2 files, got %d", got)
	}
	if artifact.Files[0].Path != "alpha.txt" || artifact.Files[1].Path != "zeta.txt" {
		t.Fatalf("expected sorted files, got %#v", artifact.Files)
	}
	if got, want := artifact.Postings, []SliceArtifactPosting{
		{NGram: "abc", FileIndexes: []uint32{0, 1}},
		{NGram: "def", FileIndexes: []uint32{0}},
		{NGram: "zzz", FileIndexes: []uint32{1}},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected postings: got %#v want %#v", got, want)
	}
}

func TestEncodeDecodeSliceArtifactRoundTrip(t *testing.T) {
	artifact := &SliceArtifact{
		Version:    CurrentArtifactVersion,
		SliceID:    "slice-1",
		CommitHash: "commit-1",
		Files: []SliceArtifactFile{
			{Path: "docs/readme.md", SearchContentHash: "hash-1"},
			{Path: "src/main.go", SearchContentHash: "hash-2"},
		},
		Postings: []SliceArtifactPosting{
			{NGram: "abc", FileIndexes: []uint32{0, 1}},
			{NGram: "def", FileIndexes: []uint32{1}},
		},
	}

	raw, err := EncodeSliceArtifact(artifact)
	if err != nil {
		t.Fatalf("EncodeSliceArtifact failed: %v", err)
	}
	decoded, err := DecodeSliceArtifact(raw)
	if err != nil {
		t.Fatalf("DecodeSliceArtifact failed: %v", err)
	}
	if !reflect.DeepEqual(decoded, artifact) {
		t.Fatalf("artifact round trip mismatch: got %#v want %#v", decoded, artifact)
	}
}
