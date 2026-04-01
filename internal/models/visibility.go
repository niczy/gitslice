package models

import (
	"strings"
	"time"
)

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

type PathVisibilityEntryType string

const (
	PathVisibilityEntryTypeFile      PathVisibilityEntryType = "file"
	PathVisibilityEntryTypeDirectory PathVisibilityEntryType = "directory"
)

type PathVisibilityRule struct {
	Path       string                  `json:"path"`
	EntryType  PathVisibilityEntryType `json:"entry_type"`
	Visibility Visibility              `json:"visibility"`
	UpdatedBy  string                  `json:"updated_by"`
	UpdatedAt  time.Time               `json:"updated_at"`
}
