package adminservice

import (
	"context"
	"strings"
	"testing"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResolveConflictRejectsUnknownPreferredSlice(t *testing.T) {
	ctx := withAdminUser(context.Background(), "alice")
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)

	sliceA := &models.Slice{ID: "slice-a", Name: "slice-a", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, sliceA); err != nil {
		t.Fatalf("CreateSlice(slice-a) failed: %v", err)
	}
	sliceB := &models.Slice{ID: "slice-b", Name: "slice-b", Owners: []string{"alice"}, CreatedBy: "alice"}
	if err := st.CreateSlice(ctx, sliceB); err != nil {
		t.Fatalf("CreateSlice(slice-b) failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "shared.txt", sliceA.ID); err != nil {
		t.Fatalf("AddFileToSlice(slice-a) failed: %v", err)
	}
	if err := st.AddFileToSlice(ctx, "shared.txt", sliceB.ID); err != nil {
		t.Fatalf("AddFileToSlice(slice-b) failed: %v", err)
	}

	_, err := svc.ResolveConflict(ctx, &adminv1.ResolveConflictRequest{
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
	ctx := withAdminUser(context.Background(), "alice")
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)

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

	before, err := svc.GetConflicts(ctx, &adminv1.ConflictsRequest{})
	if err != nil {
		t.Fatalf("GetConflicts(before) failed: %v", err)
	}
	if before.GetTotalConflicts() != 1 {
		t.Fatalf("expected one divergent conflict before resolution, got %d", before.GetTotalConflicts())
	}

	resp, err := svc.ResolveConflict(ctx, &adminv1.ResolveConflictRequest{
		FileId:           fileID,
		PreferredSliceId: sliceA.ID,
	})
	if err != nil {
		t.Fatalf("ResolveConflict failed: %v", err)
	}
	if len(resp.GetResolvedConflict().GetConflictingSliceIds()) != 0 {
		t.Fatalf("expected no remaining divergent conflicts, got %#v", resp.GetResolvedConflict().GetConflictingSliceIds())
	}

	after, err := svc.GetConflicts(ctx, &adminv1.ConflictsRequest{})
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
