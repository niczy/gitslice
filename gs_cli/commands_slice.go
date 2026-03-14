package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleSliceCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printSliceHelp()
		return
	}

	switch args[0] {
	case "create":
		handleSliceCreate(ctx, cli, args[1:])
	case "checkout", "clone":
		handleSliceCheckout(ctx, cli, args[1:])
	case "sync":
		handleSliceSync(ctx, cli, args[1:])
	case "checkouts":
		handleSliceCheckouts(args[1:])
	case "rename":
		handleRenameSlice(ctx, cli, args[1:])
	default:
		log.Printf("Unknown slice command: %s", args[0])
		printSliceHelp()
	}
}

func handleSliceCreate(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 2 {
		log.Println("Usage: gs slice create <name> <folder-path[,folder-path...]> [--folders <folder-path[,folder-path...]>]")
		return
	}

	sliceName := strings.TrimSpace(args[0])
	if sliceName == "" {
		log.Println("Slice name cannot be empty")
		return
	}

	folderPaths := parseSliceFolderPaths(args[1])

	fs := flag.NewFlagSet("slice create", flag.ExitOnError)
	description := fs.String("description", "Focused slice", "Description of the new slice")
	moreFolders := fs.String("folders", "", "Additional comma-separated folder paths to include in this slice")
	fs.Parse(args[2:])

	folderPaths = append(folderPaths, parseSliceFolderPaths(*moreFolders)...)
	if len(folderPaths) == 0 {
		log.Println("At least one folder path is required")
		return
	}

	rootResp, err := cli.sliceClient.GetRootSlice(ctx, &slicev1.GetRootSliceRequest{})
	if err != nil {
		log.Fatalf("Failed to resolve published root slice: %v", err)
	}

	req := &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: rootResp.GetSliceId(),
		FolderPath:    folderPaths[0],
		FolderPaths:   folderPaths[1:],
		Name:          sliceName,
		Description:   *description,
	}

	resp, err := cli.sliceClient.CreateSliceFromFolder(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create slice: %v", err)
	}

	fmt.Printf("Created slice: %s (id: %s)\n", resp.Name, resp.SliceId)
	if resp.GetSlug() != "" {
		fmt.Printf("Slug: %s\n", resp.GetSlug())
	}
	fmt.Printf("Status: %s\n", resp.Status)
}

func handleSliceCheckout(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice checkout|clone <slice-id-or-slug> [--commit <commit-hash>]")
		return
	}

	sliceID, err := resolveSliceRef(ctx, cli, args[0])
	if err != nil {
		log.Fatalf("Invalid slice reference: %v", err)
	}

	// Parse flags
	fs := flag.NewFlagSet("slice checkout", flag.ExitOnError)
	commitHash := fs.String("commit", "HEAD", "Commit hash to checkout")
	fs.Parse(args[1:])

	entries, err := os.ReadDir(".")
	if err != nil {
		log.Fatalf("Failed to read directory: %v", err)
	}
	if len(entries) > 0 {
		log.Fatal("Directory is not empty. Please checkout into an empty directory.")
	}

	resp, cache, err := fetchSliceCheckoutResponse(ctx, cli, sliceID, *commitHash)
	if err != nil {
		log.Fatalf("Failed to checkout slice: %v", err)
	}

	if err := os.MkdirAll(".gs", 0o755); err != nil {
		log.Fatalf("Failed to create .gs directory: %v", err)
	}
	if err := writeSliceIDConfig(sliceID); err != nil {
		log.Fatalf("Failed to write config file: %v", err)
	}

	cachedHits, err := materializeSliceCheckout(".", resp, cache, false)
	if err != nil {
		log.Fatalf("Failed to materialize checkout: %v", err)
	}

	createdRepo, err := ensureGitRepo(".")
	if err != nil {
		log.Fatalf("Failed to initialize git repository: %v", err)
	}
	if err := ensureGitignoreEntry(".", ".gs/"); err != nil {
		log.Fatalf("Failed to update .gitignore: %v", err)
	}
	if _, err := runGitCommand(".", "checkout", "-B", "main"); err != nil {
		log.Fatalf("Failed to switch to main branch: %v", err)
	}
	hasCommit, err := gitHasCommit(".")
	if err != nil {
		log.Fatalf("Failed to check git history: %v", err)
	}
	if _, err := runGitCommand(".", "add", "-A"); err != nil {
		log.Fatalf("Failed to stage checkout files: %v", err)
	}
	hasPendingChanges, err := gitHasPendingChanges(".")
	if err != nil {
		log.Fatalf("Failed to read git status: %v", err)
	}
	if createdRepo || !hasCommit || hasPendingChanges {
		if err := createCheckoutCommit(".", resp.Manifest.CommitHash); err != nil {
			log.Fatalf("Failed to create checkout commit: %v", err)
		}
	}

	// Display checkout results
	fmt.Printf("Checked out slice: %s\n", sliceID)
	fmt.Printf("Commit: %s\n", resp.Manifest.CommitHash)
	fmt.Printf("Files: %d\n", len(resp.Manifest.FileMetadata))

	if len(resp.Manifest.FileMetadata) > 0 {
		fmt.Println("\nFiles in slice:")
		for _, fm := range resp.Manifest.FileMetadata {
			fmt.Printf("  - %s (%d bytes)\n", fm.Path, fm.Size)
		}
	}

	if cache != nil {
		fmt.Printf("Cache hits: %d\n", cachedHits)
	}

	if err := registerCheckout(".", sliceID, resp.Manifest.CommitHash); err != nil {
		log.Printf("Warning: failed to register checkout path: %v", err)
	}
}

func handleSliceSync(ctx context.Context, cli *CLI, args []string) {
	fs := flag.NewFlagSet("slice sync", flag.ExitOnError)
	commitHash := fs.String("commit", "HEAD", "Commit hash to sync to")
	parseFlagSetInterspersed(fs, args)
	if fs.NArg() != 0 {
		log.Println("Usage: gs slice sync [--commit <commit-hash>]")
		return
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		log.Fatalf("Failed to read current slice binding: %v", err)
	}

	createdRepo, err := ensureGitRepo(".")
	if err != nil {
		log.Fatalf("Failed to initialize git repository: %v", err)
	}
	if createdRepo {
		if _, err := runGitCommand(".", "checkout", "-B", "main"); err != nil {
			log.Fatalf("Failed to switch to main branch: %v", err)
		}
	} else if err := requireMainBranch("."); err != nil {
		log.Fatalf("Cannot sync slice: %v", err)
	}

	hasCommit, err := gitHasCommit(".")
	if err != nil {
		log.Fatalf("Failed to check git history: %v", err)
	}
	hasPendingChanges, err := gitHasPendingChanges(".")
	if err != nil {
		log.Fatalf("Failed to read git status: %v", err)
	}
	if hasPendingChanges {
		log.Fatal("Cannot sync slice: working tree has local changes. Commit or clean the checkout first.")
	}
	if err := ensureGitignoreEntry(".", ".gs/"); err != nil {
		log.Fatalf("Failed to update .gitignore: %v", err)
	}

	resp, cache, err := fetchSliceCheckoutResponse(ctx, cli, sliceID, *commitHash)
	if err != nil {
		log.Fatalf("Failed to sync slice: %v", err)
	}
	cachedHits, err := materializeSliceCheckout(".", resp, cache, true)
	if err != nil {
		log.Fatalf("Failed to materialize synced slice: %v", err)
	}

	if _, err := runGitCommand(".", "add", "-A"); err != nil {
		log.Fatalf("Failed to stage synced files: %v", err)
	}
	hasPendingChanges, err = gitHasPendingChanges(".")
	if err != nil {
		log.Fatalf("Failed to read git status: %v", err)
	}

	lastCommitMessage := ""
	if hasCommit {
		lastCommitMessage, err = gitLatestCommitMessage(".")
		if err != nil {
			log.Fatalf("Failed to read latest git commit message: %v", err)
		}
	}

	status := "up to date"
	if createdRepo || !hasCommit || hasPendingChanges || !strings.Contains(lastCommitMessage, resp.Manifest.CommitHash) {
		if err := createSyncCommit(".", resp.Manifest.CommitHash); err != nil {
			log.Fatalf("Failed to create sync commit: %v", err)
		}
		status = "updated"
	}

	fmt.Printf("Synced slice: %s\n", sliceID)
	fmt.Printf("Commit: %s\n", resp.Manifest.CommitHash)
	fmt.Printf("Files: %d\n", len(resp.Manifest.FileMetadata))
	fmt.Printf("Status: %s\n", status)
	if cache != nil {
		fmt.Printf("Cache hits: %d\n", cachedHits)
	}

	if err := registerCheckout(".", sliceID, resp.Manifest.CommitHash); err != nil {
		log.Printf("Warning: failed to update checkout registry: %v", err)
	}
}

func handleSliceCheckouts(args []string) {
	fs := flag.NewFlagSet("slice checkouts", flag.ExitOnError)
	filterSliceID := fs.String("slice", "", "Filter to one slice ID")
	fs.Parse(args)

	normalizedSliceID := ""
	if strings.TrimSpace(*filterSliceID) != "" {
		var err error
		normalizedSliceID, err = normalizeSliceID(*filterSliceID)
		if err != nil {
			log.Fatalf("Invalid slice ID: %v", err)
		}
	}

	records, err := listCheckoutRecords()
	if err != nil {
		log.Fatalf("Failed to read checkout registry: %v", err)
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

func fetchSliceCheckoutResponse(ctx context.Context, cli *CLI, sliceID, commitHash string) (*slicev1.CheckoutResponse, *CacheManager, error) {
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

	resp, err := cli.sliceClient.CheckoutSlice(ctx, req)
	if err != nil {
		return nil, cache, err
	}
	return resp, cache, nil
}

func materializeSliceCheckout(dir string, resp *slicev1.CheckoutResponse, cache *CacheManager, pruneTracked bool) (int64, error) {
	if resp == nil || resp.GetManifest() == nil {
		return 0, fmt.Errorf("missing checkout manifest")
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

	if pruneTracked {
		if err := removeStaleTrackedCheckoutFiles(dir, resp.Manifest.FileMetadata); err != nil {
			return 0, err
		}
	}

	var cachedHits int64
	workerCount := checkoutWorkerCount(len(resp.Manifest.FileMetadata))
	if workerCount > 0 {
		runCheckoutJobs(workerCount, resp.Manifest.FileMetadata, func(fm *slicev1.FileMetadata) {
			if err := writeSliceCheckoutFile(dir, cache, fm, explicitFiles, fileContents, blockContents, &cachedHits); err != nil {
				log.Printf("Failed to write %s: %v", fm.GetPath(), err)
			}
		})
	}

	if pruneTracked {
		if err := removeEmptyCheckoutDirs(dir); err != nil {
			return 0, err
		}
	}
	return atomic.LoadInt64(&cachedHits), nil
}

func writeSliceCheckoutFile(
	dir string,
	cache *CacheManager,
	fm *slicev1.FileMetadata,
	explicitFiles map[string]struct{},
	fileContents map[string][]byte,
	blockContents map[string][]byte,
	cachedHits *int64,
) error {
	if fm == nil {
		return nil
	}

	var content []byte
	if cache != nil && fm.Hash != "" {
		if data, err := cache.ReadObject(fm.Hash); err == nil {
			content = data
			atomic.AddInt64(cachedHits, 1)
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Printf("Failed to read cached object for %s: %v", fm.Path, err)
		}
	}

	if content == nil {
		if data, hits, err := assembleCheckoutFile(cache, fm, blockContents); err == nil {
			content = data
			if hits > 0 {
				atomic.AddInt64(cachedHits, hits)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("Failed to assemble %s from cached blocks: %v", fm.Path, err)
		}
	}

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
			return nil
		}
		content = []byte{}
	}

	if cache != nil && fm.Hash != "" {
		if err := cache.StoreObject(fm.Hash, content); err != nil {
			log.Printf("Failed to update cache for %s: %v", fm.Path, err)
		}
	}

	targetPath := filepath.Join(dir, fm.Path)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, content, 0o644)
}

func removeStaleTrackedCheckoutFiles(dir string, fileMetadata []*slicev1.FileMetadata) error {
	trackedFiles, err := gitTrackedFiles(dir)
	if err != nil {
		return err
	}
	desired := make(map[string]struct{}, len(fileMetadata))
	for _, meta := range fileMetadata {
		if meta == nil {
			continue
		}
		desired[filepath.Clean(meta.GetPath())] = struct{}{}
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
			return err
		}
	}
	return nil
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
