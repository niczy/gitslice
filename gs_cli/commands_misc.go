package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleStatus(ctx context.Context, cli *CLI, args []string) {
	handleSliceStatus(ctx, cli, args)
}

func handleInit(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("init")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs init <slice-id> [--json]")
		return
	}

	sliceID, err := normalizeSliceID(fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid slice ID: %v", err)
	}

	// Check if directory is empty
	entries, err := os.ReadDir(".")
	if err != nil {
		commandFatalf("INIT_FAILED", false, "", "Failed to read directory: %v", err)
	}

	if len(entries) > 0 {
		commandFatal("DIRECTORY_NOT_EMPTY", "Directory is not empty. Please initialize in an empty directory.", false, "")
	}

	// Create .gs directory
	if err := os.MkdirAll(".gs", 0755); err != nil {
		commandFatalf("INIT_FAILED", false, "", "Failed to create .gs directory: %v", err)
	}

	// Write config file
	if err := writeSliceIDConfig(sliceID); err != nil {
		commandFatalf("INIT_FAILED", false, "", "Failed to write config file: %v", err)
	}

	if err := writeCheckoutIndex(".", &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    sliceID,
		CommitHash: "",
	}); err != nil {
		commandFatalf("INIT_FAILED", false, "", "Failed to write checkout index: %v", err)
	}
	if err := resetDirtyTracker(".", &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    sliceID,
		CommitHash: "",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to start dirty tracker: %v\n", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonInitOutput{Status: "initialized", SliceID: sliceID})
		return
	}

	fmt.Printf("Initialized empty gitslice checkout for slice: %s\n", sliceID)
}

func handleLog(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("log")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() > 1 {
		commandUsage("Usage: gs log [<slice-id>] [--json]")
		return
	}
	var sliceID string
	var err error
	if fs.NArg() == 1 {
		sliceID, err = normalizeSliceID(fs.Arg(0))
		if err != nil {
			commandFatalf("INVALID_ARGUMENT", false, "", "Invalid slice ID: %v", err)
		}
	} else {
		sliceID, err = sliceIDFromConfig()
		if err != nil {
			commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read slice binding: %v", err)
		}
	}

	req := &slicev1.CommitHistoryRequest{
		SliceId: sliceID,
		Limit:   10,
	}

	resp, err := cli.sliceClient.GetSliceCommits(ctx, req)
	if err != nil {
		commandFatalf("SLICE_LOG_FAILED", true, "", "Failed to get slice commits: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
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
		commandFatalf("ROOT_SLICE_FAILED", true, "", "Failed to get root slice: %v", err)
	}
	if cliStructuredJSON {
		writeJSONOutput(resp)
		return
	}

	fmt.Printf("Root Slice ID: %s\n", resp.SliceId)
	fmt.Printf("Commit Hash: %s\n", resp.CommitHash)
}

func handleRenameSlice(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 2 {
		commandUsage("Usage: gs slice rename <slice-id> <new-name>")
		return
	}

	sliceID, err := normalizeSliceID(args[0])
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid slice ID: %v", err)
	}

	newName := strings.TrimSpace(args[1])
	if newName == "" {
		commandFatal("INVALID_ARGUMENT", "New name cannot be empty.", false, "")
	}

	resp, err := cli.sliceClient.RenameSlice(ctx, &slicev1.RenameSliceRequest{
		SliceId: sliceID,
		NewName: newName,
	})
	if err != nil {
		commandFatalf("SLICE_RENAME_FAILED", true, "", "Failed to rename slice: %v", err)
	}

	fmt.Printf("Renamed slice %s to %q\n", resp.SliceId, resp.Name)
}
