package main

import "testing"

func TestResolvePruneEffectiveCommit(t *testing.T) {
	info := map[string]pruneSliceInfo{
		"root_slice":     {headCommit: "root-head"},
		"home.alice":     {parentID: "root_slice", headCommit: "fs-home"},
		"slice.child":    {parentID: "home.alice", headCommit: "init-slice"},
		"slice.grand":    {parentID: "slice.child", headCommit: ""},
		"slice.detached": {headCommit: "custom-head"},
	}

	cache := map[string]string{}
	if got, want := resolvePruneEffectiveCommit("home.alice", info, cache, map[string]bool{}), "fs-home"; got != want {
		t.Fatalf("home effective commit mismatch: got %q want %q", got, want)
	}
	if got, want := resolvePruneEffectiveCommit("slice.child", info, cache, map[string]bool{}), "fs-home"; got != want {
		t.Fatalf("child effective commit mismatch: got %q want %q", got, want)
	}
	if got, want := resolvePruneEffectiveCommit("slice.grand", info, cache, map[string]bool{}), "fs-home"; got != want {
		t.Fatalf("grandchild effective commit mismatch: got %q want %q", got, want)
	}
	if got, want := resolvePruneEffectiveCommit("slice.detached", info, cache, map[string]bool{}), "custom-head"; got != want {
		t.Fatalf("detached effective commit mismatch: got %q want %q", got, want)
	}
}

func TestCountAffectedPruneSlices(t *testing.T) {
	entries := []pruneCandidate{
		{sliceID: "root_slice"},
		{sliceID: "home.alice"},
		{sliceID: "home.alice"},
		{sliceID: "slice.child"},
	}
	if got, want := countAffectedPruneSlices(entries), 3; got != want {
		t.Fatalf("affected slice count mismatch: got %d want %d", got, want)
	}
}
