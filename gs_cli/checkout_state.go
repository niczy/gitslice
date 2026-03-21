package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

type checkoutTrackedFile struct {
	Path                 string `json:"path"`
	Hash                 string `json:"hash,omitempty"`
	Executable           bool   `json:"executable,omitempty"`
	SymlinkTarget        string `json:"symlink_target,omitempty"`
	Size                 int64  `json:"size,omitempty"`
	ModifiedTimeUnixNano int64  `json:"modified_time_unix_nano,omitempty"`
	ChangeTimeUnixNano   int64  `json:"change_time_unix_nano,omitempty"`
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
		info, err := os.Lstat(fullPath)
		if err != nil || info.IsDir() {
			mismatches = append(mismatches, file.Path)
			continue
		}
		matches, err := checkoutTrackedFileMatches(fullPath, info, file)
		if err != nil {
			mismatches = append(mismatches, file.Path)
			continue
		}
		if !matches {
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
		if a.Files[i].Path != b.Files[i].Path ||
			a.Files[i].Hash != b.Files[i].Hash ||
			a.Files[i].Executable != b.Files[i].Executable ||
			a.Files[i].SymlinkTarget != b.Files[i].SymlinkTarget {
			return false
		}
	}
	return true
}

func detectCheckoutMode(dir string) (*localCheckoutState, bool, error) {
	state, err := readCheckoutState(dir)
	if err != nil {
		return nil, false, err
	}
	if state != nil {
		return state, state.GitEnabled, nil
	}

	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err == nil && info.IsDir() {
		return nil, true, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	return nil, false, nil
}

func detectNoGitModifiedFiles(dir string, state *localCheckoutState) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("no checkout state available; pass --files or re-checkout the slice")
	}

	originalFiles := make(map[string]checkoutTrackedFile, len(state.Files))
	for _, file := range state.Files {
		cleaned := filepath.Clean(strings.TrimSpace(file.Path))
		if cleaned == "" || cleaned == "." {
			continue
		}
		originalFiles[cleaned] = checkoutTrackedFile{
			Path:                 cleaned,
			Hash:                 strings.TrimSpace(file.Hash),
			Executable:           file.Executable,
			SymlinkTarget:        file.SymlinkTarget,
			Size:                 file.Size,
			ModifiedTimeUnixNano: file.ModifiedTimeUnixNano,
			ChangeTimeUnixNano:   file.ChangeTimeUnixNano,
		}
	}
	changed := make([]string, 0)
	for path, original := range originalFiles {
		fullPath := filepath.Join(dir, path)
		info, err := os.Lstat(fullPath)
		if errors.Is(err, os.ErrNotExist) {
			changed = append(changed, path)
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			changed = append(changed, path)
			continue
		}
		matches, err := checkoutTrackedFileMatches(fullPath, info, original)
		if err != nil {
			return nil, err
		}
		if !matches {
			changed = append(changed, path)
		}
	}

	newFiles, err := scanCheckoutForNewFiles(dir, "", originalFiles)
	if err != nil {
		return nil, err
	}
	changed = append(changed, newFiles...)
	sort.Strings(changed)
	return uniqueCheckoutPaths(changed), nil
}

func enrichCheckoutStateWithLocalMetadata(dir string, state *localCheckoutState) (*localCheckoutState, error) {
	if state == nil {
		return nil, nil
	}

	enriched := &localCheckoutState{
		SliceID:    state.SliceID,
		CommitHash: state.CommitHash,
		GitEnabled: state.GitEnabled,
		Files:      make([]checkoutTrackedFile, 0, len(state.Files)),
	}
	for _, file := range state.Files {
		record := file
		if file.SymlinkTarget != "" {
			enriched.Files = append(enriched.Files, record)
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, file.Path))
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("expected file at %s, found directory", file.Path)
		}
		record.Size = info.Size()
		record.ModifiedTimeUnixNano = info.ModTime().UnixNano()
		record.ChangeTimeUnixNano = fileChangeTimeUnixNano(info)
		enriched.Files = append(enriched.Files, record)
	}
	return enriched, nil
}

func currentCheckoutFileRecord(fullPath, relPath string) (checkoutTrackedFile, error) {
	info, err := os.Lstat(fullPath)
	if err != nil {
		return checkoutTrackedFile{}, err
	}

	cleaned := filepath.Clean(strings.TrimSpace(relPath))
	record := checkoutTrackedFile{Path: cleaned}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return checkoutTrackedFile{}, err
		}
		record.SymlinkTarget = target
		record.Hash = storage.HashFileManifestContent([]byte(target), false, target)
		return record, nil
	}

	executable := info.Mode().Perm()&0o111 != 0
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return checkoutTrackedFile{}, err
	}
	record.Executable = executable
	record.Size = info.Size()
	record.ModifiedTimeUnixNano = info.ModTime().UnixNano()
	record.ChangeTimeUnixNano = fileChangeTimeUnixNano(info)
	record.Hash = storage.HashFileManifestContent(content, executable, "")
	return record, nil
}

func checkoutTrackedFileMatches(fullPath string, info os.FileInfo, original checkoutTrackedFile) (bool, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		if strings.TrimSpace(original.SymlinkTarget) == "" {
			return false, nil
		}
		target, err := os.Readlink(fullPath)
		if err != nil {
			return false, err
		}
		return target == original.SymlinkTarget, nil
	}

	if strings.TrimSpace(original.SymlinkTarget) != "" || info.IsDir() {
		return false, nil
	}

	executable := info.Mode().Perm()&0o111 != 0
	if executable != original.Executable {
		return false, nil
	}
	if original.ModifiedTimeUnixNano != 0 &&
		info.Size() == original.Size &&
		info.ModTime().UnixNano() == original.ModifiedTimeUnixNano &&
		(original.ChangeTimeUnixNano == 0 || fileChangeTimeUnixNano(info) == original.ChangeTimeUnixNano) {
		return true, nil
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return false, err
	}
	hash := storage.HashFileManifestContent(content, executable, "")
	return strings.TrimSpace(hash) == strings.TrimSpace(original.Hash), nil
}

func scanCheckoutForNewFiles(dir, relDir string, originalFiles map[string]checkoutTrackedFile) ([]string, error) {
	fullDir := dir
	if relDir != "" {
		fullDir = filepath.Join(dir, relDir)
	}

	entries, err := os.ReadDir(fullDir)
	if err != nil {
		return nil, err
	}

	newFiles := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".gs" {
			continue
		}

		relPath := name
		if relDir != "" {
			relPath = filepath.Join(relDir, name)
		}
		cleaned := filepath.Clean(relPath)

		if entry.IsDir() {
			childNewFiles, err := scanCheckoutForNewFiles(dir, cleaned, originalFiles)
			if err != nil {
				return nil, err
			}
			newFiles = append(newFiles, childNewFiles...)
			continue
		}
		if _, ok := originalFiles[cleaned]; ok {
			continue
		}
		newFiles = append(newFiles, cleaned)
	}

	return newFiles, nil
}

func fileChangeTimeUnixNano(info os.FileInfo) int64 {
	if info == nil || info.Sys() == nil {
		return 0
	}

	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0
	}

	for _, fieldName := range []string{"Ctim", "Ctimespec"} {
		field := value.FieldByName(fieldName)
		if !field.IsValid() || field.Kind() != reflect.Struct {
			continue
		}
		secField := field.FieldByName("Sec")
		nsecField := field.FieldByName("Nsec")
		if !secField.IsValid() || !nsecField.IsValid() {
			continue
		}
		return secField.Int()*1_000_000_000 + nsecField.Int()
	}

	return 0
}
