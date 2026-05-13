package gscli

import (
	"fmt"
	"reflect"
	"testing"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func TestBuildChangesetSwitchPlanProtectsUnmanagedChanges(t *testing.T) {
	current := &changesetSwitchState{
		Files: map[string]changesetSwitchFileState{
			"a.txt": {Hash: "old-a"},
			"d.txt": {Hash: "old-d"},
		},
	}
	target := &changesetSnapshotSwitchTarget{
		Files: map[string]*slicev1.FileMetadata{
			"a.txt": {Path: "a.txt", Hash: "new-a"},
			"b.txt": {Path: "b.txt", Hash: "new-b"},
		},
		Deleted: map[string]struct{}{"c.txt": {}},
	}
	actual := map[string]changesetSwitchFileState{
		"a.txt": {Hash: "old-a"},
		"c.txt": {Hash: "base-c"},
		"d.txt": {Hash: "old-d"},
		"e.txt": {Hash: "user-edit"},
	}
	matches := func(path string, expected changesetSwitchFileState) bool {
		got, ok := actual[path]
		if expected.Deleted {
			return !ok
		}
		return ok && got == expected
	}

	plan := buildChangesetSwitchPlan(current, target, []workingTreeStatusEntry{
		{Path: "a.txt", Status: "M"},
		{Path: "d.txt", Status: "M"},
		{Path: "e.txt", Status: "A"},
	}, matches)

	if want := []string{"a.txt", "b.txt"}; !reflect.DeepEqual(plan.FetchPaths, want) {
		t.Fatalf("FetchPaths = %#v, want %#v", plan.FetchPaths, want)
	}
	if want := []string{"c.txt"}; !reflect.DeepEqual(plan.DeletePaths, want) {
		t.Fatalf("DeletePaths = %#v, want %#v", plan.DeletePaths, want)
	}
	if want := []string{"d.txt", "e.txt"}; !reflect.DeepEqual(plan.RestorePaths, want) {
		t.Fatalf("RestorePaths = %#v, want %#v", plan.RestorePaths, want)
	}
	if want := []string{"e.txt"}; !reflect.DeepEqual(plan.UnsafePaths, want) {
		t.Fatalf("UnsafePaths = %#v, want %#v", plan.UnsafePaths, want)
	}
}

func TestBuildChangesetSwitchPlanSkipsAlreadyMaterializedTarget(t *testing.T) {
	target := &changesetSnapshotSwitchTarget{
		Files: map[string]*slicev1.FileMetadata{
			"a.txt": {Path: "a.txt", Hash: "new-a", Executable: true},
		},
		Deleted: map[string]struct{}{"gone.txt": {}},
	}
	actual := map[string]changesetSwitchFileState{
		"a.txt": {Hash: "new-a", Executable: true},
	}
	matches := func(path string, expected changesetSwitchFileState) bool {
		got, ok := actual[path]
		if expected.Deleted {
			return !ok
		}
		return ok && got == expected
	}

	plan := buildChangesetSwitchPlan(nil, target, []workingTreeStatusEntry{
		{Path: "a.txt", Status: "M"},
	}, matches)
	if len(plan.FetchPaths) != 0 {
		t.Fatalf("expected no fetch paths, got %#v", plan.FetchPaths)
	}
	if len(plan.DeletePaths) != 0 {
		t.Fatalf("expected no delete paths, got %#v", plan.DeletePaths)
	}
	if len(plan.UnsafePaths) != 0 {
		t.Fatalf("expected no unsafe paths, got %#v", plan.UnsafePaths)
	}
}

func BenchmarkBuildChangesetSwitchPlan(b *testing.B) {
	const count = 10000
	current := &changesetSwitchState{Files: make(map[string]changesetSwitchFileState, count)}
	target := &changesetSnapshotSwitchTarget{
		Files:   make(map[string]*slicev1.FileMetadata, count),
		Deleted: make(map[string]struct{}, count/10),
	}
	actual := make(map[string]changesetSwitchFileState, count)
	dirty := make([]workingTreeStatusEntry, 0, count)
	for i := 0; i < count; i++ {
		path := fmt.Sprintf("src/file-%05d.txt", i)
		current.Files[path] = changesetSwitchFileState{Hash: fmt.Sprintf("old-%d", i)}
		if i%10 == 0 {
			target.Deleted[path] = struct{}{}
		} else {
			target.Files[path] = &slicev1.FileMetadata{Path: path, Hash: fmt.Sprintf("new-%d", i)}
		}
		actual[path] = current.Files[path]
		dirty = append(dirty, workingTreeStatusEntry{Path: path, Status: "M"})
	}
	matches := func(path string, expected changesetSwitchFileState) bool {
		got, ok := actual[path]
		if expected.Deleted {
			return !ok
		}
		return ok && got == expected
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		plan := buildChangesetSwitchPlan(current, target, dirty, matches)
		if len(plan.TargetPaths) != count {
			b.Fatalf("unexpected target path count %d", len(plan.TargetPaths))
		}
	}
}
