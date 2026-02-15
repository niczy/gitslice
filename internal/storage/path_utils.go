package storage

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// NOTE: This file intentionally duplicates a few path helpers used by
// internal/common. The storage package can't import internal/common because
// internal/common currently imports internal/storage (cycle).

func cleanRelativePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	cleaned := path.Clean("/" + trimmed)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func extractParentDirs(filePath string) []string {
	var dirs []string
	parts := strings.Split(filePath, "/")
	for i := 0; i < len(parts)-1; i++ {
		dirPath := strings.Join(parts[:i+1], "/")
		dirs = append(dirs, dirPath)
	}
	return dirs
}

func sortDirsByDepth(dirs map[string]bool) []string {
	var sorted []string
	for dir := range dirs {
		sorted = append(sorted, dir)
	}
	sort.Slice(sorted, func(i, j int) bool {
		depthI := strings.Count(sorted[i], "/")
		depthJ := strings.Count(sorted[j], "/")
		if depthI != depthJ {
			return depthI < depthJ
		}
		return sorted[i] < sorted[j]
	})
	return sorted
}

func generateEntryID(sliceID, p string) string {
	return fmt.Sprintf("%s:%s", sliceID, p)
}
