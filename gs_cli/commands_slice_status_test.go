package main

import "testing"

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
