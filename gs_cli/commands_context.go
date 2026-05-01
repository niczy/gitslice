package gscli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleContext(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("context")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs context [--json]")
		return
	}

	cwd, err := filepath.Abs(".")
	if err != nil {
		commandFatalf("CONTEXT_FAILED", false, "", "Failed to resolve current directory: %v", err)
	}

	creds := credentialsConfig{}
	if authConfig.CredentialStore {
		if loaded, err := readCredentialsConfig(); err == nil {
			creds = loaded
		}
	}

	out := jsonContextOutput{
		CurrentDir: cwd,
		Auth:       buildDoctorAuthOutput(authConfig, creds),
	}

	if homeSliceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig); err == nil {
		out.HomeSliceID = homeSliceID
	}

	trackedChangesetID, trackedErr := readTrackedChangesetIDFromConfig()
	if trackedErr == nil {
		trackedChangesetID = strings.TrimSpace(trackedChangesetID)
	}
	if trackedErr != nil {
		out.TrackedChange.Error = trackedErr.Error()
	} else if trackedChangesetID != "" {
		out.TrackedChange.Present = true
		out.TrackedChange.ChangesetID = trackedChangesetID
		if reviewResp, reviewErr := cli.sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{
			ChangesetId: trackedChangesetID,
		}); reviewErr != nil {
			out.TrackedChange.Error = reviewErr.Error()
		} else {
			out.TrackedChange.ReviewStatus = reviewResp.GetReviewStatus().String()
		}
	}

	if bindingsResp, err := cli.filesystemClient.ListRepoBindings(ctx, &filesystemv1.ListRepoBindingsRequest{}); err == nil {
		out.RepoBindings = make([]jsonRepoBinding, 0, len(bindingsResp.GetBindings()))
		for _, binding := range bindingsResp.GetBindings() {
			out.RepoBindings = append(out.RepoBindings, buildRepoBindingOutput(binding))
		}
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		out.Checkout.Error = err.Error()
	} else {
		out.Checkout.Present = true
		out.Checkout.SliceID = sliceID
		out.Checkout.Mode = "no-git"

		checkoutIndex, checkoutErr := detectCheckoutMode(".")
		if checkoutErr != nil {
			out.Checkout.Error = checkoutErr.Error()
		} else {
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
			if stateResp, stateErr := cli.sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sliceID}); stateErr == nil {
				out.Checkout.RemoteHead = strings.TrimSpace(stateResp.GetLatestCommitHash())
				switch {
				case out.Checkout.CheckoutBase == "":
					out.Checkout.SyncStatus = "unknown"
				case out.Checkout.RemoteHead != "" && out.Checkout.RemoteHead != out.Checkout.CheckoutBase:
					out.Checkout.SyncStatus = "behind_remote_head"
				default:
					out.Checkout.SyncStatus = "current"
				}
			}
		}
	}

	if jsonEnabled {
		writeJSONOutput(out)
		return
	}

	fmt.Printf("Current directory: %s\n", out.CurrentDir)
	if out.Auth.Username != "" {
		fmt.Printf("Auth: %s via %s\n", out.Auth.Username, out.Auth.Source)
	} else {
		fmt.Printf("Auth source: %s\n", out.Auth.Source)
	}
	if out.Auth.AuthMethod != "" {
		fmt.Printf("Auth method: %s\n", out.Auth.AuthMethod)
	}
	if out.Auth.AgentKeyID != "" {
		fmt.Printf("Agent key: %s\n", out.Auth.AgentKeyID)
	}
	if out.HomeSliceID != "" {
		fmt.Printf("Home slice: %s\n", out.HomeSliceID)
	}
	if out.Checkout.Present {
		fmt.Printf("Checkout: %s (%s)\n", out.Checkout.SliceID, out.Checkout.Mode)
		if out.Checkout.CheckoutBase != "" {
			fmt.Printf("Checkout base: %s\n", out.Checkout.CheckoutBase)
		}
		if out.Checkout.SyncStatus != "" {
			fmt.Printf("Sync: %s\n", out.Checkout.SyncStatus)
		}
		fmt.Printf(
			"Working tree: %s (+%d ~%d -%d)\n",
			firstNonEmpty(out.Checkout.WorkingTree, "unknown"),
			out.Checkout.Changes.Added,
			out.Checkout.Changes.Modified,
			out.Checkout.Changes.Deleted,
		)
	} else {
		fmt.Printf("Checkout: none (%s)\n", out.Checkout.Error)
	}
	if out.TrackedChange.Present {
		fmt.Printf("Tracked changeset: %s [%s]\n", out.TrackedChange.ChangesetID, out.TrackedChange.ReviewStatus)
	} else {
		fmt.Println("Tracked changeset: none")
	}
	fmt.Printf("Repo bindings: %d\n", len(out.RepoBindings))
}
