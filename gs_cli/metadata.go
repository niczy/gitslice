package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type sliceMetadata struct {
	SliceID string `toml:"slice_id"`
}

func readSliceMetadata(path string) (*sliceMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var meta sliceMetadata
	if err := toml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("failed to parse metadata TOML: %w", err)
	}

	meta.SliceID = strings.TrimSpace(meta.SliceID)
	if meta.SliceID == "" {
		return nil, fmt.Errorf("metadata file %s is missing slice_id", path)
	}

	return &meta, nil
}

func validateMetadataPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("metadata path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("metadata path must be absolute: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

func sliceIDFromConfig() (string, error) {
	metadataPath, err := readSliceMetadataPathFromConfig()
	if err != nil {
		return "", err
	}
	meta, err := readSliceMetadata(metadataPath)
	if err != nil {
		return "", err
	}
	return meta.SliceID, nil
}
