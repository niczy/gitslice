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
