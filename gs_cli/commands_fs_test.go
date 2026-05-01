package gscli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAbsoluteFilesystemPathArg(t *testing.T) {
	t.Run("accepts absolute path", func(t *testing.T) {
		value, err := parseAbsoluteFilesystemPathArg("/demo/src/main.go", true)
		if err != nil {
			t.Fatalf("parseAbsoluteFilesystemPathArg failed: %v", err)
		}
		if value != "/demo/src/main.go" {
			t.Fatalf("unexpected parse result: %q", value)
		}
	})

	t.Run("cleans absolute path", func(t *testing.T) {
		value, err := parseAbsoluteFilesystemPathArg("/demo/src/../README.md", true)
		if err != nil {
			t.Fatalf("parseAbsoluteFilesystemPathArg failed: %v", err)
		}
		if value != "/demo/README.md" {
			t.Fatalf("unexpected cleaned path: %q", value)
		}
	})

	t.Run("rejects relative path", func(t *testing.T) {
		if _, err := parseAbsoluteFilesystemPathArg("demo/README.md", true); err == nil {
			t.Fatal("expected relative path rejection")
		}
	})

	t.Run("rejects missing required path", func(t *testing.T) {
		if _, err := parseAbsoluteFilesystemPathArg("", true); err == nil {
			t.Fatal("expected missing path rejection")
		}
	})
}

func TestParseAbsoluteFilesystemPatternArg(t *testing.T) {
	value, err := parseAbsoluteFilesystemPatternArg("/demo/**/*.md", true)
	if err != nil {
		t.Fatalf("parseAbsoluteFilesystemPatternArg failed: %v", err)
	}
	if value != "/demo/**/*.md" {
		t.Fatalf("unexpected pattern parse result: %q", value)
	}

	if _, err := parseAbsoluteFilesystemPatternArg("demo/**/*.md", true); err == nil {
		t.Fatal("expected relative pattern rejection")
	}
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
