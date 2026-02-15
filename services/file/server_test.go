package fileservice

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type listSliceCommitsCounter struct {
	storage.Storage
	mu    sync.Mutex
	calls int
}

func (c *listSliceCommitsCounter) ListSliceCommits(ctx context.Context, sliceID string, limit int, fromCommitHash string) ([]*models.Commit, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.Storage.ListSliceCommits(ctx, sliceID, limit, fromCommitHash)
}

func (c *listSliceCommitsCounter) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func authCtx() context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
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
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  "o/genesis/projects/org/repo/README.md",
		Path:    "o/genesis/projects/org/repo/README.md",
		Content: []byte("hello"),
		Size:    5,
	}); err != nil {
		t.Fatalf("AddFileContent failed: %v", err)
	}
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
	if err := st.AddFileContent(ctx, &models.FileContent{FileID: path, Path: path, Content: []byte("hello")}); err != nil {
		t.Fatalf("AddFileContent failed: %v", err)
	}
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
		if err := st.AddFileContent(ctx, &models.FileContent{
			FileID:  storedPath,
			Path:    storedPath,
			Content: content,
			Size:    int64(len(content)),
		}); err != nil {
			t.Fatalf("AddFileContent failed for %s: %v", storedPath, err)
		}
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
	if got := len(listResp.GetEntries()); got != 2 {
		t.Fatalf("expected 2 root folders, got %d", got)
	}
	if listResp.GetEntries()[0].GetName() != "repo-a" || listResp.GetEntries()[1].GetName() != "repo-b" {
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

func TestSnapshotPathsExcludeStaleFileIDs(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}

	const (
		sliceID    = "root_slice"
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
	listResp, err := svc.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID}},
	})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	if len(listResp.GetEntries()) != 0 {
		t.Fatalf("expected stale path to be excluded, got %#v", listResp.GetEntries())
	}

	_, err = svc.GetFile(ctx, &filev1.GetFileRequest{
		Path:    stalePath,
		Version: &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID}},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestGetCommitChangesSkipsBinaryPatchContent(t *testing.T) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "root_slice"
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

	if err := st.AddFileContent(ctx, &models.FileContent{FileID: path, Path: path, Hash: "oldhash", Content: []byte("hello\n")}); err != nil {
		t.Fatalf("AddFileContent old failed: %v", err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{FileID: path, Path: path, Hash: "newhash", Content: []byte{0xff, 0xfe, 0x00, 0x61}}); err != nil {
		t.Fatalf("AddFileContent new failed: %v", err)
	}

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
	resp, err := svc.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{CommitHash: commitHash})
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

func TestGetCommitChangesLooksUpParentCommitOncePerCommit(t *testing.T) {
	ctx := authCtx()
	baseStorage := storage.NewInMemoryStorage()

	const (
		sliceID    = "root_slice"
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

		if err := baseStorage.AddFileContent(ctx, &models.FileContent{FileID: path, Path: path, Hash: oldHash, Content: []byte("before\n")}); err != nil {
			t.Fatalf("AddFileContent old failed: %v", err)
		}
		if err := baseStorage.AddFileContent(ctx, &models.FileContent{FileID: path, Path: path, Hash: newHash, Content: []byte("after\n")}); err != nil {
			t.Fatalf("AddFileContent new failed: %v", err)
		}

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

	countedStorage := &listSliceCommitsCounter{Storage: baseStorage}
	svc := newFileServiceServer(countedStorage)

	resp, err := svc.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{CommitHash: commitHash})
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

func BenchmarkGetCommitChangesDiffLoading(b *testing.B) {
	ctx := authCtx()
	st := storage.NewInMemoryStorage()

	const (
		sliceID    = "root_slice"
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

		if err := st.AddFileContent(ctx, &models.FileContent{FileID: path, Path: path, Hash: oldHash, Content: []byte("line 1\nline 2\n")}); err != nil {
			b.Fatalf("AddFileContent old failed: %v", err)
		}
		if err := st.AddFileContent(ctx, &models.FileContent{FileID: path, Path: path, Hash: newHash, Content: []byte("line 1\nline 2 changed\n")}); err != nil {
			b.Fatalf("AddFileContent new failed: %v", err)
		}

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
	req := &filev1.GetCommitChangesRequest{CommitHash: commitHash}

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
