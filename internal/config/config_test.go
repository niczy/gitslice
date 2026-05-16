package config

import (
	"testing"
	"time"
)

func TestLoadConfigParsesPostgresPoolSettings(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "staging")
	t.Setenv("CORE_BIND_ADDR", "127.0.0.1")
	t.Setenv("AUTH_PROVIDER", "clerk")
	t.Setenv("ALLOW_LEGACY_USER_AUTH", "1")
	t.Setenv("STORAGE_TYPE", "postgres")
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/gitslice?sslmode=disable")
	t.Setenv("POSTGRES_MAX_CONNS", "25")
	t.Setenv("POSTGRES_MIN_CONNS", "3")
	t.Setenv("POSTGRES_MAX_CONN_LIFETIME", "45m")
	t.Setenv("POSTGRES_PROJECTION_MAX_CONNS", "5")
	t.Setenv("MERGE_EVENT_PROJECTION_ENABLED", "true")
	t.Setenv("MERGE_EVENT_PROJECTION_WORKERS", "3")
	t.Setenv("MERGE_EVENT_PROJECTION_BATCH_SIZE", "128")
	t.Setenv("MERGE_EVENT_PROJECTION_SHARDS", "64")
	t.Setenv("MERGE_EVENT_PROJECTION_POLL_INTERVAL", "2s")
	t.Setenv("CLERK_WEBHOOK_SECRET", "whsec_clerk_test_123")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.DeployEnv != "staging" {
		t.Fatalf("expected deploy env staging, got %q", cfg.DeployEnv)
	}
	if cfg.AuthProvider != "clerk" {
		t.Fatalf("expected auth provider clerk, got %q", cfg.AuthProvider)
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
	if cfg.PostgresProjectionMaxConns != 5 {
		t.Fatalf("expected projection max conns 5, got %d", cfg.PostgresProjectionMaxConns)
	}
	if !cfg.MergeEventProjectionEnabled {
		t.Fatalf("expected merge event projection flag to load")
	}
	if cfg.MergeEventProjectionWorkers != 3 {
		t.Fatalf("expected merge event projection workers 3, got %d", cfg.MergeEventProjectionWorkers)
	}
	if cfg.MergeEventProjectionBatchSize != 128 {
		t.Fatalf("expected merge event projection batch size 128, got %d", cfg.MergeEventProjectionBatchSize)
	}
	if cfg.MergeEventProjectionShardCount != 64 {
		t.Fatalf("expected merge event projection shards 64, got %d", cfg.MergeEventProjectionShardCount)
	}
	if cfg.MergeEventProjectionPollInterval != 2*time.Second {
		t.Fatalf("expected merge event projection poll interval 2s, got %s", cfg.MergeEventProjectionPollInterval)
	}
	if cfg.ClerkWebhookSecret != "whsec_clerk_test_123" {
		t.Fatalf("unexpected Clerk webhook secret: %q", cfg.ClerkWebhookSecret)
	}
}

func TestLoadConfigEnablesMergeEventProjectionByDefault(t *testing.T) {
	t.Setenv("MERGE_EVENT_PROJECTION_ENABLED", "")
	t.Setenv("MERGE_EVENT_PROJECTION_WORKERS", "")
	t.Setenv("MERGE_EVENT_PROJECTION_BATCH_SIZE", "")
	t.Setenv("MERGE_EVENT_PROJECTION_SHARDS", "")
	t.Setenv("MERGE_EVENT_PROJECTION_POLL_INTERVAL", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !cfg.MergeEventProjectionEnabled {
		t.Fatalf("expected merge event projection to default to enabled")
	}
	if cfg.MergeEventProjectionWorkers != 1 {
		t.Fatalf("expected default merge event projection workers 1, got %d", cfg.MergeEventProjectionWorkers)
	}
	if cfg.MergeEventProjectionBatchSize != 256 {
		t.Fatalf("expected default merge event projection batch size 256, got %d", cfg.MergeEventProjectionBatchSize)
	}
	if cfg.MergeEventProjectionShardCount != 1024 {
		t.Fatalf("expected default merge event projection shards 1024, got %d", cfg.MergeEventProjectionShardCount)
	}
	if cfg.MergeEventProjectionPollInterval != 250*time.Millisecond {
		t.Fatalf("expected default merge event projection poll interval 250ms, got %s", cfg.MergeEventProjectionPollInterval)
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

func TestValidateRejectsDurableProjectionWithSingleProjectionConnection(t *testing.T) {
	cfg := &Config{
		StorageType:                      "postgres",
		PostgresDSN:                      "postgres://user:pass@127.0.0.1:5432/gitslice?sslmode=disable",
		ObjectStoreType:                  "filesystem",
		ObjectStoreDir:                   "/tmp/objectstore",
		PostgresMaxConns:                 10,
		PostgresProjectionMaxConns:       1,
		MergeEventProjectionEnabled:      true,
		MergeEventProjectionWorkers:      1,
		MergeEventProjectionBatchSize:    64,
		MergeEventProjectionShardCount:   1024,
		MergeEventProjectionPollInterval: time.Second,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected durable projection single-connection validation failure")
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
