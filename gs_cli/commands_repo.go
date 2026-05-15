package gscli

import (
	"context"
	"fmt"
	"os"
	"strings"

	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func handleRepoCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printRepoHelp()
		return
	}

	switch args[0] {
	case "import":
		handleRepoImport(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown repo command: %s", args[0]), false, "gs repo --help")
	}
}

func handleRepoImport(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, detachRequested := consumeBoolFlag(args, "detach")
	fs := newCommandFlagSet("repo import")
	branch := fs.String("branch", "", "Remote branch to import (default: remote default branch)")
	force := fs.Bool("force", false, "Overwrite an existing directory at the target path")
	githubToken := fs.String("github-token", strings.TrimSpace(os.Getenv("GITHUB_TOKEN")), "GitHub token for private repo import")
	detach := fs.Bool("detach", false, "Run the import as a detached local CLI job")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	detachEnabled := detachRequested || *detach

	if fs.NArg() != 2 {
		commandUsage("Usage: gs repo import <repo-url> </absolute/path> [--branch <name>] [--force] [--detach] [--json]")
		return
	}
	if detachEnabled {
		record, err := startDetachedCLIJob("repo import", append([]string{"repo", "import"}, args...))
		if err != nil {
			commandFatalf("JOB_START_FAILED", false, "", "Failed to start detached repo import job: %v", err)
		}
		emitDetachedJobStarted(record, jsonEnabled)
		return
	}

	resp, err := cli.filesystemClient.ImportRepo(ctx, &filesystemv1.ImportRepoRequest{
		RepoUrl:        fs.Arg(0),
		Path:           fs.Arg(1),
		Branch:         strings.TrimSpace(*branch),
		AllowOverwrite: *force,
		GithubToken:    strings.TrimSpace(*githubToken),
	})
	if err != nil {
		commandFatalf("REPO_IMPORT_FAILED", false, "", "Import failed: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonRepoImportOutput{
			RepoURL:      resp.GetRepoUrl(),
			Path:         resp.GetPath(),
			Branch:       resp.GetBranch(),
			CommitHash:   resp.GetCommitHash(),
			RemoteCommit: resp.GetRemoteCommit(),
			FileCount:    resp.GetFileCount(),
		})
		return
	}

	fmt.Printf("Imported %s into %s\n", resp.GetRepoUrl(), resp.GetPath())
	fmt.Printf("Branch: %s\n", resp.GetBranch())
	fmt.Printf("Remote commit: %s\n", resp.GetRemoteCommit())
	if resp.GetCommitHash() != "" {
		fmt.Printf("Home commit: %s\n", resp.GetCommitHash())
	}
	fmt.Printf("Files: %d\n", resp.GetFileCount())
}
