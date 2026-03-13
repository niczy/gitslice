package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleStatus(ctx context.Context, cli *CLI) {
	// Check if in a gitslice directory
	if _, err := os.Stat(".gs"); os.IsNotExist(err) {
		log.Println("Not in a gitslice directory. Run 'gs init <slice-id>' to initialize.")
		return
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		log.Printf("Failed to read .gs/config: %v", err)
		return
	}

	req := &slicev1.StateRequest{
		SliceId: sliceID,
	}

	resp, err := cli.sliceClient.GetSliceState(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice state: %v", err)
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Printf("Head: %s\n", resp.LatestCommitHash)
	fmt.Printf("Modified files: %d\n", len(resp.ModifiedFiles))
	fmt.Printf("Last modified: %s\n", formatTimestamp(resp.LastModified))
	fmt.Printf("Working directory: Clean\n")
}

func handleInit(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs init <slice-id>")
		return
	}

	sliceID, err := normalizeSliceID(args[0])
	if err != nil {
		log.Fatalf("Invalid slice ID: %v", err)
	}

	// Check if directory is empty
	entries, err := os.ReadDir(".")
	if err != nil {
		log.Fatalf("Failed to read directory: %v", err)
	}

	if len(entries) > 0 {
		log.Fatal("Directory is not empty. Please initialize in an empty directory or use --force.")
	}

	// Create .gs directory
	if err := os.MkdirAll(".gs", 0755); err != nil {
		log.Fatalf("Failed to create .gs directory: %v", err)
	}

	// Write config file
	if err := writeSliceIDConfig(sliceID); err != nil {
		log.Fatalf("Failed to write config file: %v", err)
	}

	if _, err := ensureGitRepo("."); err != nil {
		log.Fatalf("Failed to initialize git repository: %v", err)
	}
	if err := ensureGitignoreEntry(".", ".gs/"); err != nil {
		log.Fatalf("Failed to update .gitignore: %v", err)
	}

	fmt.Printf("Initialized empty gitslice repository for slice: %s\n", sliceID)
}

func handleLog(ctx context.Context, cli *CLI, args []string) {
	if len(args) > 1 {
		log.Println("Usage: gs log [<slice-id>]")
		return
	}

	var sliceID string
	var err error
	if len(args) == 1 {
		sliceID, err = normalizeSliceID(args[0])
		if err != nil {
			log.Fatalf("Invalid slice ID: %v", err)
		}
	} else {
		sliceID, err = sliceIDFromConfig()
		if err != nil {
			log.Printf("Failed to read slice binding: %v", err)
			return
		}
	}

	req := &slicev1.CommitHistoryRequest{
		SliceId: sliceID,
		Limit:   10,
	}

	resp, err := cli.sliceClient.GetSliceCommits(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice commits: %v", err)
	}

	fmt.Printf("Commit history for slice: %s\n", sliceID)
	fmt.Printf("%d commit(s)\n\n", len(resp.Commits))
	for _, commit := range resp.Commits {
		fmt.Printf("%s %s\n", commit.CommitHash, commit.Message)
	}
}

func handleRootSlice(ctx context.Context, cli *CLI) {
	resp, err := cli.sliceClient.GetRootSlice(ctx, &slicev1.GetRootSliceRequest{})
	if err != nil {
		log.Fatalf("Failed to get root slice: %v", err)
	}

	fmt.Printf("Root Slice ID: %s\n", resp.SliceId)
	fmt.Printf("Commit Hash: %s\n", resp.CommitHash)
}

func handleRenameSlice(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 2 {
		log.Println("Usage: gs slice rename <slice-id> <new-name>")
		return
	}

	sliceID, err := normalizeSliceID(args[0])
	if err != nil {
		log.Fatalf("Invalid slice ID: %v", err)
	}

	newName := strings.TrimSpace(args[1])
	if newName == "" {
		log.Fatal("New name cannot be empty")
	}

	resp, err := cli.sliceClient.RenameSlice(ctx, &slicev1.RenameSliceRequest{
		SliceId: sliceID,
		NewName: newName,
	})
	if err != nil {
		log.Fatalf("Failed to rename slice: %v", err)
	}

	fmt.Printf("Renamed slice %s to %q\n", resp.SliceId, resp.Name)
}
