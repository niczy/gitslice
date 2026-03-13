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
	owner := strings.TrimSpace(slice.CreatedBy)
	base := defaultSliceSlugBase(slice)
	if owner == "" {
		return base
	}
	return fmt.Sprintf("%s/%s", owner, base)
}

func sliceSlugCandidate(slice *models.Slice, n int) string {
	base := defaultSliceSlug(slice)
	if n <= 1 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, n)
}
