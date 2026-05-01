package gscli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	adminv1 "github.com/niczy/gitslice/proto/admin"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"github.com/spf13/cobra"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "doctor [options]",
		Short:              "Check auth, slice binding, cache, and service health",
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			runAuthenticatedCLICommand(args, 24*time.Hour, handleDoctor)
		},
	}
}

func handleDoctor(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("doctor")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs doctor")
		return
	}

	creds := credentialsConfig{}
	if authConfig.CredentialStore {
		if loaded, err := readCredentialsConfig(); err == nil {
			creds = loaded
		}
	}

	out := jsonDoctorOutput{
		Auth: buildDoctorAuthOutput(authConfig, creds),
	}

	if meResp, err := cli.adminClient.Me(ctx, &adminv1.MeRequest{}); err != nil {
		out.Services.Admin.Error = err.Error()
	} else {
		out.Services.Admin.OK = true
		out.Services.Admin.Username = meResp.GetUser().GetUsername()
	}
	if rootResp, err := cli.sliceClient.GetRootSlice(ctx, &slicev1.GetRootSliceRequest{}); err != nil {
		out.Services.Slice.Error = err.Error()
	} else {
		out.Services.Slice.OK = true
		out.Services.Slice.RootSliceID = rootResp.GetSliceId()
		out.Services.Slice.Head = rootResp.GetCommitHash()
	}
	if stateResp, err := cli.adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{}); err != nil {
		out.Services.GlobalState.Error = err.Error()
	} else {
		out.Services.GlobalState.OK = true
		out.Services.GlobalState.Head = stateResp.GetGlobalCommitHash()
	}
	if homeSliceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig); err != nil {
		out.Services.Filesystem.Error = err.Error()
	} else {
		out.Services.Filesystem.OK = true
		out.Services.Filesystem.HomeSliceID = homeSliceID
	}

	cache, err := NewCacheManager()
	if err != nil {
		out.Cache.Error = err.Error()
	} else if stats, err := cache.Stats(); err != nil {
		out.Cache.Error = err.Error()
	} else {
		out.Cache.Root = cache.Root()
		out.Cache.ObjectCount = stats.ObjectCount
		out.Cache.TotalBytes = stats.TotalBytes
	}
	records, err := listCheckoutRecords()
	if err != nil {
		if out.Cache.Error == "" {
			out.Cache.Error = err.Error()
		}
	} else {
		out.Cache.TrackedCheckouts = len(records)
		out.Cache.UniqueSlices = countUniqueCheckoutSlices(records)
		out.Cache.StaleCheckoutRecords = countStaleCheckoutRecords(records)
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		out.Checkout.Error = err.Error()
	} else {
		out.Checkout.Present = true
		out.Checkout.SliceID = sliceID
		out.Checkout.Mode = "no-git"

		checkoutIndex, err := readCheckoutIndex(".")
		if err != nil {
			out.Checkout.Error = err.Error()
		} else if checkoutIndex != nil {
			out.Checkout.CheckoutBase = strings.TrimSpace(checkoutIndex.CommitHash)
			entries, statusErr := collectNoGitWorkingTreeStatus(".", checkoutIndex)
			if statusErr != nil {
				out.Checkout.Error = statusErr.Error()
			} else {
				entries = filterWorkingTreeStatusEntries(entries)
				added, modified, deleted := summarizeWorkingTreeStatus(entries)
				out.Checkout.Changes = jsonWorkingTreeSummary{
					Added:    added,
					Modified: modified,
					Deleted:  deleted,
				}
				if len(entries) == 0 {
					out.Checkout.WorkingTree = "clean"
				} else {
					out.Checkout.WorkingTree = "dirty"
				}
			}
		}

		if stateResp, err := cli.sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sliceID}); err != nil {
			if out.Checkout.Error == "" {
				out.Checkout.Error = err.Error()
			}
		} else {
			out.Checkout.RemoteHead = stateResp.GetLatestCommitHash()
			out.Checkout.ModifiedFiles = len(stateResp.GetModifiedFiles())
		}

		absCwd, absErr := filepath.Abs(".")
		if absErr != nil {
			if out.Checkout.Error == "" {
				out.Checkout.Error = absErr.Error()
			}
		} else {
			for _, record := range records {
				if filepath.Clean(record.Path) == filepath.Clean(absCwd) {
					out.Checkout.Registered = true
					out.Checkout.RegisteredCommitHash = record.CommitHash
					break
				}
			}
		}
	}

	if jsonEnabled {
		writeJSONOutput(out)
		return
	}

	fmt.Println("Auth:")
	fmt.Printf("  Source: %s\n", out.Auth.Source)
	if out.Auth.Username != "" {
		fmt.Printf("  Username: %s\n", out.Auth.Username)
	}
	fmt.Printf("  Stored credentials: %t\n", out.Auth.StoredCredentials)
	if out.Auth.AuthMethod != "" {
		fmt.Printf("  Method: %s\n", out.Auth.AuthMethod)
	}
	if out.Auth.SessionID != "" {
		fmt.Printf("  Session: %s\n", out.Auth.SessionID)
	}
	if out.Auth.AgentKeyID != "" {
		fmt.Printf("  Agent key: %s\n", out.Auth.AgentKeyID)
	}

	fmt.Println("Services:")
	if out.Services.Admin.OK {
		fmt.Printf("  Admin: ok as %s\n", out.Services.Admin.Username)
	} else {
		fmt.Printf("  Admin: error (%s)\n", out.Services.Admin.Error)
	}
	if out.Services.Slice.OK {
		fmt.Printf("  Slice: ok root=%s head=%s\n", out.Services.Slice.RootSliceID, out.Services.Slice.Head)
	} else {
		fmt.Printf("  Slice: error (%s)\n", out.Services.Slice.Error)
	}
	if out.Services.GlobalState.OK {
		fmt.Printf("  Global state: ok head=%s\n", out.Services.GlobalState.Head)
	} else {
		fmt.Printf("  Global state: error (%s)\n", out.Services.GlobalState.Error)
	}
	if out.Services.Filesystem.OK {
		fmt.Printf("  Filesystem: ok home=%s\n", out.Services.Filesystem.HomeSliceID)
	} else {
		fmt.Printf("  Filesystem: error (%s)\n", out.Services.Filesystem.Error)
	}

	fmt.Println("Cache:")
	if out.Cache.Error != "" {
		fmt.Printf("  Error: %s\n", out.Cache.Error)
	} else {
		fmt.Printf("  Root: %s\n", out.Cache.Root)
		fmt.Printf("  Objects: %d\n", out.Cache.ObjectCount)
		fmt.Printf("  Bytes: %d\n", out.Cache.TotalBytes)
	}
	fmt.Printf("  Tracked checkouts: %d\n", out.Cache.TrackedCheckouts)
	fmt.Printf("  Unique slices: %d\n", out.Cache.UniqueSlices)
	fmt.Printf("  Stale records: %d\n", out.Cache.StaleCheckoutRecords)

	fmt.Println("Checkout:")
	if !out.Checkout.Present {
		fmt.Printf("  Current directory: not a gitslice checkout (%s)\n", out.Checkout.Error)
		return
	}
	fmt.Printf("  Slice: %s\n", out.Checkout.SliceID)
	fmt.Printf("  Mode: %s\n", out.Checkout.Mode)
	if out.Checkout.CheckoutBase != "" {
		fmt.Printf("  Checkout base: %s\n", out.Checkout.CheckoutBase)
	}
	if out.Checkout.RemoteHead != "" {
		fmt.Printf("  Remote head: %s\n", out.Checkout.RemoteHead)
		fmt.Printf("  Modified files: %d\n", out.Checkout.ModifiedFiles)
	}
	if out.Checkout.WorkingTree != "" {
		fmt.Printf(
			"  Working tree: %s (+%d ~%d -%d)\n",
			out.Checkout.WorkingTree,
			out.Checkout.Changes.Added,
			out.Checkout.Changes.Modified,
			out.Checkout.Changes.Deleted,
		)
	}
	if out.Checkout.Error != "" {
		fmt.Printf("  Checkout metadata: error (%s)\n", out.Checkout.Error)
	}
	if out.Checkout.Registered {
		fmt.Printf("  Registered checkout: yes (%s)\n", out.Checkout.RegisteredCommitHash)
		return
	}
	fmt.Println("  Registered checkout: no")
}
