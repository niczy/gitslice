package gscli

import (
	"context"
	"fmt"
	"os"
	"strings"

	accountv1 "github.com/niczy/gitslice/proto/account"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func handleSliceEnsure(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	if len(args) < 2 {
		commandUsage("Usage: gs slice ensure <name> <folder-path[,folder-path...]> [--folders <folder-path[,folder-path...]>] [--description <text>] [--json]")
		return
	}

	sliceName := strings.TrimSpace(args[0])
	if sliceName == "" {
		commandUsage("Slice name cannot be empty")
		return
	}
	folderPaths := parseSliceFolderPaths(args[1])

	fs := newCommandFlagSet("slice ensure")
	description := fs.String("description", "Focused slice", "Description of the slice")
	moreFolders := fs.String("folders", "", "Additional comma-separated folder paths to include in this slice")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args[2:])
	jsonEnabled := jsonRequested || *jsonOutput

	folderPaths = append(folderPaths, parseSliceFolderPaths(*moreFolders)...)
	if len(folderPaths) == 0 {
		commandUsage("At least one folder path is required")
		return
	}

	meResp, err := cli.accountClient.GetMe(ctx, &accountv1.GetMeRequest{})
	if err != nil {
		commandFatalf("SLICE_ENSURE_FAILED", true, "", "Failed to resolve current user: %v", err)
	}
	slug := ensureSliceSlug(meResp.GetUsername(), sliceName)
	if slugResp, err := cli.sliceClient.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: slug}); err == nil {
		if jsonEnabled {
			writeJSONOutput(jsonSliceEnsureOutput{
				Created:     false,
				Name:        slugResp.GetName(),
				SliceID:     slugResp.GetSliceId(),
				Slug:        slugResp.GetSlug(),
				Description: slugResp.GetDescription(),
				Status:      "existing",
			})
			return
		}
		fmt.Printf("Using existing slice: %s (id: %s)\n", slugResp.GetName(), slugResp.GetSliceId())
		fmt.Printf("Slug: %s\n", slugResp.GetSlug())
		return
	} else if status.Code(err) != codes.NotFound {
		commandFatalf("SLICE_ENSURE_FAILED", true, "", "Failed to resolve slice slug %s: %v", slug, err)
	}

	rootResp, err := cli.sliceClient.GetRootSlice(ctx, &slicev1.GetRootSliceRequest{})
	if err != nil {
		commandFatalf("SLICE_ENSURE_FAILED", true, "", "Failed to resolve published root slice: %v", err)
	}
	resp, err := cli.sliceClient.CreateSliceFromFolder(ctx, &slicev1.CreateSliceFromFolderRequest{
		ParentSliceId: rootResp.GetSliceId(),
		FolderPaths:   folderPaths,
		Name:          sliceName,
		Description:   *description,
	})
	if err != nil {
		commandFatalf("SLICE_ENSURE_FAILED", true, "", "Failed to create slice: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(jsonSliceEnsureOutput{
			Created:     true,
			Name:        resp.GetName(),
			SliceID:     resp.GetSliceId(),
			Slug:        resp.GetSlug(),
			Description: *description,
			Status:      resp.GetStatus(),
		})
		return
	}
	fmt.Printf("Created slice: %s (id: %s)\n", resp.GetName(), resp.GetSliceId())
	fmt.Printf("Slug: %s\n", resp.GetSlug())
}

func handleRepoEnsure(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("repo ensure")
	branch := fs.String("branch", "", "Remote branch to import (default: remote default branch)")
	force := fs.Bool("force", false, "Replace an existing conflicting binding or path")
	pushEnabled := fs.Bool("push-enabled", false, "Ensure pushback is enabled for this binding")
	githubToken := fs.String("github-token", strings.TrimSpace(os.Getenv("GITHUB_TOKEN")), "GitHub token for private repo import")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 2 {
		commandUsage("Usage: gs repo ensure <repo-url> </absolute/path> [--branch <name>] [--push-enabled] [--force] [--json]")
		return
	}

	repoURL := strings.TrimSpace(fs.Arg(0))
	targetPath := strings.TrimSpace(fs.Arg(1))
	bindingsResp, err := cli.filesystemClient.ListRepoBindings(ctx, &filesystemv1.ListRepoBindingsRequest{})
	if err != nil {
		commandFatalf("REPO_ENSURE_FAILED", true, "", "Failed to list repo bindings: %v", err)
	}
	for _, binding := range bindingsResp.GetBindings() {
		if binding.GetPath() != targetPath {
			continue
		}
		sameRepo := binding.GetRepoUrl() == repoURL
		sameBranch := strings.TrimSpace(*branch) == "" || binding.GetBranch() == strings.TrimSpace(*branch)
		samePushMode := binding.GetPushEnabled() == *pushEnabled
		if sameRepo && sameBranch && samePushMode {
			if jsonEnabled {
				writeJSONOutput(jsonRepoEnsureOutput{
					Created: false,
					Updated: false,
					Binding: buildRepoBindingOutput(binding),
				})
				return
			}
			fmt.Printf("Using existing repo binding: %s\n", binding.GetPath())
			fmt.Printf("Repo: %s\n", binding.GetRepoUrl())
			return
		}
		if !*force {
			commandFatal(
				"REPO_BINDING_MISMATCH",
				fmt.Sprintf("Repo binding at %s does not match requested repo/branch/push settings", targetPath),
				false,
				"rerun with --force to replace the existing binding",
			)
		}
	}

	resp, err := cli.filesystemClient.ImportRepo(ctx, &filesystemv1.ImportRepoRequest{
		RepoUrl:        repoURL,
		Path:           targetPath,
		Branch:         strings.TrimSpace(*branch),
		AllowOverwrite: *force,
		PushEnabled:    *pushEnabled,
		GithubToken:    strings.TrimSpace(*githubToken),
	})
	if err != nil {
		commandFatalf("REPO_ENSURE_FAILED", true, "", "Failed to ensure repo binding: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(jsonRepoEnsureOutput{
			Created:      true,
			Updated:      *force,
			Binding:      buildRepoBindingOutput(resp.GetBinding()),
			CommitHash:   resp.GetCommitHash(),
			RemoteCommit: resp.GetRemoteCommit(),
			FileCount:    resp.GetFileCount(),
		})
		return
	}
	fmt.Printf("Ensured repo binding: %s -> %s\n", resp.GetBinding().GetPath(), resp.GetBinding().GetRepoUrl())
}

func ensureSliceSlug(username, name string) string {
	user := strings.TrimSpace(username)
	base := slugifyEnsureName(name)
	if user == "" {
		return base
	}
	return user + "/" + base
}

func slugifyEnsureName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		isAlpha := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if isAlpha || isDigit {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 48 {
		out = strings.TrimRight(out[:48], "-")
	}
	if out == "" {
		return "slice"
	}
	return out
}
