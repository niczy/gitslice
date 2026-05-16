package storage

import (
	"strings"

	"github.com/niczy/gitslice/internal/models"
)

func pathHeadViewHomeIDForRootPath(filePath string) (string, bool) {
	filePath = cleanRelativePath(filePath)
	if filePath == "" {
		return "", false
	}
	homeID, _, _ := strings.Cut(filePath, "/")
	homeID = normalizeHomePathHeadHomeID(homeID)
	return homeID, homeID != ""
}

func pathHeadViewParentPath(sliceID, parentID string) (string, bool) {
	sliceID = strings.TrimSpace(sliceID)
	parentID = strings.TrimSpace(parentID)
	if sliceID == "" || parentID == "" {
		return "", false
	}
	if parentID == sliceID {
		return "", true
	}
	prefix := sliceID + ":"
	if !strings.HasPrefix(parentID, prefix) {
		return "", false
	}
	return cleanRelativePath(strings.TrimPrefix(parentID, prefix)), true
}

func pathHeadViewEntry(sliceID, filePath, entryType, manifestHash string, manifest *models.FileManifest) *models.DirectoryEntry {
	sliceID = strings.TrimSpace(sliceID)
	filePath = cleanRelativePath(filePath)
	entryType = normalizeHomePathHeadEntryType(entryType)
	if filePath == "" {
		entryType = homePathHeadEntryTypeDirectory
	}
	entry := &models.DirectoryEntry{
		ID:       nativeEntryID(sliceID, filePath),
		Path:     filePath,
		Type:     entryType,
		ParentID: nativeParentID(sliceID, filePath),
		Hash:     strings.TrimSpace(manifestHash),
	}
	if entry.Type == homePathHeadEntryTypeFile && manifest != nil {
		entry.Size = manifest.TotalSize
		entry.Hash = strings.TrimSpace(manifest.Hash)
		entry.Executable = manifest.Executable
		entry.SymlinkTarget = manifest.SymlinkTarget
	}
	return entry
}

func cloneManifestForPath(manifest *models.FileManifest, filePath, hash string) *models.FileManifest {
	if manifest == nil {
		return nil
	}
	clone := cloneManifest(manifest)
	clone.Path = cleanRelativePath(filePath)
	if trimmedHash := strings.TrimSpace(hash); trimmedHash != "" {
		clone.Hash = trimmedHash
	}
	return clone
}
