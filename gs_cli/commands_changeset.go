package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleChangesetCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printChangesetHelp()
		return
	}

	switch args[0] {
	case "create":
		handleChangesetCreate(ctx, cli, args[1:])
	case "review":
		handleChangesetReview(ctx, cli, args[1:])
	case "merge":
		handleChangesetMerge(ctx, cli, args[1:])
	case "rebase":
		handleChangesetRebase(ctx, cli, args[1:])
	case "list":
		handleChangesetList(ctx, cli, args[1:])
	default:
		log.Printf("Unknown changeset command: %s", args[0])
		printChangesetHelp()
	}
}

func handleChangesetCreate(ctx context.Context, cli *CLI, args []string) {
	if err := requireMainBranch("."); err != nil {
		log.Fatalf("Cannot create changeset: %v", err)
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		log.Printf("Failed to read slice binding: %v", err)
		return
	}

	fs := flag.NewFlagSet("changeset create", flag.ExitOnError)
	message := fs.String("message", "", "Changeset message")
	base := fs.String("base", "", "Base commit hash")
	files := fs.String("files", "", "Comma-separated file list")
	author := fs.String("author", "user", "Author of the changeset")
	changesetID := fs.String("changeset-id", "", "Existing changeset ID to append a new snapshot")
	fs.Parse(args)

	modifiedFiles := []string{}
	if *files != "" {
		modifiedFiles = splitAndTrim(*files, ",")
	}
	modifiedFiles = append(modifiedFiles, fs.Args()...)

	req := &slicev1.CreateChangesetRequest{
		SliceId:        sliceID,
		BaseCommitHash: *base,
		ModifiedFiles:  modifiedFiles,
		Author:         *author,
		Message:        *message,
		ChangesetId:    strings.TrimSpace(*changesetID),
	}

	resp, err := cli.sliceClient.CreateChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create changeset: %v", err)
	}

	if strings.TrimSpace(*changesetID) != "" {
		fmt.Printf("Updated changeset %s (hash: %s)\n", resp.ChangesetId, resp.ChangesetHash)
	} else {
		fmt.Printf("Created changeset %s (hash: %s)\n", resp.ChangesetId, resp.ChangesetHash)
	}
	fmt.Printf("Status: %s\n", resp.Status.String())
}

func handleChangesetReview(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs changeset review <changeset-id>")
		return
	}

	req := &slicev1.ReviewChangesetRequest{ChangesetId: args[0]}
	resp, err := cli.sliceClient.ReviewChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to review changeset: %v", err)
	}

	fmt.Printf("Changeset: %s\n", resp.Changeset.GetChangesetId())
	fmt.Printf("Status: %s\n", resp.ReviewStatus.String())
	if resp.Diff != nil {
		fmt.Printf("Files changed: %d\n", resp.Diff.FilesAdded+resp.Diff.FilesModified+resp.Diff.FilesDeleted)
	}
}

func handleChangesetMerge(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs changeset merge <changeset-id>")
		return
	}

	req := &slicev1.MergeChangesetRequest{ChangesetId: args[0]}
	resp, err := cli.sliceClient.MergeChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to merge changeset: %v", err)
	}

	fmt.Printf("Merge status: %s\n", resp.Status.String())
	fmt.Printf("New commit: %s\n", resp.NewCommitHash)

	if resp.Status == slicev1.MergeStatus_MERGE_STATUS_CONFLICT {
		fmt.Println("Conflicts detected:")
		for _, conflict := range resp.Conflicts {
			fmt.Printf("- %s (slices: %s)\n", conflict.FileId, strings.Join(conflict.ConflictingSliceIds, ", "))
		}
	}
}

func handleChangesetRebase(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs changeset rebase <changeset-id>")
		return
	}

	req := &slicev1.RebaseChangesetRequest{ChangesetId: args[0]}
	resp, err := cli.sliceClient.RebaseChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to rebase changeset: %v", err)
	}

	fmt.Printf("Rebase status: %s\n", resp.Status.String())
	fmt.Printf("New base commit: %s\n", resp.NewBaseCommitHash)
}

func handleChangesetList(ctx context.Context, cli *CLI, args []string) {
	sliceID, err := sliceIDFromConfig()
	if err != nil {
		log.Printf("Failed to read slice binding: %v", err)
		return
	}

	fs := flag.NewFlagSet("changeset list", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Maximum results")
	status := &stringFlag{}
	fs.Var(status, "status", "Filter by status (pending, approved, rejected, merged)")
	fs.Parse(args)

	statusFilter := slicev1.ChangesetStatus(-1)
	if status.set {
		switch strings.ToLower(status.value) {
		case "approved":
			statusFilter = slicev1.ChangesetStatus_APPROVED
		case "rejected":
			statusFilter = slicev1.ChangesetStatus_REJECTED
		case "merged":
			statusFilter = slicev1.ChangesetStatus_MERGED
		case "pending":
			statusFilter = slicev1.ChangesetStatus_PENDING
		default:
			log.Printf("Unknown status filter: %s", status.value)
			return
		}
	}

	req := &slicev1.ListChangesetsRequest{
		SliceId:      sliceID,
		StatusFilter: statusFilter,
		Limit:        int32(*limit),
	}

	resp, err := cli.sliceClient.ListChangesets(ctx, req)
	if err != nil {
		log.Fatalf("Failed to list changesets: %v", err)
	}

	sort.Slice(resp.Changesets, func(i, j int) bool {
		return resp.Changesets[i].CreatedAt > resp.Changesets[j].CreatedAt
	})

	fmt.Printf("Found %d changeset(s) for slice %s\n", len(resp.Changesets), sliceID)
	for _, cs := range resp.Changesets {
		fmt.Printf("- %s [%s] %s\n", cs.ChangesetId, cs.Status.String(), cs.Message)
	}
}
