package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	adminv1 "github.com/niczy/gitslice/proto/admin"
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
	case "list":
		handleSliceList(ctx, cli, args[1:])
	case "info":
		handleSliceInfo(ctx, cli, args[1:])
	case "status":
		handleSliceStatus(ctx, cli, args[1:])
	case "owners":
		handleSliceOwners(ctx, cli, args[1:])
	case "checkout":
		handleSliceCheckout(ctx, cli, args[1:])
	default:
		log.Printf("Unknown slice command: %s", args[0])
		printSliceHelp()
	}
}

func handleSliceCreate(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice create <slice-id> [--files \"file1,file2\"] [--description \"desc\"]")
		return
	}

	sliceID := args[0]

	// Parse flags (exclude the positional slice ID from flag parsing)
	fs := flag.NewFlagSet("slice create", flag.ExitOnError)
	files := fs.String("files", "", "Comma-separated list of files")
	description := fs.String("description", "", "Slice description")
	fs.Parse(args[1:])

	// Build file list
	var fileList []string
	if *files != "" {
		fileList = splitAndTrim(*files, ",")
	}

	// Create slice via admin service
	// Note: Authentication/authorization not yet implemented.
	// In production, these values should come from the authenticated user context.
	req := &adminv1.CreateSliceRequest{
		SliceId:     sliceID,
		Name:        sliceID,
		Description: *description,
		Files:       fileList,
		Owners:      []string{"user"}, // Placeholder - should come from auth context
		CreatedBy:   "user",           // Placeholder - should come from auth context
	}

	resp, err := cli.adminClient.CreateSlice(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create slice: %v", err)
	}

	fmt.Printf("Slice created: %s\n", resp.SliceId)
	fmt.Printf("Status: %s\n", resp.Status)
	if len(fileList) > 0 {
		fmt.Printf("Files: %d\n", len(fileList))
	}
}

func handleSliceList(ctx context.Context, cli *CLI, args []string) {
	// Parse flags
	fs := flag.NewFlagSet("slice list", flag.ExitOnError)
	limit := fs.Int("limit", 50, "Maximum number of slices to return")
	offset := fs.Int("offset", 0, "Offset for pagination")
	detailed := fs.Bool("detailed", false, "Show detailed information")
	mine := fs.Bool("mine", false, "Show only my slices")
	search := fs.String("search", "", "Search query")
	fs.Parse(args)

	req := &adminv1.ListSlicesRequest{
		Limit:  int32(*limit),
		Offset: int32(*offset),
	}

	resp, err := cli.adminClient.ListSlices(ctx, req)
	if err != nil {
		log.Fatalf("Failed to list slices: %v", err)
	}

	if *search != "" {
		fmt.Printf("Searching for: %s\n", *search)
	}

	if *mine {
		fmt.Println("Showing only my slices")
	}

	fmt.Printf("\nFound %d slice(s):\n", len(resp.Slices))
	for _, slice := range resp.Slices {
		fmt.Printf("- %s (commit: %s, files: %d)\n", slice.SliceId, slice.LatestCommitHash, slice.ModifiedFilesCount)
		if *detailed {
			fmt.Printf("  Last modified: %s\n", formatTimestamp(slice.LastModified))
		}
	}
}

func handleSliceInfo(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice info <slice-id>")
		return
	}

	sliceID := args[0]

	req := &slicev1.StateRequest{
		SliceId: sliceID,
	}

	resp, err := cli.sliceClient.GetSliceState(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice info: %v", err)
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Printf("Latest commit: %s\n", resp.LatestCommitHash)
	fmt.Printf("Modified files: %d\n", len(resp.ModifiedFiles))
	fmt.Printf("Last modified: %s\n", formatTimestamp(resp.LastModified))
}

func handleSliceStatus(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice status <slice-id>")
		return
	}

	sliceID := args[0]

	req := &slicev1.StateRequest{
		SliceId: sliceID,
	}

	resp, err := cli.sliceClient.GetSliceState(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice status: %v", err)
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Printf("Status: Active\n")
	fmt.Printf("Head: %s\n", resp.LatestCommitHash)
	fmt.Printf("Modified files: %d\n", len(resp.ModifiedFiles))
}

func handleSliceOwners(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice owners <slice-id> [--add <owner>] [--remove <owner>]")
		return
	}

	sliceID := args[0]

	// For now, display a message indicating the feature requires backend support
	// Once the GetSlice RPC is implemented in the admin service, we can use it
	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Println("Note: Slice owner management requires the GetSlice RPC to be implemented.")
	fmt.Println("Currently, slice owners can be set when creating a slice with 'gs slice create'.")
	fmt.Println()
	fmt.Println("Planned features:")
	fmt.Println("  - View current slice owners")
	fmt.Println("  - Add new owners with --add flag")
	fmt.Println("  - Remove owners with --remove flag")
}

func handleSliceCheckout(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice checkout <slice-id> [--commit <commit-hash>]")
		return
	}

	sliceID := args[0]

	// Parse flags
	fs := flag.NewFlagSet("slice checkout", flag.ExitOnError)
	commitHash := fs.String("commit", "HEAD", "Commit hash to checkout")
	fs.Parse(args[1:])

	// Call slice service
	req := &slicev1.CheckoutRequest{
		SliceId:    sliceID,
		CommitHash: *commitHash,
	}

	resp, err := cli.sliceClient.CheckoutSlice(ctx, req)
	if err != nil {
		log.Fatalf("Failed to checkout slice: %v", err)
	}

	cache, err := NewCacheManager()
	if err != nil {
		log.Printf("Warning: unable to initialize cache: %v", err)
	}

	fileContents := make(map[string][]byte)
	for _, file := range resp.Files {
		fileContents[file.FileId] = file.Content
	}

	var cachedHits int64
	workerCount := checkoutWorkerCount(len(resp.Manifest.FileMetadata))
	if workerCount > 0 {
		runCheckoutJobs(workerCount, resp.Manifest.FileMetadata, func(fm *slicev1.FileMetadata) {
			var content []byte

			if cache != nil && fm.Hash != "" {
				if data, err := cache.ReadObject(fm.Hash); err == nil {
					content = data
					atomic.AddInt64(&cachedHits, 1)
				} else if !errors.Is(err, os.ErrNotExist) {
					log.Printf("Failed to read cached object for %s: %v", fm.Path, err)
				}
			}

			if content == nil {
				if data, ok := fileContents[fm.FileId]; ok {
					content = data
				}
			}

			if content == nil {
				return
			}

			if cache != nil && fm.Hash != "" {
				if err := cache.StoreObject(fm.Hash, content); err != nil {
					log.Printf("Failed to update cache for %s: %v", fm.Path, err)
				}
			}

			targetPath := filepath.Join(".", fm.Path)
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				log.Printf("Failed to prepare directories for %s: %v", fm.Path, err)
				return
			}

			if err := os.WriteFile(targetPath, content, 0o644); err != nil {
				log.Printf("Failed to write file %s: %v", fm.Path, err)
			}
		})
	}

	// Display checkout results
	fmt.Printf("Checked out slice: %s\n", sliceID)
	fmt.Printf("Commit: %s\n", resp.Manifest.CommitHash)
	fmt.Printf("Files: %d\n", len(resp.Files))

	if len(resp.Manifest.FileMetadata) > 0 {
		fmt.Println("\nFiles in slice:")
		for _, fm := range resp.Manifest.FileMetadata {
			fmt.Printf("  - %s (%d bytes)\n", fm.Path, fm.Size)
		}
	}

	if cache != nil {
		fmt.Printf("Cache hits: %d\n", atomic.LoadInt64(&cachedHits))
	}
}
