package gscli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	filev1 "github.com/niczy/gitslice/proto/file"
)

func handleFileCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printFileHelp()
		return
	}

	switch args[0] {
	case "ls":
		handleFileList(ctx, cli, args[1:])
	case "cat":
		handleFileCat(ctx, cli, args[1:])
	case "history":
		handleFileHistory(ctx, cli, args[1:])
	case "dir-history":
		handleDirectoryHistory(ctx, cli, args[1:])
	case "commit-changes":
		handleCommitChanges(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown file command: %s", args[0]), false, "gs file --help")
	}
}

func handleFileList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("file ls")
	sliceFlag := fs.String("slice", "", "Slice ID")
	commitFlag := fs.String("commit", "", "Commit hash")
	limit := fs.Int("limit", 200, "Maximum entries")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 1 {
		commandUsage("Usage: gs file ls [path] [--slice <slice-id>] [--commit <hash>] [--limit <n>] [--json]")
		return
	}
	path := ""
	if fs.NArg() == 1 {
		path = strings.TrimSpace(fs.Arg(0))
	}

	sliceID, err := resolveFileSliceID(*sliceFlag)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve slice ID: %v", err)
	}

	req := &filev1.ListEntriesRequest{
		Path:  path,
		Limit: int32(*limit),
	}
	applyListEntriesVersion(req, sliceID, strings.TrimSpace(*commitFlag))

	resp, err := cli.fileClient.ListEntries(ctx, req)
	if err != nil {
		commandFatalf("FILE_LIST_FAILED", true, "", "Failed to list entries: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	fmt.Printf("Slice: %s\n", resp.GetSliceId())
	fmt.Printf("Path: %s\n", resp.GetPath())
	fmt.Printf("Entries: %d\n", len(resp.GetEntries()))
	for _, entry := range resp.GetEntries() {
		label := "FILE"
		if entry.GetType() == filev1.EntryType_ENTRY_TYPE_DIRECTORY {
			label = "DIR "
		}
		line := fmt.Sprintf("- [%s] %s", label, entry.GetPath())
		if entry.GetType() == filev1.EntryType_ENTRY_TYPE_FILE {
			line = fmt.Sprintf("%s (%d bytes)", line, entry.GetSize())
		}
		if entry.GetHash() != "" {
			line = fmt.Sprintf("%s hash=%s", line, entry.GetHash())
		}
		fmt.Println(line)
	}
	if resp.GetTruncated() {
		fmt.Println("Listing truncated; use --limit to request more entries.")
	}
}

func handleFileCat(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("file cat")
	sliceFlag := fs.String("slice", "", "Slice ID")
	commitFlag := fs.String("commit", "", "Commit hash")
	raw := fs.Bool("raw", false, "Write file bytes only")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 1 {
		commandUsage("Usage: gs file cat <path> [--slice <slice-id>] [--commit <hash>] [--raw] [--json]")
		return
	}
	path := strings.TrimSpace(fs.Arg(0))
	if path == "" {
		commandFatal("INVALID_ARGUMENT", "File path is required.", false, "")
	}

	sliceID, err := resolveFileSliceID(*sliceFlag)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve slice ID: %v", err)
	}

	req := &filev1.GetFileRequest{Path: path}
	applyGetFileVersion(req, sliceID, strings.TrimSpace(*commitFlag))

	resp, err := cli.fileClient.GetFile(ctx, req)
	if err != nil {
		commandFatalf("FILE_CAT_FAILED", true, "", "Failed to fetch file: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	file := resp.GetFile()
	if file == nil {
		commandFatal("FILE_CAT_FAILED", "File response was empty.", false, "")
	}

	if *raw {
		if _, err := os.Stdout.Write(file.GetContent()); err != nil {
			commandFatalf("FILE_CAT_FAILED", false, "", "Failed writing file bytes: %v", err)
		}
		return
	}

	fmt.Printf("Path: %s\n", file.GetPath())
	fmt.Printf("Size: %d\n", file.GetSize())
	fmt.Printf("Hash: %s\n", file.GetHash())
	content := file.GetContent()
	if len(content) == 0 {
		fmt.Println("\n(empty file)")
		return
	}
	if !utf8.Valid(content) {
		fmt.Println("\n(binary file; use --raw to print raw bytes)")
		return
	}

	fmt.Println()
	fmt.Print(string(content))
	if !strings.HasSuffix(string(content), "\n") {
		fmt.Println()
	}
}

func handleFileHistory(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("file history")
	sliceFlag := fs.String("slice", "", "Slice ID")
	limit := fs.Int("limit", 50, "Maximum results")
	fromCommit := fs.String("from-commit", "", "Pagination cursor")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 1 {
		commandUsage("Usage: gs file history <path> [--slice <slice-id>] [--limit <n>] [--from-commit <hash>] [--json]")
		return
	}
	path := strings.TrimSpace(fs.Arg(0))
	if path == "" {
		commandFatal("INVALID_ARGUMENT", "File path is required.", false, "")
	}

	sliceID, err := resolveFileSliceID(*sliceFlag)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve slice ID: %v", err)
	}

	req := &filev1.GetFileHistoryRequest{
		Path:       path,
		SliceId:    sliceID,
		Limit:      int32(*limit),
		FromCommit: strings.TrimSpace(*fromCommit),
	}
	resp, err := cli.fileClient.GetFileHistory(ctx, req)
	if err != nil {
		commandFatalf("FILE_HISTORY_FAILED", true, "", "Failed to fetch file history: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	fmt.Printf("File history for %s (%d change(s))\n", path, len(resp.GetChanges()))
	for _, change := range resp.GetChanges() {
		printFileChange(change, false)
	}
	if resp.GetHasMore() {
		fmt.Printf("More changes available. Next cursor: %s\n", resp.GetNextCommit())
	}
}

func handleDirectoryHistory(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("file dir-history")
	sliceFlag := fs.String("slice", "", "Slice ID")
	limit := fs.Int("limit", 100, "Maximum results")
	fromCommit := fs.String("from-commit", "", "Pagination cursor")
	changeTypes := fs.String("type", "", "Comma-separated filter: add,modify,delete,rename")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() > 1 {
		commandUsage("Usage: gs file dir-history [path] [--slice <slice-id>] [--limit <n>] [--from-commit <hash>] [--type add,modify,...] [--json]")
		return
	}
	path := ""
	if fs.NArg() == 1 {
		path = strings.TrimSpace(fs.Arg(0))
	}

	sliceID, err := resolveFileSliceID(*sliceFlag)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Failed to resolve slice ID: %v", err)
	}

	types, err := parseChangeTypesCSV(*changeTypes)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid --type value: %v", err)
	}

	req := &filev1.GetDirectoryHistoryRequest{
		Path:        path,
		SliceId:     sliceID,
		Limit:       int32(*limit),
		FromCommit:  strings.TrimSpace(*fromCommit),
		ChangeTypes: types,
	}
	resp, err := cli.fileClient.GetDirectoryHistory(ctx, req)
	if err != nil {
		commandFatalf("DIRECTORY_HISTORY_FAILED", true, "", "Failed to fetch directory history: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	summary := resp.GetSummary()
	if summary != nil {
		fmt.Printf("Directory: %s\n", summary.GetPath())
		fmt.Printf("Total changes: %d\n", summary.GetTotalChanges())
		fmt.Printf("Files changed: %d\n", summary.GetFilesChanged())
		if len(summary.GetChangesByType()) > 0 {
			keys := make([]string, 0, len(summary.GetChangesByType()))
			for k := range summary.GetChangesByType() {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Println("Changes by type:")
			for _, k := range keys {
				fmt.Printf("- %s: %d\n", k, summary.GetChangesByType()[k])
			}
		}
	}

	fmt.Printf("Changes returned: %d\n", len(resp.GetChanges()))
	for _, change := range resp.GetChanges() {
		printFileChange(change, false)
	}
	if resp.GetHasMore() {
		fmt.Printf("More changes available. Next cursor: %s\n", resp.GetNextCommit())
	}
}

func handleCommitChanges(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("file commit-changes")
	patches := fs.Bool("patches", false, "Include unified patches")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput

	if fs.NArg() != 1 {
		commandUsage("Usage: gs file commit-changes <commit-hash> [--patches] [--json]")
		return
	}
	commitHash := strings.TrimSpace(fs.Arg(0))
	if commitHash == "" {
		commandFatal("INVALID_ARGUMENT", "Commit hash is required.", false, "")
	}

	req := &filev1.GetCommitChangesRequest{
		CommitHash:     commitHash,
		IncludePatches: *patches,
	}
	resp, err := cli.fileClient.GetCommitChanges(ctx, req)
	if err != nil {
		commandFatalf("COMMIT_CHANGES_FAILED", true, "", "Failed to fetch commit changes: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}

	fmt.Printf("Commit: %s\n", resp.GetCommitHash())
	fmt.Printf("Added: %d Modified: %d Deleted: %d Renamed: %d\n", resp.GetFilesAdded(), resp.GetFilesModified(), resp.GetFilesDeleted(), resp.GetFilesRenamed())
	fmt.Printf("Changes: %d\n", len(resp.GetChanges()))
	for _, change := range resp.GetChanges() {
		printFileChange(change, *patches)
	}
}

func printFileChange(change *filev1.FileChangeRecord, includePatch bool) {
	if change == nil {
		return
	}
	when := "-"
	if change.GetTimestamp() > 0 {
		when = formatTimestamp(change.GetTimestamp())
	}
	line := fmt.Sprintf("- %s %s %s (%s)", change.GetCommitHash(), shortChangeType(change.GetChangeType()), change.GetPath(), when)
	if change.GetChangeType() == filev1.ChangeType_CHANGE_TYPE_RENAME && change.GetOldPath() != "" {
		line = fmt.Sprintf("%s from %s", line, change.GetOldPath())
	}
	if change.GetAuthor() != "" {
		line = fmt.Sprintf("%s author=%s", line, change.GetAuthor())
	}
	if change.GetMessage() != "" {
		line = fmt.Sprintf("%s msg=%q", line, change.GetMessage())
	}
	fmt.Println(line)
	if change.GetLinesAdded() != 0 || change.GetLinesDeleted() != 0 {
		fmt.Printf("  lines +%d -%d\n", change.GetLinesAdded(), change.GetLinesDeleted())
	}
	if includePatch && change.GetPatch() != "" {
		fmt.Println("  patch:")
		for _, patchLine := range strings.Split(change.GetPatch(), "\n") {
			fmt.Printf("    %s\n", patchLine)
		}
	}
}

func shortChangeType(changeType filev1.ChangeType) string {
	switch changeType {
	case filev1.ChangeType_CHANGE_TYPE_ADD:
		return "ADD"
	case filev1.ChangeType_CHANGE_TYPE_MODIFY:
		return "MODIFY"
	case filev1.ChangeType_CHANGE_TYPE_DELETE:
		return "DELETE"
	case filev1.ChangeType_CHANGE_TYPE_RENAME:
		return "RENAME"
	default:
		return "UNKNOWN"
	}
}

func resolveFileSliceID(explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return normalizeSliceID(explicit)
	}
	if _, err := os.Stat(sliceConfigPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return sliceIDFromConfig()
}

func applyListEntriesVersion(req *filev1.ListEntriesRequest, sliceID, commitHash string) {
	if req == nil {
		return
	}
	commitHash = strings.TrimSpace(commitHash)
	sliceID = strings.TrimSpace(sliceID)
	if sliceID != "" {
		req.Version = &filev1.ListEntriesRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID, SliceHash: commitHash}}
		return
	}
	if commitHash != "" {
		req.Version = &filev1.ListEntriesRequest_CommitHash{CommitHash: commitHash}
	}
}

func applyGetFileVersion(req *filev1.GetFileRequest, sliceID, commitHash string) {
	if req == nil {
		return
	}
	commitHash = strings.TrimSpace(commitHash)
	sliceID = strings.TrimSpace(sliceID)
	if sliceID != "" {
		req.Version = &filev1.GetFileRequest_SliceVersion{SliceVersion: &filev1.SliceVersion{SliceId: sliceID, SliceHash: commitHash}}
		return
	}
	if commitHash != "" {
		req.Version = &filev1.GetFileRequest_CommitHash{CommitHash: commitHash}
	}
}

func parseChangeTypesCSV(raw string) ([]filev1.ChangeType, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	result := make([]filev1.ChangeType, 0, len(parts))
	seen := make(map[filev1.ChangeType]struct{}, len(parts))
	for _, part := range parts {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}

		var t filev1.ChangeType
		switch name {
		case "add":
			t = filev1.ChangeType_CHANGE_TYPE_ADD
		case "modify":
			t = filev1.ChangeType_CHANGE_TYPE_MODIFY
		case "delete":
			t = filev1.ChangeType_CHANGE_TYPE_DELETE
		case "rename":
			t = filev1.ChangeType_CHANGE_TYPE_RENAME
		default:
			return nil, fmt.Errorf("unsupported change type %q", part)
		}

		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		result = append(result, t)
	}
	return result, nil
}
