package main

import (
	"fmt"
	"strings"

	"github.com/niczy/gitslice/internal/common"
)

func normalizeSliceID(input string) (string, error) {
	sliceID := strings.TrimSpace(input)
	if sliceID == "" {
		return "", fmt.Errorf("slice ID is required")
	}
	if err := common.ValidateSliceID(sliceID); err != nil {
		return "", fmt.Errorf("invalid slice ID %q: %w", sliceID, err)
	}
	return sliceID, nil
}

func sliceIDFromConfig() (string, error) {
	sliceID, err := readSliceIDFromConfig()
	if err != nil {
		return "", err
	}
	return normalizeSliceID(sliceID)
}
