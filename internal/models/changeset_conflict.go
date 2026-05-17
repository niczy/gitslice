package models

import "time"

const (
	ChangesetConflictTypeStaleBase = "stale_base"
	ChangesetConflictTypeContent   = "content"
)

// ChangesetConflict is a durable artifact explaining why a changeset could not
// merge cleanly against the current path-head state.
type ChangesetConflict struct {
	ID             string
	ChangesetID    string
	SliceID        string
	Path           string
	Type           string
	Message        string
	BaseVersion    int64
	CurrentVersion int64
	BaseHash       string
	OursHash       string
	TheirsHash     string
	Patch          string
	Resolved       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ResolvedAt     *time.Time
}
