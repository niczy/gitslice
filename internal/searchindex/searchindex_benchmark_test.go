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
