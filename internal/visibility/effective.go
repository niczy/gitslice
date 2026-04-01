package visibility

import (
	"context"
	"path"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

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

// Effective returns the resolved visibility for a slice path.
// Slice-level public visibility wins over all path-specific rules.
func Effective(ctx context.Context, st storage.Storage, slice *models.Slice, rawPath string) (models.Visibility, error) {
	if slice != nil && slice.Visibility.IsPublic() {
		return models.VisibilityPublic, nil
	}

	current := NormalizePath(rawPath)
	if current == "" {
		return models.VisibilityPrivate, nil
	}

	rule, err := st.GetPathVisibilityRule(ctx, current)
	if err == nil && rule != nil {
		return models.NormalizeVisibility(rule.Visibility), nil
	}
	if err != nil && err != storage.ErrEntryNotFound {
		return models.VisibilityPrivate, err
	}

	for ancestor := path.Dir(current); ancestor != "." && ancestor != ""; ancestor = path.Dir(ancestor) {
		rule, err := st.GetPathVisibilityRule(ctx, ancestor)
		if err == nil && rule != nil && rule.EntryType == models.PathVisibilityEntryTypeDirectory {
			return models.NormalizeVisibility(rule.Visibility), nil
		}
		if err != nil && err != storage.ErrEntryNotFound {
			return models.VisibilityPrivate, err
		}
		if ancestor == "/" {
			break
		}
	}

	return models.VisibilityPrivate, nil
}

func IsPublic(ctx context.Context, st storage.Storage, slice *models.Slice, rawPath string) (bool, error) {
	resolved, err := Effective(ctx, st, slice, rawPath)
	if err != nil {
		return false, err
	}
	return resolved.IsPublic(), nil
}
