package filesystemservice

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func TestHomeSlicePromotionUpdatesRootStateWithoutMaterializingRootFiles(t *testing.T) {
	ctx := authContext("tester")
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(context.Background(), st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	srv := newFilesystemServiceServer(st)
	srv.promotionBatchWindow = 20 * time.Millisecond
	homeID := homeslice.IDForUsername("tester")

	writeResp, err := srv.WriteFiles(ctx, &filesystemv1.WriteFilesRequest{
		WorkspaceId: homeID,
		Files: []*filesystemv1.WriteFileInput{
			{Path: "/tester/docs/readme.md", Content: []byte("published from home\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteFiles failed: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("wait for promotions: %v", err)
	}

	rootSlice, err := st.GetRootSlice(context.Background())
	if err != nil {
		t.Fatalf("get root slice: %v", err)
	}
	homeFile, err := storage.ReadSliceFileContent(context.Background(), st, homeID, "tester/docs/readme.md")
	if err != nil {
		t.Fatalf("home file missing after write: %v", err)
	}
	if got := string(homeFile.Content); got != "published from home\n" {
		t.Fatalf("unexpected home file content: %q", got)
	}
	if _, err := st.GetEntryByPath(context.Background(), rootSlice.ID, "tester/docs/readme.md"); err != storage.ErrEntryNotFound {
		t.Fatalf("expected no physical root file after logical promotion, got %v", err)
	}
	state, err := st.GetGlobalState(context.Background())
	if err != nil {
		t.Fatalf("get global state: %v", err)
	}
	if got, want := state.GlobalCommitHash, writeResp.GetCommitHash(); got != want {
		t.Fatalf("expected global commit hash %q, got %q", want, got)
	}
	changesets, err := st.ListChangesets(context.Background(), homeID, nil, 10)
	if err != nil {
		t.Fatalf("list changesets: %v", err)
	}
	if len(changesets) != 1 {
		t.Fatalf("expected one implicit promotion changeset, got %d", len(changesets))
	}
	if got, want := changesets[0].Status, models.ChangesetStatusMerged; got != want {
		t.Fatalf("expected merged changeset status %v, got %v", want, got)
	}

	if _, err := srv.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: homeID,
		Path:        "/tester/docs/readme.md",
	}); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("wait for delete promotion: %v", err)
	}
	if _, err := st.GetEntryByPath(context.Background(), homeID, "tester/docs/readme.md"); err != storage.ErrEntryNotFound {
		t.Fatalf("expected home file removal after delete, got %v", err)
	}
	if _, err := st.GetEntryByPath(context.Background(), rootSlice.ID, "tester/docs/readme.md"); err != storage.ErrEntryNotFound {
		t.Fatalf("expected root to remain a logical view without a physical file, got %v", err)
	}
}

func TestHomeSlicePromotionBatchesBurstWrites(t *testing.T) {
	ctx := authContext("tester")
	base := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(context.Background(), base); err != nil {
		t.Fatalf("init root slice: %v", err)
	}
	countingStorage := &filesystemPromotionCounter{
		Storage: base,
		counts:  map[string]int{},
	}

	srv := newFilesystemServiceServer(countingStorage)
	srv.promotionBatchWindow = 100 * time.Millisecond
	homeID := homeslice.IDForUsername("tester")
	lastCommitHash := ""

	for i := 0; i < 3; i++ {
		filePath := fmt.Sprintf("/tester/burst/file-%d.txt", i)
		resp, err := srv.WriteFile(ctx, &filesystemv1.WriteFileRequest{
			WorkspaceId: homeID,
			Path:        filePath,
			Content:     []byte(fmt.Sprintf("burst-%d\n", i)),
		})
		if err != nil {
			t.Fatalf("WriteFile(%s) failed: %v", filePath, err)
		}
		lastCommitHash = resp.GetCommitHash()
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.waitForQueuedPromotions(waitCtx); err != nil {
		t.Fatalf("wait for promotions: %v", err)
	}

	if got := countingStorage.counts["global_state"]; got != 1 {
		t.Fatalf("expected one global state write, got %d", got)
	}
	if got := countingStorage.counts["root_metadata"]; got != 1 {
		t.Fatalf("expected one root metadata write, got %d", got)
	}
	state, err := countingStorage.GetGlobalState(context.Background())
	if err != nil {
		t.Fatalf("get global state: %v", err)
	}
	if got, want := state.GlobalCommitHash, lastCommitHash; got != want {
		t.Fatalf("expected latest promoted commit %q, got %q", want, got)
	}
	changesets, err := countingStorage.ListChangesets(context.Background(), homeID, nil, 10)
	if err != nil {
		t.Fatalf("list changesets: %v", err)
	}
	if len(changesets) != 1 {
		t.Fatalf("expected one implicit promotion changeset for the burst, got %d", len(changesets))
	}
}

type filesystemPromotionCounter struct {
	storage.Storage
	mu     sync.Mutex
	counts map[string]int
}

func (c *filesystemPromotionCounter) UpdateGlobalState(ctx context.Context, state *models.GlobalState) error {
	c.mu.Lock()
	c.counts["global_state"]++
	c.mu.Unlock()
	return c.Storage.UpdateGlobalState(ctx, state)
}

func (c *filesystemPromotionCounter) UpdateSliceMetadata(ctx context.Context, sliceID string, metadata *models.SliceMetadata) error {
	if sliceID == "root" {
		c.mu.Lock()
		c.counts["root_metadata"]++
		c.mu.Unlock()
	}
	return c.Storage.UpdateSliceMetadata(ctx, sliceID, metadata)
}
