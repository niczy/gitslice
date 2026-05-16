package storage

import (
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

type contentCommitDirRow struct {
	HomeID        string
	DirPath       string
	CommitHash    string
	SourceSliceID string
	ParentHash    string
	Message       string
	Author        string
	CommittedAt   time.Time
	MergeSeq      int64
}

func normalizeContentCommitScopes(scopes []ContentCommitScope) []ContentCommitScope {
	if len(scopes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(scopes))
	out := make([]ContentCommitScope, 0, len(scopes))
	for _, scope := range scopes {
		homeID := strings.TrimSpace(scope.HomeID)
		dirPath := cleanRelativePath(scope.DirPath)
		if homeID == "" || dirPath == "" {
			continue
		}
		key := homeID + "\x00" + dirPath
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ContentCommitScope{HomeID: homeID, DirPath: dirPath})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HomeID == out[j].HomeID {
			return out[i].DirPath < out[j].DirPath
		}
		return out[i].HomeID < out[j].HomeID
	})
	return out
}

func contentCommitDirRowsFromMergeEvent(event *models.MergeEvent) []*contentCommitDirRow {
	if event == nil {
		return nil
	}
	homeID := strings.TrimSpace(event.HomeID)
	commitHash := strings.TrimSpace(event.SourceCommitHash)
	sourceSliceID := strings.TrimSpace(event.SourceSliceID)
	if homeID == "" || commitHash == "" || sourceSliceID == "" {
		return nil
	}
	committedAt := event.CreatedAt
	if committedAt.IsZero() {
		committedAt = time.Now()
	}
	parentHash := firstMergeEventParentHash(event)
	dirSet := make(map[string]struct{})
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		addContentCommitDirs(dirSet, update.Path)
	}
	if len(dirSet) == 0 {
		for _, touchedPath := range event.TouchedPaths {
			addContentCommitDirs(dirSet, touchedPath)
		}
	}
	if len(dirSet) == 0 {
		return nil
	}

	dirs := make([]string, 0, len(dirSet))
	for dirPath := range dirSet {
		dirs = append(dirs, dirPath)
	}
	sort.Strings(dirs)

	rows := make([]*contentCommitDirRow, 0, len(dirs))
	for _, dirPath := range dirs {
		rows = append(rows, &contentCommitDirRow{
			HomeID:        homeID,
			DirPath:       dirPath,
			CommitHash:    commitHash,
			SourceSliceID: sourceSliceID,
			ParentHash:    parentHash,
			Message:       event.Message,
			Author:        strings.TrimSpace(event.Author),
			CommittedAt:   committedAt,
			MergeSeq:      event.MergeSeq,
		})
	}
	return rows
}

func firstMergeEventParentHash(event *models.MergeEvent) string {
	if event == nil {
		return ""
	}
	for _, update := range event.PathUpdates {
		if update == nil {
			continue
		}
		if parentHash := strings.TrimSpace(update.ParentCommitHash); parentHash != "" {
			return parentHash
		}
	}
	return ""
}

func addContentCommitDirs(dirSet map[string]struct{}, rawPath string) {
	cleaned := cleanRelativePath(rawPath)
	if cleaned == "" {
		return
	}
	for _, dirPath := range ancestorDirectoryPaths(cleaned) {
		if dirPath == "" {
			continue
		}
		dirSet[dirPath] = struct{}{}
	}
	dirSet[cleaned] = struct{}{}
}
