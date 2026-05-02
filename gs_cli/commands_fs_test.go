package gscli

import (
	"flag"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/niczy/gitslice/internal/storage"
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

func TestBuildFilesystemUploadInventorySkipsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "node_modules", ".pnpm", "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir temp tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("write regular file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "node_modules", ".pnpm", "pkg", "index.js"), []byte("module.exports = {}\n"), 0o600); err != nil {
		t.Fatalf("write package file: %v", err)
	}

	if err := os.Symlink(filepath.Join(tmpDir, "node_modules", ".pnpm", "pkg"), filepath.Join(tmpDir, "node_modules", "pkg")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(tmpDir, "linked-outside.txt")); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}

	inventory, err := buildFilesystemUploadInventory(tmpDir, "/nicholas/openclaw", filesystemUploadInventoryOptions{includeIgnored: true})
	if err != nil {
		t.Fatalf("buildFilesystemUploadInventory failed: %v", err)
	}

	got := make([]string, 0, len(inventory.files))
	for _, file := range inventory.files {
		got = append(got, file.remotePath)
	}
	want := []string{
		path.Join("/nicholas/openclaw", "README.md"),
		path.Join("/nicholas/openclaw", "node_modules/.pnpm/pkg/index.js"),
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected uploaded files: got %v want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected uploaded files: got %v want %v", got, want)
		}
	}
}

func TestBuildFilesystemUploadInventoryAppliesDefaultExcludesAndGsignore(t *testing.T) {
	tmpDir := t.TempDir()
	for _, dir := range []string{
		filepath.Join(tmpDir, "src"),
		filepath.Join(tmpDir, "node_modules", "pkg"),
		filepath.Join(tmpDir, "dist"),
		filepath.Join(tmpDir, "scratch"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	files := map[string]string{
		"src/main.go":               "package main\n",
		"src/debug.log":             "debug\n",
		"node_modules/pkg/index.js": "module.exports = {}\n",
		"dist/app.js":               "bundle\n",
		"scratch/temp.txt":          "temp\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".gsignore"), []byte("*.log\nscratch/\n"), 0o600); err != nil {
		t.Fatalf("write .gsignore: %v", err)
	}

	inventory, err := buildFilesystemUploadInventory(tmpDir, "/nicholas/app", filesystemUploadInventoryOptions{})
	if err != nil {
		t.Fatalf("buildFilesystemUploadInventory failed: %v", err)
	}

	got := make([]string, 0, len(inventory.files))
	for _, file := range inventory.files {
		got = append(got, file.remotePath)
	}
	want := []string{
		path.Join("/nicholas/app", ".gsignore"),
		path.Join("/nicholas/app", "src/main.go"),
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected uploaded files: got %v want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected uploaded files: got %v want %v", got, want)
		}
	}
}

func TestCollectFilesystemUploadBlockSourcesUsesManifestOffsets(t *testing.T) {
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "large.txt")
	content := make([]byte, storage.DefaultFileBlockSize+17)
	for index := range content {
		content[index] = byte('a' + index%26)
	}
	if err := os.WriteFile(localPath, content, 0o600); err != nil {
		t.Fatalf("write large file: %v", err)
	}

	manifest, blocks, err := buildFilesystemUploadManifest("/target/large.txt", localPath)
	if err != nil {
		t.Fatalf("buildFilesystemUploadManifest failed: %v", err)
	}
	if len(manifest.GetBlocks()) != 2 || len(blocks) != 2 {
		t.Fatalf("expected two blocks, got manifest=%d sources=%d", len(manifest.GetBlocks()), len(blocks))
	}

	inventory := &filesystemUploadInventory{files: []*filesystemUploadFile{{
		localPath: localPath,
		manifest:  manifest,
		blocks:    blocks,
	}}}
	missing := map[string]struct{}{blocks[1].hash: {}}
	sources := collectFilesystemUploadBlockSources(inventory, missing)
	if len(sources) != 1 || sources[0].offset != int64(storage.DefaultFileBlockSize) {
		t.Fatalf("unexpected sources: %+v", sources)
	}
	chunk, err := readFilesystemUploadBlockSource(sources[0])
	if err != nil {
		t.Fatalf("readFilesystemUploadBlockSource failed: %v", err)
	}
	if string(chunk) != string(content[storage.DefaultFileBlockSize:]) {
		t.Fatalf("unexpected block content")
	}
}

func TestSplitFilesystemUploadFiles(t *testing.T) {
	files := make([]*filesystemUploadFile, 205)
	for index := range files {
		files[index] = &filesystemUploadFile{remotePath: path.Join("/target", "file.txt")}
	}

	batches := splitFilesystemUploadFiles(files, 100)
	if len(batches) != 3 {
		t.Fatalf("unexpected batch count: got %d want 3", len(batches))
	}
	if len(batches[0]) != 100 || len(batches[1]) != 100 || len(batches[2]) != 5 {
		t.Fatalf("unexpected batch sizes: got %d/%d/%d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
	if &batches[0][0] != &files[0] || &batches[2][4] != &files[204] {
		t.Fatal("expected batches to preserve original file ordering")
	}
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
