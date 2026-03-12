package adminservice

import (
	"context"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
)

func TestListSlicesForAuthenticatedUserExcludesRootSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("EnsureRootSliceInitialized failed: %v", err)
	}

	now := time.Now()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          "home.alice",
		Name:        "alice",
		Description: "alice home",
		Owners:      []string{"alice"},
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   "alice",
	}); err != nil {
		t.Fatalf("CreateSlice(home.alice) failed: %v", err)
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

	resp, err := svc.ListSlices(withAdminUser(ctx, "alice"), &adminv1.ListSlicesRequest{Limit: 50})
	if err != nil {
		t.Fatalf("ListSlices failed: %v", err)
	}

	if len(resp.GetSlices()) != 2 {
		t.Fatalf("expected 2 owned slices, got %d", len(resp.GetSlices()))
	}
	for _, slice := range resp.GetSlices() {
		if slice.GetIsRoot() || slice.GetSliceId() == "root_slice" {
			t.Fatalf("expected root slice to be excluded, got %#v", slice)
		}
	}
}

func TestListSlicesWithoutUserReturnsRootSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("EnsureRootSliceInitialized failed: %v", err)
	}

	resp, err := svc.ListSlices(ctx, &adminv1.ListSlicesRequest{Limit: 50})
	if err != nil {
		t.Fatalf("ListSlices failed: %v", err)
	}

	if len(resp.GetSlices()) != 1 {
		t.Fatalf("expected only root slice for anonymous user, got %d", len(resp.GetSlices()))
	}
	if !resp.GetSlices()[0].GetIsRoot() || resp.GetSlices()[0].GetSliceId() != "root_slice" {
		t.Fatalf("expected root slice for anonymous user, got %#v", resp.GetSlices()[0])
	}
}
