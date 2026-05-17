package fileservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type commitByHashCounter struct {
	storage.Storage
	mu    sync.Mutex
	calls int
}

func (c *commitByHashCounter) GetCommitByHash(ctx context.Context, sliceID, commitHash string) (*models.Commit, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.Storage.GetCommitByHash(ctx, sliceID, commitHash)
}

func (c *commitByHashCounter) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type contentReadGuardStorage struct {
	*storage.InMemoryStorage
	mu                  sync.Mutex
	blockContentReads   bool
	getFileAtCommitCall int
}

func (c *contentReadGuardStorage) GetFileAtCommit(ctx context.Context, commitHash, path string) (*models.FileContent, error) {
	c.mu.Lock()
	c.getFileAtCommitCall++
	block := c.blockContentReads
	c.mu.Unlock()
	if block {
		return nil, errors.New("unexpected GetFileAtCommit call")
	}
	return c.InMemoryStorage.GetFileAtCommit(ctx, commitHash, path)
}

func (c *contentReadGuardStorage) contentReadCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getFileAtCommitCall
}

type snapshotLookupCounterStorage struct {
	*storage.InMemoryStorage
	mu                sync.Mutex
	getSnapshotCalls  int
	listSnapshotCalls int
	fileHashCalls     int
}

func (s *snapshotLookupCounterStorage) GetCommitSnapshot(ctx context.Context, commitHash string) (*models.CommitSnapshot, error) {
	s.mu.Lock()
	s.getSnapshotCalls++
	s.mu.Unlock()
	return s.InMemoryStorage.GetCommitSnapshot(ctx, commitHash)
}

func (s *snapshotLookupCounterStorage) ListFilesAtCommit(ctx context.Context, commitHash, pathPrefix string) ([]string, error) {
	s.mu.Lock()
	s.listSnapshotCalls++
	s.mu.Unlock()
	return s.InMemoryStorage.ListFilesAtCommit(ctx, commitHash, pathPrefix)
}

func (s *snapshotLookupCounterStorage) GetCommitSnapshotFileHash(ctx context.Context, commitHash, path string) (string, error) {
	s.mu.Lock()
	s.fileHashCalls++
	s.mu.Unlock()
	return s.InMemoryStorage.GetCommitSnapshotFileHash(ctx, commitHash, path)
}

func (s *snapshotLookupCounterStorage) counts() (getSnapshot, listSnapshot, fileHash int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getSnapshotCalls, s.listSnapshotCalls, s.fileHashCalls
}

func authCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
}

func adminAuthCtx(t testing.TB, st storage.Storage) context.Context {
	t.Helper()
	t.Setenv("ADMIN_USER_EMAILS", "admin@example.com")
	err := st.CreateUser(context.Background(), &models.User{
		Username:     "admin",
		PrimaryEmail: "admin@example.com",
		RootPath:     "admin",
	})
	if err != nil && err != storage.ErrEntryExists {
		t.Fatalf("CreateUser(admin) failed: %v", err)
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User admin"))
}

func mustWriteSliceManifest(tb testing.TB, ctx context.Context, st storage.Storage, sliceID, filePath string, content []byte) string {
	tb.Helper()
	manifest, err := storage.WriteSliceFileManifest(ctx, st, sliceID, filePath, content)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	return manifest.Hash
}

func mustWriteVersionedManifest(tb testing.TB, ctx context.Context, st storage.Storage, filePath, hash string, content []byte) {
	tb.Helper()
	blocks, payloads := storage.ChunkFile(content, storage.DefaultFileBlockSize)
	if len(payloads) > 0 {
		ordered := make([]string, 0, len(payloads))
		for key := range payloads {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			if err := st.PutBlock(ctx, key, payloads[key]); err != nil {
				tb.Fatalf("PutBlock failed: %v", err)
			}
		}
	}
	if err := st.PutVersionedFileManifest(ctx, &models.FileManifest{
		Path:      filePath,
		TotalSize: int64(len(content)),
		Hash:      hash,
		Blocks:    blocks,
	}); err != nil {
		tb.Fatalf("PutVersionedFileManifest failed: %v", err)
	}
}

func TestGetFileRequiresAuthForRootSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	const path = "o/genesis/projects/org/repo/hello.txt"

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, path, "root"); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, "root", path, []byte("hello"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("root", path),
		Path:     path,
		Type:     "file",
		ParentID: "root",
		Content:  []byte("hello"),
		Size:     5,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	meta, err := st.GetSliceMetadata(ctx, "root")
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.ModifiedFiles = []string{path}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, "root", meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	svc := newFileServiceServer(st)
	_, err = svc.GetFile(ctx, &filev1.GetFileRequest{
		Path:    path,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: "root"}},
	})
	if err == nil {
		t.Fatal("expected Unauthenticated for anonymous root slice access, got nil")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}
}

func TestSlicePathCacheTTLExpiresEntries(t *testing.T) {
	cache := newSlicePathCache(4)
	cache.ttl = 5 * time.Millisecond
	cache.put("k", &cachedPaths{
		pathMap:      map[string]string{"a": "a"},
		displayPaths: []string{"a"},
	})

	if _, ok := cache.get("k"); !ok {
		t.Fatal("expected cache hit before ttl expiry")
	}
	time.Sleep(8 * time.Millisecond)
	if _, ok := cache.get("k"); ok {
		t.Fatal("expected cache miss after ttl expiry")
	}
}

func TestGetFileRejectsLargeUnaryPayloads(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const path = "o/genesis/projects/org/repo/big.bin"
	slice := &models.Slice{ID: "big", Name: "big", Owners: []string{"tester"}, CreatedBy: "tester", Files: []string{path}}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	content := bytes.Repeat([]byte("a"), int(maxUnaryGetFileBytes)+1)
	mustWriteSliceManifest(t, ctx, st, "big", path, content)
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("big", path),
		Path:     path,
		Type:     "file",
		ParentID: "big",
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	_, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path:    path,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: "big"}},
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected ResourceExhausted, got %v", err)
	}
}

func TestGetPublicFileAllowsAnonymousReadForPublicFile(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:         "public-file",
		Name:       "public-file",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
		Files:      []string{"docs/public.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, "docs/public.txt", []byte("hello public"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, "docs/public.txt"),
		Path:     "docs/public.txt",
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("hello public")),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.GetPublicFile(context.Background(), &filev1.GetPublicFileRequest{
		SliceId: slice.ID,
		Path:    "docs/public.txt",
	})
	if err != nil {
		t.Fatalf("GetPublicFile failed: %v", err)
	}
	if got := string(resp.GetFile().GetContent()); got != "hello public" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestListEntriesAllowsAnonymousReadForPublicSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:         "public-slice-list",
		Name:       "public-slice-list",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
		Files:      []string{"docs/public.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, "docs/public.txt", []byte("hello public"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, "docs/public.txt"),
		Path:     "docs/public.txt",
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("hello public")),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.ListEntries(context.Background(), &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: slice.ID},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got, want := len(resp.GetEntries()), 1; got != want {
		t.Fatalf("len(entries) = %d, want %d", got, want)
	}
	if got, want := resp.GetEntries()[0].GetPath(), "docs"; got != want {
		t.Fatalf("entry path = %q, want %q", got, want)
	}
}

func TestGetFileAllowsAnonymousReadForPublicSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:         "public-slice-file",
		Name:       "public-slice-file",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
		Files:      []string{"docs/public.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, "docs/public.txt", []byte("hello public"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, "docs/public.txt"),
		Path:     "docs/public.txt",
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("hello public")),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.GetFile(context.Background(), &filev1.GetFileRequest{
		Path: "docs/public.txt",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: slice.ID},
		},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(resp.GetFile().GetContent()); got != "hello public" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestGetFileRequiresAuthForPrivateSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:        "private-slice-file",
		Name:      "private-slice-file",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{"docs/private.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, "docs/private.txt", []byte("hidden"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, "docs/private.txt"),
		Path:     "docs/private.txt",
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("hidden")),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	_, err := svc.GetFile(context.Background(), &filev1.GetFileRequest{
		Path: "docs/private.txt",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: slice.ID},
		},
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("GetFile error = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func TestGetPublicFileReturnsNotFoundForPrivateFile(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:        "private-file",
		Name:      "private-file",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{"docs/private.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, "docs/private.txt", []byte("hidden"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, "docs/private.txt"),
		Path:     "docs/private.txt",
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("hidden")),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	_, err := svc.GetPublicFile(context.Background(), &filev1.GetPublicFileRequest{
		SliceId: slice.ID,
		Path:    "docs/private.txt",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetPublicFile error = %v, want %v", status.Code(err), codes.NotFound)
	}
}

func TestListPublicEntriesReturnsPublicSliceEntries(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:         "public-folder",
		Name:       "public-folder",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
		Files: []string{
			"docs/public.txt",
			"docs/private.txt",
		},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	for filePath, content := range map[string]string{
		"docs/public.txt":  "public",
		"docs/private.txt": "private",
	} {
		mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte(content))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(slice.ID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: slice.ID,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
	}
	storedSlice, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.ListPublicEntries(context.Background(), &filev1.ListPublicEntriesRequest{
		SliceSlug: storage.QualifiedSliceSlug(storedSlice),
		Path:      "docs",
	})
	if err != nil {
		t.Fatalf("ListPublicEntries failed: %v", err)
	}
	if got, want := len(resp.GetEntries()), 2; got != want {
		t.Fatalf("len(entries) = %d, want %d", got, want)
	}
	gotPaths := []string{resp.GetEntries()[0].GetPath(), resp.GetEntries()[1].GetPath()}
	sort.Strings(gotPaths)
	wantPaths := []string{"docs/private.txt", "docs/public.txt"}
	for i := range wantPaths {
		if gotPaths[i] != wantPaths[i] {
			t.Fatalf("entry paths = %#v, want %#v", gotPaths, wantPaths)
		}
	}
}

func TestListPublicEntriesAllowsTraversalForPublicSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:         "public-ancestor",
		Name:       "public-ancestor",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
		Files:      []string{"docs/guides/public.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, "docs/guides/public.txt", []byte("hello"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, "docs/guides/public.txt"),
		Path:     "docs/guides/public.txt",
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("hello")),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}
	storedSlice, err := st.GetSlice(ctx, slice.ID)
	if err != nil {
		t.Fatalf("GetSlice failed: %v", err)
	}

	svc := newFileServiceServer(st)
	rootResp, err := svc.ListPublicEntries(context.Background(), &filev1.ListPublicEntriesRequest{
		SliceSlug: storage.QualifiedSliceSlug(storedSlice),
	})
	if err != nil {
		t.Fatalf("ListPublicEntries(root) failed: %v", err)
	}
	if got, want := len(rootResp.GetEntries()), 1; got != want {
		t.Fatalf("len(root entries) = %d, want %d", got, want)
	}
	if got, want := rootResp.GetEntries()[0].GetPath(), "docs"; got != want {
		t.Fatalf("root entry path = %q, want %q", got, want)
	}

	nestedResp, err := svc.ListPublicEntries(context.Background(), &filev1.ListPublicEntriesRequest{
		SliceSlug: storage.QualifiedSliceSlug(storedSlice),
		Path:      "docs",
	})
	if err != nil {
		t.Fatalf("ListPublicEntries(docs) failed: %v", err)
	}
	if got, want := len(nestedResp.GetEntries()), 1; got != want {
		t.Fatalf("len(docs entries) = %d, want %d", got, want)
	}
	if got, want := nestedResp.GetEntries()[0].GetPath(), "docs/guides"; got != want {
		t.Fatalf("docs entry path = %q, want %q", got, want)
	}
}

func TestGetPublicFileAllowsSlicePublicOverride(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:         "slice-public",
		Name:       "slice-public",
		Owners:     []string{"tester"},
		CreatedBy:  "tester",
		Visibility: models.VisibilityPublic,
		Files:      []string{"docs/private.txt"},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, slice.ID, "docs/private.txt", []byte("slice public"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(slice.ID, "docs/private.txt"),
		Path:     "docs/private.txt",
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len("slice public")),
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.GetPublicFile(context.Background(), &filev1.GetPublicFileRequest{
		SliceId: slice.ID,
		Path:    "docs/private.txt",
	})
	if err != nil {
		t.Fatalf("GetPublicFile failed: %v", err)
	}
	if got := string(resp.GetFile().GetContent()); got != "slice public" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestListEntriesReturnsFileHash(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const path = "README.md"
	slice := &models.Slice{
		ID:        "hashy",
		Name:      "hashy",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{path},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	hash := mustWriteSliceManifest(t, ctx, st, "hashy", path, []byte("hello"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("hashy", path),
		Path:     path,
		Type:     "file",
		ParentID: "hashy",
		Size:     5,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: "hashy"}},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if len(resp.GetEntries()) != 1 {
		t.Fatalf("expected one entry, got %d", len(resp.GetEntries()))
	}
	if got := resp.GetEntries()[0].GetHash(); got != hash {
		t.Fatalf("expected hash %s, got %q", hash, got)
	}
}

func TestGetFileIfNoneMatchReturnsNotModifiedWithoutContentRead(t *testing.T) {
	base := storage.NewInMemoryStorage()
	ctx := authCtx()

	const (
		sliceID = "conditional"
		path    = "README.md"
	)
	if err := base.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{path},
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	hash := mustWriteSliceManifest(t, ctx, base, sliceID, path, []byte("hello"))
	if err := base.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(sliceID, path),
		Path:     path,
		Type:     "file",
		ParentID: sliceID,
		Size:     5,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	guard := &contentReadGuardStorage{
		InMemoryStorage:   base,
		blockContentReads: true,
	}
	svc := newFileServiceServer(guard)

	reqCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"authorization", "User tester",
		"if-none-match", `"`+hash+`"`,
	))
	resp, err := svc.GetFile(reqCtx, &filev1.GetFileRequest{
		Path:    path,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID}},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if resp.GetFile().GetHash() != hash {
		t.Fatalf("expected hash %q, got %q", hash, resp.GetFile().GetHash())
	}
	if len(resp.GetFile().GetContent()) != 0 {
		t.Fatalf("expected empty content for not-modified response")
	}
	if calls := guard.contentReadCalls(); calls != 0 {
		t.Fatalf("expected no content-read storage calls, got %d", calls)
	}
}

func TestListEntriesIncludesMetadataModifiedFiles(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "org", Name: "org", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "o/genesis/projects/org/repo/README.md", "org"); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, "org", "o/genesis/projects/org/repo/README.md", []byte("hello"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("org", "o/genesis/projects/org/repo/README.md"),
		Path:     "o/genesis/projects/org/repo/README.md",
		Type:     "file",
		ParentID: "org",
		Content:  []byte("hello"),
		Size:     5,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	meta, err := st.GetSliceMetadata(ctx, "org")
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.ModifiedFiles = []string{"o/genesis/projects/org/repo/README.md"}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, "org", meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: "org"}},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if len(resp.Entries) == 0 {
		t.Fatalf("expected entries for metadata-backed files")
	}
}

func TestCurrentHeadUsesCommitSnapshotForEntriesAndFiles(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "home_tester"
		headCommit = "cmt_head"
		oldPath    = "tester/hello_world.py"
		newPath    = "tester/hello_world.zig"
	)
	slice := &models.Slice{ID: sliceID, Name: sliceID, Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	oldHash := mustWriteSliceManifest(t, ctx, st, sliceID, oldPath, []byte("print('hello')\n"))
	newHash := mustWriteSliceManifest(t, ctx, st, sliceID, newPath, []byte("pub fn main() void {}\n"))

	meta, err := st.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = headCommit
	meta.ModifiedFiles = []string{newPath}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, sliceID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: headCommit,
		SliceID:    sliceID,
		Files: map[string]string{
			oldPath: oldHash,
			newPath: newHash,
		},
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	svc := newFileServiceServer(st)
	listResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID}},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := listResp.GetSliceHash(); got != headCommit {
		t.Fatalf("expected list slice hash %q, got %q", headCommit, got)
	}
	if len(listResp.GetEntries()) != 1 || listResp.GetEntries()[0].GetPath() != "tester" {
		t.Fatalf("expected root tester directory from commit snapshot, got %#v", listResp.GetEntries())
	}

	fileResp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path:    oldPath,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID}},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(fileResp.GetFile().GetContent()); got != "print('hello')\n" {
		t.Fatalf("unexpected file content %q", got)
	}
}

func TestPinnedSnapshotGetFileUsesIndexedLookup(t *testing.T) {
	ctx := authCtx()
	st := &snapshotLookupCounterStorage{InMemoryStorage: storage.NewInMemoryStorage()}

	slice := &models.Slice{ID: "indexed", Name: "indexed", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	const (
		commitHash = "cmt_indexed_lookup"
		filePath   = "tester/projects/fast.txt"
	)
	fileHash := mustWriteSliceManifest(t, ctx, st, slice.ID, filePath, []byte("fast path\n"))
	files := make(map[string]string, 2048)
	for i := 0; i < 2048; i++ {
		files[fmt.Sprintf("tester/projects/generated-%04d.txt", i)] = fmt.Sprintf("hash-%04d", i)
	}
	files[filePath] = fileHash

	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Files:      files,
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path: filePath,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{
			SliceId:   slice.ID,
			SliceHash: commitHash,
		}},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(resp.GetFile().GetContent()); got != "fast path\n" {
		t.Fatalf("unexpected file content %q", got)
	}
	getSnapshot, listSnapshot, fileHashLookups := st.counts()
	if getSnapshot != 0 {
		t.Fatalf("GetFile loaded full commit snapshots %d times", getSnapshot)
	}
	if listSnapshot != 0 {
		t.Fatalf("GetFile listed commit snapshot paths %d times", listSnapshot)
	}
	if fileHashLookups == 0 {
		t.Fatal("expected indexed commit snapshot file hash lookup")
	}
}

func TestGetFileFindsMetadataModifiedPath(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "org", Name: "org", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	path := "o/genesis/projects/org/repo/README.md"
	if err := st.AddFileToSlice(ctx, path, "org"); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}
	mustWriteSliceManifest(t, ctx, st, "org", path, []byte("hello"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("org", path),
		Path:     path,
		Type:     "file",
		ParentID: "org",
		Content:  []byte("hello"),
		Size:     5,
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	meta, err := st.GetSliceMetadata(ctx, "org")
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.ModifiedFiles = []string{path}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, "org", meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path:    path,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: "org"}},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if string(resp.GetFile().GetContent()) != "hello" {
		t.Fatalf("unexpected content: %q", string(resp.GetFile().GetContent()))
	}
}

func TestSliceMountAliasesAtSliceRoot(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:        "multi",
		Name:      "multi",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files: []string{
			"o/genesis/projects/repo-a/README.md",
			"o/genesis/projects/repo-b/main.go",
		},
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "o/genesis/projects/repo-a", Alias: "repo-a"},
			{SourcePath: "o/genesis/projects/repo-b", Alias: "repo-b"},
		},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	seed := map[string][]byte{
		"o/genesis/projects/repo-a/README.md": []byte("repo-a"),
		"o/genesis/projects/repo-b/main.go":   []byte("package main"),
	}
	for storedPath, content := range seed {
		mustWriteSliceManifest(t, ctx, st, "multi", storedPath, content)
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID("multi", storedPath),
			Path:     storedPath,
			Type:     "file",
			ParentID: "multi",
			Content:  content,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("AddEntry failed for %s: %v", storedPath, err)
		}
	}

	svc := newFileServiceServer(st)
	listResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: "multi"},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := len(listResp.GetEntries()); got != 1 {
		t.Fatalf("expected 1 root folder (top-level component), got %d", got)
	}
	if listResp.GetEntries()[0].GetName() != "o" {
		t.Fatalf("unexpected root entries: %#v", listResp.GetEntries())
	}
	for _, entry := range listResp.GetEntries() {
		if entry.GetType() != filev1.EntryType_ENTRY_TYPE_DIRECTORY {
			t.Fatalf("expected directory entry, got %v", entry.GetType())
		}
	}

	fileResp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path: "repo-a/README.md",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: "multi"},
		},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(fileResp.GetFile().GetContent()); got != "repo-a" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestMountedHomeFileReadPrefersPathHeadManifest(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()

	homeSliceID := homeslice.IDForUsername("alice")
	home := &models.Slice{ID: homeSliceID, Name: "alice", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, home); err != nil {
		t.Fatalf("CreateSlice(home) failed: %v", err)
	}
	mounted := &models.Slice{
		ID:          "alice-mounted-app",
		Name:        "app",
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		ParentSlice: homeSliceID,
		FolderMounts: []models.SliceFolderMount{{
			SourcePath: "alice/app",
			Alias:      "app",
		}},
	}
	if err := st.CreateSlice(ctx, mounted); err != nil {
		t.Fatalf("CreateSlice(mounted) failed: %v", err)
	}

	const storedPath = "alice/app/main.go"
	mustWriteSliceManifest(t, ctx, st, homeSliceID, storedPath, []byte("old materialized\n"))
	newHash := mustWriteSliceManifest(t, ctx, st, mounted.ID, storedPath, []byte("new path head\n"))
	appDirID := common.GenerateEntryID(homeSliceID, "alice/app")
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       appDirID,
		Path:     "alice/app",
		Type:     "directory",
		ParentID: homeSliceID,
	}); err != nil {
		t.Fatalf("AddEntry(app dir) failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(homeSliceID, storedPath),
		Path:     storedPath,
		Type:     "file",
		ParentID: appDirID,
		Size:     int64(len("old materialized\n")),
		Hash:     "old-hash",
	}); err != nil {
		t.Fatalf("AddEntry(file) failed: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             storedPath,
		EntryType:        "file",
		PathVersion:      2,
		ContentHash:      newHash,
		ManifestHash:     newHash,
		SourceSliceID:    mounted.ID,
		SourceCommitHash: "cmt_new",
		LastMergeSeq:     7,
		UpdatedAt:        time.Now(),
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path: "app/main.go",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: mounted.ID},
		},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(resp.GetFile().GetContent()); got != "new path head\n" {
		t.Fatalf("GetFile content = %q, want latest path-head content", got)
	}
	if got := resp.GetFile().GetHash(); got != newHash {
		t.Fatalf("GetFile hash = %q, want %q", got, newHash)
	}
	base := resp.GetFile().GetPathBase()
	if base == nil {
		t.Fatalf("GetFile path_base is nil")
	}
	if got := base.GetPath(); got != storedPath {
		t.Fatalf("path_base.path = %q, want %q", got, storedPath)
	}
	if !base.GetExists() || base.GetContentHash() != newHash || base.GetPathVersion() != 2 || base.GetSourceCommitHash() != "cmt_new" {
		t.Fatalf("unexpected file path base: %#v", base)
	}
	token := resp.GetStateToken()
	if token == nil || token.GetSliceId() != mounted.ID || len(token.GetCursors()) != 1 {
		t.Fatalf("unexpected file state token: %#v", token)
	}
	cursor := token.GetCursors()[0]
	if cursor.GetHomeId() != "alice" || cursor.GetMergeSeq() != 7 || cursor.GetMergeShard() != fileStateTokenShardID("alice") {
		t.Fatalf("unexpected state cursor: %#v", cursor)
	}

	listResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: "app",
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: mounted.ID},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := len(listResp.GetEntries()); got != 1 {
		t.Fatalf("ListEntries returned %d entries, want 1: %#v", got, listResp.GetEntries())
	}
	entryBase := listResp.GetEntries()[0].GetPathBase()
	if entryBase == nil {
		t.Fatalf("ListEntries entry path_base is nil: %#v", listResp.GetEntries()[0])
	}
	if entryBase.GetContentHash() != newHash || entryBase.GetPathVersion() != 2 {
		t.Fatalf("unexpected entry path base: %#v", entryBase)
	}
	if listResp.GetStateToken() == nil || len(listResp.GetStateToken().GetCursors()) != 1 || listResp.GetStateToken().GetCursors()[0].GetMergeSeq() != 7 {
		t.Fatalf("unexpected list state token: %#v", listResp.GetStateToken())
	}
}

func TestSliceMountAliasesSkipMissingBackingFolders(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:        "stale-mounts",
		Name:      "stale-mounts",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "missing/folder", Alias: "missing"},
			{SourcePath: "docs", Alias: "docs"},
		},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("stale-mounts", "docs"),
		Path:     "docs",
		Type:     "directory",
		ParentID: "stale-mounts",
	}); err != nil {
		t.Fatalf("AddEntry failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: "stale-mounts"},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := len(resp.GetEntries()); got != 1 {
		t.Fatalf("expected only existing mount, got %d entries: %#v", got, resp.GetEntries())
	}
	if got := resp.GetEntries()[0].GetPath(); got != "docs" {
		t.Fatalf("expected docs mount, got %q", got)
	}
}

func TestParentMountedSliceListEntriesUsesLiveBackingTree(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	parent := &models.Slice{ID: "parent-live", Name: "parent-live", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, parent); err != nil {
		t.Fatalf("CreateSlice(parent) failed: %v", err)
	}
	writeParentFile := func(filePath, content string) {
		t.Helper()
		hash := mustWriteSliceManifest(t, ctx, st, parent.ID, filePath, []byte(content))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(parent.ID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: parent.ID,
			Size:     int64(len(content)),
			Hash:     hash,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, parent.ID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}

	writeParentFile("docs/README.md", "old")
	fork := &models.Slice{
		ID:          "fork-live",
		Name:        "fork-live",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: parent.ID,
		Files:       []string{"docs/README.md"},
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "docs", Alias: "docs"},
		},
	}
	if err := st.CreateSlice(ctx, fork); err != nil {
		t.Fatalf("CreateSlice(fork) failed: %v", err)
	}

	if err := st.DeleteEntry(ctx, common.GenerateEntryID(parent.ID, "docs/README.md")); err != nil {
		t.Fatalf("DeleteEntry old backing file failed: %v", err)
	}
	if err := st.RemoveFileFromSlice(ctx, "docs/README.md", parent.ID); err != nil {
		t.Fatalf("RemoveFileFromSlice old backing file failed: %v", err)
	}
	writeParentFile("docs/new.txt", "new")

	svc := newFileServiceServer(st)
	resp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: "docs",
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: fork.ID},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := len(resp.GetEntries()); got != 1 {
		t.Fatalf("expected one live backing entry, got %d: %#v", got, resp.GetEntries())
	}
	if got := resp.GetEntries()[0].GetPath(); got != "docs/new.txt" {
		t.Fatalf("expected live backing file docs/new.txt, got %q", got)
	}
}

func TestRootMountedSliceListEntriesUsesLiveHomeBackingTree(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	addDir := func(sliceID, dirPath string) {
		t.Helper()
		parentID := sliceID
		if strings.Contains(dirPath, "/") {
			parentPath := path.Dir(dirPath)
			parentID = common.GenerateEntryID(sliceID, parentPath)
		}
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(sliceID, dirPath),
			Path:     dirPath,
			Type:     "directory",
			ParentID: parentID,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", dirPath, err)
		}
	}
	writeHomeFile := func(filePath, content string) {
		t.Helper()
		hash := mustWriteSliceManifest(t, ctx, st, home.ID, filePath, []byte(content))
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(home.ID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: common.GenerateEntryID(home.ID, path.Dir(filePath)),
			Size:     int64(len(content)),
			Hash:     hash,
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
		if err := st.AddFileToSlice(ctx, filePath, home.ID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}

	addDir(home.ID, "tester/shared")
	writeHomeFile("tester/shared/new.txt", "new from home")
	fork := &models.Slice{
		ID:          "root-mounted-live-home",
		Name:        "root-mounted-live-home",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "tester/shared", Alias: "tester/shared"},
		},
	}
	if err := st.CreateSlice(ctx, fork); err != nil {
		t.Fatalf("CreateSlice(fork) failed: %v", err)
	}

	svc := newFileServiceServer(st)
	listResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: "tester/shared",
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: fork.ID},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := len(listResp.GetEntries()); got != 1 {
		t.Fatalf("expected one live home entry, got %d: %#v", got, listResp.GetEntries())
	}
	if got := listResp.GetEntries()[0].GetPath(); got != "tester/shared/new.txt" {
		t.Fatalf("expected live home file tester/shared/new.txt, got %q", got)
	}

	fileResp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path: "tester/shared/new.txt",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: fork.ID},
		},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(fileResp.GetFile().GetContent()); got != "new from home" {
		t.Fatalf("GetFile content = %q, want live home content", got)
	}
}

func TestRootSliceDirectReadsUseLiveHomeBackingTree(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(home.ID, "tester/shared"),
		Path:     "tester/shared",
		Type:     "directory",
		ParentID: common.GenerateEntryID(home.ID, "tester"),
	}); err != nil {
		t.Fatalf("AddEntry(home dir) failed: %v", err)
	}
	hash := mustWriteSliceManifest(t, ctx, st, home.ID, "tester/shared/live.txt", []byte("live home"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(home.ID, "tester/shared/live.txt"),
		Path:     "tester/shared/live.txt",
		Type:     "file",
		ParentID: common.GenerateEntryID(home.ID, "tester/shared"),
		Size:     int64(len("live home")),
		Hash:     hash,
	}); err != nil {
		t.Fatalf("AddEntry(home file) failed: %v", err)
	}

	svc := newFileServiceServer(st)
	requestCtx := adminAuthCtx(t, st)
	listResp, err := svc.ListEntries(requestCtx, &filev1.ListEntriesRequest{Path: "tester/shared"})
	if err != nil {
		t.Fatalf("ListEntries(root home path) failed: %v", err)
	}
	if got := len(listResp.GetEntries()); got != 1 {
		t.Fatalf("expected one logical root entry, got %d: %#v", got, listResp.GetEntries())
	}
	if got := listResp.GetEntries()[0].GetPath(); got != "tester/shared/live.txt" {
		t.Fatalf("expected logical root file tester/shared/live.txt, got %q", got)
	}

	fileResp, err := svc.GetFile(requestCtx, &filev1.GetFileRequest{Path: "tester/shared/live.txt"})
	if err != nil {
		t.Fatalf("GetFile(root home path) failed: %v", err)
	}
	if got := string(fileResp.GetFile().GetContent()); got != "live home" {
		t.Fatalf("GetFile content = %q, want live home content", got)
	}
}

func TestRootMountedSliceGetFileUsesLiveHomeBackingSliceFilesFallback(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "tester")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}
	root, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}

	if _, err := storage.WriteSliceFileManifest(ctx, st, root.ID, "tester/shared/fallback.txt", []byte("stale root")); err != nil {
		t.Fatalf("WriteSliceFileManifest(root) failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "tester/shared/fallback.txt", root.ID); err != nil {
		t.Fatalf("AddFileToSlice(root) failed: %v", err)
	}
	if _, err := storage.WriteSliceFileManifest(ctx, st, home.ID, "tester/shared/fallback.txt", []byte("live home")); err != nil {
		t.Fatalf("WriteSliceFileManifest(home) failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "tester/shared/fallback.txt", home.ID); err != nil {
		t.Fatalf("AddFileToSlice(home) failed: %v", err)
	}

	fork := &models.Slice{
		ID:          "root-mounted-live-home-files-fallback",
		Name:        "root-mounted-live-home-files-fallback",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: root.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "tester/shared", Alias: "tester/shared"},
		},
	}
	if err := st.CreateSlice(ctx, fork); err != nil {
		t.Fatalf("CreateSlice(fork) failed: %v", err)
	}

	svc := newFileServiceServer(st)
	fileResp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path: "tester/shared/fallback.txt",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: fork.ID},
		},
	})
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	if got := string(fileResp.GetFile().GetContent()); got != "live home" {
		t.Fatalf("GetFile content = %q, want live home fallback content", got)
	}
}

func TestParentMountedSliceListEntriesFallsBackToBackingSliceFiles(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	parent := &models.Slice{
		ID:        "parent-files-only",
		Name:      "parent-files-only",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files:     []string{"docs/README.md"},
	}
	if err := st.CreateSlice(ctx, parent); err != nil {
		t.Fatalf("CreateSlice(parent) failed: %v", err)
	}

	fork := &models.Slice{
		ID:          "fork-files-only",
		Name:        "fork-files-only",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: parent.ID,
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "docs", Alias: "docs"},
		},
	}
	if err := st.CreateSlice(ctx, fork); err != nil {
		t.Fatalf("CreateSlice(fork) failed: %v", err)
	}

	svc := newFileServiceServer(st)
	rootResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: fork.ID},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries(root) failed: %v", err)
	}
	if got := len(rootResp.GetEntries()); got != 1 {
		t.Fatalf("expected one mount alias, got %d: %#v", got, rootResp.GetEntries())
	}
	if got := rootResp.GetEntries()[0].GetPath(); got != "docs" {
		t.Fatalf("expected docs mount alias, got %q", got)
	}

	docsResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: "docs",
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: fork.ID},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries(docs) failed: %v", err)
	}
	if got := len(docsResp.GetEntries()); got != 1 {
		t.Fatalf("expected one file from backing slice files, got %d: %#v", got, docsResp.GetEntries())
	}
	if got := docsResp.GetEntries()[0].GetPath(); got != "docs/README.md" {
		t.Fatalf("expected docs/README.md, got %q", got)
	}
}

func TestSliceMountRootEntriesUseFullAliasName(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:        "mounted-full-alias",
		Name:      "mounted-full-alias",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files: []string{
			"o/genesis/projects/repo-a/README.md",
		},
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "o/genesis/projects/repo-a", Alias: "o/genesis/projects/repo-a"},
		},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: "mounted-full-alias"},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := len(resp.GetEntries()); got != 1 {
		t.Fatalf("expected 1 root entry, got %d", got)
	}
	entry := resp.GetEntries()[0]
	if entry.GetPath() != "o" {
		t.Fatalf("unexpected path %q", entry.GetPath())
	}
	if entry.GetName() != "o" {
		t.Fatalf("expected top-level component name, got %q", entry.GetName())
	}
}

func TestFileHistoryUsesDisplayPathForMountedSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{
		ID:        "mounted",
		Name:      "mounted",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "o/genesis/projects/repo-a", Alias: "repo-a"},
		},
	}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	change := &models.FileChangeRecord{
		ID:         "change-1",
		SliceID:    "mounted",
		CommitHash: "commit-1",
		Path:       "repo-a/README.md",
		ChangeType: models.ChangeTypeModify,
		Author:     "tester",
		Message:    "update readme",
		Timestamp:  time.Now().UTC(),
	}
	if err := st.AddFileChange(ctx, change); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	svc := newFileServiceServer(st)

	historyResp, err := svc.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
		SliceId: "mounted",
		Path:    "repo-a/README.md",
	})
	if err != nil {
		t.Fatalf("GetFileHistory failed: %v", err)
	}
	if got := len(historyResp.GetChanges()); got != 1 {
		t.Fatalf("expected 1 history item, got %d", got)
	}

	dirResp, err := svc.GetDirectoryHistory(ctx, &filev1.GetDirectoryHistoryRequest{
		SliceId: "mounted",
		Path:    "repo-a",
	})
	if err != nil {
		t.Fatalf("GetDirectoryHistory failed: %v", err)
	}
	if got := len(dirResp.GetChanges()); got != 1 {
		t.Fatalf("expected 1 directory history item, got %d", got)
	}
}

func TestFileHistoryFallsBackToParentMountedSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	parent := &models.Slice{
		ID:        "parent",
		Name:      "parent",
		Owners:    []string{"tester"},
		CreatedBy: "tester",
	}
	if err := st.CreateSlice(ctx, parent); err != nil {
		t.Fatalf("CreateSlice(parent) failed: %v", err)
	}

	fork := &models.Slice{
		ID:          "fork",
		Name:        "fork",
		Owners:      []string{"tester"},
		CreatedBy:   "tester",
		ParentSlice: "parent",
		FolderMounts: []models.SliceFolderMount{
			{SourcePath: "o/github.com/ByteByteGoHq/system-design-101", Alias: "system-design-101"},
		},
	}
	if err := st.CreateSlice(ctx, fork); err != nil {
		t.Fatalf("CreateSlice(fork) failed: %v", err)
	}

	change := &models.FileChangeRecord{
		ID:         "change-parent-1",
		SliceID:    "parent",
		CommitHash: "commit-parent-1",
		Path:       "o/github.com/ByteByteGoHq/system-design-101/CONTRIBUTING.md",
		ChangeType: models.ChangeTypeModify,
		Author:     "tester",
		Message:    "update contributing",
		Timestamp:  time.Now().UTC(),
	}
	if err := st.AddFileChange(ctx, change); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	svc := newFileServiceServer(st)

	fileResp, err := svc.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
		SliceId: "fork",
		Path:    "system-design-101/CONTRIBUTING.md",
	})
	if err != nil {
		t.Fatalf("GetFileHistory failed: %v", err)
	}
	if got := len(fileResp.GetChanges()); got != 1 {
		t.Fatalf("expected 1 history item, got %d", got)
	}
	if got := fileResp.GetChanges()[0].GetPath(); got != "system-design-101/CONTRIBUTING.md" {
		t.Fatalf("expected remapped display path, got %q", got)
	}

	dirResp, err := svc.GetDirectoryHistory(ctx, &filev1.GetDirectoryHistoryRequest{
		SliceId: "fork",
		Path:    "system-design-101",
	})
	if err != nil {
		t.Fatalf("GetDirectoryHistory failed: %v", err)
	}
	if got := len(dirResp.GetChanges()); got != 1 {
		t.Fatalf("expected 1 directory history item, got %d", got)
	}
	if got := dirResp.GetChanges()[0].GetPath(); got != "system-design-101/CONTRIBUTING.md" {
		t.Fatalf("expected remapped directory history path, got %q", got)
	}
	if got := dirResp.GetSummary().GetPath(); got != "system-design-101/" {
		t.Fatalf("expected remapped summary path, got %q", got)
	}
}

func TestSnapshotPathsExcludeStaleFileIDs(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}

	const (
		sliceID    = "root"
		headCommit = "head-1"
		stalePath  = "o/genesis/projects/org/repo/hello.py"
	)

	if err := st.AddFileToSlice(ctx, stalePath, sliceID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	meta, err := st.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		t.Fatalf("GetSliceMetadata failed: %v", err)
	}
	meta.HeadCommitHash = headCommit
	meta.ModifiedFiles = []string{stalePath}
	meta.ModifiedFilesCount = 1
	if err := st.UpdateSliceMetadata(ctx, sliceID, meta); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: headCommit,
		SliceID:    sliceID,
		Files:      map[string]string{},
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	svc := newFileServiceServer(st)
	requestCtx := adminAuthCtx(t, st)
	listResp, err := svc.ListEntries(requestCtx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID, SliceHash: headCommit}},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if len(listResp.GetEntries()) != 0 {
		t.Fatalf("expected stale path to be excluded, got %#v", listResp.GetEntries())
	}

	_, err = svc.GetFile(requestCtx, &filev1.GetFileRequest{
		Path:    stalePath,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID, SliceHash: headCommit}},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestPinnedHomeListEntriesUseCommitSnapshotNotLiveTree(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	st := storage.NewInMemoryStorage()

	home, err := homeslice.EnsureUserHomeSlice(ctx, st, "alice")
	if err != nil {
		t.Fatalf("EnsureUserHomeSlice failed: %v", err)
	}

	const (
		commitHash = "cmt_home_snapshot"
		dirPath    = "alice/test4"
		oldPath    = "alice/test4/hello.txt"
		newPath    = "alice/test4/test.md"
	)

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(home.ID, dirPath),
		Path:     dirPath,
		Type:     "directory",
		ParentID: common.GenerateEntryID(home.ID, "alice"),
	}); err != nil {
		t.Fatalf("AddEntry(dir) failed: %v", err)
	}
	oldHash := mustWriteSliceManifest(t, ctx, st, home.ID, oldPath, []byte("old\n"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(home.ID, oldPath),
		Path:     oldPath,
		Type:     "file",
		ParentID: common.GenerateEntryID(home.ID, dirPath),
		Size:     int64(len("old\n")),
		Hash:     oldHash,
	}); err != nil {
		t.Fatalf("AddEntry(old file) failed: %v", err)
	}
	newHash := mustWriteSliceManifest(t, ctx, st, home.ID, newPath, []byte("new\n"))
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(home.ID, newPath),
		Path:     newPath,
		Type:     "file",
		ParentID: common.GenerateEntryID(home.ID, dirPath),
		Size:     int64(len("new\n")),
		Hash:     newHash,
	}); err != nil {
		t.Fatalf("AddEntry(new file) failed: %v", err)
	}
	if err := st.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
		HomeID:           "alice",
		Path:             newPath,
		EntryType:        "file",
		PathVersion:      2,
		ContentHash:      newHash,
		ManifestHash:     newHash,
		SourceSliceID:    "custom-slice",
		SourceCommitHash: "cmt_live",
		LastMergeSeq:     7,
		UpdatedAt:        time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("UpsertHomePathHeads failed: %v", err)
	}

	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    home.ID,
		Files: map[string]string{
			oldPath: oldHash,
		},
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	svc := newFileServiceServer(st)
	listResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: dirPath,
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: home.ID, SliceHash: commitHash},
		},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if got := len(listResp.GetEntries()); got != 1 {
		t.Fatalf("expected only snapshot entry, got %d: %#v", got, listResp.GetEntries())
	}
	if got := listResp.GetEntries()[0].GetPath(); got != oldPath {
		t.Fatalf("ListEntries returned %q, want %q", got, oldPath)
	}
	if got := listResp.GetEntries()[0].GetPathBase().GetSourceCommitHash(); got != commitHash {
		t.Fatalf("path base source commit = %q, want %q", got, commitHash)
	}

	_, err = svc.GetFile(ctx, &filev1.GetFileRequest{
		Path: newPath,
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: home.ID, SliceHash: commitHash},
		},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected new live-only file to be absent from snapshot, got %v", err)
	}

	liveListResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: dirPath,
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: home.ID},
		},
	})
	if err != nil {
		t.Fatalf("live ListEntries failed: %v", err)
	}
	foundLivePath := false
	for _, entry := range liveListResp.GetEntries() {
		if entry.GetPath() == newPath {
			foundLivePath = true
			break
		}
	}
	if !foundLivePath {
		t.Fatalf("expected live tree to include %q, got %#v", newPath, liveListResp.GetEntries())
	}

	fileResp, err := svc.GetFile(ctx, &filev1.GetFileRequest{
		Path: newPath,
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: home.ID},
		},
	})
	if err != nil {
		t.Fatalf("live GetFile failed: %v", err)
	}
	if got := string(fileResp.GetFile().GetContent()); got != "new\n" {
		t.Fatalf("live GetFile content = %q, want new path head content", got)
	}
	if got := fileResp.GetFile().GetPathBase().GetSourceCommitHash(); got != "cmt_live" {
		t.Fatalf("live path base source commit = %q, want cmt_live", got)
	}
}

func TestGetCommitChangesSkipsBinaryPatchContent(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "root"
		parentHash = "c0"
		commitHash = "c1"
		path       = "o/genesis/projects/org/repo/image.bin"
	)

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}

	if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{CommitHash: commitHash, ParentHash: parentHash, Timestamp: time.Now().UTC(), Message: "update binary"}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}

	mustWriteVersionedManifest(t, ctx, st, path, "oldhash", []byte("hello\n"))
	mustWriteVersionedManifest(t, ctx, st, path, "newhash", []byte{0xff, 0xfe, 0x00, 0x61})

	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: parentHash, SliceID: sliceID, Files: map[string]string{path: "oldhash"}}); err != nil {
		t.Fatalf("SaveCommitSnapshot parent failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: commitHash, SliceID: sliceID, Files: map[string]string{path: "newhash"}}); err != nil {
		t.Fatalf("SaveCommitSnapshot head failed: %v", err)
	}

	if err := st.AddFileChange(ctx, &models.FileChangeRecord{ID: "fc1", SliceID: sliceID, CommitHash: commitHash, Path: path, ChangeType: models.ChangeTypeModify, OldHash: "oldhash", NewHash: "newhash", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	svc := newFileServiceServer(st)
	requestCtx := adminAuthCtx(t, st)
	resp, err := svc.GetCommitChanges(requestCtx, &filev1.GetCommitChangesRequest{CommitHash: commitHash, IncludePatches: true})
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if len(resp.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(resp.Changes))
	}
	if got := resp.Changes[0].Patch; got != "" {
		t.Fatalf("expected empty patch for binary/non-utf8 content, got %q", got)
	}
}

func TestGetCommitChangesLooksUpParentCommitByHashOncePerCommit(t *testing.T) {
	ctx := authCtx()
	baseStorage := storage.NewInMemoryStorage()

	const (
		sliceID    = "root"
		parentHash = "c0"
		commitHash = "c1"
	)

	if err := baseStorage.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}

	if err := baseStorage.AddSliceCommit(ctx, sliceID, &models.Commit{CommitHash: commitHash, ParentHash: parentHash, Timestamp: time.Now().UTC(), Message: "batched updates"}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}

	parentFiles := make(map[string]string)
	headFiles := make(map[string]string)
	for i := 0; i < 3; i++ {
		path := fmt.Sprintf("o/genesis/projects/org/repo/file-%d.txt", i)
		oldHash := fmt.Sprintf("old-%d", i)
		newHash := fmt.Sprintf("new-%d", i)

		mustWriteVersionedManifest(t, ctx, baseStorage, path, oldHash, []byte("before\n"))
		mustWriteVersionedManifest(t, ctx, baseStorage, path, newHash, []byte("after\n"))

		parentFiles[path] = oldHash
		headFiles[path] = newHash

		if err := baseStorage.AddFileChange(ctx, &models.FileChangeRecord{ID: fmt.Sprintf("fc-%d", i), SliceID: sliceID, CommitHash: commitHash, Path: path, ChangeType: models.ChangeTypeModify, OldHash: oldHash, NewHash: newHash, Timestamp: time.Now().UTC()}); err != nil {
			t.Fatalf("AddFileChange failed: %v", err)
		}
	}

	if err := baseStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: parentHash, SliceID: sliceID, Files: parentFiles}); err != nil {
		t.Fatalf("SaveCommitSnapshot parent failed: %v", err)
	}
	if err := baseStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: commitHash, SliceID: sliceID, Files: headFiles}); err != nil {
		t.Fatalf("SaveCommitSnapshot head failed: %v", err)
	}

	countedStorage := &commitByHashCounter{Storage: baseStorage}
	svc := newFileServiceServer(countedStorage)
	requestCtx := adminAuthCtx(t, baseStorage)

	resp, err := svc.GetCommitChanges(requestCtx, &filev1.GetCommitChangesRequest{CommitHash: commitHash, IncludePatches: true})
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if len(resp.Changes) != 3 {
		t.Fatalf("expected 3 changes, got %d", len(resp.Changes))
	}
	if got := countedStorage.CallCount(); got != 1 {
		t.Fatalf("expected one parent lookup, got %d", got)
	}
}

func TestGetCommitChangesRequiresAuthForPrivateSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "private"
		commitHash = "c1"
	)

	if err := st.CreateSlice(ctx, &models.Slice{ID: sliceID, Name: sliceID, Owners: []string{"owner"}, CreatedBy: "owner"}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{ID: "fc1", SliceID: sliceID, CommitHash: commitHash, Path: "README.md", ChangeType: models.ChangeTypeModify, Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	svc := newFileServiceServer(st)
	_, err := svc.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{CommitHash: commitHash})
	if err == nil {
		t.Fatalf("expected auth error")
	}
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied, got %v", status.Code(err))
	}
}

func TestGetCommitChangesAllowsAnonymousPublicSlice(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "public"
		commitHash = "c1"
	)

	if err := st.CreateSlice(ctx, &models.Slice{
		ID:         sliceID,
		Name:       sliceID,
		Owners:     []string{"owner"},
		CreatedBy:  "owner",
		Visibility: models.VisibilityPublic,
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         "fc1",
		SliceID:    sliceID,
		CommitHash: commitHash,
		Path:       "README.md",
		ChangeType: models.ChangeTypeModify,
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	svc := newFileServiceServer(st)
	resp, err := svc.GetCommitChanges(context.Background(), &filev1.GetCommitChangesRequest{CommitHash: commitHash})
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if got, want := len(resp.GetChanges()), 1; got != want {
		t.Fatalf("len(changes) = %d, want %d", got, want)
	}
	if got, want := resp.GetChanges()[0].GetSliceId(), sliceID; got != want {
		t.Fatalf("slice id = %q, want %q", got, want)
	}
}

func TestGetCommitChangesRequiresLoginForPrivateSlice(t *testing.T) {
	ctx := context.Background()
	seedCtx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "private"
		commitHash = "c1"
	)

	if err := st.CreateSlice(seedCtx, &models.Slice{ID: sliceID, Name: sliceID, Owners: []string{"owner"}, CreatedBy: "owner"}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.AddFileChange(seedCtx, &models.FileChangeRecord{ID: "fc1", SliceID: sliceID, CommitHash: commitHash, Path: "README.md", ChangeType: models.ChangeTypeModify, Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	svc := newFileServiceServer(st)
	_, err := svc.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{CommitHash: commitHash})
	if err == nil {
		t.Fatalf("expected auth error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v", status.Code(err))
	}
}

func BenchmarkGetCommitChangesDiffLoading(b *testing.B) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "root"
		parentHash = "c0"
		commitHash = "c1"
		fileCount  = 100
	)

	if err := st.InitializeRootSlice(ctx); err != nil {
		b.Fatalf("InitializeRootSlice failed: %v", err)
	}

	if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{CommitHash: commitHash, ParentHash: parentHash, Timestamp: time.Now().UTC(), Message: "batched updates"}); err != nil {
		b.Fatalf("AddSliceCommit failed: %v", err)
	}

	parentFiles := make(map[string]string, fileCount)
	headFiles := make(map[string]string, fileCount)
	for i := 0; i < fileCount; i++ {
		path := fmt.Sprintf("o/genesis/projects/org/repo/file-%03d.txt", i)
		oldHash := fmt.Sprintf("old-%03d", i)
		newHash := fmt.Sprintf("new-%03d", i)

		mustWriteVersionedManifest(b, ctx, st, path, oldHash, []byte("line 1\nline 2\n"))
		mustWriteVersionedManifest(b, ctx, st, path, newHash, []byte("line 1\nline 2 changed\n"))

		parentFiles[path] = oldHash
		headFiles[path] = newHash

		if err := st.AddFileChange(ctx, &models.FileChangeRecord{ID: fmt.Sprintf("fc-%03d", i), SliceID: sliceID, CommitHash: commitHash, Path: path, ChangeType: models.ChangeTypeModify, OldHash: oldHash, NewHash: newHash, Timestamp: time.Now().UTC()}); err != nil {
			b.Fatalf("AddFileChange failed: %v", err)
		}
	}

	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: parentHash, SliceID: sliceID, Files: parentFiles}); err != nil {
		b.Fatalf("SaveCommitSnapshot parent failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: commitHash, SliceID: sliceID, Files: headFiles}); err != nil {
		b.Fatalf("SaveCommitSnapshot head failed: %v", err)
	}

	svc := newFileServiceServer(st)
	req := &filev1.GetCommitChangesRequest{CommitHash: commitHash, IncludePatches: true}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := svc.GetCommitChanges(ctx, req)
		if err != nil {
			b.Fatalf("GetCommitChanges failed: %v", err)
		}
		if len(resp.Changes) != fileCount {
			b.Fatalf("unexpected change count %d", len(resp.Changes))
		}
	}
}

func BenchmarkPinnedSnapshotGetFileIndexedLookup(b *testing.B) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		commitHash = "cmt_indexed_bench"
		filePath   = "tester/projects/fast.txt"
		fileCount  = 10000
	)
	slice := &models.Slice{ID: "indexed-bench", Name: "indexed-bench", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		b.Fatalf("CreateSlice failed: %v", err)
	}
	fileHash := mustWriteSliceManifest(b, ctx, st, slice.ID, filePath, []byte("fast path\n"))
	files := make(map[string]string, fileCount)
	for i := 0; i < fileCount; i++ {
		files[fmt.Sprintf("tester/projects/generated-%05d.txt", i)] = fmt.Sprintf("hash-%05d", i)
	}
	files[filePath] = fileHash
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
		CommitHash: commitHash,
		SliceID:    slice.ID,
		Files:      files,
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		b.Fatalf("SaveCommitSnapshot failed: %v", err)
	}

	svc := newFileServiceServer(st)
	req := &filev1.GetFileRequest{
		Path: filePath,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{
			SliceId:   slice.ID,
			SliceHash: commitHash,
		}},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := svc.GetFile(ctx, req)
		if err != nil {
			b.Fatalf("GetFile failed: %v", err)
		}
		if string(resp.GetFile().GetContent()) != "fast path\n" {
			b.Fatal("unexpected content")
		}
	}
}

func TestGetCommitChangesOmitsPatchesByDefault(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "root"
		parentHash = "c0"
		commitHash = "c1"
		filePath   = "o/genesis/projects/org/repo/hello.txt"
	)

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{CommitHash: commitHash, ParentHash: parentHash, Timestamp: time.Now().UTC(), Message: "edit"}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}
	mustWriteVersionedManifest(t, ctx, st, filePath, "old", []byte("before\n"))
	mustWriteVersionedManifest(t, ctx, st, filePath, "new", []byte("after\n"))
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: parentHash, SliceID: sliceID, Files: map[string]string{filePath: "old"}}); err != nil {
		t.Fatalf("SaveCommitSnapshot parent failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: commitHash, SliceID: sliceID, Files: map[string]string{filePath: "new"}}); err != nil {
		t.Fatalf("SaveCommitSnapshot head failed: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{ID: "fc1", SliceID: sliceID, CommitHash: commitHash, Path: filePath, ChangeType: models.ChangeTypeModify, OldHash: "old", NewHash: "new", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	svc := newFileServiceServer(st)
	requestCtx := adminAuthCtx(t, st)

	// Without include_patches: no patch generated.
	resp, err := svc.GetCommitChanges(requestCtx, &filev1.GetCommitChangesRequest{CommitHash: commitHash})
	if err != nil {
		t.Fatalf("GetCommitChanges (no patches) failed: %v", err)
	}
	if len(resp.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(resp.Changes))
	}
	if resp.Changes[0].Patch != "" {
		t.Fatalf("expected empty patch without include_patches, got %q", resp.Changes[0].Patch)
	}

	// With include_patches: patch should be generated.
	resp, err = svc.GetCommitChanges(requestCtx, &filev1.GetCommitChangesRequest{CommitHash: commitHash, IncludePatches: true})
	if err != nil {
		t.Fatalf("GetCommitChanges (with patches) failed: %v", err)
	}
	if len(resp.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(resp.Changes))
	}
	if resp.Changes[0].Patch == "" {
		t.Fatal("expected non-empty patch with include_patches=true")
	}
}

func TestGetCommitChangesSkipsPatchesOverThreshold(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "root"
		parentHash = "c0"
		commitHash = "c1"
	)

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}
	if err := st.AddSliceCommit(ctx, sliceID, &models.Commit{CommitHash: commitHash, ParentHash: parentHash, Timestamp: time.Now().UTC(), Message: "big commit"}); err != nil {
		t.Fatalf("AddSliceCommit failed: %v", err)
	}

	// Create maxPatchableChanges+1 file changes to exceed the threshold.
	count := maxPatchableChanges + 1
	parentFiles := make(map[string]string, count)
	headFiles := make(map[string]string, count)
	for i := 0; i < count; i++ {
		p := fmt.Sprintf("o/genesis/projects/org/repo/file-%03d.txt", i)
		oldHash := fmt.Sprintf("old-%03d", i)
		newHash := fmt.Sprintf("new-%03d", i)
		mustWriteVersionedManifest(t, ctx, st, p, oldHash, []byte("before\n"))
		mustWriteVersionedManifest(t, ctx, st, p, newHash, []byte("after\n"))
		parentFiles[p] = oldHash
		headFiles[p] = newHash
		if err := st.AddFileChange(ctx, &models.FileChangeRecord{ID: fmt.Sprintf("fc-%03d", i), SliceID: sliceID, CommitHash: commitHash, Path: p, ChangeType: models.ChangeTypeModify, OldHash: oldHash, NewHash: newHash, Timestamp: time.Now().UTC()}); err != nil {
			t.Fatalf("AddFileChange failed: %v", err)
		}
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: parentHash, SliceID: sliceID, Files: parentFiles}); err != nil {
		t.Fatalf("SaveCommitSnapshot parent failed: %v", err)
	}
	if err := st.SaveCommitSnapshot(ctx, &models.CommitSnapshot{CommitHash: commitHash, SliceID: sliceID, Files: headFiles}); err != nil {
		t.Fatalf("SaveCommitSnapshot head failed: %v", err)
	}

	svc := newFileServiceServer(st)
	requestCtx := adminAuthCtx(t, st)

	// Even with include_patches=true, patches should be skipped when over threshold.
	resp, err := svc.GetCommitChanges(requestCtx, &filev1.GetCommitChangesRequest{CommitHash: commitHash, IncludePatches: true})
	if err != nil {
		t.Fatalf("GetCommitChanges failed: %v", err)
	}
	if len(resp.Changes) != count {
		t.Fatalf("expected %d changes, got %d", count, len(resp.Changes))
	}
	for i, ch := range resp.Changes {
		if ch.Patch != "" {
			t.Fatalf("expected empty patch for change %d over threshold, got %q", i, ch.Patch)
		}
	}
}
