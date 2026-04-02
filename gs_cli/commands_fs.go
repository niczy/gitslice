package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/niczy/gitslice/internal/homeslice"
	accountv1 "github.com/niczy/gitslice/proto/account"
	filev1 "github.com/niczy/gitslice/proto/file"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
)

func handleFilesystemCommand(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) < 1 {
		printFilesystemHelp()
		return
	}

	switch args[0] {
	case "cat":
		handleFilesystemCat(ctx, cli, authConfig, args[1:])
	case "write":
		handleFilesystemWrite(ctx, cli, authConfig, args[1:])
	case "batch":
		handleFilesystemBatch(ctx, cli, authConfig, args[1:])
	case "ls":
		handleFilesystemListDirectory(ctx, cli, authConfig, args[1:])
	case "mkdir":
		handleFilesystemMkdir(ctx, cli, authConfig, args[1:])
	case "ensure-dir":
		handleFilesystemEnsureDir(ctx, cli, authConfig, args[1:])
	case "rm":
		handleFilesystemRemove(ctx, cli, authConfig, args[1:])
	case "mv":
		handleFilesystemMove(ctx, cli, authConfig, args[1:])
	case "cp":
		handleFilesystemCopy(ctx, cli, authConfig, args[1:])
	case "glob":
		handleFilesystemGlob(ctx, cli, authConfig, args[1:])
	case "search":
		handleFilesystemSearch(ctx, cli, authConfig, args[1:])
	case "stat":
		handleFilesystemStat(ctx, cli, authConfig, args[1:])
	case "snapshot":
		handleFilesystemSnapshot(ctx, cli, authConfig, args[1:])
	case "snapshots":
		handleFilesystemSnapshots(ctx, cli, authConfig, args[1:])
	case "log":
		handleFilesystemLog(ctx, cli, authConfig, args[1:])
	case "restore":
		handleFilesystemRestore(ctx, cli, authConfig, args[1:])
	case "diff":
		handleFilesystemDiff(ctx, cli, authConfig, args[1:])
	case "show":
		handleFilesystemShow(ctx, cli, authConfig, args[1:])
	case "shell":
		handleFilesystemShell(ctx, cli, authConfig, args[1:])
	case "sync":
		handleFilesystemSync(ctx, cli, authConfig, args[1:])
	case "upload":
		handleFilesystemUpload(ctx, cli, authConfig, args[1:])
	case "download":
		handleFilesystemDownload(ctx, cli, authConfig, args[1:])
	case "visibility":
		handleFilesystemVisibilityCommand(ctx, cli, authConfig, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown fs command: %s", args[0]), false, "gs fs --help")
	}
}

func handleFilesystemEnsureDir(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs ensure-dir")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs ensure-dir </absolute/path> [--json]")
		return
	}

	workspaceID, dirPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}

	entry, exists, err := statFilesystemEntry(ctx, cli.filesystemClient, workspaceID, dirPath)
	if err != nil {
		commandFatalf("FS_ENSURE_DIR_FAILED", true, "", "Failed to inspect path: %v", err)
	}
	if exists {
		if entry.GetType() != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
			commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Path exists and is not a directory: %s", dirPath), false, "")
		}
		out := jsonFilesystemActionOutput{
			Action:    "ensure-dir",
			Status:    "exists",
			Path:      dirPath,
			EntryType: filesystemEntryTypeLabel(entry.GetType()),
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Directory already exists: %s\n", dirPath)
		return
	}

	resp, err := cli.filesystemClient.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath,
	})
	if err != nil {
		commandFatalf("FS_ENSURE_DIR_FAILED", true, "", "Failed to create directory: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonFilesystemActionOutput{
			Action:     "ensure-dir",
			Status:     "created",
			Path:       resp.GetPath(),
			CommitHash: resp.GetCommitHash(),
			EntryType:  "directory",
		})
		return
	}
	fmt.Printf("Ensured directory: %s\n", resp.GetPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemCat(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs cat")
	raw := fs.Bool("raw", false, "Write file bytes only")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs cat </absolute/path> [--raw] [--json]")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}

	resp, err := cli.filesystemClient.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
	})
	if err != nil {
		commandFatalf("FS_READ_FAILED", true, "", "Failed to read file: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	if _, err := os.Stdout.Write(resp.GetContent()); err != nil {
		commandFatalf("FS_READ_FAILED", false, "", "Failed to write file content: %v", err)
	}
	if !*raw && (len(resp.GetContent()) == 0 || resp.GetContent()[len(resp.GetContent())-1] != '\n') {
		fmt.Println()
	}
}

func handleFilesystemWrite(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs write")
	fileFlag := fs.String("f", "", "Read file content from a local path instead of stdin")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs write </absolute/path> [-f <local-file>] [--json] < input")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}
	if strings.TrimSpace(*fileFlag) == "" && stdinIsTerminal(os.Stdin) {
		commandFatal("INPUT_REQUIRED", "File content must be provided via -f or stdin.", false, "gs fs write </absolute/path> -f <local-file>")
	}

	content, err := readFilesystemWriteInput(strings.TrimSpace(*fileFlag))
	if err != nil {
		commandFatalf("FS_WRITE_FAILED", false, "", "Failed to read input content: %v", err)
	}

	resp, err := cli.filesystemClient.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
		Content:     content,
	})
	if err != nil {
		commandFatalf("FS_WRITE_FAILED", true, "", "Failed to write file: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	fmt.Printf("Wrote %s (%d bytes)\n", resp.GetPath(), resp.GetSize())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemListDirectory(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs ls")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs ls </absolute/path> [--json]")
		return
	}

	workspaceID, dirPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}

	resp, err := cli.filesystemClient.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath,
	})
	if err != nil {
		commandFatalf("FS_LIST_FAILED", true, "", "Failed to list directory: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	fmt.Printf("Path: %s\n", filesystemDisplayPath(resp.GetPath()))
	for _, entry := range resp.GetEntries() {
		line := fmt.Sprintf("- %s", entry.GetPath())
		if entry.GetType() == filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
			line += "/"
		} else {
			line = fmt.Sprintf("%s (%d bytes)", line, entry.GetSize())
		}
		fmt.Println(line)
	}
}

func handleFilesystemMkdir(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs mkdir")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs mkdir </absolute/path> [--json]")
		return
	}

	workspaceID, dirPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}

	resp, err := cli.filesystemClient.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath,
	})
	if err != nil {
		commandFatalf("FS_MKDIR_FAILED", true, "", "Failed to create directory: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	fmt.Printf("Created directory %s\n", resp.GetPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemRemove(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs rm")
	dryRun := fs.Bool("dry-run", false, "Preview the delete without applying it")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs rm </absolute/path> [--dry-run] [--json]")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}
	entry, exists, err := statFilesystemEntry(ctx, cli.filesystemClient, workspaceID, filePath)
	if err != nil {
		commandFatalf("FS_DELETE_FAILED", true, "", "Failed to inspect path before delete: %v", err)
	}
	if !exists {
		out := jsonFilesystemActionOutput{
			Action:  "delete",
			Status:  "no_op",
			Path:    filePath,
			Message: "path already absent",
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Path already absent: %s\n", filePath)
		return
	}
	if *dryRun {
		out := jsonFilesystemActionOutput{
			Action:    "delete",
			Status:    "would_delete",
			DryRun:    true,
			Path:      filePath,
			EntryType: filesystemEntryTypeLabel(entry.GetType()),
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Would delete %s (%s)\n", filePath, filesystemEntryTypeLabel(entry.GetType()))
		return
	}

	resp, err := cli.filesystemClient.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
	})
	if err != nil {
		commandFatalf("FS_DELETE_FAILED", true, "", "Failed to delete path: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonFilesystemActionOutput{
			Action:     "delete",
			Status:     "deleted",
			Path:       resp.GetPath(),
			CommitHash: resp.GetCommitHash(),
		})
		return
	}

	fmt.Printf("Deleted %s\n", resp.GetPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemMove(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs mv")
	dryRun := fs.Bool("dry-run", false, "Preview the move without applying it")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs mv </absolute/source> </absolute/destination> [--dry-run] [--json]")
		return
	}

	workspaceID, sourcePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve source path: %v", err)
	}
	destinationWorkspaceID, destinationPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(1))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve destination path: %v", err)
	}
	sourceWorkspaceID := workspaceID
	if sourceWorkspaceID != destinationWorkspaceID {
		commandFatal("INVALID_ARGUMENT", "Source and destination resolved to different home slices.", false, "")
	}
	if sourcePath == destinationPath {
		out := jsonFilesystemActionOutput{
			Action:          "move",
			Status:          "no_op",
			SourcePath:      sourcePath,
			DestinationPath: destinationPath,
			Message:         "source and destination are identical",
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Source and destination are identical: %s\n", sourcePath)
		return
	}
	sourceEntry, sourceExists, err := statFilesystemEntry(ctx, cli.filesystemClient, sourceWorkspaceID, sourcePath)
	if err != nil {
		commandFatalf("FS_MOVE_FAILED", true, "", "Failed to inspect source path: %v", err)
	}
	_, destinationExists, err := statFilesystemEntry(ctx, cli.filesystemClient, sourceWorkspaceID, destinationPath)
	if err != nil {
		commandFatalf("FS_MOVE_FAILED", true, "", "Failed to inspect destination path: %v", err)
	}
	if !sourceExists {
		if destinationExists {
			out := jsonFilesystemActionOutput{
				Action:          "move",
				Status:          "no_op",
				SourcePath:      sourcePath,
				DestinationPath: destinationPath,
				Message:         "source already absent and destination exists",
			}
			if jsonEnabled {
				writeJSONOutput(out)
				return
			}
			fmt.Printf("Source already absent and destination exists: %s -> %s\n", sourcePath, destinationPath)
			return
		}
		commandFatalf("FS_MOVE_FAILED", false, "", "Source path not found: %s", sourcePath)
	}
	if *dryRun {
		out := jsonFilesystemActionOutput{
			Action:          "move",
			Status:          "would_move",
			DryRun:          true,
			SourcePath:      sourcePath,
			DestinationPath: destinationPath,
			EntryType:       filesystemEntryTypeLabel(sourceEntry.GetType()),
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Would move %s -> %s (%s)\n", sourcePath, destinationPath, filesystemEntryTypeLabel(sourceEntry.GetType()))
		return
	}

	resp, err := cli.filesystemClient.MoveFile(ctx, &filesystemv1.MoveFileRequest{
		WorkspaceId:     sourceWorkspaceID,
		SourcePath:      sourcePath,
		DestinationPath: destinationPath,
	})
	if err != nil {
		commandFatalf("FS_MOVE_FAILED", true, "", "Failed to move path: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonFilesystemActionOutput{
			Action:          "move",
			Status:          "moved",
			SourcePath:      resp.GetSourcePath(),
			DestinationPath: resp.GetDestinationPath(),
			CommitHash:      resp.GetCommitHash(),
		})
		return
	}

	fmt.Printf("Moved %s -> %s\n", resp.GetSourcePath(), resp.GetDestinationPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemCopy(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs cp")
	dryRun := fs.Bool("dry-run", false, "Preview the copy without applying it")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 2 {
		commandUsage("Usage: gs fs cp </absolute/source> </absolute/destination> [--dry-run] [--json]")
		return
	}

	workspaceID, sourcePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve source path: %v", err)
	}
	destinationWorkspaceID, destinationPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(1))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve destination path: %v", err)
	}
	sourceWorkspaceID := workspaceID
	if sourceWorkspaceID != destinationWorkspaceID {
		commandFatal("INVALID_ARGUMENT", "Source and destination resolved to different home slices.", false, "")
	}
	if sourcePath == destinationPath {
		out := jsonFilesystemActionOutput{
			Action:          "copy",
			Status:          "no_op",
			SourcePath:      sourcePath,
			DestinationPath: destinationPath,
			Message:         "source and destination are identical",
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Source and destination are identical: %s\n", sourcePath)
		return
	}
	sourceEntry, sourceExists, err := statFilesystemEntry(ctx, cli.filesystemClient, sourceWorkspaceID, sourcePath)
	if err != nil {
		commandFatalf("FS_COPY_FAILED", true, "", "Failed to inspect source path: %v", err)
	}
	if !sourceExists {
		commandFatalf("FS_COPY_FAILED", false, "", "Source path not found: %s", sourcePath)
	}
	destinationEntry, destinationExists, err := statFilesystemEntry(ctx, cli.filesystemClient, sourceWorkspaceID, destinationPath)
	if err != nil {
		commandFatalf("FS_COPY_FAILED", true, "", "Failed to inspect destination path: %v", err)
	}
	if destinationExists && filesystemEntriesEquivalent(sourceEntry, destinationEntry) {
		out := jsonFilesystemActionOutput{
			Action:          "copy",
			Status:          "no_op",
			SourcePath:      sourcePath,
			DestinationPath: destinationPath,
			Message:         "destination already matches source",
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Destination already matches source: %s -> %s\n", sourcePath, destinationPath)
		return
	}
	if *dryRun {
		out := jsonFilesystemActionOutput{
			Action:          "copy",
			Status:          "would_copy",
			DryRun:          true,
			SourcePath:      sourcePath,
			DestinationPath: destinationPath,
			EntryType:       filesystemEntryTypeLabel(sourceEntry.GetType()),
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Would copy %s -> %s (%s)\n", sourcePath, destinationPath, filesystemEntryTypeLabel(sourceEntry.GetType()))
		return
	}

	resp, err := cli.filesystemClient.CopyFile(ctx, &filesystemv1.CopyFileRequest{
		WorkspaceId:     sourceWorkspaceID,
		SourcePath:      sourcePath,
		DestinationPath: destinationPath,
	})
	if err != nil {
		commandFatalf("FS_COPY_FAILED", true, "", "Failed to copy path: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonFilesystemActionOutput{
			Action:          "copy",
			Status:          "copied",
			SourcePath:      resp.GetSourcePath(),
			DestinationPath: resp.GetDestinationPath(),
			CommitHash:      resp.GetCommitHash(),
			EntryType:       filesystemEntryTypeLabel(sourceEntry.GetType()),
		})
		return
	}

	fmt.Printf("Copied %s -> %s\n", resp.GetSourcePath(), resp.GetDestinationPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemGlob(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs glob")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs glob </absolute/pattern> [--json]")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_GLOB_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}
	pattern, err := parseAbsoluteFilesystemPatternArg(fs.Arg(0), true)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid absolute pattern: %v", err)
	}
	resp, err := cli.filesystemClient.Glob(ctx, &filesystemv1.GlobRequest{
		WorkspaceId: workspaceID,
		Pattern:     pattern,
	})
	if err != nil {
		commandFatalf("FS_GLOB_FAILED", true, "", "Failed to glob workspace: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	for _, path := range resp.GetPaths() {
		fmt.Println(path)
	}
}

func handleFilesystemSearch(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs search")
	glob := fs.String("glob", "", "Restrict search to matching paths")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs search <query> [--glob </absolute/pattern>] [--json]")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_SEARCH_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}
	globPattern, err := parseAbsoluteFilesystemPatternArg(*glob, false)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid absolute pattern: %v", err)
	}
	resp, err := cli.filesystemClient.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: workspaceID,
		Query:       strings.TrimSpace(fs.Arg(0)),
		Glob:        globPattern,
	})
	if err != nil {
		commandFatalf("FS_SEARCH_FAILED", true, "", "Failed to search workspace: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	for _, match := range resp.GetMatches() {
		fmt.Printf("%s:%d:%s\n", match.GetPath(), match.GetLineNumber(), match.GetLine())
	}
}

func handleFilesystemStat(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs stat")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs stat </absolute/path> [--json]")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve absolute path: %v", err)
	}

	resp, err := cli.filesystemClient.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
	})
	if err != nil {
		commandFatalf("FS_STAT_FAILED", true, "", "Failed to stat path: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}
	if !resp.GetExists() {
		fmt.Printf("Not found: %s\n", filePath)
		return
	}

	entry := resp.GetEntry()
	fmt.Printf("Path: %s\n", entry.GetPath())
	fmt.Printf("Type: %s\n", filesystemEntryTypeLabel(entry.GetType()))
	fmt.Printf("Size: %d\n", entry.GetSize())
	if entry.GetHash() != "" {
		fmt.Printf("Hash: %s\n", entry.GetHash())
	}
}

func handleFilesystemSnapshot(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs snapshot")
	message := fs.String("m", "", "Snapshot message")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 0 {
		commandUsage("Usage: gs fs snapshot -m <message> [--json]")
		return
	}
	if strings.TrimSpace(*message) == "" {
		commandFatal("INVALID_ARGUMENT", "Snapshot message is required.", false, "gs fs snapshot -m <message>")
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_SNAPSHOT_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}

	resp, err := cli.filesystemClient.Snapshot(ctx, &filesystemv1.SnapshotRequest{
		WorkspaceId: workspaceID,
		Message:     strings.TrimSpace(*message),
	})
	if err != nil {
		commandFatalf("FS_SNAPSHOT_FAILED", true, "", "Failed to create snapshot: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	snapshot := resp.GetSnapshot()
	fmt.Printf("Snapshot created: %s\n", snapshot.GetSnapshotId())
	fmt.Printf("Message: %s\n", snapshot.GetMessage())
}

func handleFilesystemSnapshots(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs snapshots")
	limit := fs.Int("limit", 10, "Maximum snapshots to return")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 0 {
		commandUsage("Usage: gs fs snapshots [--limit <n>] [--json]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_SNAPSHOTS_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}

	resp, err := cli.filesystemClient.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId: workspaceID,
		Limit:       int32(*limit),
	})
	if err != nil {
		commandFatalf("FS_SNAPSHOTS_FAILED", true, "", "Failed to list snapshots: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	for _, snapshot := range resp.GetSnapshots() {
		fmt.Printf("%s %s %q files=%d\n", snapshot.GetSnapshotId(), formatTimestamp(snapshot.GetCreatedAt()), snapshot.GetMessage(), snapshot.GetFileCount())
	}
}

func handleFilesystemLog(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs log")
	limit := fs.Int("limit", 20, "Maximum commits to return")
	fromSnapshot := fs.String("from", "", "Pagination cursor")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 0 {
		commandUsage("Usage: gs fs log [--limit <n>] [--from <snapshot-id>] [--json]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_LOG_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}

	resp, err := cli.filesystemClient.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId:    workspaceID,
		Limit:          int32(*limit),
		FromSnapshotId: strings.TrimSpace(*fromSnapshot),
	})
	if err != nil {
		commandFatalf("FS_LOG_FAILED", true, "", "Failed to list filesystem history: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	for _, snapshot := range resp.GetSnapshots() {
		fmt.Printf("commit %s\n", snapshot.GetSnapshotId())
		fmt.Printf("Date: %s\n", formatTimestamp(snapshot.GetCreatedAt()))
		if snapshot.GetParentSnapshotId() != "" {
			fmt.Printf("Parent: %s\n", snapshot.GetParentSnapshotId())
		}
		fmt.Printf("\n    %s\n\n", snapshot.GetMessage())
	}
}

func handleFilesystemRestore(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs restore")
	dryRun := fs.Bool("dry-run", false, "Preview the restore without applying it")
	message := fs.String("m", "", "Optional restore message")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs restore <snapshot-id> [--dry-run] [-m <message>] [--json]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_RESTORE_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}
	targetSnapshotID := strings.TrimSpace(fs.Arg(0))
	currentSnapshotID, err := currentFilesystemSnapshotID(ctx, cli.filesystemClient, workspaceID)
	if err != nil {
		commandFatalf("FS_RESTORE_FAILED", true, "", "Failed to resolve current snapshot: %v", err)
	}
	diffResp, err := cli.filesystemClient.Diff(ctx, &filesystemv1.DiffRequest{
		WorkspaceId:    workspaceID,
		FromSnapshotId: targetSnapshotID,
		ToSnapshotId:   currentSnapshotID,
		IncludePatches: false,
	})
	if err != nil {
		commandFatalf("FS_RESTORE_FAILED", true, "", "Failed to preview restore diff: %v", err)
	}
	if filesystemDiffSummaryIsZero(diffResp.GetSummary()) {
		out := jsonFilesystemActionOutput{
			Action:            "restore",
			Status:            "no_op",
			SnapshotID:        targetSnapshotID,
			CurrentSnapshotID: currentSnapshotID,
			Summary:           buildFilesystemDiffSummaryOutput(diffResp.GetSummary()),
			Message:           "workspace already matches target snapshot",
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Workspace already matches snapshot %s\n", targetSnapshotID)
		return
	}
	if *dryRun {
		out := jsonFilesystemActionOutput{
			Action:            "restore",
			Status:            "would_restore",
			DryRun:            true,
			SnapshotID:        targetSnapshotID,
			CurrentSnapshotID: currentSnapshotID,
			Summary:           buildFilesystemDiffSummaryOutput(diffResp.GetSummary()),
		}
		if jsonEnabled {
			writeJSONOutput(out)
			return
		}
		fmt.Printf("Would restore workspace from %s to %s\n", blankOrCurrent(currentSnapshotID), targetSnapshotID)
		if out.Summary != nil {
			fmt.Printf("Files: +%d ~%d -%d\n", out.Summary.FilesAdded, out.Summary.FilesModified, out.Summary.FilesDeleted)
			fmt.Printf("Lines: +%d -%d\n", out.Summary.LinesAdded, out.Summary.LinesRemoved)
		}
		return
	}

	resp, err := cli.filesystemClient.RestoreSnapshot(ctx, &filesystemv1.RestoreSnapshotRequest{
		WorkspaceId: workspaceID,
		SnapshotId:  targetSnapshotID,
		Message:     strings.TrimSpace(*message),
	})
	if err != nil {
		commandFatalf("FS_RESTORE_FAILED", true, "", "Failed to restore snapshot: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(jsonFilesystemActionOutput{
			Action:            "restore",
			Status:            "restored",
			SnapshotID:        resp.GetRestoredSnapshotId(),
			CurrentSnapshotID: currentSnapshotID,
		})
		return
	}

	fmt.Printf("Restored to %s\n", resp.GetRestoredSnapshotId())
	if snapshot := resp.GetSnapshot(); snapshot != nil {
		fmt.Printf("New head snapshot: %s\n", snapshot.GetSnapshotId())
	}
}

func handleFilesystemDiff(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs diff")
	toSnapshot := fs.String("to", "", "Optional target snapshot ID")
	includePatches := fs.Bool("patch", true, "Include unified patches")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 1 {
		commandUsage("Usage: gs fs diff [snapshot-id] [--to <snapshot-id>] [--patch=false] [--json]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_DIFF_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}

	fromSnapshotID := ""
	if fs.NArg() == 1 {
		fromSnapshotID = strings.TrimSpace(fs.Arg(0))
	} else {
		listResp, err := cli.filesystemClient.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
			WorkspaceId: workspaceID,
			Limit:       1,
		})
		if err != nil {
			commandFatalf("FS_DIFF_FAILED", true, "", "Failed to resolve latest snapshot for diff: %v", err)
		}
		if len(listResp.GetSnapshots()) == 0 {
			commandFatal("FS_DIFF_FAILED", "No snapshots found; provide a snapshot ID explicitly or create one first.", false, "gs fs snapshot -m <message>")
		}
		fromSnapshotID = listResp.GetSnapshots()[0].GetSnapshotId()
	}

	resp, err := cli.filesystemClient.Diff(ctx, &filesystemv1.DiffRequest{
		WorkspaceId:    workspaceID,
		FromSnapshotId: fromSnapshotID,
		ToSnapshotId:   strings.TrimSpace(*toSnapshot),
		IncludePatches: *includePatches,
	})
	if err != nil {
		commandFatalf("FS_DIFF_FAILED", true, "", "Failed to diff workspace: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	summary := resp.GetSummary()
	fmt.Printf("Diff %s -> %s\n", resp.GetFromSnapshotId(), blankOrCurrent(resp.GetToSnapshotId()))
	fmt.Printf("Files: +%d ~%d -%d\n", summary.GetFilesAdded(), summary.GetFilesModified(), summary.GetFilesDeleted())
	fmt.Printf("Lines: +%d -%d\n", summary.GetLinesAdded(), summary.GetLinesDeleted())
	for _, file := range resp.GetFiles() {
		fmt.Printf("\n%s %s (+%d -%d)\n", filesystemDiffTypeLabel(file.GetChangeType()), file.GetPath(), file.GetLinesAdded(), file.GetLinesDeleted())
		if file.GetPatch() != "" {
			fmt.Println(file.GetPatch())
		}
	}
}

func handleFilesystemShow(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("fs show")
	patches := fs.Bool("patches", true, "Include unified patches")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 1 {
		commandUsage("Usage: gs fs show <commit-hash> [--patches=false] [--json]")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("FS_SHOW_FAILED", true, "", "Failed to resolve home workspace: %v", err)
	}

	commitHash := strings.TrimSpace(fs.Arg(0))
	resp, err := cli.fileClient.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
		CommitHash:     commitHash,
		IncludePatches: *patches,
	})
	if err != nil {
		commandFatalf("FS_SHOW_FAILED", true, "", "Failed to fetch filesystem commit changes: %v", err)
	}

	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
	fmt.Printf("Added: %d Modified: %d Deleted: %d Renamed: %d\n", resp.GetFilesAdded(), resp.GetFilesModified(), resp.GetFilesDeleted(), resp.GetFilesRenamed())
	fmt.Printf("Changes: %d\n", len(resp.GetChanges()))
	for _, change := range resp.GetChanges() {
		remapFilesystemHomeChange(change, workspaceID)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}
	for _, change := range resp.GetChanges() {
		printFileChange(change, *patches)
	}
}

func resolveFilesystemHomeIdentity(ctx context.Context, cli *CLI, authConfig cliAuth) (string, string, error) {
	username := strings.TrimSpace(authConfig.Username)
	if username == "" {
		resp, err := cli.accountClient.GetMe(ctx, &accountv1.GetMeRequest{})
		if err != nil {
			return "", "", fmt.Errorf("resolve current user for home slice: %w", err)
		}
		username = strings.TrimSpace(resp.GetUsername())
	}
	if username == "" {
		return "", "", errors.New("current auth did not resolve a username")
	}
	return homeslice.IDForUsername(username), username, nil
}

func resolveFilesystemHomeWorkspace(ctx context.Context, cli *CLI, authConfig cliAuth) (string, error) {
	workspaceID, _, err := resolveFilesystemHomeIdentity(ctx, cli, authConfig)
	return workspaceID, err
}

func resolveFilesystemAbsolutePath(ctx context.Context, cli *CLI, authConfig cliAuth, raw string) (string, string, error) {
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		return "", "", err
	}
	remotePath, err := parseAbsoluteFilesystemPathArg(raw, true)
	if err != nil {
		return "", "", err
	}
	return workspaceID, remotePath, nil
}

func statFilesystemEntry(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID, remotePath string,
) (*filesystemv1.WorkspaceEntry, bool, error) {
	resp, err := client.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: workspaceID,
		Path:        remotePath,
	})
	if err != nil {
		return nil, false, err
	}
	return resp.GetEntry(), resp.GetExists(), nil
}

func filesystemEntriesEquivalent(a, b *filesystemv1.WorkspaceEntry) bool {
	if a == nil || b == nil {
		return false
	}
	if a.GetType() != b.GetType() {
		return false
	}
	if a.GetType() == filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
		return false
	}
	if strings.TrimSpace(a.GetHash()) != "" && strings.TrimSpace(b.GetHash()) != "" {
		return a.GetHash() == b.GetHash()
	}
	return a.GetSize() == b.GetSize()
}

func currentFilesystemSnapshotID(
	ctx context.Context,
	client filesystemv1.FilesystemServiceClient,
	workspaceID string,
) (string, error) {
	resp, err := client.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId: workspaceID,
		Limit:       1,
	})
	if err != nil {
		return "", err
	}
	if len(resp.GetSnapshots()) == 0 {
		return "", nil
	}
	return strings.TrimSpace(resp.GetSnapshots()[0].GetSnapshotId()), nil
}

func buildFilesystemDiffSummaryOutput(summary *filesystemv1.DiffSummary) *jsonChangesetDiffSummary {
	if summary == nil {
		return nil
	}
	return &jsonChangesetDiffSummary{
		FilesAdded:    summary.GetFilesAdded(),
		FilesModified: summary.GetFilesModified(),
		FilesDeleted:  summary.GetFilesDeleted(),
		LinesAdded:    int64(summary.GetLinesAdded()),
		LinesRemoved:  int64(summary.GetLinesDeleted()),
	}
}

func filesystemDiffSummaryIsZero(summary *filesystemv1.DiffSummary) bool {
	if summary == nil {
		return true
	}
	return summary.GetFilesAdded() == 0 &&
		summary.GetFilesModified() == 0 &&
		summary.GetFilesDeleted() == 0 &&
		summary.GetLinesAdded() == 0 &&
		summary.GetLinesDeleted() == 0
}

func parseAbsoluteFilesystemPathArg(value string, required bool) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		if required {
			return "", fmt.Errorf("absolute remote path is required")
		}
		return "", nil
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("absolute remote path is required")
	}
	cleaned := path.Clean(raw)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned, nil
}

func parseAbsoluteFilesystemPatternArg(value string, required bool) (string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		if required {
			return "", fmt.Errorf("absolute remote pattern is required")
		}
		return "", nil
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("absolute remote pattern is required")
	}
	return raw, nil
}

func readFilesystemWriteInput(localPath string) ([]byte, error) {
	if localPath != "" {
		return os.ReadFile(filepath.Clean(localPath))
	}
	return io.ReadAll(os.Stdin)
}

func filesystemDisplayPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "/"
	}
	return path
}

func filesystemEntryTypeLabel(entryType filesystemv1.EntryType) string {
	switch entryType {
	case filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY:
		return "directory"
	case filesystemv1.EntryType_ENTRY_TYPE_FILE:
		return "file"
	default:
		return "unspecified"
	}
}

func filesystemDiffTypeLabel(changeType filesystemv1.DiffChangeType) string {
	switch changeType {
	case filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_ADD:
		return "ADD"
	case filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_MODIFY:
		return "MODIFY"
	case filesystemv1.DiffChangeType_DIFF_CHANGE_TYPE_DELETE:
		return "DELETE"
	default:
		return "CHANGE"
	}
}

func remapFilesystemHomeChange(change *filev1.FileChangeRecord, workspaceID string) {
	if change == nil || strings.TrimSpace(change.GetSliceId()) != strings.TrimSpace(workspaceID) {
		return
	}
	change.Path = homeslice.VisiblePathForStored(change.GetPath())
	if change.GetOldPath() != "" {
		change.OldPath = homeslice.VisiblePathForStored(change.GetOldPath())
	}
}

func blankOrCurrent(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(current)"
	}
	return value
}

func parseFlagSetInterspersed(fs *flag.FlagSet, args []string) {
	if err := fs.Parse(reorderInterspersedArgs(fs, args)); err != nil {
		commandFatal("INVALID_ARGUMENT", err.Error(), false, "")
	}
}

func reorderInterspersedArgs(fs *flag.FlagSet, args []string) []string {
	type boolFlag interface {
		IsBoolFlag() bool
	}

	flagTypes := make(map[string]bool)
	fs.VisitAll(func(f *flag.Flag) {
		isBool := false
		if bf, ok := f.Value.(boolFlag); ok && bf.IsBoolFlag() {
			isBool = true
		}
		flagTypes["-"+f.Name] = isBool
		flagTypes["--"+f.Name] = isBool
	})

	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			positionals = append(positionals, args[index:]...)
			break
		}

		flagName := arg
		if cutFlag, _, ok := strings.Cut(arg, "="); ok {
			flagName = cutFlag
		}
		isBool, ok := flagTypes[flagName]
		if !ok {
			if strings.HasPrefix(arg, "-") {
				flagArgs = append(flagArgs, arg)
				continue
			}
			positionals = append(positionals, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") || isBool {
			continue
		}
		if index+1 < len(args) {
			index++
			flagArgs = append(flagArgs, args[index])
		}
	}

	return append(flagArgs, positionals...)
}
