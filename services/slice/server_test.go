package sliceservice

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filev1 "github.com/niczy/gitslice/proto/file"
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

	srv := newSliceServiceServer(st)

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

	srv := newSliceServiceServer(st)
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
	if mounts["o/genesis/projects/repo-a"] != "o/genesis/projects/repo-a" {
		t.Fatalf("unexpected alias for repo-a: %q", mounts["o/genesis/projects/repo-a"])
	}
	if mounts["o/genesis/projects/repo-b"] != "o/genesis/projects/repo-b" {
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
	}

	expectedPaths := []string{
		"o/genesis/projects/repo-a/README.md",
		"o/genesis/projects/repo-a/pkg/util.go",
		"o/genesis/projects/repo-b/main.go",
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

type addFileToSliceCounter struct {
	storage.Storage
	counts map[string]int
}

func (c *addFileToSliceCounter) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	if c.counts == nil {
		c.counts = make(map[string]int)
	}
	c.counts[sliceID+":"+fileID]++
	return c.Storage.AddFileToSlice(ctx, fileID, sliceID)
}

func TestMergeChangesetDeduplicatesModifiedFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	slice := &models.Slice{ID: "slice-dup", Name: "slice-dup", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := base.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	cs := &models.Changeset{
		ID:            "cs-dup",
		SliceID:       slice.ID,
		ModifiedFiles: []string{"dup.txt", "dup.txt", "dup.txt"},
		Status:        models.ChangesetStatusPending,
		CreatedAt:     time.Now(),
	}
	if err := base.CreateChangeset(ctx, cs); err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	countingStorage := &addFileToSliceCounter{Storage: base, counts: map[string]int{}}
	srv := newSliceServiceServer(countingStorage)

	resp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if resp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", resp.GetStatus())
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root promotion queue: %v", err)
	}

	if got := countingStorage.counts["slice-dup:dup.txt"]; got != 1 {
		t.Fatalf("expected one ownership write for slice file, got %d", got)
	}
	if got := countingStorage.counts["root_slice:dup.txt"]; got != 1 {
		t.Fatalf("expected one ownership write for root file, got %d", got)
	}

	updatedCS, err := base.GetChangeset(ctx, cs.ID)
	if err != nil {
		t.Fatalf("failed to load merged changeset: %v", err)
	}
	if len(updatedCS.ModifiedFiles) != 1 || updatedCS.ModifiedFiles[0] != "dup.txt" {
		t.Fatalf("expected deduplicated modified files, got %#v", updatedCS.ModifiedFiles)
	}
}

func TestCreateChangesetDeduplicatesModifiedFiles(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-create-dup", Name: "slice-create-dup", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:       slice.ID,
		ModifiedFiles: []string{"dup.txt", "dup.txt", "dup.txt"},
		Message:       "dedupe",
	})
	if err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}

	cs, err := st.GetChangeset(ctx, createResp.GetChangesetId())
	if err != nil {
		t.Fatalf("failed to load changeset: %v", err)
	}
	if len(cs.ModifiedFiles) != 1 || cs.ModifiedFiles[0] != "dup.txt" {
		t.Fatalf("expected deduplicated modified files, got %#v", cs.ModifiedFiles)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: cs.ID})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if reviewResp.GetDiff().GetFilesAdded() != 1 {
		t.Fatalf("expected diff files_added=1, got %d", reviewResp.GetDiff().GetFilesAdded())
	}
}

type promotionWriteCounter struct {
	storage.Storage

	mu                      sync.Mutex
	rootAddCalls            map[string]int
	updateGlobalStateCalls  int
	updateRootMetadataCalls int
}

func (c *promotionWriteCounter) AddFileToSlice(ctx context.Context, fileID, sliceID string) error {
	if sliceID == "root_slice" {
		c.mu.Lock()
		if c.rootAddCalls == nil {
			c.rootAddCalls = make(map[string]int)
		}
		c.rootAddCalls[fileID]++
		c.mu.Unlock()
	}
	return c.Storage.AddFileToSlice(ctx, fileID, sliceID)
}

func (c *promotionWriteCounter) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	c.mu.Lock()
	c.updateGlobalStateCalls++
	c.mu.Unlock()
	return c.Storage.UpdateGlobalState(ctx, state)
}

func (c *promotionWriteCounter) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	if sliceID == "root_slice" {
		c.mu.Lock()
		c.updateRootMetadataCalls++
		c.mu.Unlock()
	}
	return c.Storage.UpdateSliceMetadata(ctx, sliceID, metadata)
}

func TestRootPromotionQueueBatchesSameSlice(t *testing.T) {
	ctx := context.Background()
	base := storage.NewInMemoryStorage()
	if err := base.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	countingStorage := &promotionWriteCounter{
		Storage:      base,
		rootAddCalls: make(map[string]int),
	}
	srv := newSliceServiceServer(countingStorage)
	srv.promotionBatchWindow = 50 * time.Millisecond

	now := time.Now()
	jobs := []struct {
		commitHash string
		files      []string
	}{
		{commitHash: "commit-1", files: []string{"a.txt", "a.txt"}},
		{commitHash: "commit-2", files: []string{"a.txt", "b.txt"}},
		{commitHash: "commit-3", files: []string{"b.txt", "c.txt"}},
	}
	for i, job := range jobs {
		if err := srv.enqueueRootPromotion(ctx, "slice-batch", job.commitHash, job.files, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("enqueueRootPromotion(%s) failed: %v", job.commitHash, err)
		}
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for promotion queue: %v", err)
	}

	if got := countingStorage.updateGlobalStateCalls; got != 1 {
		t.Fatalf("expected one global state write for batched promotion, got %d", got)
	}
	if got := countingStorage.updateRootMetadataCalls; got != 1 {
		t.Fatalf("expected one root metadata write for batched promotion, got %d", got)
	}
	for _, fileID := range []string{"a.txt", "b.txt", "c.txt"} {
		if got := countingStorage.rootAddCalls[fileID]; got != 1 {
			t.Fatalf("expected one root ownership write for %s, got %d", fileID, got)
		}
	}

	state, err := base.GetGlobalState(ctx)
	if err != nil {
		t.Fatalf("failed to load global state: %v", err)
	}
	if got, want := state.GlobalCommitHash, "commit-3"; got != want {
		t.Fatalf("expected head commit %q, got %q", want, got)
	}
	if got, want := len(state.History), len(jobs); got != want {
		t.Fatalf("expected %d history entries, got %d", want, got)
	}
	if got, want := state.History[0].CommitHash, "commit-3"; got != want {
		t.Fatalf("expected newest history commit %q, got %q", want, got)
	}
}

func TestRevertChangesetHashRoundTrip(t *testing.T) {
	commitHash := "commit-abcdef123456"
	changeID := "chg-001"

	hash := buildRevertChangesetHash(commitHash, changeID)
	gotCommit, gotChange, ok := parseRevertChangesetHash(hash)
	if !ok {
		t.Fatalf("expected parseRevertChangesetHash to parse %q", hash)
	}
	if gotCommit != commitHash {
		t.Fatalf("expected commit hash %q, got %q", commitHash, gotCommit)
	}
	if gotChange != changeID {
		t.Fatalf("expected change id %q, got %q", changeID, gotChange)
	}
}

func TestReviewChangesetIncludesRevertPatch(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert", Name: "slice-revert", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  "content-old",
		Path:    "README.md",
		Content: []byte("line1\n"),
		Size:    int64(len("line1\n")),
		Hash:    "content-old",
	}); err != nil {
		t.Fatalf("failed to add old content: %v", err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  "content-new",
		Path:    "README.md",
		Content: []byte("line1\nline2\n"),
		Size:    int64(len("line1\nline2\n")),
		Hash:    "content-new",
	}); err != nil {
		t.Fatalf("failed to add new content: %v", err)
	}

	const sourceCommit = "commit-source"
	const sourceChangeID = "chg-source"
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:           sourceChangeID,
		SliceID:      slice.ID,
		CommitHash:   sourceCommit,
		Path:         "README.md",
		ChangeType:   models.ChangeTypeModify,
		OldHash:      "content-old",
		NewHash:      "content-new",
		LinesAdded:   1,
		LinesDeleted: 0,
		Author:       "tester",
		Message:      "add line2",
		Timestamp:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("failed to add source change: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		ChangeId:   sourceChangeID,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if len(reviewResp.GetChanges()) != 1 {
		t.Fatalf("expected 1 review change, got %d", len(reviewResp.GetChanges()))
	}

	change := reviewResp.GetChanges()[0]
	if got, want := change.GetChangeType(), filev1.ChangeType_CHANGE_TYPE_MODIFY; got != want {
		t.Fatalf("expected revert change type %v, got %v", want, got)
	}
	if !strings.Contains(change.GetPatch(), "-line2") {
		t.Fatalf("expected revert patch to remove line2, got %q", change.GetPatch())
	}
	if reviewResp.GetDiff().GetFilesModified() != 1 {
		t.Fatalf("expected diff files_modified=1, got %d", reviewResp.GetDiff().GetFilesModified())
	}
}

func TestReviewChangesetIncludesAllCommitDiffChangesForRevert(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert-all", Name: "slice-revert-all", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	contents := []*models.FileContent{
		{FileID: "readme-old", Path: "README.md", Content: []byte("line1\n"), Size: int64(len("line1\n")), Hash: "readme-old"},
		{FileID: "readme-new", Path: "README.md", Content: []byte("line1\nline2\n"), Size: int64(len("line1\nline2\n")), Hash: "readme-new"},
		{FileID: "newfile", Path: "NEW.md", Content: []byte("new file\n"), Size: int64(len("new file\n")), Hash: "newfile"},
	}
	for _, content := range contents {
		if err := st.AddFileContent(ctx, content); err != nil {
			t.Fatalf("failed to add content %s: %v", content.Hash, err)
		}
	}

	const sourceCommit = "commit-source-all"
	seedChanges := []*models.FileChangeRecord{
		{
			ID:           "chg-modify",
			SliceID:      slice.ID,
			CommitHash:   sourceCommit,
			Path:         "README.md",
			ChangeType:   models.ChangeTypeModify,
			OldHash:      "readme-old",
			NewHash:      "readme-new",
			LinesAdded:   1,
			LinesDeleted: 0,
			Author:       "tester",
			Message:      "modify readme",
			Timestamp:    time.Now().UTC(),
		},
		{
			ID:           "chg-add",
			SliceID:      slice.ID,
			CommitHash:   sourceCommit,
			Path:         "NEW.md",
			ChangeType:   models.ChangeTypeAdd,
			OldHash:      "",
			NewHash:      "newfile",
			LinesAdded:   1,
			LinesDeleted: 0,
			Author:       "tester",
			Message:      "add new file",
			Timestamp:    time.Now().UTC().Add(time.Second),
		},
	}
	if err := st.AddFileChanges(ctx, seedChanges); err != nil {
		t.Fatalf("failed to add source changes: %v", err)
	}

	srv := NewService(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	reviewResp, err := srv.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("ReviewChangeset failed: %v", err)
	}
	if len(reviewResp.GetChanges()) != 2 {
		t.Fatalf("expected 2 review changes, got %d", len(reviewResp.GetChanges()))
	}

	changesByPath := make(map[string]*filev1.FileChangeRecord, len(reviewResp.GetChanges()))
	for _, change := range reviewResp.GetChanges() {
		changesByPath[change.GetPath()] = change
	}
	readmeChange := changesByPath["README.md"]
	if readmeChange == nil {
		t.Fatalf("expected README.md revert change in response")
	}
	if got, want := readmeChange.GetChangeType(), filev1.ChangeType_CHANGE_TYPE_MODIFY; got != want {
		t.Fatalf("expected README.md revert change type %v, got %v", want, got)
	}
	if !strings.Contains(readmeChange.GetPatch(), "-line2") {
		t.Fatalf("expected README.md revert patch to remove line2, got %q", readmeChange.GetPatch())
	}

	newFileChange := changesByPath["NEW.md"]
	if newFileChange == nil {
		t.Fatalf("expected NEW.md revert change in response")
	}
	if got, want := newFileChange.GetChangeType(), filev1.ChangeType_CHANGE_TYPE_DELETE; got != want {
		t.Fatalf("expected NEW.md revert change type %v, got %v", want, got)
	}
	if newFileChange.GetPatch() == "" {
		t.Fatalf("expected NEW.md revert patch to be present")
	}

	if reviewResp.GetDiff().GetFilesModified() != 1 {
		t.Fatalf("expected diff files_modified=1, got %d", reviewResp.GetDiff().GetFilesModified())
	}
	if reviewResp.GetDiff().GetFilesDeleted() != 1 {
		t.Fatalf("expected diff files_deleted=1, got %d", reviewResp.GetDiff().GetFilesDeleted())
	}
}

func TestMergeRevertChangesetAppliesRevertedContent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert-merge", Name: "slice-revert-merge", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	const filePath = "README.md"
	const oldHash = "hash-old"
	const newHash = "hash-new"
	oldContent := []byte("line1\n")
	newContent := []byte("line1\nline2\n")

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(newContent)),
	}); err != nil {
		t.Fatalf("failed to add file entry: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("failed to add file to slice index: %v", err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  filePath,
		Path:    filePath,
		Content: newContent,
		Size:    int64(len(newContent)),
		Hash:    newHash,
	}); err != nil {
		t.Fatalf("failed to add current slice content: %v", err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  oldHash,
		Path:    filePath,
		Content: oldContent,
		Size:    int64(len(oldContent)),
		Hash:    oldHash,
	}); err != nil {
		t.Fatalf("failed to add old versioned content: %v", err)
	}

	const sourceCommit = "commit-revert-merge"
	const sourceChangeID = "chg-revert-merge"
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:           sourceChangeID,
		SliceID:      slice.ID,
		CommitHash:   sourceCommit,
		Path:         filePath,
		ChangeType:   models.ChangeTypeModify,
		OldHash:      oldHash,
		NewHash:      newHash,
		LinesAdded:   1,
		LinesDeleted: 0,
		Author:       "tester",
		Message:      "introduce line2",
		Timestamp:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("failed to add source change: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", mergeResp.GetStatus())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root promotion queue: %v", err)
	}

	fileAfterMerge, err := st.GetSliceFileByPath(ctx, slice.ID, filePath)
	if err != nil {
		t.Fatalf("failed to load file after merge: %v", err)
	}
	if string(fileAfterMerge.Content) != string(oldContent) {
		t.Fatalf("expected reverted content %q, got %q", string(oldContent), string(fileAfterMerge.Content))
	}
	if fileAfterMerge.Hash != oldHash {
		t.Fatalf("expected reverted hash %q, got %q", oldHash, fileAfterMerge.Hash)
	}

	commitChanges, err := st.GetCommitChanges(ctx, mergeResp.GetNewCommitHash())
	if err != nil {
		t.Fatalf("failed to load commit changes for merged revert: %v", err)
	}
	if len(commitChanges) != 1 {
		t.Fatalf("expected 1 recorded commit change, got %d", len(commitChanges))
	}
	recorded := commitChanges[0]
	if recorded.OldHash != newHash {
		t.Fatalf("expected recorded old hash %q, got %q", newHash, recorded.OldHash)
	}
	if recorded.NewHash != oldHash {
		t.Fatalf("expected recorded new hash %q, got %q", oldHash, recorded.NewHash)
	}

	snapshot, err := st.GetCommitSnapshot(ctx, mergeResp.GetNewCommitHash())
	if err != nil {
		t.Fatalf("failed to load commit snapshot: %v", err)
	}
	if got := snapshot.Files[filePath]; got != oldHash {
		t.Fatalf("expected commit snapshot hash %q, got %q", oldHash, got)
	}
}

func TestMergeRevertChangesetBackfillsMissingOldHash(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User tester"))
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-revert-backfill", Name: "slice-revert-backfill", Owners: []string{"tester"}, CreatedBy: "tester"}
	if err := st.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	const filePath = "README.md"
	const oldHash = "hash-old-backfill"
	const newHash = "hash-new-backfill"
	oldContent := []byte("line1\n")
	newContent := []byte("line1\nline2\n")

	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       slice.ID + ":" + filePath,
		Path:     filePath,
		Type:     "file",
		ParentID: slice.ID,
		Size:     int64(len(newContent)),
	}); err != nil {
		t.Fatalf("failed to add file entry: %v", err)
	}
	if err := st.AddFileToSlice(ctx, filePath, slice.ID); err != nil {
		t.Fatalf("failed to add file to slice index: %v", err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  filePath,
		Path:    filePath,
		Content: newContent,
		Size:    int64(len(newContent)),
		Hash:    newHash,
	}); err != nil {
		t.Fatalf("failed to add current slice content: %v", err)
	}
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  oldHash,
		Path:    filePath,
		Content: oldContent,
		Size:    int64(len(oldContent)),
		Hash:    oldHash,
	}); err != nil {
		t.Fatalf("failed to add old versioned content: %v", err)
	}

	previousCommit := "commit-backfill-previous"
	sourceCommit := "commit-backfill-source"
	now := time.Now().UTC()

	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         "chg-backfill-previous",
		SliceID:    slice.ID,
		CommitHash: previousCommit,
		Path:       filePath,
		ChangeType: models.ChangeTypeModify,
		OldHash:    "hash-base-backfill",
		NewHash:    oldHash,
		Author:     "tester",
		Message:    "prepare history",
		Timestamp:  now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("failed to add previous change: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         "chg-backfill-source",
		SliceID:    slice.ID,
		CommitHash: sourceCommit,
		Path:       filePath,
		ChangeType: models.ChangeTypeModify,
		OldHash:    "",
		NewHash:    newHash,
		Author:     "tester",
		Message:    "source with missing old hash",
		Timestamp:  now,
	}); err != nil {
		t.Fatalf("failed to add source change: %v", err)
	}

	srv := newSliceServiceServer(st)
	createResp, err := srv.RevertCommitChange(ctx, &slicev1.RevertCommitChangeRequest{
		CommitHash: sourceCommit,
		SliceId:    slice.ID,
	})
	if err != nil {
		t.Fatalf("RevertCommitChange failed: %v", err)
	}

	mergeResp, err := srv.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("MergeChangeset failed: %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got %v", mergeResp.GetStatus())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("timed out waiting for root promotion queue: %v", err)
	}

	fileAfterMerge, err := st.GetSliceFileByPath(ctx, slice.ID, filePath)
	if err != nil {
		t.Fatalf("failed to load file after merge: %v", err)
	}
	if string(fileAfterMerge.Content) != string(oldContent) {
		t.Fatalf("expected reverted content %q, got %q", string(oldContent), string(fileAfterMerge.Content))
	}
	if fileAfterMerge.Hash != oldHash {
		t.Fatalf("expected reverted hash %q, got %q", oldHash, fileAfterMerge.Hash)
	}
}
