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

func normalizeHomePathHeadHomeID(homeID string) string {
	homeID = strings.TrimSpace(homeID)
	if strings.HasPrefix(homeID, homePathHeadSlicePrefix) {
		return strings.TrimSpace(strings.TrimPrefix(homeID, homePathHeadSlicePrefix))
	}
	return homeID
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

func cloneHomePathHead(head *models.HomePathHead) *models.HomePathHead {
	if head == nil {
		return nil
	}
	clone := *head
	return &clone
}

func normalizeHomePathHead(head *models.HomePathHead) (*models.HomePathHead, error) {
	if head == nil {
		return nil, ErrInvalidInput
	}
	normalized := *head
	normalized.HomeID = normalizeHomePathHeadHomeID(normalized.HomeID)
	normalized.Path = cleanRelativePath(normalized.Path)
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
	if normalized.ManifestHash == "" {
		normalized.ManifestHash = normalized.ContentHash
	}
	if normalized.ContentHash == "" {
		normalized.ContentHash = normalized.ManifestHash
	}
	if normalized.UpdatedAt.IsZero() {
		normalized.UpdatedAt = time.Now()
	}
	return &normalized, nil
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
				manifestHash := strings.TrimSpace(entry.Hash)
				if manifestHash == "" {
					manifest, err := st.GetFileManifest(ctx, sourceSliceID, entryPath)
					if err != nil {
						if errors.Is(err, ErrEntryNotFound) {
							continue
						}
						return nil, sourceSliceID, err
					}
					if manifest != nil {
						manifestHash = strings.TrimSpace(manifest.Hash)
					}
				}
				head, err := normalizeHomePathHead(&models.HomePathHead{
					HomeID:           homeID,
					Path:             entryPath,
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
