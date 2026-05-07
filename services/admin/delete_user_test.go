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

func TestAdminDeleteUserByEmailRequiresConfiguredAdmin(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	t.Setenv("AUTH_SECRET", "test-auth-secret")

	if err := st.CreateUser(ctx, &models.User{
		Username:     "operator",
		PrimaryEmail: "operator@example.com",
		ClerkUserID:  "user_operator",
	}); err != nil {
		t.Fatalf("CreateUser(operator) failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "target",
		PrimaryEmail: "target@example.com",
	}); err != nil {
		t.Fatalf("CreateUser(target) failed: %v", err)
	}

	adminCtx := withClerkAdminSession(t, ctx, "user_operator", "operator@example.com")
	_, err := svc.DeleteUserByEmail(adminCtx, &adminv1.DeleteUserByEmailRequest{Email: "target@example.com"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied without ADMIN_USER_EMAILS, got %v", err)
	}

	t.Setenv("ADMIN_USER_EMAILS", "operator@example.com")
	if _, err := svc.DeleteUserByEmail(adminCtx, &adminv1.DeleteUserByEmailRequest{Email: "target@example.com"}); err != nil {
		t.Fatalf("DeleteUserByEmail failed: %v", err)
	}
}

func TestAdminDeleteUserByEmailRejectsLocalSession(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	t.Setenv("AUTH_SECRET", "test-auth-secret")
	t.Setenv("ADMIN_USER_EMAILS", "operator@example.com")

	if err := st.CreateUser(ctx, &models.User{
		Username:     "operator",
		PrimaryEmail: "operator@example.com",
	}); err != nil {
		t.Fatalf("CreateUser(operator) failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "target",
		PrimaryEmail: "target@example.com",
	}); err != nil {
		t.Fatalf("CreateUser(target) failed: %v", err)
	}

	_, err := svc.DeleteUserByEmail(withAdminUser(ctx, "operator"), &adminv1.DeleteUserByEmailRequest{Email: "target@example.com"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated without Clerk admin session, got %v", err)
	}
}

func TestAdminDeleteUserByEmailRemovesOwnedData(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	t.Setenv("AUTH_SECRET", "test-auth-secret")
	t.Setenv("ADMIN_USER_EMAILS", `["admin@example.com"]`)

	if err := st.CreateUser(ctx, &models.User{
		Username:     "adminuser",
		PrimaryEmail: "admin@example.com",
		ClerkUserID:  "user_admin",
	}); err != nil {
		t.Fatalf("CreateUser(adminuser) failed: %v", err)
	}
	if err := st.CreateUser(ctx, &models.User{
		Username:     "target",
		PrimaryEmail: "target@example.com",
	}); err != nil {
		t.Fatalf("CreateUser(target) failed: %v", err)
	}
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "home.target",
		Name:      "target",
		Owners:    []string{"target"},
		CreatedBy: "target",
	}); err != nil {
		t.Fatalf("CreateSlice(home.target) failed: %v", err)
	}
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "target/custom",
		Name:      "custom",
		Owners:    []string{"target"},
		CreatedBy: "target",
	}); err != nil {
		t.Fatalf("CreateSlice(target/custom) failed: %v", err)
	}
	if err := st.CreateAuthSession(ctx, &models.AuthSession{
		SessionID: "sess-target",
		Username:  "target",
		Token:     "token-target",
	}); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}
	if err := st.CreateAgentKey(ctx, &models.AgentKey{
		KeyID:       "key-target",
		Username:    "target",
		Name:        "Target key",
		Algorithm:   "ed25519",
		PublicKey:   []byte("public-key"),
		Fingerprint: "fp-target",
	}); err != nil {
		t.Fatalf("CreateAgentKey failed: %v", err)
	}

	resp, err := svc.DeleteUserByEmail(withClerkAdminSession(t, ctx, "user_admin", "admin@example.com"), &adminv1.DeleteUserByEmailRequest{Email: "target@example.com"})
	if err != nil {
		t.Fatalf("DeleteUserByEmail failed: %v", err)
	}
	if resp.GetUsername() != "target" || resp.GetDeletedSlices() != 2 || resp.GetDeletedSessions() != 1 || resp.GetDeletedAgentKeys() != 1 {
		t.Fatalf("unexpected delete response: %#v", resp)
	}
	if _, err := st.GetUser(ctx, "target"); err != storage.ErrEntryNotFound {
		t.Fatalf("expected target user deleted, got %v", err)
	}
	if _, err := st.GetSlice(ctx, "home.target"); err != storage.ErrSliceNotFound {
		t.Fatalf("expected home slice deleted, got %v", err)
	}
	if keys, err := st.ListAgentKeysByUser(ctx, "target"); err != nil || len(keys) != 0 {
		t.Fatalf("expected target keys deleted, keys=%#v err=%v", keys, err)
	}
}
