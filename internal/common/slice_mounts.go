package common

import (
	"path"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/models"
)

// CleanRelativePath normalizes a possibly-empty relative path.
func CleanRelativePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	cleaned := path.Clean("/" + trimmed)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func normalizedMounts(slice *models.Slice) []models.SliceFolderMount {
	if slice == nil || len(slice.FolderMounts) == 0 {
		return nil
	}

	mounts := make([]models.SliceFolderMount, 0, len(slice.FolderMounts))
	for _, mount := range slice.FolderMounts {
		source := CleanRelativePath(mount.SourcePath)
		alias := CleanRelativePath(mount.Alias)
		if source == "" || alias == "" {
			continue
		}
		mounts = append(mounts, models.SliceFolderMount{
			SourcePath: source,
			Alias:      alias,
		})
	}

	sort.Slice(mounts, func(i, j int) bool {
		if len(mounts[i].SourcePath) == len(mounts[j].SourcePath) {
			return mounts[i].SourcePath < mounts[j].SourcePath
		}
		return len(mounts[i].SourcePath) > len(mounts[j].SourcePath)
	})

	return mounts
}

// SliceDisplayPath maps a stored slice file path into the display path for the
// selected slice root.
func SliceDisplayPath(slice *models.Slice, storedPath string) string {
	cleaned := CleanRelativePath(storedPath)
	if cleaned == "" {
		return ""
	}

	for _, mount := range normalizedMounts(slice) {
		if cleaned == mount.SourcePath {
			return mount.Alias
		}
		prefix := mount.SourcePath + "/"
		if strings.HasPrefix(cleaned, prefix) {
			suffix := strings.TrimPrefix(cleaned, prefix)
			if suffix == "" {
				return mount.Alias
			}
			return path.Join(mount.Alias, suffix)
		}
	}

	return cleaned
}

// SliceStoredPath maps a display path under a slice root to the underlying
// stored path.
func SliceStoredPath(slice *models.Slice, displayPath string) string {
	cleaned := CleanRelativePath(displayPath)
	if cleaned == "" {
		return ""
	}

	for _, mount := range normalizedMounts(slice) {
		if cleaned == mount.Alias {
			return mount.SourcePath
		}
		prefix := mount.Alias + "/"
		if strings.HasPrefix(cleaned, prefix) {
			suffix := strings.TrimPrefix(cleaned, prefix)
			if suffix == "" {
				return mount.SourcePath
			}
			return path.Join(mount.SourcePath, suffix)
		}
	}

	return cleaned
}
