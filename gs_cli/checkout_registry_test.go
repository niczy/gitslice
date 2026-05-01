package gscli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckoutRegistryRegisterListAndRemove(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	firstDir := filepath.Join(t.TempDir(), "checkout-a")
	secondDir := filepath.Join(t.TempDir(), "checkout-b")
	if err := os.MkdirAll(filepath.Join(firstDir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir first checkout: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(secondDir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir second checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firstDir, ".gs", "config"), []byte("home.tester"), 0o600); err != nil {
		t.Fatalf("write first config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secondDir, ".gs", "config"), []byte("slice.ui"), 0o600); err != nil {
		t.Fatalf("write second config: %v", err)
	}

	if err := registerCheckout(firstDir, "home.tester", "commit-1"); err != nil {
		t.Fatalf("register first checkout: %v", err)
	}
	if err := registerCheckout(secondDir, "slice.ui", "commit-2"); err != nil {
		t.Fatalf("register second checkout: %v", err)
	}

	records, err := listCheckoutRecords()
	if err != nil {
		t.Fatalf("list checkout records: %v", err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("expected %d records, got %d", want, got)
	}
	if got, want := countUniqueCheckoutSlices(records), 2; got != want {
		t.Fatalf("expected %d unique slices, got %d", want, got)
	}

	removed, err := removeCheckoutRecord(secondDir)
	if err != nil {
		t.Fatalf("remove checkout record: %v", err)
	}
	if !removed {
		t.Fatalf("expected checkout record removal")
	}

	records, err = listCheckoutRecords()
	if err != nil {
		t.Fatalf("list checkout records after remove: %v", err)
	}
	if got, want := len(records), 1; got != want {
		t.Fatalf("expected %d records after remove, got %d", want, got)
	}
	if records[0].SliceID != "home.tester" {
		t.Fatalf("unexpected remaining slice ID: %s", records[0].SliceID)
	}
}

func TestCheckoutRegistryPruneStaleRecords(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	activeDir := filepath.Join(t.TempDir(), "active-checkout")
	staleDir := filepath.Join(t.TempDir(), "stale-checkout")
	if err := os.MkdirAll(filepath.Join(activeDir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir active checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(activeDir, ".gs", "config"), []byte("home.active"), 0o600); err != nil {
		t.Fatalf("write active config: %v", err)
	}
	if err := registerCheckout(activeDir, "home.active", "commit-active"); err != nil {
		t.Fatalf("register active checkout: %v", err)
	}
	if err := registerCheckout(staleDir, "home.stale", "commit-stale"); err != nil {
		t.Fatalf("register stale checkout: %v", err)
	}

	records, err := listCheckoutRecords()
	if err != nil {
		t.Fatalf("list records: %v", err)
	}
	if got, want := countStaleCheckoutRecords(records), 1; got != want {
		t.Fatalf("expected %d stale record, got %d", want, got)
	}

	removed, err := pruneStaleCheckoutRecords()
	if err != nil {
		t.Fatalf("prune stale checkout records: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected to remove 1 stale record, got %d", removed)
	}

	records, err = listCheckoutRecords()
	if err != nil {
		t.Fatalf("list records after prune: %v", err)
	}
	if got, want := len(records), 1; got != want {
		t.Fatalf("expected %d remaining record, got %d", want, got)
	}
	if records[0].Path != filepath.Clean(activeDir) {
		t.Fatalf("unexpected remaining checkout path: %s", records[0].Path)
	}
}
