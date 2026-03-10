package homeslice

import "testing"

func TestResolveVisiblePath(t *testing.T) {
	stored, visible, err := ResolveVisiblePath("tester", "/tester/docs/README.md", true)
	if err != nil {
		t.Fatalf("ResolveVisiblePath failed: %v", err)
	}
	if stored != "tester/docs/README.md" || visible != "/tester/docs/README.md" {
		t.Fatalf("unexpected resolved path: stored=%q visible=%q", stored, visible)
	}
}

func TestResolveVisiblePathRejectsForeignPrefix(t *testing.T) {
	if _, _, err := ResolveVisiblePath("tester", "/other/README.md", true); err == nil {
		t.Fatal("expected foreign path to be rejected")
	}
}

func TestResolveVisiblePattern(t *testing.T) {
	pattern, err := ResolveVisiblePattern("tester", "/tester/**/*.md", true)
	if err != nil {
		t.Fatalf("ResolveVisiblePattern failed: %v", err)
	}
	if pattern != "tester/**/*.md" {
		t.Fatalf("unexpected pattern: %q", pattern)
	}
}
