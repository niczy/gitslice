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

	if !strings.Contains(output, "Unknown") && !strings.Contains(output, "not implemented") {
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
