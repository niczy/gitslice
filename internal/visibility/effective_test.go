package visibility

import (
	"context"
	"testing"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestEffectiveDefaultsPrivate(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()

	got, err := Effective(ctx, st, &models.Slice{ID: "private"}, "docs/README.md")
	if err != nil {
		t.Fatalf("Effective failed: %v", err)
	}
	if got != models.VisibilityPrivate {
		t.Fatalf("Effective = %q, want %q", got, models.VisibilityPrivate)
	}
}

func TestEffectiveUsesNearestAncestorDirectoryRule(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs",
		EntryType:  models.PathVisibilityEntryTypeDirectory,
		Visibility: models.VisibilityPublic,
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule failed: %v", err)
	}

	got, err := Effective(ctx, st, &models.Slice{ID: "private"}, "docs/README.md")
	if err != nil {
		t.Fatalf("Effective failed: %v", err)
	}
	if got != models.VisibilityPublic {
		t.Fatalf("Effective = %q, want %q", got, models.VisibilityPublic)
	}
}

func TestEffectiveExactRuleOverridesAncestor(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs",
		EntryType:  models.PathVisibilityEntryTypeDirectory,
		Visibility: models.VisibilityPublic,
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule(dir) failed: %v", err)
	}
	if err := st.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs/README.md",
		EntryType:  models.PathVisibilityEntryTypeFile,
		Visibility: models.VisibilityPrivate,
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule(file) failed: %v", err)
	}

	got, err := Effective(ctx, st, &models.Slice{ID: "private"}, "docs/README.md")
	if err != nil {
		t.Fatalf("Effective failed: %v", err)
	}
	if got != models.VisibilityPrivate {
		t.Fatalf("Effective = %q, want %q", got, models.VisibilityPrivate)
	}
}

func TestEffectiveSlicePublicWins(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs",
		EntryType:  models.PathVisibilityEntryTypeDirectory,
		Visibility: models.VisibilityPrivate,
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule failed: %v", err)
	}

	got, err := Effective(ctx, st, &models.Slice{ID: "public", Visibility: models.VisibilityPublic}, "docs/README.md")
	if err != nil {
		t.Fatalf("Effective failed: %v", err)
	}
	if got != models.VisibilityPublic {
		t.Fatalf("Effective = %q, want %q", got, models.VisibilityPublic)
	}
}

func TestResolveTracksNearestRuleSource(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs",
		EntryType:  models.PathVisibilityEntryTypeDirectory,
		Visibility: models.VisibilityPublic,
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule failed: %v", err)
	}

	resolution, err := Resolve(ctx, st, &models.Slice{ID: "private"}, "docs/README.md")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got, want := resolution.ResolvedFromPath, "/docs"; got != want {
		t.Fatalf("ResolvedFromPath = %q, want %q", got, want)
	}
	if resolution.ExplicitRule {
		t.Fatal("expected inherited rule, got explicit")
	}
	if got, want := resolution.Visibility, models.VisibilityPublic; got != want {
		t.Fatalf("Visibility = %q, want %q", got, want)
	}
}

func TestResolveKeepsRuleVisibilityWhenSliceIsPublic(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs",
		EntryType:  models.PathVisibilityEntryTypeDirectory,
		Visibility: models.VisibilityPrivate,
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule failed: %v", err)
	}

	resolution, err := Resolve(ctx, st, &models.Slice{ID: "public", Visibility: models.VisibilityPublic}, "docs/README.md")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got, want := resolution.Visibility, models.VisibilityPrivate; got != want {
		t.Fatalf("Visibility = %q, want %q", got, want)
	}
	if got, want := resolution.EffectiveVisibility, models.VisibilityPublic; got != want {
		t.Fatalf("EffectiveVisibility = %q, want %q", got, want)
	}
}
