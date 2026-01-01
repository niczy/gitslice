package main

import (
	"context"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

const statusFilterAll = slicev1.ChangesetStatus(-1)

func TestListChangesetsFiltersByStatus(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	slice := &models.Slice{ID: "slice-1", Name: "slice-1"}
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
