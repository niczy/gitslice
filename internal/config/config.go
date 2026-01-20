package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all configuration values for gitslice services.
type Config struct {
	// Service ports
	SliceServicePort  string
	AdminServicePort  string
	GatewayPort       string

	// Storage type (memory, redis)
	StorageType       string

	// Redis configuration (if storage type is redis)
	RedisAddr         string
	RedisPassword     string
	RedisDB           int

	// Object store configuration
	S3Endpoint        string
	S3AccessKeyID     string
	S3SecretAccessKey string
	S3Bucket          string
	S3Region          string
}

// LoadConfig loads configuration from environment variables with defaults.
func LoadConfig() *Config {
	return &Config{
		SliceServicePort:  getEnv("SLICE_SERVICE_PORT", "50051"),
		AdminServicePort:  getEnv("ADMIN_SERVICE_PORT", "50052"),
		GatewayPort:       getEnv("GATEWAY_PORT", "8080"),
		StorageType:       getEnv("STORAGE_TYPE", "memory"),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		RedisDB:           getEnvInt("REDIS_DB", 0),
		S3Endpoint:        getEnv("S3_ENDPOINT", ""),
		S3AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", ""),
		S3SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", ""),
		S3Bucket:          getEnv("S3_BUCKET", "gitslice-objects"),
		S3Region:          getEnv("S3_REGION", "us-east-1"),
	}
}

// GetSliceServiceAddr returns the full address for the slice service.
func (c *Config) GetSliceServiceAddr() string {
	return fmt.Sprintf(":%s", c.SliceServicePort)
}

// GetAdminServiceAddr returns the full address for the admin service.
func (c *Config) GetAdminServiceAddr() string {
	return fmt.Sprintf(":%s", c.AdminServicePort)
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

// getEnvInt retrieves an integer environment variable or returns a default value.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
