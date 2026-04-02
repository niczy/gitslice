package searchindex

import (
	"bytes"
	"fmt"
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
	query, err := BuildRegexQuery(`alpha.*(beta|gamma).*delta`, DefaultWeighter(), SparseModeCovering)
	if err != nil {
		b.Fatalf("BuildRegexQuery failed: %v", err)
	}
	matchingNGrams := []string{"alp", "pha", "beta", "delt", "ta"}
	for i := 0; i < 2000; i++ {
		ngrams := append([]string(nil), matchingNGrams...)
		if i%7 == 0 {
			ngrams = append(ngrams, "extra", "grams")
		}
		files = append(files, ArtifactInputFile{
			Path:              fmt.Sprintf("src/file-%04d.go", i),
			SearchContentHash: fmt.Sprintf("hash-%04d", i),
			NGrams:            ngrams,
		})
	}
	artifact := BuildSliceArtifact("slice-bench", "commit-bench", files)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got := CandidateFileIndexes(artifact, query); len(got) == 0 {
			b.Fatal("expected candidate matches")
		}
	}
}
