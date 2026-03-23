package main

import (
	"context"
	"flag"
	"fmt"
	"log"
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
	case "list":
		handleRepoList(ctx, cli, args[1:])
	case "status":
		handleRepoStatus(ctx, cli, args[1:])
	case "pull":
		handleRepoPull(ctx, cli, args[1:])
	case "push":
		handleRepoPush(ctx, cli, args[1:])
	case "unlink":
		handleRepoUnlink(ctx, cli, args[1:])
	default:
		log.Printf("Unknown repo command: %s", args[0])
		printRepoHelp()
	}
}

func handleRepoImport(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("repo import", flag.ExitOnError)
	branch := fs.String("branch", "", "Remote branch to import (default: remote default branch)")
	force := fs.Bool("force", false, "Overwrite an existing directory or binding at the target path")
	pushEnabled := fs.Bool("push-enabled", false, "Allow future gs repo push operations for this binding")
	githubToken := fs.String("github-token", strings.TrimSpace(os.Getenv("GITHUB_TOKEN")), "GitHub token for private repo import")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 2 {
		log.Fatalf("Usage: gs repo import <repo-url> </absolute/path>")
	}

	resp, err := cli.filesystemClient.ImportRepo(ctx, &filesystemv1.ImportRepoRequest{
		RepoUrl:        fs.Arg(0),
		Path:           fs.Arg(1),
		Branch:         strings.TrimSpace(*branch),
		AllowOverwrite: *force,
		PushEnabled:    *pushEnabled,
		GithubToken:    strings.TrimSpace(*githubToken),
	})
	if err != nil {
		log.Fatalf("Import failed: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonRepoImportOutput{
			Binding:      buildRepoBindingOutput(resp.GetBinding()),
			CommitHash:   resp.GetCommitHash(),
			RemoteCommit: resp.GetRemoteCommit(),
			FileCount:    resp.GetFileCount(),
		})
		return
	}

	fmt.Printf("Imported %s into %s\n", resp.GetBinding().GetRepoUrl(), resp.GetBinding().GetPath())
	fmt.Printf("Branch: %s\n", resp.GetBinding().GetBranch())
	fmt.Printf("Remote commit: %s\n", resp.GetRemoteCommit())
	if resp.GetCommitHash() != "" {
		fmt.Printf("Home commit: %s\n", resp.GetCommitHash())
	}
	fmt.Printf("Files: %d\n", resp.GetFileCount())
}

func handleRepoList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("repo list", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput

	resp, err := cli.filesystemClient.ListRepoBindings(ctx, &filesystemv1.ListRepoBindingsRequest{})
	if err != nil {
		log.Fatalf("List failed: %v", err)
	}
	if jsonEnabled {
		out := jsonRepoListOutput{
			Total:    len(resp.GetBindings()),
			Bindings: make([]jsonRepoBinding, 0, len(resp.GetBindings())),
		}
		for _, binding := range resp.GetBindings() {
			out.Bindings = append(out.Bindings, buildRepoBindingOutput(binding))
		}
		writeJSONOutput(out)
		return
	}
	if len(resp.GetBindings()) == 0 {
		fmt.Println("No repo bindings.")
		return
	}
	for _, binding := range resp.GetBindings() {
		fmt.Printf("%s\t%s\t%s\tpush=%t\n", binding.GetPath(), binding.GetRepoUrl(), binding.GetBranch(), binding.GetPushEnabled())
	}
}

func handleRepoStatus(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("repo status", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		log.Fatalf("Usage: gs repo status </absolute/path>")
	}
	path := strings.TrimSpace(fs.Arg(0))
	resp, err := cli.filesystemClient.ListRepoBindings(ctx, &filesystemv1.ListRepoBindingsRequest{})
	if err != nil {
		log.Fatalf("Status failed: %v", err)
	}
	for _, binding := range resp.GetBindings() {
		if binding.GetPath() != path {
			continue
		}
		if jsonEnabled {
			out := jsonRepoStatusOutput{
				Found:   true,
				Binding: func() *jsonRepoBinding { b := buildRepoBindingOutput(binding); return &b }(),
			}
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Path: %s\n", binding.GetPath())
		fmt.Printf("Repo: %s\n", binding.GetRepoUrl())
		fmt.Printf("Branch: %s\n", binding.GetBranch())
		fmt.Printf("Push enabled: %t\n", binding.GetPushEnabled())
		fmt.Printf("Last imported: %s\n", binding.GetLastImportedCommit())
		fmt.Printf("Last pushed: %s\n", binding.GetLastPushedCommit())
		fmt.Printf("Last seen remote: %s\n", binding.GetLastSeenRemoteCommit())
		return
	}
	if jsonEnabled {
		writeJSONOutput(jsonRepoStatusOutput{Found: false})
		return
	}
	log.Fatalf("No repo binding found for %s", path)
}

func handleRepoPull(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("repo pull", flag.ExitOnError)
	force := fs.Bool("force", true, "Overwrite the bound directory with the remote snapshot")
	githubToken := fs.String("github-token", strings.TrimSpace(os.Getenv("GITHUB_TOKEN")), "GitHub token for private repo pull")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		log.Fatalf("Usage: gs repo pull </absolute/path>")
	}

	resp, err := cli.filesystemClient.PullRepoBinding(ctx, &filesystemv1.PullRepoBindingRequest{
		Path:           fs.Arg(0),
		AllowOverwrite: *force,
		GithubToken:    strings.TrimSpace(*githubToken),
	})
	if err != nil {
		log.Fatalf("Pull failed: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonRepoPullOutput{
			Binding:      buildRepoBindingOutput(resp.GetBinding()),
			CommitHash:   resp.GetCommitHash(),
			RemoteCommit: resp.GetRemoteCommit(),
			FileCount:    resp.GetFileCount(),
			Updated:      resp.GetCommitHash() != "",
		})
		return
	}
	if resp.GetCommitHash() == "" {
		fmt.Printf("Already up to date: %s\n", resp.GetRemoteCommit())
		return
	}
	fmt.Printf("Pulled %s to %s\n", resp.GetRemoteCommit(), resp.GetBinding().GetPath())
	fmt.Printf("Home commit: %s\n", resp.GetCommitHash())
	fmt.Printf("Files: %d\n", resp.GetFileCount())
}

func handleRepoPush(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("repo push", flag.ExitOnError)
	message := fs.String("message", "", "Commit message for the remote push")
	githubToken := fs.String("github-token", strings.TrimSpace(os.Getenv("GITHUB_TOKEN")), "GitHub token used to push to the remote")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		log.Fatalf("Usage: gs repo push </absolute/path>")
	}

	resp, err := cli.filesystemClient.PushRepoBinding(ctx, &filesystemv1.PushRepoBindingRequest{
		Path:        fs.Arg(0),
		Message:     strings.TrimSpace(*message),
		GithubToken: strings.TrimSpace(*githubToken),
	})
	if err != nil {
		log.Fatalf("Push failed: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonRepoPushOutput{
			Binding:      buildRepoBindingOutput(resp.GetBinding()),
			RemoteCommit: resp.GetRemoteCommit(),
			Pushed:       resp.GetPushed(),
		})
		return
	}
	if !resp.GetPushed() {
		fmt.Printf("No remote changes to push. Remote commit: %s\n", resp.GetRemoteCommit())
		return
	}
	fmt.Printf("Pushed %s from %s\n", resp.GetRemoteCommit(), resp.GetBinding().GetPath())
}

func handleRepoUnlink(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := flag.NewFlagSet("repo unlink", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	fs.Parse(args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		log.Fatalf("Usage: gs repo unlink </absolute/path>")
	}

	resp, err := cli.filesystemClient.DeleteRepoBinding(ctx, &filesystemv1.DeleteRepoBindingRequest{Path: fs.Arg(0)})
	if err != nil {
		log.Fatalf("Unlink failed: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonRepoUnlinkOutput{
			Path:   resp.GetPath(),
			Status: "removed",
		})
		return
	}
	fmt.Printf("Removed repo binding: %s\n", resp.GetPath())
}
