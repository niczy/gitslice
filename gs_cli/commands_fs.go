package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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
	case "ls":
		handleFilesystemListDirectory(ctx, cli, authConfig, args[1:])
	case "mkdir":
		handleFilesystemMkdir(ctx, cli, authConfig, args[1:])
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
	case "upload":
		handleFilesystemUpload(ctx, cli, authConfig, args[1:])
	case "download":
		handleFilesystemDownload(ctx, cli, authConfig, args[1:])
	default:
		log.Printf("Unknown fs command: %s", args[0])
		printFilesystemHelp()
	}
}

func handleFilesystemCat(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) != 1 {
		log.Println("Usage: gs fs cat </absolute/path>")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[0])
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
	})
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	if _, err := os.Stdout.Write(resp.GetContent()); err != nil {
		log.Fatalf("Failed to write file content: %v", err)
	}
}

func handleFilesystemWrite(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs write", flag.ExitOnError)
	fileFlag := fs.String("f", "", "Read file content from a local path instead of stdin")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 1 {
		log.Println("Usage: gs fs write </absolute/path> [-f <local-file>] < input")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, fs.Arg(0))
	if err != nil {
		log.Fatal(err)
	}

	content, err := readFilesystemWriteInput(strings.TrimSpace(*fileFlag))
	if err != nil {
		log.Fatalf("Failed to read input content: %v", err)
	}

	resp, err := cli.filesystemClient.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
		Content:     content,
	})
	if err != nil {
		log.Fatalf("Failed to write file: %v", err)
	}

	fmt.Printf("Wrote %s (%d bytes)\n", resp.GetPath(), resp.GetSize())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemListDirectory(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) != 1 {
		log.Println("Usage: gs fs ls </absolute/path>")
		return
	}

	workspaceID, dirPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[0])
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.ListDirectory(ctx, &filesystemv1.ListDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath,
	})
	if err != nil {
		log.Fatalf("Failed to list directory: %v", err)
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
	if len(args) != 1 {
		log.Println("Usage: gs fs mkdir </absolute/path>")
		return
	}

	workspaceID, dirPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[0])
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.MakeDirectory(ctx, &filesystemv1.MakeDirectoryRequest{
		WorkspaceId: workspaceID,
		Path:        dirPath,
	})
	if err != nil {
		log.Fatalf("Failed to create directory: %v", err)
	}

	fmt.Printf("Created directory %s\n", resp.GetPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemRemove(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) != 1 {
		log.Println("Usage: gs fs rm </absolute/path>")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[0])
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.DeleteFile(ctx, &filesystemv1.DeleteFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
	})
	if err != nil {
		log.Fatalf("Failed to delete path: %v", err)
	}

	fmt.Printf("Deleted %s\n", resp.GetPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemMove(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) != 2 {
		log.Println("Usage: gs fs mv </absolute/source> </absolute/destination>")
		return
	}

	workspaceID, sourcePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[0])
	if err != nil {
		log.Fatal(err)
	}
	destinationWorkspaceID, destinationPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[1])
	if err != nil {
		log.Fatal(err)
	}
	sourceWorkspaceID := workspaceID
	if sourceWorkspaceID != destinationWorkspaceID {
		log.Fatal("source and destination resolved to different home slices")
	}

	resp, err := cli.filesystemClient.MoveFile(ctx, &filesystemv1.MoveFileRequest{
		WorkspaceId:     sourceWorkspaceID,
		SourcePath:      sourcePath,
		DestinationPath: destinationPath,
	})
	if err != nil {
		log.Fatalf("Failed to move path: %v", err)
	}

	fmt.Printf("Moved %s -> %s\n", resp.GetSourcePath(), resp.GetDestinationPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemCopy(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) != 2 {
		log.Println("Usage: gs fs cp </absolute/source> </absolute/destination>")
		return
	}

	workspaceID, sourcePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[0])
	if err != nil {
		log.Fatal(err)
	}
	destinationWorkspaceID, destinationPath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[1])
	if err != nil {
		log.Fatal(err)
	}
	sourceWorkspaceID := workspaceID
	if sourceWorkspaceID != destinationWorkspaceID {
		log.Fatal("source and destination resolved to different home slices")
	}

	resp, err := cli.filesystemClient.CopyFile(ctx, &filesystemv1.CopyFileRequest{
		WorkspaceId:     sourceWorkspaceID,
		SourcePath:      sourcePath,
		DestinationPath: destinationPath,
	})
	if err != nil {
		log.Fatalf("Failed to copy path: %v", err)
	}

	fmt.Printf("Copied %s -> %s\n", resp.GetSourcePath(), resp.GetDestinationPath())
	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
}

func handleFilesystemGlob(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) != 1 {
		log.Println("Usage: gs fs glob </absolute/pattern>")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}
	pattern, err := parseAbsoluteFilesystemPatternArg(args[0], true)
	if err != nil {
		log.Fatal(err)
	}
	resp, err := cli.filesystemClient.Glob(ctx, &filesystemv1.GlobRequest{
		WorkspaceId: workspaceID,
		Pattern:     pattern,
	})
	if err != nil {
		log.Fatalf("Failed to glob workspace: %v", err)
	}

	for _, path := range resp.GetPaths() {
		fmt.Println(path)
	}
}

func handleFilesystemSearch(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs search", flag.ExitOnError)
	glob := fs.String("glob", "", "Restrict search to matching paths")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 1 {
		log.Println("Usage: gs fs search <query> [--glob </absolute/pattern>]")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}
	globPattern, err := parseAbsoluteFilesystemPatternArg(*glob, false)
	if err != nil {
		log.Fatal(err)
	}
	resp, err := cli.filesystemClient.Search(ctx, &filesystemv1.SearchRequest{
		WorkspaceId: workspaceID,
		Query:       strings.TrimSpace(fs.Arg(0)),
		Glob:        globPattern,
	})
	if err != nil {
		log.Fatalf("Failed to search workspace: %v", err)
	}

	for _, match := range resp.GetMatches() {
		fmt.Printf("%s:%d:%s\n", match.GetPath(), match.GetLineNumber(), match.GetLine())
	}
}

func handleFilesystemStat(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) != 1 {
		log.Println("Usage: gs fs stat </absolute/path>")
		return
	}

	workspaceID, filePath, err := resolveFilesystemAbsolutePath(ctx, cli, authConfig, args[0])
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
	})
	if err != nil {
		log.Fatalf("Failed to stat path: %v", err)
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
	fs := flag.NewFlagSet("fs snapshot", flag.ExitOnError)
	message := fs.String("m", "", "Snapshot message")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 0 {
		log.Println("Usage: gs fs snapshot -m <message>")
		return
	}
	if strings.TrimSpace(*message) == "" {
		log.Println("Snapshot message is required")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.Snapshot(ctx, &filesystemv1.SnapshotRequest{
		WorkspaceId: workspaceID,
		Message:     strings.TrimSpace(*message),
	})
	if err != nil {
		log.Fatalf("Failed to create snapshot: %v", err)
	}

	snapshot := resp.GetSnapshot()
	fmt.Printf("Snapshot created: %s\n", snapshot.GetSnapshotId())
	fmt.Printf("Message: %s\n", snapshot.GetMessage())
}

func handleFilesystemSnapshots(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs snapshots", flag.ExitOnError)
	limit := fs.Int("limit", 10, "Maximum snapshots to return")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 0 {
		log.Println("Usage: gs fs snapshots [--limit <n>]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId: workspaceID,
		Limit:       int32(*limit),
	})
	if err != nil {
		log.Fatalf("Failed to list snapshots: %v", err)
	}

	for _, snapshot := range resp.GetSnapshots() {
		fmt.Printf("%s %s %q files=%d\n", snapshot.GetSnapshotId(), formatTimestamp(snapshot.GetCreatedAt()), snapshot.GetMessage(), snapshot.GetFileCount())
	}
}

func handleFilesystemLog(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs log", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Maximum commits to return")
	fromSnapshot := fs.String("from", "", "Pagination cursor")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 0 {
		log.Println("Usage: gs fs log [--limit <n>] [--from <snapshot-id>]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.ListSnapshots(ctx, &filesystemv1.ListSnapshotsRequest{
		WorkspaceId:    workspaceID,
		Limit:          int32(*limit),
		FromSnapshotId: strings.TrimSpace(*fromSnapshot),
	})
	if err != nil {
		log.Fatalf("Failed to list filesystem history: %v", err)
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
	fs := flag.NewFlagSet("fs restore", flag.ExitOnError)
	message := fs.String("m", "", "Optional restore message")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 1 {
		log.Println("Usage: gs fs restore <snapshot-id> [-m <message>]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cli.filesystemClient.RestoreSnapshot(ctx, &filesystemv1.RestoreSnapshotRequest{
		WorkspaceId: workspaceID,
		SnapshotId:  strings.TrimSpace(fs.Arg(0)),
		Message:     strings.TrimSpace(*message),
	})
	if err != nil {
		log.Fatalf("Failed to restore snapshot: %v", err)
	}

	fmt.Printf("Restored to %s\n", resp.GetRestoredSnapshotId())
	if snapshot := resp.GetSnapshot(); snapshot != nil {
		fmt.Printf("New head snapshot: %s\n", snapshot.GetSnapshotId())
	}
}

func handleFilesystemDiff(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	fs := flag.NewFlagSet("fs diff", flag.ExitOnError)
	toSnapshot := fs.String("to", "", "Optional target snapshot ID")
	includePatches := fs.Bool("patch", true, "Include unified patches")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() > 1 {
		log.Println("Usage: gs fs diff [snapshot-id] [--to <snapshot-id>] [--patch=false]")
		return
	}
	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
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
			log.Fatalf("Failed to resolve latest snapshot for diff: %v", err)
		}
		if len(listResp.GetSnapshots()) == 0 {
			log.Fatal("No snapshots found; provide a snapshot ID explicitly or create one first")
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
		log.Fatalf("Failed to diff workspace: %v", err)
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
	fs := flag.NewFlagSet("fs show", flag.ExitOnError)
	patches := fs.Bool("patches", true, "Include unified patches")
	parseFlagSetInterspersed(fs, args)

	if fs.NArg() != 1 {
		log.Println("Usage: gs fs show <commit-hash> [--patches=false]")
		return
	}

	workspaceID, err := resolveFilesystemHomeWorkspace(ctx, cli, authConfig)
	if err != nil {
		log.Fatal(err)
	}

	commitHash := strings.TrimSpace(fs.Arg(0))
	resp, err := cli.fileClient.GetCommitChanges(ctx, &filev1.GetCommitChangesRequest{
		CommitHash:     commitHash,
		IncludePatches: *patches,
	})
	if err != nil {
		log.Fatalf("Failed to fetch filesystem commit changes: %v", err)
	}

	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
	fmt.Printf("Added: %d Modified: %d Deleted: %d Renamed: %d\n", resp.GetFilesAdded(), resp.GetFilesModified(), resp.GetFilesDeleted(), resp.GetFilesRenamed())
	fmt.Printf("Changes: %d\n", len(resp.GetChanges()))
	for _, change := range resp.GetChanges() {
		remapFilesystemHomeChange(change, workspaceID)
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
		log.Fatal(err)
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
