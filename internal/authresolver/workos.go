package authresolver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/workosauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	workOSVerifierMu    sync.Mutex
	workOSVerifierKey   string
	workOSVerifierCache *workosauth.Verifier
	workOSVerifierErr   error
)

func ResolveGRPCWorkOSClaims(ctx context.Context) (*workosauth.Claims, error) {
	token := auth.TokenFromGRPCContext(ctx)
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "WorkOS login required")
	}
	return verifyWorkOSAccessToken(ctx, token)
}

func VerifyExplicitWorkOSAccessToken(ctx context.Context, token string) (*workosauth.Claims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "WorkOS access token required")
	}
	return verifyWorkOSAccessToken(ctx, token)
}

func workOSVerifierFromEnv() (*workosauth.Verifier, error) {
	clientID := strings.TrimSpace(os.Getenv("WORKOS_CLIENT_ID"))
	jwksURL := strings.TrimSpace(os.Getenv("WORKOS_JWKS_URL"))
	authKitDomain := strings.TrimSpace(os.Getenv("WORKOS_AUTHKIT_DOMAIN"))
	cacheKey := strings.Join([]string{clientID, jwksURL, authKitDomain}, "|")

	workOSVerifierMu.Lock()
	defer workOSVerifierMu.Unlock()

	if cacheKey == workOSVerifierKey && (workOSVerifierCache != nil || workOSVerifierErr != nil) {
		return workOSVerifierCache, workOSVerifierErr
	}

	verifier, err := workosauth.NewVerifier(workosauth.VerifierConfig{
		ClientID:      clientID,
		JWKSURL:       jwksURL,
		AuthKitDomain: authKitDomain,
	})
	workOSVerifierKey = cacheKey
	workOSVerifierCache = verifier
	workOSVerifierErr = err
	return verifier, err
}

func verifyWorkOSAccessToken(ctx context.Context, token string) (*workosauth.Claims, error) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_PROVIDER")), "workos") {
		return nil, status.Error(codes.Unimplemented, "workos auth disabled")
	}

	verifier, err := workOSVerifierFromEnv()
	if err != nil {
		if errors.Is(err, workosauth.ErrNotConfigured) {
			return nil, status.Error(codes.Internal, "workos auth is not configured")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to initialize WorkOS auth: %v", err))
	}

	claims, err := verifier.VerifyAccessToken(ctx, token)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid WorkOS access token")
	}
	return claims, nil
}
