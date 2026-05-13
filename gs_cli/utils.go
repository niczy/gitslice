package gscli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sliceConfigPath            = ".gs/config"
	agentSessionConfigPath     = ".gs/agent_session_id"
	checkoutIndexPath          = ".gs/index"
	trackedChangesetConfigPath = ".gs/changeset_id"
	searchArtifactDirPath      = ".gs/search"
	searchArtifactBasePath     = ".gs/search/base.artifact"
	searchArtifactMetadataPath = ".gs/search/metadata.json"
	searchArtifactOverlayDir   = ".gs/search/overlay"
)

// readSliceIDFromConfig reads the slice ID from the .gs/config file.
func readSliceIDFromConfig() (string, error) {
	// Check if config file exists first
	if _, err := os.Stat(sliceConfigPath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%s file not found - have you run 'gs init'?", sliceConfigPath)
		}
		return "", fmt.Errorf("cannot access %s: %w", sliceConfigPath, err)
	}

	data, err := os.ReadFile(sliceConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", sliceConfigPath, err)
	}
	sliceID := strings.TrimSpace(string(data))
	if sliceID == "" {
		return "", fmt.Errorf("slice ID in %s is empty", sliceConfigPath)
	}
	return sliceID, nil
}

// writeSliceIDConfig writes the slice ID to the .gs/config file.
func writeSliceIDConfig(sliceID string) error {
	return writeSliceIDConfigAt(".", sliceID)
}

func writeSliceIDConfigAt(dir, sliceID string) error {
	return os.WriteFile(filepath.Join(dir, sliceConfigPath), []byte(sliceID), 0600)
}

func readAgentSessionIDFromConfig() (string, error) {
	data, err := os.ReadFile(agentSessionConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read %s: %w", agentSessionConfigPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func writeAgentSessionIDConfigAt(dir, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	path := filepath.Join(dir, agentSessionConfigPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sessionID), 0o600)
}

// readTrackedChangesetIDFromConfig reads the locally tracked changeset ID.
// Missing tracking file is treated as "no tracked changeset" and does not error.
func readTrackedChangesetIDFromConfig() (string, error) {
	data, err := os.ReadFile(trackedChangesetConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read %s: %w", trackedChangesetConfigPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// writeTrackedChangesetIDConfig persists the tracked changeset ID for this workspace.
// Empty IDs clear the tracking file.
func writeTrackedChangesetIDConfig(changesetID string) error {
	changesetID = strings.TrimSpace(changesetID)
	if changesetID == "" {
		return clearTrackedChangesetIDConfig()
	}
	if err := os.MkdirAll(filepath.Dir(trackedChangesetConfigPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(trackedChangesetConfigPath, []byte(changesetID), 0o600)
}

func clearTrackedChangesetIDConfig() error {
	err := os.Remove(trackedChangesetConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func clearTrackedChangesetIDIfMatches(changesetID string) error {
	changesetID = strings.TrimSpace(changesetID)
	if changesetID == "" {
		return nil
	}
	tracked, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		return err
	}
	if strings.TrimSpace(tracked) == changesetID {
		return clearTrackedChangesetIDConfig()
	}
	return nil
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
