package adminservice

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
)

func withAdminUser(ctx context.Context, username string) context.Context {
	return metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "User "+username))
}

func withClerkAdminSession(tb testing.TB, ctx context.Context, userID, email string) context.Context {
	tb.Helper()
	secret := strings.TrimSpace(os.Getenv("AUTH_SECRET"))
	if secret == "" {
		tb.Fatal("AUTH_SECRET must be configured for Clerk admin session tests")
	}
	now := time.Now()
	payload, err := json.Marshal(map[string]any{
		"provider":    "clerk",
		"userId":      userID,
		"sessionId":   "sess_admin_test",
		"email":       email,
		"name":        "Admin User",
		"issuedAtMs":  now.UnixMilli(),
		"expiresAtMs": now.Add(5 * time.Minute).UnixMilli(),
	})
	if err != nil {
		tb.Fatalf("Marshal Clerk admin claims failed: %v", err)
	}
	payloadPart := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadPart))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return metadata.NewIncomingContext(ctx, metadata.Pairs(clerkAdminClaimsMetadataKey, payloadPart+"."+signature))
}
