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

func TestResolveVisiblePatternAcceptsRelativeHomeGlobs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "filename pattern searches home tree",
			raw:  "*.md",
			want: "tester/**/*.md",
		},
		{
			name: "relative subdirectory pattern stays under home",
			raw:  "docs/**/*.md",
			want: "tester/docs/**/*.md",
		},
		{
			name: "home-prefixed relative pattern is accepted as-is",
			raw:  "tester/docs/**/*.md",
			want: "tester/docs/**/*.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern, err := ResolveVisiblePattern("tester", tt.raw, true)
			if err != nil {
				t.Fatalf("ResolveVisiblePattern(%q) failed: %v", tt.raw, err)
			}
			if pattern != tt.want {
				t.Fatalf("ResolveVisiblePattern(%q) = %q, want %q", tt.raw, pattern, tt.want)
			}
		})
	}
}
