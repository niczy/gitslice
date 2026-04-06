package workosauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifierAcceptsValidAccessToken(t *testing.T) {
	t.Parallel()

	privateKey, jwksServer := startJWKSFixture(t)
	verifier, err := NewVerifier(VerifierConfig{
		ClientID: "client_test_123",
		JWKSURL:  jwksServer.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier failed: %v", err)
	}

	tokenString := signFixtureToken(t, privateKey, "client_test_123", "user_123", "sess_123")
	claims, err := verifier.VerifyAccessToken(context.Background(), tokenString)
	if err != nil {
		t.Fatalf("VerifyAccessToken failed: %v", err)
	}
	if claims.Subject != "user_123" || claims.SessionID != "sess_123" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestVerifierRejectsAudienceMismatch(t *testing.T) {
	t.Parallel()

	privateKey, jwksServer := startJWKSFixture(t)
	verifier, err := NewVerifier(VerifierConfig{
		ClientID: "client_test_123",
		JWKSURL:  jwksServer.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier failed: %v", err)
	}

	tokenString := signFixtureToken(t, privateKey, "client_other", "user_123", "sess_123")
	if _, err := verifier.VerifyAccessToken(context.Background(), tokenString); err == nil {
		t.Fatal("expected VerifyAccessToken to fail for audience mismatch")
	}
}

func TestVerifierAcceptsCustomAuthKitIssuer(t *testing.T) {
	t.Parallel()

	privateKey, jwksServer := startJWKSFixture(t)
	verifier, err := NewVerifier(VerifierConfig{
		ClientID:      "client_test_123",
		JWKSURL:       jwksServer.URL,
		AuthKitDomain: "auth.gitslice.io",
	})
	if err != nil {
		t.Fatalf("NewVerifier failed: %v", err)
	}

	tokenString := signFixtureTokenWithIssuer(t, privateKey, "client_test_123", "user_123", "sess_123", "https://auth.gitslice.io/")
	if _, err := verifier.VerifyAccessToken(context.Background(), tokenString); err != nil {
		t.Fatalf("VerifyAccessToken failed for custom issuer: %v", err)
	}
}

func TestVerifierAcceptsClientScopedUserManagementIssuer(t *testing.T) {
	t.Parallel()

	privateKey, jwksServer := startJWKSFixture(t)
	verifier, err := NewVerifier(VerifierConfig{
		ClientID: "client_test_123",
		JWKSURL:  jwksServer.URL,
	})
	if err != nil {
		t.Fatalf("NewVerifier failed: %v", err)
	}

	tokenString := signFixtureTokenWithIssuer(
		t,
		privateKey,
		"client_test_123",
		"user_123",
		"sess_123",
		"https://api.workos.com/user_management/client_test_123",
	)
	if _, err := verifier.VerifyAccessToken(context.Background(), tokenString); err != nil {
		t.Fatalf("VerifyAccessToken failed for client-scoped issuer: %v", err)
	}
}

func startJWKSFixture(t *testing.T) (*rsa.PrivateKey, *httptest.Server) {
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

func signFixtureToken(t *testing.T, privateKey *rsa.PrivateKey, audience, subject, sessionID string) string {
	t.Helper()
	return signFixtureTokenWithIssuer(t, privateKey, audience, subject, sessionID, defaultIssuer)
}

func signFixtureTokenWithIssuer(t *testing.T, privateKey *rsa.PrivateKey, audience, subject, sessionID, issuer string) string {
	t.Helper()

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    issuer,
			Subject:   subject,
		},
		SessionID: sessionID,
	})
	token.Header["kid"] = "test-kid"

	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("SignedString failed: %v", err)
	}
	return signed
}
