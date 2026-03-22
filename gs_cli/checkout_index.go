package main

import (
	"bufio"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

const (
	checkoutIndexMagic   = "GSINDEX1"
	checkoutIndexVersion = 1
)

type checkoutTrackedFile struct {
	Path                 string
	Hash                 string
	Executable           bool
	SymlinkTarget        string
	Size                 int64
	ModifiedTimeUnixNano int64
	ChangeTimeUnixNano   int64
	Device               uint64
	Inode                uint64
}

type checkoutTrackedDirectory struct {
	Path                 string
	ModifiedTimeUnixNano int64
	ChangeTimeUnixNano   int64
	Device               uint64
	Inode                uint64
	ChildCount           int
	ChildNameFingerprint uint64
}

type localCheckoutIndex struct {
	Version     uint32
	SliceID     string
	CommitHash  string
	GitEnabled  bool
	Files       []checkoutTrackedFile
	Directories []checkoutTrackedDirectory
}

func checkoutIndexFilePath(dir string) string {
	return filepath.Join(dir, checkoutIndexPath)
}

func readCheckoutIndex(dir string) (*localCheckoutIndex, error) {
	file, err := os.Open(checkoutIndexFilePath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	header := make([]byte, len(checkoutIndexMagic))
	if _, err := ioReadFull(file, header); err != nil {
		return nil, err
	}
	if string(header) != checkoutIndexMagic {
		return nil, fmt.Errorf("invalid checkout index header")
	}

	var index localCheckoutIndex
	if err := gob.NewDecoder(bufio.NewReader(file)).Decode(&index); err != nil {
		return nil, err
	}
	if index.Version != checkoutIndexVersion {
		return nil, fmt.Errorf("unsupported checkout index version %d", index.Version)
	}
	return &index, nil
}

func writeCheckoutIndex(dir string, index *localCheckoutIndex) error {
	if index == nil {
		return nil
	}
	index.Version = checkoutIndexVersion

	indexDir := filepath.Dir(checkoutIndexFilePath(dir))
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(indexDir, "checkout-index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		_ = tmpFile.Close()
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	writer := bufio.NewWriter(tmpFile)
	if _, err := writer.WriteString(checkoutIndexMagic); err != nil {
		return err
	}
	if err := gob.NewEncoder(writer).Encode(index); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, checkoutIndexFilePath(dir)); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}

func buildCheckoutIndex(dir, sliceID string, manifest *slicev1.SliceManifest, gitEnabled bool) (*localCheckoutIndex, error) {
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    strings.TrimSpace(sliceID),
		CommitHash: strings.TrimSpace(manifest.GetCommitHash()),
		GitEnabled: gitEnabled,
	}
	if manifest == nil {
		return index, nil
	}

	directoryPaths := map[string]struct{}{
		"": {},
	}
	index.Files = make([]checkoutTrackedFile, 0, len(manifest.GetFileMetadata()))
	for _, meta := range manifest.GetFileMetadata() {
		if meta == nil {
			continue
		}
		cleanedPath := filepath.Clean(strings.TrimSpace(meta.GetPath()))
		if cleanedPath == "" || cleanedPath == "." {
			continue
		}

		fullPath := filepath.Join(dir, cleanedPath)
		info, err := os.Lstat(fullPath)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			addParentDirectoryPaths(cleanedPath, directoryPaths)
			directoryPaths[normalizeTrackedDirectoryPath(cleanedPath)] = struct{}{}
			continue
		}

		record := checkoutTrackedFile{
			Path:          cleanedPath,
			Hash:          strings.TrimSpace(meta.GetHash()),
			Executable:    meta.GetExecutable(),
			SymlinkTarget: meta.GetSymlinkTarget(),
		}
		populateTrackedFileLocalMetadata(&record, info)
		index.Files = append(index.Files, record)
		addParentDirectoryPaths(cleanedPath, directoryPaths)
	}

	index.Directories = make([]checkoutTrackedDirectory, 0, len(directoryPaths))
	for dirPath := range directoryPaths {
		record, _, err := currentCheckoutDirectorySnapshot(dir, dirPath)
		if err != nil {
			return nil, err
		}
		index.Directories = append(index.Directories, record)
	}

	sort.Slice(index.Files, func(i, j int) bool {
		return index.Files[i].Path < index.Files[j].Path
	})
	sort.Slice(index.Directories, func(i, j int) bool {
		return index.Directories[i].Path < index.Directories[j].Path
	})
	return index, nil
}

func trackedCheckoutPathsForPrune(dir string) ([]string, error) {
	index, err := readCheckoutIndex(dir)
	if err != nil {
		return nil, err
	}
	if index == nil {
		return nil, fmt.Errorf("checkout metadata missing; run gs slice checkout again")
	}

	paths := make([]string, 0, len(index.Files))
	for _, file := range index.Files {
		cleaned := filepath.Clean(strings.TrimSpace(file.Path))
		if cleaned == "" || cleaned == "." {
			continue
		}
		paths = append(paths, cleaned)
	}
	sort.Strings(paths)
	return uniqueCheckoutPaths(paths), nil
}

func verifyCheckoutIndexClean(dir string, index *localCheckoutIndex) error {
	if index == nil {
		return nil
	}

	modified, err := detectNoGitModifiedFiles(dir, index)
	if err != nil {
		return err
	}
	if len(modified) == 0 {
		return nil
	}
	if len(modified) > 5 {
		modified = modified[:5]
	}
	return fmt.Errorf("working tree has local changes: %s", strings.Join(modified, ", "))
}

func checkoutIndicesEqualContent(a, b *localCheckoutIndex) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Files) != len(b.Files) || len(a.Directories) != len(b.Directories) {
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
	for i := range a.Directories {
		if a.Directories[i].Path != b.Directories[i].Path {
			return false
		}
	}
	return true
}

func detectCheckoutMode(dir string) (*localCheckoutIndex, bool, error) {
	index, err := readCheckoutIndex(dir)
	if err != nil {
		return nil, false, err
	}
	if index == nil {
		return nil, false, fmt.Errorf("checkout metadata missing; run gs slice checkout again")
	}
	return index, index.GitEnabled, nil
}

func detectNoGitModifiedFiles(dir string, index *localCheckoutIndex) ([]string, error) {
	if index == nil {
		return nil, fmt.Errorf("checkout metadata missing; run gs slice checkout again")
	}

	originalFiles, originalDirs := checkoutIndexMaps(index)
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

	newFiles, err := scanCheckoutForNewFiles(dir, "", originalFiles, originalDirs)
	if err != nil {
		return nil, err
	}
	changed = append(changed, newFiles...)
	sort.Strings(changed)
	return uniqueCheckoutPaths(changed), nil
}

func checkoutIndexMaps(index *localCheckoutIndex) (map[string]checkoutTrackedFile, map[string]checkoutTrackedDirectory) {
	originalFiles := make(map[string]checkoutTrackedFile, len(index.Files))
	for _, file := range index.Files {
		cleaned := filepath.Clean(strings.TrimSpace(file.Path))
		if cleaned == "" || cleaned == "." {
			continue
		}
		file.Path = cleaned
		originalFiles[cleaned] = file
	}

	originalDirs := make(map[string]checkoutTrackedDirectory, len(index.Directories))
	for _, dir := range index.Directories {
		dir.Path = normalizeTrackedDirectoryPath(dir.Path)
		originalDirs[dir.Path] = dir
	}
	if _, ok := originalDirs[""]; !ok {
		originalDirs[""] = checkoutTrackedDirectory{Path: ""}
	}
	return originalFiles, originalDirs
}

func populateTrackedFileLocalMetadata(record *checkoutTrackedFile, info os.FileInfo) {
	if record == nil || info == nil {
		return
	}
	record.Size = info.Size()
	record.ModifiedTimeUnixNano = info.ModTime().UnixNano()
	record.ChangeTimeUnixNano = fileChangeTimeUnixNano(info)
	record.Device, record.Inode = fileStatDeviceInode(info)
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

	currentDevice, currentInode := fileStatDeviceInode(info)
	if original.Device != 0 && original.Inode != 0 && currentDevice != 0 && currentInode != 0 {
		if currentDevice != original.Device || currentInode != original.Inode {
			return false, nil
		}
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

func scanCheckoutForNewFiles(
	dir, relDir string,
	originalFiles map[string]checkoutTrackedFile,
	originalDirs map[string]checkoutTrackedDirectory,
) ([]string, error) {
	fullDir := dir
	if relDir != "" {
		fullDir = filepath.Join(dir, relDir)
	}

	info, err := os.Lstat(fullDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if relDir == "" {
			return nil, nil
		}
		if _, ok := originalFiles[filepath.Clean(relDir)]; ok {
			return nil, nil
		}
		return []string{filepath.Clean(relDir)}, nil
	}

	normalizedDir := normalizeTrackedDirectoryPath(relDir)
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
		if normalizedDir != "" {
			relPath = filepath.Join(normalizedDir, name)
		}
		cleaned := filepath.Clean(relPath)

		if entry.IsDir() {
			if _, ok := originalDirs[normalizeTrackedDirectoryPath(cleaned)]; !ok {
				childNewFiles, err := collectAllFiles(dir, cleaned)
				if err != nil {
					return nil, err
				}
				newFiles = append(newFiles, childNewFiles...)
				continue
			}
			childNewFiles, err := scanCheckoutForNewFiles(dir, cleaned, originalFiles, originalDirs)
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

func collectAllFiles(dir, relDir string) ([]string, error) {
	fullDir := filepath.Join(dir, relDir)
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		return nil, err
	}

	files := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".gs" {
			continue
		}

		relPath := filepath.Join(relDir, name)
		if entry.IsDir() {
			childFiles, err := collectAllFiles(dir, relPath)
			if err != nil {
				return nil, err
			}
			files = append(files, childFiles...)
			continue
		}
		files = append(files, filepath.Clean(relPath))
	}
	return files, nil
}

func addParentDirectoryPaths(path string, directories map[string]struct{}) {
	dir := normalizeTrackedDirectoryPath(filepath.Dir(path))
	for {
		directories[dir] = struct{}{}
		if dir == "" {
			return
		}
		next := normalizeTrackedDirectoryPath(filepath.Dir(dir))
		if next == dir {
			return
		}
		dir = next
	}
}

func normalizeTrackedDirectoryPath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return cleaned
}

func currentCheckoutDirectorySnapshot(rootDir, relDir string) (checkoutTrackedDirectory, []os.DirEntry, error) {
	fullDir := rootDir
	if relDir != "" {
		fullDir = filepath.Join(rootDir, relDir)
	}

	info, err := os.Lstat(fullDir)
	if err != nil {
		return checkoutTrackedDirectory{}, nil, err
	}
	if !info.IsDir() {
		return checkoutTrackedDirectory{}, nil, fmt.Errorf("expected directory at %s", fullDir)
	}

	entries, err := os.ReadDir(fullDir)
	if err != nil {
		return checkoutTrackedDirectory{}, nil, err
	}

	filtered := make([]os.DirEntry, 0, len(entries))
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".gs" {
			continue
		}
		filtered = append(filtered, entry)
		names = append(names, name)
	}

	device, inode := fileStatDeviceInode(info)
	return checkoutTrackedDirectory{
		Path:                 normalizeTrackedDirectoryPath(relDir),
		ModifiedTimeUnixNano: info.ModTime().UnixNano(),
		ChangeTimeUnixNano:   fileChangeTimeUnixNano(info),
		Device:               device,
		Inode:                inode,
		ChildCount:           len(filtered),
		ChildNameFingerprint: childNameFingerprint(names),
	}, filtered, nil
}

func childNameFingerprint(names []string) uint64 {
	if len(names) == 0 {
		return 0
	}
	sort.Strings(names)
	hasher := fnv.New64a()
	for _, name := range names {
		_, _ = hasher.Write([]byte(name))
		_, _ = hasher.Write([]byte{0})
	}
	return hasher.Sum64()
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

func fileStatDeviceInode(info os.FileInfo) (uint64, uint64) {
	if info == nil || info.Sys() == nil {
		return 0, 0
	}

	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, 0
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0, 0
	}

	devField := value.FieldByName("Dev")
	inoField := value.FieldByName("Ino")
	if !devField.IsValid() || !inoField.IsValid() {
		return 0, 0
	}
	return valueToUint64(devField), valueToUint64(inoField)
}

func valueToUint64(value reflect.Value) uint64 {
	switch value.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value.Int() < 0 {
			return 0
		}
		return uint64(value.Int())
	default:
		return 0
	}
}

func ioReadFull(file *os.File, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := file.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}
