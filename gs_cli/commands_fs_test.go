package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestParseWorkspacePathArg(t *testing.T) {
	t.Run("workspace and path", func(t *testing.T) {
		workspaceID, path, err := parseWorkspacePathArg("demo:src/main.go", true)
		if err != nil {
			t.Fatalf("parseWorkspacePathArg failed: %v", err)
		}
		if workspaceID != "demo" || path != "src/main.go" {
			t.Fatalf("unexpected parse result: workspace=%q path=%q", workspaceID, path)
		}
	})

	t.Run("workspace only allowed", func(t *testing.T) {
		workspaceID, path, err := parseWorkspacePathArg("demo", false)
		if err != nil {
			t.Fatalf("parseWorkspacePathArg failed: %v", err)
		}
		if workspaceID != "demo" || path != "" {
			t.Fatalf("unexpected parse result: workspace=%q path=%q", workspaceID, path)
		}
	})

	t.Run("missing path rejected", func(t *testing.T) {
		if _, _, err := parseWorkspacePathArg("demo", true); err == nil {
			t.Fatal("expected error when path is required")
		}
	})

	t.Run("missing workspace rejected", func(t *testing.T) {
		if _, _, err := parseWorkspacePathArg(":README.md", true); err == nil {
			t.Fatal("expected error when workspace is missing")
		}
	})
}

func TestReadFilesystemWriteInput(t *testing.T) {
	t.Run("reads local file", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "input.txt")
		if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
			t.Fatalf("write temp file: %v", err)
		}

		data, err := readFilesystemWriteInput(path)
		if err != nil {
			t.Fatalf("readFilesystemWriteInput failed: %v", err)
		}
		if string(data) != "hello\n" {
			t.Fatalf("unexpected file content: %q", string(data))
		}
	})
}

func TestReorderInterspersedArgs(t *testing.T) {
	fs := flag.NewFlagSet("fs snapshot", flag.ContinueOnError)
	message := fs.String("m", "", "")
	patch := fs.Bool("patch", true, "")

	parseFlagSetInterspersed(fs, []string{"demo", "-m", "initial", "--patch=false"})

	if fs.NArg() != 1 || fs.Arg(0) != "demo" {
		t.Fatalf("unexpected positional args after reordering: %#v", fs.Args())
	}
	if *message != "initial" {
		t.Fatalf("unexpected message flag: %q", *message)
	}
	if *patch {
		t.Fatal("expected patch flag to parse as false")
	}
}
