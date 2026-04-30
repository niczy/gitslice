package gscli

import (
	"os"
	"path/filepath"
	"testing"
)

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})
}

func TestTrackedChangesetConfigRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	if got, err := readTrackedChangesetIDFromConfig(); err != nil {
		t.Fatalf("read missing tracked id failed: %v", err)
	} else if got != "" {
		t.Fatalf("expected empty tracked id for missing file, got %q", got)
	}

	if err := writeTrackedChangesetIDConfig("cs-123"); err != nil {
		t.Fatalf("write tracked id failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(".gs", "changeset_id"))
	if err != nil {
		t.Fatalf("failed to read tracked changeset file directly: %v", err)
	}
	if string(raw) != "cs-123" {
		t.Fatalf("unexpected tracked changeset file contents: %q", string(raw))
	}

	got, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		t.Fatalf("read tracked id failed: %v", err)
	}
	if got != "cs-123" {
		t.Fatalf("expected tracked id cs-123, got %q", got)
	}

	if err := clearTrackedChangesetIDConfig(); err != nil {
		t.Fatalf("clear tracked id failed: %v", err)
	}
	if got, err := readTrackedChangesetIDFromConfig(); err != nil {
		t.Fatalf("read after clear failed: %v", err)
	} else if got != "" {
		t.Fatalf("expected empty tracked id after clear, got %q", got)
	}
}

func TestClearTrackedChangesetIDIfMatches(t *testing.T) {
	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	if err := writeTrackedChangesetIDConfig("cs-keep"); err != nil {
		t.Fatalf("write tracked id failed: %v", err)
	}
	if err := clearTrackedChangesetIDIfMatches("cs-other"); err != nil {
		t.Fatalf("clear-if-matches(other) failed: %v", err)
	}
	if got, err := readTrackedChangesetIDFromConfig(); err != nil {
		t.Fatalf("read tracked id failed: %v", err)
	} else if got != "cs-keep" {
		t.Fatalf("expected tracked id cs-keep, got %q", got)
	}

	if err := clearTrackedChangesetIDIfMatches("cs-keep"); err != nil {
		t.Fatalf("clear-if-matches(match) failed: %v", err)
	}
	if got, err := readTrackedChangesetIDFromConfig(); err != nil {
		t.Fatalf("read tracked id after clear failed: %v", err)
	} else if got != "" {
		t.Fatalf("expected empty tracked id after clear, got %q", got)
	}
}

func TestResolveChangesetIDForCreate(t *testing.T) {
	tmp := t.TempDir()
	withWorkingDir(t, tmp)

	id, isUpdate, err := resolveChangesetIDForCreate("")
	if err != nil {
		t.Fatalf("resolve without tracked id failed: %v", err)
	}
	if id != "" || isUpdate {
		t.Fatalf("expected new changeset mode, got id=%q isUpdate=%v", id, isUpdate)
	}

	id, isUpdate, err = resolveChangesetIDForCreate("cs-explicit")
	if err != nil {
		t.Fatalf("resolve with explicit id failed: %v", err)
	}
	if id != "cs-explicit" || !isUpdate {
		t.Fatalf("expected explicit update mode, got id=%q isUpdate=%v", id, isUpdate)
	}

	if err := writeTrackedChangesetIDConfig("cs-tracked"); err != nil {
		t.Fatalf("write tracked id failed: %v", err)
	}
	id, isUpdate, err = resolveChangesetIDForCreate("")
	if err != nil {
		t.Fatalf("resolve with tracked id failed: %v", err)
	}
	if id != "cs-tracked" || !isUpdate {
		t.Fatalf("expected tracked update mode, got id=%q isUpdate=%v", id, isUpdate)
	}
}
