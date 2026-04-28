package storage

import (
	"fmt"
	"strings"

	"github.com/niczy/gitslice/internal/models"
)

func slugifySliceName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.TrimRight(out[:48], "-")
	}
	return out
}

func defaultSliceSlugBase(slice *models.Slice) string {
	if slice == nil {
		return "slice"
	}
	if slice.IsRoot {
		return "root"
	}
	base := slugifySliceName(slice.Name)
	if base == "" {
		base = slugifySliceName(slice.ID)
	}
	if base == "" {
		base = "slice"
	}
	return base
}

func defaultSliceSlug(slice *models.Slice) string {
	if slice == nil {
		return ""
	}
	if slice.IsRoot {
		return "root"
	}
	return defaultSliceSlugBase(slice)
}

func sliceSlugCandidate(slice *models.Slice, n int) string {
	base := defaultSliceSlug(slice)
	if n <= 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, n)
}

func SplitQualifiedSliceRef(ref string) (owner, slug string, ok bool) {
	ref = strings.Trim(strings.TrimSpace(ref), "/")
	if ref == "" {
		return "", "", false
	}
	parts := strings.Split(ref, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	owner = strings.TrimSpace(parts[0])
	slug = strings.TrimSpace(parts[1])
	if owner == "" || slug == "" {
		return "", "", false
	}
	return owner, slug, true
}

func QualifiedSliceSlug(slice *models.Slice) string {
	if slice == nil {
		return ""
	}
	slug := strings.TrimSpace(slice.Slug)
	if slug == "" {
		slug = defaultSliceSlug(slice)
	}
	if slug == "" {
		return ""
	}
	if slice.IsRoot {
		return slug
	}
	owner := strings.TrimSpace(slice.CreatedBy)
	if owner == "" {
		return slug
	}
	return owner + "/" + slug
}

func normalizeStoredSliceSlug(slice *models.Slice, raw string) (string, error) {
	slug := strings.Trim(strings.TrimSpace(raw), "/")
	if slug == "" {
		return "", nil
	}
	if owner, local, ok := SplitQualifiedSliceRef(slug); ok {
		expectedOwner := strings.TrimSpace(slice.CreatedBy)
		if expectedOwner != "" && owner != expectedOwner {
			return "", ErrInvalidInput
		}
		slug = local
	}
	if strings.Contains(slug, "/") || strings.Contains(slug, `\`) {
		return "", ErrInvalidInput
	}
	return slug, nil
}
