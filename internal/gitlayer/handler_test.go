package gitlayer

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
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
	token := createTestAuthSession(t, ctx, st, "alice")
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
		"-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token),
		"clone",
		server.URL+"/git/alice/clone-slice.git",
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

func TestHandlerImportsGitPushToSlice(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	slice := &models.Slice{
		ID:         "push-slice",
		Name:       "Push Slice",
		Slug:       "push-slice",
		Visibility: models.VisibilityPrivate,
		Owners:     []string{"alice"},
		CreatedBy:  "alice",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	token := createTestAuthSession(t, ctx, st, "alice")
	writeTestFile(t, ctx, st, slice.ID, "README.md", []byte("old\n"), false, "")
	writeTestFile(t, ctx, st, slice.ID, "stale.txt", []byte("delete me\n"), false, "")

	handler := NewHandler(st, filepath.Join(t.TempDir(), "cache"))
	server := httptest.NewServer(handler)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitTest(t, ctx, "", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "clone", server.URL+"/git/alice/push-slice.git", cloneDir)
	if err := os.WriteFile(filepath.Join(cloneDir, "README.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.Remove(filepath.Join(cloneDir, "stale.txt")); err != nil {
		t.Fatalf("remove stale: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cloneDir, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "bin/run.sh"), []byte("#!/bin/sh\necho pushed\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.Symlink("../README.md", filepath.Join(cloneDir, "bin/readme-link")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	runGitTest(t, ctx, cloneDir, "config", "user.name", "Alice")
	runGitTest(t, ctx, cloneDir, "config", "user.email", "alice@example.com")
	runGitTest(t, ctx, cloneDir, "add", "-A")
	runGitTest(t, ctx, cloneDir, "commit", "-m", "push slice updates")
	runGitTest(t, ctx, cloneDir, "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "push", "origin", "HEAD:main")

	readme, err := storage.ReadSliceFileContent(ctx, st, slice.ID, "README.md")
	if err != nil {
		t.Fatalf("read pushed README: %v", err)
	}
	if got, want := string(readme.Content), "new\n"; got != want {
		t.Fatalf("README content = %q, want %q", got, want)
	}
	if _, err := st.GetEntryByPath(ctx, slice.ID, "stale.txt"); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("stale entry error = %v, want ErrEntryNotFound", err)
	}
	scriptEntry, err := st.GetEntryByPath(ctx, slice.ID, "bin/run.sh")
	if err != nil {
		t.Fatalf("get script entry: %v", err)
	}
	if !scriptEntry.Executable {
		t.Fatal("pushed executable bit was not imported")
	}
	linkEntry, err := st.GetEntryByPath(ctx, slice.ID, "bin/readme-link")
	if err != nil {
		t.Fatalf("get symlink entry: %v", err)
	}
	if linkEntry.SymlinkTarget != "../README.md" {
		t.Fatalf("symlink target = %q, want ../README.md", linkEntry.SymlinkTarget)
	}
	meta, err := st.GetSliceMetadata(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata: %v", err)
	}
	if !strings.HasPrefix(meta.HeadCommitHash, "git-") {
		t.Fatalf("head commit = %q, want git-imported commit", meta.HeadCommitHash)
	}
}

func TestHandlerClonesMountedSliceWithBrowserTreeShape(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	parent, mounted := createMountedGitSlice(t, ctx, st)
	token := createTestAuthSession(t, ctx, st, "alice")
	writeTestFile(t, ctx, st, parent.ID, "nicholas/git-auth-smoke/README.md", []byte("mounted readme\n"), false, "")
	writeTestFile(t, ctx, st, parent.ID, "nicholas/other/keep.txt", []byte("keep\n"), false, "")

	handler := NewHandler(st, filepath.Join(t.TempDir(), "cache"))
	server := httptest.NewServer(handler)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitTest(t, ctx, "", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "clone", server.URL+"/git/alice/mounted-slice.git", cloneDir)

	content, err := os.ReadFile(filepath.Join(cloneDir, "nicholas/git-auth-smoke/README.md"))
	if err != nil {
		t.Fatalf("read mounted README from clone: %v", err)
	}
	if got, want := string(content), "mounted readme\n"; got != want {
		t.Fatalf("mounted README content = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "README.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root README stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "nicholas/other/keep.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside mount stat error = %v, want not exist", err)
	}
	if mounted.ParentSlice != parent.ID {
		t.Fatalf("mounted parent = %q, want %q", mounted.ParentSlice, parent.ID)
	}
}

func TestCollectGitProjectionFilesFallsBackToBackingSliceFiles(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	parent, mounted := createMountedGitSlice(t, ctx, st)
	if _, err := storage.WriteSliceFileManifest(ctx, st, parent.ID, "nicholas/git-auth-smoke/README.md", []byte("legacy git readme\n")); err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "nicholas/git-auth-smoke/README.md", parent.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	files, err := collectGitProjectionFiles(ctx, st, newGitProjection(mounted))
	if err != nil {
		t.Fatalf("collectGitProjectionFiles failed: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one projected file, got %d: %#v", len(files), files)
	}
	if got := files[0].Path; got != "nicholas/git-auth-smoke/README.md" {
		t.Fatalf("projected path = %q, want mounted display path", got)
	}
	if got := string(files[0].Content); got != "legacy git readme\n" {
		t.Fatalf("projected content = %q, want backing slice content", got)
	}
}

func TestHandlerImportsMountedSlicePushUnderMountAlias(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	parent, mounted := createMountedGitSlice(t, ctx, st)
	token := createTestAuthSession(t, ctx, st, "alice")
	writeTestFile(t, ctx, st, parent.ID, "nicholas/git-auth-smoke/README.md", []byte("old\n"), false, "")
	writeTestFile(t, ctx, st, parent.ID, "nicholas/git-auth-smoke/stale.txt", []byte("delete me\n"), false, "")
	writeTestFile(t, ctx, st, parent.ID, "nicholas/other/keep.txt", []byte("keep\n"), false, "")

	handler := NewHandler(st, filepath.Join(t.TempDir(), "cache"))
	server := httptest.NewServer(handler)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitTest(t, ctx, "", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "clone", server.URL+"/git/alice/mounted-slice.git", cloneDir)
	if err := os.WriteFile(filepath.Join(cloneDir, "nicholas/git-auth-smoke/README.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write mounted README: %v", err)
	}
	if err := os.Remove(filepath.Join(cloneDir, "nicholas/git-auth-smoke/stale.txt")); err != nil {
		t.Fatalf("remove mounted stale file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cloneDir, "nicholas/git-auth-smoke/src"), 0o755); err != nil {
		t.Fatalf("mkdir mounted src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "nicholas/git-auth-smoke/src/hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write mounted hello: %v", err)
	}

	runGitTest(t, ctx, cloneDir, "config", "user.name", "Alice")
	runGitTest(t, ctx, cloneDir, "config", "user.email", "alice@example.com")
	runGitTest(t, ctx, cloneDir, "add", "-A")
	runGitTest(t, ctx, cloneDir, "commit", "-m", "update mounted slice")
	runGitTest(t, ctx, cloneDir, "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "push", "origin", "HEAD:main")

	readme, err := storage.ReadSliceFileContent(ctx, st, parent.ID, "nicholas/git-auth-smoke/README.md")
	if err != nil {
		t.Fatalf("read parent mounted README: %v", err)
	}
	if got, want := string(readme.Content), "new\n"; got != want {
		t.Fatalf("parent mounted README = %q, want %q", got, want)
	}
	hello, err := storage.ReadSliceFileContent(ctx, st, parent.ID, "nicholas/git-auth-smoke/src/hello.txt")
	if err != nil {
		t.Fatalf("read parent mounted hello: %v", err)
	}
	if got, want := string(hello.Content), "hello\n"; got != want {
		t.Fatalf("parent mounted hello = %q, want %q", got, want)
	}
	if _, err := st.GetEntryByPath(ctx, parent.ID, "nicholas/git-auth-smoke/stale.txt"); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("stale parent entry error = %v, want ErrEntryNotFound", err)
	}
	if _, err := storage.ReadSliceFileContent(ctx, st, parent.ID, "nicholas/other/keep.txt"); err != nil {
		t.Fatalf("outside parent file was not preserved: %v", err)
	}
	meta, err := st.GetSliceMetadata(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetSliceMetadata(parent): %v", err)
	}
	snapshot, err := st.GetCommitSnapshot(ctx, meta.HeadCommitHash)
	if err != nil {
		t.Fatalf("GetCommitSnapshot(parent head): %v", err)
	}
	if _, ok := snapshot.Files["nicholas/other/keep.txt"]; !ok {
		t.Fatalf("outside parent file missing from commit snapshot: %#v", snapshot.Files)
	}
	if _, ok := snapshot.Files["nicholas/git-auth-smoke/stale.txt"]; ok {
		t.Fatalf("deleted mounted file still present in commit snapshot: %#v", snapshot.Files)
	}
	if _, ok := snapshot.Files["nicholas/git-auth-smoke/src/hello.txt"]; !ok {
		t.Fatalf("new mounted file missing from commit snapshot: %#v", snapshot.Files)
	}
	if _, err := st.GetEntryByPath(ctx, mounted.ID, "src/hello.txt"); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("raw mounted slice entry error = %v, want ErrEntryNotFound", err)
	}
}

func TestHandlerImportsRootMountedPushThroughHomeSliceAndPromotesRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	token := createTestAuthSession(t, ctx, st, "alice")
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	mounted := &models.Slice{
		ID:          "home-mounted-slice",
		Name:        "Home Mounted Slice",
		Slug:        "home-mounted-slice",
		Visibility:  models.VisibilityPrivate,
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "alice/git-auth-smoke", Alias: "alice/git-auth-smoke"},
		},
	}
	if err := st.CreateSlice(ctx, mounted); err != nil {
		t.Fatalf("CreateSlice(mounted) failed: %v", err)
	}
	writeTestFile(t, ctx, st, home.ID, "alice/git-auth-smoke/README.md", []byte("old home\n"), false, "")
	writeTestFile(t, ctx, st, root.ID, "alice/git-auth-smoke/README.md", []byte("old root\n"), false, "")

	handler := NewHandler(st, filepath.Join(t.TempDir(), "cache"))
	server := httptest.NewServer(handler)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitTest(t, ctx, "", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "clone", server.URL+"/git/alice/home-mounted-slice.git", cloneDir)
	if err := os.WriteFile(filepath.Join(cloneDir, "alice/git-auth-smoke/README.md"), []byte("new via git\n"), 0o644); err != nil {
		t.Fatalf("write mounted README: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cloneDir, "alice/git-auth-smoke/src"), 0o755); err != nil {
		t.Fatalf("mkdir mounted src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "alice/git-auth-smoke/src/new.txt"), []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write mounted new file: %v", err)
	}

	runGitTest(t, ctx, cloneDir, "config", "user.name", "Alice")
	runGitTest(t, ctx, cloneDir, "config", "user.email", "alice@example.com")
	runGitTest(t, ctx, cloneDir, "add", "-A")
	runGitTest(t, ctx, cloneDir, "commit", "-m", "update home mounted slice")
	runGitTest(t, ctx, cloneDir, "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "push", "origin", "HEAD:main")

	homeReadme, err := storage.ReadSliceFileContent(ctx, st, home.ID, "alice/git-auth-smoke/README.md")
	if err != nil {
		t.Fatalf("read home README: %v", err)
	}
	if got, want := string(homeReadme.Content), "new via git\n"; got != want {
		t.Fatalf("home README = %q, want %q", got, want)
	}
	if _, err := storage.ReadSliceFileContent(ctx, st, home.ID, "alice/git-auth-smoke/src/new.txt"); err != nil {
		t.Fatalf("read home new file: %v", err)
	}

	if err := handler.waitForQueuedProjections(ctx); err != nil {
		t.Fatalf("wait for projection: %v", err)
	}
	rootReadme, err := storage.ReadSliceFileContent(ctx, st, root.ID, "alice/git-auth-smoke/README.md")
	if err != nil {
		t.Fatalf("read root README: %v", err)
	}
	if got, want := string(rootReadme.Content), "new via git\n"; got != want {
		t.Fatalf("root README = %q, want %q", got, want)
	}
	if _, err := storage.ReadSliceFileContent(ctx, st, root.ID, "alice/git-auth-smoke/src/new.txt"); err != nil {
		t.Fatalf("read projected root new file: %v", err)
	}
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if slices.Contains(rootSlice.Files, "alice/git-auth-smoke/src/new.txt") {
		t.Fatalf("expected root file index to stay projection-only, got %#v", rootSlice.Files)
	}
}

func TestHandlerRejectsMountedSlicePushOutsideAliases(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	parent, _ := createMountedGitSlice(t, ctx, st)
	token := createTestAuthSession(t, ctx, st, "alice")
	writeTestFile(t, ctx, st, parent.ID, "nicholas/git-auth-smoke/README.md", []byte("old\n"), false, "")

	handler := NewHandler(st, filepath.Join(t.TempDir(), "cache"))
	server := httptest.NewServer(handler)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitTest(t, ctx, "", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "clone", server.URL+"/git/alice/mounted-slice.git", cloneDir)
	if err := os.WriteFile(filepath.Join(cloneDir, "outside.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cloneDir, "nicholas/other"), 0o755); err != nil {
		t.Fatalf("mkdir outside nested path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cloneDir, "nicholas/other/evil.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatalf("write outside nested file: %v", err)
	}

	runGitTest(t, ctx, cloneDir, "config", "user.name", "Alice")
	runGitTest(t, ctx, cloneDir, "config", "user.email", "alice@example.com")
	runGitTest(t, ctx, cloneDir, "add", "-A")
	runGitTest(t, ctx, cloneDir, "commit", "-m", "write outside mount")
	cmd := exec.CommandContext(ctx, "git", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "push", "origin", "HEAD:main")
	cmd.Dir = cloneDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("git push outside mount succeeded, want failure\n%s", string(out))
	}
	if _, err := storage.ReadSliceFileContent(ctx, st, parent.ID, "outside.txt"); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("outside root content error = %v, want ErrEntryNotFound", err)
	}
	if _, err := storage.ReadSliceFileContent(ctx, st, parent.ID, "nicholas/other/evil.txt"); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("outside nested content error = %v, want ErrEntryNotFound", err)
	}
}

func TestHandlerRejectsMountedSliceRootPushWithHelpfulMessage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is not available")
	}

	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	parent, _ := createMountedGitSlice(t, ctx, st)
	token := createTestAuthSession(t, ctx, st, "alice")
	writeTestFile(t, ctx, st, parent.ID, "nicholas/git-auth-smoke/README.md", []byte("old\n"), false, "")

	handler := NewHandler(st, filepath.Join(t.TempDir(), "cache"))
	server := httptest.NewServer(handler)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitTest(t, ctx, "", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "clone", server.URL+"/git/alice/mounted-slice.git", cloneDir)
	if err := os.WriteFile(filepath.Join(cloneDir, "ROOT.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatalf("write root file: %v", err)
	}

	runGitTest(t, ctx, cloneDir, "config", "user.name", "Alice")
	runGitTest(t, ctx, cloneDir, "config", "user.email", "alice@example.com")
	runGitTest(t, ctx, cloneDir, "add", "-A")
	runGitTest(t, ctx, cloneDir, "commit", "-m", "write slice root")
	cmd := exec.CommandContext(ctx, "git", "-c", "http.extraHeader=Authorization: "+basicAuthHeader("alice", token), "push", "origin", "HEAD:main")
	cmd.Dir = cloneDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("git push root file succeeded, want failure\n%s", string(out))
	}
	output := string(out)
	for _, want := range []string{
		"Gitslice rejected push",
		"cannot add or modify \"ROOT.txt\" at this slice root",
		"mounted slices only allow changes under existing mounted folder(s): \"nicholas/git-auth-smoke\"",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("git push output missing %q:\n%s", want, output)
		}
	}
	if _, err := storage.ReadSliceFileContent(ctx, st, parent.ID, "ROOT.txt"); !errors.Is(err, storage.ErrEntryNotFound) {
		t.Fatalf("root content error = %v, want ErrEntryNotFound", err)
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

	resp, err := server.Client().Get(server.URL + "/git/alice/private-slice.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatalf("GET info/refs failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `Basic realm="Gitslice Git"`) {
		t.Fatalf("WWW-Authenticate = %q, want Gitslice Git Basic challenge", got)
	}
}

func TestHandlerRejectsInvalidTokenWithBasicChallenge(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:         "invalid-token-slice",
		Name:       "Invalid Token Slice",
		Slug:       "invalid-token-slice",
		Visibility: models.VisibilityPrivate,
		Owners:     []string{"alice"},
		CreatedBy:  "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	server := httptest.NewServer(NewHandler(st, filepath.Join(t.TempDir(), "cache")))
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/git/alice/invalid-token-slice.git/info/refs?service=git-upload-pack", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", basicAuthHeader("alice", "bad-token"))
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET info/refs failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, `Basic realm="Gitslice Git"`) {
		t.Fatalf("WWW-Authenticate = %q, want Gitslice Git Basic challenge", got)
	}
}

func TestHandlerRejectsAuthenticatedNonOwnerWithForbidden(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:         "forbidden-slice",
		Name:       "Forbidden Slice",
		Slug:       "forbidden-slice",
		Visibility: models.VisibilityPrivate,
		Owners:     []string{"alice"},
		CreatedBy:  "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	token := createTestAuthSession(t, ctx, st, "bob")

	server := httptest.NewServer(NewHandler(st, filepath.Join(t.TempDir(), "cache")))
	defer server.Close()

	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/git/alice/forbidden-slice.git/info/refs?service=git-upload-pack", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Authorization", basicAuthHeader("bob", token))
	resp, err := server.Client().Do(req)
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

func createMountedGitSlice(t *testing.T, ctx context.Context, st storage.Storage) (*models.Slice, *models.Slice) {
	t.Helper()
	parent := &models.Slice{
		ID:         "root-slice",
		Name:       "Root Slice",
		Slug:       "root-slice",
		Visibility: models.VisibilityPrivate,
		Owners:     []string{"alice"},
		CreatedBy:  "alice",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		IsRoot:     true,
	}
	if err := st.CreateSlice(ctx, parent); err != nil {
		t.Fatalf("CreateSlice(parent) failed: %v", err)
	}
	mounted := &models.Slice{
		ID:          "mounted-slice",
		Name:        "Mounted Slice",
		Slug:        "mounted-slice",
		Visibility:  models.VisibilityPrivate,
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		ParentSlice: parent.ID,
		Files: []string{
			"nicholas/git-auth-smoke/README.md",
		},
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "nicholas/git-auth-smoke", Alias: "nicholas/git-auth-smoke"},
		},
	}
	if err := st.CreateSlice(ctx, mounted); err != nil {
		t.Fatalf("CreateSlice(mounted) failed: %v", err)
	}
	return parent, mounted
}

func runGitTest(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func createTestAuthSession(t *testing.T, ctx context.Context, st storage.Storage, username string) string {
	t.Helper()
	testName := strings.NewReplacer("/", "-", " ", "-", "_", "-").Replace(t.Name())
	now := time.Now()
	accountID := "acct-" + username + "-" + testName
	if err := st.CreateAccount(ctx, &models.Account{
		AccountID:  accountID,
		OwnerMode:  models.AccountOwnerModeHumanAttached,
		ClaimState: models.AccountClaimStateClaimed,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     username,
		AccountID:    accountID,
		PrimaryEmail: username + "@example.com",
		AuthSource:   "test",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	token := "token-" + username + "-" + testName
	if err := st.CreateAuthSession(ctx, &models.AuthSession{
		SessionID:  "sess-" + username + "-" + testName,
		Username:   username,
		Token:      token,
		DeviceInfo: "gitlayer-test",
		CreatedAt:  now,
		LastSeenAt: now,
	}); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}
	return token
}

func basicAuthHeader(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}
