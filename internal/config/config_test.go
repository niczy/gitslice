package config

import (
	"testing"
	"time"
)

func TestLoadConfigParsesPostgresPoolSettings(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "staging")
	t.Setenv("CORE_BIND_ADDR", "127.0.0.1")
	t.Setenv("AUTH_PROVIDER", "workos")
	t.Setenv("ALLOW_LEGACY_USER_AUTH", "1")
	t.Setenv("STORAGE_TYPE", "postgres")
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/gitslice?sslmode=disable")
	t.Setenv("POSTGRES_MAX_CONNS", "25")
	t.Setenv("POSTGRES_MIN_CONNS", "3")
	t.Setenv("POSTGRES_MAX_CONN_LIFETIME", "45m")
	t.Setenv("WORKOS_CLIENT_ID", "client_123")
	t.Setenv("WORKOS_API_KEY", "sk_test_123")
	t.Setenv("WORKOS_REDIRECT_URI", "https://agenttools.dev/auth/callback/workos")
	t.Setenv("WORKOS_JWKS_URL", "https://api.workos.com/sso/jwks/client_123")
	t.Setenv("WORKOS_COOKIE_PASSWORD", "cookie-secret")
	t.Setenv("WORKOS_AUTHKIT_DOMAIN", "auth.gitslice.io")
	t.Setenv("WORKOS_WEBHOOK_SECRET", "whsec_test_123")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_clerk_test_123")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.DeployEnv != "staging" {
		t.Fatalf("expected deploy env staging, got %q", cfg.DeployEnv)
	}
	if cfg.AuthProvider != "workos" {
		t.Fatalf("expected auth provider workos, got %q", cfg.AuthProvider)
	}
	if !cfg.AllowLegacyUserAuth {
		t.Fatalf("expected legacy user auth override to load")
	}
	if cfg.CoreBindAddr != "127.0.0.1" {
		t.Fatalf("expected core bind addr 127.0.0.1, got %q", cfg.CoreBindAddr)
	}
	if cfg.PostgresMaxConns != 25 {
		t.Fatalf("expected max conns 25, got %d", cfg.PostgresMaxConns)
	}
	if cfg.PostgresMinConns != 3 {
		t.Fatalf("expected min conns 3, got %d", cfg.PostgresMinConns)
	}
	if cfg.PostgresMaxConnLifetime != 45*time.Minute {
		t.Fatalf("expected max conn lifetime 45m, got %s", cfg.PostgresMaxConnLifetime)
	}
	if cfg.WorkOSClientID != "client_123" || cfg.WorkOSAPIKey != "sk_test_123" {
		t.Fatalf("expected WorkOS config to load, got client=%q api_key=%q", cfg.WorkOSClientID, cfg.WorkOSAPIKey)
	}
	if cfg.WorkOSRedirectURI != "https://agenttools.dev/auth/callback/workos" {
		t.Fatalf("unexpected WorkOS redirect uri: %q", cfg.WorkOSRedirectURI)
	}
	if cfg.WorkOSJWKSURL != "https://api.workos.com/sso/jwks/client_123" {
		t.Fatalf("unexpected WorkOS JWKS url: %q", cfg.WorkOSJWKSURL)
	}
	if cfg.WorkOSCookiePassword != "cookie-secret" {
		t.Fatalf("unexpected WorkOS cookie password: %q", cfg.WorkOSCookiePassword)
	}
	if cfg.WorkOSAuthKitDomain != "auth.gitslice.io" {
		t.Fatalf("unexpected WorkOS authkit domain: %q", cfg.WorkOSAuthKitDomain)
	}
	if cfg.WorkOSWebhookSecret != "whsec_test_123" {
		t.Fatalf("unexpected WorkOS webhook secret: %q", cfg.WorkOSWebhookSecret)
	}
	if cfg.ClerkWebhookSecret != "whsec_clerk_test_123" {
		t.Fatalf("unexpected Clerk webhook secret: %q", cfg.ClerkWebhookSecret)
	}
}

func TestCoreServiceAddrDefaultsToWildcard(t *testing.T) {
	cfg := &Config{CoreServicePort: "50051"}

	if got := cfg.GetCoreServiceAddr(); got != ":50051" {
		t.Fatalf("expected wildcard core addr, got %q", got)
	}
	if got := cfg.GetCoreServiceDialAddr(); got != "127.0.0.1:50051" {
		t.Fatalf("expected loopback dial addr, got %q", got)
	}
}

func TestCoreServiceAddrUsesExplicitBindAddr(t *testing.T) {
	cfg := &Config{CoreServicePort: "50052", CoreBindAddr: "127.0.0.1"}

	if got := cfg.GetCoreServiceAddr(); got != "127.0.0.1:50052" {
		t.Fatalf("expected explicit core addr, got %q", got)
	}
	if got := cfg.GetCoreServiceDialAddr(); got != "127.0.0.1:50052" {
		t.Fatalf("expected explicit dial addr, got %q", got)
	}
}

func TestValidateRejectsRemotePostgresWithoutTLS(t *testing.T) {
	cfg := &Config{
		StorageType:             "postgres",
		PostgresDSN:             "postgres://user:pass@db.neon.tech:5432/gitslice?sslmode=disable",
		ObjectStoreType:         "filesystem",
		ObjectStoreDir:          "/tmp/objectstore",
		PostgresMaxConns:        10,
		PostgresMinConns:        2,
		PostgresMaxConnLifetime: 30 * time.Minute,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected remote postgres validation failure")
	}
}

func TestValidateAllowsLocalPostgresWithoutTLS(t *testing.T) {
	cfg := &Config{
		StorageType:             "postgres",
		PostgresDSN:             "postgres://user:pass@127.0.0.1:5432/gitslice?sslmode=disable",
		ObjectStoreType:         "filesystem",
		ObjectStoreDir:          "/tmp/objectstore",
		PostgresMaxConns:        10,
		PostgresMinConns:        2,
		PostgresMaxConnLifetime: 30 * time.Minute,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected local postgres validation to pass, got %v", err)
	}
}

func TestValidateRejectsMinConnsAboveMax(t *testing.T) {
	cfg := &Config{
		StorageType:             "postgres",
		PostgresDSN:             "postgres://user:pass@127.0.0.1:5432/gitslice?sslmode=disable",
		ObjectStoreType:         "filesystem",
		ObjectStoreDir:          "/tmp/objectstore",
		PostgresMaxConns:        2,
		PostgresMinConns:        3,
		PostgresMaxConnLifetime: 30 * time.Minute,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected min/max validation failure")
	}
}

func TestValidateRequiresR2Fields(t *testing.T) {
	cfg := &Config{
		DeployEnv:       "production",
		StorageType:     "postgres",
		PostgresDSN:     "postgres://user:pass@db.example.com:5432/gitslice?sslmode=require",
		ObjectStoreType: "r2",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected missing R2 config validation failure")
	}
}

func TestValidateRejectsR2PrefixOutsideDeployEnv(t *testing.T) {
	cfg := &Config{
		DeployEnv:         "production",
		StorageType:       "postgres",
		PostgresDSN:       "postgres://user:pass@db.example.com:5432/gitslice?sslmode=require",
		ObjectStoreType:   "r2",
		R2Bucket:          "bucket",
		R2Prefix:          "staging",
		R2Endpoint:        "https://account.r2.cloudflarestorage.com",
		R2AccessKeyID:     "access",
		R2SecretAccessKey: "secret",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected mismatched R2 prefix validation failure")
	}
}

func TestValidateAllowsR2ConfigForMatchingEnv(t *testing.T) {
	cfg := &Config{
		DeployEnv:         "staging",
		StorageType:       "postgres",
		PostgresDSN:       "postgres://user:pass@db.example.com:5432/gitslice?sslmode=require",
		ObjectStoreType:   "r2",
		R2Bucket:          "bucket",
		R2Prefix:          "staging/core",
		R2Endpoint:        "https://account.r2.cloudflarestorage.com",
		R2AccessKeyID:     "access",
		R2SecretAccessKey: "secret",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected matching R2 config to validate, got %v", err)
	}
}

func TestValidateRejectsUnknownAuthProvider(t *testing.T) {
	cfg := &Config{AuthProvider: "mystery"}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected auth provider validation failure")
	}
}
