package homeslice

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type PromotionResult struct {
	HomeSliceID        string
	RootSliceID        string
	RootPath           string
	FilesUpserted      int
	FilesRemoved       int
	DirectoriesCreated int
	DirectoriesRemoved int
}

func IsHomeSliceID(sliceID string) bool {
	return strings.HasPrefix(strings.TrimSpace(sliceID), idPrefix)
}

func UsernameFromSliceID(sliceID string) string {
	if !IsHomeSliceID(sliceID) {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(sliceID), idPrefix)
}

func SyncHomeSliceToRoot(ctx context.Context, st storage.Storage, homeSliceID string) (*PromotionResult, error) {
	if st == nil {
		return nil, fmt.Errorf("storage is nil")
	}
	homeSliceID = strings.TrimSpace(homeSliceID)
	if !IsHomeSliceID(homeSliceID) {
		return nil, fmt.Errorf("slice %q is not a home slice", homeSliceID)
	}
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		return nil, err
	}

	homeSlice, err := st.GetSlice(ctx, homeSliceID)
	if err != nil {
		return nil, err
	}
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		return nil, err
	}

	username := resolveHomeSliceUsername(homeSlice)
	if username == "" {
		return nil, fmt.Errorf("home slice %q has no associated username", homeSliceID)
	}
	rootPath := rootPathForUsername(ctx, st, username)

	if _, err := ensureDirectory(ctx, st, homeSlice.ID, rootPath); err != nil {
		return nil, err
	}
	if _, err := ensureDirectory(ctx, st, rootSlice.ID, rootPath); err != nil {
		return nil, err
	}

	homeEntries, err := collectSubtreeEntries(ctx, st, homeSlice.ID, rootPath)
	if err != nil {
		return nil, err
	}
	rootEntries, err := collectSubtreeEntries(ctx, st, rootSlice.ID, rootPath)
	if err != nil {
		return nil, err
	}

	homeByPath := entriesByPath(homeEntries)
	rootByPath := entriesByPath(rootEntries)
	result := &PromotionResult{
		HomeSliceID: homeSlice.ID,
		RootSliceID: rootSlice.ID,
		RootPath:    rootPath,
	}

	deleteEntries := make([]*models.DirectoryEntry, 0)
	for filePath, rootEntry := range rootByPath {
		if filePath == rootPath {
			continue
		}
		homeEntry, ok := homeByPath[filePath]
		if !ok || homeEntry.Type != rootEntry.Type {
			deleteEntries = append(deleteEntries, rootEntry)
		}
	}
	sort.Slice(deleteEntries, func(i, j int) bool {
		leftDepth := pathDepth(deleteEntries[i].Path)
		rightDepth := pathDepth(deleteEntries[j].Path)
		if leftDepth != rightDepth {
			return leftDepth > rightDepth
		}
		return deleteEntries[i].Path > deleteEntries[j].Path
	})
	for _, entry := range deleteEntries {
		if entry == nil {
			continue
		}
		if entry.Type == "file" {
			if err := st.RemoveFileFromSlice(ctx, entry.Path, rootSlice.ID); err != nil {
				return nil, err
			}
			result.FilesRemoved++
		} else {
			result.DirectoriesRemoved++
		}
		if err := st.DeleteEntry(ctx, entry.ID); err != nil && err != storage.ErrEntryNotFound {
			return nil, err
		}
	}

	directories := make([]*models.DirectoryEntry, 0, len(homeEntries))
	files := make([]*models.DirectoryEntry, 0, len(homeEntries))
	for _, entry := range homeEntries {
		if entry == nil {
			continue
		}
		if entry.Type == "directory" {
			directories = append(directories, entry)
		} else if entry.Type == "file" {
			files = append(files, entry)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		leftDepth := pathDepth(directories[i].Path)
		rightDepth := pathDepth(directories[j].Path)
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return directories[i].Path < directories[j].Path
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	for _, entry := range directories {
		if entry == nil {
			continue
		}
		created := false
		if existing, ok := rootByPath[entry.Path]; !ok || existing == nil || existing.Type != "directory" {
			created = true
		}
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(rootSlice.ID, entry.Path),
			Path:     entry.Path,
			Type:     "directory",
			ParentID: rootSlice.ID,
		}); err != nil {
			return nil, err
		}
		if created {
			result.DirectoriesCreated++
		}
	}

	for _, entry := range files {
		if entry == nil {
			continue
		}
		content, err := storage.ReadSliceFileContent(ctx, st, homeSlice.ID, entry.Path)
		if err != nil {
			return nil, err
		}
		hash := strings.TrimSpace(content.Hash)
		if hash == "" {
			hash = entry.Hash
		}
		manifest, err := storage.WriteSliceFileManifest(ctx, st, rootSlice.ID, entry.Path, append([]byte(nil), content.Content...))
		if err != nil {
			return nil, err
		}
		if err := st.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(rootSlice.ID, entry.Path),
			Path:     entry.Path,
			Type:     "file",
			ParentID: rootSlice.ID,
			Size:     content.Size,
			Hash:     manifest.Hash,
		}); err != nil {
			return nil, err
		}
		if err := st.AddFileToSlice(ctx, entry.Path, rootSlice.ID); err != nil {
			return nil, err
		}
		result.FilesUpserted++
	}

	return result, nil
}

func entriesByPath(entries []*models.DirectoryEntry) map[string]*models.DirectoryEntry {
	result := make(map[string]*models.DirectoryEntry, len(entries))
	for _, entry := range entries {
		if entry == nil || strings.TrimSpace(entry.Path) == "" {
			continue
		}
		result[entry.Path] = entry
	}
	return result
}

func pathDepth(value string) int {
	trimmed := strings.Trim(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "/") + 1
}

func resolveHomeSliceUsername(slice *models.Slice) string {
	if slice == nil {
		return ""
	}
	for _, owner := range slice.Owners {
		if strings.TrimSpace(owner) != "" {
			return strings.TrimSpace(owner)
		}
	}
	if strings.TrimSpace(slice.CreatedBy) != "" {
		return strings.TrimSpace(slice.CreatedBy)
	}
	return UsernameFromSliceID(slice.ID)
}

func rootPathForUsername(ctx context.Context, st storage.Storage, username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	user, err := st.GetUser(ctx, username)
	if err == nil {
		rootPath := strings.TrimPrefix(strings.TrimSpace(user.RootPath), "/")
		if rootPath != "" {
			return rootPath
		}
	}
	return RelativeRootPath(username)
}
