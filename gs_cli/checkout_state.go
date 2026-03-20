package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

type checkoutTrackedFile struct {
	Path          string `json:"path"`
	Hash          string `json:"hash,omitempty"`
	Executable    bool   `json:"executable,omitempty"`
	SymlinkTarget string `json:"symlink_target,omitempty"`
}

type localCheckoutState struct {
	SliceID    string                `json:"slice_id"`
	CommitHash string                `json:"commit_hash,omitempty"`
	GitEnabled bool                  `json:"git_enabled"`
	Files      []checkoutTrackedFile `json:"files,omitempty"`
}

func checkoutStateFilePath(dir string) string {
	return filepath.Join(dir, checkoutStatePath)
}

func readCheckoutState(dir string) (*localCheckoutState, error) {
	data, err := os.ReadFile(checkoutStateFilePath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var state localCheckoutState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func writeCheckoutState(dir string, state *localCheckoutState) error {
	if state == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(checkoutStateFilePath(dir)), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(checkoutStateFilePath(dir), data, 0o600)
}

func checkoutStateFromManifest(sliceID string, manifest *slicev1.SliceManifest, gitEnabled bool) *localCheckoutState {
	state := &localCheckoutState{
		SliceID:    strings.TrimSpace(sliceID),
		GitEnabled: gitEnabled,
	}
	if manifest == nil {
		return state
	}

	state.CommitHash = strings.TrimSpace(manifest.GetCommitHash())
	directoryMarkers := checkoutDirectoryMarkers(manifest.GetFileMetadata())
	state.Files = make([]checkoutTrackedFile, 0, len(manifest.GetFileMetadata()))
	for _, meta := range manifest.GetFileMetadata() {
		if meta == nil {
			continue
		}
		cleanedPath := filepath.Clean(strings.TrimSpace(meta.GetPath()))
		if cleanedPath == "" || cleanedPath == "." {
			continue
		}
		if _, ok := directoryMarkers[cleanedPath]; ok {
			continue
		}
		state.Files = append(state.Files, checkoutTrackedFile{
			Path:          cleanedPath,
			Hash:          strings.TrimSpace(meta.GetHash()),
			Executable:    meta.GetExecutable(),
			SymlinkTarget: meta.GetSymlinkTarget(),
		})
	}
	sort.Slice(state.Files, func(i, j int) bool {
		return state.Files[i].Path < state.Files[j].Path
	})
	return state
}

func trackedCheckoutPathsForPrune(dir string) ([]string, error) {
	state, err := readCheckoutState(dir)
	if err != nil {
		return nil, err
	}
	if state != nil {
		paths := make([]string, 0, len(state.Files))
		for _, file := range state.Files {
			cleaned := filepath.Clean(strings.TrimSpace(file.Path))
			if cleaned == "" || cleaned == "." {
				continue
			}
			paths = append(paths, cleaned)
		}
		sort.Strings(paths)
		return uniqueCheckoutPaths(paths), nil
	}
	return gitTrackedFiles(dir)
}

func verifyCheckoutStateClean(dir string, state *localCheckoutState) error {
	if state == nil {
		return nil
	}

	mismatches := make([]string, 0)
	for _, file := range state.Files {
		if len(mismatches) >= 5 {
			break
		}
		fullPath := filepath.Join(dir, file.Path)
		if file.SymlinkTarget != "" {
			target, err := os.Readlink(fullPath)
			if err != nil || target != file.SymlinkTarget {
				mismatches = append(mismatches, file.Path)
			}
			continue
		}

		info, err := os.Lstat(fullPath)
		if err != nil || info.IsDir() {
			mismatches = append(mismatches, file.Path)
			continue
		}
		executable := info.Mode().Perm()&0o111 != 0
		if executable != file.Executable {
			mismatches = append(mismatches, file.Path)
			continue
		}
		content, err := os.ReadFile(fullPath)
		if err != nil {
			mismatches = append(mismatches, file.Path)
			continue
		}
		hash := storage.HashFileManifestContent(content, executable, "")
		if strings.TrimSpace(hash) != strings.TrimSpace(file.Hash) {
			mismatches = append(mismatches, file.Path)
		}
	}

	if len(mismatches) > 0 {
		return fmt.Errorf("working tree has local changes: %s", strings.Join(mismatches, ", "))
	}
	return nil
}

func checkoutStatesEqualContent(a, b *localCheckoutState) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if a.Files[i] != b.Files[i] {
			return false
		}
	}
	return true
}
