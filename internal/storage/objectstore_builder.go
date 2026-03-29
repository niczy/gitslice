package storage

import (
	"context"
	"fmt"
	"strings"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/niczy/gitslice/internal/config"
	"google.golang.org/api/option"
)

type ObjectStoreConfig struct {
	Type string

	FilesystemDir string

	GCSBucket          string
	GCSEndpoint        string
	GCSCredentialsFile string
	GCSCredentialsJSON string
	GCSDisableAuth     bool

	R2Bucket          string
	R2Prefix          string
	R2Endpoint        string
	R2Region          string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2UsePathStyle    bool
}

func ObjectStoreConfigFromAppConfig(cfg *config.Config) ObjectStoreConfig {
	if cfg == nil {
		return ObjectStoreConfig{}
	}
	return ObjectStoreConfig{
		Type:               cfg.ObjectStoreType,
		FilesystemDir:      cfg.ObjectStoreDir,
		GCSBucket:          cfg.GCSBucket,
		GCSEndpoint:        cfg.GCSEndpoint,
		GCSCredentialsFile: cfg.GCSCredentialsFile,
		GCSCredentialsJSON: cfg.GCSCredentialsJSON,
		GCSDisableAuth:     cfg.GCSDisableAuth,
		R2Bucket:           cfg.R2Bucket,
		R2Prefix:           cfg.R2Prefix,
		R2Endpoint:         cfg.R2Endpoint,
		R2Region:           cfg.R2Region,
		R2AccessKeyID:      cfg.R2AccessKeyID,
		R2SecretAccessKey:  cfg.R2SecretAccessKey,
		R2UsePathStyle:     cfg.R2UsePathStyle,
	}
}

func BuildObjectStore(ctx context.Context, cfg ObjectStoreConfig) (ObjectStore, func(), error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "filesystem", "fs", "file":
		store, err := NewFilesystemObjectStore(cfg.FilesystemDir)
		if err != nil {
			return nil, nil, err
		}
		return store, func() {}, nil
	case "", "gcs":
		if strings.TrimSpace(cfg.GCSBucket) == "" {
			return nil, nil, fmt.Errorf("GCS_BUCKET is required")
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
		return NewGCSObjectStore(client, cfg.GCSBucket), func() { _ = client.Close() }, nil
	case "r2":
		store, err := NewR2ObjectStore(R2ObjectStoreConfig{
			Bucket:          cfg.R2Bucket,
			Prefix:          cfg.R2Prefix,
			Endpoint:        cfg.R2Endpoint,
			Region:          cfg.R2Region,
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretAccessKey,
			UsePathStyle:    cfg.R2UsePathStyle,
		})
		if err != nil {
			return nil, nil, err
		}
		return store, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported OBJECT_STORE_TYPE: %s", cfg.Type)
	}
}
