package main

import (
	"context"
	"fmt"
	"strings"

	commonv1 "github.com/niczy/gitslice/proto/common"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type jsonSliceVisibilityOutput struct {
	SliceID             string `json:"slice_id"`
	Visibility          string `json:"visibility"`
	PathPropagationMode string `json:"path_propagation_mode,omitempty"`
}

type jsonPathVisibilityOutput struct {
	WorkspaceID         string `json:"workspace_id,omitempty"`
	Path                string `json:"path"`
	Visibility          string `json:"visibility"`
	ExplicitRule        bool   `json:"explicit_rule"`
	ResolvedFromPath    string `json:"resolved_from_path,omitempty"`
	EffectiveVisibility string `json:"effective_visibility"`
	Recursive           bool   `json:"recursive"`
}

func handleSliceVisibilityCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) == 0 {
		printSliceVisibilityHelp()
		return
	}
	if args[0] == "--help" || args[0] == "-h" || (args[0] == "--json" && len(args) == 1) {
		printSliceVisibilityHelp()
		return
	}

	switch args[0] {
	case "get":
		handleSliceVisibilityGet(ctx, cli, args[1:])
	case "set":
		handleSliceVisibilitySet(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown slice visibility command: %s", args[0]), false, "gs slice visibility --help")
	}
}

func handleSliceVisibilityGet(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice visibility get")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs slice visibility get <slice-id-or-ref> [--json]")
		return
	}

	sliceID, err := resolveSliceRef(ctx, cli, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Invalid slice reference: %v", err)
	}

	resp, err := cli.sliceClient.GetSliceVisibility(ctx, &slicev1.GetSliceVisibilityRequest{
		SliceId: sliceID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			commandFatalf("SLICE_NOT_FOUND", false, "gs slice list --json", "Failed to load slice visibility: %v", err)
		}
		commandFatalf("SLICE_VISIBILITY_FAILED", true, "", "Failed to load slice visibility: %v", err)
	}

	output := buildSliceVisibilityOutput(resp.GetSliceId(), resp.GetVisibility(), commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_UNSPECIFIED)
	if jsonEnabled {
		writeJSONOutput(output)
		return
	}

	fmt.Printf("Slice: %s\n", output.SliceID)
	fmt.Printf("Visibility: %s\n", output.Visibility)
}

func handleSliceVisibilitySet(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice visibility set")
	propagate := fs.String("propagate", "unchanged", "Update global path visibility for current slice paths: unchanged, public, private")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 2 {
		commandUsage("Usage: gs slice visibility set <slice-id-or-ref> <public|private> [--propagate unchanged|public|private] [--json]")
		return
	}

	sliceID, err := resolveSliceRef(ctx, cli, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Invalid slice reference: %v", err)
	}
	visibility, err := parseVisibilityArg(fs.Arg(1))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid visibility: %v", err)
	}
	propagationMode, err := parsePathPropagationModeArg(*propagate)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid propagation mode: %v", err)
	}
	if visibility != commonv1.Visibility_VISIBILITY_PUBLIC &&
		propagationMode != commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_UNCHANGED {
		commandFatal("INVALID_ARGUMENT", "Path propagation is only supported when making a slice public.", false, "")
	}

	resp, err := cli.sliceClient.SetSliceVisibility(ctx, &slicev1.SetSliceVisibilityRequest{
		SliceId:             sliceID,
		Visibility:          visibility,
		PathPropagationMode: propagationMode,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			commandFatalf("SLICE_NOT_FOUND", false, "gs slice list --json", "Failed to update slice visibility: %v", err)
		}
		commandFatalf("SLICE_VISIBILITY_FAILED", true, "", "Failed to update slice visibility: %v", err)
	}

	output := buildSliceVisibilityOutput(resp.GetSliceId(), resp.GetVisibility(), resp.GetPathPropagationMode())
	if jsonEnabled {
		writeJSONOutput(output)
		return
	}

	fmt.Printf("Slice: %s\n", output.SliceID)
	fmt.Printf("Visibility: %s\n", output.Visibility)
	if output.PathPropagationMode != "" {
		fmt.Printf("Path propagation: %s\n", output.PathPropagationMode)
	}
}

func handleFilesystemVisibilityCommand(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) == 0 {
		printFilesystemVisibilityHelp()
		return
	}
	if args[0] == "--help" || args[0] == "-h" || (args[0] == "--json" && len(args) == 1) {
		printFilesystemVisibilityHelp()
		return
	}

	switch args[0] {
	case "get":
		handleFilesystemVisibilityGet(ctx, cli, authConfig, args[1:])
	case "set":
		handleFilesystemVisibilitySet(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown fs visibility command: %s", args[0]), false, "gs fs visibility --help")
	}
}

func handleFilesystemVisibilityGet(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs visibility get")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs visibility get </absolute/path> [--json]")
		return
	}

	workspaceID, remotePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}

	resp, err := cli.filesystemClient.GetPathVisibility(ctx, &filesystemv1.GetPathVisibilityRequest{
		WorkspaceId: workspaceID,
		Path:        remotePath,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			commandFatalf("PATH_NOT_FOUND", false, "", "Failed to load path visibility: %v", err)
		}
		commandFatalf("FS_VISIBILITY_FAILED", true, "", "Failed to load path visibility: %v", err)
	}

	output := buildPathVisibilityOutput(resp.GetWorkspaceId(), resp.GetVisibility(), false)
	if jsonEnabled {
		writeJSONOutput(output)
		return
	}

	printPathVisibility(output)
}

func handleFilesystemVisibilitySet(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs visibility set")
	recursive := fs.Bool("recursive", false, "Treat the target path as a directory visibility rule")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs visibility set </absolute/path> <public|private> [--recursive] [--json]")
		return
	}

	remotePath, err := parseAbsoluteFilesystemPathArg(fs.Arg(0), true)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}
	visibility, err := parseVisibilityArg(fs.Arg(1))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid visibility: %v", err)
	}

	resp, err := cli.filesystemClient.SetPathVisibility(ctx, &filesystemv1.SetPathVisibilityRequest{
		Path:       remotePath,
		Visibility: visibility,
		Recursive:  *recursive,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			commandFatalf("PATH_NOT_FOUND", false, "", "Failed to update path visibility: %v", err)
		}
		commandFatalf("FS_VISIBILITY_FAILED", true, "", "Failed to update path visibility: %v", err)
	}

	output := buildPathVisibilityOutput("", resp.GetVisibility(), resp.GetRecursive())
	if jsonEnabled {
		writeJSONOutput(output)
		return
	}

	printPathVisibility(output)
}

func buildSliceVisibilityOutput(sliceID string, visibility commonv1.Visibility, propagationMode commonv1.PathVisibilityPropagationMode) jsonSliceVisibilityOutput {
	output := jsonSliceVisibilityOutput{
		SliceID:    strings.TrimSpace(sliceID),
		Visibility: visibilityLabel(visibility),
	}
	if propagationMode != commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_UNSPECIFIED {
		output.PathPropagationMode = pathPropagationModeLabel(propagationMode)
	}
	return output
}

func buildPathVisibilityOutput(workspaceID string, info *filesystemv1.PathVisibilityInfo, recursive bool) jsonPathVisibilityOutput {
	if info == nil {
		return jsonPathVisibilityOutput{WorkspaceID: strings.TrimSpace(workspaceID), Recursive: recursive}
	}
	return jsonPathVisibilityOutput{
		WorkspaceID:         strings.TrimSpace(workspaceID),
		Path:                strings.TrimSpace(info.GetPath()),
		Visibility:          visibilityLabel(info.GetVisibility()),
		ExplicitRule:        info.GetExplicitRule(),
		ResolvedFromPath:    strings.TrimSpace(info.GetResolvedFromPath()),
		EffectiveVisibility: visibilityLabel(info.GetEffectiveVisibility()),
		Recursive:           recursive,
	}
}

func printPathVisibility(output jsonPathVisibilityOutput) {
	if output.WorkspaceID != "" {
		fmt.Printf("Workspace: %s\n", output.WorkspaceID)
	}
	fmt.Printf("Path: %s\n", output.Path)
	fmt.Printf("Visibility: %s\n", output.Visibility)
	fmt.Printf("Effective visibility: %s\n", output.EffectiveVisibility)
	fmt.Printf("Explicit rule: %t\n", output.ExplicitRule)
	if output.ResolvedFromPath != "" {
		fmt.Printf("Resolved from: %s\n", output.ResolvedFromPath)
	}
	fmt.Printf("Recursive: %t\n", output.Recursive)
}

func parseVisibilityArg(raw string) (commonv1.Visibility, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "private":
		return commonv1.Visibility_VISIBILITY_PRIVATE, nil
	case "public":
		return commonv1.Visibility_VISIBILITY_PUBLIC, nil
	default:
		return commonv1.Visibility_VISIBILITY_UNSPECIFIED, fmt.Errorf("must be public or private")
	}
}

func visibilityLabel(visibility commonv1.Visibility) string {
	switch visibility {
	case commonv1.Visibility_VISIBILITY_PRIVATE:
		return "private"
	case commonv1.Visibility_VISIBILITY_PUBLIC:
		return "public"
	default:
		return "unspecified"
	}
}

func parsePathPropagationModeArg(raw string) (commonv1.PathVisibilityPropagationMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "unchanged":
		return commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_UNCHANGED, nil
	case "public":
		return commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PUBLIC, nil
	case "private":
		return commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PRIVATE, nil
	default:
		return commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_UNSPECIFIED, fmt.Errorf("must be unchanged, public, or private")
	}
}

func pathPropagationModeLabel(mode commonv1.PathVisibilityPropagationMode) string {
	switch mode {
	case commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_UNCHANGED:
		return "unchanged"
	case commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PUBLIC:
		return "public"
	case commonv1.PathVisibilityPropagationMode_PATH_VISIBILITY_PROPAGATION_MODE_PRIVATE:
		return "private"
	default:
		return "unspecified"
	}
}
