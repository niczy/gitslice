package sliceservice

import (
	"context"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type mergeBulkWriter interface {
	BulkWrite(ctx context.Context, fn func(st storage.Storage) error) error
}

type activeSlicesBatchReader interface {
	GetActiveSlicesForFiles(ctx context.Context, fileIDs []string) (map[string][]string, error)
}

type fileIndexerBatchWriter interface {
	AddFilesToSlice(ctx context.Context, fileIDs []string, sliceID string) error
}

type fileManifestHashBatchReader interface {
	GetFileManifestHashes(ctx context.Context, sliceID string, paths []string) (map[string]string, error)
}

type existingEntriesBatchReader interface {
	GetExistingEntriesByPaths(ctx context.Context, sliceID string, paths []string) (map[string]bool, error)
}

func withMergeStorage(ctx context.Context, st storage.Storage, fn func(storage.Storage) error) error {
	if bw, ok := st.(mergeBulkWriter); ok {
		return bw.BulkWrite(ctx, fn)
	}
	return fn(st)
}

func getActiveSlicesForFiles(ctx context.Context, st storage.Storage, fileIDs []string) (map[string][]string, error) {
	if batch, ok := st.(activeSlicesBatchReader); ok {
		return batch.GetActiveSlicesForFiles(ctx, fileIDs)
	}
	result := make(map[string][]string, len(fileIDs))
	for _, fileID := range normalizeModifiedFiles(fileIDs) {
		slices, err := st.GetActiveSlicesForFile(ctx, fileID)
		if err != nil {
			return nil, err
		}
		result[fileID] = slices
	}
	return result, nil
}

func addFilesToSlice(ctx context.Context, st storage.Storage, fileIDs []string, sliceID string) error {
	cleaned := normalizeModifiedFiles(fileIDs)
	if len(cleaned) == 0 {
		return nil
	}
	if batch, ok := st.(fileIndexerBatchWriter); ok {
		return batch.AddFilesToSlice(ctx, cleaned, strings.TrimSpace(sliceID))
	}
	for _, fileID := range cleaned {
		if err := st.AddFileToSlice(ctx, fileID, sliceID); err != nil {
			return err
		}
	}
	return nil
}

func getFileManifestHashes(ctx context.Context, st storage.Storage, sliceID string, paths []string) (map[string]string, error) {
	cleaned := normalizeModifiedFiles(paths)
	if len(cleaned) == 0 {
		return map[string]string{}, nil
	}
	if batch, ok := st.(fileManifestHashBatchReader); ok {
		return batch.GetFileManifestHashes(ctx, sliceID, cleaned)
	}
	result := make(map[string]string, len(cleaned))
	for _, filePath := range cleaned {
		manifest, err := st.GetFileManifest(ctx, sliceID, filePath)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				continue
			}
			return nil, err
		}
		if manifest != nil {
			result[filePath] = strings.TrimSpace(manifest.Hash)
		}
	}
	return result, nil
}

func getExistingEntriesByPaths(ctx context.Context, st storage.Storage, sliceID string, paths []string) (map[string]bool, error) {
	cleaned := normalizeModifiedFiles(paths)
	if len(cleaned) == 0 {
		return map[string]bool{}, nil
	}
	if batch, ok := st.(existingEntriesBatchReader); ok {
		return batch.GetExistingEntriesByPaths(ctx, sliceID, cleaned)
	}
	result := make(map[string]bool, len(cleaned))
	for _, filePath := range cleaned {
		entry, err := st.GetEntryByPath(ctx, sliceID, filePath)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				continue
			}
			return nil, err
		}
		result[filePath] = entry != nil
	}
	return result, nil
}

func cloneEntryPresence(entries map[string]bool) map[string]bool {
	if len(entries) == 0 {
		return map[string]bool{}
	}
	cloned := make(map[string]bool, len(entries))
	for path, exists := range entries {
		cloned[path] = exists
	}
	return cloned
}

func cloneManifestHashes(hashes map[string]string) map[string]string {
	if len(hashes) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(hashes))
	for path, hash := range hashes {
		cloned[path] = hash
	}
	return cloned
}

func cloneChangeRecords(changes []*models.FileChangeRecord) []*models.FileChangeRecord {
	if len(changes) == 0 {
		return nil
	}
	cloned := make([]*models.FileChangeRecord, 0, len(changes))
	for _, change := range changes {
		if change == nil {
			continue
		}
		copyChange := *change
		cloned = append(cloned, &copyChange)
	}
	return cloned
}
