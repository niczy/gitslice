package gscli

import (
	"context"
	"fmt"
	"strings"
	"time"

	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func handleImportCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printImportHelp()
		return
	}

	switch args[0] {
	case "git":
		handleImportGit(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown import command: %s", args[0]), false, "gs import --help")
	}
}

func handleImportGit(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("import git")
	repo := fs.String("repo", "", "Remote git repository URL (e.g. https://github.com/org/repo.git)")
	ref := fs.String("ref", "HEAD", "Git ref to import (e.g. HEAD, main, <sha>)")
	mount := fs.String("mount", "", "Absolute mount path under your home directory (default: /<user>/<repo-name>)")
	firstParent := fs.Bool("first-parent", true, "Import first-parent linear history (merges are not represented)")
	maxCommits := fs.Int("max-commits", 0, "Optional cap for number of commits imported (0 = server default)")
	timeout := fs.Duration("timeout", 30*time.Minute, "Timeout for the import operation")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	repoArg := strings.TrimSpace(*repo)
	if repoArg == "" {
		if fs.NArg() > 0 {
			repoArg = strings.TrimSpace(fs.Arg(0))
		}
	}
	if !looksLikeGitURL(repoArg) {
		commandFatal("INVALID_ARGUMENT", "Remote https repo URL is required", false, "gs import git --repo https://github.com/org/repo.git")
	}
	req := &slicev1.ImportGitRepoRequest{
		RepoUrl:     repoArg,
		Ref:         *ref,
		MountPath:   *mount,
		FirstParent: *firstParent,
		MaxCommits:  int32(*maxCommits),
	}

	importCtx := ctx
	if *timeout > 0 {
		var cancel context.CancelFunc
		importCtx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	resp, err := cli.sliceClient.ImportGitRepo(importCtx, req)
	if err != nil {
		commandFatalf("IMPORT_FAILED", true, "", "Import failed: %v", err)
	}

	displayMount := *mount
	if displayMount == "" {
		displayMount = resp.GetMountPath()
	}
	if jsonEnabled {
		writeJSONOutput(jsonImportGitOutput{
			SliceID:         resp.GetSliceId(),
			MountPath:       displayMount,
			ImportedCommits: int(resp.GetImportedCommits()),
			HeadCommitHash:  resp.GetHeadCommitHash(),
			Warnings:        append([]string(nil), resp.GetWarnings()...),
		})
		return
	}

	fmt.Printf("Imported %d commit(s) into slice %s at %s\n", resp.ImportedCommits, resp.GetSliceId(), displayMount)
	fmt.Printf("Head commit: %s\n", resp.HeadCommitHash)
	if len(resp.Warnings) > 0 {
		fmt.Printf("Warnings: %d\n", len(resp.Warnings))
		for _, w := range resp.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func looksLikeGitURL(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	return strings.HasPrefix(s, "https://")
}
