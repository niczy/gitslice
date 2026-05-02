package auth

import (
	"encoding/base64"
	"testing"
)

func TestUsernameFromAuthorizationHeaderAllowsLegacyUserAuthByDefault(t *testing.T) {
	if got := UsernameFromAuthorizationHeader("User alice"); got != "alice" {
		t.Fatalf("expected default legacy user auth to work, got %q", got)
	}
}

func TestUsernameFromAuthorizationHeaderRejectsLegacyUserAuthInProduction(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "production")

	if got := UsernameFromAuthorizationHeader("User alice"); got != "" {
		t.Fatalf("expected production legacy user auth to be disabled, got %q", got)
	}
}

func TestUsernameFromAuthorizationHeaderAllowsExplicitProductionLegacyUserAuth(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "production")
	t.Setenv("ALLOW_LEGACY_USER_AUTH", "1")

	if got := UsernameFromAuthorizationHeader("User alice"); got != "alice" {
		t.Fatalf("expected explicit legacy user auth override, got %q", got)
	}
}

func TestUsernameFromAuthorizationHeaderAllowsProductionDevLoginOverride(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "production")
	t.Setenv("ALLOW_DEV_LOGIN", "true")

	if got := UsernameFromAuthorizationHeader("User alice"); got != "alice" {
		t.Fatalf("expected dev login override to allow legacy user auth, got %q", got)
	}
}

func TestTokenFromBasicAuthorizationHeaderUsesPassword(t *testing.T) {
	header := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:token-123"))

	if got := TokenFromBasicAuthorizationHeader(header); got != "token-123" {
		t.Fatalf("expected Basic password token, got %q", got)
	}
}

func TestTokenFromBasicAuthorizationHeaderRejectsInvalidPayload(t *testing.T) {
	if got := TokenFromBasicAuthorizationHeader("Basic not-base64"); got != "" {
		t.Fatalf("expected invalid Basic payload to be ignored, got %q", got)
	}
}
