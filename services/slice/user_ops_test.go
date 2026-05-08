package sliceservice

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func withSliceUser(ctx context.Context, username string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "User "+username))
}

func TestListSlicesForAuthenticatedUserExcludesRootSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newSliceServiceServer(st)
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("EnsureRootSliceInitialized failed: %v", err)
	}

	now := time.Now()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          "home_alice",
		Name:        "alice",
		Description: "alice home",
		Owners:      []string{"alice"},
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   "alice",
	}); err != nil {
		t.Fatalf("CreateSlice(home_alice) failed: %v", err)
	}
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          "team.alpha",
		Name:        "team.alpha",
		Description: "team slice",
		Owners:      []string{"alice"},
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   "alice",
	}); err != nil {
		t.Fatalf("CreateSlice(team.alpha) failed: %v", err)
	}

	resp, err := svc.ListSlices(withSliceUser(ctx, "alice"), &slicev1.ListSlicesRequest{Limit: 50})
	if err != nil {
		t.Fatalf("ListSlices failed: %v", err)
	}

	if len(resp.GetSlices()) != 2 {
		t.Fatalf("expected 2 owned slices, got %d", len(resp.GetSlices()))
	}
	for _, slice := range resp.GetSlices() {
		if slice.GetIsRoot() || slice.GetSliceId() == "root" {
			t.Fatalf("expected root slice to be excluded, got %#v", slice)
		}
		if slice.GetSlug() == "" {
			t.Fatalf("expected slice slug to be populated, got %#v", slice)
		}
		if slice.GetSliceId() == "home_alice" && slice.GetSlug() != "alice" {
			t.Fatalf("expected home slice slug alice, got %#v", slice)
		}
	}
}

func TestListSlicesWithoutUserReturnsRootSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newSliceServiceServer(st)
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("EnsureRootSliceInitialized failed: %v", err)
	}

	resp, err := svc.ListSlices(ctx, &slicev1.ListSlicesRequest{Limit: 50})
	if err != nil {
		t.Fatalf("ListSlices failed: %v", err)
	}

	if len(resp.GetSlices()) != 1 {
		t.Fatalf("expected only root slice for anonymous user, got %d", len(resp.GetSlices()))
	}
	if !resp.GetSlices()[0].GetIsRoot() || resp.GetSlices()[0].GetSliceId() != "root" {
		t.Fatalf("expected root slice for anonymous user, got %#v", resp.GetSlices()[0])
	}
	if resp.GetSlices()[0].GetSlug() != "root" {
		t.Fatalf("expected root slug, got %#v", resp.GetSlices()[0])
	}
}

func TestResolveConflictRejectsUnknownPreferredSlice(t *testing.T) {
	ctx := withSliceUser(context.Background(), "alice")
	st := storage.NewInMemoryStorage()
	svc := newSliceServiceServer(st)

	sliceA := &models.Slice{ID: "slice-a", Name: "slice-a", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("CreateSlice(slice-a) failed: %v", err)
	}
	sliceB := &models.Slice{ID: "slice-b", Name: "slice-b", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("CreateSlice(slice-b) failed: %v", err)
	}
	for _, setup := range []struct {
		sliceID string
		content string
	}{
		{sliceA.ID, "alpha\n"},
		{sliceB.ID, "beta\n"},
	} {
		manifest, err := storage.WriteSliceFileManifest(ctx, st, setup.sliceID, "shared.txt", []byte(setup.content))
		if err != nil {
			t.Fatalf("WriteSliceFileManifest(%s) failed: %v", setup.sliceID, err)
		}
		entry := &models.DirectoryEntry{
			ID:       setup.sliceID + ":shared.txt",
			Path:     "shared.txt",
			Type:     "file",
			ParentID: setup.sliceID,
			Size:     manifest.TotalSize,
			Hash:     manifest.Hash,
		}
		if err := st.AddEntry(ctx, entry); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", setup.sliceID, err)
		}
		if err := st.AddFileToSlice(ctx, "shared.txt", setup.sliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", setup.sliceID, err)
		}
	}

	_, err := svc.ResolveConflict(ctx, &slicev1.ResolveConflictRequest{
		FileId:           "shared.txt",
		PreferredSliceId: "slice-missing",
	})
	if err == nil {
		t.Fatal("expected ResolveConflict to fail for unknown preferred slice")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v (%v)", got, err)
	}
}

func TestResolveConflictNormalizesConflictingSliceContent(t *testing.T) {
	ctx := withSliceUser(context.Background(), "alice")
	st := storage.NewInMemoryStorage()
	svc := newSliceServiceServer(st)

	sliceA := &models.Slice{ID: "slice-a", Name: "slice-a", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("CreateSlice(slice-a) failed: %v", err)
	}
	sliceB := &models.Slice{ID: "slice-b", Name: "slice-b", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("CreateSlice(slice-b) failed: %v", err)
	}

	fileID := "shared.txt"
	for _, setup := range []struct {
		sliceID    string
		content    string
		executable bool
	}{
		{sliceA.ID, "alpha\n", true},
		{sliceB.ID, "beta\n", false},
	} {
		manifest, err := storage.WriteSliceFileManifestWithMetadata(ctx, st, setup.sliceID, fileID, []byte(setup.content), setup.executable, "")
		if err != nil {
			t.Fatalf("WriteSliceFileManifestWithMetadata(%s) failed: %v", setup.sliceID, err)
		}
		entry := &models.DirectoryEntry{
			ID:         setup.sliceID + ":" + fileID,
			Path:       fileID,
			Type:       "file",
			ParentID:   setup.sliceID,
			Size:       manifest.TotalSize,
			Hash:       manifest.Hash,
			Executable: setup.executable,
		}
		if err := st.AddEntry(ctx, entry); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", setup.sliceID, err)
		}
		if err := st.AddFileToSlice(ctx, fileID, setup.sliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", setup.sliceID, err)
		}
	}

	before, err := svc.GetConflicts(ctx, &slicev1.ConflictsRequest{})
	if err != nil {
		t.Fatalf("GetConflicts(before) failed: %v", err)
	}
	if before.GetTotalConflicts() != 1 {
		t.Fatalf("expected one divergent conflict before resolution, got %d", before.GetTotalConflicts())
	}

	resp, err := svc.ResolveConflict(ctx, &slicev1.ResolveConflictRequest{
		FileId:           fileID,
		PreferredSliceId: sliceA.ID,
	})
	if err != nil {
		t.Fatalf("ResolveConflict failed: %v", err)
	}
	if len(resp.GetResolvedConflict().GetConflictingSliceIds()) != 0 {
		t.Fatalf("expected no remaining divergent conflicts, got %#v", resp.GetResolvedConflict().GetConflictingSliceIds())
	}

	after, err := svc.GetConflicts(ctx, &slicev1.ConflictsRequest{})
	if err != nil {
		t.Fatalf("GetConflicts(after) failed: %v", err)
	}
	if after.GetTotalConflicts() != 0 {
		t.Fatalf("expected conflicts to clear after normalization, got %d", after.GetTotalConflicts())
	}

	contentA, err := storage.ReadSliceFileContent(ctx, st, sliceA.ID, fileID)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(slice-a) failed: %v", err)
	}
	contentB, err := storage.ReadSliceFileContent(ctx, st, sliceB.ID, fileID)
	if err != nil {
		t.Fatalf("ReadSliceFileContent(slice-b) failed: %v", err)
	}
	if string(contentA.Content) != string(contentB.Content) {
		t.Fatalf("expected normalized content to match, got %q vs %q", string(contentA.Content), string(contentB.Content))
	}
	entryB, err := st.GetEntryByPath(ctx, sliceB.ID, fileID)
	if err != nil {
		t.Fatalf("GetEntryByPath(slice-b) failed: %v", err)
	}
	if !entryB.Executable {
		t.Fatalf("expected executable bit to be normalized from preferred slice")
	}
	if !strings.EqualFold(strings.TrimSpace(entryB.Hash), strings.TrimSpace(contentA.Hash)) {
		t.Fatalf("expected normalized hash %q, got %q", contentA.Hash, entryB.Hash)
	}
}
