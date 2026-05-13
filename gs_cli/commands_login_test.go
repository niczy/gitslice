package gscli

import (
	"context"
	"errors"
	"testing"
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
