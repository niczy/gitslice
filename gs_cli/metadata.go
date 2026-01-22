package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niczy/gitslice/internal/common"
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

	// Validate slice ID to prevent injection attacks
	if err := common.ValidateSliceID(meta.SliceID); err != nil {
		return nil, fmt.Errorf("invalid slice_id in metadata file %s: %w", path, err)
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

	// Check if file exists and is accessible
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("metadata file not accessible: %w", err)
	}

	// Prevent symlink attacks - metadata file must be a regular file
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("metadata path cannot be a symlink: %s", path)
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("metadata path must be a regular file: %s", path)
	}

	// Resolve to clean absolute path to prevent path traversal
	cleanPath := filepath.Clean(path)

	return cleanPath, nil
}

func sliceIDFromConfig() (string, error) {
	metadataPath, err := readSliceMetadataPathFromConfig()
	if err != nil {
		return "", err
	}

	// Re-validate metadata path to prevent TOCTOU attacks
	validPath, err := validateMetadataPath(metadataPath)
	if err != nil {
		return "", fmt.Errorf("metadata path from config is invalid: %w", err)
	}

	meta, err := readSliceMetadata(validPath)
	if err != nil {
		return "", err
	}
	return meta.SliceID, nil
}
