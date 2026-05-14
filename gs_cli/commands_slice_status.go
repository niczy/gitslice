package gscli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

const defaultSliceStatusPathLimit = 20

type workingTreeStatusEntry struct {
	Path   string
	Status string
}

func handleSliceStatus(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice status")
	all := fs.Bool("all", false, "Show all changed paths")
	limit := fs.Int("limit", defaultSliceStatusPathLimit, "Maximum changed paths to print")
	remote := fs.Bool("remote", false, "Fetch remote head and tracked changeset status")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs slice status [--all] [--limit <n>] [--remote] [--json]")
		return
	}
	if *limit < 0 {
		commandFatal("INVALID_ARGUMENT", "Limit must be >= 0", false, "")
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read current slice binding: %v", err)
		return
	}

	checkoutIndex, err := detectCheckoutMode(".")
	if err != nil {
		commandFatalf("CHECKOUT_METADATA_MISSING", false, "gs slice checkout <slice-id>", "Failed to read checkout mode: %v", err)
	}

	statusEntries, err := collectNoGitWorkingTreeStatus(".", checkoutIndex)
	if err != nil {
		commandFatalf("STATUS_FAILED", false, "", "Failed to inspect working tree: %v", err)
	}
	statusEntries = filterWorkingTreeStatusEntries(statusEntries)

	trackedChangesetID, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		commandFatalf("STATUS_FAILED", false, "", "Failed to read tracked changeset ID: %v", err)
	}
	trackedChangesetID = strings.TrimSpace(trackedChangesetID)
	trackedChangesetStatus := ""
	if *remote && trackedChangesetID != "" {
		reviewResp, reviewErr := cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
			ChangesetId: trackedChangesetID,
		})
		if reviewErr != nil {
			trackedChangesetStatus = fmt.Sprintf("error (%v)", reviewErr)
		} else {
			trackedChangesetStatus = reviewResp.GetReviewStatus().String()
		}
	}

	localCommitHash := ""
	if checkoutIndex != nil {
		localCommitHash = strings.TrimSpace(checkoutIndex.CommitHash)
	}
	remoteHead := ""
	behindRemote := false
	if *remote {
		stateResp, err := cli.sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sliceID})
		if err != nil {
			commandFatalf("STATUS_REMOTE_FAILED", true, "gs slice status", "Failed to get slice state: %v", err)
		}
		remoteHead = strings.TrimSpace(stateResp.GetLatestCommitHash())
		behindRemote = localCommitHash != "" && remoteHead != "" && localCommitHash != remoteHead
	}

	added, modified, deleted := summarizeWorkingTreeStatus(statusEntries)
	workingTreeState := "clean"
	if len(statusEntries) > 0 {
		workingTreeState = "dirty"
	}

	displayLimit := *limit
	if *all {
		displayLimit = 0
	}
	printed := statusEntries
	truncated := false
	if displayLimit > 0 && len(statusEntries) > displayLimit {
		printed = statusEntries[:displayLimit]
		truncated = true
	}

	if jsonEnabled {
		out := jsonSliceStatusOutput{
			SliceID:                sliceID,
			Mode:                   "no-git",
			CheckoutBase:           localCommitHash,
			RemoteQueried:          *remote,
			RemoteHead:             remoteHead,
			TrackedChangesetID:     trackedChangesetID,
			TrackedChangesetStatus: trackedChangesetStatus,
			WorkingTree:            workingTreeState,
			Changes: jsonWorkingTreeSummary{
				Added:    added,
				Modified: modified,
				Deleted:  deleted,
			},
			PathCount: len(statusEntries),
			Truncated: truncated,
			Paths:     make([]jsonWorkingTreePath, 0, len(printed)),
		}
		switch {
		case !*remote:
			out.SyncStatus = "skipped"
			if trackedChangesetID != "" {
				out.TrackedChangesetStatus = "skipped"
			}
		case localCommitHash == "":
			out.SyncStatus = "unknown"
		case behindRemote:
			out.SyncStatus = "behind_remote_head"
		default:
			out.SyncStatus = "current"
		}
		for _, entry := range printed {
			out.Paths = append(out.Paths, jsonWorkingTreePath{
				Path:   entry.Path,
				Status: entry.Status,
			})
		}
		writeJSONOutput(out)
		return
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Println("Mode: no-git")
	if localCommitHash != "" {
		fmt.Printf("Checkout base: %s\n", localCommitHash)
	} else {
		fmt.Println("Checkout base: unknown")
	}
	if *remote {
		fmt.Printf("Remote head: %s\n", remoteHead)
		switch {
		case localCommitHash == "":
			fmt.Println("Sync: unknown (no checkout base recorded)")
		case behindRemote:
			fmt.Println("Sync: behind remote head")
		default:
			fmt.Println("Sync: current with remote head")
		}
	} else {
		fmt.Println("Remote head: skipped (use --remote)")
		fmt.Println("Sync: skipped (use --remote)")
	}
	if trackedChangesetID == "" {
		fmt.Println("Tracked changeset: none")
	} else {
		fmt.Printf("Tracked changeset: %s\n", trackedChangesetID)
		if *remote {
			fmt.Printf("Tracked changeset status: %s\n", trackedChangesetStatus)
		} else {
			fmt.Println("Tracked changeset status: skipped (use --remote)")
		}
	}
	fmt.Printf("Working tree: %s\n", workingTreeState)
	fmt.Printf("Changes: +%d ~%d -%d\n", added, modified, deleted)

	if len(statusEntries) == 0 {
		return
	}

	fmt.Println("Paths:")
	for _, entry := range printed {
		fmt.Printf("  %s %s\n", entry.Status, entry.Path)
	}
	if len(printed) < len(statusEntries) {
		fmt.Printf("  ... and %d more\n", len(statusEntries)-len(printed))
	}
}

func collectNoGitWorkingTreeStatus(dir string, index *localCheckoutIndex) ([]workingTreeStatusEntry, error) {
	if index == nil {
		return nil, fmt.Errorf("checkout metadata missing; re-checkout the slice")
	}

	lookup := newCheckoutIndexLookup(index)
	if candidates, ok, err := collectDirtyTrackerCandidates(dir, index); err == nil && ok {
		entries, remaining, err := collectNoGitWorkingTreeStatusFromCandidates(dir, lookup, candidates)
		if err == nil {
			entries = filterAddedWorkingTreeStatusEntriesForCheckout(entries, index)
			remaining = collectWorkingTreeStatusPaths(entries)
			_ = writeDirtyTrackerPaths(dir, remaining)
			sortWorkingTreeStatus(entries)
			return entries, nil
		}
	}

	entries, err := collectNoGitTrackedStatus(dir, lookup)
	if err != nil {
		return nil, err
	}

	newFiles, err := scanCheckoutForNewFiles(dir, "", lookup)
	if err != nil {
		return nil, err
	}
	for _, path := range newFiles {
		entries = append(entries, workingTreeStatusEntry{Path: path, Status: "A"})
	}

	entries = filterAddedWorkingTreeStatusEntriesForCheckout(entries, index)
	_ = writeDirtyTrackerPaths(dir, collectWorkingTreeStatusPaths(entries))
	sortWorkingTreeStatus(entries)
	return entries, nil
}

func collectNoGitWorkingTreeStatusFullScan(dir string, index *localCheckoutIndex) ([]workingTreeStatusEntry, error) {
	if index == nil {
		return nil, fmt.Errorf("checkout metadata missing; re-checkout the slice")
	}

	lookup := newCheckoutIndexLookup(index)
	entries, err := collectNoGitTrackedStatus(dir, lookup)
	if err != nil {
		return nil, err
	}

	newFiles, err := scanCheckoutForNewFiles(dir, "", lookup)
	if err != nil {
		return nil, err
	}
	for _, path := range newFiles {
		entries = append(entries, workingTreeStatusEntry{Path: path, Status: "A"})
	}

	entries = filterAddedWorkingTreeStatusEntriesForCheckout(entries, index)
	_ = writeDirtyTrackerPaths(dir, collectWorkingTreeStatusPaths(entries))
	sortWorkingTreeStatus(entries)
	return entries, nil
}

func sortWorkingTreeStatus(entries []workingTreeStatusEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Path == entries[j].Path {
			return entries[i].Status < entries[j].Status
		}
		return entries[i].Path < entries[j].Path
	})
}

func summarizeWorkingTreeStatus(entries []workingTreeStatusEntry) (added, modified, deleted int) {
	for _, entry := range entries {
		switch entry.Status {
		case "A":
			added++
		case "D":
			deleted++
		default:
			modified++
		}
	}
	return added, modified, deleted
}

func filterWorkingTreeStatusEntries(entries []workingTreeStatusEntry) []workingTreeStatusEntry {
	if len(entries) == 0 {
		return nil
	}
	filtered := make([]workingTreeStatusEntry, 0, len(entries))
	for _, entry := range entries {
		cleaned := filepath.Clean(strings.TrimSpace(entry.Path))
		if cleaned == ".gs" || strings.HasPrefix(cleaned, ".gs"+string(os.PathSeparator)) {
			continue
		}
		filtered = append(filtered, workingTreeStatusEntry{
			Path:   cleaned,
			Status: entry.Status,
		})
	}
	return filtered
}

func collectWorkingTreeStatusPaths(entries []workingTreeStatusEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		cleaned := filepath.Clean(strings.TrimSpace(entry.Path))
		if cleaned == "." || cleaned == "" {
			continue
		}
		paths = append(paths, cleaned)
	}
	sort.Strings(paths)
	return uniqueCheckoutPaths(paths)
}
