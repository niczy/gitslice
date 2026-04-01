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
