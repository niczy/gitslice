package gscli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareAgentSessionCheckoutTargetRootReplacesIncompleteDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "slice-session")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "partial.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := prepareAgentSessionCheckoutTargetRoot(root); err != nil {
		t.Fatalf("prepareAgentSessionCheckoutTargetRoot failed: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected prepared checkout dir to be empty, got %d entries", len(entries))
	}
}
