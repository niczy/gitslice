package visibility

import (
	"path"
	"strings"

	"github.com/niczy/gitslice/internal/models"
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

// Resolve returns the visibility details for a slice path. Visibility is a
// slice-level property; paths inherit the containing slice's visibility.
func Resolve(slice *models.Slice, rawPath string) (*Resolution, error) {
	current := NormalizePath(rawPath)
	effective := models.VisibilityPrivate
	if slice != nil && slice.Visibility.IsPublic() {
		effective = models.VisibilityPublic
	}
	resolution := &Resolution{
		Path:                current,
		Visibility:          effective,
		EffectiveVisibility: effective,
	}
	return resolution, nil
}

// Effective returns the inherited visibility for a slice path.
func Effective(slice *models.Slice, rawPath string) (models.Visibility, error) {
	resolution, err := Resolve(slice, rawPath)
	if err != nil {
		return models.VisibilityPrivate, err
	}
	return resolution.EffectiveVisibility, nil
}

func IsPublic(slice *models.Slice, rawPath string) (bool, error) {
	resolved, err := Effective(slice, rawPath)
	if err != nil {
		return false, err
	}
	return resolved.IsPublic(), nil
}
