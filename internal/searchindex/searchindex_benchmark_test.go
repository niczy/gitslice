package searchindex

import (
	"bytes"
	"testing"
)

func BenchmarkBuildFileBlob(b *testing.B) {
	content := bytes.Repeat([]byte("package main\nfunc main() { println(\"hello\") }\n"), 512)
	weighter := DefaultWeighter()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := BuildFileBlob(content, weighter, SparseModeCovering); err != nil {
			b.Fatalf("BuildFileBlob failed: %v", err)
		}
	}
}

func BenchmarkBuildRegexQuery(b *testing.B) {
	weighter := DefaultWeighter()
	pattern := `foo(bar|baz|qux).*service.*(client|server).*handler`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := BuildRegexQuery(pattern, weighter, SparseModeCovering); err != nil {
			b.Fatalf("BuildRegexQuery failed: %v", err)
		}
	}
}

func BenchmarkCandidateFileIndexesLargeArtifact(b *testing.B) {
	files := make([]ArtifactInputFile, 0, 2000)
	for i := 0; i < 2000; i++ {
		content := bytes.Repeat([]byte("package search\nfunc match() { println(\"alpha beta gamma delta\") }\n"), 8)
		blob, err := BuildFileBlob(content, DefaultWeighter(), SparseModeCovering)
		if err != nil {
			b.Fatalf("BuildFileBlob failed: %v", err)
		}
		files = append(files, ArtifactInputFile{
			Path:              "src/file-" + string(rune('a'+(i%26))) + ".go",
			SearchContentHash: SearchContentHash(content),
			NGrams:            append([]string(nil), blob.NGrams...),
		})
	}
	artifact := BuildSliceArtifact("slice-bench", "commit-bench", files)
	query, err := BuildRegexQuery(`alpha.*(beta|gamma).*delta`, DefaultWeighter(), SparseModeCovering)
	if err != nil {
		b.Fatalf("BuildRegexQuery failed: %v", err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got := CandidateFileIndexes(artifact, query); len(got) == 0 {
			b.Fatal("expected candidate matches")
		}
	}
}
