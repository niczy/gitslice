package authresolver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/niczy/gitslice/internal/storage"
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

func resolveWorkOSIdentity(ctx context.Context, st storage.Storage, token string) (*Identity, error) {
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

	user, err := st.GetUserByWorkOSUserID(ctx, claims.Subject)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil, status.Error(codes.Unauthenticated, "WorkOS user is not linked")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve linked WorkOS user: %v", err))
	}

	return &Identity{
		Username:       user.Username,
		SessionID:      strings.TrimSpace(claims.SessionID),
		AuthSource:     "workos",
		WorkOSUserID:   claims.Subject,
		OrganizationID: strings.TrimSpace(claims.OrganizationID),
	}, nil
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
