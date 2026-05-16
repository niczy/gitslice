package filesystemservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/searchindex"
	"github.com/niczy/gitslice/internal/storage"
	commonv1 "github.com/niczy/gitslice/proto/common"
	filev1 "github.com/niczy/gitslice/proto/file"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	fileservice "github.com/niczy/gitslice/services/file"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func authContext(username string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User "+username))
}

func bearerAuthContext(token string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
}

func adminAuthContextForUser(t testing.TB, st storage.Storage, username string) context.Context {
	t.Helper()
	email := strings.ToLower(strings.TrimSpace(username)) + "@example.com"
	t.Setenv("ADMIN_USER_EMAILS", email)
	if err := st.CreateUser(context.Background(), &models.User{
		Username:     username,
		PrimaryEmail: email,
		RootPath:     username,
	}); err != nil && err != storage.ErrEntryExists {
		t.Fatalf("CreateUser(%s) failed: %v", username, err)
	}
	return authContext(username)
}

func waitForSearchIndexForTest(t *testing.T, svc filesystemv1.FilesystemServiceServer) {
	t.Helper()
	impl, ok := svc.(*filesystemServiceServer)
	if !ok {
		t.Fatalf("unexpected filesystem service type %T", svc)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := impl.waitForQueuedSearchIndexing(ctx); err != nil {
		t.Fatalf("wait for search index: %v", err)
	}
}

func uploadManifestForTest(filePath string, content []byte) *filesystemv1.UploadFileManifest {
	blocks, _ := storage.ChunkFile(content, storage.DefaultFileBlockSize)
	manifest := &filesystemv1.UploadFileManifest{
		Path:   filePath,
		Size:   int64(len(content)),
		Hash:   hashContent(content),
		Blocks: make([]*filesystemv1.UploadBlockRef, 0, len(blocks)),
	}
	for _, block := range blocks {
		manifest.Blocks = append(manifest.Blocks, &filesystemv1.UploadBlockRef{
			Hash: block.Hash,
			Size: int64(block.Size),
		})
	}
	return manifest
}

func mustWorkspaceSearchArtifact(t *testing.T, st storage.Storage, workspaceID string) *searchindex.SliceArtifact {
	t.Helper()

	payload, err := st.GetWorkspaceSearchArtifact(context.Background(), workspaceID, searchindex.CurrentArtifactVersion)
	if err != nil {
		t.Fatalf("GetWorkspaceSearchArtifact(%s) failed: %v", workspaceID, err)
	}
	artifact, err := searchindex.DecodeSliceArtifact(payload)
	if err != nil {
		t.Fatalf("DecodeSliceArtifact(%s) failed: %v", workspaceID, err)
	}
	return artifact
}

func waitForWorkspaceSearchArtifactCommitForTest(t *testing.T, svc filesystemv1.FilesystemServiceServer, st storage.Storage, workspaceID, commitHash string) *searchindex.SliceArtifact {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	var lastArtifact *searchindex.SliceArtifact
	var lastErr error
	for time.Now().Before(deadline) {
		waitForSearchIndexForTest(t, svc)
		payload, err := st.GetWorkspaceSearchArtifact(context.Background(), workspaceID, searchindex.CurrentArtifactVersion)
		if err != nil {
			lastErr = err
		} else {
			artifact, decodeErr := searchindex.DecodeSliceArtifact(payload)
			if decodeErr != nil {
				t.Fatalf("DecodeSliceArtifact(%s) failed: %v", workspaceID, decodeErr)
			}
			lastArtifact = artifact
			if artifact.CommitHash == commitHash {
				return artifact
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	if lastArtifact != nil {
		t.Fatalf("expected artifact commit %q for %s, got %q", commitHash, workspaceID, lastArtifact.CommitHash)
	}
	t.Fatalf("expected artifact commit %q for %s, last load error: %v", commitHash, workspaceID, lastErr)
	return nil
}

type sliceSearchArtifactLoadCounter struct {
	storage.Storage
	mu    sync.Mutex
	calls int
}

func (c *sliceSearchArtifactLoadCounter) GetWorkspaceSearchArtifact(ctx context.Context, sliceID string, version uint32) ([]byte, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.Storage.GetWorkspaceSearchArtifact(ctx, sliceID, version)
}

func (c *sliceSearchArtifactLoadCounter) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type emptyBatchManifestHashStorage struct {
	storage.Storage
}

func (s *emptyBatchManifestHashStorage) GetFileManifestHashes(ctx context.Context, sliceID string, paths []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func searchArtifactPaths(artifact *searchindex.SliceArtifact) []string {
	if artifact == nil {
		return nil
	}
	paths := make([]string, 0, len(artifact.Files))
	for _, file := range artifact.Files {
		paths = append(paths, file.Path)
	}
	return paths
}

func runGitOrFailFS(t *testing.T, workdir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = workdir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func createFilesystemTestGitRemote(t *testing.T) (string, string) {
	t.Helper()

	baseDir := t.TempDir()
	remoteDir := filepath.Join(baseDir, "remote.git")
	sourceDir := filepath.Join(baseDir, "source")

	runGitOrFailFS(t, "", "init", "--bare", "--initial-branch=main", remoteDir)
	runGitOrFailFS(t, "", "clone", remoteDir, sourceDir)
	runGitOrFailFS(t, sourceDir, "config", "user.name", "repo-test")
	runGitOrFailFS(t, sourceDir, "config", "user.email", "repo-test@example.com")

	if err := os.WriteFile(filepath.Join(sourceDir, "README.md"), []byte("version 1\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitOrFailFS(t, sourceDir, "add", "README.md")
	runGitOrFailFS(t, sourceDir, "commit", "-m", "initial import")
	runGitOrFailFS(t, sourceDir, "push", "-u", "origin", "main")

	return remoteDir, sourceDir
}

func TestCreateWorkspaceAcceptsBearerSessionToken(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}
	if _, err := st.EnsureUser(ctx, "tester"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}
	if err := st.CreateAuthSession(ctx, &models.AuthSession{
		SessionID: "sess-tester",
		Username:  "tester",
		Token:     "gs_test_tester",
	}); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}

	svc := NewService(st)
	workspace, err := svc.CreateWorkspace(bearerAuthContext("gs_test_tester"), &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-bearer",
		Name:        "Bearer Workspace",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if workspace.GetCreatedBy() != "tester" {
		t.Fatalf("expected created_by tester, got %q", workspace.GetCreatedBy())
	}
	if got, want := workspace.GetVisibility(), commonv1.Visibility_VISIBILITY_PRIVATE; got != want {
		t.Fatalf("expected visibility %v, got %v", want, got)
	}
}

func TestListDirectoryAndStatIncludeEffectiveVisibility(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-visibility-list",
		Name:        "Visibility List Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: "ws-visibility-list",
		Path:        "docs/guides",
	}); err != nil {
		t.Fatalf("MakeDirectory failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-visibility-list",
		Path:        "docs/guides/public.txt",
		Content:     []byte("public file"),
	}); err != nil {
		t.Fatalf("WriteFile(public) failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-visibility-list",
		Path:        "docs/private.txt",
		Content:     []byte("private file"),
	}); err != nil {
		t.Fatalf("WriteFile(private) failed: %v", err)
	}
	if err := st.UpdateSliceVisibility(ctx, "ws-visibility-list", models.VisibilityPublic); err != nil {
		t.Fatalf("UpdateSliceVisibility failed: %v", err)
	}

	rootStat, err := svc.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: "ws-visibility-list",
	})
	if err != nil {
		t.Fatalf("Stat(root) failed: %v", err)
	}
	if got, want := rootStat.GetEntry().GetEffectiveVisibility(), commonv1.Visibility_VISIBILITY_PUBLIC; got != want {
		t.Fatalf("root effective_visibility = %v, want %v", got, want)
	}

	rootList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-visibility-list",
	})
	if err != nil {
		t.Fatalf("ListDirectory(root) failed: %v", err)
	}
	if got, want := len(rootList.GetEntries()), 1; got != want {
		t.Fatalf("expected 1 root entry, got %d", got)
	}
	if got, want := rootList.GetEntries()[0].GetPath(), "docs"; got != want {
		t.Fatalf("root entry path = %q, want %q", got, want)
	}
	if got, want := rootList.GetEntries()[0].GetEffectiveVisibility(), commonv1.Visibility_VISIBILITY_PUBLIC; got != want {
		t.Fatalf("root list effective_visibility = %v, want %v", got, want)
	}

	docsList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-visibility-list",
		Path:        "docs",
	})
	if err != nil {
		t.Fatalf("ListDirectory(docs) failed: %v", err)
	}
	if got, want := len(docsList.GetEntries()), 2; got != want {
		t.Fatalf("expected 2 docs entries, got %d", got)
	}
	entriesByPath := make(map[string]*filesystemv1.WorkspaceEntry, len(docsList.GetEntries()))
	for _, entry := range docsList.GetEntries() {
		entriesByPath[entry.GetPath()] = entry
	}
	if got, want := entriesByPath["docs/guides"].GetEffectiveVisibility(), commonv1.Visibility_VISIBILITY_PUBLIC; got != want {
		t.Fatalf("guides effective_visibility = %v, want %v", got, want)
	}
	if got, want := entriesByPath["docs/private.txt"].GetEffectiveVisibility(), commonv1.Visibility_VISIBILITY_PUBLIC; got != want {
		t.Fatalf("private file effective_visibility = %v, want %v", got, want)
	}

	guidesStat, err := svc.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: "ws-visibility-list",
		Path:        "docs/guides",
	})
	if err != nil {
		t.Fatalf("Stat(guides) failed: %v", err)
	}
	if got, want := guidesStat.GetEntry().GetEffectiveVisibility(), commonv1.Visibility_VISIBILITY_PUBLIC; got != want {
		t.Fatalf("guides stat effective_visibility = %v, want %v", got, want)
	}
}

func TestStatDirectoryReturnsRecursiveSize(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-stat-size",
		Name:        "Stat Size Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-stat-size",
		Path:        "docs/readme.md",
		Content:     []byte("hello"),
	}); err != nil {
		t.Fatalf("WriteFile(readme) failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-stat-size",
		Path:        "docs/guides/setup.md",
		Content:     []byte("world!!"),
	}); err != nil {
		t.Fatalf("WriteFile(setup) failed: %v", err)
	}

	docsStat, err := svc.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: "ws-stat-size",
		Path:        "docs",
	})
	if err != nil {
		t.Fatalf("Stat(docs) failed: %v", err)
	}
	if got, want := docsStat.GetEntry().GetType(), filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY; got != want {
		t.Fatalf("docs type = %v, want %v", got, want)
	}
	if got, want := docsStat.GetEntry().GetSize(), int64(12); got != want {
		t.Fatalf("docs size = %d, want %d", got, want)
	}

	rootStat, err := svc.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: "ws-stat-size",
	})
	if err != nil {
		t.Fatalf("Stat(root) failed: %v", err)
	}
	if got, want := rootStat.GetEntry().GetSize(), int64(12); got != want {
		t.Fatalf("root size = %d, want %d", got, want)
	}
}

func TestSearchReturnsPrivateAndPublicMatchesForAuthorizedUser(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-search-visibility",
		Name:        "Search Visibility Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: "ws-search-visibility",
		Path:        "docs",
	}); err != nil {
		t.Fatalf("MakeDirectory failed: %v", err)
	}
	for filePath, content := range map[string]string{
		"docs/public.txt":  "needle public",
		"docs/private.txt": "needle private",
	} {
		if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
			WorkspaceId: "ws-search-visibility",
			Path:        filePath,
			Content:     []byte(content),
		}); err != nil {
			t.Fatalf("WriteFile(%s) failed: %v", filePath, err)
		}
	}
	waitForSearchIndexForTest(t, svc)
	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: "ws-search-visibility",
		Query:       "needle",
		Glob:        "docs/*.txt",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if got, want := len(searchResp.GetMatches()), 2; got != want {
		t.Fatalf("expected 2 search matches, got %d", got)
	}
	if got, want := searchResp.GetMatches()[0].GetPath(), "docs/private.txt"; got != want {
		t.Fatalf("first search path = %q, want %q", got, want)
	}
	if got, want := searchResp.GetMatches()[1].GetPath(), "docs/public.txt"; got != want {
		t.Fatalf("second search path = %q, want %q", got, want)
	}
}

func TestSearchWithRequiredStateTokenReturnsNotReady(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	homeID := homeslice.IDForUsername("tester")
	writeResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/search.md",
		Content:     []byte("needle from token\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	token := &filev1.SliceStateToken{
		SliceId:   homeID,
		SliceHash: writeResp.GetCommitHash(),
	}
	if err := st.PutWorkspaceSearchArtifact(context.Background(), homeID, searchindex.CurrentArtifactVersion, []byte("corrupt")); err != nil {
		t.Fatalf("PutWorkspaceSearchArtifact(corrupt) failed: %v", err)
	}
	if impl, ok := svc.(*filesystemServiceServer); ok {
		impl.searchArtifactCache.DeleteSlice(homeID)
	}

	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId:        homeID,
		Query:              "needle",
		RequiredStateToken: token,
	})
	if err != nil {
		t.Fatalf("Search with required token returned error: %v", err)
	}
	if searchResp.GetStatus() != filesystemv1.SearchStatus_SEARCH_STATUS_INDEX_NOT_READY {
		t.Fatalf("search status = %v, want index not ready", searchResp.GetStatus())
	}
	if len(searchResp.GetMatches()) != 0 {
		t.Fatalf("not-ready search returned matches: %#v", searchResp.GetMatches())
	}

	waitForSearchIndexForTest(t, svc)
	searchResp, err = svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId:        homeID,
		Query:              "needle",
		RequiredStateToken: token,
	})
	if err != nil {
		t.Fatalf("Search after indexing failed: %v", err)
	}
	if searchResp.GetStatus() != filesystemv1.SearchStatus_SEARCH_STATUS_READY {
		t.Fatalf("search status after indexing = %v, want ready", searchResp.GetStatus())
	}
	if got := searchResp.GetIndexedStateToken().GetSliceHash(); got != writeResp.GetCommitHash() {
		t.Fatalf("indexed token slice hash = %q, want %q", got, writeResp.GetCommitHash())
	}
	if len(searchResp.GetMatches()) != 1 || searchResp.GetMatches()[0].GetPath() != "/tester/docs/search.md" {
		t.Fatalf("unexpected ready search matches: %#v", searchResp.GetMatches())
	}
}

func TestSearchUsesMountedLiveBackingSlice(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}
	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: home.ID,
		Path:        "/tester/test2",
	}); err != nil {
		t.Fatalf("MakeDirectory failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: home.ID,
		Path:        "/tester/test2/live-api-web-smoke.txt",
		Content:     []byte("live cross-slice API smoke\n"),
	}); err != nil {
		t.Fatalf("WriteFile(home live file) failed: %v", err)
	}

	mountedSlice := &models.Slice{
		ID:          "sl_mounted-live-search",
		Name:        "mounted-live-search",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "tester/test2", Alias: "tester/test2"},
		},
	}
	if err := st.CreateSlice(ctx, mountedSlice); err != nil {
		t.Fatalf("CreateSlice(mounted) failed: %v", err)
	}

	globResp, err := svc.Glob(ctx, &filesystemv1.GlobRequest{
		WorkspaceId: mountedSlice.ID,
		Pattern:     "tester/test2/*",
	})
	if err != nil {
		t.Fatalf("Glob(mounted) failed: %v", err)
	}
	if got := globResp.GetPaths(); len(got) != 1 || got[0] != "tester/test2/live-api-web-smoke.txt" {
		t.Fatalf("mounted glob paths = %#v, want tester/test2/live-api-web-smoke.txt", got)
	}

	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: mountedSlice.ID,
		Query:       "live",
	})
	if err != nil {
		t.Fatalf("Search(mounted) failed: %v", err)
	}
	if got, want := len(searchResp.GetMatches()), 1; got != want {
		t.Fatalf("mounted search match count = %d, want %d: %#v", got, want, searchResp.GetMatches())
	}
	match := searchResp.GetMatches()[0]
	if got, want := match.GetPath(), "tester/test2/live-api-web-smoke.txt"; got != want {
		t.Fatalf("mounted search path = %q, want %q", got, want)
	}
	if got, want := match.GetLine(), "live cross-slice API smoke"; got != want {
		t.Fatalf("mounted search line = %q, want %q", got, want)
	}
}

func TestSearchCapsLargeMatchSet(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-search-cap",
		Name:        "Search Cap Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	var body strings.Builder
	for i := 0; i < filesystemSearchMaxMatches+25; i++ {
		fmt.Fprintf(&body, "needle line %d\n", i)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-search-cap",
		Path:        "docs/many.txt",
		Content:     []byte(body.String()),
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	waitForSearchIndexForTest(t, svc)
	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: "ws-search-cap",
		Query:       "needle",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if got, want := len(searchResp.GetMatches()), filesystemSearchMaxMatches; got != want {
		t.Fatalf("expected capped search matches = %d, got %d", want, got)
	}
	if got, want := searchResp.GetMatches()[filesystemSearchMaxMatches-1].GetLineNumber(), int32(filesystemSearchMaxMatches); got != want {
		t.Fatalf("last capped match line = %d, want %d", got, want)
	}
}

func TestSearchCachesSliceSearchArtifactByCommit(t *testing.T) {
	ctx := authContext("tester")
	base := storage.NewInMemoryStorage()
	st := &sliceSearchArtifactLoadCounter{Storage: base}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "sl_search-cache",
		Name:        "Search Cache Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "sl_search-cache",
		Path:        "docs/one.txt",
		Content:     []byte("needle one\n"),
	}); err != nil {
		t.Fatalf("WriteFile(one) failed: %v", err)
	}

	waitForSearchIndexForTest(t, svc)
	for i := 0; i < 2; i++ {
		searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
			WorkspaceId: "sl_search-cache",
			Query:       "needle",
		})
		if err != nil {
			t.Fatalf("Search(%d) failed: %v", i, err)
		}
		if got, want := len(searchResp.GetMatches()), 1; got != want {
			t.Fatalf("Search(%d) matches = %d, want %d", i, got, want)
		}
	}
	if got, want := st.CallCount(), 1; got != want {
		t.Fatalf("GetWorkspaceSearchArtifact calls after repeated same-commit searches = %d, want %d", got, want)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "sl_search-cache",
		Path:        "docs/two.txt",
		Content:     []byte("needle two\n"),
	}); err != nil {
		t.Fatalf("WriteFile(two) failed: %v", err)
	}
	waitForSearchIndexForTest(t, svc)
	if _, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: "sl_search-cache",
		Query:       "needle",
	}); err != nil {
		t.Fatalf("Search(after mutation) failed: %v", err)
	}
	if got, want := st.CallCount(), 2; got != want {
		t.Fatalf("GetWorkspaceSearchArtifact calls after new slice commit = %d, want %d", got, want)
	}
}

func TestWorkspaceFileLifecycle(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)

	workspace, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-demo",
		Name:        "Demo Workspace",
		Description: "test workspace",
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if got, want := workspace.GetWorkspaceId(), "ws-demo"; got != want {
		t.Fatalf("workspace_id mismatch: got %q want %q", got, want)
	}

	if _, err := svc.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides",
	}); err != nil {
		t.Fatalf("MakeDirectory failed: %v", err)
	}

	writeResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
		Content:     []byte("hello world\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if writeResp.GetCommitHash() == "" {
		t.Fatalf("expected commit hash after write")
	}

	readResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if got, want := string(readResp.GetContent()), "hello world\n"; got != want {
		t.Fatalf("content mismatch: got %q want %q", got, want)
	}

	editResp, err := svc.EditFile(ctx, &filesystemv1.EditFileRequest{
		WorkspaceId:  "ws-demo",
		Path:         "docs/guides/README.md",
		ExpectedHash: readResp.GetHash(),
		Edits: []*filesystemv1.FileEdit{
			{OldText: "world", NewText: "agent"},
		},
	})
	if err != nil {
		t.Fatalf("EditFile failed: %v", err)
	}
	if editResp.GetCommitHash() == "" || editResp.GetHash() == readResp.GetHash() {
		t.Fatalf("expected new commit/hash after edit: %#v", editResp)
	}

	editedResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(after edit) failed: %v", err)
	}
	if got, want := string(editedResp.GetContent()), "hello agent\n"; got != want {
		t.Fatalf("edited content mismatch: got %q want %q", got, want)
	}

	rootList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-demo",
	})
	if err != nil {
		t.Fatalf("ListDirectory(root) failed: %v", err)
	}
	if len(rootList.GetEntries()) != 1 || rootList.GetEntries()[0].GetPath() != "docs" {
		t.Fatalf("unexpected root entries: %#v", rootList.GetEntries())
	}

	guidesList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides",
	})
	if err != nil {
		t.Fatalf("ListDirectory(docs/guides) failed: %v", err)
	}
	if len(guidesList.GetEntries()) != 1 || guidesList.GetEntries()[0].GetPath() != "docs/guides/README.md" {
		t.Fatalf("unexpected guides entries: %#v", guidesList.GetEntries())
	}

	statResp, err := svc.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !statResp.GetExists() || statResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_FILE {
		t.Fatalf("unexpected stat response: %#v", statResp)
	}

	existsResp, err := svc.Exists(ctx, &filesystemv1.ExistsRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !existsResp.GetExists() {
		t.Fatalf("expected file to exist")
	}

	if _, err := svc.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	}); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	existsAfterDelete, err := svc.Exists(ctx, &filesystemv1.ExistsRequest{
		WorkspaceId: "ws-demo",
		Path:        "docs/guides/README.md",
	})
	if err != nil {
		t.Fatalf("Exists after delete failed: %v", err)
	}
	if existsAfterDelete.GetExists() {
		t.Fatalf("expected file to be deleted")
	}

	workspaces, err := svc.ListWorkspaces(ctx, &filesystemv1.ListWorkspacesRequest{})
	if err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}
	if workspaces.GetTotal() != 1 {
		t.Fatalf("expected created workspace only, got total=%d", workspaces.GetTotal())
	}
	for _, workspace := range workspaces.GetWorkspaces() {
		if workspace.GetIsRoot() || workspace.GetWorkspaceId() == "root" {
			t.Fatalf("root workspace should not be visible to non-admin users: %#v", workspace)
		}
	}
}

func TestWorkspaceSearchArtifactTracksMutations(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-search",
		Name:        "Search Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-search",
		Path:        "docs/README.md",
		Content:     []byte("alpha beta gamma\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	waitForSearchIndexForTest(t, svc)
	artifact := mustWorkspaceSearchArtifact(t, st, "ws-search")
	if artifact.CommitHash != writeResp.GetCommitHash() {
		t.Fatalf("expected artifact commit %q, got %q", writeResp.GetCommitHash(), artifact.CommitHash)
	}
	if got := searchArtifactPaths(artifact); len(got) != 1 || got[0] != "docs/README.md" {
		t.Fatalf("unexpected artifact paths after write: %#v", got)
	}

	moveResp, err := svc.MoveFile(ctx, &filesystemv1.MoveFileRequest{
		WorkspaceId:     "ws-search",
		SourcePath:      "docs/README.md",
		DestinationPath: "docs/GUIDE.md",
	})
	if err != nil {
		t.Fatalf("MoveFile failed: %v", err)
	}
	waitForSearchIndexForTest(t, svc)
	artifact = mustWorkspaceSearchArtifact(t, st, "ws-search")
	if artifact.CommitHash != moveResp.GetCommitHash() {
		t.Fatalf("expected artifact commit %q after move, got %q", moveResp.GetCommitHash(), artifact.CommitHash)
	}
	if got := searchArtifactPaths(artifact); len(got) != 1 || got[0] != "docs/GUIDE.md" {
		t.Fatalf("unexpected artifact paths after move: %#v", got)
	}

	copyResp, err := svc.CopyFile(ctx, &filesystemv1.CopyFileRequest{
		WorkspaceId:     "ws-search",
		SourcePath:      "docs/GUIDE.md",
		DestinationPath: "docs/GUIDE-copy.md",
	})
	if err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}
	waitForSearchIndexForTest(t, svc)
	artifact = mustWorkspaceSearchArtifact(t, st, "ws-search")
	if artifact.CommitHash != copyResp.GetCommitHash() {
		t.Fatalf("expected artifact commit %q after copy, got %q", copyResp.GetCommitHash(), artifact.CommitHash)
	}
	if got := searchArtifactPaths(artifact); len(got) != 2 || got[0] != "docs/GUIDE-copy.md" || got[1] != "docs/GUIDE.md" {
		t.Fatalf("unexpected artifact paths after copy: %#v", got)
	}
	if artifact.Files[0].SearchContentHash != artifact.Files[1].SearchContentHash {
		t.Fatalf("expected copied file to reuse search content hash, got %#v", artifact.Files)
	}

	binaryResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-search",
		Path:        "bin/data.bin",
		Content:     []byte{0x00, 0x01, 0x02},
	})
	if err != nil {
		t.Fatalf("WriteFile(binary) failed: %v", err)
	}
	waitForSearchIndexForTest(t, svc)
	artifact = mustWorkspaceSearchArtifact(t, st, "ws-search")
	if artifact.CommitHash != binaryResp.GetCommitHash() {
		t.Fatalf("expected artifact commit %q after binary write, got %q", binaryResp.GetCommitHash(), artifact.CommitHash)
	}
	if got := searchArtifactPaths(artifact); len(got) != 2 {
		t.Fatalf("expected binary file to be excluded from artifact, got %#v", got)
	}

	deleteResp, err := svc.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: "ws-search",
		Path:        "docs/GUIDE.md",
	})
	if err != nil {
		t.Fatalf("DeleteFile(GUIDE) failed: %v", err)
	}
	waitForSearchIndexForTest(t, svc)
	artifact = mustWorkspaceSearchArtifact(t, st, "ws-search")
	if artifact.CommitHash != deleteResp.GetCommitHash() {
		t.Fatalf("expected artifact commit %q after delete, got %q", deleteResp.GetCommitHash(), artifact.CommitHash)
	}
	if got := searchArtifactPaths(artifact); len(got) != 1 || got[0] != "docs/GUIDE-copy.md" {
		t.Fatalf("unexpected artifact paths after first delete: %#v", got)
	}

	deleteResp, err = svc.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: "ws-search",
		Path:        "docs/GUIDE-copy.md",
	})
	if err != nil {
		t.Fatalf("DeleteFile(copy) failed: %v", err)
	}
	waitForSearchIndexForTest(t, svc)
	artifact = mustWorkspaceSearchArtifact(t, st, "ws-search")
	if artifact.CommitHash != deleteResp.GetCommitHash() {
		t.Fatalf("expected artifact commit %q after second delete, got %q", deleteResp.GetCommitHash(), artifact.CommitHash)
	}
	if got := searchArtifactPaths(artifact); len(got) != 0 {
		t.Fatalf("expected no indexed files after deleting last text file, got %#v", got)
	}
}

func TestWorkspaceSearchArtifactTracksRepoImportAndForceImport(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	remoteDir, sourceDir := createFilesystemTestGitRemote(t)
	importResp, err := svc.ImportRepo(ctx, &filesystemv1.ImportRepoRequest{
		RepoUrl: remoteDir,
		Path:    "/tester/vendor/demo",
	})
	if err != nil {
		t.Fatalf("ImportRepo failed: %v", err)
	}

	artifact := waitForWorkspaceSearchArtifactCommitForTest(t, svc, st, homeslice.IDForUsername("tester"), importResp.GetCommitHash())
	if got := searchArtifactPaths(artifact); len(got) != 1 || got[0] != "tester/vendor/demo/README.md" {
		t.Fatalf("unexpected artifact paths after import: %#v", got)
	}

	if err := os.WriteFile(filepath.Join(sourceDir, "README.md"), []byte("version 2 from remote\n"), 0o644); err != nil {
		t.Fatalf("rewrite remote README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "CHANGELOG.md"), []byte("remote changelog\n"), 0o644); err != nil {
		t.Fatalf("write remote changelog: %v", err)
	}
	runGitOrFailFS(t, sourceDir, "add", "-A")
	runGitOrFailFS(t, sourceDir, "commit", "-m", "remote update")
	runGitOrFailFS(t, sourceDir, "push", "origin", "main")

	forceResp, err := svc.ImportRepo(ctx, &filesystemv1.ImportRepoRequest{
		RepoUrl:        remoteDir,
		Path:           "/tester/vendor/demo",
		AllowOverwrite: true,
	})
	if err != nil {
		t.Fatalf("force ImportRepo failed: %v", err)
	}

	artifact = waitForWorkspaceSearchArtifactCommitForTest(t, svc, st, homeslice.IDForUsername("tester"), forceResp.GetCommitHash())
	if got := searchArtifactPaths(artifact); len(got) != 2 || got[0] != "tester/vendor/demo/CHANGELOG.md" || got[1] != "tester/vendor/demo/README.md" {
		t.Fatalf("unexpected artifact paths after force import: %#v", got)
	}
}

func TestFilesystemMutationsRecordCommitChanges(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	fsSvc := NewService(st)
	fileSvc := fileservice.NewService(st)

	if _, err := fsSvc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-history",
		Name:        "History Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeResp, err := fsSvc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-history",
		Path:        "docs/README.md",
		Content:     []byte("hello v1\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	showAddResp, err := fileSvc.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
		CommitHash:     writeResp.GetCommitHash(),
		IncludePatches: true,
	})
	if err != nil {
		t.Fatalf("GetCommitChanges(add) failed: %v", err)
	}
	if len(showAddResp.GetChanges()) != 1 {
		t.Fatalf("expected 1 add change, got %d", len(showAddResp.GetChanges()))
	}
	addChange := showAddResp.GetChanges()[0]
	if addChange.GetChangeType() != filev1.ChangeType_CHANGE_TYPE_ADD {
		t.Fatalf("expected add change, got %v", addChange.GetChangeType())
	}
	if addChange.GetPath() != "docs/README.md" {
		t.Fatalf("unexpected add path: %q", addChange.GetPath())
	}
	if !strings.Contains(addChange.GetPatch(), "+hello v1") {
		t.Fatalf("expected add patch to include content, got: %q", addChange.GetPatch())
	}

	editResp, err := fsSvc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-history",
		Path:        "docs/README.md",
		Content:     []byte("hello v2\n"),
	})
	if err != nil {
		t.Fatalf("second WriteFile failed: %v", err)
	}

	showModifyResp, err := fileSvc.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
		CommitHash:     editResp.GetCommitHash(),
		IncludePatches: true,
	})
	if err != nil {
		t.Fatalf("GetCommitChanges(modify) failed: %v", err)
	}
	if len(showModifyResp.GetChanges()) != 1 {
		t.Fatalf("expected 1 modify change, got %d", len(showModifyResp.GetChanges()))
	}
	modifyChange := showModifyResp.GetChanges()[0]
	if modifyChange.GetChangeType() != filev1.ChangeType_CHANGE_TYPE_MODIFY {
		t.Fatalf("expected modify change, got %v", modifyChange.GetChangeType())
	}
	if !strings.Contains(modifyChange.GetPatch(), "-hello v1") || !strings.Contains(modifyChange.GetPatch(), "+hello v2") {
		t.Fatalf("expected modify patch to show v1->v2, got: %q", modifyChange.GetPatch())
	}
	if modifyChange.GetLinesAdded() != 1 || modifyChange.GetLinesDeleted() != 1 {
		t.Fatalf("expected line delta +1/-1, got +%d/-%d", modifyChange.GetLinesAdded(), modifyChange.GetLinesDeleted())
	}
}

func TestFilesystemMoveRecordsRenameChange(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	fsSvc := NewService(st)
	fileSvc := fileservice.NewService(st)

	if _, err := fsSvc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-rename",
		Name:        "Rename Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := fsSvc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-rename",
		Path:        "docs/original.txt",
		Content:     []byte("same content\n"),
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	moveResp, err := fsSvc.MoveFile(ctx, &filesystemv1.MoveFileRequest{
		WorkspaceId:     "ws-rename",
		SourcePath:      "docs/original.txt",
		DestinationPath: "docs/renamed.txt",
	})
	if err != nil {
		t.Fatalf("MoveFile failed: %v", err)
	}

	showResp, err := fileSvc.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
		CommitHash: moveResp.GetCommitHash(),
	})
	if err != nil {
		t.Fatalf("GetCommitChanges(rename) failed: %v", err)
	}
	if len(showResp.GetChanges()) != 1 {
		t.Fatalf("expected 1 rename change, got %d", len(showResp.GetChanges()))
	}
	rename := showResp.GetChanges()[0]
	if rename.GetChangeType() != filev1.ChangeType_CHANGE_TYPE_RENAME {
		t.Fatalf("expected rename change, got %v", rename.GetChangeType())
	}
	if rename.GetOldPath() != "docs/original.txt" || rename.GetPath() != "docs/renamed.txt" {
		t.Fatalf("unexpected rename paths: old=%q new=%q", rename.GetOldPath(), rename.GetPath())
	}
}

func TestEditFileExpectedHashConflict(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-edit-conflict",
		Name:        "Edit Conflict",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-edit-conflict",
		Path:        "README.md",
		Content:     []byte("hello world\n"),
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err := svc.EditFile(ctx, &filesystemv1.EditFileRequest{
		WorkspaceId:  "ws-edit-conflict",
		Path:         "README.md",
		ExpectedHash: "stale-hash",
		Edits: []*filesystemv1.FileEdit{
			{OldText: "world", NewText: "agent"},
		},
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("expected Aborted, got %v", err)
	}
}

func TestEditFilesAppliesSingleCommit(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-edit-files",
		Name:        "Edit Files",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	readmeWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-edit-files",
		Path:        "README.md",
		Content:     []byte("hello world\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(README) failed: %v", err)
	}
	noteWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-edit-files",
		Path:        "notes/todo.txt",
		Content:     []byte("ship later\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(notes) failed: %v", err)
	}

	editResp, err := svc.EditFiles(ctx, &filesystemv1.EditFilesRequest{
		WorkspaceId: "ws-edit-files",
		Files: []*filesystemv1.EditFileInput{
			{
				Path:         "README.md",
				ExpectedHash: readmeWrite.GetHash(),
				Edits: []*filesystemv1.FileEdit{
					{OldText: "world", NewText: "agent"},
				},
			},
			{
				Path:         "notes/todo.txt",
				ExpectedHash: noteWrite.GetHash(),
				Edits: []*filesystemv1.FileEdit{
					{OldText: "later", NewText: "now"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("EditFiles failed: %v", err)
	}
	if editResp.GetCommitHash() == "" {
		t.Fatalf("expected commit hash after EditFiles")
	}
	if len(editResp.GetFiles()) != 2 {
		t.Fatalf("expected 2 edit results, got %d", len(editResp.GetFiles()))
	}

	readmeResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-edit-files",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(README after edit) failed: %v", err)
	}
	if got, want := string(readmeResp.GetContent()), "hello agent\n"; got != want {
		t.Fatalf("README content mismatch: got %q want %q", got, want)
	}

	noteResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-edit-files",
		Path:        "notes/todo.txt",
	})
	if err != nil {
		t.Fatalf("ReadFile(notes after edit) failed: %v", err)
	}
	if got, want := string(noteResp.GetContent()), "ship now\n"; got != want {
		t.Fatalf("notes content mismatch: got %q want %q", got, want)
	}
}

func TestEditFilesRejectsConflictingExpectedHash(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-edit-files-conflict",
		Name:        "Edit Files Conflict",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-edit-files-conflict",
		Path:        "README.md",
		Content:     []byte("hello world\n"),
	}); err != nil {
		t.Fatalf("WriteFile(README) failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-edit-files-conflict",
		Path:        "notes/todo.txt",
		Content:     []byte("ship later\n"),
	}); err != nil {
		t.Fatalf("WriteFile(notes) failed: %v", err)
	}

	_, err := svc.EditFiles(ctx, &filesystemv1.EditFilesRequest{
		WorkspaceId: "ws-edit-files-conflict",
		Files: []*filesystemv1.EditFileInput{
			{
				Path:         "README.md",
				ExpectedHash: "stale-hash",
				Edits: []*filesystemv1.FileEdit{
					{OldText: "world", NewText: "agent"},
				},
			},
			{
				Path: "notes/todo.txt",
				Edits: []*filesystemv1.FileEdit{
					{OldText: "later", NewText: "now"},
				},
			},
		},
	})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("expected Aborted, got %v", err)
	}

	noteResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-edit-files-conflict",
		Path:        "notes/todo.txt",
	})
	if err != nil {
		t.Fatalf("ReadFile(notes after rejected edit) failed: %v", err)
	}
	if got, want := string(noteResp.GetContent()), "ship later\n"; got != want {
		t.Fatalf("expected notes file to remain unchanged, got %q want %q", got, want)
	}
}

func TestFilesystemBlockMetricsTrackReuse(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	before := snapshotFilesystemMetrics()
	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-metrics",
		Name:        "Metrics",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	content := []byte("shared block content\n")
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-metrics",
		Path:        "README.md",
		Content:     content,
	}); err != nil {
		t.Fatalf("WriteFile(README) failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-metrics",
		Path:        "docs/README.md",
		Content:     content,
	}); err != nil {
		t.Fatalf("WriteFile(docs/README) failed: %v", err)
	}

	after := snapshotFilesystemMetrics()
	if got, want := after.BlocksWrittenTotal-before.BlocksWrittenTotal, int64(1); got != want {
		t.Fatalf("blocks written delta mismatch: got %d want %d", got, want)
	}
	if got, want := after.BlocksReusedTotal-before.BlocksReusedTotal, int64(1); got != want {
		t.Fatalf("blocks reused delta mismatch: got %d want %d", got, want)
	}
	if got, want := after.ManifestWrites-before.ManifestWrites, int64(2); got != want {
		t.Fatalf("manifest writes delta mismatch: got %d want %d", got, want)
	}
	if got := after.ManifestBytesTotal - before.ManifestBytesTotal; got <= 0 {
		t.Fatalf("expected manifest bytes total to increase, got delta=%d", got)
	}
	if got, want := filesystemDedupRatio(
		after.BlocksWrittenTotal-before.BlocksWrittenTotal,
		after.BlocksReusedTotal-before.BlocksReusedTotal,
	), 0.5; got != want {
		t.Fatalf("dedup ratio mismatch: got %.2f want %.2f", got, want)
	}
	if after.DedupRatio < 0 || after.DedupRatio > 1 {
		t.Fatalf("global dedup ratio out of range: %.4f", after.DedupRatio)
	}
}

func TestPlanUploadSkipsExistingVersionedBlocks(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-upload-plan",
		Name:        "Upload Plan",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	content := []byte("reused upload content\n")
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-upload-plan",
		Path:        "docs/original.txt",
		Content:     content,
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	unchangedResp, err := svc.PlanUpload(ctx, &filesystemv1.PlanUploadRequest{
		WorkspaceId: "ws-upload-plan",
		Files: []*filesystemv1.UploadFileManifest{
			uploadManifestForTest("docs/original.txt", content),
		},
	})
	if err != nil {
		t.Fatalf("PlanUpload(unchanged) failed: %v", err)
	}
	if len(unchangedResp.GetMissingBlockHashes()) != 0 {
		t.Fatalf("expected unchanged upload to have no missing blocks, got %v", unchangedResp.GetMissingBlockHashes())
	}
	if len(unchangedResp.GetSkippedPaths()) != 1 || unchangedResp.GetSkippedPaths()[0] != "docs/original.txt" {
		t.Fatalf("expected unchanged path to be skipped, got %v", unchangedResp.GetSkippedPaths())
	}

	reusedResp, err := svc.PlanUpload(ctx, &filesystemv1.PlanUploadRequest{
		WorkspaceId: "ws-upload-plan",
		Files: []*filesystemv1.UploadFileManifest{
			uploadManifestForTest("docs/copied.txt", content),
		},
	})
	if err != nil {
		t.Fatalf("PlanUpload(reused) failed: %v", err)
	}
	if len(reusedResp.GetMissingBlockHashes()) != 0 {
		t.Fatalf("expected reused upload to have no missing blocks, got %v", reusedResp.GetMissingBlockHashes())
	}
	if len(reusedResp.GetSkippedPaths()) != 0 {
		t.Fatalf("expected reused upload to write a new path, got skipped=%v", reusedResp.GetSkippedPaths())
	}
}

func TestPlanUploadReusesVersionedContentWithDifferentBlockLayout(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-upload-layout",
		Name:        "Upload Layout",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	content := bytes.Repeat([]byte("different block layout\n"), 4096)
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-upload-layout",
		Path:        "docs/original.txt",
		Content:     content,
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	manifest := &filesystemv1.UploadFileManifest{
		Path: "docs/copied.txt",
		Size: int64(len(content)),
		Hash: hashContent(content),
		Blocks: []*filesystemv1.UploadBlockRef{{
			Hash: hashContent(content),
			Size: int64(len(content)),
		}},
	}
	resp, err := svc.PlanUpload(ctx, &filesystemv1.PlanUploadRequest{
		WorkspaceId: "ws-upload-layout",
		Files:       []*filesystemv1.UploadFileManifest{manifest},
	})
	if err != nil {
		t.Fatalf("PlanUpload failed: %v", err)
	}
	if len(resp.GetMissingBlockHashes()) != 0 {
		t.Fatalf("expected versioned content reuse without missing blocks, got %v", resp.GetMissingBlockHashes())
	}
}

func TestUploadBlocksAndFinalizeUpload(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	client, conn := newFilesystemTestClient(t, st)
	defer conn.Close()

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "User tester")
	if _, err := client.CreateWorkspace(authCtx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-upload-stream",
		Name:        "Upload Stream",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	content := bytes.Repeat([]byte("upload-block-data-"), 2048)
	manifest := uploadManifestForTest("docs/big.txt", content)

	planResp, err := client.PlanUpload(authCtx, &filesystemv1.PlanUploadRequest{
		WorkspaceId: "ws-upload-stream",
		Files:       []*filesystemv1.UploadFileManifest{manifest},
	})
	if err != nil {
		t.Fatalf("PlanUpload failed: %v", err)
	}
	if len(planResp.GetMissingBlockHashes()) == 0 {
		t.Fatalf("expected missing blocks for new upload")
	}

	missing := make(map[string]struct{}, len(planResp.GetMissingBlockHashes()))
	for _, hash := range planResp.GetMissingBlockHashes() {
		missing[hash] = struct{}{}
	}

	uploadStream, err := client.UploadBlocks(authCtx)
	if err != nil {
		t.Fatalf("UploadBlocks start failed: %v", err)
	}
	remaining := make(map[string]struct{}, len(missing))
	for hash := range missing {
		remaining[hash] = struct{}{}
	}
	for offset := 0; offset < len(content); offset += storage.DefaultFileBlockSize {
		end := offset + storage.DefaultFileBlockSize
		if end > len(content) {
			end = len(content)
		}
		chunk := append([]byte(nil), content[offset:end]...)
		sum := sha256.Sum256(chunk)
		hash := hex.EncodeToString(sum[:])
		if _, needed := remaining[hash]; !needed {
			continue
		}
		if err := uploadStream.Send(&filesystemv1.UploadBlocksRequest{
			Chunk: &filesystemv1.UploadBlocksRequest_Metadata{
				Metadata: &filesystemv1.UploadBlockMetadata{
					WorkspaceId: "ws-upload-stream",
					Hash:        hash,
					Size:        int64(len(chunk)),
				},
			},
		}); err != nil {
			t.Fatalf("send upload metadata: %v", err)
		}
		if err := uploadStream.Send(&filesystemv1.UploadBlocksRequest{
			Chunk: &filesystemv1.UploadBlocksRequest_Content{Content: chunk},
		}); err != nil {
			t.Fatalf("send upload content: %v", err)
		}
		delete(remaining, hash)
	}
	uploadResp, err := uploadStream.CloseAndRecv()
	if err != nil {
		t.Fatalf("UploadBlocks failed: %v", err)
	}
	if uploadResp.GetBlocksWritten() == 0 {
		t.Fatalf("expected UploadBlocks to persist new blocks, got %#v", uploadResp)
	}

	finalizeResp, err := client.FinalizeUpload(authCtx, &filesystemv1.FinalizeUploadRequest{
		WorkspaceId: "ws-upload-stream",
		Files:       []*filesystemv1.UploadFileManifest{manifest},
	})
	if err != nil {
		t.Fatalf("FinalizeUpload failed: %v", err)
	}
	if finalizeResp.GetCommitHash() == "" || finalizeResp.GetFilesWritten() != 1 {
		t.Fatalf("unexpected FinalizeUpload response: %#v", finalizeResp)
	}

	readResp, err := client.ReadFile(authCtx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-upload-stream",
		Path:        "docs/big.txt",
	})
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(readResp.GetContent(), content) {
		t.Fatalf("uploaded file content mismatch")
	}
}

func TestFinalizeUploadCommitsManifestMissingFromHeadSnapshot(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	impl := svc.(*filesystemServiceServer)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-upload-retry",
		Name:        "Upload Retry",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	content := []byte("retry content\n")
	protoManifest := uploadManifestForTest("docs/retry.txt", content)
	_, payloads := storage.ChunkFile(content, storage.DefaultFileBlockSize)
	if err := st.PutBlocks(ctx, payloads); err != nil {
		t.Fatalf("PutBlocks failed: %v", err)
	}
	modelManifest, err := filesystemManifestFromProto("docs/retry.txt", protoManifest)
	if err != nil {
		t.Fatalf("filesystemManifestFromProto failed: %v", err)
	}
	if err := impl.writeWorkspaceFileManifest(ctx, "ws-upload-retry", modelManifest, true); err != nil {
		t.Fatalf("writeWorkspaceFileManifest failed: %v", err)
	}

	finalizeResp, err := svc.FinalizeUpload(ctx, &filesystemv1.FinalizeUploadRequest{
		WorkspaceId: "ws-upload-retry",
		Files:       []*filesystemv1.UploadFileManifest{protoManifest},
	})
	if err != nil {
		t.Fatalf("FinalizeUpload retry failed: %v", err)
	}
	if finalizeResp.GetCommitHash() == "" || finalizeResp.GetFilesWritten() != 0 || finalizeResp.GetFilesSkipped() != 1 {
		t.Fatalf("unexpected FinalizeUpload retry response: %#v", finalizeResp)
	}

	snapshot, err := st.GetCommitSnapshot(ctx, finalizeResp.GetCommitHash())
	if err != nil {
		t.Fatalf("GetCommitSnapshot failed: %v", err)
	}
	if got, want := snapshot.Files["docs/retry.txt"], protoManifest.GetHash(); got != want {
		t.Fatalf("snapshot did not recover uploaded file: got %q want %q", got, want)
	}
}

func TestWriteFileCommitSnapshotFallsBackWhenBatchHashMissing(t *testing.T) {
	ctx := authContext("tester")
	base := storage.NewInMemoryStorage()
	st := &emptyBatchManifestHashStorage{Storage: base}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-snapshot-fallback",
		Name:        "Snapshot Fallback",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	content := []byte("ready\n")
	writeResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-snapshot-fallback",
		Path:        "docs/app.txt",
		Content:     content,
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	snapshot, err := base.GetCommitSnapshot(ctx, writeResp.GetCommitHash())
	if err != nil {
		t.Fatalf("GetCommitSnapshot failed: %v", err)
	}
	if got, want := snapshot.Files["docs/app.txt"], hashContent(content); got != want {
		t.Fatalf("snapshot file hash = %q, want %q", got, want)
	}
}

func TestRecordWorkspaceFileChangesSkipsLineDeltasForBulkCommits(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	impl := svc.(*filesystemServiceServer)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-bulk-changes",
		Name:        "Bulk Changes",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	currentFiles := make(map[string]string, filesystemInlineChangeDiffLimit+1)
	modifiedPaths := make([]string, 0, filesystemInlineChangeDiffLimit+1)
	for index := 0; index <= filesystemInlineChangeDiffLimit; index++ {
		filePath := fmt.Sprintf("bulk/file-%03d.txt", index)
		currentFiles[filePath] = fmt.Sprintf("hash-%03d", index)
		modifiedPaths = append(modifiedPaths, filePath)
	}

	if err := impl.recordWorkspaceFileChanges(ctx, &models.Slice{ID: "ws-bulk-changes"}, "commit-bulk", "", "upload bulk", time.Now(), modifiedPaths, map[string]string{}, currentFiles); err != nil {
		t.Fatalf("recordWorkspaceFileChanges failed: %v", err)
	}
	changes, err := st.GetCommitChanges(ctx, "commit-bulk")
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if len(changes) != filesystemInlineChangeDiffLimit+1 {
		t.Fatalf("expected %d changes, got %d", filesystemInlineChangeDiffLimit+1, len(changes))
	}
	for _, change := range changes {
		if change.LinesAdded != 0 || change.LinesDeleted != 0 {
			t.Fatalf("expected bulk change line deltas to be skipped, got %s +%d/-%d", change.Path, change.LinesAdded, change.LinesDeleted)
		}
	}
}

func TestReadFileRanges(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-read-range",
		Name:        "Read Range",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	var builder strings.Builder
	lines := make([]string, 0, 800)
	for i := 0; i < 800; i++ {
		line := fmt.Sprintf("line-%03d: 0123456789abcdef0123456789abcdef\n", i)
		lines = append(lines, line)
		builder.WriteString(line)
	}
	original := []byte(builder.String())

	writeResp, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-read-range",
		Path:        "README.md",
		Content:     original,
	})
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	byteOffset := int64(storage.DefaultFileBlockSize - 8)
	byteLimit := int64(32)
	byteResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-read-range",
		Path:        "README.md",
		ByteOffset:  byteOffset,
		ByteLimit:   byteLimit,
	})
	if err != nil {
		t.Fatalf("ReadFile(byte range) failed: %v", err)
	}
	if got, want := string(byteResp.GetContent()), string(original[int(byteOffset):int(byteOffset+byteLimit)]); got != want {
		t.Fatalf("byte range mismatch: got %q want %q", got, want)
	}
	if got, want := byteResp.GetSize(), int64(len(original)); got != want {
		t.Fatalf("byte range size mismatch: got %d want %d", got, want)
	}
	if got, want := byteResp.GetHash(), writeResp.GetHash(); got != want {
		t.Fatalf("byte range hash mismatch: got %q want %q", got, want)
	}

	lineResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-read-range",
		Path:        "README.md",
		LineOffset:  10,
		LineLimit:   3,
	})
	if err != nil {
		t.Fatalf("ReadFile(line range) failed: %v", err)
	}
	expectedLines := strings.Join(lines[10:13], "")
	if got, want := string(lineResp.GetContent()), expectedLines; got != want {
		t.Fatalf("line range mismatch: got %q want %q", got, want)
	}
	if got, want := lineResp.GetSize(), int64(len(original)); got != want {
		t.Fatalf("line range size mismatch: got %d want %d", got, want)
	}
	if got, want := lineResp.GetHash(), writeResp.GetHash(); got != want {
		t.Fatalf("line range hash mismatch: got %q want %q", got, want)
	}
}

func TestReadFileRejectsMixedRanges(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-read-range-invalid",
		Name:        "Read Range Invalid",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-read-range-invalid",
		Path:        "README.md",
		Content:     []byte("hello\nworld\n"),
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-read-range-invalid",
		Path:        "README.md",
		ByteLimit:   5,
		LineLimit:   1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestWorkspaceAccessControl(t *testing.T) {
	ownerCtx := authContext("owner")
	otherCtx := authContext("other")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ownerCtx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "private-ws",
		Name:        "Private",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.ReadFile(otherCtx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "private-ws",
		Path:        "secret.txt",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}

	if _, err := svc.WriteFile(otherCtx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "private-ws",
		Path:        "secret.txt",
		Content:     []byte("nope"),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on write, got %v", err)
	}
}

func TestDeleteWorkspaceRemovesWorkspace(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-delete",
		Name:        "ws-delete",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-delete",
		Path:        "README.md",
		Content:     []byte("bye\n"),
	}); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	deleteResp, err := svc.DeleteWorkspace(ctx, &filesystemv1.DeleteWorkspaceRequest{WorkspaceId: "ws-delete"})
	if err != nil {
		t.Fatalf("DeleteWorkspace failed: %v", err)
	}
	if deleteResp.GetWorkspaceId() != "ws-delete" {
		t.Fatalf("unexpected DeleteWorkspace response: %#v", deleteResp)
	}

	if _, err := svc.GetWorkspaceInfo(ctx, &filesystemv1.GetWorkspaceInfoRequest{WorkspaceId: "ws-delete"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected workspace to be gone, got %v", err)
	}
	if _, err := st.GetWorkspaceSearchArtifact(context.Background(), "ws-delete", searchindex.CurrentArtifactVersion); err != storage.ErrEntryNotFound {
		t.Fatalf("expected workspace search artifact to be removed, got %v", err)
	}
}

func TestDeleteWorkspaceRejectsRootAndActiveSession(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	rootCtx := adminAuthContextForUser(t, st, "system")
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	svc := NewService(st)
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if _, err := svc.DeleteWorkspace(rootCtx, &filesystemv1.DeleteWorkspaceRequest{WorkspaceId: root.ID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition deleting root, got %v", err)
	}

	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-delete-active",
		Name:        "ws-delete-active",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if err := st.CreateAgentSession(ctx, &models.AgentSession{
		SessionID:      "session-delete-active",
		SliceID:        "ws-delete-active",
		UserID:         "tester",
		State:          models.AgentSessionStateRunning,
		Provider:       "local",
		IdleTimeoutSec: 60,
		TTLSec:         60,
	}); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	if _, err := svc.DeleteWorkspace(ctx, &filesystemv1.DeleteWorkspaceRequest{WorkspaceId: "ws-delete-active"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition deleting active workspace, got %v", err)
	}
}

func TestWorkspaceStreamReadAndWrite(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	client, conn := newFilesystemTestClient(t, st)
	defer conn.Close()

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "User tester")
	if _, err := client.CreateWorkspace(authCtx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-stream",
		Name:        "ws-stream",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	original := bytes.Repeat([]byte("stream-data-"), 32768)
	writeStream, err := client.StreamWrite(authCtx)
	if err != nil {
		t.Fatalf("StreamWrite start failed: %v", err)
	}
	if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
		Chunk: &filesystemv1.StreamWriteRequest_Metadata{
			Metadata: &filesystemv1.StreamWriteMetadata{
				WorkspaceId: "ws-stream",
				Path:        "large.bin",
			},
		},
	}); err != nil {
		t.Fatalf("send metadata: %v", err)
	}
	for offset := 0; offset < len(original); offset += 65536 {
		end := offset + 65536
		if end > len(original) {
			end = len(original)
		}
		if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
			Chunk: &filesystemv1.StreamWriteRequest_Content{
				Content: append([]byte(nil), original[offset:end]...),
			},
		}); err != nil {
			t.Fatalf("send content chunk: %v", err)
		}
	}
	writeResp, err := writeStream.CloseAndRecv()
	if err != nil {
		t.Fatalf("StreamWrite failed: %v", err)
	}
	if writeResp.GetCommitHash() == "" || writeResp.GetHash() == "" || writeResp.GetSize() != int64(len(original)) {
		t.Fatalf("unexpected StreamWrite response: %#v", writeResp)
	}

	readStream, err := client.StreamRead(authCtx, &filesystemv1.StreamReadRequest{
		WorkspaceId: "ws-stream",
		Path:        "large.bin",
		ChunkSize:   131072,
	})
	if err != nil {
		t.Fatalf("StreamRead failed: %v", err)
	}

	var streamed bytes.Buffer
	chunks := 0
	for {
		resp, err := readStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv stream chunk: %v", err)
		}
		chunks++
		if resp.GetPath() != "large.bin" || resp.GetWorkspaceId() != "ws-stream" {
			t.Fatalf("unexpected stream chunk metadata: %#v", resp)
		}
		if _, err := streamed.Write(resp.GetContent()); err != nil {
			t.Fatalf("buffer stream chunk: %v", err)
		}
	}
	if chunks < 2 {
		t.Fatalf("expected multiple stream chunks, got %d", chunks)
	}
	if !bytes.Equal(streamed.Bytes(), original) {
		t.Fatalf("streamed content mismatch")
	}
}

func TestWorkspaceStreamWriteRequiresMetadataFirst(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	client, conn := newFilesystemTestClient(t, st)
	defer conn.Close()

	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "User tester")
	if _, err := client.CreateWorkspace(authCtx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-stream-invalid",
		Name:        "ws-stream-invalid",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeStream, err := client.StreamWrite(authCtx)
	if err != nil {
		t.Fatalf("StreamWrite start failed: %v", err)
	}
	if err := writeStream.Send(&filesystemv1.StreamWriteRequest{
		Chunk: &filesystemv1.StreamWriteRequest_Content{Content: []byte("oops")},
	}); err != nil {
		t.Fatalf("send content: %v", err)
	}
	_, err = writeStream.CloseAndRecv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestWorkspaceFileContentsAreIsolatedPerWorkspace(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	for _, workspaceID := range []string{"ws-one", "ws-two"} {
		if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
			WorkspaceId: workspaceID,
			Name:        workspaceID,
		}); err != nil {
			t.Fatalf("CreateWorkspace(%s) failed: %v", workspaceID, err)
		}
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-one",
		Path:        "README.md",
		Content:     []byte("workspace one\n"),
	}); err != nil {
		t.Fatalf("WriteFile(ws-one) failed: %v", err)
	}
	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-two",
		Path:        "README.md",
		Content:     []byte("workspace two\n"),
	}); err != nil {
		t.Fatalf("WriteFile(ws-two) failed: %v", err)
	}

	readOne, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-one",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(ws-one) failed: %v", err)
	}
	readTwo, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-two",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(ws-two) failed: %v", err)
	}

	if got, want := string(readOne.GetContent()), "workspace one\n"; got != want {
		t.Fatalf("ws-one content mismatch: got %q want %q", got, want)
	}
	if got, want := string(readTwo.GetContent()), "workspace two\n"; got != want {
		t.Fatalf("ws-two content mismatch: got %q want %q", got, want)
	}
}

func newFilesystemTestClient(t *testing.T, st storage.Storage) (filesystemv1.FilesystemServiceClient, *grpc.ClientConn) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	RegisterGRPCServer(srv, st)
	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("grpc serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		srv.GracefulStop()
	})

	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc server: %v", err)
	}
	return filesystemv1.NewFilesystemServiceClient(conn), conn
}

func TestWorkspaceBatchAndPosixOperations(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-batch",
		Name:        "Batch Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeResp, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-batch",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("batch workspace\n")},
			{Path: "src/main.py", Content: []byte("print('hello')\n")},
			{Path: "src/lib/helper.py", Content: []byte("def helper():\n    return 'hello'\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles failed: %v", err)
	}
	if writeResp.GetCommitHash() == "" {
		t.Fatalf("expected batch commit hash")
	}
	if len(writeResp.GetFiles()) != 3 {
		t.Fatalf("expected 3 write results, got %d", len(writeResp.GetFiles()))
	}

	commits, err := st.ListSliceCommits(ctx, "ws-batch", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits failed: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected create + batch commit, got %d", len(commits))
	}

	readMany, err := svc.ReadFiles(ctx, &filesystemv1.ReadFilesRequest{
		WorkspaceId: "ws-batch",
		Paths:       []string{"src/main.py", "missing.py"},
	})
	if err != nil {
		t.Fatalf("ReadFiles failed: %v", err)
	}
	if len(readMany.GetFiles()) != 2 {
		t.Fatalf("expected 2 read results, got %d", len(readMany.GetFiles()))
	}
	if !readMany.GetFiles()[0].GetFound() || string(readMany.GetFiles()[0].GetContent()) != "print('hello')\n" {
		t.Fatalf("unexpected found read result: %#v", readMany.GetFiles()[0])
	}
	if readMany.GetFiles()[1].GetFound() || readMany.GetFiles()[1].GetError() != "file not found" {
		t.Fatalf("unexpected missing read result: %#v", readMany.GetFiles()[1])
	}

	globResp, err := svc.Glob(ctx, &filesystemv1.GlobRequest{
		WorkspaceId: "ws-batch",
		Pattern:     "src/**/*.py",
	})
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if got, want := len(globResp.GetPaths()), 2; got != want {
		t.Fatalf("glob count mismatch: got %d want %d (%#v)", got, want, globResp.GetPaths())
	}
	if globResp.GetPaths()[0] != "src/lib/helper.py" || globResp.GetPaths()[1] != "src/main.py" {
		t.Fatalf("unexpected glob results: %#v", globResp.GetPaths())
	}

	waitForSearchIndexForTest(t, svc)
	searchResp, err := svc.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: "ws-batch",
		Query:       "hello",
		Glob:        "src/**/*.py",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if got := len(searchResp.GetMatches()); got != 2 {
		t.Fatalf("expected 2 search matches, got %d", got)
	}
	if searchResp.GetMatches()[0].GetPath() != "src/lib/helper.py" || searchResp.GetMatches()[1].GetPath() != "src/main.py" {
		t.Fatalf("unexpected search results: %#v", searchResp.GetMatches())
	}

	copyResp, err := svc.CopyFile(ctx, &filesystemv1.CopyFileRequest{
		WorkspaceId:     "ws-batch",
		SourcePath:      "src/main.py",
		DestinationPath: "src/main_copy.py",
	})
	if err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}
	if copyResp.GetCommitHash() == "" {
		t.Fatalf("expected copy commit hash")
	}
	copyRead, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-batch",
		Path:        "src/main_copy.py",
	})
	if err != nil {
		t.Fatalf("ReadFile(copy) failed: %v", err)
	}
	if got, want := string(copyRead.GetContent()), "print('hello')\n"; got != want {
		t.Fatalf("copy content mismatch: got %q want %q", got, want)
	}

	moveResp, err := svc.MoveFile(ctx, &filesystemv1.MoveFileRequest{
		WorkspaceId:     "ws-batch",
		SourcePath:      "src/main_copy.py",
		DestinationPath: "archive/main_copy.py",
	})
	if err != nil {
		t.Fatalf("MoveFile failed: %v", err)
	}
	if moveResp.GetCommitHash() == "" {
		t.Fatalf("expected move commit hash")
	}

	movedRead, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-batch",
		Path:        "archive/main_copy.py",
	})
	if err != nil {
		t.Fatalf("ReadFile(moved) failed: %v", err)
	}
	if got, want := string(movedRead.GetContent()), "print('hello')\n"; got != want {
		t.Fatalf("moved content mismatch: got %q want %q", got, want)
	}
	if _, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-batch",
		Path:        "src/main_copy.py",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected moved source to be missing, got %v", err)
	}

	commits, err = st.ListSliceCommits(ctx, "ws-batch", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits(after copy/move) failed: %v", err)
	}
	if len(commits) != 4 {
		t.Fatalf("expected create + batch + copy + move commits, got %d", len(commits))
	}
}

func TestWorkspaceMixedBatchCommit(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-mixed-batch",
		Name:        "Mixed Batch Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	resp, err := svc.Batch(ctx, &filesystemv1.BatchRequest{
		WorkspaceId: "ws-mixed-batch",
		Message:     "batch test",
		Operations: []*filesystemv1.BatchOperation{
			{Id: "mkdir-docs", Operation: &filesystemv1.BatchOperation_Mkdir{Mkdir: &filesystemv1.BatchMkdirOperation{Path: "docs"}}},
			{Id: "write-readme", Operation: &filesystemv1.BatchOperation_Write{Write: &filesystemv1.BatchWriteOperation{Path: "docs/README.md", Content: []byte("hello\n")}}},
			{Id: "copy-readme", Operation: &filesystemv1.BatchOperation_Copy{Copy: &filesystemv1.BatchCopyOperation{SourcePath: "docs/README.md", DestinationPath: "docs/COPY.md"}}},
			{Id: "edit-copy", Operation: &filesystemv1.BatchOperation_Edit{Edit: &filesystemv1.BatchEditOperation{Path: "docs/COPY.md", Edits: []*filesystemv1.FileEdit{{OldText: "hello", NewText: "copied"}}}}},
			{Id: "move-copy", Operation: &filesystemv1.BatchOperation_Move{Move: &filesystemv1.BatchMoveOperation{SourcePath: "docs/COPY.md", DestinationPath: "docs/FINAL.md"}}},
			{Id: "delete-readme", Operation: &filesystemv1.BatchOperation_Delete{Delete: &filesystemv1.BatchDeleteOperation{Path: "docs/README.md"}}},
		},
	})
	if err != nil {
		t.Fatalf("Batch failed: %v", err)
	}
	if resp.GetCommitHash() == "" {
		t.Fatal("expected batch commit hash")
	}
	if len(resp.GetResults()) != 6 {
		t.Fatalf("expected 6 batch results, got %d", len(resp.GetResults()))
	}

	commits, err := st.ListSliceCommits(ctx, "ws-mixed-batch", 10, "")
	if err != nil {
		t.Fatalf("ListSliceCommits failed: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected create + batch commit, got %d", len(commits))
	}

	finalResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-mixed-batch",
		Path:        "docs/FINAL.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(final) failed: %v", err)
	}
	if got, want := string(finalResp.GetContent()), "copied\n"; got != want {
		t.Fatalf("final content mismatch: got %q want %q", got, want)
	}

	existsResp, err := svc.Exists(ctx, &filesystemv1.ExistsRequest{
		WorkspaceId: "ws-mixed-batch",
		Path:        "docs/README.md",
	})
	if err != nil {
		t.Fatalf("Exists(original) failed: %v", err)
	}
	if existsResp.GetExists() {
		t.Fatal("expected original README to be deleted")
	}
}

func TestWorkspaceSnapshotsAndRestore(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-snapshots",
		Name:        "Snapshot Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-snapshots",
		Path:        "README.md",
		Content:     []byte("v1\n"),
	}); err != nil {
		t.Fatalf("WriteFile(v1) failed: %v", err)
	}

	snapshotResp, err := svc.Snapshot(ctx, &filesystemv1.SnapshotRequest{
		WorkspaceId: "ws-snapshots",
		Message:     "checkpoint one",
	})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}
	if snapshotResp.GetSnapshot() == nil || snapshotResp.GetSnapshot().GetSnapshotId() == "" {
		t.Fatalf("expected snapshot metadata, got %#v", snapshotResp)
	}
	if snapshotResp.GetSnapshot().GetMessage() != "checkpoint one" {
		t.Fatalf("unexpected snapshot message: %#v", snapshotResp.GetSnapshot())
	}
	if snapshotResp.GetSnapshot().GetFileCount() != 1 {
		t.Fatalf("expected snapshot file count 1, got %#v", snapshotResp.GetSnapshot())
	}

	if _, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-snapshots",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v2\n")},
			{Path: "notes/todo.txt", Content: []byte("later\n")},
		},
	}); err != nil {
		t.Fatalf("WriteFiles(v2) failed: %v", err)
	}

	listResp, err := svc.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId: "ws-snapshots",
		Limit:       4,
	})
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}
	if len(listResp.GetSnapshots()) != 4 {
		t.Fatalf("expected 4 snapshots, got %#v", listResp.GetSnapshots())
	}
	if listResp.GetSnapshots()[0].GetFileCount() != 2 {
		t.Fatalf("expected newest snapshot to include 2 files, got %#v", listResp.GetSnapshots()[0])
	}
	if listResp.GetSnapshots()[1].GetSnapshotId() != snapshotResp.GetSnapshot().GetSnapshotId() {
		t.Fatalf("expected explicit snapshot second, got %#v", listResp.GetSnapshots())
	}

	olderResp, err := svc.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId:    "ws-snapshots",
		Limit:          2,
		FromSnapshotId: snapshotResp.GetSnapshot().GetSnapshotId(),
	})
	if err != nil {
		t.Fatalf("ListSnapshots(from) failed: %v", err)
	}
	if len(olderResp.GetSnapshots()) != 2 {
		t.Fatalf("expected 2 older snapshots, got %#v", olderResp.GetSnapshots())
	}
	if olderResp.GetSnapshots()[0].GetMessage() != "write README.md" {
		t.Fatalf("unexpected paginated snapshot list: %#v", olderResp.GetSnapshots())
	}

	restoreResp, err := svc.RestoreSnapshot(ctx, &filesystemv1.RestoreSnapshotRequest{
		WorkspaceId: "ws-snapshots",
		SnapshotId:  snapshotResp.GetSnapshot().GetSnapshotId(),
	})
	if err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}
	if restoreResp.GetSnapshot() == nil || restoreResp.GetSnapshot().GetSnapshotId() == "" {
		t.Fatalf("expected restore snapshot metadata, got %#v", restoreResp)
	}
	if restoreResp.GetRestoredSnapshotId() != snapshotResp.GetSnapshot().GetSnapshotId() {
		t.Fatalf("unexpected restored snapshot id: %#v", restoreResp)
	}
	if restoreResp.GetSnapshot().GetFileCount() != 1 {
		t.Fatalf("expected restored workspace to have 1 file, got %#v", restoreResp.GetSnapshot())
	}

	readResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-snapshots",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(after restore) failed: %v", err)
	}
	if got, want := string(readResp.GetContent()), "v1\n"; got != want {
		t.Fatalf("restored content mismatch: got %q want %q", got, want)
	}
	if _, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-snapshots",
		Path:        "notes/todo.txt",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected restored workspace to remove notes/todo.txt, got %v", err)
	}
}

func TestWorkspaceDiff(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-diff",
		Name:        "Diff Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-diff",
		Path:        "README.md",
		Content:     []byte("v1\n"),
	}); err != nil {
		t.Fatalf("WriteFile(v1) failed: %v", err)
	}

	snapshotResp, err := svc.Snapshot(ctx, &filesystemv1.SnapshotRequest{
		WorkspaceId: "ws-diff",
		Message:     "checkpoint one",
	})
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	writeResp, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-diff",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v2\n")},
			{Path: "notes/todo.txt", Content: []byte("later\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(v2) failed: %v", err)
	}

	diffResp, err := svc.Diff(ctx, &filesystemv1.DiffRequest{
		WorkspaceId:    "ws-diff",
		FromSnapshotId: snapshotResp.GetSnapshot().GetSnapshotId(),
		ToSnapshotId:   writeResp.GetCommitHash(),
		IncludePatches: true,
	})
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if diffResp.GetSummary().GetFilesAdded() != 1 || diffResp.GetSummary().GetFilesModified() != 1 || diffResp.GetSummary().GetFilesDeleted() != 0 {
		t.Fatalf("unexpected diff summary counts: %#v", diffResp.GetSummary())
	}
	if diffResp.GetSummary().GetLinesAdded() != 2 || diffResp.GetSummary().GetLinesDeleted() != 1 {
		t.Fatalf("unexpected diff summary line counts: %#v", diffResp.GetSummary())
	}
	if len(diffResp.GetFiles()) != 2 {
		t.Fatalf("expected 2 file diffs, got %#v", diffResp.GetFiles())
	}

	byPath := make(map[string]*filesystemv1.FileDiff, len(diffResp.GetFiles()))
	for _, file := range diffResp.GetFiles() {
		byPath[file.GetPath()] = file
	}
	readmeDiff := byPath["README.md"]
	if readmeDiff == nil || readmeDiff.GetChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY {
		t.Fatalf("expected README.md modify diff, got %#v", diffResp.GetFiles())
	}
	if !strings.Contains(readmeDiff.GetPatch(), "-v1") || !strings.Contains(readmeDiff.GetPatch(), "+v2") {
		t.Fatalf("unexpected README diff patch: %q", readmeDiff.GetPatch())
	}

	notesDiff := byPath["notes/todo.txt"]
	if notesDiff == nil || notesDiff.GetChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_ADD {
		t.Fatalf("expected notes/todo.txt add diff, got %#v", diffResp.GetFiles())
	}
	if !strings.Contains(notesDiff.GetPatch(), "+later") {
		t.Fatalf("unexpected notes diff patch: %q", notesDiff.GetPatch())
	}
}

func TestWorkspaceFork(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-source",
		Name:        "Source Workspace",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	writeResp, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-source",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v1\n")},
			{Path: "docs/info.txt", Content: []byte("source info\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(source) failed: %v", err)
	}

	forkResp, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-source",
		ForkWorkspaceId: "ws-fork",
		Name:            "Fork Workspace",
	})
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	if forkResp.GetWorkspace().GetWorkspaceId() != "ws-fork" {
		t.Fatalf("unexpected fork workspace: %#v", forkResp.GetWorkspace())
	}
	if forkResp.GetSourceWorkspaceId() != "ws-source" || forkResp.GetSourceSnapshotId() != writeResp.GetCommitHash() {
		t.Fatalf("unexpected fork response metadata: %#v", forkResp)
	}
	if forkResp.GetWorkspace().GetFileCount() != 2 {
		t.Fatalf("expected 2 forked files, got %#v", forkResp.GetWorkspace())
	}

	readmeResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-fork",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork README) failed: %v", err)
	}
	if got, want := string(readmeResp.GetContent()), "v1\n"; got != want {
		t.Fatalf("unexpected fork README content: got %q want %q", got, want)
	}

	infoResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-fork",
		Path:        "docs/info.txt",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork docs/info.txt) failed: %v", err)
	}
	if got, want := string(infoResp.GetContent()), "source info\n"; got != want {
		t.Fatalf("unexpected fork info content: got %q want %q", got, want)
	}

	rootList, err := svc.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: "ws-fork",
	})
	if err != nil {
		t.Fatalf("ListDirectory(fork root) failed: %v", err)
	}
	if len(rootList.GetEntries()) != 2 {
		t.Fatalf("expected 2 root entries in fork, got %#v", rootList.GetEntries())
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-source",
		Path:        "README.md",
		Content:     []byte("v2\n"),
	}); err != nil {
		t.Fatalf("WriteFile(source v2) failed: %v", err)
	}

	forkReadmeAfterSourceWrite, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-fork",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork README after source write) failed: %v", err)
	}
	if got, want := string(forkReadmeAfterSourceWrite.GetContent()), "v1\n"; got != want {
		t.Fatalf("fork README should remain independent: got %q want %q", got, want)
	}
}

func TestWorkspaceMerge(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-merge-main",
		Name:        "Merge Main",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	baseWrite, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-merge-main",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v1\n")},
			{Path: "docs/info.txt", Content: []byte("base info\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(base) failed: %v", err)
	}

	if _, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-merge-main",
		ForkWorkspaceId: "ws-merge-fork",
		Name:            "Merge Fork",
	}); err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	targetOnlyWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "LOCAL.md",
		Content:     []byte("keep me\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(target-only) failed: %v", err)
	}

	sourceWrite, err := svc.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: "ws-merge-fork",
		Files: []*filesystemv1.WriteFileInput{
			{Path: "README.md", Content: []byte("v2\n")},
			{Path: "notes/todo.txt", Content: []byte("later\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles(source changes) failed: %v", err)
	}

	mergeResp, err := svc.Merge(ctx, &filesystemv1.MergeRequest{
		WorkspaceId:       "ws-merge-main",
		SourceWorkspaceId: "ws-merge-fork",
	})
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if mergeResp.GetStatus() != filesystemv1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %#v", mergeResp)
	}
	if mergeResp.GetBaseWorkspaceId() != "ws-merge-main" || mergeResp.GetBaseSnapshotId() != baseWrite.GetCommitHash() {
		t.Fatalf("unexpected inferred base: %#v", mergeResp)
	}
	if mergeResp.GetTargetSnapshotId() != targetOnlyWrite.GetCommitHash() || mergeResp.GetSourceSnapshotId() != sourceWrite.GetCommitHash() {
		t.Fatalf("unexpected merge snapshot ids: %#v", mergeResp)
	}
	if mergeResp.GetSummary().GetFilesAdded() != 1 || mergeResp.GetSummary().GetFilesModified() != 1 || mergeResp.GetSummary().GetFilesDeleted() != 0 {
		t.Fatalf("unexpected merge summary counts: %#v", mergeResp.GetSummary())
	}
	if mergeResp.GetSummary().GetLinesAdded() != 2 || mergeResp.GetSummary().GetLinesDeleted() != 1 {
		t.Fatalf("unexpected merge summary lines: %#v", mergeResp.GetSummary())
	}
	if len(mergeResp.GetMergedPaths()) != 2 {
		t.Fatalf("expected 2 merged paths, got %#v", mergeResp.GetMergedPaths())
	}

	readmeResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(merged README) failed: %v", err)
	}
	if got, want := string(readmeResp.GetContent()), "v2\n"; got != want {
		t.Fatalf("unexpected merged README content: got %q want %q", got, want)
	}

	localResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "LOCAL.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(target-only LOCAL.md) failed: %v", err)
	}
	if got, want := string(localResp.GetContent()), "keep me\n"; got != want {
		t.Fatalf("unexpected LOCAL.md content after merge: got %q want %q", got, want)
	}

	notesResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-main",
		Path:        "notes/todo.txt",
	})
	if err != nil {
		t.Fatalf("ReadFile(merged notes/todo.txt) failed: %v", err)
	}
	if got, want := string(notesResp.GetContent()), "later\n"; got != want {
		t.Fatalf("unexpected notes/todo.txt content after merge: got %q want %q", got, want)
	}
}

func TestWorkspaceMergeConflicts(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Name:        "Merge Conflict Main",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	baseWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Path:        "README.md",
		Content:     []byte("base\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(base) failed: %v", err)
	}

	if _, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-merge-conflict-main",
		ForkWorkspaceId: "ws-merge-conflict-fork",
		Name:            "Merge Conflict Fork",
	}); err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Path:        "README.md",
		Content:     []byte("main change\n"),
	}); err != nil {
		t.Fatalf("WriteFile(main change) failed: %v", err)
	}

	sourceWrite, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-merge-conflict-fork",
		Path:        "README.md",
		Content:     []byte("fork change\n"),
	})
	if err != nil {
		t.Fatalf("WriteFile(fork change) failed: %v", err)
	}

	mergeResp, err := svc.Merge(ctx, &filesystemv1.MergeRequest{
		WorkspaceId:       "ws-merge-conflict-main",
		SourceWorkspaceId: "ws-merge-conflict-fork",
	})
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if mergeResp.GetStatus() != filesystemv1.MergeStatus_MERGE_STATUS_CONFLICT {
		t.Fatalf("expected merge conflict, got %#v", mergeResp)
	}
	if mergeResp.GetCommitHash() != "" {
		t.Fatalf("expected no commit hash on conflict, got %#v", mergeResp)
	}
	if mergeResp.GetBaseWorkspaceId() != "ws-merge-conflict-main" || mergeResp.GetBaseSnapshotId() != baseWrite.GetCommitHash() {
		t.Fatalf("unexpected inferred conflict base: %#v", mergeResp)
	}
	if mergeResp.GetSourceSnapshotId() != sourceWrite.GetCommitHash() {
		t.Fatalf("unexpected source snapshot on conflict: %#v", mergeResp)
	}
	if len(mergeResp.GetConflicts()) != 1 {
		t.Fatalf("expected 1 merge conflict, got %#v", mergeResp.GetConflicts())
	}
	conflict := mergeResp.GetConflicts()[0]
	if conflict.GetPath() != "README.md" {
		t.Fatalf("unexpected conflict path: %#v", conflict)
	}
	if conflict.GetSourceChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY || conflict.GetTargetChangeType() != filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY {
		t.Fatalf("unexpected conflict change types: %#v", conflict)
	}
	if !strings.Contains(conflict.GetSourcePatch(), "+fork change") || !strings.Contains(conflict.GetTargetPatch(), "+main change") {
		t.Fatalf("unexpected conflict patches: %#v", conflict)
	}

	readmeResp, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-merge-conflict-main",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(main after conflict) failed: %v", err)
	}
	if got, want := string(readmeResp.GetContent()), "main change\n"; got != want {
		t.Fatalf("target workspace should remain unchanged on conflict: got %q want %q", got, want)
	}
}

func TestWorkspaceListAndResolveConflicts(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()

	svc := NewService(st)
	if _, err := svc.CreateWorkspace(ctx, &filesystemv1.CreateWorkspaceRequest{
		WorkspaceId: "ws-conflict-main",
		Name:        "Conflict Main",
	}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	if _, err := svc.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: "ws-conflict-main",
		Path:        "README.md",
		Content:     []byte("shared\n"),
	}); err != nil {
		t.Fatalf("WriteFile(shared) failed: %v", err)
	}

	if _, err := svc.Fork(ctx, &filesystemv1.ForkRequest{
		WorkspaceId:     "ws-conflict-main",
		ForkWorkspaceId: "ws-conflict-fork",
		Name:            "Conflict Fork",
	}); err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	listResp, err := svc.ListConflicts(ctx, &filesystemv1.ListConflictsRequest{
		WorkspaceId: "ws-conflict-main",
	})
	if err != nil {
		t.Fatalf("ListConflicts failed: %v", err)
	}
	if len(listResp.GetConflicts()) != 1 {
		t.Fatalf("expected 1 conflict, got %#v", listResp.GetConflicts())
	}
	conflict := listResp.GetConflicts()[0]
	if conflict.GetPath() != "README.md" {
		t.Fatalf("unexpected conflict path: %#v", conflict)
	}
	if len(conflict.GetWorkspaceIds()) != 2 || conflict.GetWorkspaceIds()[0] != "ws-conflict-fork" || conflict.GetWorkspaceIds()[1] != "ws-conflict-main" {
		t.Fatalf("unexpected conflict workspaces: %#v", conflict)
	}

	resolveResp, err := svc.ResolveConflict(ctx, &filesystemv1.ResolveConflictRequest{
		WorkspaceId:          "ws-conflict-main",
		Path:                 "README.md",
		PreferredWorkspaceId: "ws-conflict-main",
	})
	if err != nil {
		t.Fatalf("ResolveConflict failed: %v", err)
	}
	if resolveResp.GetConflict().GetPath() != "README.md" {
		t.Fatalf("unexpected resolved conflict payload: %#v", resolveResp.GetConflict())
	}
	if len(resolveResp.GetConflict().GetWorkspaceIds()) != 1 || resolveResp.GetConflict().GetWorkspaceIds()[0] != "ws-conflict-main" {
		t.Fatalf("unexpected resolved conflict owners: %#v", resolveResp.GetConflict())
	}

	listAfterResolve, err := svc.ListConflicts(ctx, &filesystemv1.ListConflictsRequest{
		WorkspaceId: "ws-conflict-main",
	})
	if err != nil {
		t.Fatalf("ListConflicts after resolve failed: %v", err)
	}
	if len(listAfterResolve.GetConflicts()) != 0 {
		t.Fatalf("expected conflicts to be resolved, got %#v", listAfterResolve.GetConflicts())
	}

	forkReadme, err := svc.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: "ws-conflict-fork",
		Path:        "README.md",
	})
	if err != nil {
		t.Fatalf("ReadFile(fork after resolve) failed: %v", err)
	}
	if got, want := string(forkReadme.GetContent()), "shared\n"; got != want {
		t.Fatalf("fork content should remain readable after resolve: got %q want %q", got, want)
	}
}
