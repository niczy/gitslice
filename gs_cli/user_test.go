package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveAuthConfigPrefersAPIKeyFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GS_API_KEY", "env-token")

	authConfig, err := resolveAuthConfig("flag-token", "tester")
	if err != nil {
		t.Fatalf("resolveAuthConfig failed: %v", err)
	}
	if authConfig.Authorization != "Bearer flag-token" {
		t.Fatalf("unexpected authorization: %q", authConfig.Authorization)
	}
	if authConfig.Source != "--api-key" {
		t.Fatalf("unexpected source: %q", authConfig.Source)
	}
}

func TestResolveAuthConfigUsesCredentialsFileBeforeLegacyUsername(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".gitslice")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "credentials.json"), []byte("{\"access_token\":\"cred-token\",\"username\":\"cred-user\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile credentials failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "user"), []byte("legacy-user\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user failed: %v", err)
	}

	authConfig, err := resolveAuthConfig("", "")
	if err != nil {
		t.Fatalf("resolveAuthConfig failed: %v", err)
	}
	if authConfig.Authorization != "Bearer cred-token" {
		t.Fatalf("unexpected authorization: %q", authConfig.Authorization)
	}
	if authConfig.Username != "cred-user" {
		t.Fatalf("unexpected username: %q", authConfig.Username)
	}
	if authConfig.Source != "~/.gitslice/credentials.json" {
		t.Fatalf("unexpected source: %q", authConfig.Source)
	}
}

func TestResolveAuthConfigFallsBackToLegacyUsername(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".gitslice")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "user"), []byte("legacy-user\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user failed: %v", err)
	}

	authConfig, err := resolveAuthConfig("", "")
	if err != nil {
		t.Fatalf("resolveAuthConfig failed: %v", err)
	}
	if authConfig.Authorization != "User legacy-user" {
		t.Fatalf("unexpected authorization: %q", authConfig.Authorization)
	}
	if authConfig.Username != "legacy-user" {
		t.Fatalf("unexpected username: %q", authConfig.Username)
	}
	if authConfig.Source != "legacy username" {
		t.Fatalf("unexpected source: %q", authConfig.Source)
	}
}

func TestResolveAuthConfigRejectsMalformedCredentialsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".gitslice")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "credentials.json"), []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile credentials failed: %v", err)
	}

	if _, err := resolveAuthConfig("", ""); err == nil {
		t.Fatal("expected malformed credentials file error")
	}
}

func TestResolveAuthConfigAllowsRefreshOnlyCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configDir := filepath.Join(home, ".gitslice")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "credentials.json"), []byte("{\"refresh_token\":\"refresh-token\",\"username\":\"cred-user\"}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile credentials failed: %v", err)
	}

	authConfig, err := resolveAuthConfig("", "")
	if err != nil {
		t.Fatalf("resolveAuthConfig failed: %v", err)
	}
	if authConfig.Authorization != "" {
		t.Fatalf("expected no access token authorization, got %q", authConfig.Authorization)
	}
	if !authConfig.CredentialStore {
		t.Fatal("expected credential-backed auth config")
	}
}

func TestCredentialsConfigNeedsRefresh(t *testing.T) {
	now := time.Now()
	expiringSoon := now.Add(10 * time.Second).Format(time.RFC3339)
	expiringLater := now.Add(5 * time.Minute).Format(time.RFC3339)

	needsRefresh, err := (credentialsConfig{RefreshToken: "refresh-token"}).needsRefresh(now)
	if err != nil {
		t.Fatalf("needsRefresh returned error: %v", err)
	}
	if !needsRefresh {
		t.Fatal("expected refresh-only credentials to need refresh")
	}

	needsRefresh, err = (credentialsConfig{
		AccessToken:          "access-token",
		RefreshToken:         "refresh-token",
		AccessTokenExpiresAt: expiringSoon,
	}).needsRefresh(now)
	if err != nil {
		t.Fatalf("needsRefresh expiringSoon returned error: %v", err)
	}
	if !needsRefresh {
		t.Fatal("expected soon-expiring token to need refresh")
	}

	needsRefresh, err = (credentialsConfig{
		AccessToken:          "access-token",
		RefreshToken:         "refresh-token",
		AccessTokenExpiresAt: expiringLater,
	}).needsRefresh(now)
	if err != nil {
		t.Fatalf("needsRefresh expiringLater returned error: %v", err)
	}
	if needsRefresh {
		t.Fatal("expected later-expiring token to remain usable")
	}
}
