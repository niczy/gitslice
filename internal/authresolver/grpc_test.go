package authresolver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

func TestRequireGRPCIdentityRejectsLinkedWorkOSToken(t *testing.T) {
	resetWorkOSVerifierCacheForTest()

	privateKey, jwksServer := startWorkOSJWKSFixture(t)
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	st := storage.NewInMemoryStorage()
	if err := st.CreateAccount(context.Background(), &models.Account{
		AccountID:  "acct_123",
		OwnerMode:  models.AccountOwnerModeHumanAttached,
		ClaimState: models.AccountClaimStateClaimed,
	}); err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if err := st.CreateUser(context.Background(), &models.User{
		Username:     "alice",
		AccountID:    "acct_123",
		AuthSource:   "workos",
		WorkOSUserID: "user_123",
	}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
	token := signWorkOSToken(t, privateKey, "client_test_123", "user_123", "sess_workos_123")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))

	_, err := RequireGRPCIdentity(ctx, st)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected direct WorkOS bearer to be exchange-only, got %v", err)
	}
}

func TestRequireGRPCIdentityRejectsUnlinkedWorkOSToken(t *testing.T) {
	resetWorkOSVerifierCacheForTest()

	privateKey, jwksServer := startWorkOSJWKSFixture(t)
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("WORKOS_CLIENT_ID", "client_test_123")
	t.Setenv("WORKOS_JWKS_URL", jwksServer.URL)

	st := storage.NewInMemoryStorage()
	token := signWorkOSToken(t, privateKey, "client_test_123", "user_missing", "sess_workos_123")
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))

	_, err := RequireGRPCIdentity(ctx, st)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func resetWorkOSVerifierCacheForTest() {
	workOSVerifierMu.Lock()
	defer workOSVerifierMu.Unlock()
	workOSVerifierKey = ""
	workOSVerifierCache = nil
	workOSVerifierErr = nil
}

func startWorkOSJWKSFixture(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kid": "test-kid",
				"kty": "RSA",
				"alg": "RS256",
				"use": "sig",
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes()),
			}},
		})
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return privateKey, server
}

func signWorkOSToken(t *testing.T, privateKey *rsa.PrivateKey, audience, subject, sessionID string) string {
	t.Helper()

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"aud": []string{audience},
		"exp": now.Add(10 * time.Minute).Unix(),
		"iat": now.Unix(),
		"iss": "https://api.workos.com/",
		"sub": subject,
		"sid": sessionID,
	})
	token.Header["kid"] = "test-kid"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("SignedString failed: %v", err)
	}
	return signed
}

func TestMain(m *testing.M) {
	resetWorkOSVerifierCacheForTest()
	code := m.Run()
	resetWorkOSVerifierCacheForTest()
	os.Exit(code)
}
