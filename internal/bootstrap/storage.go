package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/api/option"
)

// InitStorage initializes a storage backend based on runtime configuration.
func InitStorage(ctx context.Context, cfg *config.Config) (storage.Storage, func(), error) {
	switch strings.ToLower(cfg.StorageType) {
	case "memory":
		return storage.NewInMemoryStorage(), func() {}, nil
	case "postgres":
		if cfg.PostgresDSN == "" {
			return nil, nil, fmt.Errorf("POSTGRES_DSN is required for STORAGE_TYPE=postgres")
		}
		objectStore, closeObjectStore, err := BuildObjectStore(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		st, err := storage.NewPostgresStorage(ctx, cfg.PostgresDSN, objectStore, "core")
		if err != nil {
			closeObjectStore()
			return nil, nil, err
		}
		return st, func() {
			_ = st.Close()
			closeObjectStore()
		}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported STORAGE_TYPE: %s", cfg.StorageType)
	}
}

// BuildObjectStore creates an object store backend used by Postgres storage.
func BuildObjectStore(ctx context.Context, cfg *config.Config) (storage.ObjectStore, func(), error) {
	switch strings.ToLower(cfg.ObjectStoreType) {
	case "filesystem", "file", "fs":
		if cfg.ObjectStoreDir == "" {
			return nil, nil, fmt.Errorf("OBJECT_STORE_DIR is required for OBJECT_STORE_TYPE=%s", cfg.ObjectStoreType)
		}
		if err := os.MkdirAll(cfg.ObjectStoreDir, 0755); err != nil {
			return nil, nil, err
		}
		return storage.NewFilesystemObjectStore(cfg.ObjectStoreDir), func() {}, nil
	case "gcs", "":
		if cfg.GCSBucket == "" {
			return nil, nil, fmt.Errorf("GCS_BUCKET is required for OBJECT_STORE_TYPE=gcs")
		}

		clientOpts := []option.ClientOption{}
		if cfg.GCSEndpoint != "" {
			clientOpts = append(clientOpts, option.WithEndpoint(cfg.GCSEndpoint))
		}
		if cfg.GCSDisableAuth {
			clientOpts = append(clientOpts, option.WithoutAuthentication())
		}
		if cfg.GCSCredentialsFile != "" {
			clientOpts = append(clientOpts, option.WithCredentialsFile(cfg.GCSCredentialsFile))
		}
		if cfg.GCSCredentialsJSON != "" {
			clientOpts = append(clientOpts, option.WithCredentialsJSON([]byte(cfg.GCSCredentialsJSON)))
		}

		client, err := gcsstorage.NewClient(ctx, clientOpts...)
		if err != nil {
			return nil, nil, err
		}

		return storage.NewGCSObjectStore(client, cfg.GCSBucket), func() { _ = client.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unsupported OBJECT_STORE_TYPE: %s", cfg.ObjectStoreType)
	}
}
