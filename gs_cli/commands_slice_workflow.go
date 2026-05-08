package gscli

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleSliceList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice list")
	limit := fs.Int("limit", 100, "Maximum slices to list")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs slice list [--limit <n>] [--json]")
		return
	}

	resp, err := cli.sliceClient.ListSlices(ctx, &slicev1.ListSlicesRequest{Limit: int32(*limit)})
	if err != nil {
		commandFatalf("SLICE_LIST_FAILED", true, "", "Failed to list slices: %v", err)
	}

	slices := append([]*slicev1.SliceInfo(nil), resp.GetSlices()...)
	sort.Slice(slices, func(i, j int) bool {
		if slices[i].GetUpdatedAt() == slices[j].GetUpdatedAt() {
			return slices[i].GetSliceId() < slices[j].GetSliceId()
		}
		return slices[i].GetUpdatedAt() > slices[j].GetUpdatedAt()
	})

	if jsonEnabled {
		out := jsonSliceListOutput{
			Total:  len(slices),
			Slices: make([]jsonSliceInfo, 0, len(slices)),
		}
		for _, slice := range slices {
			out.Slices = append(out.Slices, buildSliceInfoOutput(slice))
		}
		writeJSONOutput(out)
		return
	}

	fmt.Printf("Slices: %d\n", len(slices))
	for _, slice := range slices {
		fmt.Printf("- %s\n", slice.GetName())
		fmt.Printf("  ID: %s\n", slice.GetSliceId())
		if slice.GetSlug() != "" {
			fmt.Printf("  Slug: %s\n", slice.GetSlug())
		}
		if slice.GetDescription() != "" {
			fmt.Printf("  Description: %s\n", slice.GetDescription())
		}
		if len(slice.GetOwners()) > 0 {
			fmt.Printf("  Owners: %s\n", strings.Join(slice.GetOwners(), ", "))
		}
		fmt.Printf("  Files: %d\n", slice.GetFileCount())
		if slice.GetUpdatedAt() > 0 {
			fmt.Printf("  Updated: %s\n", formatTimestamp(slice.GetUpdatedAt()))
		}
	}
}

func handleSlicePublish(ctx context.Context, cli *CLI, commandName string, args []string) {
	sliceID, err := sliceIDFromConfig()
	if err != nil {
		commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read slice binding: %v", err)
		return
	}

	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice " + commandName)
	message := fs.String("message", "", "Changeset message")
	base := fs.String("base", "", "Base commit hash")
	files := fs.String("files", "", "Comma-separated file list")
	author := fs.String("author", "user", "Author of the changeset")
	changesetID := fs.String("changeset-id", "", "Existing changeset ID to append a new snapshot")
	reviewOnly := fs.Bool("review-only", false, "Create/update the tracked changeset and show review output without merging")
	noMerge := fs.Bool("no-merge", false, "Alias for --review-only")
	explicitMerge := fs.Bool("merge", false, "Merge the tracked changeset after review")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if (*reviewOnly || *noMerge) && *explicitMerge {
		commandFatal("INVALID_ARGUMENT", "Use either --merge or --review-only/--no-merge, not both.", false, "")
	}
	shouldMerge := commandName == "publish"
	if *reviewOnly || *noMerge {
		shouldMerge = false
	}
	if *explicitMerge {
		shouldMerge = true
	}

	resolvedChangesetID, isUpdate, err := resolveChangesetIDForExport(*changesetID)
	if err != nil {
		commandFatalf("CHANGESET_RESOLUTION_FAILED", false, "", "Failed to resolve tracked changeset ID: %v", err)
		return
	}

	modifiedFiles := []string{}
	if *files != "" {
		modifiedFiles = append(modifiedFiles, splitAndTrim(*files, ",")...)
	}
	modifiedFiles = append(modifiedFiles, fs.Args()...)
	modifiedFiles, _, err = resolveWorkingTreeModifiedFiles(".", modifiedFiles)
	if err != nil {
		commandFatalf("SLICE_PUBLISH_FAILED", false, "gs slice diff", "Cannot publish slice: %v", err)
	}
	if len(modifiedFiles) == 0 && resolvedChangesetID == "" {
		commandFatal("NO_LOCAL_CHANGES", "No modified files specified and working tree is clean", false, "Edit files or pass --files explicitly")
	}

	reusedExisting := false
	var changesetOutput jsonChangesetCreateOutput
	var reviewResp *slicev1.ReviewChangesetResponse
	targetChangesetID := resolvedChangesetID

	if len(modifiedFiles) == 0 {
		reusedExisting = true
		reviewResp, err = cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
			ChangesetId: targetChangesetID,
		})
		if err != nil {
			commandFatalf("CHANGESET_REVIEW_FAILED", true, "", "Failed to review changeset: %v", err)
		}
		changesetOutput = buildChangesetOutputFromInfo(reviewResp.GetChangeset())
	} else {
		fileContents, contentErr := buildLocalChangesetFileContents(".", modifiedFiles)
		if contentErr != nil {
			commandFatalf("CHANGESET_CREATE_FAILED", false, "gs slice diff", "Cannot read local changes: %v", contentErr)
		}
		baseCommitHash := strings.TrimSpace(*base)
		if baseCommitHash == "" && !isUpdate {
			baseCommitHash, err = resolveCheckoutBaseCommit(".", "")
			if err != nil {
				commandFatalf("CHANGESET_CREATE_FAILED", false, "gs slice status", "Cannot resolve checkout base commit: %v", err)
			}
		}
		createResp, createErr := cli.sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
			SliceId:        sliceID,
			BaseCommitHash: baseCommitHash,
			ModifiedFiles:  modifiedFiles,
			Author:         strings.TrimSpace(*author),
			Message:        strings.TrimSpace(*message),
			ChangesetId:    resolvedChangesetID,
			FileContents:   fileContents,
		})
		if createErr != nil {
			commandFatalf("CHANGESET_CREATE_FAILED", true, "", "Failed to create changeset: %v", createErr)
		}
		targetChangesetID = createResp.GetChangesetId()
		if err := writeTrackedChangesetIDConfig(targetChangesetID); err != nil {
			log.Printf("Warning: failed to track changeset ID locally: %v", err)
		}
		reviewResp, err = cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
			ChangesetId: targetChangesetID,
		})
		if err != nil {
			commandFatalf("CHANGESET_REVIEW_FAILED", true, "", "Failed to review changeset: %v", err)
		}
		changesetOutput = buildChangesetCreateOutput(createResp, isUpdate, sliceID, modifiedFiles)
	}

	if !shouldMerge {
		if jsonEnabled {
			writeJSONOutput(jsonSlicePublishOutput{
				Changeset:      changesetOutput,
				Review:         buildChangesetReviewOutput(reviewResp, false),
				ReviewOnly:     true,
				ReusedExisting: reusedExisting,
			})
			return
		}
		if reusedExisting {
			fmt.Printf("Reusing tracked changeset %s (hash: %s)\n", changesetOutput.ChangesetID, changesetOutput.ChangesetHash)
		} else if isUpdate {
			fmt.Printf("Updated changeset %s (hash: %s)\n", changesetOutput.ChangesetID, changesetOutput.ChangesetHash)
		} else {
			fmt.Printf("Created changeset %s (hash: %s)\n", changesetOutput.ChangesetID, changesetOutput.ChangesetHash)
		}
		fmt.Printf("Status: %s\n", changesetOutput.Status)
		printChangesetReview(reviewResp, false)
		fmt.Println("Merge explicitly with: gs changeset merge")
		return
	}

	mergeResp, err := cli.sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{
		ChangesetId: targetChangesetID,
	})
	if err != nil {
		commandFatalf("CHANGESET_MERGE_FAILED", true, "gs slice sync", "Failed to merge changeset: %v", err)
	}
	if mergeResp.GetStatus() == slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		if err := clearTrackedChangesetIDIfMatches(targetChangesetID); err != nil {
			log.Printf("Warning: failed to clear tracked changeset ID: %v", err)
		}
	}

	if jsonEnabled {
		writeJSONOutput(jsonSlicePublishOutput{
			Changeset:      changesetOutput,
			Review:         buildChangesetReviewOutput(reviewResp, false),
			ReviewOnly:     false,
			ReusedExisting: reusedExisting,
			Merge:          buildMergeOutput(mergeResp),
		})
		return
	}

	if reusedExisting {
		fmt.Printf("Reusing tracked changeset %s (hash: %s)\n", changesetOutput.ChangesetID, changesetOutput.ChangesetHash)
	} else if isUpdate {
		fmt.Printf("Updated changeset %s (hash: %s)\n", changesetOutput.ChangesetID, changesetOutput.ChangesetHash)
	} else {
		fmt.Printf("Created changeset %s (hash: %s)\n", changesetOutput.ChangesetID, changesetOutput.ChangesetHash)
	}
	fmt.Printf("Status: %s\n", changesetOutput.Status)
	printChangesetReview(reviewResp, false)
	printMergeResult(mergeResp)
}

func handleSliceTree(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice tree")
	sliceFlag := fs.String("slice", "", "Slice ID or slug (defaults to the current checkout)")
	commitFlag := fs.String("commit", "", "Commit hash")
	depth := fs.Int("depth", 0, "Maximum recursion depth (0 = unlimited)")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() > 1 {
		commandUsage("Usage: gs slice tree [path] [--slice <slice-id-or-ref>] [--commit <hash>] [--depth <n>] [--json]")
		return
	}

	rootPath := ""
	if fs.NArg() == 1 {
		rootPath = strings.TrimSpace(fs.Arg(0))
	}

	sliceID, err := resolveSliceRefOrCurrent(ctx, cli, *sliceFlag)
	if err != nil {
		commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Failed to resolve slice: %v", err)
	}
	commitHash := strings.TrimSpace(*commitFlag)

	if jsonEnabled {
		nodes, err := buildSliceTreeNodes(ctx, cli, sliceID, commitHash, rootPath, 1, *depth)
		if err != nil {
			commandFatalf("SLICE_TREE_FAILED", true, "", "Failed to build slice tree: %v", err)
		}
		displayPath := "/"
		if rootPath != "" {
			displayPath = rootPath
		}
		writeJSONOutput(jsonSliceTreeOutput{
			SliceID: sliceID,
			Commit:  commitHash,
			Path:    displayPath,
			Nodes:   nodes,
		})
		return
	}

	fmt.Printf("Slice: %s\n", sliceID)
	if rootPath == "" {
		fmt.Println("Path: /")
	} else {
		fmt.Printf("Path: %s\n", rootPath)
	}

	if err := printSliceTree(ctx, cli, sliceID, commitHash, rootPath, "", 1, *depth); err != nil {
		commandFatalf("SLICE_TREE_FAILED", true, "", "Failed to print slice tree: %v", err)
	}
}

func handleSliceDelete(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice delete")
	force := fs.Bool("force", false, "Delete even if the slice still has open changesets")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs slice delete <slice-id-or-ref> [--force] [--json]")
		return
	}

	sliceID, err := resolveSliceRef(ctx, cli, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Invalid slice reference: %v", err)
	}

	resp, err := cli.sliceClient.DeleteSlice(ctx, &slicev1.DeleteSliceRequest{
		SliceId: sliceID,
		Force:   *force,
	})
	if err != nil {
		commandFatalf("SLICE_DELETE_FAILED", true, "", "Failed to delete slice: %v", err)
	}

	removed, err := removeCheckoutRecordsForSlice(sliceID)
	if err != nil {
		log.Printf("Warning: failed to remove checkout registry entries: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(jsonSliceDeleteOutput{
			SliceID:                resp.GetSliceId(),
			Slug:                   resp.GetSlug(),
			Status:                 resp.GetStatus(),
			RemovedCheckoutRecords: removed,
		})
		return
	}

	fmt.Printf("Deleted slice: %s\n", resp.GetSliceId())
	if resp.GetSlug() != "" {
		fmt.Printf("Slug: %s\n", resp.GetSlug())
	}
	fmt.Printf("Status: %s\n", resp.GetStatus())
	if removed > 0 {
		fmt.Printf("Removed checkout records: %d\n", removed)
	}
}

func resolveSliceRefOrCurrent(ctx context.Context, cli *CLI, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit == "" {
		return sliceIDFromConfig()
	}
	return resolveSliceRef(ctx, cli, explicit)
}

func printSliceTree(
	ctx context.Context,
	cli *CLI,
	sliceID, commitHash, currentPath, prefix string,
	level, maxDepth int,
) error {
	req := &filev1.ListEntriesRequest{Path: currentPath, Limit: 10000}
	applyListEntriesVersion(req, sliceID, commitHash)
	resp, err := cli.fileClient.ListEntries(ctx, req)
	if err != nil {
		return err
	}

	entries := append([]*filev1.DirectoryEntry(nil), resp.GetEntries()...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].GetType() != entries[j].GetType() {
			return entries[i].GetType() == filev1.EntryType_ENTRY_TYPE_DIRECTORY
		}
		return entries[i].GetName() < entries[j].GetName()
	})

	for idx, entry := range entries {
		last := idx == len(entries)-1
		connector := "|- "
		childPrefix := prefix + "|  "
		if last {
			connector = "`- "
			childPrefix = prefix + "   "
		}

		name := entry.GetName()
		if entry.GetType() == filev1.EntryType_ENTRY_TYPE_DIRECTORY {
			fmt.Printf("%s%s%s/\n", prefix, connector, name)
			if maxDepth == 0 || level < maxDepth {
				if err := printSliceTree(ctx, cli, sliceID, commitHash, entry.GetPath(), childPrefix, level+1, maxDepth); err != nil {
					return err
				}
			}
			continue
		}
		fmt.Printf("%s%s%s (%d bytes)\n", prefix, connector, name, entry.GetSize())
	}

	return nil
}

func buildSliceTreeNodes(
	ctx context.Context,
	cli *CLI,
	sliceID, commitHash, currentPath string,
	level, maxDepth int,
) ([]jsonSliceTreeNode, error) {
	req := &filev1.ListEntriesRequest{Path: currentPath, Limit: 10000}
	applyListEntriesVersion(req, sliceID, commitHash)
	resp, err := cli.fileClient.ListEntries(ctx, req)
	if err != nil {
		return nil, err
	}

	entries := append([]*filev1.DirectoryEntry(nil), resp.GetEntries()...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].GetType() != entries[j].GetType() {
			return entries[i].GetType() == filev1.EntryType_ENTRY_TYPE_DIRECTORY
		}
		return entries[i].GetName() < entries[j].GetName()
	})

	nodes := make([]jsonSliceTreeNode, 0, len(entries))
	for _, entry := range entries {
		node := jsonSliceTreeNode{
			Name: entry.GetName(),
			Path: entry.GetPath(),
			Type: strings.ToLower(strings.TrimPrefix(entry.GetType().String(), "ENTRY_TYPE_")),
			Size: entry.GetSize(),
		}
		if entry.GetType() == filev1.EntryType_ENTRY_TYPE_DIRECTORY && (maxDepth == 0 || level < maxDepth) {
			children, err := buildSliceTreeNodes(ctx, cli, sliceID, commitHash, entry.GetPath(), level+1, maxDepth)
			if err != nil {
				return nil, err
			}
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}
