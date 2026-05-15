package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

// assertUnsupportedCommand ensures that a CLI invocation either errors or clearly
// reports that the command is not implemented. This keeps the new workflow
// tests meaningful even for commands that do not have full server-side
// support yet.
func assertUnsupportedCommand(t *testing.T, args ...string) {
	t.Helper()

	output, err := runCLIWithDirForTest(t, "", args...)
	if err != nil {
		return
	}

	if !strings.Contains(output, "Unknown") &&
		!strings.Contains(output, "not implemented") &&
		!strings.Contains(output, "Usage:") {
		t.Fatalf("expected command %v to be unsupported, got output: %s", args, output)
	}
}

func runCLIJSONOrFail[T any](t *testing.T, workdir string, args ...string) T {
	t.Helper()

	jsonArgs := appendJSONFlag(args)
	output := runCLIOrFail(t, workdir, jsonArgs...)

	var decoded T
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("failed to decode JSON output for %v: %v\nOutput:\n%s", jsonArgs, err, output)
	}
	return decoded
}

func runCLIAsRootAdminOrFail(t *testing.T, workdir string, args ...string) string {
	t.Helper()

	adminUsername := workflowRootAdminUser(t)
	output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", workflowProcessEnv(t, nil), true, adminUsername, args...)
	if err != nil {
		t.Fatalf("admin CLI command failed: %v\nOutput:\n%s\n%s", err, output, workflowFailureDiagnostics(t, workdir, args...))
	}
	return output
}

func runCLIJSONAsRootAdminOrFail[T any](t *testing.T, workdir string, args ...string) T {
	t.Helper()

	jsonArgs := appendJSONFlag(args)
	output := runCLIAsRootAdminOrFail(t, workdir, jsonArgs...)

	var decoded T
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("failed to decode admin JSON output for %v: %v\nOutput:\n%s", jsonArgs, err, output)
	}
	return decoded
}

func runCLIWithEnvOrFail(t *testing.T, workdir string, env map[string]string, args ...string) string {
	t.Helper()

	output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", workflowProcessEnv(t, env), true, workflowUsername(t), args...)
	if err != nil {
		t.Fatalf("CLI command failed: %v\nOutput:\n%s\n%s", err, output, workflowFailureDiagnostics(t, workdir, args...))
	}
	return output
}

func runCLIJSONWithEnvOrFail[T any](t *testing.T, workdir string, env map[string]string, args ...string) T {
	t.Helper()

	jsonArgs := appendJSONFlag(args)
	output := runCLIWithEnvOrFail(t, workdir, env, jsonArgs...)

	var decoded T
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("failed to decode JSON output for %v: %v\nOutput:\n%s", jsonArgs, err, output)
	}
	return decoded
}

func runCLIJSONErrorOrFail[T any](t *testing.T, workdir string, args ...string) T {
	t.Helper()

	jsonArgs := appendJSONFlag(args)
	output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", workflowProcessEnv(t, nil), true, workflowUsername(t), jsonArgs...)
	if err == nil {
		t.Fatalf("expected CLI command to fail for %v, got output:\n%s", jsonArgs, output)
	}

	var decoded T
	if unmarshalErr := json.Unmarshal([]byte(output), &decoded); unmarshalErr != nil {
		t.Fatalf("failed to decode JSON error output for %v: %v\nOutput:\n%s", jsonArgs, unmarshalErr, output)
	}
	return decoded
}

func appendJSONFlag(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	for _, arg := range args {
		if arg == "--json" {
			return append([]string(nil), args...)
		}
	}
	withFlag := append([]string(nil), args...)
	withFlag = append(withFlag, "--json")
	return withFlag
}
