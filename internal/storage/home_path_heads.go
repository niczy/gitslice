package storage

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

const homePathHeadSlicePrefix = "home_"

const (
	homePathHeadEntryTypeFile      = "file"
	homePathHeadEntryTypeDirectory = "directory"
)

func normalizeHomePathHeadHomeID(homeID string) string {
	return strings.TrimSpace(homeID)
}

func homePathHeadSourceSliceID(homeID string) string {
	homeID = normalizeHomePathHeadHomeID(homeID)
	if homeID == "" {
		return ""
	}
	return homePathHeadSlicePrefix + homeID
}

func homePathHeadKey(homeID, filePath string) string {
	return normalizeHomePathHeadHomeID(homeID) + ":" + cleanRelativePath(filePath)
}

func homePathHeadCurrentVersion(head *models.HomePathHead) int64 {
	if head == nil || head.PathVersion < 0 {
		return 0
	}
	return head.PathVersion
}

func cloneHomePathHead(head *models.HomePathHead) *models.HomePathHead {
	if head == nil {
		return nil
	}
	clone := *head
	return &clone
}

func homePathHeadIncomingIsCurrentOrNewer(incoming, existing *models.HomePathHead) bool {
	if incoming == nil {
		return false
	}
	if existing == nil {
		return true
	}
	if incoming.LastMergeSeq != existing.LastMergeSeq {
		return incoming.LastMergeSeq > existing.LastMergeSeq
	}
	return incoming.PathVersion >= existing.PathVersion
}

func normalizeHomePathHead(head *models.HomePathHead) (*models.HomePathHead, error) {
	if head == nil {
		return nil, ErrInvalidInput
	}
	normalized := *head
	normalized.HomeID = normalizeHomePathHeadHomeID(normalized.HomeID)
	normalized.Path = cleanRelativePath(normalized.Path)
	normalized.EntryType = normalizeHomePathHeadEntryType(normalized.EntryType)
	normalized.ContentHash = strings.TrimSpace(normalized.ContentHash)
	normalized.ManifestHash = strings.TrimSpace(normalized.ManifestHash)
	normalized.SourceSliceID = strings.TrimSpace(normalized.SourceSliceID)
	normalized.SourceCommitHash = strings.TrimSpace(normalized.SourceCommitHash)
	if normalized.HomeID == "" || normalized.Path == "" {
		return nil, ErrInvalidInput
	}
	if normalized.PathVersion <= 0 {
		normalized.PathVersion = 1
	}
	if normalized.LastMergeSeq < 0 {
		return nil, ErrInvalidInput
	}
	if normalized.EntryType == homePathHeadEntryTypeFile {
		if normalized.ManifestHash == "" {
			normalized.ManifestHash = normalized.ContentHash
		}
		if normalized.ContentHash == "" {
			normalized.ContentHash = normalized.ManifestHash
		}
	} else {
		normalized.ContentHash = ""
		normalized.ManifestHash = ""
	}
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = time.Now()
	}
	return &normalized, nil
}

func normalizeHomePathHeadEntryType(entryType string) string {
	switch strings.ToLower(strings.TrimSpace(entryType)) {
	case homePathHeadEntryTypeDirectory:
		return homePathHeadEntryTypeDirectory
	default:
		return homePathHeadEntryTypeFile
	}
}

func homePathHeadHomeIDFromSliceID(sliceID string) (string, bool) {
	sliceID = strings.TrimSpace(sliceID)
	if !strings.HasPrefix(sliceID, homePathHeadSlicePrefix) {
		return "", false
	}
	homeID := strings.TrimSpace(strings.TrimPrefix(sliceID, homePathHeadSlicePrefix))
	return homeID, homeID != ""
}

func normalizeHomePathHeadPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		filePath := cleanRelativePath(rawPath)
		if filePath == "" {
			continue
		}
		if _, ok := seen[filePath]; ok {
			continue
		}
		seen[filePath] = struct{}{}
		out = append(out, filePath)
	}
	sort.Strings(out)
	return out
}

// UpdateHomePathHeadsFromSlicePaths projects direct writes to a home slice into
// the path-head view used by root/home reads. It is intentionally scoped to
// home slices; normal custom slices write path heads through merge events.
func UpdateHomePathHeadsFromSlicePaths(ctx context.Context, st Storage, sliceID, commitHash string, commitTime time.Time, paths []string) error {
	heads, ok := st.(HomePathHeadStore)
	if !ok {
		return nil
	}
	homeID, ok := homePathHeadHomeIDFromSliceID(sliceID)
	if !ok {
		return nil
	}
	cleanedPaths := normalizeHomePathHeadPaths(paths)
	if len(cleanedPaths) == 0 {
		return nil
	}
	existing, err := heads.GetHomePathHeads(ctx, homeID, cleanedPaths)
	if err != nil {
		return err
	}
	materializedEntries := map[string]bool(nil)
	if lister, ok := st.(interface {
		GetExistingEntriesByPaths(context.Context, string, []string) (map[string]bool, error)
	}); ok {
		materializedEntries, err = lister.GetExistingEntriesByPaths(ctx, sliceID, cleanedPaths)
		if err != nil {
			return err
		}
	}
	if commitTime.IsZero() {
		commitTime = time.Now()
	}
	lastMergeSeq := commitTime.UnixNano()
	if lastMergeSeq < 0 {
		lastMergeSeq = 0
	}

	projected := make([]*models.HomePathHead, 0, len(cleanedPaths))
	for _, filePath := range cleanedPaths {
		current := existing[filePath]
		pathVersion := homePathHeadCurrentVersion(current) + 1
		if pathVersion <= 0 {
			pathVersion = 1
		}

		if materializedEntries != nil && !materializedEntries[filePath] {
			projected = append(projected, &models.HomePathHead{
				HomeID:           homeID,
				Path:             filePath,
				EntryType:        homePathHeadEntryTypeFile,
				PathVersion:      pathVersion,
				SourceSliceID:    strings.TrimSpace(sliceID),
				SourceCommitHash: strings.TrimSpace(commitHash),
				LastMergeSeq:     lastMergeSeq,
				Deleted:          true,
				UpdatedAt:        commitTime,
			})
			continue
		}

		entry, entryErr := st.GetEntryByPath(ctx, sliceID, filePath)
		switch {
		case entryErr == nil && entry != nil && strings.TrimSpace(entry.Type) == homePathHeadEntryTypeDirectory:
			projected = append(projected, &models.HomePathHead{
				HomeID:           homeID,
				Path:             filePath,
				EntryType:        homePathHeadEntryTypeDirectory,
				PathVersion:      pathVersion,
				SourceSliceID:    strings.TrimSpace(sliceID),
				SourceCommitHash: strings.TrimSpace(commitHash),
				LastMergeSeq:     lastMergeSeq,
				UpdatedAt:        commitTime,
			})
		case entryErr == nil && entry != nil:
			manifest, err := st.GetFileManifest(ctx, sliceID, filePath)
			if err != nil {
				return err
			}
			projected = append(projected, &models.HomePathHead{
				HomeID:           homeID,
				Path:             filePath,
				EntryType:        homePathHeadEntryTypeFile,
				PathVersion:      pathVersion,
				ContentHash:      strings.TrimSpace(manifest.Hash),
				ManifestHash:     strings.TrimSpace(manifest.Hash),
				SourceSliceID:    strings.TrimSpace(sliceID),
				SourceCommitHash: strings.TrimSpace(commitHash),
				LastMergeSeq:     lastMergeSeq,
				UpdatedAt:        commitTime,
			})
		case errors.Is(entryErr, ErrEntryNotFound):
			projected = append(projected, &models.HomePathHead{
				HomeID:           homeID,
				Path:             filePath,
				EntryType:        homePathHeadEntryTypeFile,
				PathVersion:      pathVersion,
				SourceSliceID:    strings.TrimSpace(sliceID),
				SourceCommitHash: strings.TrimSpace(commitHash),
				LastMergeSeq:     lastMergeSeq,
				Deleted:          true,
				UpdatedAt:        commitTime,
			})
		default:
			return entryErr
		}
	}
	return heads.UpsertHomePathHeads(ctx, projected)
}

func homePathHeadsFromMergeEvent(event *models.MergeEvent) ([]*models.HomePathHead, error) {
	normalized, err := normalizeMergeEvent(event)
	if err != nil {
		return nil, err
	}
	heads := make([]*models.HomePathHead, 0, len(normalized.PathUpdates))
	for _, update := range normalized.PathUpdates {
		if update == nil {
			continue
		}
		path := cleanRelativePath(update.Path)
		if path == "" || update.NewVersion <= 0 {
			return nil, ErrInvalidInput
		}
		sourceSliceID := strings.TrimSpace(update.SourceSliceID)
		if sourceSliceID == "" {
			sourceSliceID = normalized.SourceSliceID
		}
		sourceCommitHash := strings.TrimSpace(update.SourceCommitHash)
		if sourceCommitHash == "" {
			sourceCommitHash = normalized.SourceCommitHash
		}
		head, err := normalizeHomePathHead(&models.HomePathHead{
			HomeID:           normalized.HomeID,
			Path:             path,
			EntryType:        homePathHeadEntryTypeFile,
			PathVersion:      update.NewVersion,
			ContentHash:      update.ContentHash,
			ManifestHash:     update.ManifestHash,
			SourceSliceID:    sourceSliceID,
			SourceCommitHash: sourceCommitHash,
			LastMergeSeq:     normalized.MergeSeq,
			Deleted:          update.Deleted,
			UpdatedAt:        normalized.CreatedAt,
		})
		if err != nil {
			return nil, err
		}
		heads = append(heads, head)
	}
	return heads, nil
}

func collectMaterializedHomePathHeads(ctx context.Context, st Storage, homeID string) ([]*models.HomePathHead, string, error) {
	homeID = normalizeHomePathHeadHomeID(homeID)
	sourceSliceID := homePathHeadSourceSliceID(homeID)
	if homeID == "" || sourceSliceID == "" {
		return nil, "", ErrInvalidInput
	}
	if _, err := st.GetSlice(ctx, sourceSliceID); err != nil {
		return nil, sourceSliceID, err
	}

	sourceCommitHash := ""
	if metadata, err := st.GetSliceMetadata(ctx, sourceSliceID); err == nil && metadata != nil {
		sourceCommitHash = strings.TrimSpace(metadata.HeadCommitHash)
	} else if err != nil && !errors.Is(err, ErrEntryNotFound) && !errors.Is(err, ErrSliceNotFound) {
		return nil, sourceSliceID, err
	}

	heads := make([]*models.HomePathHead, 0)
	queue := []string{sourceSliceID}
	seenParents := map[string]struct{}{sourceSliceID: {}}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		entries, err := st.ListEntries(ctx, sourceSliceID, parentID)
		if err != nil {
			return nil, sourceSliceID, err
		}
		for _, entry := range entries {
			if entry == nil {
				continue
			}
			entryPath := cleanRelativePath(entry.Path)
			switch strings.TrimSpace(entry.Type) {
			case "directory":
				if entry.ID == "" {
					continue
				}
				if _, ok := seenParents[entry.ID]; ok {
					continue
				}
				seenParents[entry.ID] = struct{}{}
				queue = append(queue, entry.ID)
			case "file":
				if entryPath == "" {
					continue
				}
				manifest, err := st.GetFileManifest(ctx, sourceSliceID, entryPath)
				if err != nil {
					if errors.Is(err, ErrEntryNotFound) {
						continue
					}
					return nil, sourceSliceID, err
				}
				manifestHash := ""
				if manifest != nil {
					manifestHash = strings.TrimSpace(manifest.Hash)
				}
				head, err := normalizeHomePathHead(&models.HomePathHead{
					HomeID:           homeID,
					Path:             entryPath,
					EntryType:        homePathHeadEntryTypeFile,
					PathVersion:      1,
					ContentHash:      manifestHash,
					ManifestHash:     manifestHash,
					SourceSliceID:    sourceSliceID,
					SourceCommitHash: sourceCommitHash,
					UpdatedAt:        time.Now(),
				})
				if err != nil {
					return nil, sourceSliceID, err
				}
				heads = append(heads, head)
			}
		}
	}
	sort.Slice(heads, func(i, j int) bool {
		return heads[i].Path < heads[j].Path
	})
	return heads, sourceSliceID, nil
}

func backfillHomePathHeads(ctx context.Context, st Storage, heads HomePathHeadStore, homeID string) (*models.HomePathHeadBackfillResult, error) {
	if heads == nil {
		return nil, ErrInvalidInput
	}
	materialized, sourceSliceID, err := collectMaterializedHomePathHeads(ctx, st, homeID)
	if err != nil {
		return nil, err
	}
	if err := heads.UpsertHomePathHeads(ctx, materialized); err != nil {
		return nil, err
	}
	return &models.HomePathHeadBackfillResult{
		HomeID:        normalizeHomePathHeadHomeID(homeID),
		SourceSliceID: sourceSliceID,
		Upserted:      len(materialized),
	}, nil
}

func validateHomePathHeads(ctx context.Context, st Storage, heads HomePathHeadStore, homeID string) (*models.HomePathHeadValidationResult, error) {
	if heads == nil {
		return nil, ErrInvalidInput
	}
	materialized, sourceSliceID, err := collectMaterializedHomePathHeads(ctx, st, homeID)
	if err != nil {
		return nil, err
	}
	stored, err := heads.ListHomePathHeads(ctx, homeID, maxHomePathHeadListLimit)
	if err != nil {
		return nil, err
	}

	materializedByPath := make(map[string]*models.HomePathHead, len(materialized))
	for _, head := range materialized {
		if head != nil {
			materializedByPath[head.Path] = head
		}
	}
	storedByPath := make(map[string]*models.HomePathHead, len(stored))
	for _, head := range stored {
		if head != nil {
			storedByPath[head.Path] = head
		}
	}

	drifts := make([]*models.HomePathHeadDrift, 0)
	for filePath, materializedHead := range materializedByPath {
		storedHead := storedByPath[filePath]
		if storedHead == nil {
			drifts = append(drifts, &models.HomePathHeadDrift{
				Path:                     filePath,
				Reason:                   "missing_head",
				MaterializedManifestHash: materializedHead.ManifestHash,
			})
			continue
		}
		if storedHead.Deleted {
			drifts = append(drifts, &models.HomePathHeadDrift{
				Path:                     filePath,
				Reason:                   "deleted_mismatch",
				HeadManifestHash:         storedHead.ManifestHash,
				MaterializedManifestHash: materializedHead.ManifestHash,
				HeadDeleted:              true,
			})
			continue
		}
		if strings.TrimSpace(storedHead.ManifestHash) != strings.TrimSpace(materializedHead.ManifestHash) {
			drifts = append(drifts, &models.HomePathHeadDrift{
				Path:                     filePath,
				Reason:                   "manifest_mismatch",
				HeadManifestHash:         storedHead.ManifestHash,
				MaterializedManifestHash: materializedHead.ManifestHash,
			})
		}
	}
	for filePath, storedHead := range storedByPath {
		if _, ok := materializedByPath[filePath]; ok || storedHead.Deleted {
			continue
		}
		drifts = append(drifts, &models.HomePathHeadDrift{
			Path:             filePath,
			Reason:           "extra_head",
			HeadManifestHash: storedHead.ManifestHash,
		})
	}
	sort.Slice(drifts, func(i, j int) bool {
		if drifts[i].Path != drifts[j].Path {
			return drifts[i].Path < drifts[j].Path
		}
		return drifts[i].Reason < drifts[j].Reason
	})

	return &models.HomePathHeadValidationResult{
		HomeID:        normalizeHomePathHeadHomeID(homeID),
		SourceSliceID: sourceSliceID,
		Checked:       len(materializedByPath),
		Drifts:        drifts,
	}, nil
}
