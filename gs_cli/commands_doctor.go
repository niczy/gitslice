package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	adminv1 "github.com/niczy/gitslice/proto/admin"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleDoctor(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 0 {
		log.Println("Usage: gs doctor")
		return
	}

	fmt.Println("Auth:")
	fmt.Printf("  Source: %s\n", strings.TrimSpace(authConfig.Source))
	if strings.TrimSpace(authConfig.Username) != "" {
		fmt.Printf("  Username: %s\n", strings.TrimSpace(authConfig.Username))
	}
	fmt.Printf("  Stored credentials: %t\n", authConfig.CredentialStore)

	fmt.Println("Services:")
	if meResp, err := cli.adminClient.Me(ctx, &adminv1.MeRequest{}); err != nil {
		fmt.Printf("  Admin: error (%v)\n", err)
	} else {
		fmt.Printf("  Admin: ok as %s\n", meResp.GetUser().GetUsername())
	}
	if rootResp, err := cli.sliceClient.GetRootSlice(ctx, &slicev1.GetRootSliceRequest{}); err != nil {
		fmt.Printf("  Slice: error (%v)\n", err)
	} else {
		fmt.Printf("  Slice: ok root=%s head=%s\n", rootResp.GetSliceId(), rootResp.GetCommitHash())
	}
	if stateResp, err := cli.adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{}); err != nil {
		fmt.Printf("  Global state: error (%v)\n", err)
	} else {
		fmt.Printf("  Global state: ok head=%s\n", stateResp.GetGlobalCommitHash())
	}
	if homeSliceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig); err != nil {
		fmt.Printf("  Filesystem: error (%v)\n", err)
	} else {
		fmt.Printf("  Filesystem: ok home=%s\n", homeSliceID)
	}

	fmt.Println("Cache:")
	cache, err := NewCacheManager()
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
	} else if stats, err := cache.Stats(); err != nil {
		fmt.Printf("  Error: %v\n", err)
	} else {
		fmt.Printf("  Root: %s\n", cache.Root())
		fmt.Printf("  Objects: %d\n", stats.ObjectCount)
		fmt.Printf("  Bytes: %d\n", stats.TotalBytes)
	}

	records, err := listCheckoutRecords()
	if err != nil {
		fmt.Printf("  Checkout registry: error (%v)\n", err)
	} else {
		fmt.Printf("  Tracked checkouts: %d\n", len(records))
		fmt.Printf("  Unique slices: %d\n", countUniqueCheckoutSlices(records))
		fmt.Printf("  Stale records: %d\n", countStaleCheckoutRecords(records))
	}

	fmt.Println("Checkout:")
	sliceID, err := sliceIDFromConfig()
	if err != nil {
		fmt.Printf("  Current directory: not a gitslice checkout (%v)\n", err)
		return
	}
	fmt.Printf("  Slice: %s\n", sliceID)
	fmt.Println("  Mode: no-git")

	checkoutIndex, err := readCheckoutIndex(".")
	if err != nil {
		fmt.Printf("  Checkout metadata: error (%v)\n", err)
	} else if checkoutIndex == nil {
		fmt.Println("  Checkout metadata: missing")
	} else if strings.TrimSpace(checkoutIndex.CommitHash) != "" {
		fmt.Printf("  Checkout base: %s\n", strings.TrimSpace(checkoutIndex.CommitHash))
	}

	if stateResp, err := cli.sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sliceID}); err != nil {
		fmt.Printf("  Remote state: error (%v)\n", err)
	} else {
		fmt.Printf("  Remote head: %s\n", stateResp.GetLatestCommitHash())
		fmt.Printf("  Modified files: %d\n", len(stateResp.GetModifiedFiles()))
	}
	if checkoutIndex != nil {
		entries, err := collectNoGitWorkingTreeStatus(".", checkoutIndex)
		if err != nil {
			fmt.Printf("  Working tree: error (%v)\n", err)
		} else {
			entries = filterWorkingTreeStatusEntries(entries)
			added, modified, deleted := summarizeWorkingTreeStatus(entries)
			state := "clean"
			if len(entries) > 0 {
				state = "dirty"
			}
			fmt.Printf("  Working tree: %s (+%d ~%d -%d)\n", state, added, modified, deleted)
		}
	}

	absCwd, err := filepath.Abs(".")
	if err != nil {
		fmt.Printf("  Registry path: error (%v)\n", err)
		return
	}
	for _, record := range records {
		if filepath.Clean(record.Path) == filepath.Clean(absCwd) {
			fmt.Printf("  Registered checkout: yes (%s)\n", record.CommitHash)
			return
		}
	}
	fmt.Println("  Registered checkout: no")
}
