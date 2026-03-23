package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
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
	case "show":
		handleChangesetShow(ctx, cli, args[1:])
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

func handleChangesetShow(ctx context.Context, cli *CLI, args []string) {
	fs := flag.NewFlagSet("changeset show", flag.ExitOnError)
	snapshotVersion := fs.Int("snapshot", 0, "Show a specific snapshot version")
	includePatches := fs.Bool("patches", false, "Include inline patch text when available")
	fs.Parse(args)

	if fs.NArg() > 1 {
		log.Println("Usage: gs changeset show [<changeset-id>] [--snapshot <version>] [--patches]")
		return
	}

	changesetID, err := resolveChangesetIDForRead("")
	if fs.NArg() == 1 {
		changesetID, err = resolveChangesetIDForRead(fs.Arg(0))
	}
	if err != nil {
		log.Printf("Failed to resolve changeset ID: %v", err)
		return
	}

	resp, err := cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
		ChangesetId:     changesetID,
		SnapshotVersion: int32(*snapshotVersion),
	})
	if err != nil {
		log.Fatalf("Failed to show changeset: %v", err)
	}

	printChangesetReview(resp, *includePatches)
}

func handleChangesetCreate(ctx context.Context, cli *CLI, args []string) {
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
	modifiedFiles, _, err = resolveWorkingTreeModifiedFiles(".", modifiedFiles)
	if err != nil {
		log.Fatalf("Cannot create changeset: %v", err)
	}
	if len(modifiedFiles) == 0 {
		log.Fatal("No modified files specified and working tree is clean")
	}

	resolvedChangesetID, isUpdate, err := resolveChangesetIDForCreate(*changesetID)
	if err != nil {
		log.Printf("Failed to resolve tracked changeset ID: %v", err)
		return
	}

	req := &slicev1.CreateChangesetRequest{
		SliceId:        sliceID,
		BaseCommitHash: *base,
		ModifiedFiles:  modifiedFiles,
		Author:         *author,
		Message:        *message,
		ChangesetId:    resolvedChangesetID,
	}

	resp, err := cli.sliceClient.CreateChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create changeset: %v", err)
	}
	if err := writeTrackedChangesetIDConfig(resp.GetChangesetId()); err != nil {
		log.Printf("Warning: failed to track changeset ID locally: %v", err)
	}

	if isUpdate {
		fmt.Printf("Updated changeset %s (hash: %s)\n", resp.ChangesetId, resp.ChangesetHash)
	} else {
		fmt.Printf("Created changeset %s (hash: %s)\n", resp.ChangesetId, resp.ChangesetHash)
	}
	fmt.Printf("Status: %s\n", resp.Status.String())
}

func resolveWorkingTreeModifiedFiles(dir string, explicit []string) ([]string, bool, error) {
	modifiedFiles := normalizeLocalModifiedFiles(explicit)
	checkoutIndex, err := detectCheckoutMode(dir)
	if err != nil {
		return nil, false, err
	}

	if len(modifiedFiles) == 0 {
		modifiedFiles, err = detectNoGitModifiedFiles(dir, checkoutIndex)
		if err != nil {
			return nil, false, err
		}
	}
	return modifiedFiles, false, nil
}

func normalizeLocalModifiedFiles(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	out := make([]string, 0, len(files))
	for _, raw := range files {
		cleaned := filepath.Clean(strings.TrimSpace(raw))
		if cleaned == "" || cleaned == "." {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	sort.Strings(out)
	return out
}

func resolveChangesetIDForCreate(explicit string) (string, bool, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, true, nil
	}
	tracked, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		return "", false, err
	}
	tracked = strings.TrimSpace(tracked)
	if tracked != "" {
		return tracked, true, nil
	}
	return "", false, nil
}

func handleChangesetReview(ctx context.Context, cli *CLI, args []string) {
	changesetID, err := resolveChangesetIDForRead("")
	switch len(args) {
	case 0:
	case 1:
		changesetID, err = resolveChangesetIDForRead(args[0])
	default:
		log.Println("Usage: gs changeset review [<changeset-id>]")
		return
	}
	if err != nil {
		log.Printf("Failed to resolve changeset ID: %v", err)
		return
	}

	resp, err := cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: changesetID})
	if err != nil {
		log.Fatalf("Failed to review changeset: %v", err)
	}

	printChangesetReview(resp, false)
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
	if resp.GetStatus() == slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		if err := clearTrackedChangesetIDIfMatches(req.ChangesetId); err != nil {
			log.Printf("Warning: failed to clear tracked changeset ID: %v", err)
		}
	}

	printMergeResult(resp)
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

func resolveChangesetIDForRead(explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, nil
	}
	tracked, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		return "", err
	}
	tracked = strings.TrimSpace(tracked)
	if tracked == "" {
		return "", fmt.Errorf("no tracked changeset; pass an explicit changeset ID")
	}
	return tracked, nil
}

func printMergeConflicts(conflicts []*slicev1.Conflict) {
	if len(conflicts) == 0 {
		return
	}
	fmt.Println("Conflicts detected:")
	for _, conflict := range conflicts {
		typeLabel := strings.TrimPrefix(conflict.GetType().String(), "CONFLICT_TYPE_")
		if typeLabel == "UNSPECIFIED" {
			typeLabel = ""
		}
		if typeLabel != "" {
			fmt.Printf("- %s [%s] (slices: %s)\n", conflict.GetFileId(), typeLabel, strings.Join(conflict.GetConflictingSliceIds(), ", "))
		} else {
			fmt.Printf("- %s (slices: %s)\n", conflict.GetFileId(), strings.Join(conflict.GetConflictingSliceIds(), ", "))
		}
		if message := strings.TrimSpace(conflict.GetMessage()); message != "" {
			fmt.Printf("  %s\n", message)
		}
	}
}

func printSliceConflictGuidance() {
	fmt.Println("Hint: sync to the latest slice head, review your local changes, then publish again.")
	fmt.Println("      Suggested flow: gs slice sync && gs slice diff && gs slice publish")
}

func printMergeResult(resp *slicev1.MergeChangesetResponse) {
	if resp == nil {
		fmt.Println("Merge status: unknown")
		return
	}

	fmt.Printf("Merge status: %s\n", resp.GetStatus().String())
	if resp.GetNewCommitHash() != "" {
		fmt.Printf("New commit: %s\n", resp.GetNewCommitHash())
	}
	if message := strings.TrimSpace(resp.GetMessage()); message != "" {
		fmt.Printf("Message: %s\n", message)
	}

	switch resp.GetStatus() {
	case slicev1.MergeStatus_MERGE_STATUS_CONFLICT:
		printMergeConflicts(resp.GetConflicts())
		printSliceConflictGuidance()
	case slicev1.MergeStatus_MERGE_STATUS_STALE_BASE:
		fmt.Println("Hint: rebase the changeset onto the latest slice head, then merge again.")
		fmt.Printf("      Suggested flow: gs changeset rebase %s && gs changeset merge %s\n", resp.GetChangesetId(), resp.GetChangesetId())
	case slicev1.MergeStatus_MERGE_STATUS_LOCKED:
		fmt.Println("Hint: another merge is already operating on this slice or file set. Retry shortly.")
	}
}

func printChangesetReview(resp *slicev1.ReviewChangesetResponse, includePatches bool) {
	if resp == nil {
		fmt.Println("No changeset review response")
		return
	}

	changeset := resp.GetChangeset()
	changesetID := ""
	if changeset != nil {
		changesetID = changeset.GetChangesetId()
	}
	fmt.Printf("Changeset: %s\n", changesetID)
	fmt.Printf("Status: %s\n", resp.GetReviewStatus().String())
	if changeset != nil {
		fmt.Printf("Slice: %s\n", changeset.GetSliceId())
		if changeset.GetMessage() != "" {
			fmt.Printf("Message: %s\n", changeset.GetMessage())
		}
	}
	if snapshot := resp.GetSnapshot(); snapshot != nil {
		fmt.Printf("Snapshot: v%d %s\n", snapshot.GetVersion(), snapshot.GetHash())
	}
	if diff := resp.GetDiff(); diff != nil {
		fmt.Printf(
			"Files: +%d ~%d -%d\n",
			diff.GetFilesAdded(),
			diff.GetFilesModified(),
			diff.GetFilesDeleted(),
		)
		fmt.Printf("Lines: +%d -%d\n", diff.GetLinesAdded(), diff.GetLinesRemoved())
	}
	if warnings := resp.GetWarnings(); len(warnings) > 0 {
		fmt.Println("Warnings:")
		for _, warning := range warnings {
			fmt.Printf("- %s\n", warning)
		}
	}
	if issues := resp.GetIssues(); len(issues) > 0 {
		fmt.Println("Issues:")
		for _, issue := range issues {
			label := strings.TrimPrefix(issue.GetType().String(), "REVIEW_ISSUE_TYPE_")
			switch {
			case issue.GetFileId() != "" && len(issue.GetConflictingSliceIds()) > 0:
				fmt.Printf("- [%s] %s (slices: %s)\n", label, issue.GetFileId(), strings.Join(issue.GetConflictingSliceIds(), ", "))
			case issue.GetFileId() != "":
				fmt.Printf("- [%s] %s\n", label, issue.GetFileId())
			default:
				fmt.Printf("- [%s]\n", label)
			}
			if message := strings.TrimSpace(issue.GetMessage()); message != "" {
				fmt.Printf("  %s\n", message)
			}
		}
	}
	if len(resp.GetChanges()) == 0 {
		return
	}

	fmt.Println("Changes:")
	for _, change := range resp.GetChanges() {
		path := change.GetPath()
		if change.GetOldPath() != "" && change.GetOldPath() != change.GetPath() {
			path = fmt.Sprintf("%s -> %s", change.GetOldPath(), change.GetPath())
		}
		fmt.Printf(
			"- [%s] %s (+%d -%d)\n",
			change.GetChangeType().String(),
			path,
			change.GetLinesAdded(),
			change.GetLinesDeleted(),
		)
		if includePatches && strings.TrimSpace(change.GetPatch()) != "" {
			fmt.Println(change.GetPatch())
		}
	}
}
