package main

import "testing"

func TestParseGitPorcelainStatus(t *testing.T) {
	output := " M tracked.txt\nA  staged.txt\nD  removed.txt\n?? new.txt\nR  old.txt -> renamed.txt\n"

	entries := parseGitPorcelainStatus(output)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	expected := map[string]string{
		"tracked.txt": "M",
		"staged.txt":  "A",
		"removed.txt": "D",
		"new.txt":     "A",
		"renamed.txt": "M",
	}
	for _, entry := range entries {
		want, ok := expected[entry.Path]
		if !ok {
			t.Fatalf("unexpected path %q in entries", entry.Path)
		}
		if entry.Status != want {
			t.Fatalf("path %q: got %q want %q", entry.Path, entry.Status, want)
		}
	}
}

func TestSummarizeWorkingTreeStatus(t *testing.T) {
	added, modified, deleted := summarizeWorkingTreeStatus([]workingTreeStatusEntry{
		{Path: "a.txt", Status: "A"},
		{Path: "b.txt", Status: "M"},
		{Path: "c.txt", Status: "D"},
		{Path: "d.txt", Status: "M"},
	})
	if added != 1 || modified != 2 || deleted != 1 {
		t.Fatalf("unexpected summary: +%d ~%d -%d", added, modified, deleted)
	}
}
