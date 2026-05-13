package gscli

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

const changesetSwitchStatePath = ".gs/changeset_switch.json"

type changesetSwitchFileState struct {
	Hash          string `json:"hash,omitempty"`
	Executable    bool   `json:"executable,omitempty"`
	SymlinkTarget string `json:"symlink_target,omitempty"`
	Deleted       bool   `json:"deleted,omitempty"`
}

type changesetSwitchState struct {
	ChangesetID     string                              `json:"changeset_id"`
	SnapshotVersion int32                               `json:"snapshot_version"`
	SnapshotHash    string                              `json:"snapshot_hash"`
	BaseCommitHash  string                              `json:"base_commit_hash,omitempty"`
	SliceID         string                              `json:"slice_id,omitempty"`
	Files           map[string]changesetSwitchFileState `json:"files,omitempty"`
	UpdatedAt       string                              `json:"updated_at,omitempty"`
}

type changesetSnapshotSwitchTarget struct {
	ChangesetID     string
	SliceID         string
	SnapshotVersion int32
	SnapshotHash    string
	BaseCommitHash  string
	Files           map[string]*slicev1.FileMetadata
	Deleted         map[string]struct{}
}

type changesetSwitchPlan struct {
	FetchPaths   []string
	DeletePaths  []string
	RestorePaths []string
	UnsafePaths  []string
	TargetPaths  []string
}

type jsonChangesetSwitchOutput struct {
	ChangesetID     string   `json:"changeset_id"`
	SnapshotVersion int32    `json:"snapshot_version"`
	SnapshotHash    string   `json:"snapshot_hash"`
	DryRun          bool     `json:"dry_run,omitempty"`
	FetchPaths      []string `json:"fetch_paths,omitempty"`
	DeletePaths     []string `json:"delete_paths,omitempty"`
	RestorePaths    []string `json:"restore_paths,omitempty"`
	UnsafePaths     []string `json:"unsafe_paths,omitempty"`
	CacheHits       int64    `json:"cache_hits,omitempty"`
}

type jsonChangesetSnapshotsOutput struct {
	ChangesetID string                  `json:"changeset_id"`
	Snapshots   []jsonChangesetSnapshot `json:"snapshots"`
}

func handleChangesetSnapshots(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset snapshots")
	limit := fs.Int("limit", 100, "Maximum number of snapshots to list")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 1 {
		commandUsage("Usage: gs changeset snapshots [<changeset-id>] [--limit <n>] [--json]")
		return
	}
	if *limit < 0 {
		commandFatal("INVALID_ARGUMENT", "--limit must be non-negative", false, "")
	}
	changesetID, err := resolveChangesetIDForRead("")
	if fs.NArg() == 1 {
		changesetID, err = resolveChangesetIDForRead(fs.Arg(0))
	}
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}

	resp, err := cli.sliceClient.ListChangesetSnapshots(ctx, &slicev1.ListChangesetSnapshotsRequest{
		ChangesetId:       changesetID,
		Limit:             int32(*limit),
		OmitModifiedFiles: true,
	})
	if err != nil {
		commandFatalf("CHANGESET_SNAPSHOTS_FAILED", true, "", "Failed to list changeset snapshots: %v", err)
	}
	if jsonEnabled {
		out := jsonChangesetSnapshotsOutput{ChangesetID: changesetID}
		for _, snapshot := range resp.GetSnapshots() {
			out.Snapshots = append(out.Snapshots, jsonChangesetSnapshot{
				Version: snapshot.GetVersion(),
				Hash:    snapshot.GetHash(),
			})
		}
		writeJSONOutput(out)
		return
	}
	fmt.Printf("Snapshots for changeset %s\n", changesetID)
	for _, snapshot := range resp.GetSnapshots() {
		fmt.Printf("- v%d %s files=%d\n", snapshot.GetVersion(), snapshot.GetHash(), snapshot.GetModifiedFileCount())
	}
}

func handleChangesetSwitch(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("changeset switch")
	snapshotVersion := fs.Int("snapshot", 0, "Switch to a specific snapshot version")
	snapshotHash := fs.String("hash", "", "Switch to a specific snapshot hash")
	dryRun := fs.Bool("dry-run", false, "Show the switch plan without changing files")
	force := fs.Bool("force", false, "Overwrite unmanaged local changes")
	allowBaseDrift := fs.Bool("allow-base-drift", false, "Allow switching when the checkout base differs from the snapshot base")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 1 {
		commandUsage("Usage: gs changeset switch [<changeset-id>] [--snapshot <version>|--hash <hash>] [--dry-run] [--force] [--allow-base-drift] [--json]")
		return
	}
	if *snapshotVersion < 0 {
		commandFatal("INVALID_ARGUMENT", "--snapshot must be non-negative", false, "")
	}

	changesetID, err := resolveChangesetIDForRead("")
	if fs.NArg() == 1 {
		changesetID, err = resolveChangesetIDForRead(fs.Arg(0))
	}
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve changeset ID: %v", err)
		return
	}

	checkoutIndex, err := detectCheckoutMode(".")
	if err != nil {
		commandFatalf("CHECKOUT_METADATA_MISSING", false, "gs slice checkout <slice-id>", "Failed to read checkout mode: %v", err)
	}

	target, err := fetchChangesetSnapshotSwitchTarget(ctx, cli, changesetID, int32(*snapshotVersion), strings.TrimSpace(*snapshotHash))
	if err != nil {
		commandFatalf("CHANGESET_SWITCH_FAILED", true, "", "Failed to fetch changeset snapshot metadata: %v", err)
	}
	if target.SliceID != "" && checkoutIndex.SliceID != "" && strings.TrimSpace(target.SliceID) != strings.TrimSpace(checkoutIndex.SliceID) {
		commandFatalf("SLICE_MISMATCH", false, "gs slice checkout "+target.SliceID, "Changeset belongs to slice %s, but checkout is bound to %s", target.SliceID, checkoutIndex.SliceID)
	}
	if target.BaseCommitHash != "" && checkoutIndex.CommitHash != "" && strings.TrimSpace(target.BaseCommitHash) != strings.TrimSpace(checkoutIndex.CommitHash) && !*allowBaseDrift {
		commandFatalf("BASE_COMMIT_MISMATCH", false, "gs changeset switch --allow-base-drift", "Snapshot base %s does not match checkout base %s", target.BaseCommitHash, checkoutIndex.CommitHash)
	}

	currentState, err := readChangesetSwitchState(".")
	if err != nil {
		commandFatalf("CHANGESET_SWITCH_FAILED", false, "", "Failed to read local changeset switch state: %v", err)
	}
	if currentState != nil {
		if strings.TrimSpace(currentState.ChangesetID) != strings.TrimSpace(changesetID) ||
			(target.SliceID != "" && currentState.SliceID != "" && strings.TrimSpace(currentState.SliceID) != strings.TrimSpace(target.SliceID)) {
			currentState = nil
		}
	}
	dirtyEntries, err := collectNoGitWorkingTreeStatus(".", checkoutIndex)
	if err != nil {
		commandFatalf("CHANGESET_SWITCH_FAILED", false, "gs slice status", "Failed to inspect local changes: %v", err)
	}
	dirtyEntries = filterWorkingTreeStatusEntries(dirtyEntries)
	plan := buildChangesetSwitchPlan(currentState, target, dirtyEntries, func(path string, state changesetSwitchFileState) bool {
		ok, matchErr := localPathMatchesChangesetSwitchState(".", path, state)
		if matchErr != nil {
			log.Printf("Warning: failed to inspect %s while planning changeset switch: %v", path, matchErr)
			return false
		}
		return ok
	})
	if len(plan.UnsafePaths) > 0 && !*force {
		if jsonEnabled {
			writeJSONOutput(jsonChangesetSwitchOutput{
				ChangesetID:     changesetID,
				SnapshotVersion: target.SnapshotVersion,
				SnapshotHash:    target.SnapshotHash,
				DryRun:          *dryRun,
				FetchPaths:      plan.FetchPaths,
				DeletePaths:     plan.DeletePaths,
				RestorePaths:    plan.RestorePaths,
				UnsafePaths:     plan.UnsafePaths,
			})
			return
		}
		commandFatalf("LOCAL_CHANGES_CONFLICT", false, "gs changeset switch --force", "Local changes would be overwritten: %s", strings.Join(plan.UnsafePaths, ", "))
	}

	output := jsonChangesetSwitchOutput{
		ChangesetID:     changesetID,
		SnapshotVersion: target.SnapshotVersion,
		SnapshotHash:    target.SnapshotHash,
		DryRun:          *dryRun,
		FetchPaths:      plan.FetchPaths,
		DeletePaths:     plan.DeletePaths,
		RestorePaths:    plan.RestorePaths,
		UnsafePaths:     plan.UnsafePaths,
	}
	if *dryRun {
		if jsonEnabled {
			writeJSONOutput(output)
			return
		}
		printChangesetSwitchPlan(output)
		return
	}

	cache, cacheErr := NewCacheManager()
	if cacheErr != nil {
		log.Printf("Warning: unable to initialize cache: %v", cacheErr)
		cache = nil
	}

	if err := stopDirtyTracker("."); err != nil {
		log.Printf("Warning: failed to stop dirty tracker before changeset switch: %v", err)
	}

	if err := restoreChangesetSwitchPathsToBase(ctx, cli, checkoutIndex, cache, plan.RestorePaths); err != nil {
		commandFatalf("CHANGESET_SWITCH_FAILED", false, "", "Failed to restore paths to base: %v", err)
	}
	for _, path := range plan.DeletePaths {
		if err := os.RemoveAll(filepath.Join(".", filepath.FromSlash(path))); err != nil && !errors.Is(err, os.ErrNotExist) {
			commandFatalf("CHANGESET_SWITCH_FAILED", false, "", "Failed to delete %s: %v", path, err)
		}
	}

	if len(plan.FetchPaths) > 0 {
		materialized, err := materializeChangesetSnapshotPaths(ctx, cli, changesetID, target.SnapshotVersion, target.SnapshotHash, plan.FetchPaths, checkoutIndex, cache)
		if err != nil {
			commandFatalf("CHANGESET_SWITCH_FAILED", true, "", "Failed to materialize changeset snapshot: %v", err)
		}
		if materialized != nil {
			output.CacheHits = materialized.CacheHits
		}
	}
	if err := writeChangesetSwitchState(".", changesetSwitchStateFromTarget(target)); err != nil {
		commandFatalf("CHANGESET_SWITCH_FAILED", false, "", "Failed to write local switch state: %v", err)
	}
	if cache != nil {
		if err := cache.PersistIndex(); err != nil {
			log.Printf("Warning: failed to persist cache index: %v", err)
		}
	}
	_ = writeDirtyTrackerPaths(".", plan.TargetPaths)
	if err := resetDirtyTracker(".", checkoutIndex); err != nil {
		log.Printf("Warning: failed to restart dirty tracker: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(output)
		return
	}
	fmt.Printf("Switched changeset %s to snapshot v%d (%s)\n", changesetID, target.SnapshotVersion, target.SnapshotHash)
	fmt.Printf("Files: fetched=%d deleted=%d restored=%d cache_hits=%d\n", len(plan.FetchPaths), len(plan.DeletePaths), len(plan.RestorePaths), output.CacheHits)
}

func fetchChangesetSnapshotSwitchTarget(ctx context.Context, cli *CLI, changesetID string, version int32, hash string) (*changesetSnapshotSwitchTarget, error) {
	stream, err := cli.sliceClient.StreamChangesetSnapshot(ctx, &slicev1.ChangesetSnapshotRequest{
		ChangesetId:     changesetID,
		SnapshotVersion: version,
		SnapshotHash:    hash,
		MetadataOnly:    true,
	})
	if err != nil {
		return nil, err
	}
	target := &changesetSnapshotSwitchTarget{
		ChangesetID: changesetID,
		Files:       make(map[string]*slicev1.FileMetadata),
		Deleted:     make(map[string]struct{}),
	}
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		manifest := chunk.GetManifest()
		if manifest == nil {
			continue
		}
		if snapshot := manifest.GetSnapshot(); snapshot != nil {
			target.SnapshotVersion = snapshot.GetVersion()
			target.SnapshotHash = strings.TrimSpace(snapshot.GetHash())
			target.BaseCommitHash = strings.TrimSpace(snapshot.GetBaseCommitHash())
		}
		if sliceID := strings.TrimSpace(manifest.GetSliceId()); sliceID != "" {
			target.SliceID = sliceID
		}
		for _, path := range manifest.GetDeletedPaths() {
			cleaned := cleanChangesetSwitchPath(path)
			if cleaned == "" {
				continue
			}
			target.Deleted[cleaned] = struct{}{}
		}
		for _, meta := range manifest.GetFileMetadata() {
			if meta == nil {
				continue
			}
			cleaned := cleanChangesetSwitchPath(meta.GetPath())
			if cleaned == "" {
				continue
			}
			copyMeta := *meta
			copyMeta.Path = cleaned
			copyMeta.FileId = cleaned
			target.Files[cleaned] = &copyMeta
		}
	}
	return target, nil
}

func buildChangesetSwitchPlan(current *changesetSwitchState, target *changesetSnapshotSwitchTarget, dirtyEntries []workingTreeStatusEntry, matches func(string, changesetSwitchFileState) bool) changesetSwitchPlan {
	targetStates := changesetSwitchStatesFromTarget(target)
	currentStates := map[string]changesetSwitchFileState{}
	if current != nil {
		for path, state := range current.Files {
			cleaned := cleanChangesetSwitchPath(path)
			if cleaned == "" {
				continue
			}
			currentStates[cleaned] = state
		}
	}

	dirtyPaths := make([]string, 0, len(dirtyEntries))
	seenDirty := map[string]struct{}{}
	for _, entry := range dirtyEntries {
		path := cleanChangesetSwitchPath(entry.Path)
		if path == "" {
			continue
		}
		if _, ok := seenDirty[path]; ok {
			continue
		}
		seenDirty[path] = struct{}{}
		dirtyPaths = append(dirtyPaths, path)
	}

	unsafe := make([]string, 0)
	for _, path := range dirtyPaths {
		if targetState, ok := targetStates[path]; ok && matches(path, targetState) {
			continue
		}
		if currentState, ok := currentStates[path]; ok && matches(path, currentState) {
			continue
		}
		unsafe = append(unsafe, path)
	}

	restoreSet := map[string]struct{}{}
	for path := range currentStates {
		if _, ok := targetStates[path]; !ok {
			restoreSet[path] = struct{}{}
		}
	}
	for _, path := range dirtyPaths {
		if _, ok := targetStates[path]; ok {
			continue
		}
		if _, ok := currentStates[path]; ok {
			continue
		}
		restoreSet[path] = struct{}{}
	}

	fetchSet := map[string]struct{}{}
	deleteSet := map[string]struct{}{}
	targetPathSet := map[string]struct{}{}
	for path, state := range targetStates {
		targetPathSet[path] = struct{}{}
		if state.Deleted {
			if !matches(path, state) {
				deleteSet[path] = struct{}{}
			}
			continue
		}
		if !matches(path, state) {
			fetchSet[path] = struct{}{}
		}
	}

	return changesetSwitchPlan{
		FetchPaths:   sortedStringSet(fetchSet),
		DeletePaths:  sortedStringSet(deleteSet),
		RestorePaths: sortedStringSet(restoreSet),
		UnsafePaths:  sortedStringSet(sliceToSet(unsafe)),
		TargetPaths:  sortedStringSet(targetPathSet),
	}
}

func materializeChangesetSnapshotPaths(ctx context.Context, cli *CLI, changesetID string, version int32, hash string, paths []string, checkoutIndex *localCheckoutIndex, cache *CacheManager) (*checkoutMaterialization, error) {
	req := &slicev1.ChangesetSnapshotRequest{
		ChangesetId:     changesetID,
		SnapshotVersion: version,
		SnapshotHash:    hash,
		Paths:           append([]string(nil), paths...),
	}
	if cache != nil {
		knownHashes, err := cache.ListObjectHashes()
		if err != nil {
			log.Printf("Warning: unable to list cache objects: %v", err)
		} else {
			req.KnownHashes = knownHashes
		}
	}
	materialized, err := materializeChangesetSnapshotStream(ctx, cli, req, checkoutIndex, cache)
	var staleCacheErr *staleCheckoutCacheError
	if err != nil && cache != nil && errors.As(err, &staleCacheErr) && len(staleCacheErr.Hashes) > 0 {
		if dropErr := cache.DropObjects(staleCacheErr.Hashes); dropErr != nil {
			log.Printf("Warning: unable to drop stale cache hashes: %v", dropErr)
		}
		req.KnownHashes = filterCheckoutKnownHashes(req.KnownHashes, staleCacheErr.Hashes)
		materialized, err = materializeChangesetSnapshotStream(ctx, cli, req, checkoutIndex, cache)
	}
	return materialized, err
}

func materializeChangesetSnapshotStream(ctx context.Context, cli *CLI, req *slicev1.ChangesetSnapshotRequest, checkoutIndex *localCheckoutIndex, cache *CacheManager) (*checkoutMaterialization, error) {
	stream, err := cli.sliceClient.StreamChangesetSnapshot(ctx, req)
	if err != nil {
		return nil, err
	}

	manifest := &slicev1.SliceManifest{}
	knownHashes := make(map[string]struct{}, len(req.GetKnownHashes()))
	for _, hash := range req.GetKnownHashes() {
		hash = strings.TrimSpace(hash)
		if hash != "" {
			knownHashes[hash] = struct{}{}
		}
	}

	var materializer *streamedCheckoutMaterializer
	prepare := func() error {
		if materializer != nil {
			return nil
		}
		var prepErr error
		materializer, prepErr = newStreamedCheckoutMaterializer(".", manifest, cache, false, knownHashes, checkoutIndex)
		return prepErr
	}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch payload := chunk.GetChunk().(type) {
		case *slicev1.ChangesetSnapshotChunk_Manifest:
			if payload.Manifest == nil {
				continue
			}
			manifest.FileMetadata = append(manifest.FileMetadata, payload.Manifest.GetFileMetadata()...)
			if snapshot := payload.Manifest.GetSnapshot(); snapshot != nil && manifest.CommitHash == "" {
				manifest.CommitHash = snapshot.GetHash()
			}
		case *slicev1.ChangesetSnapshotChunk_Block:
			if payload.Block == nil {
				continue
			}
			if err := prepare(); err != nil {
				return nil, err
			}
			if err := materializer.handleBlock(payload.Block); err != nil {
				return nil, err
			}
		case *slicev1.ChangesetSnapshotChunk_File:
			if payload.File == nil {
				continue
			}
			if err := prepare(); err != nil {
				return nil, err
			}
			if err := materializer.handleFile(payload.File); err != nil {
				return nil, err
			}
		}
	}
	if err := prepare(); err != nil {
		return nil, err
	}
	return materializer.finish()
}

func restoreChangesetSwitchPathsToBase(ctx context.Context, cli *CLI, index *localCheckoutIndex, cache *CacheManager, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	lookup := newCheckoutIndexLookup(index)
	for _, path := range paths {
		cleaned := filepath.Clean(filepath.FromSlash(path))
		if tracked, ok := lookup.files[cleaned]; ok {
			if err := restoreTrackedCheckoutFile(ctx, cli, index, cache, tracked); err != nil {
				return err
			}
			if err := refreshRestoredCheckoutFileMetadata(index, tracked.Path); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(filepath.Join(".", cleaned)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func changesetSwitchStateFromTarget(target *changesetSnapshotSwitchTarget) *changesetSwitchState {
	state := &changesetSwitchState{
		Files: map[string]changesetSwitchFileState{},
	}
	if target == nil {
		return state
	}
	state.ChangesetID = target.ChangesetID
	state.SnapshotVersion = target.SnapshotVersion
	state.SnapshotHash = target.SnapshotHash
	state.BaseCommitHash = target.BaseCommitHash
	state.SliceID = target.SliceID
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	for path, file := range target.Files {
		cleaned := cleanChangesetSwitchPath(path)
		if cleaned == "" || file == nil {
			continue
		}
		state.Files[cleaned] = changesetSwitchFileState{
			Hash:          strings.TrimSpace(file.GetHash()),
			Executable:    file.GetExecutable(),
			SymlinkTarget: strings.TrimSpace(file.GetSymlinkTarget()),
		}
	}
	for path := range target.Deleted {
		cleaned := cleanChangesetSwitchPath(path)
		if cleaned == "" {
			continue
		}
		state.Files[cleaned] = changesetSwitchFileState{Deleted: true}
	}
	return state
}

func changesetSwitchStatesFromTarget(target *changesetSnapshotSwitchTarget) map[string]changesetSwitchFileState {
	if target == nil {
		return nil
	}
	return changesetSwitchStateFromTarget(target).Files
}

func readChangesetSwitchState(root string) (*changesetSwitchState, error) {
	raw, err := os.ReadFile(filepath.Join(root, changesetSwitchStatePath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state changesetSwitchState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.Files == nil {
		state.Files = map[string]changesetSwitchFileState{}
	}
	return &state, nil
}

func writeChangesetSwitchState(root string, state *changesetSwitchState) error {
	if state == nil {
		return nil
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, changesetSwitchStatePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func localPathMatchesChangesetSwitchState(root, relPath string, expected changesetSwitchFileState) (bool, error) {
	current, exists, err := readLocalChangesetSwitchFileState(root, relPath)
	if err != nil {
		return false, err
	}
	if expected.Deleted {
		return !exists, nil
	}
	if !exists {
		return false, nil
	}
	return current.Hash == strings.TrimSpace(expected.Hash) &&
		current.Executable == expected.Executable &&
		strings.TrimSpace(current.SymlinkTarget) == strings.TrimSpace(expected.SymlinkTarget), nil
}

func readLocalChangesetSwitchFileState(root, relPath string) (changesetSwitchFileState, bool, error) {
	cleaned := cleanChangesetSwitchPath(relPath)
	if cleaned == "" {
		return changesetSwitchFileState{}, false, fmt.Errorf("path is empty")
	}
	fullPath := filepath.Join(root, filepath.FromSlash(cleaned))
	info, err := os.Lstat(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return changesetSwitchFileState{Deleted: true}, false, nil
	}
	if err != nil {
		return changesetSwitchFileState{}, false, err
	}
	if info.IsDir() {
		return changesetSwitchFileState{}, true, fmt.Errorf("%s is a directory", cleaned)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(fullPath)
		if err != nil {
			return changesetSwitchFileState{}, true, err
		}
		return changesetSwitchFileState{
			Hash:          storage.HashFileManifestContent([]byte(target), false, target),
			SymlinkTarget: target,
		}, true, nil
	}
	executable := info.Mode().Perm()&0o111 != 0
	hash, err := hashLocalRegularFileForManifest(fullPath, executable)
	if err != nil {
		return changesetSwitchFileState{}, true, err
	}
	return changesetSwitchFileState{Hash: hash, Executable: executable}, true, nil
}

func hashLocalRegularFileForManifest(path string, executable bool) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if executable {
		if _, err := hasher.Write([]byte("gitslice-manifest-meta-v1\x00")); err != nil {
			return "", err
		}
		if _, err := hasher.Write([]byte{1}); err != nil {
			return "", err
		}
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], 0)
		if _, err := hasher.Write(length[:]); err != nil {
			return "", err
		}
	}
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func cleanChangesetSwitchPath(raw string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw)))
	if cleaned == "" || cleaned == "." || cleaned == "/" {
		return ""
	}
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}

func sortedStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		if cleaned := cleanChangesetSwitchPath(value); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	sort.Strings(out)
	return uniqueCheckoutPaths(out)
}

func sliceToSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if cleaned := cleanChangesetSwitchPath(value); cleaned != "" {
			out[cleaned] = struct{}{}
		}
	}
	return out
}

func printChangesetSwitchPlan(output jsonChangesetSwitchOutput) {
	fmt.Printf("Changeset: %s\n", output.ChangesetID)
	fmt.Printf("Snapshot: v%d %s\n", output.SnapshotVersion, output.SnapshotHash)
	fmt.Printf("Would fetch: %d\n", len(output.FetchPaths))
	fmt.Printf("Would delete: %d\n", len(output.DeletePaths))
	fmt.Printf("Would restore: %d\n", len(output.RestorePaths))
	for _, path := range output.FetchPaths {
		fmt.Printf("  fetch %s\n", path)
	}
	for _, path := range output.DeletePaths {
		fmt.Printf("  delete %s\n", path)
	}
	for _, path := range output.RestorePaths {
		fmt.Printf("  restore %s\n", path)
	}
}
