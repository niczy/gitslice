package main

import (
	"os"
	"reflect"
	"testing"

	filev1 "github.com/niczy/gitslice/proto/file"
)

func TestParseChangeTypesCSV(t *testing.T) {
	types, err := parseChangeTypesCSV("add, modify,delete,rename,add")
	if err != nil {
		t.Fatalf("parseChangeTypesCSV failed: %v", err)
	}

	want := []filev1.ChangeType{
		filev1.ChangeType_CHANGE_TYPE_ADD,
		filev1.ChangeType_CHANGE_TYPE_MODIFY,
		filev1.ChangeType_CHANGE_TYPE_DELETE,
		filev1.ChangeType_CHANGE_TYPE_RENAME,
	}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("unexpected parsed types: got=%v want=%v", types, want)
	}
}

func TestParseChangeTypesCSVInvalid(t *testing.T) {
	if _, err := parseChangeTypesCSV("add,unknown"); err == nil {
		t.Fatalf("expected error for unsupported change type")
	}
}

func TestApplyListEntriesVersion(t *testing.T) {
	t.Run("slice and commit", func(t *testing.T) {
		req := &filev1.ListEntriesRequest{}
		applyListEntriesVersion(req, "slice-a", "c1")
		version, ok := req.GetVersion().(*filev1.ListEntriesRequest_SliceVersion)
		if !ok {
			t.Fatalf("expected slice version, got %T", req.GetVersion())
		}
		if version.SliceVersion.GetSliceId() != "slice-a" || version.SliceVersion.GetSliceHash() != "c1" {
			t.Fatalf("unexpected slice version payload: %+v", version.SliceVersion)
		}
	})

	t.Run("commit only", func(t *testing.T) {
		req := &filev1.ListEntriesRequest{}
		applyListEntriesVersion(req, "", "c2")
		version, ok := req.GetVersion().(*filev1.ListEntriesRequest_CommitHash)
		if !ok {
			t.Fatalf("expected commit hash version, got %T", req.GetVersion())
		}
		if version.CommitHash != "c2" {
			t.Fatalf("unexpected commit hash: %q", version.CommitHash)
		}
	})
}

func TestApplyGetFileVersion(t *testing.T) {
	t.Run("slice and commit", func(t *testing.T) {
		req := &filev1.GetFileRequest{}
		applyGetFileVersion(req, "slice-a", "c1")
		version, ok := req.GetVersion().(*filev1.GetFileRequest_SliceVersion)
		if !ok {
			t.Fatalf("expected slice version, got %T", req.GetVersion())
		}
		if version.SliceVersion.GetSliceId() != "slice-a" || version.SliceVersion.GetSliceHash() != "c1" {
			t.Fatalf("unexpected slice version payload: %+v", version.SliceVersion)
		}
	})

	t.Run("commit only", func(t *testing.T) {
		req := &filev1.GetFileRequest{}
		applyGetFileVersion(req, "", "c2")
		version, ok := req.GetVersion().(*filev1.GetFileRequest_CommitHash)
		if !ok {
			t.Fatalf("expected commit hash version, got %T", req.GetVersion())
		}
		if version.CommitHash != "c2" {
			t.Fatalf("unexpected commit hash: %q", version.CommitHash)
		}
	})
}

func TestResolveFileSliceID(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		got, err := resolveFileSliceID("root_slice")
		if err != nil {
			t.Fatalf("resolve explicit slice failed: %v", err)
		}
		if got != "root_slice" {
			t.Fatalf("unexpected explicit slice id: %q", got)
		}
	})

	t.Run("missing config returns empty", func(t *testing.T) {
		tmp := t.TempDir()
		withWorkingDir(t, tmp)

		got, err := resolveFileSliceID("")
		if err != nil {
			t.Fatalf("resolve without config failed: %v", err)
		}
		if got != "" {
			t.Fatalf("expected empty slice id when config is missing, got %q", got)
		}
	})

	t.Run("reads config", func(t *testing.T) {
		tmp := t.TempDir()
		withWorkingDir(t, tmp)

		if err := os.MkdirAll(".gs", 0o755); err != nil {
			t.Fatalf("mkdir .gs failed: %v", err)
		}
		if err := writeSliceIDConfig("root_slice"); err != nil {
			t.Fatalf("write config failed: %v", err)
		}
		got, err := resolveFileSliceID("")
		if err != nil {
			t.Fatalf("resolve from config failed: %v", err)
		}
		if got != "root_slice" {
			t.Fatalf("unexpected config slice id: %q", got)
		}
	})
}
