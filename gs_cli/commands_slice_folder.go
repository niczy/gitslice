package gscli

import (
	"context"
	"fmt"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func handleSliceFolderCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) == 0 {
		printSliceFolderHelp()
		return
	}
	if args[0] == "--help" || args[0] == "-h" || (args[0] == "--json" && len(args) == 1) {
		printSliceFolderHelp()
		return
	}

	switch args[0] {
	case "add":
		handleSliceFolderAdd(ctx, cli, args[1:])
	case "remove":
		handleSliceFolderRemove(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown slice folder command: %s", args[0]), false, "gs slice folder --help")
	}
}

func handleSliceFolderAdd(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice folder add")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 2 {
		commandUsage("Usage: gs slice folder add <slice-id-or-ref> <folder-path> [--json]")
		return
	}

	sliceID, err := resolveSliceRef(ctx, cli, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Invalid slice reference: %v", err)
	}
	folderPath := strings.TrimSpace(fs.Arg(1))
	if folderPath == "" {
		commandFatal("INVALID_ARGUMENT", "Folder path cannot be empty.", false, "")
	}

	resp, err := cli.sliceClient.AddSliceFolder(ctx, &slicev1.AddSliceFolderRequest{
		SliceId:    sliceID,
		FolderPath: folderPath,
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			commandFatalf("FOLDER_ALREADY_TRACKED", false, "", "Folder %q is already tracked by this slice.", folderPath)
		}
		if status.Code(err) == codes.NotFound {
			commandFatalf("FOLDER_NOT_FOUND", false, "", "Folder %q does not exist in the parent slice.", folderPath)
		}
		commandFatalf("SLICE_FOLDER_FAILED", true, "", "Failed to add tracked folder: %v", err)
	}

	mounts := resp.GetFolderMounts()
	if jsonEnabled {
		output := struct {
			SliceID string                 `json:"slice_id"`
			Path    string                 `json:"path"`
			Mounts  []*slicev1.FolderMount `json:"folder_mounts"`
			Files   []string               `json:"files"`
		}{
			SliceID: resp.GetSliceId(),
			Path:    folderPath,
			Mounts:  mounts,
			Files:   resp.GetFiles(),
		}
		writeJSONOutput(output)
		return
	}

	fmt.Printf("Added tracked folder %q to slice %s\n", folderPath, sliceID)
	if len(mounts) > 0 {
		fmt.Println("Tracked folders:")
		for _, m := range mounts {
			fmt.Printf("  %s -> %s\n", m.GetSourcePath(), m.GetAlias())
		}
	}
}

func handleSliceFolderRemove(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice folder remove")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 2 {
		commandUsage("Usage: gs slice folder remove <slice-id-or-ref> <folder-path> [--json]")
		return
	}

	sliceID, err := resolveSliceRef(ctx, cli, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_SLICE_REFERENCE", false, "gs slice list --json", "Invalid slice reference: %v", err)
	}
	folderPath := strings.TrimSpace(fs.Arg(1))
	if folderPath == "" {
		commandFatal("INVALID_ARGUMENT", "Folder path cannot be empty.", false, "")
	}

	resp, err := cli.sliceClient.RemoveSliceFolder(ctx, &slicev1.RemoveSliceFolderRequest{
		SliceId:    sliceID,
		FolderPath: folderPath,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			commandFatalf("FOLDER_NOT_TRACKED", false, "", "Folder %q is not tracked by this slice.", folderPath)
		}
		commandFatalf("SLICE_FOLDER_FAILED", true, "", "Failed to remove tracked folder: %v", err)
	}

	mounts := resp.GetFolderMounts()
	if jsonEnabled {
		output := struct {
			SliceID string                 `json:"slice_id"`
			Path    string                 `json:"path"`
			Mounts  []*slicev1.FolderMount `json:"folder_mounts"`
			Files   []string               `json:"files"`
		}{
			SliceID: resp.GetSliceId(),
			Path:    folderPath,
			Mounts:  mounts,
			Files:   resp.GetFiles(),
		}
		writeJSONOutput(output)
		return
	}

	fmt.Printf("Removed tracked folder %q from slice %s\n", folderPath, sliceID)
	if len(mounts) > 0 {
		fmt.Println("Remaining tracked folders:")
		for _, m := range mounts {
			fmt.Printf("  %s -> %s\n", m.GetSourcePath(), m.GetAlias())
		}
	} else {
		fmt.Println("No tracked folders remain.")
	}
}

func printSliceFolderHelp() {
	fmt.Println("Usage: gs slice folder add|remove <slice-id-or-ref> <folder-path> [--json]")
	fmt.Println("\nCommands:")
	fmt.Println("  add       Add a tracked folder from the parent slice")
	fmt.Println("  remove    Remove a tracked folder from the slice")
	fmt.Println("\nExamples:")
	fmt.Println("  gs slice folder add my-project src/components --json")
	fmt.Println("  gs slice folder remove my-project src/components --json")
}
