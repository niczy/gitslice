package ci

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	ciinternal "github.com/niczy/gitslice/internal/ci"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type pathPrefixEntryReader interface {
	ListEntriesByPathPrefixes(ctx context.Context, sliceID string, prefixes []string) ([]*models.DirectoryEntry, error)
}

// UpdateManifestIndexForHome rebuilds the path-scoped CI manifest index for
// the current trusted head of a home slice.
func UpdateManifestIndexForHome(ctx context.Context, st storage.Storage, homeID string) error {
	return (&server{st: st}).updateManifestIndexForHome(ctx, homeID)
}

func (s *server) updateManifestIndexForHome(ctx context.Context, homeID string) error {
	homeID = strings.Trim(strings.TrimSpace(homeID), "/")
	if homeID == "" {
		return storage.ErrInvalidInput
	}
	homeSliceID := homeslice.IDForUsername(homeID)
	homeSlice, err := s.st.GetSlice(ctx, homeSliceID)
	if err != nil {
		return err
	}
	metadata, err := s.st.GetSliceMetadata(ctx, homeSliceID)
	if err != nil {
		return err
	}
	commitHash := strings.TrimSpace(metadata.HeadCommitHash)
	if commitHash == "" {
		return nil
	}

	now := time.Now()
	paths, err := s.homeManifestIndexCandidatePaths(ctx, homeSliceID, homeID, commitHash, homeSlice.Files)
	if err != nil {
		return err
	}
	sort.Strings(paths)
	seen := make(map[string]struct{}, len(paths))
	entries := make([]*storage.CIManifestIndexEntry, 0)
	for _, rawPath := range paths {
		storedPath := common.CleanRelativePath(rawPath)
		if storedPath == "" || path.Base(storedPath) != ciinternal.FolderManifestName {
			continue
		}
		if _, ok := seen[storedPath]; ok {
			continue
		}
		seen[storedPath] = struct{}{}
		entry, err := s.buildManifestIndexEntry(ctx, homeSliceID, homeID, commitHash, storedPath, now)
		if err != nil {
			return err
		}
		if entry != nil {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ManifestPath < entries[j].ManifestPath })
	return s.st.ReplaceCIManifestIndex(ctx, homeID, commitHash, entries)
}

func (s *server) homeManifestIndexCandidatePaths(ctx context.Context, homeSliceID string, homeID string, commitHash string, sliceFiles []string) ([]string, error) {
	seen := make(map[string]struct{}, len(sliceFiles))
	add := func(rawPath string) {
		cleaned := common.CleanRelativePath(rawPath)
		if cleaned == "" {
			return
		}
		seen[cleaned] = struct{}{}
	}
	for _, filePath := range sliceFiles {
		add(filePath)
	}
	if files, err := s.st.ListFilesAtCommit(ctx, commitHash, strings.Trim(homeID, "/")+"/"); err == nil {
		for _, filePath := range files {
			add(filePath)
		}
	} else if !errors.Is(err, storage.ErrCommitNotFound) && !errors.Is(err, storage.ErrEntryNotFound) {
		return nil, err
	}
	if reader, ok := s.st.(pathPrefixEntryReader); ok {
		entries, err := reader.ListEntriesByPathPrefixes(ctx, homeSliceID, []string{strings.Trim(homeID, "/")})
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry == nil || entry.Type == "directory" {
				continue
			}
			add(entry.Path)
		}
	}
	paths := make([]string, 0, len(seen))
	for filePath := range seen {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *server) buildManifestIndexEntry(ctx context.Context, homeSliceID string, homeID string, commitHash string, storedPath string, now time.Time) (*storage.CIManifestIndexEntry, error) {
	logicalPath := storagePathToLogical(homeID, storedPath)
	manifestDir, err := ciinternal.ManifestDir(logicalPath)
	if err != nil {
		return nil, err
	}
	content, err := storage.ReadSliceFileContent(ctx, s.st, homeSliceID, storedPath)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil, nil
		}
		return nil, err
	}
	entry := &storage.CIManifestIndexEntry{
		HomeID:         homeID,
		HomeCommitHash: commitHash,
		ManifestPath:   logicalPath,
		ManifestDir:    manifestDir,
		ManifestHash:   content.Hash,
		ParseStatus:    "ok",
		UpdatedAt:      now,
	}
	manifest, err := ciinternal.ParseFolderManifest(content.Content)
	if err != nil {
		entry.ParseStatus = "error"
		entry.ParseError = err.Error()
		return entry, nil
	}
	entry.WatchGlobs = append([]string(nil), manifest.Watch...)
	entry.IgnoreGlobs = append([]string(nil), manifest.Ignore...)
	entry.AppliesToGlobs = append([]string(nil), manifest.AppliesTo...)
	return entry, nil
}

func (s *server) indexedManifestPathsForChangedPaths(ctx context.Context, homeID string, changedPaths []string) ([]string, error) {
	homeID = strings.Trim(strings.TrimSpace(homeID), "/")
	if homeID == "" {
		return nil, nil
	}
	metadata, err := s.st.GetSliceMetadata(ctx, homeslice.IDForUsername(homeID))
	if err != nil {
		if errors.Is(err, storage.ErrSliceNotFound) {
			return nil, nil
		}
		return nil, err
	}
	commitHash := strings.TrimSpace(metadata.HeadCommitHash)
	if commitHash == "" {
		return nil, nil
	}
	entries, err := s.st.ListCIManifestIndex(ctx, homeID, commitHash)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		if err := s.updateManifestIndexForHome(ctx, homeID); err != nil {
			return nil, err
		}
		entries, err = s.st.ListCIManifestIndex(ctx, homeID, commitHash)
		if err != nil {
			return nil, err
		}
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry == nil || entry.ParseStatus != "ok" || len(entry.AppliesToGlobs) == 0 {
			continue
		}
		for _, changed := range changedPaths {
			ignored, err := ciinternal.MatchManifestPatterns(entry.IgnoreGlobs, entry.ManifestDir, changed)
			if err != nil {
				return nil, fmt.Errorf("%s ignore: %w", entry.ManifestPath, err)
			}
			if ignored {
				continue
			}
			matched, err := ciinternal.MatchManifestPatterns(entry.AppliesToGlobs, entry.ManifestDir, changed)
			if err != nil {
				return nil, fmt.Errorf("%s applies_to: %w", entry.ManifestPath, err)
			}
			if matched {
				seen[entry.ManifestPath] = struct{}{}
				break
			}
		}
	}
	paths := make([]string, 0, len(seen))
	for manifestPath := range seen {
		paths = append(paths, manifestPath)
	}
	sort.Strings(paths)
	return paths, nil
}
