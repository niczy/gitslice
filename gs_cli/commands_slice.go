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

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleSliceCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printSliceHelp()
		return
	}

	switch args[0] {
	case "checkout", "clone":
		handleSliceCheckout(ctx, cli, args[1:])
	default:
		log.Printf("Unknown slice command: %s", args[0])
		printSliceHelp()
	}
}

func handleSliceCheckout(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice checkout|clone <slice-id> [--commit <commit-hash>]")
		return
	}

	sliceID, err := normalizeSliceID(args[0])
	if err != nil {
		log.Fatalf("Invalid slice ID: %v", err)
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

	if err := os.MkdirAll(".gs", 0o755); err != nil {
		log.Fatalf("Failed to create .gs directory: %v", err)
	}
	if err := writeSliceIDConfig(sliceID); err != nil {
		log.Fatalf("Failed to write config file: %v", err)
	}

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
