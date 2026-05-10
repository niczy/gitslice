package adminservice

import (
	"context"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		if slice.GetSlug() == "" {
			t.Fatalf("expected slice slug to be populated, got %#v", slice)
		}
		if slice.GetSliceId() == "home.alice" && slice.GetSlug() != "alice" {
			t.Fatalf("expected home slice slug alice, got %#v", slice)
		}
	}
}

func TestListSlicesWithoutUserReturnsUnauthenticated(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("EnsureRootSliceInitialized failed: %v", err)
	}

	_, err := svc.ListSlices(ctx, &adminv1.ListSlicesRequest{Limit: 50})
	if err == nil {
		t.Fatal("expected Unauthenticated error for anonymous user, got nil")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", status.Code(err))
	}
}
