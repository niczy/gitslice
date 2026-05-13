package gscli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	accountv1 "github.com/niczy/gitslice/proto/account"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLoginStartsFreshDeviceFlowWhenStoredRefreshFails(t *testing.T) {
	originalEnsure := ensureLoginAuthReady
	originalStart := startLoginDevice
	originalJSON := cliStructuredJSON
	originalNonInteractive := cliNonInteractive
	defer func() {
		ensureLoginAuthReady = originalEnsure
		startLoginDevice = originalStart
		cliStructuredJSON = originalJSON
		cliNonInteractive = originalNonInteractive
	}()

	cliStructuredJSON = false
	cliNonInteractive = false
	ensureLoginAuthReady = func(context.Context, *CLI, cliAuth) (cliAuth, error) {
		return cliAuth{}, errors.New("invalid refresh token")
	}
	startedFreshLogin := false
	startLoginDevice = func(context.Context, *CLI) {
		startedFreshLogin = true
	}

	handleLogin(context.Background(), &CLI{}, cliAuth{
		Authorization:   "Bearer stale",
		Username:        "nicholas",
		Source:          "~/.gitslice/credentials.json",
		CredentialStore: true,
	}, nil)

	if !startedFreshLogin {
		t.Fatal("expected stale stored credentials to fall through to a fresh device login")
	}
}

func TestEnsureLoginAuthAcceptedValidatesStoredTokenWithEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := writeCredentialsConfig(credentialsConfig{
		AccessToken:          "stored-access",
		AccessTokenExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
		Username:             "nicholas",
	}); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	accountClient := &fakeAccountServiceClient{
		authContext: &accountv1.GetAuthContextResponse{
			Authenticated: true,
			Username:      "nicholas",
			AuthSource:    "local_session",
		},
	}

	authConfig, err := ensureLoginAuthAccepted(context.Background(), &CLI{accountClient: accountClient}, cliAuth{
		Authorization:   "Bearer stored-access",
		Username:        "nicholas",
		Source:          "~/.gitslice/credentials.json",
		CredentialStore: true,
	})
	if err != nil {
		t.Fatalf("ensure login auth accepted failed: %v", err)
	}
	if !accountClient.authContextSeen {
		t.Fatal("expected stored token to be validated against the endpoint")
	}
	if authConfig.Authorization != "Bearer stored-access" {
		t.Fatalf("unexpected authorization: %q", authConfig.Authorization)
	}
}

func TestEnsureLoginAuthAcceptedRejectsInvalidStoredToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := writeCredentialsConfig(credentialsConfig{
		AccessToken:          "stored-access",
		AccessTokenExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
		Username:             "nicholas",
	}); err != nil {
		t.Fatalf("write credentials: %v", err)
	}

	_, err := ensureLoginAuthAccepted(context.Background(), &CLI{accountClient: &fakeAccountServiceClient{
		authContextErr: status.Error(codes.Unauthenticated, "invalid session token"),
	}}, cliAuth{
		Authorization:   "Bearer stored-access",
		Username:        "nicholas",
		Source:          "~/.gitslice/credentials.json",
		CredentialStore: true,
	})
	if err == nil || !strings.Contains(err.Error(), "validate stored login") {
		t.Fatalf("expected stored login validation error, got %v", err)
	}
}
