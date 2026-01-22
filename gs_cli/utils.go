package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type sliceConfig struct {
	SlicePath string `toml:"slice_path"`
}

// readSlicePathFromConfig reads the slice path from the .gs/config file.
func readSlicePathFromConfig() (string, error) {
	data, err := os.ReadFile(".gs/config")
	if err != nil {
		return "", err
	}

	var cfg sliceConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	if cfg.SlicePath == "" {
		return "", errors.New("slice path is missing from config")
	}

	return normalizeSlicePath(cfg.SlicePath)
}

// writeConfigFile writes the slice path to the .gs/config file.
func writeConfigFile(slicePath string) error {
	normalizedPath, err := normalizeSlicePath(slicePath)
	if err != nil {
		return err
	}

	data, err := toml.Marshal(sliceConfig{SlicePath: normalizedPath})
	if err != nil {
		return err
	}

	return os.WriteFile(".gs/config", data, 0644)
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

func normalizeSlicePath(slicePath string) (string, error) {
	cleaned := strings.TrimSpace(slicePath)
	if cleaned == "" {
		return "", errors.New("slice path is empty")
	}

	cleaned = filepath.Clean(cleaned)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("slice path must be an absolute file path: %s", slicePath)
	}
	if cleaned == string(filepath.Separator) {
		return "", fmt.Errorf("slice path must point to a file: %s", slicePath)
	}

	return cleaned, nil
}

func ensureGitRepo(dir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}

	if _, err := runGitCommand(dir, "init", "-b", "main"); err == nil {
		return true, nil
	}

	if _, err := runGitCommand(dir, "init"); err != nil {
		return false, err
	}

	if _, err := runGitCommand(dir, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return false, err
	}

	return true, nil
}

func ensureGitignoreEntry(dir, entry string) error {
	path := filepath.Join(dir, ".gitignore")
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	normalizedEntry := strings.TrimSpace(entry)
	if normalizedEntry == "" {
		return nil
	}

	if len(content) > 0 {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == normalizedEntry {
				return nil
			}
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	_, err = f.WriteString(normalizedEntry + "\n")
	return err
}

func gitCurrentBranch(dir string) (string, error) {
	output, err := runGitCommand(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return output, nil
}

func requireMainBranch(dir string) error {
	branch, err := gitCurrentBranch(dir)
	if err != nil {
		return err
	}
	if branch != "main" {
		return fmt.Errorf("changesets can only be created from the main branch (current: %s)", branch)
	}
	return nil
}

func createCheckoutCommit(dir, commitHash string) error {
	message := "gitslice checkout"
	if commitHash != "" {
		message = fmt.Sprintf("gitslice checkout %s", commitHash)
	}

	_, err := runGitCommand(dir, "-c", "user.name=gitslice", "-c", "user.email=gitslice@local",
		"commit", "--allow-empty", "-m", message)
	return err
}

func gitHasCommit(dir string) (bool, error) {
	_, err := runGitCommand(dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "Needed a single revision") || strings.Contains(errMsg, "unknown revision") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func gitHasPendingChanges(dir string) (bool, error) {
	output, err := runGitCommand(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return output != "", nil
}

func runGitCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return trimmed, fmt.Errorf("git is required but was not found in PATH")
		}
		if trimmed != "" {
			return trimmed, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, trimmed)
		}
		return trimmed, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return trimmed, nil
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
