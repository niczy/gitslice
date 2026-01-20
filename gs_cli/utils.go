package main

import (
	"os"
	"strings"
	"time"
)

// readSliceIDFromConfig reads the slice ID from the .gs/config file.
func readSliceIDFromConfig() (string, error) {
	data, err := os.ReadFile(".gs/config")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// writeConfigFile writes the slice ID to the .gs/config file.
func writeConfigFile(sliceID string) error {
	return os.WriteFile(".gs/config", []byte(sliceID), 0644)
}

// splitAndTrim splits a string by a delimiter and trims whitespace from each part.
func splitAndTrim(s, delim string) []string {
	parts := strings.Split(s, delim)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// formatTimestamp formats a Unix timestamp into RFC3339 format.
func formatTimestamp(ts int64) string {
	return time.Unix(ts, 0).Format(time.RFC3339)
}

// stringFlag tracks whether a string flag was explicitly set
// so we can distinguish between a zero value and an omitted flag.
type stringFlag struct {
	value string
	set   bool
}

func (f *stringFlag) String() string {
	return f.value
}

func (f *stringFlag) Set(v string) error {
	f.value = v
	f.set = true
	return nil
}
