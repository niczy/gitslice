package storage

import (
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/ids"
	"github.com/niczy/gitslice/internal/models"
)

func normalizeChangesetConflictForStore(changesetID string, conflict *models.ChangesetConflict, now time.Time) *models.ChangesetConflict {
	if conflict == nil {
		return nil
	}
	changesetID = strings.TrimSpace(changesetID)
	path := cleanRelativePath(conflict.Path)
	if changesetID == "" || path == "" {
		return nil
	}
	out := *conflict
	out.ChangesetID = changesetID
	out.ID = strings.TrimSpace(out.ID)
	if out.ID == "" {
		out.ID = ids.GenerateChangesetConflictID(changesetID, path)
	}
	out.SliceID = strings.TrimSpace(out.SliceID)
	out.Path = path
	out.Type = normalizeChangesetConflictType(out.Type)
	out.Message = strings.TrimSpace(out.Message)
	out.BaseHash = strings.TrimSpace(out.BaseHash)
	out.OursHash = strings.TrimSpace(out.OursHash)
	out.TheirsHash = strings.TrimSpace(out.TheirsHash)
	if out.CreatedAt.IsZero() {
		out.CreatedAt = now
	}
	out.UpdatedAt = now
	if out.Resolved && out.ResolvedAt == nil {
		resolvedAt := now
		out.ResolvedAt = &resolvedAt
	}
	return &out
}

func normalizeChangesetConflictType(value string) string {
	switch strings.TrimSpace(value) {
	case models.ChangesetConflictTypeContent:
		return models.ChangesetConflictTypeContent
	default:
		return models.ChangesetConflictTypeStaleBase
	}
}
