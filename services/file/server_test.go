package fileservice

import (
	"context"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
	"google.golang.org/grpc/metadata"
)

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
