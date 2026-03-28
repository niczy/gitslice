package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCLIJSONSuccessWritesOnlyStdout(t *testing.T) {
	username := workflowUsername(t)
	env := workflowProcessEnv(t, nil)
	targetPath := fmt.Sprintf("/%s/agent-json-%d", username, time.Now().UnixNano())

	stdout, stderr, err := runCLIWithDirInputEnvLegacyUserStreams("", "", env, true, username, "fs", "ensure-dir", targetPath, "--json")
	if err != nil {
		t.Fatalf("expected ensure-dir JSON command to succeed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected JSON success command to keep stderr empty, got:\n%s", stderr)
	}

	var resp filesystemActionJSON
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("decode ensure-dir JSON: %v\nstdout:\n%s", err, stdout)
	}
	if resp.Action != "ensure-dir" || resp.Status != "created" || resp.Path != targetPath {
		t.Fatalf("unexpected ensure-dir JSON response: %+v", resp)
	}
}

func TestCLIJSONErrorWritesOnlyStdout(t *testing.T) {
	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	stdout, stderr, err := runCLIWithDirInputEnvLegacyUserStreams("", "", env, false, "", "login", "--non-interactive", "--json")
	if err == nil {
		t.Fatalf("expected non-interactive JSON login to fail\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %T (%v)", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected usage exit code 2, got %d\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected JSON error command to keep stderr empty, got:\n%s", stderr)
	}

	var errResp cliErrorJSON
	if err := json.Unmarshal([]byte(stdout), &errResp); err != nil {
		t.Fatalf("decode JSON error output: %v\nstdout:\n%s", err, stdout)
	}
	if errResp.ErrorCode != "INTERACTIVE_REQUIRED" {
		t.Fatalf("unexpected JSON error response: %+v", errResp)
	}
}

func TestCLINonJSONErrorWritesOnlyStderr(t *testing.T) {
	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	stdout, stderr, err := runCLIWithDirInputEnvLegacyUserStreams("", "", env, false, "", "login", "--non-interactive")
	if err == nil {
		t.Fatalf("expected non-interactive login to fail\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %T (%v)", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected usage exit code 2, got %d\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected non-JSON error command to keep stdout empty, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "Device login is interactive.") {
		t.Fatalf("expected stderr guidance for non-interactive login failure, got:\n%s", stderr)
	}
}
