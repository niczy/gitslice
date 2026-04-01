package visibility

import (
	"context"
	"path"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type Resolution struct {
	Path                string
	Visibility          models.Visibility
	EffectiveVisibility models.Visibility
	ExplicitRule        bool
	ResolvedFromPath    string
}

// NormalizePath converts a logical relative path into the stored visibility-rule key form.
func NormalizePath(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	cleaned = path.Clean(cleaned)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

// Resolve returns the resolved visibility details for a slice path.
// Slice-level public visibility wins over all path-specific rules for the
// effective visibility, but path/ancestor rules are still surfaced.
func Resolve(ctx context.Context, st storage.Storage, slice *models.Slice, rawPath string) (*Resolution, error) {
	current := NormalizePath(rawPath)
	resolution := &Resolution{
		Path:                current,
		Visibility:          models.VisibilityPrivate,
		EffectiveVisibility: models.VisibilityPrivate,
	}
	if slice != nil && slice.Visibility.IsPublic() {
		resolution.EffectiveVisibility = models.VisibilityPublic
	}
	if current == "" {
		return resolution, nil
	}

	rule, err := st.GetPathVisibilityRule(ctx, current)
	if err == nil && rule != nil {
		resolution.Visibility = models.NormalizeVisibility(rule.Visibility)
		resolution.EffectiveVisibility = resolution.Visibility
		resolution.ExplicitRule = true
		resolution.ResolvedFromPath = rule.Path
		if slice != nil && slice.Visibility.IsPublic() {
			resolution.EffectiveVisibility = models.VisibilityPublic
		}
		return resolution, nil
	}
	if err != nil && err != storage.ErrEntryNotFound {
		return nil, err
	}

	for ancestor := path.Dir(current); ancestor != "." && ancestor != ""; ancestor = path.Dir(ancestor) {
		rule, err := st.GetPathVisibilityRule(ctx, ancestor)
		if err == nil && rule != nil && rule.EntryType == models.PathVisibilityEntryTypeDirectory {
			resolution.Visibility = models.NormalizeVisibility(rule.Visibility)
			resolution.EffectiveVisibility = resolution.Visibility
			resolution.ResolvedFromPath = rule.Path
			if slice != nil && slice.Visibility.IsPublic() {
				resolution.EffectiveVisibility = models.VisibilityPublic
			}
			return resolution, nil
		}
		if err != nil && err != storage.ErrEntryNotFound {
			return nil, err
		}
		if ancestor == "/" {
			break
		}
	}

	return resolution, nil
}

// Effective returns the resolved visibility for a slice path.
// Slice-level public visibility wins over all path-specific rules.
func Effective(ctx context.Context, st storage.Storage, slice *models.Slice, rawPath string) (models.Visibility, error) {
	resolution, err := Resolve(ctx, st, slice, rawPath)
	if err != nil {
		return models.VisibilityPrivate, err
	}
	return resolution.EffectiveVisibility, nil
}

func IsPublic(ctx context.Context, st storage.Storage, slice *models.Slice, rawPath string) (bool, error) {
	resolved, err := Effective(ctx, st, slice, rawPath)
	if err != nil {
		return false, err
	}
	return resolved.IsPublic(), nil
}
