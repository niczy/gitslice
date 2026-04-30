package gscli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niczy/gitslice/internal/searchindex"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

type localSliceSearchArtifactMetadata struct {
	Version    uint32 `json:"version"`
	SliceID    string `json:"slice_id"`
	CommitHash string `json:"commit_hash"`
	Source     string `json:"source"`
}

type localSliceSearchOverlayState struct {
	Version      uint32   `json:"version"`
	SliceID      string   `json:"slice_id"`
	CommitHash   string   `json:"commit_hash"`
	RemovedPaths []string `json:"removed_paths,omitempty"`
}

func localSearchArtifactBaseFilePath(dir string) string {
	return filepath.Join(dir, searchArtifactBasePath)
}

func localSearchArtifactMetadataFilePath(dir string) string {
	return filepath.Join(dir, searchArtifactMetadataPath)
}

func localSearchArtifactOverlayDirPath(dir string) string {
	return filepath.Join(dir, searchArtifactOverlayDir)
}

func localSearchArtifactOverlayFilePath(dir string) string {
	return filepath.Join(localSearchArtifactOverlayDirPath(dir), "artifact")
}

func localSearchArtifactOverlayStateFilePath(dir string) string {
	return filepath.Join(localSearchArtifactOverlayDirPath(dir), "state.json")
}

func ensureLocalSliceSearchArtifact(ctx context.Context, cli *CLI, dir, sliceID string, manifest *slicev1.SliceManifest) error {
	if manifest == nil {
		return nil
	}
	commitHash := strings.TrimSpace(manifest.GetCommitHash())
	if sliceID == "" || commitHash == "" {
		return nil
	}
	if artifactUpToDate(dir, sliceID, commitHash) {
		return ensureCleanLocalSearchOverlay(dir)
	}

	artifact, source, err := resolveLocalSliceSearchArtifact(ctx, cli, dir, sliceID, manifest)
	if err != nil {
		return err
	}
	if err := writeLocalSliceSearchArtifact(dir, artifact, &localSliceSearchArtifactMetadata{
		Version:    artifact.Version,
		SliceID:    sliceID,
		CommitHash: commitHash,
		Source:     source,
	}); err != nil {
		return err
	}
	return ensureCleanLocalSearchOverlay(dir)
}

func artifactUpToDate(dir, sliceID, commitHash string) bool {
	meta, err := readLocalSliceSearchArtifactMetadata(dir)
	if err != nil || meta == nil {
		return false
	}
	if meta.Version != searchindex.CurrentArtifactVersion ||
		strings.TrimSpace(meta.SliceID) != strings.TrimSpace(sliceID) ||
		strings.TrimSpace(meta.CommitHash) != strings.TrimSpace(commitHash) {
		return false
	}
	if _, err := os.Stat(localSearchArtifactBaseFilePath(dir)); err != nil {
		return false
	}
	return true
}

func resolveLocalSliceSearchArtifact(ctx context.Context, cli *CLI, dir, sliceID string, manifest *slicev1.SliceManifest) (*searchindex.SliceArtifact, string, error) {
	var remoteErr error
	if os.Getenv("GS_DISABLE_SLICE_SEARCH_ARTIFACT_DOWNLOAD") == "" {
		artifact, err := fetchSliceSearchArtifact(ctx, cli, sliceID, manifest.GetCommitHash())
		if err == nil {
			return artifact, "downloaded", nil
		}
		remoteErr = err
	}

	artifact, err := buildLocalSliceSearchArtifact(dir, sliceID, manifest)
	if err == nil {
		return artifact, "rebuilt_local", nil
	}
	if remoteErr != nil {
		return nil, "", fmt.Errorf("failed to fetch or rebuild slice search artifact: fetch=%w rebuild=%v", remoteErr, err)
	}
	return nil, "", err
}

func buildLocalSliceSearchArtifact(dir, sliceID string, manifest *slicev1.SliceManifest) (*searchindex.SliceArtifact, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is nil")
	}
	inputs := make([]searchindex.ArtifactInputFile, 0, len(manifest.GetFileMetadata()))
	for _, file := range manifest.GetFileMetadata() {
		if file == nil {
			continue
		}
		if strings.TrimSpace(file.GetPath()) == "" || strings.TrimSpace(file.GetSymlinkTarget()) != "" {
			continue
		}

		absPath := filepath.Join(dir, filepath.FromSlash(file.GetPath()))
		info, err := os.Lstat(absPath)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", file.GetPath(), err)
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file.GetPath(), err)
		}
		blob, err := searchindex.BuildFileBlob(content, nil, searchindex.SparseModeCovering)
		if err != nil {
			if errors.Is(err, searchindex.ErrNonIndexableText) {
				continue
			}
			return nil, fmt.Errorf("build search blob for %s: %w", file.GetPath(), err)
		}
		inputs = append(inputs, searchindex.ArtifactInputFile{
			Path:              filepath.ToSlash(filepath.Clean(file.GetPath())),
			SearchContentHash: blob.SearchContentHash,
			NGrams:            blob.NGrams,
		})
	}
	return searchindex.BuildSliceArtifact(sliceID, strings.TrimSpace(manifest.GetCommitHash()), inputs), nil
}

func buildLocalSliceSearchArtifactFromIndex(dir string, index *localCheckoutIndex) (*searchindex.SliceArtifact, error) {
	if index == nil {
		return nil, fmt.Errorf("checkout index is nil")
	}
	manifest := &slicev1.SliceManifest{
		CommitHash:   index.CommitHash,
		FileMetadata: make([]*slicev1.FileMetadata, 0, len(index.Files)),
	}
	for _, record := range index.Files {
		manifest.FileMetadata = append(manifest.FileMetadata, &slicev1.FileMetadata{
			Path:          record.Path,
			SymlinkTarget: record.SymlinkTarget,
		})
	}
	return buildLocalSliceSearchArtifact(dir, index.SliceID, manifest)
}

func writeLocalSliceSearchArtifact(dir string, artifact *searchindex.SliceArtifact, metadata *localSliceSearchArtifactMetadata) error {
	if artifact == nil {
		return fmt.Errorf("artifact is nil")
	}
	if metadata == nil {
		return fmt.Errorf("artifact metadata is nil")
	}
	if err := os.MkdirAll(filepath.Join(dir, searchArtifactDirPath), 0o755); err != nil {
		return err
	}

	payload, err := searchindex.EncodeSliceArtifact(artifact)
	if err != nil {
		return err
	}
	if err := writeAtomicFile(localSearchArtifactBaseFilePath(dir), payload, 0o600); err != nil {
		return err
	}

	rawMetadata, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomicFile(localSearchArtifactMetadataFilePath(dir), append(rawMetadata, '\n'), 0o600); err != nil {
		return err
	}
	return nil
}

func readLocalSliceSearchArtifactMetadata(dir string) (*localSliceSearchArtifactMetadata, error) {
	raw, err := os.ReadFile(localSearchArtifactMetadataFilePath(dir))
	if err != nil {
		return nil, err
	}
	var metadata localSliceSearchArtifactMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func readLocalSliceSearchBaseArtifact(dir string) (*searchindex.SliceArtifact, error) {
	raw, err := os.ReadFile(localSearchArtifactBaseFilePath(dir))
	if err != nil {
		return nil, err
	}
	return searchindex.DecodeSliceArtifact(raw)
}

func buildLocalSliceSearchOverlay(dir string, index *localCheckoutIndex, entries []workingTreeStatusEntry) (*searchindex.SliceArtifact, *localSliceSearchOverlayState, error) {
	if index == nil {
		return nil, nil, fmt.Errorf("checkout index is nil")
	}
	inputs := make([]searchindex.ArtifactInputFile, 0, len(entries))
	removedPaths := make([]string, 0, len(entries))
	seenRemoved := make(map[string]struct{}, len(entries))

	for _, entry := range entries {
		path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(entry.Path)))
		if path == "." || path == "" {
			continue
		}
		if entry.Status == "deleted" {
			if _, ok := seenRemoved[path]; !ok {
				removedPaths = append(removedPaths, path)
				seenRemoved[path] = struct{}{}
			}
			continue
		}

		absPath := filepath.Join(dir, filepath.FromSlash(path))
		info, err := os.Lstat(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				if _, ok := seenRemoved[path]; !ok {
					removedPaths = append(removedPaths, path)
					seenRemoved[path] = struct{}{}
				}
				continue
			}
			return nil, nil, fmt.Errorf("stat %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			if _, ok := seenRemoved[path]; !ok {
				removedPaths = append(removedPaths, path)
				seenRemoved[path] = struct{}{}
			}
			continue
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", path, err)
		}
		blob, err := searchindex.BuildFileBlob(content, nil, searchindex.SparseModeCovering)
		if err != nil {
			if errors.Is(err, searchindex.ErrNonIndexableText) {
				if _, ok := seenRemoved[path]; !ok {
					removedPaths = append(removedPaths, path)
					seenRemoved[path] = struct{}{}
				}
				continue
			}
			return nil, nil, fmt.Errorf("build overlay blob for %s: %w", path, err)
		}
		inputs = append(inputs, searchindex.ArtifactInputFile{
			Path:              path,
			SearchContentHash: blob.SearchContentHash,
			NGrams:            blob.NGrams,
		})
	}

	artifact := searchindex.BuildSliceArtifact(index.SliceID, index.CommitHash, inputs)
	return artifact, &localSliceSearchOverlayState{
		Version:      searchindex.CurrentArtifactVersion,
		SliceID:      index.SliceID,
		CommitHash:   index.CommitHash,
		RemovedPaths: uniqueCheckoutPaths(removedPaths),
	}, nil
}

func writeLocalSliceSearchOverlay(dir string, artifact *searchindex.SliceArtifact, state *localSliceSearchOverlayState) error {
	if err := ensureCleanLocalSearchOverlay(dir); err != nil {
		return err
	}
	if artifact == nil {
		return fmt.Errorf("overlay artifact is nil")
	}
	if state == nil {
		return fmt.Errorf("overlay state is nil")
	}

	payload, err := searchindex.EncodeSliceArtifact(artifact)
	if err != nil {
		return err
	}
	if err := writeAtomicFile(localSearchArtifactOverlayFilePath(dir), payload, 0o600); err != nil {
		return err
	}
	rawState, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(localSearchArtifactOverlayStateFilePath(dir), append(rawState, '\n'), 0o600)
}

func readLocalSliceSearchOverlay(dir string) (*searchindex.SliceArtifact, *localSliceSearchOverlayState, error) {
	rawArtifact, err := os.ReadFile(localSearchArtifactOverlayFilePath(dir))
	if err != nil {
		return nil, nil, err
	}
	artifact, err := searchindex.DecodeSliceArtifact(rawArtifact)
	if err != nil {
		return nil, nil, err
	}

	rawState, err := os.ReadFile(localSearchArtifactOverlayStateFilePath(dir))
	if err != nil {
		return nil, nil, err
	}
	var state localSliceSearchOverlayState
	if err := json.Unmarshal(rawState, &state); err != nil {
		return nil, nil, err
	}
	return artifact, &state, nil
}

func ensureCleanLocalSearchOverlay(dir string) error {
	overlayDir := localSearchArtifactOverlayDirPath(dir)
	if err := os.RemoveAll(overlayDir); err != nil {
		return err
	}
	return os.MkdirAll(overlayDir, 0o755)
}

func writeAtomicFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, content, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
