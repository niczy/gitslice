package sliceservice

import (
	"reflect"
	"strings"
	"testing"
)

func TestThreeWayMergeLinesCombinesDisjointChanges(t *testing.T) {
	base := splitMergeTestLines("a\nb\nc\n")
	ours := splitMergeTestLines("a\nours\nc\n")
	theirs := splitMergeTestLines("a\nb\ntheirs\n")

	merged, ok := threeWayMergeLines(base, ours, theirs)
	if !ok {
		t.Fatal("threeWayMergeLines returned conflict for disjoint edits")
	}
	want := splitMergeTestLines("a\nours\ntheirs\n")
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged lines = %#v, want %#v", merged, want)
	}
}

func TestThreeWayMergeLinesRejectsOverlappingChanges(t *testing.T) {
	base := splitMergeTestLines("a\nb\nc\n")
	ours := splitMergeTestLines("a\nours\nc\n")
	theirs := splitMergeTestLines("a\ntheirs\nc\n")

	if merged, ok := threeWayMergeLines(base, ours, theirs); ok {
		t.Fatalf("threeWayMergeLines returned %#v, want conflict", merged)
	}
}

func TestThreeWayMergeLinesDeduplicatesIdenticalInsert(t *testing.T) {
	base := splitMergeTestLines("a\nb\n")
	ours := splitMergeTestLines("a\ninsert\nb\n")
	theirs := splitMergeTestLines("a\ninsert\nb\n")

	merged, ok := threeWayMergeLines(base, ours, theirs)
	if !ok {
		t.Fatal("threeWayMergeLines returned conflict for identical insert")
	}
	want := splitMergeTestLines("a\ninsert\nb\n")
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged lines = %#v, want %#v", merged, want)
	}
}

func splitMergeTestLines(content string) []string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}
