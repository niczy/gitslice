package common

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateFilePath validates that a file path is safe and doesn't contain
// directory traversal attempts or other malicious patterns.
func ValidateFilePath(path string) error {
	if path == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Clean the path to resolve . and .. elements
	cleaned := filepath.Clean(path)

	// Check for directory traversal attempts
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("file path contains directory traversal: %s", path)
	}

	// Path should not start with / (must be relative)
	if filepath.IsAbs(cleaned) {
		return fmt.Errorf("file path must be relative, not absolute: %s", path)
	}

	// Check for null bytes (directory traversal technique)
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("file path contains null byte: %s", path)
	}

	for _, segment := range strings.Split(filepath.ToSlash(cleaned), "/") {
		switch strings.TrimSpace(segment) {
		case "", ".", "..":
			return fmt.Errorf("file path contains invalid segment: %s", path)
		case "~":
			return fmt.Errorf("file path contains home directory segment: %s", path)
		}
	}

	return nil
}

// ValidateFileID validates a file ID, which should follow similar rules to file paths.
func ValidateFileID(fileID string) error {
	if fileID == "" {
		return fmt.Errorf("file ID cannot be empty")
	}

	// File IDs should be valid file paths
	return ValidateFilePath(fileID)
}

// ValidateSliceID validates a slice ID to ensure it contains only safe characters.
func ValidateSliceID(sliceID string) error {
	if sliceID == "" {
		return fmt.Errorf("slice ID cannot be empty")
	}

	// Slice IDs should be alphanumeric with hyphens, underscores, and forward slashes
	for _, r := range sliceID {
		if !isValidSliceIDChar(r) {
			return fmt.Errorf("slice ID contains invalid character: %c", r)
		}
	}

	// Check length constraints
	if len(sliceID) > 256 {
		return fmt.Errorf("slice ID too long (max 256 characters): %d", len(sliceID))
	}

	return nil
}

// isValidSliceIDChar checks if a rune is valid for a slice ID.
func isValidSliceIDChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' || r == '_' || r == '/' || r == '.'
}
