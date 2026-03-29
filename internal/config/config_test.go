package config

import (
	"testing"
	"time"
)

func TestLoadConfigParsesPostgresPoolSettings(t *testing.T) {
	t.Setenv("DEPLOY_ENV", "staging")
	t.Setenv("STORAGE_TYPE", "postgres")
	t.Setenv("POSTGRES_DSN", "postgres://user:pass@localhost:5432/gitslice?sslmode=disable")
	t.Setenv("POSTGRES_MAX_CONNS", "25")
	t.Setenv("POSTGRES_MIN_CONNS", "3")
	t.Setenv("POSTGRES_MAX_CONN_LIFETIME", "45m")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.DeployEnv != "staging" {
		t.Fatalf("expected deploy env staging, got %q", cfg.DeployEnv)
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
}

func TestValidateRejectsRemotePostgresWithoutTLS(t *testing.T) {
	cfg := &Config{
		StorageType:             "postgres",
		PostgresDSN:             "postgres://user:pass@db.neon.tech:5432/gitslice?sslmode=disable",
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
		PostgresMaxConns:        2,
		PostgresMinConns:        3,
		PostgresMaxConnLifetime: 30 * time.Minute,
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected min/max validation failure")
	}
}
