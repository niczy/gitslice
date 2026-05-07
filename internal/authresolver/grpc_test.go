package authresolver

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRequireGRPCIdentityAcceptsUserHeader(t *testing.T) {
	st := storage.NewInMemoryStorage()
	if _, err := st.EnsureUser(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	identity, err := RequireGRPCIdentity(ctx, st)
	if err != nil {
		t.Fatalf("RequireGRPCIdentity failed: %v", err)
	}
	if identity.Username != "alice" {
		t.Fatalf("unexpected username: %q", identity.Username)
	}
}

func TestRequireGRPCIdentityRejectsUserHeaderInProduction(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "production")

	st := storage.NewInMemoryStorage()
	if _, err := st.EnsureUser(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	_, err := RequireGRPCIdentity(ctx, st)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected production legacy user auth to be rejected, got %v", err)
	}
}

func TestRequireGRPCIdentityAllowsExplicitProductionUserHeader(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "production")
	t.Setenv("ALLOW_LEGACY_USER_AUTH", "1")

	st := storage.NewInMemoryStorage()
	if _, err := st.EnsureUser(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User alice"))
	identity, err := RequireGRPCIdentity(ctx, st)
	if err != nil {
		t.Fatalf("RequireGRPCIdentity failed: %v", err)
	}
	if identity.Username != "alice" {
		t.Fatalf("unexpected username: %q", identity.Username)
	}
}

func TestRequireGRPCIdentityAcceptsBearerSessionToken(t *testing.T) {
	st := storage.NewInMemoryStorage()
	if _, err := st.EnsureUser(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}
	if err := st.CreateAuthSession(context.Background(), &models.AuthSession{
		SessionID: "sess-alice",
		Username:  "alice",
		Token:     "gs_test_alice",
	}); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer gs_test_alice"))
	identity, err := RequireGRPCIdentity(ctx, st)
	if err != nil {
		t.Fatalf("RequireGRPCIdentity failed: %v", err)
	}
	if identity.Username != "alice" || identity.SessionID != "sess-alice" {
		t.Fatalf("unexpected identity: %#v", identity)
	}
}

func TestRequireGRPCIdentityRejectsInvalidBearerSessionToken(t *testing.T) {
	st := storage.NewInMemoryStorage()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer gs_test_missing"))

	_, err := RequireGRPCIdentity(ctx, st)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}
