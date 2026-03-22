package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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
	fs := flag.NewFlagSet("slice status", flag.ExitOnError)
	all := fs.Bool("all", false, "Show all changed paths")
	limit := fs.Int("limit", defaultSliceStatusPathLimit, "Maximum changed paths to print")
	remote := fs.Bool("remote", false, "Fetch remote head and tracked changeset status")
	fs.Parse(args)
	if fs.NArg() != 0 {
		log.Println("Usage: gs slice status [--all] [--limit <n>] [--remote]")
		return
	}
	if *limit < 0 {
		log.Fatal("Limit must be >= 0")
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		log.Printf("Failed to read current slice binding: %v", err)
		return
	}

	checkoutIndex, gitEnabled, err := detectCheckoutMode(".")
	if err != nil {
		log.Fatalf("Failed to read checkout mode: %v", err)
	}

	statusEntries, err := collectWorkingTreeStatus(".", checkoutIndex, gitEnabled)
	if err != nil {
		log.Fatalf("Failed to inspect working tree: %v", err)
	}
	statusEntries = filterWorkingTreeStatusEntries(statusEntries)

	branch := ""
	if gitEnabled {
		branch, err = gitCurrentBranch(".")
		if err != nil {
			log.Fatalf("Failed to read git branch: %v", err)
		}
	}

	trackedChangesetID, err := readTrackedChangesetIDFromConfig()
	if err != nil {
		log.Fatalf("Failed to read tracked changeset ID: %v", err)
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
			log.Fatalf("Failed to get slice state: %v", err)
		}
		remoteHead = strings.TrimSpace(stateResp.GetLatestCommitHash())
		behindRemote = localCommitHash != "" && remoteHead != "" && localCommitHash != remoteHead
	}

	added, modified, deleted := summarizeWorkingTreeStatus(statusEntries)
	mode := "no-git"
	if gitEnabled {
		mode = "git"
	}
	workingTreeState := "clean"
	if len(statusEntries) > 0 {
		workingTreeState = "dirty"
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Printf("Mode: %s\n", mode)
	if branch != "" {
		fmt.Printf("Branch: %s\n", branch)
	}
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

	displayLimit := *limit
	if *all {
		displayLimit = 0
	}
	printed := statusEntries
	if displayLimit > 0 && len(statusEntries) > displayLimit {
		printed = statusEntries[:displayLimit]
	}

	fmt.Println("Paths:")
	for _, entry := range printed {
		fmt.Printf("  %s %s\n", entry.Status, entry.Path)
	}
	if len(printed) < len(statusEntries) {
		fmt.Printf("  ... and %d more\n", len(statusEntries)-len(printed))
	}
}

func collectWorkingTreeStatus(dir string, checkoutIndex *localCheckoutIndex, gitEnabled bool) ([]workingTreeStatusEntry, error) {
	if gitEnabled {
		return collectGitWorkingTreeStatus(dir)
	}
	return collectNoGitWorkingTreeStatus(dir, checkoutIndex)
}

func collectGitWorkingTreeStatus(dir string) ([]workingTreeStatusEntry, error) {
	output, err := runGitCommand(dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseGitPorcelainStatus(output), nil
}

func parseGitPorcelainStatus(output string) []workingTreeStatusEntry {
	if strings.TrimSpace(output) == "" {
		return nil
	}

	seen := make(map[string]struct{})
	entries := make([]workingTreeStatusEntry, 0)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || len(line) < 4 {
			continue
		}

		code := line[:2]
		pathSpec := strings.TrimSpace(line[3:])
		if pathSpec == "" {
			continue
		}
		if idx := strings.LastIndex(pathSpec, " -> "); idx >= 0 {
			pathSpec = strings.TrimSpace(pathSpec[idx+4:])
		}

		path := filepath.Clean(pathSpec)
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}

		entries = append(entries, workingTreeStatusEntry{
			Path:   path,
			Status: gitPorcelainCodeToStatus(code),
		})
	}

	sortWorkingTreeStatus(entries)
	return entries
}

func gitPorcelainCodeToStatus(code string) string {
	code = strings.TrimSpace(code)
	switch {
	case code == "??":
		return "A"
	case strings.Contains(code, "D"):
		return "D"
	case strings.Contains(code, "A"):
		return "A"
	default:
		return "M"
	}
}

func collectNoGitWorkingTreeStatus(dir string, index *localCheckoutIndex) ([]workingTreeStatusEntry, error) {
	if index == nil {
		return nil, fmt.Errorf("checkout metadata missing; re-checkout the slice")
	}

	lookup := newCheckoutIndexLookup(index)

	entries := make([]workingTreeStatusEntry, 0)
	for path, original := range lookup.files {
		fullPath := filepath.Join(dir, path)
		info, err := os.Lstat(fullPath)
		if os.IsNotExist(err) {
			entries = append(entries, workingTreeStatusEntry{Path: path, Status: "D"})
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			entries = append(entries, workingTreeStatusEntry{Path: path, Status: "M"})
			continue
		}
		matches, err := checkoutTrackedFileMatches(fullPath, info, original)
		if err != nil {
			return nil, err
		}
		if !matches {
			entries = append(entries, workingTreeStatusEntry{Path: path, Status: "M"})
		}
	}

	newFiles, err := scanCheckoutForNewFiles(dir, "", lookup)
	if err != nil {
		return nil, err
	}
	for _, path := range newFiles {
		entries = append(entries, workingTreeStatusEntry{Path: path, Status: "A"})
	}

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
