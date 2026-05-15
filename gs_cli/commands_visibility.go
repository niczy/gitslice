package gscli

import (
	"context"
	"fmt"
	"strings"

	commonv1 "github.com/niczy/gitslice/proto/common"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type jsonSliceVisibilityOutput struct {
	SliceID    string `json:"slice_id"`
	Visibility string `json:"visibility"`
}

type jsonPathSliceVisibilityOutput struct {
	SliceID    string `json:"slice_id,omitempty"`
	Path       string `json:"path"`
	Visibility string `json:"visibility"`
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

	output := buildSliceVisibilityOutput(resp.GetSliceId(), resp.GetVisibility())
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
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 2 {
		commandUsage("Usage: gs slice visibility set <slice-id-or-ref> <public|private> [--json]")
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

	resp, err := cli.sliceClient.SetSliceVisibility(ctx, &slicev1.SetSliceVisibilityRequest{
		SliceId:    sliceID,
		Visibility: visibility,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			commandFatalf("SLICE_NOT_FOUND", false, "gs slice list --json", "Failed to update slice visibility: %v", err)
		}
		commandFatalf("SLICE_VISIBILITY_FAILED", true, "", "Failed to update slice visibility: %v", err)
	}

	output := buildSliceVisibilityOutput(resp.GetSliceId(), resp.GetVisibility())
	if jsonEnabled {
		writeJSONOutput(output)
		return
	}

	fmt.Printf("Slice: %s\n", output.SliceID)
	fmt.Printf("Visibility: %s\n", output.Visibility)
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
		handleFilesystemVisibilitySet(args[1:])
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

	sliceID, remotePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}

	resp, err := cli.sliceClient.GetSliceVisibility(ctx, &slicev1.GetSliceVisibilityRequest{
		SliceId: sliceID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			commandFatalf("SLICE_NOT_FOUND", false, "", "Failed to load slice visibility: %v", err)
		}
		commandFatalf("SLICE_VISIBILITY_FAILED", true, "", "Failed to load slice visibility: %v", err)
	}

	output := buildPathSliceVisibilityOutput(resp.GetSliceId(), remotePath, resp.GetVisibility())
	if jsonEnabled {
		writeJSONOutput(output)
		return
	}

	printPathSliceVisibility(output)
}

func handleFilesystemVisibilitySet(args []string) {
	args, _ = consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs visibility set")
	fs.Bool("json", false, "Print structured JSON output")
	fs.Bool("recursive", false, "Deprecated; path visibility has been removed")
	parseFlagSetInterspersed(fs, args)
	if fs.NArg() != 2 {
		commandUsage("Usage: gs slice visibility set <slice-id-or-ref> <public|private> [--json]")
		return
	}
	commandFatal("PATH_VISIBILITY_REMOVED", "Path visibility has been removed; use `gs slice visibility set <slice-id-or-ref> <public|private>`.", false, "gs slice visibility --help")
}

func buildSliceVisibilityOutput(sliceID string, visibility commonv1.Visibility) jsonSliceVisibilityOutput {
	return jsonSliceVisibilityOutput{
		SliceID:    strings.TrimSpace(sliceID),
		Visibility: visibilityLabel(visibility),
	}
}

func buildPathSliceVisibilityOutput(sliceID, remotePath string, visibility commonv1.Visibility) jsonPathSliceVisibilityOutput {
	return jsonPathSliceVisibilityOutput{
		SliceID:    strings.TrimSpace(sliceID),
		Path:       strings.TrimSpace(remotePath),
		Visibility: visibilityLabel(visibility),
	}
}

func printPathSliceVisibility(output jsonPathSliceVisibilityOutput) {
	if output.SliceID != "" {
		fmt.Printf("Slice: %s\n", output.SliceID)
	}
	fmt.Printf("Path: %s\n", output.Path)
	fmt.Printf("Visibility: %s\n", output.Visibility)
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
