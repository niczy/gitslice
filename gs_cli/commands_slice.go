package gscli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newSliceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "slice <command> [options]",
		Short:              "Manage slices",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			if isHelpRequest(args) {
				_ = cmd.Help()
				return
			}
			args = configureCLIBehavior(args)
			configureCLIOutputMode(args)
			if len(args) > 0 && args[0] == "checkouts" {
				handleSliceCheckouts(args[1:])
				return
			}
			runAuthenticatedCLICommand(args, 24*time.Hour, func(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
				handleSliceCommand(ctx, cli, args)
			})
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printSliceHelp()
	})
	return cmd
}

func handleSliceCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printSliceHelp()
		return
	}

	switch args[0] {
	case "bind":
		handleInit(ctx, cli, args[1:])
	case "list":
		handleSliceList(ctx, cli, args[1:])
	case "ensure":
		handleSliceEnsure(ctx, cli, args[1:])
	case "create":
		handleSliceCreate(ctx, cli, args[1:])
	case "checkout", "clone":
		handleSliceCheckout(ctx, cli, args[1:])
	case "sync", "pull":
		handleSliceSync(ctx, cli, args[1:])
	case "publish", "export":
		handleSlicePublish(ctx, cli, args[0], args[1:])
	case "status":
		handleSliceStatus(ctx, cli, args[1:])
	case "visibility":
		handleSliceVisibilityCommand(ctx, cli, args[1:])
	case "tree", "list-files":
		handleSliceTree(ctx, cli, args[1:])
	case "history":
		handleLog(ctx, cli, args[1:])
	case "root":
		handleRootSlice(ctx, cli)
	case "search":
		handleSliceSearch(ctx, cli, args[1:])
	case "diff":
		handleSliceDiff(ctx, cli, args[1:])
	case "restore":
		handleSliceRestore(ctx, cli, args[1:])
	case "checkouts":
		handleSliceCheckouts(args[1:])
	case "delete":
		handleSliceDelete(ctx, cli, args[1:])
	case "rename":
		handleRenameSlice(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown slice command: %s", args[0]), false, "gs slice --help")
	}
}

func handleSliceCreate(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	if len(args) < 2 {
		commandUsage("Usage: gs slice create <name> <folder-path[,folder-path...]> [--folders <folder-path[,folder-path...]>]")
		return
	}

	sliceName := strings.TrimSpace(args[0])
	if sliceName == "" {
		commandUsage("Slice name cannot be empty")
		return
	}

	folderPaths := parseSliceFolderPaths(args[1])

	fs := newCommandFlagSet("slice create")
	description := fs.String("description", "Focused slice", "Description of the new slice")
	moreFolders := fs.String("folders", "", "Additional comma-separated folder paths to include in this slice")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args[2:])
	jsonEnabled := jsonRequested || *jsonOutput

	folderPaths = append(folderPaths, parseSliceFolderPaths(*moreFolders)...)
	if len(folderPaths) == 0 {
		commandUsage("At least one folder path is required")
		return
	}

	rootResp, err := cli.sliceClient.GetRootSlice(ctx, &slicev1.GetRootSliceRequest{})
	if err != nil {
		commandFatalf("SLICE_CREATE_FAILED", true, "", "Failed to resolve published root slice: %v", err)
	}

	req := &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: rootResp.GetSliceId(),
		FolderPaths:   folderPaths,
		Name:          sliceName,
		Description:   *description,
	}

	resp, err := cli.sliceClient.CreateSliceFromFolder(ctx, req)
	if err != nil {
		commandFatalf("SLICE_CREATE_FAILED", true, "", "Failed to create slice: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(jsonSliceCreateOutput{
			Name:        resp.Name,
			SliceID:     resp.SliceId,
			Slug:        resp.GetSlug(),
			Description: *description,
			Status:      resp.Status,
		})
		return
	}

	fmt.Printf("Created slice: %s (id: %s)\n", resp.Name, resp.SliceId)
	if resp.GetSlug() != "" {
		fmt.Printf("Slug: %s\n", resp.GetSlug())
	}
	fmt.Printf("Status: %s\n", resp.Status)
}

func handleSliceCheckout(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	if len(args) < 1 {
		commandUsage("Usage: gs slice checkout|clone <slice-id-or-ref> [--commit <commit-hash>] [--files] [--json]")
		return
	}

	sliceID, err := resolveSliceRef(ctx, cli, args[0])
	if err != nil {
		commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Invalid slice reference: %v", err)
	}

	// Parse flags
	fs := newCommandFlagSet("slice checkout")
	commitHash := fs.String("commit", "HEAD", "Commit hash to checkout")
	showFiles := fs.Bool("files", false, "Print each file in the slice after checkout")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args[1:])
	jsonEnabled := jsonRequested || *jsonOutput

	entries, err := os.ReadDir(".")
	if err != nil {
		commandFatalf("SLICE_CHECKOUT_FAILED", false, "", "Failed to read directory: %v", err)
	}
	if len(entries) > 0 {
		commandFatal("DIRECTORY_NOT_EMPTY", "Directory is not empty. Please checkout into an empty directory.", false, "")
	}

	checkoutResult, err := fetchAndMaterializeSliceCheckout(ctx, cli, sliceID, *commitHash, ".", false, nil)
	if err != nil {
		commandFatalf("SLICE_CHECKOUT_FAILED", true, "", "Failed to checkout slice: %v", err)
	}

	if err := os.MkdirAll(".gs", 0o755); err != nil {
		commandFatalf("SLICE_CHECKOUT_FAILED", false, "", "Failed to create .gs directory: %v", err)
	}
	if err := writeSliceIDConfig(sliceID); err != nil {
		commandFatalf("SLICE_CHECKOUT_FAILED", false, "", "Failed to write config file: %v", err)
	}

	nextCheckoutIndex, err := buildCheckoutIndex(".", sliceID, checkoutResult.Manifest)
	if err != nil {
		commandFatalf("SLICE_CHECKOUT_FAILED", false, "", "Failed to build checkout index: %v", err)
	}
	if err := writeCheckoutIndex(".", nextCheckoutIndex); err != nil {
		commandFatalf("SLICE_CHECKOUT_FAILED", false, "", "Failed to write checkout index: %v", err)
	}
	if err := ensureLocalSliceSearchArtifact(ctx, cli, ".", sliceID, checkoutResult.Manifest); err != nil {
		log.Printf("Warning: failed to prepare local slice search artifact: %v", err)
	}
	if err := resetDirtyTracker(".", nextCheckoutIndex); err != nil {
		log.Printf("Warning: failed to start dirty tracker: %v", err)
	}
	if err := registerCheckout(".", sliceID, checkoutResult.Manifest.CommitHash); err != nil {
		log.Printf("Warning: failed to register checkout path: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(buildSliceCheckoutOutput(sliceID, checkoutResult, *showFiles))
		return
	}

	// Display checkout results
	fmt.Printf("Checked out slice: %s\n", sliceID)
	fmt.Printf("Commit: %s\n", checkoutResult.Manifest.CommitHash)
	fmt.Printf("Files: %d\n", len(checkoutResult.Manifest.FileMetadata))

	if *showFiles && len(checkoutResult.Manifest.FileMetadata) > 0 {
		fmt.Println("\nFiles in slice:")
		for _, fm := range checkoutResult.Manifest.FileMetadata {
			fmt.Printf("  - %s (%d bytes)\n", fm.Path, fm.Size)
		}
	}

	if checkoutResult.Cache != nil {
		fmt.Printf("Cache hits: %d\n", checkoutResult.Materialized.CacheHits)
	}
}

func handleSliceSync(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice sync")
	commitHash := fs.String("commit", "HEAD", "Commit hash to sync to")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs slice sync [--commit <commit-hash>] [--json]")
		return
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read current slice binding: %v", err)
	}

	checkoutIndex, err := readCheckoutIndex(".")
	if err != nil {
		commandFatalf("CHECKOUT_METADATA_MISSING", false, "gs slice checkout <slice-id>", "Failed to read checkout index: %v", err)
	}
	if checkoutIndex == nil {
		commandFatal("CHECKOUT_METADATA_MISSING", "Cannot sync slice: checkout metadata missing. Run gs slice checkout again.", false, "gs slice checkout <slice-id>")
	}
	if err := verifyCheckoutIndexClean(".", checkoutIndex); err != nil {
		commandFatalf("WORKING_TREE_DIRTY", false, "gs slice diff", "Cannot sync slice: %v", err)
	}
	if err := stopDirtyTracker("."); err != nil {
		log.Printf("Warning: failed to stop dirty tracker before sync: %v", err)
	}

	checkoutResult, err := fetchAndMaterializeSliceCheckout(ctx, cli, sliceID, *commitHash, ".", true, checkoutIndex)
	if err != nil {
		commandFatalf("SLICE_SYNC_FAILED", true, "", "Failed to sync slice: %v", err)
	}
	nextCheckoutIndex, err := buildCheckoutIndex(".", sliceID, checkoutResult.Manifest)
	if err != nil {
		commandFatalf("SLICE_SYNC_FAILED", false, "", "Failed to build checkout index: %v", err)
	}

	status := "up to date"
	if !checkoutIndicesEqualContent(checkoutIndex, nextCheckoutIndex) {
		status = "updated"
	}

	if err := writeCheckoutIndex(".", nextCheckoutIndex); err != nil {
		commandFatalf("SLICE_SYNC_FAILED", false, "", "Failed to write checkout index: %v", err)
	}
	if err := ensureLocalSliceSearchArtifact(ctx, cli, ".", sliceID, checkoutResult.Manifest); err != nil {
		log.Printf("Warning: failed to refresh local slice search artifact: %v", err)
	}
	if err := resetDirtyTracker(".", nextCheckoutIndex); err != nil {
		log.Printf("Warning: failed to restart dirty tracker: %v", err)
	}
	if err := registerCheckout(".", sliceID, checkoutResult.Manifest.CommitHash); err != nil {
		log.Printf("Warning: failed to update checkout registry: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(buildSliceSyncOutput(sliceID, status, checkoutResult))
		return
	}

	fmt.Printf("Synced slice: %s\n", sliceID)
	fmt.Printf("Commit: %s\n", checkoutResult.Manifest.CommitHash)
	fmt.Printf("Files: %d\n", len(checkoutResult.Manifest.FileMetadata))
	fmt.Printf("Status: %s\n", status)
	if checkoutResult.Cache != nil {
		fmt.Printf("Cache hits: %d\n", checkoutResult.Materialized.CacheHits)
	}
}

func handleSliceCheckouts(args []string) {
	fs := newCommandFlagSet("slice checkouts")
	filterSliceID := fs.String("slice", "", "Filter to one slice ID")
	parseCommandFlags(fs, args)

	normalizedSliceID := ""
	if strings.TrimSpace(*filterSliceID) != "" {
		var err error
		normalizedSliceID, err = normalizeSliceID(*filterSliceID)
		if err != nil {
			commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Invalid slice ID: %v", err)
		}
	}

	records, err := listCheckoutRecords()
	if err != nil {
		commandFatalf("CHECKOUT_REGISTRY_FAILED", false, "", "Failed to read checkout registry: %v", err)
	}

	filtered := make([]CheckoutRecord, 0, len(records))
	for _, record := range records {
		if normalizedSliceID != "" && record.SliceID != normalizedSliceID {
			continue
		}
		filtered = append(filtered, record)
	}

	fmt.Printf("Tracked checkouts: %d\n", len(filtered))
	fmt.Printf("Unique slices: %d\n", countUniqueCheckoutSlices(filtered))
	fmt.Printf("Stale records: %d\n", countStaleCheckoutRecords(filtered))
	if len(filtered) == 0 {
		return
	}

	fmt.Println()
	for _, record := range filtered {
		status := "active"
		if checkoutRecordIsStale(record) {
			status = "stale"
		}
		fmt.Printf("- %s\n", record.SliceID)
		fmt.Printf("  Path: %s\n", record.Path)
		if strings.TrimSpace(record.CommitHash) != "" {
			fmt.Printf("  Commit: %s\n", record.CommitHash)
		}
		if strings.TrimSpace(record.UpdatedAt) != "" {
			fmt.Printf("  Updated: %s\n", record.UpdatedAt)
		}
		fmt.Printf("  Status: %s\n", status)
	}
}

func parseSliceFolderPaths(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		cleaned := strings.TrimSpace(part)
		if cleaned == "" {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func fetchAndMaterializeSliceCheckout(
	ctx context.Context,
	cli *CLI,
	sliceID, commitHash, dir string,
	pruneTracked bool,
	previousIndex *localCheckoutIndex,
) (*checkoutFetchResult, error) {
	req := &slicev1.CheckoutRequest{
		SliceId:    sliceID,
		CommitHash: commitHash,
	}

	cache, err := NewCacheManager()
	if err != nil {
		log.Printf("Warning: unable to initialize cache: %v", err)
	}
	if cache != nil {
		knownHashes, err := cache.ListObjectHashes()
		if err != nil {
			log.Printf("Warning: unable to list cache objects: %v", err)
		} else {
			req.KnownHashes = knownHashes
		}
	}

	manifest, materialized, err := materializeSliceCheckoutStream(ctx, cli, req, cache, dir, pruneTracked, previousIndex)
	var staleCacheErr *staleCheckoutCacheError
	if err != nil && cache != nil && errors.As(err, &staleCacheErr) && len(staleCacheErr.Hashes) > 0 {
		if dropErr := cache.DropObjects(staleCacheErr.Hashes); dropErr != nil {
			log.Printf("Warning: unable to drop stale cache hashes: %v", dropErr)
		}
		if persistErr := cache.PersistIndex(); persistErr != nil {
			log.Printf("Warning: unable to persist cache index: %v", persistErr)
		}
		req.KnownHashes = filterCheckoutKnownHashes(req.KnownHashes, staleCacheErr.Hashes)
		manifest, materialized, err = materializeSliceCheckoutStream(ctx, cli, req, cache, dir, pruneTracked, previousIndex)
	}
	if err == nil {
		if cache != nil {
			if persistErr := cache.PersistIndex(); persistErr != nil {
				log.Printf("Warning: unable to persist cache index: %v", persistErr)
			}
		}
		return &checkoutFetchResult{
			Manifest:     manifest,
			Materialized: materialized,
			Cache:        cache,
		}, nil
	}
	if status.Code(err) != codes.Unimplemented {
		return nil, err
	}

	resp, err := cli.sliceClient.CheckoutSlice(ctx, req)
	if err != nil {
		return nil, err
	}
	materialized, err = materializeSliceCheckout(dir, resp, cache, pruneTracked, previousIndex)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		if persistErr := cache.PersistIndex(); persistErr != nil {
			log.Printf("Warning: unable to persist cache index: %v", persistErr)
		}
	}
	return &checkoutFetchResult{
		Manifest:     resp.GetManifest(),
		Materialized: materialized,
		Cache:        cache,
	}, nil
}

func filterCheckoutKnownHashes(knownHashes, dropHashes []string) []string {
	if len(knownHashes) == 0 || len(dropHashes) == 0 {
		return knownHashes
	}
	drop := make(map[string]struct{}, len(dropHashes))
	for _, hash := range dropHashes {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		drop[hash] = struct{}{}
	}
	filtered := make([]string, 0, len(knownHashes))
	for _, hash := range knownHashes {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		if _, ok := drop[hash]; ok {
			continue
		}
		filtered = append(filtered, hash)
	}
	return filtered
}

func materializeSliceCheckoutStream(
	ctx context.Context,
	cli *CLI,
	req *slicev1.CheckoutRequest,
	cache *CacheManager,
	dir string,
	pruneTracked bool,
	previousIndex *localCheckoutIndex,
) (*slicev1.SliceManifest, *checkoutMaterialization, error) {
	stream, err := cli.sliceClient.StreamCheckoutSlice(ctx, req)
	if err != nil {
		return nil, nil, err
	}

	manifest := &slicev1.SliceManifest{}
	knownHashes := make(map[string]struct{}, len(req.GetKnownHashes()))
	for _, hash := range req.GetKnownHashes() {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		knownHashes[hash] = struct{}{}
	}

	var materializer *streamedCheckoutMaterializer
	prepareMaterializer := func() error {
		if materializer != nil {
			return nil
		}
		var prepErr error
		materializer, prepErr = newStreamedCheckoutMaterializer(dir, manifest, cache, pruneTracked, knownHashes, previousIndex)
		return prepErr
	}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		switch payload := chunk.GetChunk().(type) {
		case *slicev1.CheckoutChunk_Manifest:
			if payload.Manifest == nil {
				continue
			}
			if manifest.GetCommitHash() == "" {
				manifest.CommitHash = payload.Manifest.GetCommitHash()
			}
			manifest.FileMetadata = append(manifest.FileMetadata, payload.Manifest.GetFileMetadata()...)
		case *slicev1.CheckoutChunk_Block:
			if payload.Block == nil {
				continue
			}
			if err := prepareMaterializer(); err != nil {
				return nil, nil, err
			}
			if err := materializer.handleBlock(payload.Block); err != nil {
				return nil, nil, err
			}
		case *slicev1.CheckoutChunk_File:
			if payload.File == nil {
				continue
			}
			if err := prepareMaterializer(); err != nil {
				return nil, nil, err
			}
			if err := materializer.handleFile(payload.File); err != nil {
				return nil, nil, err
			}
		}
	}

	if err := prepareMaterializer(); err != nil {
		return nil, nil, err
	}
	materialized, err := materializer.finish()
	if err != nil {
		return nil, nil, err
	}
	return manifest, materialized, nil
}

type checkoutMaterialization struct {
	CacheHits    int64
	ChangedPaths []string
}

type checkoutFetchResult struct {
	Manifest     *slicev1.SliceManifest
	Materialized *checkoutMaterialization
	Cache        *CacheManager
}

type staleCheckoutCacheError struct {
	Hashes []string
}

func (e *staleCheckoutCacheError) Error() string {
	return fmt.Sprintf("stale checkout cache index: %d missing objects", len(e.Hashes))
}

func materializeSliceCheckout(dir string, resp *slicev1.CheckoutResponse, cache *CacheManager, pruneTracked bool, previousIndex *localCheckoutIndex) (*checkoutMaterialization, error) {
	if resp == nil || resp.GetManifest() == nil {
		return nil, fmt.Errorf("missing checkout manifest")
	}

	fileContents := make(map[string][]byte)
	explicitFiles := make(map[string]struct{}, len(resp.Files))
	for _, file := range resp.Files {
		if file == nil {
			continue
		}
		fileContents[file.FileId] = file.Content
		explicitFiles[file.FileId] = struct{}{}
	}
	blockContents := make(map[string][]byte)
	for _, block := range resp.Blocks {
		if block == nil {
			continue
		}
		blockContents[block.Hash] = block.Content
		if cache != nil && block.Hash != "" {
			if err := cache.StoreObject(block.Hash, block.Content); err != nil {
				log.Printf("Failed to update cache block %s: %v", block.Hash, err)
			}
		}
	}

	var changedPaths []string
	if pruneTracked {
		trackedPaths, err := trackedCheckoutPathsForPrune(dir)
		if err != nil {
			return nil, err
		}
		removedPaths, err := removeStaleTrackedCheckoutFiles(dir, trackedPaths, resp.Manifest.FileMetadata)
		if err != nil {
			return nil, err
		}
		changedPaths = append(changedPaths, removedPaths...)
	}
	removedConflicts, err := prepareCheckoutDirectories(dir, resp.Manifest.FileMetadata)
	if err != nil {
		return nil, err
	}
	changedPaths = append(changedPaths, removedConflicts...)
	directoryMarkers := checkoutDirectoryMarkers(resp.Manifest.FileMetadata)
	previousLookup := newCheckoutMaterializationLookup(previousIndex)

	var cachedHits int64
	var changedPathsMu sync.Mutex
	workerCount := checkoutWorkerCount(len(resp.Manifest.FileMetadata))
	if workerCount > 0 {
		runCheckoutJobs(workerCount, resp.Manifest.FileMetadata, func(fm *slicev1.FileMetadata) {
			if fm == nil {
				return
			}
			if _, ok := directoryMarkers[filepath.Clean(fm.GetPath())]; ok {
				return
			}
			if reuse, err := canReuseExistingCheckoutFile(dir, previousLookup, fm); err != nil {
				log.Printf("Failed to inspect existing checkout file %s: %v", fm.GetPath(), err)
				return
			} else if reuse {
				return
			}
			writtenPath, err := writeSliceCheckoutFile(dir, cache, fm, explicitFiles, fileContents, blockContents, &cachedHits)
			if err != nil {
				log.Printf("Failed to write %s: %v", fm.GetPath(), err)
				return
			}
			if writtenPath != "" {
				changedPathsMu.Lock()
				changedPaths = append(changedPaths, writtenPath)
				changedPathsMu.Unlock()
			}
		})
	}

	if pruneTracked {
		if err := removeEmptyCheckoutDirs(dir); err != nil {
			return nil, err
		}
	}
	sort.Strings(changedPaths)
	return &checkoutMaterialization{
		CacheHits:    atomic.LoadInt64(&cachedHits),
		ChangedPaths: uniqueCheckoutPaths(changedPaths),
	}, nil
}

type streamedPendingFile struct {
	meta               *slicev1.FileMetadata
	missingBlockHashes []string
	remainingBlocks    int
}

type streamedCheckoutMaterializer struct {
	dir            string
	cache          *CacheManager
	manifest       *slicev1.SliceManifest
	previousLookup *checkoutIndexLookup
	blockContents  map[string][]byte
	blockWaiters   map[string][]*streamedPendingFile
	blockRefCounts map[string]int
	pendingFiles   map[string]*streamedPendingFile
	directFiles    map[string]*slicev1.FileMetadata
	changedPaths   []string
	cachedHits     int64
	missingKnown   map[string]struct{}
}

func newStreamedCheckoutMaterializer(
	dir string,
	manifest *slicev1.SliceManifest,
	cache *CacheManager,
	pruneTracked bool,
	knownHashes map[string]struct{},
	previousIndex *localCheckoutIndex,
) (*streamedCheckoutMaterializer, error) {
	if manifest == nil {
		return nil, fmt.Errorf("missing checkout manifest")
	}

	materializer := &streamedCheckoutMaterializer{
		dir:            dir,
		cache:          cache,
		manifest:       manifest,
		previousLookup: newCheckoutMaterializationLookup(previousIndex),
		blockContents:  make(map[string][]byte),
		blockWaiters:   make(map[string][]*streamedPendingFile),
		blockRefCounts: make(map[string]int),
		pendingFiles:   make(map[string]*streamedPendingFile),
		directFiles:    make(map[string]*slicev1.FileMetadata),
		missingKnown:   make(map[string]struct{}),
	}

	if pruneTracked {
		trackedPaths, err := trackedCheckoutPathsForPrune(dir)
		if err != nil {
			return nil, err
		}
		removedPaths, err := removeStaleTrackedCheckoutFiles(dir, trackedPaths, manifest.FileMetadata)
		if err != nil {
			return nil, err
		}
		materializer.changedPaths = append(materializer.changedPaths, removedPaths...)
	}
	removedConflicts, err := prepareCheckoutDirectories(dir, manifest.FileMetadata)
	if err != nil {
		return nil, err
	}
	materializer.changedPaths = append(materializer.changedPaths, removedConflicts...)
	directoryMarkers := checkoutDirectoryMarkers(manifest.FileMetadata)

	workItems := make([]*slicev1.FileMetadata, 0, len(manifest.FileMetadata))
	for _, fm := range manifest.FileMetadata {
		if fm == nil {
			continue
		}
		if _, ok := directoryMarkers[filepath.Clean(fm.GetPath())]; ok {
			continue
		}
		workItems = append(workItems, fm)
	}

	var initMu sync.Mutex
	var initErr error
	var initErrOnce sync.Once
	var initFailed int32
	setInitErr := func(err error) {
		if err == nil {
			return
		}
		initErrOnce.Do(func() {
			initErr = err
			atomic.StoreInt32(&initFailed, 1)
		})
	}

	runCheckoutJobs(checkoutWorkerCount(len(workItems)), workItems, func(fm *slicev1.FileMetadata) {
		if atomic.LoadInt32(&initFailed) != 0 {
			return
		}

		if reuse, err := canReuseExistingCheckoutFile(dir, materializer.previousLookup, fm); err != nil {
			setInitErr(err)
			return
		} else if reuse {
			return
		}

		if writtenPath, wrote, err := tryWriteSliceCheckoutFileFromCache(dir, cache, fm, nil, &materializer.cachedHits); err != nil {
			setInitErr(err)
			return
		} else if wrote {
			initMu.Lock()
			materializer.changedPaths = append(materializer.changedPaths, writtenPath)
			initMu.Unlock()
			return
		}

		if len(fm.GetBlocks()) == 0 {
			initMu.Lock()
			if hash := strings.TrimSpace(fm.GetHash()); hash != "" {
				if _, ok := knownHashes[hash]; ok {
					materializer.missingKnown[hash] = struct{}{}
				}
			}
			materializer.directFiles[fm.GetFileId()] = fm
			initMu.Unlock()
			return
		}

		pending := &streamedPendingFile{meta: fm}
		for _, block := range fm.GetBlocks() {
			if block == nil {
				continue
			}
			hash := strings.TrimSpace(block.GetHash())
			if hash == "" {
				continue
			}
			if _, ok := knownHashes[hash]; ok {
				continue
			}
			pending.missingBlockHashes = append(pending.missingBlockHashes, hash)
			pending.remainingBlocks++
		}
		if pending.remainingBlocks == 0 {
			if writtenPath, err := writeSliceCheckoutFile(dir, cache, fm, nil, nil, materializer.blockContents, &materializer.cachedHits); err != nil {
				setInitErr(err)
				return
			} else if writtenPath != "" {
				initMu.Lock()
				materializer.changedPaths = append(materializer.changedPaths, writtenPath)
				initMu.Unlock()
				return
			}
			initMu.Lock()
			if hash := strings.TrimSpace(fm.GetHash()); hash != "" {
				if _, ok := knownHashes[hash]; ok {
					materializer.missingKnown[hash] = struct{}{}
				}
			}
			for _, block := range fm.GetBlocks() {
				if block == nil {
					continue
				}
				hash := strings.TrimSpace(block.GetHash())
				if hash == "" {
					continue
				}
				if _, ok := knownHashes[hash]; ok {
					materializer.missingKnown[hash] = struct{}{}
				}
			}
			initMu.Unlock()
			setInitErr(fmt.Errorf("failed to materialize %s from local cache", fm.GetPath()))
			return
		}

		initMu.Lock()
		materializer.pendingFiles[fm.GetFileId()] = pending
		for _, blockHash := range pending.missingBlockHashes {
			materializer.blockWaiters[blockHash] = append(materializer.blockWaiters[blockHash], pending)
			materializer.blockRefCounts[blockHash]++
		}
		initMu.Unlock()
	})
	if initErr != nil {
		return nil, initErr
	}

	return materializer, nil
}

func (m *streamedCheckoutMaterializer) handleBlock(block *slicev1.BlockContent) error {
	if block == nil {
		return nil
	}
	hash := strings.TrimSpace(block.GetHash())
	if hash == "" {
		return nil
	}
	if m.cache != nil {
		if err := m.cache.StoreObject(hash, block.GetContent()); err != nil {
			log.Printf("Failed to update cache block %s: %v", hash, err)
		}
	}
	m.blockContents[hash] = block.GetContent()

	waiters := m.blockWaiters[hash]
	for _, pending := range waiters {
		pending.remainingBlocks--
		if pending.remainingBlocks > 0 {
			continue
		}
		writtenPath, err := writeSliceCheckoutFile(m.dir, m.cache, pending.meta, nil, nil, m.blockContents, &m.cachedHits)
		if err != nil {
			return err
		}
		if writtenPath == "" {
			return fmt.Errorf("failed to materialize %s after receiving all checkout blocks", pending.meta.GetPath())
		}
		m.changedPaths = append(m.changedPaths, writtenPath)
		delete(m.pendingFiles, pending.meta.GetFileId())
		for _, blockHash := range pending.missingBlockHashes {
			if remaining := m.blockRefCounts[blockHash] - 1; remaining <= 0 {
				delete(m.blockRefCounts, blockHash)
				delete(m.blockContents, blockHash)
				delete(m.blockWaiters, blockHash)
			} else {
				m.blockRefCounts[blockHash] = remaining
			}
		}
	}
	return nil
}

func (m *streamedCheckoutMaterializer) handleFile(file *slicev1.FileContent) error {
	if file == nil {
		return nil
	}
	meta, ok := m.directFiles[file.GetFileId()]
	if !ok {
		return nil
	}
	fileContents := map[string][]byte{file.GetFileId(): file.GetContent()}
	explicitFiles := map[string]struct{}{file.GetFileId(): {}}
	writtenPath, err := writeSliceCheckoutFile(m.dir, m.cache, meta, explicitFiles, fileContents, nil, &m.cachedHits)
	if err != nil {
		return err
	}
	if writtenPath == "" {
		return fmt.Errorf("failed to materialize streamed file %s", meta.GetPath())
	}
	m.changedPaths = append(m.changedPaths, writtenPath)
	delete(m.directFiles, file.GetFileId())
	return nil
}

func (m *streamedCheckoutMaterializer) finish() (*checkoutMaterialization, error) {
	if len(m.missingKnown) > 0 {
		hashes := make([]string, 0, len(m.missingKnown))
		for hash := range m.missingKnown {
			hashes = append(hashes, hash)
		}
		sort.Strings(hashes)
		return nil, &staleCheckoutCacheError{Hashes: hashes}
	}
	if len(m.pendingFiles) > 0 {
		pending := make([]string, 0, len(m.pendingFiles))
		for _, file := range m.pendingFiles {
			pending = append(pending, file.meta.GetPath())
		}
		sort.Strings(pending)
		return nil, fmt.Errorf("checkout stream ended before receiving blocks for %s", strings.Join(pending, ", "))
	}
	if len(m.directFiles) > 0 {
		pending := make([]string, 0, len(m.directFiles))
		for _, meta := range m.directFiles {
			pending = append(pending, meta.GetPath())
		}
		sort.Strings(pending)
		return nil, fmt.Errorf("checkout stream ended before receiving files for %s", strings.Join(pending, ", "))
	}
	if err := removeEmptyCheckoutDirs(m.dir); err != nil {
		return nil, err
	}
	sort.Strings(m.changedPaths)
	return &checkoutMaterialization{
		CacheHits:    m.cachedHits,
		ChangedPaths: uniqueCheckoutPaths(m.changedPaths),
	}, nil
}

func writeSliceCheckoutFile(
	dir string,
	cache *CacheManager,
	fm *slicev1.FileMetadata,
	explicitFiles map[string]struct{},
	fileContents map[string][]byte,
	blockContents map[string][]byte,
	cachedHits *int64,
) (string, error) {
	if fm == nil {
		return "", nil
	}

	if writtenPath, wrote, err := tryWriteSliceCheckoutFileFromCache(dir, cache, fm, blockContents, cachedHits); err != nil {
		return "", err
	} else if wrote {
		return writtenPath, nil
	}

	var content []byte
	if content == nil {
		if data, ok := fileContents[fm.FileId]; ok {
			content = data
			if data == nil {
				content = []byte{}
			}
		}
	}

	if content == nil {
		if _, ok := explicitFiles[fm.FileId]; !ok {
			return "", nil
		}
		content = []byte{}
	}

	resolvedHash := checkoutFileContentHash(fm, content)
	if cache != nil && resolvedHash != "" {
		if err := cache.StoreObject(resolvedHash, content); err != nil {
			log.Printf("Failed to update cache for %s: %v", fm.Path, err)
		}
	}

	targetPath := filepath.Join(dir, fm.Path)
	if err := os.RemoveAll(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	mode := os.FileMode(0o644)
	if fm.GetExecutable() {
		mode = 0o755
	}
	return fm.GetPath(), os.WriteFile(targetPath, content, mode)
}

func tryWriteSliceCheckoutFileFromCache(
	dir string,
	cache *CacheManager,
	fm *slicev1.FileMetadata,
	blockContents map[string][]byte,
	cachedHits *int64,
) (string, bool, error) {
	if fm == nil {
		return "", false, nil
	}

	targetPath := filepath.Join(dir, fm.Path)
	if fm.GetSymlinkTarget() != "" {
		if err := os.RemoveAll(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		return fm.GetPath(), true, os.Symlink(fm.GetSymlinkTarget(), targetPath)
	}

	mode := os.FileMode(0o644)
	if fm.GetExecutable() {
		mode = 0o755
	}

	if cache != nil {
		resolvedHash := checkoutFileContentHash(fm, nil)
		if resolvedHash == "" {
			goto assembleFromContent
		}
		if info, err := os.Lstat(targetPath); err == nil {
			if info.IsDir() {
				if err := os.RemoveAll(targetPath); err != nil {
					return "", false, err
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		if err := cache.CopyObjectToFile(resolvedHash, targetPath, mode); err == nil {
			atomic.AddInt64(cachedHits, 1)
			return fm.GetPath(), true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Printf("Failed to copy cached object for %s: %v", fm.Path, err)
		}
	}

assembleFromContent:
	content, hits, err := assembleCheckoutFile(cache, fm, blockContents)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		log.Printf("Failed to assemble %s from cached blocks: %v", fm.Path, err)
		return "", false, nil
	}
	if hits > 0 {
		atomic.AddInt64(cachedHits, hits)
	}
	resolvedHash := checkoutFileContentHash(fm, content)
	if cache != nil && resolvedHash != "" {
		if err := cache.StoreObject(resolvedHash, content); err != nil {
			log.Printf("Failed to update cache for %s: %v", fm.Path, err)
		}
	}
	if err := os.RemoveAll(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	return fm.GetPath(), true, os.WriteFile(targetPath, content, mode)
}

func newCheckoutMaterializationLookup(index *localCheckoutIndex) *checkoutIndexLookup {
	if index == nil {
		return nil
	}
	return newCheckoutIndexLookup(index)
}

func canReuseExistingCheckoutFile(dir string, previousLookup *checkoutIndexLookup, fm *slicev1.FileMetadata) (bool, error) {
	if previousLookup == nil || fm == nil {
		return false, nil
	}

	cleanedPath := filepath.Clean(strings.TrimSpace(fm.GetPath()))
	if cleanedPath == "" || cleanedPath == "." {
		return false, nil
	}
	tracked, ok := previousLookup.files[cleanedPath]
	if !ok {
		return false, nil
	}
	if tracked.Executable != fm.GetExecutable() || strings.TrimSpace(tracked.SymlinkTarget) != strings.TrimSpace(fm.GetSymlinkTarget()) {
		return false, nil
	}

	resolvedHash := checkoutFileContentHash(fm, nil)
	if resolvedHash == "" || strings.TrimSpace(tracked.Hash) == "" || strings.TrimSpace(tracked.Hash) != resolvedHash {
		return false, nil
	}

	targetPath := filepath.Join(dir, cleanedPath)
	info, err := os.Lstat(targetPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(tracked.SymlinkTarget) != "" {
		if info.Mode()&os.ModeSymlink == 0 {
			return false, nil
		}
		target, err := os.Readlink(targetPath)
		if err != nil {
			return false, err
		}
		return target == tracked.SymlinkTarget, nil
	}
	if info.IsDir() {
		return false, nil
	}
	return checkoutTrackedFileMatches(targetPath, info, tracked)
}

func checkoutFileContentHash(fm *slicev1.FileMetadata, content []byte) string {
	if fm == nil {
		return ""
	}
	if hash := strings.TrimSpace(fm.GetHash()); hash != "" {
		return hash
	}
	if fm.GetSymlinkTarget() != "" {
		return storage.HashFileManifestContent([]byte(fm.GetSymlinkTarget()), false, fm.GetSymlinkTarget())
	}
	if content == nil {
		return ""
	}
	return storage.HashFileManifestContent(content, fm.GetExecutable(), "")
}

func prepareCheckoutDirectories(root string, fileMetadata []*slicev1.FileMetadata) ([]string, error) {
	if len(fileMetadata) == 0 {
		return nil, nil
	}

	dirs := make(map[string]struct{}, len(fileMetadata))
	for _, meta := range fileMetadata {
		if meta == nil {
			continue
		}
		targetDir := filepath.Dir(filepath.Join(root, meta.GetPath()))
		if targetDir == "." || targetDir == root {
			continue
		}
		dirs[targetDir] = struct{}{}
	}

	if len(dirs) == 0 {
		return nil, nil
	}

	ordered := make([]string, 0, len(dirs))
	for dir := range dirs {
		ordered = append(ordered, dir)
	}
	sort.Strings(ordered)
	removedConflicts := make([]string, 0)
	for _, dir := range ordered {
		removed, err := ensureCheckoutDirectory(root, dir)
		if err != nil {
			return nil, err
		}
		removedConflicts = append(removedConflicts, removed...)
	}
	sort.Strings(removedConflicts)
	return uniqueCheckoutPaths(removedConflicts), nil
}

func ensureCheckoutDirectory(root, targetDir string) ([]string, error) {
	rel, err := filepath.Rel(root, targetDir)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return nil, nil
	}

	current := root
	removed := make([]string, 0)
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if strings.TrimSpace(part) == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil {
			if info.IsDir() {
				continue
			}
			if err := os.RemoveAll(current); err != nil {
				return nil, err
			}
			relCurrent, relErr := filepath.Rel(root, current)
			if relErr != nil {
				return nil, relErr
			}
			removed = append(removed, relCurrent)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	return removed, nil
}

func removeStaleTrackedCheckoutFiles(dir string, trackedFiles []string, fileMetadata []*slicev1.FileMetadata) ([]string, error) {
	removed := make([]string, 0)
	desired := make(map[string]struct{}, len(fileMetadata))
	directoryMarkers := checkoutDirectoryMarkers(fileMetadata)
	for _, meta := range fileMetadata {
		if meta == nil {
			continue
		}
		cleaned := filepath.Clean(meta.GetPath())
		if _, ok := directoryMarkers[cleaned]; ok {
			continue
		}
		desired[cleaned] = struct{}{}
	}
	for _, tracked := range trackedFiles {
		cleaned := filepath.Clean(tracked)
		if cleaned == ".gitignore" {
			continue
		}
		if strings.HasPrefix(cleaned, ".gs"+string(os.PathSeparator)) || cleaned == ".gs" {
			continue
		}
		if _, ok := desired[cleaned]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(dir, cleaned)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		removed = append(removed, cleaned)
	}
	sort.Strings(removed)
	return removed, nil
}

func checkoutDirectoryMarkers(fileMetadata []*slicev1.FileMetadata) map[string]struct{} {
	if len(fileMetadata) == 0 {
		return nil
	}

	paths := make(map[string]struct{}, len(fileMetadata))
	for _, meta := range fileMetadata {
		if meta == nil {
			continue
		}
		cleaned := filepath.Clean(strings.TrimSpace(meta.GetPath()))
		if cleaned == "" || cleaned == "." {
			continue
		}
		paths[cleaned] = struct{}{}
	}

	markers := make(map[string]struct{})
	for path := range paths {
		dir := filepath.Dir(path)
		for dir != "." && dir != "/" && dir != "" {
			if _, ok := paths[dir]; ok {
				markers[dir] = struct{}{}
			}
			next := filepath.Dir(dir)
			if next == dir {
				break
			}
			dir = next
		}
	}
	return markers
}

func uniqueCheckoutPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := paths[:0]
	var prev string
	for _, path := range paths {
		cleaned := filepath.Clean(strings.TrimSpace(path))
		if cleaned == "" {
			continue
		}
		if len(out) > 0 && cleaned == prev {
			continue
		}
		out = append(out, cleaned)
		prev = cleaned
	}
	return out
}

func removeEmptyCheckoutDirs(root string) error {
	dirs := make([]string, 0)
	err := filepath.Walk(root, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			return nil
		}
		if current == root {
			return nil
		}
		base := filepath.Base(current)
		if base == ".git" || base == ".gs" {
			return filepath.SkipDir
		}
		dirs = append(dirs, current)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, dir := range dirs {
		if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			if !errors.Is(err, os.ErrExist) && !strings.Contains(err.Error(), "directory not empty") {
				return err
			}
		}
	}
	return nil
}

func assembleCheckoutFile(cache *CacheManager, fm *slicev1.FileMetadata, blockContents map[string][]byte) ([]byte, int64, error) {
	if fm == nil {
		return nil, 0, os.ErrNotExist
	}
	if len(fm.GetBlocks()) == 0 {
		if fm.GetSize() == 0 {
			return []byte{}, 0, nil
		}
		return nil, 0, os.ErrNotExist
	}

	manifest := &models.FileManifest{
		Path:      fm.GetFileId(),
		TotalSize: fm.GetSize(),
		Hash:      fm.GetHash(),
		Blocks:    make([]models.Block, 0, len(fm.GetBlocks())),
	}
	for _, block := range fm.GetBlocks() {
		if block == nil {
			continue
		}
		manifest.Blocks = append(manifest.Blocks, models.Block{
			Hash: block.GetHash(),
			Size: int(block.GetSize()),
		})
	}
	if len(manifest.Blocks) == 0 {
		if fm.GetSize() == 0 {
			return []byte{}, 0, nil
		}
		return nil, 0, os.ErrNotExist
	}

	var cacheHits int64
	data, err := storage.AssembleFile(manifest, func(hash string) ([]byte, error) {
		if data, ok := blockContents[hash]; ok {
			return data, nil
		}
		if cache == nil {
			return nil, os.ErrNotExist
		}
		data, err := cache.ReadObject(hash)
		if err == nil {
			cacheHits++
		}
		return data, err
	})
	if err != nil {
		return nil, 0, err
	}
	return data, cacheHits, nil
}
