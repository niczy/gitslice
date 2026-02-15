package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration values for gitslice services.
type Config struct {
	// Service ports
	CoreServicePort string
	GatewayPort     string

	// Storage type (memory, postgres, postgres_native)
	StorageType string

	// PostgresStorageMode controls the migration path from snapshot to native storage.
	// Values: "snapshot" (default), "native"
	PostgresStorageMode string

	// Postgres configuration (if storage type is postgres or postgres_native)
	PostgresDSN string

	// Object store configuration (filesystem, GCS)
	//
	// For STORAGE_TYPE=postgres we persist metadata/state in Postgres and store blob
	// payloads (file content) in an object store.
	//
	// OBJECT_STORE_TYPE may be:
	// - "gcs" (default)
	// - "filesystem" (stores objects under OBJECT_STORE_DIR)
	ObjectStoreType string
	ObjectStoreDir  string

	// GCS object store configuration (when OBJECT_STORE_TYPE=gcs)
	GCSBucket          string
	GCSEndpoint        string
	GCSCredentialsFile string
	GCSCredentialsJSON string
	GCSDisableAuth     bool
}

// LoadConfig loads configuration from environment variables with defaults.
func LoadConfig() *Config {
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
	return &Config{
		CoreServicePort:     corePort,
		GatewayPort:         getEnv("GATEWAY_PORT", "8080"),
		StorageType:         getEnv("STORAGE_TYPE", "memory"),
		PostgresStorageMode: getEnv("POSTGRES_STORAGE_MODE", "snapshot"),
		PostgresDSN:         getEnv("POSTGRES_DSN", ""),
		ObjectStoreType:    getEnv("OBJECT_STORE_TYPE", "gcs"),
		ObjectStoreDir:     getEnv("OBJECT_STORE_DIR", ""),
		GCSBucket:          getEnv("GCS_BUCKET", "gitslice-objects"),
		GCSEndpoint:        getEnv("GCS_ENDPOINT", ""),
		GCSCredentialsFile: getEnv("GCS_CREDENTIALS_FILE", ""),
		GCSCredentialsJSON: getEnv("GCS_CREDENTIALS_JSON", ""),
		GCSDisableAuth:     getEnvBool("GCS_DISABLE_AUTH", false),
	}
}

// GetCoreServiceAddr returns the full address for the core gRPC server.
func (c *Config) GetCoreServiceAddr() string {
	return fmt.Sprintf(":%s", c.CoreServicePort)
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
func (c *Config) GetGatewayAddr() string {
	return fmt.Sprintf(":%s", c.GatewayPort)
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
