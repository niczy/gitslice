package gitlayer

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestHandlerSupportsGitCloneFromSlice(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{
		ID:         "clone-slice",
		Name:       "Clone Slice",
		Slug:       "clone-slice",
		Visibility: models.VisibilityPrivate,
		Owners:     []string{"alice"},
		CreatedBy:  "alice",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	writeTestFile(t, ctx, st, slice.ID, "README.md", []byte("# hello from slice\n"), false, "")
	writeTestFile(t, ctx, st, slice.ID, "bin/run.sh", []byte("#!/bin/sh\necho hi\n"), true, "")
	writeTestFile(t, ctx, st, slice.ID, "docs/latest", []byte("README.md"), false, "README.md")

	handler := NewHandler(st, filepath.Join(t.TempDir(), "cache"))
	server := httptest.NewServer(handler)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	cmd := exec.CommandContext(
		ctx,
		"git",
		"-c", "http.extraHeader=Authorization: User alice",
		"clone",
		server.URL+"/git/clone-slice.git",
		cloneDir,
	)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone failed: %v\n%s", err, string(out))
	}

	content, err := os.ReadFile(filepath.Join(cloneDir, "README.md"))
	if err != nil {
		t.Fatalf("read cloned README: %v", err)
	}
	if got, want := string(content), "# hello from slice\n"; got != want {
		t.Fatalf("README content = %q, want %q", got, want)
	}

	info, err := os.Stat(filepath.Join(cloneDir, "bin/run.sh"))
	if err != nil {
		t.Fatalf("stat cloned executable: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("bin/run.sh is not executable: %v", info.Mode())
	}

	target, err := os.Readlink(filepath.Join(cloneDir, "docs/latest"))
	if err != nil {
		t.Fatalf("read cloned symlink: %v", err)
	}
	if target != "README.md" {
		t.Fatalf("symlink target = %q, want README.md", target)
	}
}

func TestHandlerRejectsUnauthenticatedPrivateClone(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:         "private-slice",
		Name:       "Private Slice",
		Slug:       "private-slice",
		Visibility: models.VisibilityPrivate,
		Owners:     []string{"alice"},
		CreatedBy:  "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	server := httptest.NewServer(NewHandler(st, filepath.Join(t.TempDir(), "cache")))
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/git/private-slice.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatalf("GET info/refs failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func writeTestFile(t *testing.T, ctx context.Context, st storage.Storage, sliceID, filePath string, content []byte, executable bool, symlinkTarget string) {
	t.Helper()
	filePath = strings.TrimSpace(filePath)
	manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, st, sliceID, filePath, content, executable, symlinkTarget)
	if err != nil {
		t.Fatalf("WriteSliceFileManifestWithMetadata(%s) failed: %v", filePath, err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:            common.GenerateEntryID(sliceID, filePath),
		Path:          filePath,
		Type:          "file",
		Size:          int64(len(content)),
		Hash:          manifest.Hash,
		Executable:    executable,
		SymlinkTarget: symlinkTarget,
	}); err != nil {
		t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
	}
	if err := st.AddFileToSlice(ctx, filePath, sliceID); err != nil {
		t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
	}
}
