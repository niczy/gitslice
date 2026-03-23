package adminservice

import (
	"context"
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
