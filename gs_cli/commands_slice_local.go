package gscli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	filev1 "github.com/niczy/gitslice/proto/file"
	"github.com/pmezard/go-difflib/difflib"
)

type checkoutFileState struct {
	exists        bool
	path          string
	content       []byte
	executable    bool
	symlinkTarget string
}

type localSliceDiff struct {
	Path          string
	Status        string
	Patch         string
	LinesAdded    int
	LinesDeleted  int
	MetadataNotes []string
	Binary        bool
}

func handleSliceDiff(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("slice diff")
	statOnly := fs.Bool("stat", false, "Show diffstat only")
	nameOnly := fs.Bool("name-only", false, "Show changed file names only")
	summaryOnly := fs.Bool("summary", false, "Show only the change summary and matching paths")
	parseCommandFlags(fs, args)

	checkoutIndex, err := detectCheckoutMode(".")
	if err != nil {
		commandFatalf("CHECKOUT_METADATA_MISSING", false, "gs slice checkout <slice-id>", "Failed to read checkout mode: %v", err)
	}

	entries, err := collectNoGitWorkingTreeStatus(".", checkoutIndex)
	if err != nil {
		commandFatalf("DIFF_FAILED", false, "", "Failed to diff no-git checkout: %v", err)
	}
	entries = filterWorkingTreeStatusEntries(entries)
	entries = filterWorkingTreeStatusByPaths(entries, fs.Args())
	if len(entries) == 0 {
		fmt.Println("(no diff)")
		return
	}
	if *nameOnly {
		for _, entry := range entries {
			fmt.Println(entry.Path)
		}
		return
	}
	if *summaryOnly {
		printWorkingTreeSummary(entries)
		return
	}

	cache, cacheErr := NewCacheManager()
	if cacheErr != nil {
		log.Printf("Warning: unable to initialize cache: %v", cacheErr)
		cache = nil
	}

	diffs, err := buildLocalSliceDiffs(ctx, cli, checkoutIndex, cache, entries)
	if err != nil {
		commandFatalf("DIFF_FAILED", false, "", "Failed to build local diff: %v", err)
	}
	if *statOnly {
		printLocalSliceDiffStat(diffs)
		return
	}
	printLocalSliceDiffs(diffs)
}

func handleSliceRestore(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("slice restore")
	dryRun := fs.Bool("dry-run", false, "Show what would be restored or removed without changing files")
	parseCommandFlags(fs, args)

	checkoutIndex, err := detectCheckoutMode(".")
	if err != nil {
		commandFatalf("CHECKOUT_METADATA_MISSING", false, "gs slice checkout <slice-id>", "Failed to read checkout mode: %v", err)
	}

	entries, err := collectNoGitWorkingTreeStatus(".", checkoutIndex)
	if err != nil {
		commandFatalf("RESTORE_FAILED", false, "", "Failed to inspect no-git checkout: %v", err)
	}
	entries = filterWorkingTreeStatusEntries(entries)
	if len(fs.Args()) > 0 {
		entries = filterWorkingTreeStatusByPaths(entries, fs.Args())
	}
	if len(entries) == 0 {
		fmt.Println("Working tree already matches the recorded checkout state.")
		return
	}
	lookup := newCheckoutIndexLookup(checkoutIndex)
	if *dryRun {
		restoredTracked := 0
		removedNew := 0
		fmt.Println("Planned restore:")
		for _, entry := range entries {
			if _, ok := lookup.files[entry.Path]; ok {
				restoredTracked++
				fmt.Printf("  restore %s\n", entry.Path)
				continue
			}
			removedNew++
			fmt.Printf("  remove %s\n", entry.Path)
		}
		fmt.Printf("Would restore tracked files: %d\n", restoredTracked)
		fmt.Printf("Would remove new paths: %d\n", removedNew)
		return
	}
	if err := stopDirtyTracker("."); err != nil {
		log.Printf("Warning: failed to stop dirty tracker before restore: %v", err)
	}

	cache, cacheErr := NewCacheManager()
	if cacheErr != nil {
		log.Printf("Warning: unable to initialize cache: %v", cacheErr)
		cache = nil
	}
	restoredTracked := 0
	removedNew := 0
	for _, entry := range entries {
		tracked, ok := lookup.files[entry.Path]
		if !ok {
			if err := os.RemoveAll(filepath.Join(".", entry.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
				commandFatalf("RESTORE_FAILED", false, "", "Failed to remove new path %s: %v", entry.Path, err)
			}
			removedNew++
			continue
		}
		if err := restoreTrackedCheckoutFile(ctx, cli, checkoutIndex, cache, tracked); err != nil {
			commandFatalf("RESTORE_FAILED", false, "", "Failed to restore %s: %v", entry.Path, err)
		}
		if err := refreshRestoredCheckoutFileMetadata(checkoutIndex, tracked.Path); err != nil {
			commandFatalf("RESTORE_FAILED", false, "", "Failed to refresh checkout metadata for %s: %v", entry.Path, err)
		}
		restoredTracked++
	}
	if restoredTracked > 0 {
		if err := writeCheckoutIndex(".", checkoutIndex); err != nil {
			commandFatalf("RESTORE_FAILED", false, "", "Failed to update checkout metadata: %v", err)
		}
	}
	if err := removeEmptyCheckoutDirs("."); err != nil {
		commandFatalf("RESTORE_FAILED", false, "", "Failed to clean up empty directories: %v", err)
	}
	if cache != nil {
		if err := cache.PersistIndex(); err != nil {
			log.Printf("Warning: failed to persist cache index: %v", err)
		}
	}
	if err := ensureCleanLocalSearchOverlay("."); err != nil {
		log.Printf("Warning: failed to reset local search overlay after restore: %v", err)
	}
	if err := resetDirtyTracker(".", checkoutIndex); err != nil {
		log.Printf("Warning: failed to restart dirty tracker: %v", err)
	}
	fmt.Printf("Restored tracked files: %d\n", restoredTracked)
	fmt.Printf("Removed new paths: %d\n", removedNew)
}

func buildLocalSliceDiffs(
	ctx context.Context,
	cli *CLI,
	index *localCheckoutIndex,
	cache *CacheManager,
	entries []workingTreeStatusEntry,
) ([]localSliceDiff, error) {
	return buildLocalSliceDiffsAt(ctx, cli, index, cache, entries, ".")
}

func buildLocalSliceDiffsAt(
	ctx context.Context,
	cli *CLI,
	index *localCheckoutIndex,
	cache *CacheManager,
	entries []workingTreeStatusEntry,
	rootDir string,
) ([]localSliceDiff, error) {
	lookup := newCheckoutIndexLookup(index)
	diffs := make([]localSliceDiff, 0, len(entries))
	for _, entry := range entries {
		diff, err := buildLocalSliceDiffAt(ctx, cli, index, cache, lookup, entry, rootDir)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, diff)
	}
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Path == diffs[j].Path {
			return diffs[i].Status < diffs[j].Status
		}
		return diffs[i].Path < diffs[j].Path
	})
	return diffs, nil
}

func buildLocalSliceDiff(
	ctx context.Context,
	cli *CLI,
	index *localCheckoutIndex,
	cache *CacheManager,
	lookup *checkoutIndexLookup,
	entry workingTreeStatusEntry,
) (localSliceDiff, error) {
	return buildLocalSliceDiffAt(ctx, cli, index, cache, lookup, entry, ".")
}

func buildLocalSliceDiffAt(
	ctx context.Context,
	cli *CLI,
	index *localCheckoutIndex,
	cache *CacheManager,
	lookup *checkoutIndexLookup,
	entry workingTreeStatusEntry,
	rootDir string,
) (localSliceDiff, error) {
	diff := localSliceDiff{
		Path:   entry.Path,
		Status: entry.Status,
	}
	var before checkoutFileState
	var after checkoutFileState
	var err error

	if tracked, ok := lookup.files[entry.Path]; ok {
		before, err = loadTrackedCheckoutFileState(ctx, cli, index, cache, tracked)
		if err != nil {
			return diff, err
		}
	}
	after, err = readCurrentCheckoutFileState(rootDir, entry.Path)
	if err != nil {
		return diff, err
	}

	diff.MetadataNotes = describeCheckoutStateMetadataDiff(before, after)
	patch, added, deleted, binary := buildCheckoutStatePatch(entry.Path, before, after)
	diff.Patch = patch
	diff.LinesAdded = added
	diff.LinesDeleted = deleted
	diff.Binary = binary
	return diff, nil
}

func loadTrackedCheckoutFileState(
	ctx context.Context,
	cli *CLI,
	index *localCheckoutIndex,
	cache *CacheManager,
	tracked checkoutTrackedFile,
) (checkoutFileState, error) {
	state := checkoutFileState{
		exists:        true,
		path:          tracked.Path,
		executable:    tracked.Executable,
		symlinkTarget: tracked.SymlinkTarget,
	}
	if strings.TrimSpace(tracked.SymlinkTarget) != "" {
		state.content = []byte(tracked.SymlinkTarget)
		return state, nil
	}
	if cache != nil && strings.TrimSpace(tracked.Hash) != "" {
		content, err := cache.ReadObject(tracked.Hash)
		if err == nil {
			state.content = content
			return state, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("Warning: failed to read cached base object for %s: %v", tracked.Path, err)
		}
	}
	if cli == nil || cli.fileClient == nil {
		return checkoutFileState{}, fmt.Errorf("file client unavailable while loading base content for %s", tracked.Path)
	}

	req := &filev1.GetFileRequest{Path: tracked.Path}
	applyCheckoutBaseGetFileVersion(req, index)
	resp, err := cli.fileClient.GetFile(ctx, req)
	if err != nil {
		return checkoutFileState{}, err
	}
	file := resp.GetFile()
	if file == nil {
		return checkoutFileState{}, fmt.Errorf("empty file response for %s", tracked.Path)
	}
	state.content = append([]byte(nil), file.GetContent()...)
	if cache != nil && strings.TrimSpace(tracked.Hash) != "" {
		if err := cache.StoreObject(tracked.Hash, state.content); err != nil {
			log.Printf("Warning: failed to cache base object for %s: %v", tracked.Path, err)
		}
	}
	return state, nil
}

func readCurrentCheckoutFileState(rootDir, relPath string) (checkoutFileState, error) {
	fullPath := filepath.Join(rootDir, relPath)
	info, err := os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return checkoutFileState{path: relPath}, nil
	}
	if err != nil {
		return checkoutFileState{}, err
	}
	state := checkoutFileState{
		exists:        true,
		path:          relPath,
		executable:    info.Mode().Perm()&0o111 != 0,
		content:       []byte{},
		symlinkTarget: "",
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return checkoutFileState{}, err
		}
		state.symlinkTarget = target
		state.content = []byte(target)
		return state, nil
	}
	if info.IsDir() {
		state.content = nil
		return state, nil
	}
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return checkoutFileState{}, err
	}
	state.content = content
	return state, nil
}

func restoreTrackedCheckoutFile(
	ctx context.Context,
	cli *CLI,
	index *localCheckoutIndex,
	cache *CacheManager,
	tracked checkoutTrackedFile,
) error {
	targetPath := filepath.Join(".", tracked.Path)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if strings.TrimSpace(tracked.SymlinkTarget) != "" {
		return os.Symlink(tracked.SymlinkTarget, targetPath)
	}

	mode := os.FileMode(0o644)
	if tracked.Executable {
		mode = 0o755
	}
	if cache != nil && strings.TrimSpace(tracked.Hash) != "" {
		if err := cache.CopyObjectToFile(tracked.Hash, targetPath, mode); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Printf("Warning: failed to copy cached base object for %s: %v", tracked.Path, err)
		}
	}

	req := &filev1.GetFileRequest{Path: tracked.Path}
	applyCheckoutBaseGetFileVersion(req, index)
	resp, err := cli.fileClient.GetFile(ctx, req)
	if err != nil {
		return err
	}
	file := resp.GetFile()
	if file == nil {
		return fmt.Errorf("empty file response for %s", tracked.Path)
	}
	content := file.GetContent()
	if cache != nil && strings.TrimSpace(tracked.Hash) != "" {
		if err := cache.StoreObject(tracked.Hash, content); err != nil {
			log.Printf("Warning: failed to cache restored base object for %s: %v", tracked.Path, err)
		}
	}
	return os.WriteFile(targetPath, content, mode)
}

func refreshRestoredCheckoutFileMetadata(index *localCheckoutIndex, path string) error {
	if index == nil {
		return nil
	}
	for i := range index.Files {
		if index.Files[i].Path != path {
			continue
		}
		info, err := os.Lstat(filepath.Join(".", path))
		if err != nil {
			return err
		}
		populateTrackedFileLocalMetadata(&index.Files[i], info)
		return nil
	}
	return nil
}

func buildCheckoutStatePatch(path string, before, after checkoutFileState) (string, int, int, bool) {
	switch {
	case !before.exists && !after.exists:
		return "", 0, 0, false
	case before.exists && !after.exists:
		beforeLines, ok := splitLocalDiffLines(before.content)
		if !ok {
			return "", 0, 0, true
		}
		patch := buildUnifiedDiffPatch(path, "/dev/null", beforeLines, nil)
		added, deleted := summarizeLocalPatchLineDelta(patch)
		return patch, added, deleted, false
	case !before.exists && after.exists:
		afterLines, ok := splitLocalDiffLines(after.content)
		if !ok {
			return "", 0, 0, true
		}
		patch := buildUnifiedDiffPatch("/dev/null", path, nil, afterLines)
		added, deleted := summarizeLocalPatchLineDelta(patch)
		return patch, added, deleted, false
	default:
		beforeLines, beforeOK := splitLocalDiffLines(before.content)
		afterLines, afterOK := splitLocalDiffLines(after.content)
		if !beforeOK || !afterOK {
			return "", 0, 0, true
		}
		patch := buildUnifiedDiffPatch(path, path, beforeLines, afterLines)
		added, deleted := summarizeLocalPatchLineDelta(patch)
		return patch, added, deleted, false
	}
}

func buildUnifiedDiffPatch(oldPath, newPath string, beforeLines, afterLines []string) string {
	oldLabel := "a/" + cleanLocalDiffPath(oldPath)
	newLabel := "b/" + cleanLocalDiffPath(newPath)
	if cleanLocalDiffPath(oldPath) == "/dev/null" || oldPath == "/dev/null" {
		oldLabel = "/dev/null"
	}
	if cleanLocalDiffPath(newPath) == "/dev/null" || newPath == "/dev/null" {
		newLabel = "/dev/null"
	}
	patch, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        beforeLines,
		B:        afterLines,
		FromFile: oldLabel,
		ToFile:   newLabel,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return strings.TrimRight(patch, "\n")
}

func splitLocalDiffLines(content []byte) ([]string, bool) {
	if len(content) == 0 {
		return nil, true
	}
	if !utf8.Valid(content) || bytesLookBinary(content) {
		return nil, false
	}
	return difflib.SplitLines(string(content)), true
}

func bytesLookBinary(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

func summarizeLocalPatchLineDelta(patch string) (added int, deleted int) {
	for _, line := range strings.Split(patch, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "-"):
			deleted++
		}
	}
	return added, deleted
}

func describeCheckoutStateMetadataDiff(before, after checkoutFileState) []string {
	notes := make([]string, 0, 2)
	if before.symlinkTarget != after.symlinkTarget {
		switch {
		case before.symlinkTarget == "" && after.symlinkTarget != "":
			notes = append(notes, "type: file -> symlink")
		case before.symlinkTarget != "" && after.symlinkTarget == "":
			notes = append(notes, "type: symlink -> file")
		case before.symlinkTarget != after.symlinkTarget:
			notes = append(notes, fmt.Sprintf("symlink: %s -> %s", before.symlinkTarget, after.symlinkTarget))
		}
	}
	if before.executable != after.executable {
		if after.executable {
			notes = append(notes, "mode: executable")
		} else {
			notes = append(notes, "mode: non-executable")
		}
	}
	return notes
}

func printLocalSliceDiffStat(diffs []localSliceDiff) {
	for _, diff := range diffs {
		line := fmt.Sprintf("%s %s (+%d -%d)", diff.Status, diff.Path, diff.LinesAdded, diff.LinesDeleted)
		if diff.Binary {
			line = fmt.Sprintf("%s (binary/non-text)", line)
		}
		if len(diff.MetadataNotes) > 0 {
			line = fmt.Sprintf("%s [%s]", line, strings.Join(diff.MetadataNotes, ", "))
		}
		fmt.Println(line)
	}
}

func printWorkingTreeSummary(entries []workingTreeStatusEntry) {
	added, modified, deleted := summarizeWorkingTreeStatus(entries)
	fmt.Printf("Changes: +%d ~%d -%d\n", added, modified, deleted)
	fmt.Println("Paths:")
	for _, entry := range entries {
		fmt.Printf("  %s %s\n", entry.Status, entry.Path)
	}
}

func printLocalSliceDiffs(diffs []localSliceDiff) {
	for i, diff := range diffs {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("%s %s (+%d -%d)\n", diff.Status, diff.Path, diff.LinesAdded, diff.LinesDeleted)
		for _, note := range diff.MetadataNotes {
			fmt.Printf("metadata: %s\n", note)
		}
		switch {
		case strings.TrimSpace(diff.Patch) != "":
			fmt.Println(diff.Patch)
		case diff.Binary:
			fmt.Println("(binary or non-text change)")
		default:
			fmt.Println("(no textual diff)")
		}
	}
}

func filterWorkingTreeStatusByPaths(entries []workingTreeStatusEntry, rawSpecs []string) []workingTreeStatusEntry {
	specs := normalizeLocalModifiedFiles(rawSpecs)
	if len(specs) == 0 {
		return entries
	}
	filtered := make([]workingTreeStatusEntry, 0, len(entries))
	for _, entry := range entries {
		for _, spec := range specs {
			if workingTreePathMatchesSpec(entry.Path, spec) {
				filtered = append(filtered, entry)
				break
			}
		}
	}
	return filtered
}

func workingTreePathMatchesSpec(path, spec string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	spec = filepath.Clean(strings.TrimSpace(spec))
	if path == spec {
		return true
	}
	if spec == "." {
		return true
	}
	return strings.HasPrefix(path, spec+string(os.PathSeparator))
}

func cleanLocalDiffPath(raw string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return strings.TrimPrefix(cleaned, "/")
}

func applyCheckoutBaseGetFileVersion(req *filev1.GetFileRequest, index *localCheckoutIndex) {
	if req == nil || index == nil {
		return
	}
	if strings.TrimSpace(index.CommitHash) != "" {
		applyGetFileVersion(req, "", index.CommitHash)
		return
	}
	applyGetFileVersion(req, index.SliceID, "")
}
