package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	dirtyTrackerStatePath   = ".gs/dirty_state.json"
	dirtyTrackerPathsPath   = ".gs/dirty_paths.json"
	dirtyTrackerLogPath     = ".gs/dirty_watcher.log"
	dirtyTrackerVersion     = 1
	dirtyTrackerStatusAlive = "active"
	dirtyTrackerStatusError = "error"
	dirtyTrackerStatusStop  = "stopped"
)

type dirtyTrackerState struct {
	Version    int    `json:"version"`
	SliceID    string `json:"slice_id"`
	CommitHash string `json:"commit_hash"`
	Generation string `json:"generation"`
	Checkout   string `json:"checkout"`
	PID        int    `json:"pid"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	Error      string `json:"error,omitempty"`
}

func dirtyTrackerStateFilePath(dir string) string {
	return filepath.Join(dir, dirtyTrackerStatePath)
}

func dirtyTrackerPathsFilePath(dir string) string {
	return filepath.Join(dir, dirtyTrackerPathsPath)
}

func dirtyTrackerLogFilePath(dir string) string {
	return filepath.Join(dir, dirtyTrackerLogPath)
}

func dirtyTrackerDisabled() bool {
	return strings.TrimSpace(os.Getenv("GS_DISABLE_DIRTY_TRACKER")) == "1"
}

func readDirtyTrackerState(dir string) (*dirtyTrackerState, error) {
	raw, err := os.ReadFile(dirtyTrackerStateFilePath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var state dirtyTrackerState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.Version != dirtyTrackerVersion {
		return nil, nil
	}
	return &state, nil
}

func writeDirtyTrackerState(dir string, state *dirtyTrackerState) error {
	if state == nil {
		return nil
	}
	state.Version = dirtyTrackerVersion
	if err := os.MkdirAll(filepath.Dir(dirtyTrackerStateFilePath(dir)), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(dirtyTrackerStateFilePath(dir)), "dirty-state-*.json")
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
	if _, err := tmpFile.Write(raw); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dirtyTrackerStateFilePath(dir)); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}

func readDirtyTrackerPaths(dir string) ([]string, error) {
	raw, err := os.ReadFile(dirtyTrackerPathsFilePath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	if err := json.Unmarshal(raw, &paths); err != nil {
		return nil, err
	}
	return normalizeDirtyTrackerPaths(paths), nil
}

func writeDirtyTrackerPaths(dir string, paths []string) error {
	if err := os.MkdirAll(filepath.Dir(dirtyTrackerPathsFilePath(dir)), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(normalizeDirtyTrackerPaths(paths))
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(dirtyTrackerPathsFilePath(dir)), "dirty-paths-*.json")
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
	if _, err := tmpFile.Write(raw); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dirtyTrackerPathsFilePath(dir)); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}

func resetDirtyTracker(dir string, index *localCheckoutIndex) error {
	if dirtyTrackerDisabled() || index == nil || index.GitEnabled {
		return nil
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := stopDirtyTracker(absDir); err != nil {
		return err
	}
	if err := writeDirtyTrackerPaths(absDir, nil); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(dirtyTrackerLogFilePath(absDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	generation := fmt.Sprintf("%d", time.Now().UnixNano())
	cmd := exec.Command(
		exe,
		"__watch-checkout",
		"--dir", absDir,
		"--generation", generation,
		"--slice", strings.TrimSpace(index.SliceID),
		"--commit", strings.TrimSpace(index.CommitHash),
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return waitForDirtyTracker(absDir, generation, 3*time.Second)
}

func stopDirtyTracker(dir string) error {
	state, err := readDirtyTrackerState(dir)
	if err != nil || state == nil || state.PID <= 0 {
		return err
	}
	if !dirtyTrackerProcessAlive(state.PID) {
		return nil
	}
	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func waitForDirtyTracker(dir, generation string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := readDirtyTrackerState(dir)
		if err == nil && state != nil && state.Generation == generation && state.Status == dirtyTrackerStatusAlive && dirtyTrackerProcessAlive(state.PID) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("dirty tracker did not become ready")
}

func dirtyTrackerProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func collectDirtyTrackerCandidates(dir string, index *localCheckoutIndex) ([]string, bool, error) {
	if dirtyTrackerDisabled() || index == nil || index.GitEnabled {
		return nil, false, nil
	}
	state, err := readDirtyTrackerState(dir)
	if err != nil || state == nil {
		return nil, false, err
	}
	if state.Status != dirtyTrackerStatusAlive ||
		strings.TrimSpace(state.SliceID) != strings.TrimSpace(index.SliceID) ||
		strings.TrimSpace(state.CommitHash) != strings.TrimSpace(index.CommitHash) ||
		!dirtyTrackerProcessAlive(state.PID) {
		return nil, false, nil
	}
	paths, err := readDirtyTrackerPaths(dir)
	if err != nil {
		return nil, false, err
	}
	return paths, true, nil
}

func normalizeDirtyTrackerPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		cleaned := filepath.Clean(strings.TrimSpace(path))
		if cleaned == "." || cleaned == "" {
			continue
		}
		if cleaned == ".gs" || cleaned == ".git" || strings.HasPrefix(cleaned, ".gs"+string(os.PathSeparator)) || strings.HasPrefix(cleaned, ".git"+string(os.PathSeparator)) {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	sort.Strings(out)
	return out
}

func collectNoGitWorkingTreeStatusFromCandidates(dir string, lookup *checkoutIndexLookup, candidates []string) ([]workingTreeStatusEntry, []string, error) {
	normalized := normalizeDirtyTrackerPaths(candidates)
	if lookup == nil || len(normalized) == 0 {
		return nil, nil, nil
	}

	statusByPath := make(map[string]string)
	remaining := make(map[string]struct{})
	addEntry := func(path, status string) {
		if path == "" || path == "." {
			return
		}
		statusByPath[path] = status
		remaining[path] = struct{}{}
	}

	validateTrackedFile := func(path string) error {
		original, ok := lookup.files[path]
		if !ok {
			return nil
		}
		fullPath := filepath.Join(dir, path)
		info, err := os.Lstat(fullPath)
		if os.IsNotExist(err) {
			addEntry(path, "D")
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			addEntry(path, "M")
			return nil
		}
		matches, err := checkoutTrackedFileMatches(fullPath, info, original)
		if err != nil {
			return err
		}
		if !matches {
			addEntry(path, "M")
		}
		return nil
	}

	validateTrackedUnderDir := func(relDir string) error {
		prefix := normalizeTrackedDirectoryPath(relDir)
		for path := range lookup.files {
			if prefix == "" || path == prefix || strings.HasPrefix(path, prefix+string(os.PathSeparator)) {
				if err := validateTrackedFile(path); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, candidate := range normalized {
		if _, ok := lookup.files[candidate]; ok {
			if err := validateTrackedFile(candidate); err != nil {
				return nil, nil, err
			}
			continue
		}

		fullPath := filepath.Join(dir, candidate)
		info, err := os.Lstat(fullPath)
		if err == nil && !info.IsDir() {
			addEntry(candidate, "A")
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}

		scanDir := candidate
		if err == nil && info.IsDir() {
			newFiles, scanErr := scanCheckoutForNewFiles(dir, scanDir, lookup)
			if scanErr != nil {
				return nil, nil, scanErr
			}
			for _, path := range newFiles {
				addEntry(path, "A")
			}
			if scanErr := validateTrackedUnderDir(scanDir); scanErr != nil {
				return nil, nil, scanErr
			}
			continue
		}

		if _, ok := lookup.directories[normalizeTrackedDirectoryPath(candidate)]; ok || dirtyTrackerHasTrackedDescendants(lookup, candidate) {
			if err := validateTrackedUnderDir(candidate); err != nil {
				return nil, nil, err
			}
		}
	}

	entries := make([]workingTreeStatusEntry, 0, len(statusByPath))
	for path, status := range statusByPath {
		entries = append(entries, workingTreeStatusEntry{Path: path, Status: status})
	}
	sortWorkingTreeStatus(entries)
	remainingPaths := make([]string, 0, len(remaining))
	for path := range remaining {
		remainingPaths = append(remainingPaths, path)
	}
	sort.Strings(remainingPaths)
	return entries, remainingPaths, nil
}

func dirtyTrackerHasTrackedDescendants(lookup *checkoutIndexLookup, candidate string) bool {
	prefix := normalizeTrackedDirectoryPath(candidate)
	if prefix == "" {
		return len(lookup.files) > 0
	}
	for path := range lookup.files {
		if path == prefix || strings.HasPrefix(path, prefix+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func handleCheckoutWatcher(args []string) {
	fs := flag.NewFlagSet("__watch-checkout", flag.ExitOnError)
	dir := fs.String("dir", "", "Absolute checkout directory")
	generation := fs.String("generation", "", "Watcher generation token")
	sliceID := fs.String("slice", "", "Slice ID")
	commitHash := fs.String("commit", "", "Checkout base commit hash")
	fs.Parse(args)

	root := strings.TrimSpace(*dir)
	if root == "" {
		log.Fatalf("missing --dir")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		log.Fatalf("resolve watcher root: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runCheckoutDirtyTracker(ctx, absRoot, strings.TrimSpace(*generation), strings.TrimSpace(*sliceID), strings.TrimSpace(*commitHash)); err != nil {
		log.Fatalf("dirty tracker failed: %v", err)
	}
}

func runCheckoutDirtyTracker(ctx context.Context, root, generation, sliceID, commitHash string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	state := &dirtyTrackerState{
		Version:    dirtyTrackerVersion,
		SliceID:    sliceID,
		CommitHash: commitHash,
		Generation: generation,
		Checkout:   root,
		PID:        os.Getpid(),
		Status:     dirtyTrackerStatusAlive,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	watchedDirs := make(map[string]struct{})
	addWatchTree := func(startDir string) error {
		return filepath.WalkDir(startDir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !d.IsDir() {
				return nil
			}
			base := d.Name()
			if base == ".gs" || base == ".git" {
				if path == startDir {
					return nil
				}
				return filepath.SkipDir
			}
			if _, ok := watchedDirs[path]; ok {
				return nil
			}
			if err := watcher.Add(path); err != nil {
				return err
			}
			watchedDirs[path] = struct{}{}
			return nil
		})
	}

	if err := addWatchTree(root); err != nil {
		state.Status = dirtyTrackerStatusError
		state.Error = err.Error()
		_ = writeDirtyTrackerState(root, state)
		return err
	}
	if err := writeDirtyTrackerPaths(root, nil); err != nil {
		return err
	}
	if err := writeDirtyTrackerState(root, state); err != nil {
		return err
	}

	dirtySet := make(map[string]struct{})
	flushDirty := func() error {
		paths := make([]string, 0, len(dirtySet))
		for path := range dirtySet {
			paths = append(paths, path)
		}
		return writeDirtyTrackerPaths(root, paths)
	}

	heartbeat := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			if err := flushDirty(); err != nil {
				return err
			}
			state.Status = dirtyTrackerStatusStop
			state.Error = ""
			return writeDirtyTrackerState(root, state)
		case event, ok := <-watcher.Events:
			if !ok {
				if err := flushDirty(); err != nil {
					return err
				}
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) == 0 {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := addWatchTree(event.Name); err != nil {
						state.Status = dirtyTrackerStatusError
						state.Error = err.Error()
						_ = writeDirtyTrackerState(root, state)
						return err
					}
				}
			}
			for _, path := range dirtyTrackerEventPaths(root, event.Name, event.Op) {
				dirtySet[path] = struct{}{}
			}
			if err := flushDirty(); err != nil {
				state.Status = dirtyTrackerStatusError
				state.Error = err.Error()
				_ = writeDirtyTrackerState(root, state)
				return err
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				continue
			}
			state.Status = dirtyTrackerStatusError
			state.Error = err.Error()
			_ = writeDirtyTrackerState(root, state)
			_ = flushDirty()
			return err
		case <-heartbeat.C:
			if _, err := os.Stat(root); err != nil {
				_ = flushDirty()
				state.Status = dirtyTrackerStatusStop
				state.Error = ""
				_ = writeDirtyTrackerState(root, state)
				return nil
			}
			if _, err := os.Stat(checkoutIndexFilePath(root)); err != nil {
				_ = flushDirty()
				state.Status = dirtyTrackerStatusStop
				state.Error = ""
				_ = writeDirtyTrackerState(root, state)
				return nil
			}
		}
	}
}

func dirtyTrackerEventPaths(root, eventPath string, op fsnotify.Op) []string {
	rel, err := filepath.Rel(root, eventPath)
	if err != nil {
		return nil
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return nil
	}
	paths := []string{rel}
	if op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		parent := normalizeTrackedDirectoryPath(filepath.Dir(rel))
		if parent != "" {
			paths = append(paths, parent)
		}
	}
	return normalizeDirtyTrackerPaths(paths)
}
