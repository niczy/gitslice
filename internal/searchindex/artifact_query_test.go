package searchindex

import (
	"reflect"
	"testing"
)

func TestCandidateFileIndexesEvaluatesLogicalQuery(t *testing.T) {
	artifact := BuildSliceArtifact("slice-1", "commit-1", []ArtifactInputFile{
		{Path: "docs/readme.md", SearchContentHash: "hash-1", NGrams: []string{"alp", "bet"}},
		{Path: "src/main.go", SearchContentHash: "hash-2", NGrams: []string{"bet", "gam"}},
		{Path: "notes/todo.txt", SearchContentHash: "hash-3", NGrams: []string{"del"}},
	})

	query := &QueryNode{
		Kind: QueryNodeOr,
		Children: []*QueryNode{
			{Kind: QueryNodeTerm, Literal: "alpha", NGrams: []string{"alp"}},
			{
				Kind: QueryNodeAnd,
				Children: []*QueryNode{
					{Kind: QueryNodeTerm, Literal: "beta", NGrams: []string{"bet"}},
					{Kind: QueryNodeTerm, Literal: "gamma", NGrams: []string{"gam"}},
				},
			},
		},
	}

	got := CandidateFileIndexes(artifact, query)
	want := []uint32{0, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CandidateFileIndexes() = %#v, want %#v", got, want)
	}
}

func TestCandidateFileIndexesReturnsAllForBroadQuery(t *testing.T) {
	artifact := BuildSliceArtifact("slice-1", "commit-1", []ArtifactInputFile{
		{Path: "a.txt", SearchContentHash: "hash-1", NGrams: []string{"ab"}},
		{Path: "b.txt", SearchContentHash: "hash-2", NGrams: []string{"bc"}},
	})

	got := CandidateFileIndexes(artifact, &QueryNode{Kind: QueryNodeTrue})
	want := []uint32{0, 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CandidateFileIndexes(TRUE) = %#v, want %#v", got, want)
	}
}
