package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	adminv1 "github.com/niczy/gitslice/proto/admin"
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
		log.Printf("Unknown import command: %s", args[0])
		printImportHelp()
	}
}

func handleImportGit(ctx context.Context, cli *CLI, args []string) {
	fs := flag.NewFlagSet("import git", flag.ExitOnError)
	repo := fs.String("repo", ".", "Git repo source: local path on server host or remote URL (e.g. https://github.com/org/repo.git)")
	ref := fs.String("ref", "HEAD", "Git ref to import (e.g. HEAD, main, <sha>)")
	sliceID := fs.String("slice", "root_slice", "Target slice ID")
	mount := fs.String("mount", "", "Mount path prefix (default: /o/genesis/projects/<repo-name>)")
	reset := fs.Bool("reset", false, "Reset storage namespace before importing (DANGEROUS)")
	firstParent := fs.Bool("first-parent", true, "Import first-parent linear history (merges are not represented)")
	maxCommits := fs.Int("max-commits", 0, "Optional cap for number of commits imported (0 = no cap)")
	timeout := fs.Duration("timeout", 30*time.Minute, "Timeout for the import operation")
	fs.Parse(args)

	repoArg := strings.TrimSpace(*repo)
	req := &adminv1.ImportGitRepoRequest{
		Ref:          *ref,
		SliceId:      *sliceID,
		MountPath:    *mount,
		ResetStorage: *reset,
		FirstParent:  *firstParent,
		MaxCommits:   int32(*maxCommits),
	}
	if looksLikeGitURL(repoArg) {
		req.RepoUrl = repoArg
	} else {
		req.RepoPath = repoArg
	}

	importCtx := ctx
	if *timeout > 0 {
		var cancel context.CancelFunc
		importCtx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	resp, err := cli.adminClient.ImportGitRepo(importCtx, req)
	if err != nil {
		log.Fatalf("Import failed: %v", err)
	}

	displayMount := *mount
	if displayMount == "" {
		displayMount = "/o/genesis/projects/" + repoNameForDisplay(repoArg)
	}

	fmt.Printf("Imported %d commit(s) into slice %s at %s\n", resp.ImportedCommits, req.SliceId, displayMount)
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
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "ssh://") {
		return true
	}
	// Common GitHub SSH form: git@github.com:org/repo.git
	if strings.HasPrefix(s, "git@") && strings.Contains(s, ":") {
		return true
	}
	return false
}

func repoNameForDisplay(repoArg string) string {
	repoArg = strings.TrimSpace(repoArg)
	if repoArg == "" {
		return "repo"
	}
	// For URLs, strip any trailing slashes and .git suffix, then take the last path segment.
	if looksLikeGitURL(repoArg) {
		s := strings.TrimSuffix(repoArg, "/")
		s = strings.TrimSuffix(s, ".git")
		// Split on both / and : (for git@github.com:org/repo).
		s = strings.ReplaceAll(s, ":", "/")
		parts := strings.Split(s, "/")
		if len(parts) > 0 {
			last := strings.TrimSpace(parts[len(parts)-1])
			if last != "" {
				return last
			}
		}
		return "repo"
	}
	return filepath.Base(filepath.Clean(repoArg))
}
