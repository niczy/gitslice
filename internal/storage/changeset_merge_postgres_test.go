package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func TestPostgresAcceptChangesetMergeByIDBasicFileChange(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}

	ctx := context.Background()
	st, err := NewPostgresNativeStorage(ctx, dsn, NewInMemoryObjectStore(), fmt.Sprintf("test-accept-merge-by-id-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("NewPostgresNativeStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sliceID := "home_alice"
	filePath := fmt.Sprintf("alice/app/%s.txt", suffix)
	changesetID := "chg_accept_merge_by_id_" + suffix
	commitHash := "cmt_accept_merge_by_id_" + suffix

	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "alice",
		CreatedBy: "alice",
		Owners:    []string{"alice"},
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	manifest, err := WriteSliceFileManifest(ctx, st, sliceID, filePath, []byte("hello\n"))
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := st.CreateChangeset(ctx, &models.Changeset{
		ID:             changesetID,
		Hash:           "chg_hash_" + suffix,
		SliceID:        sliceID,
		BaseCommitHash: "initial",
		ModifiedFiles:  []string{filePath},
		Status:         models.ChangesetStatusPending,
		Author:         "alice",
		Message:        "add file",
		CreatedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	if err := st.CreateChangesetSnapshot(ctx, &models.ChangesetSnapshot{
		ID:               "snap_accept_merge_by_id_" + suffix,
		ChangesetID:      changesetID,
		Version:          1,
		Hash:             "snap_hash_" + suffix,
		BaseCommitHash:   "initial",
		ModifiedFiles:    []string{filePath},
		FileHashes:       map[string]string{filePath: manifest.Hash},
		BasePathVersions: map[string]int64{filePath: 0},
		RenameSources:    map[string]string{},
		Author:           "alice",
		Message:          "add file",
		CreatedAt:        time.Now(),
	}); err != nil {
		t.Fatalf("CreateChangesetSnapshot failed: %v", err)
	}

	result, err := st.AcceptChangesetMergeByID(ctx, changesetID, "alice", commitHash, time.Now())
	if err != nil {
		t.Fatalf("AcceptChangesetMergeByID failed: %v", err)
	}
	if result.Event == nil || result.Event.SourceCommitHash != commitHash || len(result.Event.PathUpdates) != 1 {
		t.Fatalf("unexpected merge result: %#v", result)
	}
	heads, err := st.GetHomePathHeads(ctx, "alice", []string{filePath})
	if err != nil {
		t.Fatalf("GetHomePathHeads failed: %v", err)
	}
	head := heads[filePath]
	if head == nil || head.ManifestHash != manifest.Hash || head.PathVersion != 1 || head.SourceCommitHash != commitHash {
		t.Fatalf("unexpected path head: %#v", head)
	}
}
