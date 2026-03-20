package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	sliceConfigPath            = ".gs/config"
	trackedChangesetConfigPath = ".gs/changeset_id"
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
	return os.WriteFile(sliceConfigPath, []byte(sliceID), 0600)
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

func ensureGitignoreEntry(dir, entry string) (bool, error) {
	path := filepath.Join(dir, ".gitignore")
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	normalizedEntry := strings.TrimSpace(entry)
	if normalizedEntry == "" {
		return false, nil
	}

	if len(content) > 0 {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == normalizedEntry {
				return false, nil
			}
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()

	if len(content) > 0 && !strings.HasSuffix(string(content), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return false, err
		}
	}

	_, err = f.WriteString(normalizedEntry + "\n")
	if err != nil {
		return false, err
	}
	return true, nil
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

func createSyncCommit(dir, commitHash string) error {
	message := "gitslice sync"
	if commitHash != "" {
		message = fmt.Sprintf("gitslice sync %s", commitHash)
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
	hasWorktreeChanges, err := gitCommandReportsChanges(dir, "diff", "--quiet", "--ignore-submodules", "--")
	if err != nil {
		return false, err
	}
	if hasWorktreeChanges {
		return true, nil
	}

	hasStagedChanges, err := gitHasStagedChanges(dir)
	if err != nil {
		return false, err
	}
	if hasStagedChanges {
		return true, nil
	}

	output, err := runGitCommand(dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func gitTrackedFiles(dir string) ([]string, error) {
	output, err := runGitCommand(dir, "ls-files")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}
	lines := strings.Split(output, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned := strings.TrimSpace(line)
		if cleaned == "" {
			continue
		}
		files = append(files, filepath.Clean(cleaned))
	}
	return files, nil
}

func gitChangedFiles(dir string) ([]string, error) {
	output, err := runGitCommand(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}

	seen := make(map[string]struct{})
	files := make([]string, 0)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) < 4 {
			continue
		}
		pathSpec := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(pathSpec, " -> "); idx >= 0 {
			pathSpec = strings.TrimSpace(pathSpec[idx+4:])
		}
		if pathSpec == "" {
			continue
		}
		cleaned := filepath.Clean(pathSpec)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		files = append(files, cleaned)
	}
	return files, nil
}

func gitLatestCommitMessage(dir string) (string, error) {
	return runGitCommand(dir, "log", "-1", "--pretty=%s")
}

func gitHasStagedChanges(dir string) (bool, error) {
	return gitCommandReportsChanges(dir, "diff", "--cached", "--quiet", "--ignore-submodules", "--")
}

func gitStagePaths(dir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	const maxPathsPerBatch = 256
	for start := 0; start < len(paths); start += maxPathsPerBatch {
		end := start + maxPathsPerBatch
		if end > len(paths) {
			end = len(paths)
		}
		args := []string{"add", "-A", "--"}
		args = append(args, paths[start:end]...)
		if _, err := runGitCommand(dir, args...); err != nil {
			return err
		}
	}
	return nil
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

func gitCommandReportsChanges(dir string, args ...string) (bool, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 1 {
			return true, nil
		}
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return false, fmt.Errorf("git is required but was not found in PATH")
	}
	return false, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
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
