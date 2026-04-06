package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func decodeCLIErrorJSON(t *testing.T, stdout string) cliErrorJSON {
	t.Helper()
	var errResp cliErrorJSON
	if err := json.Unmarshal([]byte(stdout), &errResp); err != nil {
		t.Fatalf("decode JSON error output: %v\nstdout:\n%s", err, stdout)
	}
	return errResp
}

func assertCLIJSONError(t *testing.T, stdout, stderr string, err error, wantExit int, wantCode string) cliErrorJSON {
	t.Helper()
	if err == nil {
		t.Fatalf("expected CLI command to fail\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %T (%v)", err, err)
	}
	if exitErr.ExitCode() != wantExit {
		t.Fatalf("expected exit code %d, got %d\nstdout:\n%s\nstderr:\n%s", wantExit, exitErr.ExitCode(), stdout, stderr)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("expected JSON error command to keep stderr empty, got:\n%s", stderr)
	}
	errResp := decodeCLIErrorJSON(t, stdout)
	if errResp.ErrorCode != wantCode {
		t.Fatalf("unexpected JSON error response: %+v", errResp)
	}
	return errResp
}

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
	assertCLIJSONError(t, stdout, stderr, err, 2, "INTERACTIVE_REQUIRED")
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
	if !strings.Contains(stderr, "interactive browser sign-in") {
		t.Fatalf("expected stderr guidance for non-interactive login failure, got:\n%s", stderr)
	}
}

func TestCLIExitCodeClassesAcrossAgentCommands(t *testing.T) {
	username := workflowUsername(t)
	stateWorkdir := t.TempDir()
	homeDir := t.TempDir()

	cases := []struct {
		name            string
		workdir         string
		env             map[string]string
		includeLegacy   bool
		legacyUser      string
		args            []string
		wantExit        int
		wantCode        string
		wantActionMatch string
	}{
		{
			name:            "auth class",
			env:             map[string]string{"HOME": homeDir},
			includeLegacy:   false,
			args:            []string{"auth", "login", "--key", filepath.Join(homeDir, "missing-ed25519"), "--json"},
			wantExit:        3,
			wantCode:        "AUTH_LOGIN_FAILED",
			wantActionMatch: "",
		},
		{
			name:            "state class",
			workdir:         stateWorkdir,
			env:             workflowProcessEnv(t, nil),
			includeLegacy:   true,
			legacyUser:      username,
			args:            []string{"slice", "status", "--json"},
			wantExit:        5,
			wantCode:        "SLICE_NOT_BOUND",
			wantActionMatch: "gs slice checkout",
		},
		{
			name:            "not found class",
			env:             workflowProcessEnv(t, nil),
			includeLegacy:   false,
			args:            []string{"jobs", "get", "job-does-not-exist", "--json"},
			wantExit:        4,
			wantCode:        "JOB_NOT_FOUND",
			wantActionMatch: "gs jobs list --json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCLIWithDirInputEnvLegacyUserStreams(tc.workdir, "", tc.env, tc.includeLegacy, tc.legacyUser, tc.args...)
			errResp := assertCLIJSONError(t, stdout, stderr, err, tc.wantExit, tc.wantCode)
			if tc.wantActionMatch != "" && !strings.Contains(errResp.SuggestedAction, tc.wantActionMatch) {
				t.Fatalf("expected suggested action %q in %+v", tc.wantActionMatch, errResp)
			}
		})
	}
}

func TestDetachedJobFailurePreservesJSONContractAndWaitIsIdempotent(t *testing.T) {
	username := fmt.Sprintf("jobfail%d", time.Now().UnixNano()%1_000_000_000)
	env := workflowProcessEnv(t, nil)

	runCLIJSONForUser := func(workdir string, args ...string) string {
		t.Helper()
		args = append(args, "--json")
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	boundPath := fmt.Sprintf("/%s/repos/missing-%d", username, time.Now().UnixNano())
	startOutput := runCLIJSONForUser("", "repo", "import", "--detach", filepath.Join(t.TempDir(), "does-not-exist.git"), boundPath)
	var started jobJSON
	if err := json.Unmarshal([]byte(startOutput), &started); err != nil {
		t.Fatalf("decode detached start JSON: %v\nOutput:\n%s", err, startOutput)
	}
	if started.JobID == "" || started.Kind != "repo import" {
		t.Fatalf("unexpected detached start output: %+v", started)
	}

	waitArgs := []string{"jobs", "wait", started.JobID, "--timeout", "10s", "--json"}
	firstStdout, firstStderr, firstErr := runCLIWithDirInputEnvLegacyUserStreams("", "", env, true, username, waitArgs...)
	if strings.TrimSpace(firstStderr) != "" {
		t.Fatalf("expected failed jobs wait JSON to keep stderr empty, got:\n%s", firstStderr)
	}
	var firstWait jobJSON
	if err := json.Unmarshal([]byte(firstStdout), &firstWait); err != nil {
		t.Fatalf("decode failed jobs wait JSON: %v\nOutput:\n%s", err, firstStdout)
	}
	if firstWait.Status != "failed" || firstWait.JobID != started.JobID {
		t.Fatalf("unexpected failed wait output: %+v", firstWait)
	}
	if len(firstWait.Result) == 0 {
		t.Fatalf("expected failed job result JSON, got: %+v", firstWait)
	}
	var exitErr *exec.ExitError
	if !errors.As(firstErr, &exitErr) {
		t.Fatalf("expected failed jobs wait to exit with job code, got %T (%v)", firstErr, firstErr)
	}
	if exitErr.ExitCode() != firstWait.ExitCode || firstWait.ExitCode != 1 {
		t.Fatalf("expected jobs wait to return failed job exit code, got exit=%d payload=%d", exitErr.ExitCode(), firstWait.ExitCode)
	}

	var jobErr cliErrorJSON
	if err := json.Unmarshal(firstWait.Result, &jobErr); err != nil {
		t.Fatalf("decode failed job result JSON: %v\nResult:\n%s", err, string(firstWait.Result))
	}
	if jobErr.ErrorCode != "REPO_IMPORT_FAILED" {
		t.Fatalf("unexpected failed job result: %+v", jobErr)
	}

	secondStdout, secondStderr, secondErr := runCLIWithDirInputEnvLegacyUserStreams("", "", env, true, username, waitArgs...)
	if strings.TrimSpace(secondStderr) != "" {
		t.Fatalf("expected repeated failed jobs wait JSON to keep stderr empty, got:\n%s", secondStderr)
	}
	var secondWait jobJSON
	if err := json.Unmarshal([]byte(secondStdout), &secondWait); err != nil {
		t.Fatalf("decode repeated failed jobs wait JSON: %v\nOutput:\n%s", err, secondStdout)
	}
	if secondWait.JobID != firstWait.JobID || secondWait.Status != firstWait.Status || secondWait.ExitCode != firstWait.ExitCode || string(secondWait.Result) != string(firstWait.Result) {
		t.Fatalf("expected repeated jobs wait to be idempotent, got first=%+v second=%+v", firstWait, secondWait)
	}
	if !errors.As(secondErr, &exitErr) || exitErr.ExitCode() != secondWait.ExitCode {
		t.Fatalf("expected repeated failed jobs wait to preserve exit code, err=%v payload=%+v", secondErr, secondWait)
	}

	logsOutput := runCLIJSONForUser("", "jobs", "logs", started.JobID)
	var logs jobLogsJSON
	if err := json.Unmarshal([]byte(logsOutput), &logs); err != nil {
		t.Fatalf("decode jobs logs JSON: %v\nOutput:\n%s", err, logsOutput)
	}
	var loggedErr cliErrorJSON
	if err := json.Unmarshal([]byte(logs.Stdout), &loggedErr); err != nil {
		t.Fatalf("decode jobs logs stdout JSON: %v\nstdout:\n%s", err, logs.Stdout)
	}
	if logs.JobID != started.JobID || loggedErr.ErrorCode != "REPO_IMPORT_FAILED" || strings.TrimSpace(logs.Stderr) != "" {
		t.Fatalf("unexpected failed jobs logs output: %+v", logs)
	}
}

func TestCLINonInteractiveAuthDeviceCommandsFailFast(t *testing.T) {
	homeDir := t.TempDir()
	env := map[string]string{
		"HOME":               homeDir,
		"GS_NON_INTERACTIVE": "1",
	}

	cases := []struct {
		name            string
		args            []string
		wantActionMatch string
	}{
		{
			name:            "auth login device",
			args:            []string{"auth", "login", "--device", "--json"},
			wantActionMatch: "gs auth login --key <private-key-path>",
		},
		{
			name:            "auth ensure device",
			args:            []string{"auth", "ensure", "--device", "--json"},
			wantActionMatch: "gs auth ensure --key <private-key-path>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCLIWithDirInputEnvLegacyUserStreams("", "", env, false, "", tc.args...)
			errResp := assertCLIJSONError(t, stdout, stderr, err, 2, "INTERACTIVE_REQUIRED")
			if !strings.Contains(errResp.SuggestedAction, tc.wantActionMatch) {
				t.Fatalf("expected suggested action %q in %+v", tc.wantActionMatch, errResp)
			}
		})
	}
}
