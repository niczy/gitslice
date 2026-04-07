package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds all configuration values for gitslice services.
type Config struct {
	// Service ports. CoreServicePort is the single ingress for both gRPC and HTTP.
	CoreServicePort string
	// CoreBindAddr optionally constrains the core listener to a specific local interface.
	CoreBindAddr string
	// GatewayPort is a deprecated legacy setting kept for compatibility with older env files.
	GatewayPort string
	// DeployEnv identifies the target environment for logging and validation.
	DeployEnv string

	// Auth provider controls the human auth integration mode.
	AuthProvider string

	// Storage type (memory, postgres, postgres_native)
	StorageType string

	// Postgres configuration (if storage type is postgres or postgres_native)
	PostgresDSN             string
	PostgresMaxConns        int32
	PostgresMinConns        int32
	PostgresMaxConnLifetime time.Duration

	// Object store configuration (filesystem, GCS)
	//
	// For STORAGE_TYPE=postgres we persist metadata/state in Postgres and store blob
	// payloads (file content) in an object store.
	//
	// OBJECT_STORE_TYPE may be:
	// - "gcs" (default)
	// - "filesystem" (stores objects under OBJECT_STORE_DIR)
	// - "r2" (stores objects in Cloudflare R2 via the S3-compatible API)
	ObjectStoreType string
	ObjectStoreDir  string

	// GCS object store configuration (when OBJECT_STORE_TYPE=gcs)
	GCSBucket          string
	GCSEndpoint        string
	GCSCredentialsFile string
	GCSCredentialsJSON string
	GCSDisableAuth     bool

	// R2 object store configuration (when OBJECT_STORE_TYPE=r2)
	R2Bucket          string
	R2Prefix          string
	R2Endpoint        string
	R2Region          string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2UsePathStyle    bool

	// Agent session WebSocket token signing.
	AgentWSTokenSecret string

	// WorkOS auth configuration.
	WorkOSClientID       string
	WorkOSAPIKey         string
	WorkOSRedirectURI    string
	WorkOSJWKSURL        string
	WorkOSCookiePassword string
	WorkOSAuthKitDomain  string
	WorkOSWebhookSecret  string

	// E2B runtime provider settings for agent sessions.
	E2BAPIURL                string
	E2BDomain                string
	E2BAPIKey                string
	E2BAccessToken           string
	CodexAPIKey              string
	ClaudeAPIKey             string
	AgentEgressAllowlist     string
	AgentEgressDenyByDefault bool
	E2BRuntimeWSPort         int
	E2BRuntimeWSPath         string
	E2BRequestTimeoutSec     int

	// Cloudflare Containers runtime control-plane settings.
	AgentRuntimeProviderDefault string
	CFCControlBaseURL           string
	CFCControlAudience          string
	CFCServiceTokenID           string
	CFCServiceTokenSecret       string
	CFCRequestTimeoutSec        int
}

// LoadConfig loads configuration from environment variables with defaults.
func LoadConfig() (*Config, error) {
	corePort := getEnv("CORE_SERVICE_PORT", "")
	if corePort == "" {
		if value, ok := os.LookupEnv("SLICE_SERVICE_PORT"); ok && value != "" {
			corePort = value
		} else if value, ok := os.LookupEnv("ADMIN_SERVICE_PORT"); ok && value != "" {
			corePort = value
		} else {
			corePort = "50051"
		}
	}
	postgresMaxConns, err := getEnvOptionalInt32("POSTGRES_MAX_CONNS")
	if err != nil {
		return nil, err
	}
	postgresMinConns, err := getEnvOptionalInt32("POSTGRES_MIN_CONNS")
	if err != nil {
		return nil, err
	}
	postgresMaxConnLifetime, err := getEnvOptionalDuration("POSTGRES_MAX_CONN_LIFETIME")
	if err != nil {
		return nil, err
	}
	return &Config{
		CoreServicePort:         corePort,
		CoreBindAddr:            getEnv("CORE_BIND_ADDR", ""),
		GatewayPort:             getEnv("GATEWAY_PORT", corePort),
		DeployEnv:               getEnv("DEPLOY_ENV", ""),
		AuthProvider:            getEnv("AUTH_PROVIDER", "local"),
		StorageType:             getEnv("STORAGE_TYPE", "memory"),
		PostgresDSN:             getEnv("POSTGRES_DSN", ""),
		PostgresMaxConns:        postgresMaxConns,
		PostgresMinConns:        postgresMinConns,
		PostgresMaxConnLifetime: postgresMaxConnLifetime,
		ObjectStoreType:         getEnv("OBJECT_STORE_TYPE", "gcs"),
		ObjectStoreDir:          getEnv("OBJECT_STORE_DIR", ""),
		GCSBucket:               getEnv("GCS_BUCKET", "gitslice-objects"),
		GCSEndpoint:             getEnv("GCS_ENDPOINT", ""),
		GCSCredentialsFile:      getEnv("GCS_CREDENTIALS_FILE", ""),
		GCSCredentialsJSON:      getEnv("GCS_CREDENTIALS_JSON", ""),
		GCSDisableAuth:          getEnvBool("GCS_DISABLE_AUTH", false),
		R2Bucket:                getEnv("R2_BUCKET", ""),
		R2Prefix:                getEnv("R2_PREFIX", ""),
		R2Endpoint:              getEnv("R2_ENDPOINT", ""),
		R2Region:                getEnv("R2_REGION", "auto"),
		R2AccessKeyID:           getEnv("R2_ACCESS_KEY_ID", ""),
		R2SecretAccessKey:       getEnv("R2_SECRET_ACCESS_KEY", ""),
		R2UsePathStyle:          getEnvBool("R2_USE_PATH_STYLE", false),
		AgentWSTokenSecret:      getEnv("AGENT_WS_TOKEN_SECRET", "dev-insecure-agent-secret"),
		WorkOSClientID:          getEnv("WORKOS_CLIENT_ID", ""),
		WorkOSAPIKey:            getEnv("WORKOS_API_KEY", ""),
		WorkOSRedirectURI:       getEnv("WORKOS_REDIRECT_URI", ""),
		WorkOSJWKSURL:           getEnv("WORKOS_JWKS_URL", ""),
		WorkOSCookiePassword:    getEnv("WORKOS_COOKIE_PASSWORD", ""),
		WorkOSAuthKitDomain:     getEnv("WORKOS_AUTHKIT_DOMAIN", ""),
		WorkOSWebhookSecret:     getEnv("WORKOS_WEBHOOK_SECRET", ""),
		E2BAPIURL:               getEnv("E2B_API_URL", ""),
		E2BDomain:               getEnv("E2B_DOMAIN", "e2b.app"),
		E2BAPIKey:               getEnv("E2B_API_KEY", ""),
		E2BAccessToken:          getEnv("E2B_ACCESS_TOKEN", ""),
		CodexAPIKey:             getEnv("OPENAI_API_KEY", ""),
		ClaudeAPIKey:            getEnv("ANTHROPIC_API_KEY", ""),
		AgentEgressAllowlist: getEnv(
			"AGENT_EGRESS_ALLOWLIST",
			"",
		),
		AgentEgressDenyByDefault: getEnvBool("AGENT_EGRESS_DENY_BY_DEFAULT", false),
		E2BRuntimeWSPort:         getEnvInt("E2B_RUNTIME_WS_PORT", 9000),
		E2BRuntimeWSPath:         getEnv("E2B_RUNTIME_WS_PATH", "/ws"),
		E2BRequestTimeoutSec: getEnvInt(
			"E2B_REQUEST_TIMEOUT_SEC",
			30,
		),
		AgentRuntimeProviderDefault: getEnv("AGENT_RUNTIME_PROVIDER_DEFAULT", ""),
		CFCControlBaseURL:           getEnv("CFC_CONTROL_BASE_URL", ""),
		CFCControlAudience:          getEnv("CFC_CONTROL_AUDIENCE", ""),
		CFCServiceTokenID:           getEnv("CFC_SERVICE_TOKEN_ID", ""),
		CFCServiceTokenSecret:       getEnv("CFC_SERVICE_TOKEN_SECRET", ""),
		CFCRequestTimeoutSec:        getEnvInt("CFC_REQUEST_TIMEOUT_SEC", 30),
	}, nil
}

type PostgresTargetSummary struct {
	Host     string
	Database string
	Remote   bool
	UsesTLS  bool
}

func (c *Config) Validate() error {
	switch strings.ToLower(strings.TrimSpace(c.AuthProvider)) {
	case "", "local", "workos":
	default:
		return fmt.Errorf("AUTH_PROVIDER must be one of: local, workos")
	}
	if !strings.EqualFold(c.StorageType, "postgres") {
		return nil
	}
	if strings.TrimSpace(c.PostgresDSN) == "" {
		return fmt.Errorf("POSTGRES_DSN is required for STORAGE_TYPE=postgres")
	}
	if err := c.validateObjectStore(); err != nil {
		return err
	}
	if c.PostgresMaxConns < 0 {
		return fmt.Errorf("POSTGRES_MAX_CONNS must be >= 0")
	}
	if c.PostgresMinConns < 0 {
		return fmt.Errorf("POSTGRES_MIN_CONNS must be >= 0")
	}
	if c.PostgresMaxConns > 0 && c.PostgresMinConns > c.PostgresMaxConns {
		return fmt.Errorf("POSTGRES_MIN_CONNS must be <= POSTGRES_MAX_CONNS")
	}
	if c.PostgresMaxConnLifetime < 0 {
		return fmt.Errorf("POSTGRES_MAX_CONN_LIFETIME must be >= 0")
	}
	summary, err := c.PostgresTargetSummary()
	if err != nil {
		return fmt.Errorf("parse POSTGRES_DSN: %w", err)
	}
	if summary.Remote && !summary.UsesTLS {
		return fmt.Errorf("remote POSTGRES_DSN must enable TLS")
	}
	return nil
}

func (c *Config) PostgresTargetSummary() (*PostgresTargetSummary, error) {
	if strings.TrimSpace(c.PostgresDSN) == "" {
		return nil, nil
	}
	poolCfg, err := pgxpool.ParseConfig(c.PostgresDSN)
	if err != nil {
		return nil, err
	}
	host := strings.TrimSpace(poolCfg.ConnConfig.Host)
	return &PostgresTargetSummary{
		Host:     host,
		Database: strings.TrimSpace(poolCfg.ConnConfig.Database),
		Remote:   !isLocalPostgresHost(host),
		UsesTLS:  poolCfg.ConnConfig.TLSConfig != nil,
	}, nil
}

func (c *Config) validateObjectStore() error {
	switch strings.ToLower(strings.TrimSpace(c.ObjectStoreType)) {
	case "filesystem", "fs", "file":
		if strings.TrimSpace(c.ObjectStoreDir) == "" {
			return fmt.Errorf("OBJECT_STORE_DIR is required for OBJECT_STORE_TYPE=filesystem")
		}
		return nil
	case "", "gcs":
		if strings.TrimSpace(c.GCSBucket) == "" {
			return fmt.Errorf("GCS_BUCKET is required for OBJECT_STORE_TYPE=gcs")
		}
		return nil
	case "r2":
		if strings.TrimSpace(c.R2Bucket) == "" {
			return fmt.Errorf("R2_BUCKET is required for OBJECT_STORE_TYPE=r2")
		}
		if strings.TrimSpace(c.R2Prefix) == "" {
			return fmt.Errorf("R2_PREFIX is required for OBJECT_STORE_TYPE=r2")
		}
		if strings.TrimSpace(c.R2Endpoint) == "" {
			return fmt.Errorf("R2_ENDPOINT is required for OBJECT_STORE_TYPE=r2")
		}
		if strings.TrimSpace(c.R2AccessKeyID) == "" {
			return fmt.Errorf("R2_ACCESS_KEY_ID is required for OBJECT_STORE_TYPE=r2")
		}
		if strings.TrimSpace(c.R2SecretAccessKey) == "" {
			return fmt.Errorf("R2_SECRET_ACCESS_KEY is required for OBJECT_STORE_TYPE=r2")
		}
		deployEnv := strings.ToLower(strings.TrimSpace(c.DeployEnv))
		if deployEnv != "" {
			prefix := strings.Trim(strings.TrimSpace(c.R2Prefix), "/")
			if prefix != deployEnv && !strings.HasPrefix(prefix, deployEnv+"/") {
				return fmt.Errorf("R2_PREFIX must match DEPLOY_ENV namespace")
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported OBJECT_STORE_TYPE: %s", c.ObjectStoreType)
	}
}

// GetCoreServiceAddr returns the full address for the core gRPC server.
func (c *Config) GetCoreServiceAddr() string {
	if bindAddr := strings.TrimSpace(c.CoreBindAddr); bindAddr != "" {
		return net.JoinHostPort(bindAddr, c.CoreServicePort)
	}
	return fmt.Sprintf(":%s", c.CoreServicePort)
}

// GetCoreServiceDialAddr returns the loopback-safe address used by internal clients.
func (c *Config) GetCoreServiceDialAddr() string {
	bindAddr := strings.TrimSpace(c.CoreBindAddr)
	switch bindAddr {
	case "", "0.0.0.0", "::":
		return net.JoinHostPort("127.0.0.1", c.CoreServicePort)
	default:
		return net.JoinHostPort(bindAddr, c.CoreServicePort)
	}
}

// GetSliceServiceAddr returns the full address for the slice service (legacy alias).
func (c *Config) GetSliceServiceAddr() string {
	return c.GetCoreServiceAddr()
}

// GetAdminServiceAddr returns the full address for the admin service (legacy alias).
func (c *Config) GetAdminServiceAddr() string {
	return c.GetCoreServiceAddr()
}

// GetGatewayAddr returns the full address for the HTTP gateway.
// The gateway is served on the same port as the gRPC core service.
func (c *Config) GetGatewayAddr() string {
	return c.GetCoreServiceAddr()
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func getEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

func getEnvOptionalInt32(key string) (int32, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return int32(parsed), nil
}

func getEnvOptionalDuration(key string) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}

func isLocalPostgresHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	default:
		return strings.HasPrefix(host, "/")
	}
}
