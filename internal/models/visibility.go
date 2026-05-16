package models

import "strings"

type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

func NormalizeVisibility(v Visibility) Visibility {
	switch strings.ToLower(strings.TrimSpace(string(v))) {
	case string(VisibilityPublic):
		return VisibilityPublic
	default:
		return VisibilityPrivate
	}
}

func (v Visibility) IsPublic() bool {
	return NormalizeVisibility(v) == VisibilityPublic
}
