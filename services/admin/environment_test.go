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

	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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

func TestCreateEnvironmentPersistsProviderConfig(t *testing.T) {
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	ctx := withAdminUser(context.Background(), "alice")

	created, err := svc.CreateEnvironment(ctx, &adminv1.CreateEnvironmentRequest{
		Name:        "cfc-dev",
		DisplayName: "Cloudflare Dev",
		Provider:    "cloudflare_containers",
		ProviderId:  "cfc-profile-dev",
		Region:      "us-east-1",
		ProviderConfig: map[string]string{
			"worker_base_url": "https://edge.example.internal",
			"container_class": "sandbox",
			"instance_type":   "basic",
		},
	})
	if err != nil {
		t.Fatalf("CreateEnvironment failed: %v", err)
	}
	if created.GetProviderConfig()["worker_base_url"] != "https://edge.example.internal" {
		t.Fatalf("expected provider_config.worker_base_url to round-trip, got %#v", created.GetProviderConfig())
	}

	fetched, err := svc.GetEnvironment(ctx, &adminv1.GetEnvironmentRequest{Name: "cfc-dev"})
	if err != nil {
		t.Fatalf("GetEnvironment failed: %v", err)
	}
	if fetched.GetProviderConfig()["container_class"] != "sandbox" || fetched.GetProviderConfig()["instance_type"] != "basic" {
		t.Fatalf("expected cloudflare provider config fields, got %#v", fetched.GetProviderConfig())
	}
}

func TestCreateEnvironmentValidatesCloudflareProviderConfig(t *testing.T) {
	st := storage.NewInMemoryStorage()
	svc := newAdminServiceServer(st)
	ctx := withAdminUser(context.Background(), "alice")

	_, err := svc.CreateEnvironment(ctx, &adminv1.CreateEnvironmentRequest{
		Name:        "cfc-invalid",
		DisplayName: "Cloudflare Invalid",
		Provider:    "cloudflare_containers",
		ProviderId:  "cfc-profile-invalid",
		Region:      "us-east-1",
	})
	if err == nil {
		t.Fatalf("expected invalid config error")
	}
	stErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if stErr.Code() != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %s", stErr.Code())
	}
	if !strings.Contains(stErr.Message(), "providerConfig.worker_base_url") {
		t.Fatalf("expected worker_base_url validation message, got %q", stErr.Message())
	}
}
