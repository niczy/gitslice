package visibility

import (
	"testing"

	"github.com/niczy/gitslice/internal/models"
)

func TestEffectiveDefaultsPrivate(t *testing.T) {
	got, err := Effective(&models.Slice{ID: "private"}, "docs/README.md")
	if err != nil {
		t.Fatalf("Effective failed: %v", err)
	}
	if got != models.VisibilityPrivate {
		t.Fatalf("Effective = %q, want %q", got, models.VisibilityPrivate)
	}
}

func TestEffectiveUsesSliceVisibility(t *testing.T) {
	got, err := Effective(&models.Slice{ID: "public", Visibility: models.VisibilityPublic}, "docs/README.md")
	if err != nil {
		t.Fatalf("Effective failed: %v", err)
	}
	if got != models.VisibilityPublic {
		t.Fatalf("Effective = %q, want %q", got, models.VisibilityPublic)
	}
}

func TestResolveReportsSliceVisibilityWithoutPathRule(t *testing.T) {
	resolution, err := Resolve(&models.Slice{ID: "public", Visibility: models.VisibilityPublic}, "docs/README.md")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got, want := resolution.Path, "/docs/README.md"; got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	if resolution.ExplicitRule {
		t.Fatal("ExplicitRule = true, want false")
	}
	if got, want := resolution.ResolvedFromPath, ""; got != want {
		t.Fatalf("ResolvedFromPath = %q, want %q", got, want)
	}
	if got, want := resolution.Visibility, models.VisibilityPublic; got != want {
		t.Fatalf("Visibility = %q, want %q", got, want)
	}
	if got, want := resolution.EffectiveVisibility, models.VisibilityPublic; got != want {
		t.Fatalf("EffectiveVisibility = %q, want %q", got, want)
	}
}
