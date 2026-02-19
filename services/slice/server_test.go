package sliceservice

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/metadata"
)

const statusFilterAll = slicev1.ChangesetStatus(-1)

func TestListChangesetsFiltersByStatus(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-1", Name: "slice-1", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	now := time.Now()
	changesets := []*models.Changeset{
		{
			ID:            "cs-pending",
			SliceID:       slice.ID,
			Status:        models.ChangesetStatusPending,
			ModifiedFiles: []string{"file1"},
			CreatedAt:     now,
		},
		{
			ID:            "cs-merged",
			SliceID:       slice.ID,
			Status:        models.ChangesetStatusMerged,
			ModifiedFiles: []string{"file2"},
			CreatedAt:     now.Add(time.Minute),
		},
	}

	for _, cs := range changesets {
		if err := st.CreateChangeset(ctx, cs); err != nil {
			t.Fatalf("failed to seed changeset %s: %v", cs.ID, err)
		}
	}

	srv := NewService(st)

	t.Run("no filter returns all", func(t *testing.T) {
		resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{SliceId: slice.ID, StatusFilter: statusFilterAll})
		if err != nil {
			t.Fatalf("ListChangesets returned error: %v", err)
		}
		if got, want := len(resp.Changesets), len(changesets); got != want {
			t.Fatalf("expected %d changesets, got %d", want, got)
		}
	})

	t.Run("pending filter", func(t *testing.T) {
		resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{SliceId: slice.ID, StatusFilter: slicev1.ChangesetStatus_PENDING})
		if err != nil {
			t.Fatalf("ListChangesets returned error: %v", err)
		}
		if got, want := len(resp.Changesets), 1; got != want {
			t.Fatalf("expected %d pending changeset, got %d", want, got)
		}
		if resp.Changesets[0].ChangesetId != "cs-pending" {
			t.Fatalf("expected cs-pending, got %s", resp.Changesets[0].ChangesetId)
		}
	})

	t.Run("merged filter", func(t *testing.T) {
		resp, err := srv.ListChangesets(ctx, &slicev1.ListChangesetsRequest{SliceId: slice.ID, StatusFilter: slicev1.ChangesetStatus_MERGED})
		if err != nil {
			t.Fatalf("ListChangesets returned error: %v", err)
		}
		if got, want := len(resp.Changesets), 1; got != want {
			t.Fatalf("expected %d merged changeset, got %d", want, got)
		}
		if resp.Changesets[0].ChangesetId != "cs-merged" {
			t.Fatalf("expected cs-merged, got %s", resp.Changesets[0].ChangesetId)
		}
	})
}

func TestCreateSliceAutoGeneratesID(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	if err := st.AddFileToSlice(ctx, "o/genesis/projects/repo/main.go", "root_slice"); err != nil {
		t.Fatalf("failed to add root file: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		FolderPath:    "o/genesis/projects/repo",
		Name:          "my-slice",
		Description:   "auto id test",
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}

	if !strings.HasPrefix(resp.SliceId, "sl-") {
		t.Fatalf("expected auto-generated ID with sl- prefix, got %q", resp.SliceId)
	}
	if resp.Name != "my-slice" {
		t.Fatalf("expected name %q, got %q", "my-slice", resp.Name)
	}

	// Verify we can retrieve it
	slice, err := st.GetSlice(ctx, resp.SliceId)
	if err != nil {
		t.Fatalf("failed to get slice by generated ID: %v", err)
	}
	if slice.Name != "my-slice" {
		t.Fatalf("stored name mismatch: %q", slice.Name)
	}
}

func TestRenameSlice(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl-test-rename", Name: "old-name", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.RenameSlice(ctx, &slicev1.RenameSliceRequest{
		SliceId: "sl-test-rename",
		NewName: "new-name",
	})
	if err != nil {
		t.Fatalf("RenameSlice failed: %v", err)
	}

	if resp.Name != "new-name" {
		t.Fatalf("expected name %q, got %q", "new-name", resp.Name)
	}

	// Verify persistence
	updated, err := st.GetSlice(ctx, "sl-test-rename")
	if err != nil {
		t.Fatalf("failed to get slice: %v", err)
	}
	if updated.Name != "new-name" {
		t.Fatalf("stored name not updated: %q", updated.Name)
	}
}

func TestGetSliceByName(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "sl-lookup-test", Name: "my-project", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	resp, err := srv.GetSliceByName(ctx, &slicev1.GetSliceByNameRequest{Name: "my-project"})
	if err != nil {
		t.Fatalf("GetSliceByName failed: %v", err)
	}

	if resp.SliceId != "sl-lookup-test" {
		t.Fatalf("expected ID %q, got %q", "sl-lookup-test", resp.SliceId)
	}
	if resp.Name != "my-project" {
		t.Fatalf("expected name %q, got %q", "my-project", resp.Name)
	}
}

func TestCreateSliceFromMultipleFoldersRemapsCheckoutPaths(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	seedFiles := map[string][]byte{
		"o/genesis/projects/repo-a/README.md":   []byte("repo a"),
		"o/genesis/projects/repo-a/pkg/util.go": []byte("package repoa"),
		"o/genesis/projects/repo-b/main.go":     []byte("package main"),
	}
	for filePath, content := range seedFiles {
		if err := st.AddFileToSlice(ctx, filePath, "root_slice"); err != nil {
			t.Fatalf("failed to add root file %s: %v", filePath, err)
		}
		if err := st.AddFileContent(ctx, &models.FileContent{
			FileID:  filePath,
			Path:    filePath,
			Content: content,
			Size:    int64(len(content)),
		}); err != nil {
			t.Fatalf("failed to add content for %s: %v", filePath, err)
		}
	}

	srv := NewService(st)
	createResp, err := srv.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: "root_slice",
		NewSliceId:    "multi-folder-slice",
		Name:          "multi-folder-slice",
		Description:   "multi folder test",
		FolderPath:    "o/genesis/projects/repo-a",
		FolderPaths:   []string{"o/genesis/projects/repo-b"},
	})
	if err != nil {
		t.Fatalf("CreateSliceFromFolder failed: %v", err)
	}
	if got, want := createResp.GetSliceId(), "multi-folder-slice"; got != want {
		t.Fatalf("expected slice %q, got %q", want, got)
	}

	slice, err := st.GetSlice(ctx, "multi-folder-slice")
	if err != nil {
		t.Fatalf("failed to load created slice: %v", err)
	}
	if len(slice.FolderMounts) != 2 {
		t.Fatalf("expected 2 folder mounts, got %d", len(slice.FolderMounts))
	}

	mounts := map[string]string{}
	for _, mount := range slice.FolderMounts {
		mounts[mount.SourcePath] = mount.Alias
	}
	if mounts["o/genesis/projects/repo-a"] != "repo-a" {
		t.Fatalf("unexpected alias for repo-a: %q", mounts["o/genesis/projects/repo-a"])
	}
	if mounts["o/genesis/projects/repo-b"] != "repo-b" {
		t.Fatalf("unexpected alias for repo-b: %q", mounts["o/genesis/projects/repo-b"])
	}

	checkoutResp, err := srv.CheckoutSlice(ctx, &slicev1.CheckoutRequest{
		SliceId:    "multi-folder-slice",
		CommitHash: "HEAD",
	})
	if err != nil {
		t.Fatalf("CheckoutSlice failed: %v", err)
	}

	manifestPaths := map[string]bool{}
	for _, fm := range checkoutResp.GetManifest().GetFileMetadata() {
		manifestPaths[fm.GetPath()] = true
		if strings.HasPrefix(fm.GetPath(), "o/genesis/projects/") {
			t.Fatalf("checkout path should be slice-root relative, got %q", fm.GetPath())
		}
	}

	expectedPaths := []string{
		"repo-a/README.md",
		"repo-a/pkg/util.go",
		"repo-b/main.go",
	}
	for _, path := range expectedPaths {
		if !manifestPaths[path] {
			t.Fatalf("expected checkout path %q, got %#v", path, manifestPaths)
		}
	}
}

type rootSliceLookupCounter struct {
	storage.Storage
	lookups int
}

func (c *rootSliceLookupCounter) GetRootSlice(ctx context.Context) (*models.Slice, error) {
	c.lookups++
	return c.Storage.GetRootSlice(ctx)
}

func TestPromoteSliceCachesRootSliceLookup(t *testing.T) {
	ctx := context.Background()
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	countingStorage := &rootSliceLookupCounter{Storage: base}
	srv := newSliceServiceServer(countingStorage)

	if err := srv.promoteSlice(ctx, "slice-a", "commit-a", []string{"a.txt"}, time.Now()); err != nil {
		t.Fatalf("first promoteSlice failed: %v", err)
	}
	if err := srv.promoteSlice(ctx, "slice-b", "commit-b", []string{"b.txt"}, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("second promoteSlice failed: %v", err)
	}

	if countingStorage.lookups != 1 {
		t.Fatalf("expected one GetRootSlice lookup across promotions, got %d", countingStorage.lookups)
	}
}
