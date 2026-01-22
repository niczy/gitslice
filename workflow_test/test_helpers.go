package workflow

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// assertUnsupportedCommand ensures that a CLI invocation either errors or clearly
// reports that the command is not implemented. This keeps the new workflow
// tests meaningful even for commands that do not have full server-side
// support yet.
func assertUnsupportedCommand(t *testing.T, args ...string) {
	t.Helper()

	output, err := runCLI(args...)
	if err != nil {
		return
	}

	if !strings.Contains(output, "Unknown") && !strings.Contains(output, "not implemented") {
		t.Fatalf("expected command %v to be unsupported, got output: %s", args, output)
	}
}

func newSlicePath(t *testing.T, name string) string {
	t.Helper()

	return filepath.Join(t.TempDir(), fmt.Sprintf("%s.json", name))
}
