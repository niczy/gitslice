package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	adminv1 "github.com/niczy/gitslice/proto/admin"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	sliceServerAddr = flag.String("slice-addr", "localhost:50051", "Slice service address")
	adminServerAddr = flag.String("admin-addr", "localhost:50052", "Admin service address")
)

type CLI struct {
	sliceConn   *grpc.ClientConn
	adminConn   *grpc.ClientConn
	sliceClient slicev1.SliceServiceClient
	adminClient adminv1.AdminServiceClient
}

// stringFlag tracks whether a string flag was explicitly set
// so we can distinguish between a zero value and an omitted flag.
type stringFlag struct {
	value string
	set   bool
}

func (f *stringFlag) String() string {
	return f.value
}

func (f *stringFlag) Set(v string) error {
	f.value = v
	f.set = true
	return nil
}

func main() {
	flag.Parse()

	cli, err := NewCLI(*sliceServerAddr, *adminServerAddr)
	if err != nil {
		log.Fatalf("Failed to initialize CLI: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	args := flag.Args()
	if len(args) < 1 {
		printHelp()
		return
	}

	switch args[0] {
	case "slice":
		handleSliceCommand(ctx, cli, args[1:])
	case "changeset":
		handleChangesetCommand(ctx, cli, args[1:])
	case "status":
		handleStatus(ctx, cli)
	case "init":
		handleInit(ctx, cli, args[1:])
	case "log":
		handleLog(ctx, cli, args[1:])
	case "conflict":
		handleConflictCommand(ctx, cli, args[1:])
	default:
		log.Printf("Unknown command: %s", args[0])
		printHelp()
	}
}

func NewCLI(sliceAddr, adminAddr string) (*CLI, error) {
	sliceConn, err := grpc.Dial(sliceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to slice service: %w", err)
	}

	adminConn, err := grpc.Dial(adminAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		sliceConn.Close()
		return nil, fmt.Errorf("failed to connect to admin service: %w", err)
	}

	return &CLI{
		sliceConn:   sliceConn,
		adminConn:   adminConn,
		sliceClient: slicev1.NewSliceServiceClient(sliceConn),
		adminClient: adminv1.NewAdminServiceClient(adminConn),
	}, nil
}

func (c *CLI) Close() {
	if c.sliceConn != nil {
		c.sliceConn.Close()
	}
	if c.adminConn != nil {
		c.adminConn.Close()
	}
}

func handleSliceCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printSliceHelp()
		return
	}

	switch args[0] {
	case "create":
		handleSliceCreate(ctx, cli, args[1:])
	case "list":
		handleSliceList(ctx, cli, args[1:])
	case "info":
		handleSliceInfo(ctx, cli, args[1:])
	case "status":
		handleSliceStatus(ctx, cli, args[1:])
	case "owners":
		handleSliceOwners(ctx, cli, args[1:])
	default:
		log.Printf("Unknown slice command: %s", args[0])
		printSliceHelp()
	}
}

func handleSliceCreate(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice create <slice-id> [--files \"file1,file2\"] [--description \"desc\"]")
		return
	}

	sliceID := args[0]

	// Parse flags
	fs := flag.NewFlagSet("slice create", flag.ExitOnError)
	files := fs.String("files", "", "Comma-separated list of files")
	description := fs.String("description", "", "Slice description")
	fs.Parse(args)

	// Build file list
	var fileList []string
	if *files != "" {
		fileList = strings.Split(*files, ",")
		// Trim whitespace
		for i, f := range fileList {
			fileList[i] = strings.TrimSpace(f)
		}
	}

	// Create slice via admin service
	req := &adminv1.CreateSliceRequest{
		SliceId:     sliceID,
		Name:        sliceID,
		Description: *description,
		Files:       fileList,
		Owners:      []string{"user"}, // TODO: Get from auth context
		CreatedBy:   "user",           // TODO: Get from auth context
	}

	resp, err := cli.adminClient.CreateSlice(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create slice: %v", err)
	}

	fmt.Printf("Slice created: %s\n", resp.SliceId)
	fmt.Printf("Status: %s\n", resp.Status)
	if len(fileList) > 0 {
		fmt.Printf("Files: %d\n", len(fileList))
	}
}

func handleSliceList(ctx context.Context, cli *CLI, args []string) {
	// Parse flags
	fs := flag.NewFlagSet("slice list", flag.ExitOnError)
	limit := fs.Int("limit", 50, "Maximum number of slices to return")
	offset := fs.Int("offset", 0, "Offset for pagination")
	detailed := fs.Bool("detailed", false, "Show detailed information")
	mine := fs.Bool("mine", false, "Show only my slices")
	search := fs.String("search", "", "Search query")
	fs.Parse(args)

	req := &adminv1.ListSlicesRequest{
		Limit:  int32(*limit),
		Offset: int32(*offset),
	}

	resp, err := cli.adminClient.ListSlices(ctx, req)
	if err != nil {
		log.Fatalf("Failed to list slices: %v", err)
	}

	if *search != "" {
		fmt.Printf("Searching for: %s\n", *search)
	}

	if *mine {
		fmt.Println("Showing only my slices")
	}

	fmt.Printf("\nFound %d slice(s):\n", len(resp.Slices))
	for _, slice := range resp.Slices {
		fmt.Printf("- %s (commit: %s, files: %d)\n", slice.SliceId, slice.LatestCommitHash, slice.ModifiedFilesCount)
		if *detailed {
			fmt.Printf("  Last modified: %s\n", time.Unix(slice.LastModified, 0).Format(time.RFC3339))
		}
	}
}

func handleSliceInfo(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice info <slice-id>")
		return
	}

	sliceID := args[0]

	req := &slicev1.StateRequest{
		SliceId: sliceID,
	}

	resp, err := cli.sliceClient.GetSliceState(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice info: %v", err)
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Printf("Latest commit: %s\n", resp.LatestCommitHash)
	fmt.Printf("Modified files: %d\n", len(resp.ModifiedFiles))
	fmt.Printf("Last modified: %s\n", time.Unix(resp.LastModified, 0).Format(time.RFC3339))
}

func handleSliceStatus(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice status <slice-id>")
		return
	}

	sliceID := args[0]

	req := &slicev1.StateRequest{
		SliceId: sliceID,
	}

	resp, err := cli.sliceClient.GetSliceState(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice status: %v", err)
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Printf("Status: Active\n")
	fmt.Printf("Head: %s\n", resp.LatestCommitHash)
	fmt.Printf("Modified files: %d\n", len(resp.ModifiedFiles))
}

func handleSliceOwners(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs slice owners <slice-id>")
		return
	}

	sliceID := args[0]
	log.Printf("Owners for slice %s: not implemented yet", sliceID)
}

func handleChangesetCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printChangesetHelp()
		return
	}

	switch args[0] {
	case "create":
		handleChangesetCreate(ctx, cli, args[1:])
	case "review":
		handleChangesetReview(ctx, cli, args[1:])
	case "merge":
		handleChangesetMerge(ctx, cli, args[1:])
	case "rebase":
		handleChangesetRebase(ctx, cli, args[1:])
	case "list":
		handleChangesetList(ctx, cli, args[1:])
	default:
		log.Printf("Unknown changeset command: %s", args[0])
		printChangesetHelp()
	}
}

func handleChangesetCreate(ctx context.Context, cli *CLI, args []string) {
	sliceID, err := readSliceIDFromConfig()
	if err != nil {
		log.Printf("Failed to read slice binding: %v", err)
		return
	}

	fs := flag.NewFlagSet("changeset create", flag.ExitOnError)
	message := fs.String("message", "", "Changeset message")
	base := fs.String("base", "", "Base commit hash")
	files := fs.String("files", "", "Comma-separated file list")
	author := fs.String("author", "user", "Author of the changeset")
	fs.Parse(args)

	modifiedFiles := []string{}
	if *files != "" {
		for _, f := range strings.Split(*files, ",") {
			modifiedFiles = append(modifiedFiles, strings.TrimSpace(f))
		}
	}
	modifiedFiles = append(modifiedFiles, fs.Args()...)

	req := &slicev1.CreateChangesetRequest{
		SliceId:        sliceID,
		BaseCommitHash: *base,
		ModifiedFiles:  modifiedFiles,
		Author:         *author,
		Message:        *message,
	}

	resp, err := cli.sliceClient.CreateChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to create changeset: %v", err)
	}

	fmt.Printf("Created changeset %s (hash: %s)\n", resp.ChangesetId, resp.ChangesetHash)
	fmt.Printf("Status: %s\n", resp.Status.String())
}

func handleChangesetReview(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs changeset review <changeset-id>")
		return
	}

	req := &slicev1.ReviewChangesetRequest{ChangesetId: args[0]}
	resp, err := cli.sliceClient.ReviewChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to review changeset: %v", err)
	}

	fmt.Printf("Changeset: %s\n", resp.Changeset.GetChangesetId())
	fmt.Printf("Status: %s\n", resp.ReviewStatus.String())
	if resp.Diff != nil {
		fmt.Printf("Files changed: %d\n", resp.Diff.FilesAdded+resp.Diff.FilesModified+resp.Diff.FilesDeleted)
	}
}

func handleChangesetMerge(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs changeset merge <changeset-id>")
		return
	}

	req := &slicev1.MergeChangesetRequest{ChangesetId: args[0]}
	resp, err := cli.sliceClient.MergeChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to merge changeset: %v", err)
	}

	fmt.Printf("Merge status: %s\n", resp.Status.String())
	fmt.Printf("New commit: %s\n", resp.NewCommitHash)

	if resp.Status == slicev1.MergeStatus_MERGE_STATUS_CONFLICT {
		fmt.Println("Conflicts detected:")
		for _, conflict := range resp.Conflicts {
			fmt.Printf("- %s (slices: %s)\n", conflict.FileId, strings.Join(conflict.ConflictingSliceIds, ", "))
		}
	}
}

func handleChangesetRebase(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs changeset rebase <changeset-id>")
		return
	}

	req := &slicev1.RebaseChangesetRequest{ChangesetId: args[0]}
	resp, err := cli.sliceClient.RebaseChangeset(ctx, req)
	if err != nil {
		log.Fatalf("Failed to rebase changeset: %v", err)
	}

	fmt.Printf("Rebase status: %s\n", resp.Status.String())
	fmt.Printf("New base commit: %s\n", resp.NewBaseCommitHash)
}

func handleChangesetList(ctx context.Context, cli *CLI, args []string) {
	sliceID, err := readSliceIDFromConfig()
	if err != nil {
		log.Printf("Failed to read slice binding: %v", err)
		return
	}

	fs := flag.NewFlagSet("changeset list", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Maximum results")
	status := &stringFlag{}
	fs.Var(status, "status", "Filter by status (pending, approved, rejected, merged)")
	fs.Parse(args)

	statusFilter := slicev1.ChangesetStatus(-1)
	if status.set {
		switch strings.ToLower(status.value) {
		case "approved":
			statusFilter = slicev1.ChangesetStatus_APPROVED
		case "rejected":
			statusFilter = slicev1.ChangesetStatus_REJECTED
		case "merged":
			statusFilter = slicev1.ChangesetStatus_MERGED
		case "pending":
			statusFilter = slicev1.ChangesetStatus_PENDING
		default:
			log.Printf("Unknown status filter: %s", status.value)
			return
		}
	}

	req := &slicev1.ListChangesetsRequest{
		SliceId:      sliceID,
		StatusFilter: statusFilter,
		Limit:        int32(*limit),
	}

	resp, err := cli.sliceClient.ListChangesets(ctx, req)
	if err != nil {
		log.Fatalf("Failed to list changesets: %v", err)
	}

	sort.Slice(resp.Changesets, func(i, j int) bool {
		return resp.Changesets[i].CreatedAt > resp.Changesets[j].CreatedAt
	})

	fmt.Printf("Found %d changeset(s) for slice %s\n", len(resp.Changesets), sliceID)
	for _, cs := range resp.Changesets {
		fmt.Printf("- %s [%s] %s\n", cs.ChangesetId, cs.Status.String(), cs.Message)
	}
}

func handleStatus(ctx context.Context, cli *CLI) {
	// Check if in a gitslice directory
	if _, err := os.Stat(".gs"); os.IsNotExist(err) {
		log.Println("Not in a gitslice directory. Run 'gs init <slice-id>' to initialize.")
		return
	}

	// Read slice ID from .gs/config
	sliceID, err := readSliceIDFromConfig()
	if err != nil {
		log.Printf("Failed to read .gs/config: %v", err)
		return
	}

	req := &slicev1.StateRequest{
		SliceId: sliceID,
	}

	resp, err := cli.sliceClient.GetSliceState(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice state: %v", err)
	}

	fmt.Printf("Slice: %s\n", sliceID)
	fmt.Printf("Head: %s\n", resp.LatestCommitHash)
	fmt.Printf("Modified files: %d\n", len(resp.ModifiedFiles))
	fmt.Printf("Last modified: %s\n", time.Unix(resp.LastModified, 0).Format(time.RFC3339))
	fmt.Printf("Working directory: Clean\n")
}

func handleInit(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs init <slice-id>")
		return
	}

	sliceID := args[0]

	// Check if directory is empty
	entries, err := os.ReadDir(".")
	if err != nil {
		log.Fatalf("Failed to read directory: %v", err)
	}

	if len(entries) > 0 {
		log.Fatal("Directory is not empty. Please initialize in an empty directory or use --force.")
	}

	// Create .gs directory
	if err := os.MkdirAll(".gs", 0755); err != nil {
		log.Fatalf("Failed to create .gs directory: %v", err)
	}

	// Write config file
	if err := writeConfigFile(sliceID); err != nil {
		log.Fatalf("Failed to write config file: %v", err)
	}

	fmt.Printf("Initialized empty gitslice repository in slice: %s\n", sliceID)
}

func handleLog(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs log <slice-id>")
		return
	}

	sliceID := args[0]

	req := &slicev1.CommitHistoryRequest{
		SliceId: sliceID,
		Limit:   10,
	}

	resp, err := cli.sliceClient.GetSliceCommits(ctx, req)
	if err != nil {
		log.Fatalf("Failed to get slice commits: %v", err)
	}

	fmt.Printf("Commit history for slice: %s\n", sliceID)
	fmt.Printf("%d commit(s)\n\n", len(resp.Commits))
	for _, commit := range resp.Commits {
		fmt.Printf("%s %s\n", commit.CommitHash, commit.Message)
	}
}

func handleConflictCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		printConflictHelp()
		return
	}

	switch args[0] {
	case "list":
		handleConflictList(ctx, cli, args[1:])
	case "resolve":
		handleConflictResolve(ctx, cli, args[1:])
	case "show":
		handleConflictShow(ctx, cli, args[1:])
	default:
		log.Printf("Unknown conflict command: %s", args[0])
		printConflictHelp()
	}
}

func handleConflictList(ctx context.Context, cli *CLI, args []string) {
	fs := flag.NewFlagSet("conflict list", flag.ExitOnError)
	sliceFlag := fs.String("slice", "", "Slice ID to inspect for conflicts")
	detailed := fs.Bool("detailed", false, "Show detailed conflict information")
	severity := fs.Bool("severity", false, "Show severity level")
	fs.Parse(args)

	sliceID := *sliceFlag
	if sliceID == "" {
		if cfgSlice, err := readSliceIDFromConfig(); err == nil {
			sliceID = cfgSlice
		}
	}

	req := &adminv1.ConflictsRequest{}
	if sliceID != "" {
		req.SliceId = &sliceID
	}

	resp, err := cli.adminClient.GetConflicts(ctx, req)
	if err != nil {
		log.Fatalf("Failed to list conflicts: %v", err)
	}

	fmt.Printf("Found %d conflict(s)\n", len(resp.Conflicts))
	for _, conflict := range resp.Conflicts {
		severityLabel := ""
		if *severity {
			switch {
			case len(conflict.ConflictingSliceIds) >= 3:
				severityLabel = "HIGH"
			case len(conflict.ConflictingSliceIds) == 2:
				severityLabel = "MEDIUM"
			default:
				severityLabel = "LOW"
			}
		}

		line := fmt.Sprintf("- %s", conflict.FileId)
		if *detailed || len(conflict.ConflictingSliceIds) > 0 {
			line = fmt.Sprintf("%s (slices: %s)", line, strings.Join(conflict.ConflictingSliceIds, ", "))
		}
		if severityLabel != "" {
			line = fmt.Sprintf("%s severity: %s", line, severityLabel)
		}

		fmt.Println(line)
	}
}

func handleConflictResolve(ctx context.Context, cli *CLI, args []string) {
	fs := flag.NewFlagSet("conflict resolve", flag.ExitOnError)
	theirs := fs.String("theirs", "", "Resolve in favor of provided slice ID")
	ours := fs.Bool("ours", false, "Resolve in favor of current slice")
	fs.Parse(args)

	remaining := fs.Args()
	if len(remaining) < 1 {
		log.Println("Usage: gs conflict resolve [--ours|--theirs <slice-id>] <file>")
		return
	}

	fileID := remaining[0]
	preferredSlice := *theirs
	if preferredSlice == "" {
		if *ours {
			cfgSlice, err := readSliceIDFromConfig()
			if err != nil {
				log.Fatalf("Failed to read slice binding: %v", err)
			}
			preferredSlice = cfgSlice
		}
	}

	req := &adminv1.ResolveConflictRequest{FileId: fileID, PreferredSliceId: preferredSlice}
	resp, err := cli.adminClient.ResolveConflict(ctx, req)
	if err != nil {
		log.Fatalf("Failed to resolve conflict: %v", err)
	}

	fmt.Printf("Resolved conflict for %s\n", resp.ResolvedConflict.FileId)
	fmt.Printf("Remaining ownership: %s\n", strings.Join(resp.ResolvedConflict.ConflictingSliceIds, ", "))
}

func handleConflictShow(ctx context.Context, cli *CLI, args []string) {
	if len(args) < 1 {
		log.Println("Usage: gs conflict show <file>")
		return
	}

	fileID := args[0]
	req := &adminv1.ConflictsRequest{}
	resp, err := cli.adminClient.GetConflicts(ctx, req)
	if err != nil {
		log.Fatalf("Failed to fetch conflicts: %v", err)
	}

	for _, conflict := range resp.Conflicts {
		if conflict.FileId == fileID {
			fmt.Printf("Conflict for %s\n", fileID)
			fmt.Printf("Conflicting slices: %s\n", strings.Join(conflict.ConflictingSliceIds, ", "))
			return
		}
	}

	fmt.Printf("No conflict found for %s\n", fileID)
}

func readSliceIDFromConfig() (string, error) {
	data, err := os.ReadFile(".gs/config")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writeConfigFile(sliceID string) error {
	return os.WriteFile(".gs/config", []byte(sliceID), 0644)
}

func printHelp() {
	fmt.Println("Usage: gs <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  slice       Manage slices")
	fmt.Println("  changeset   Manage change lists")
	fmt.Println("  conflict    Detect and resolve conflicts")
	fmt.Println("  init        Initialize working directory")
	fmt.Println("  status      Show working directory status")
	fmt.Println("  log         Show slice commit history")
	fmt.Println("\nUse 'gs <command> --help' for more information about a command.")
}

func printSliceHelp() {
	fmt.Println("Usage: gs slice <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  create    Create a new slice")
	fmt.Println("  list      List all slices")
	fmt.Println("  info      Show slice information")
	fmt.Println("  status    Show slice status")
	fmt.Println("  owners    Show slice owners")
}

func printChangesetHelp() {
	fmt.Println("Usage: gs changeset <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  create    Create a new changeset from local modifications")
	fmt.Println("  review    Review a changeset")
	fmt.Println("  merge     Merge a changeset into the slice")
	fmt.Println("  rebase    Rebase a changeset onto the latest slice head")
	fmt.Println("  list      List changesets for the current slice")
}

func printConflictHelp() {
	fmt.Println("Usage: gs conflict <command> [options]")
	fmt.Println("\nCommands:")
	fmt.Println("  list       List conflicts for the current or specified slice")
	fmt.Println("  resolve    Resolve a conflict in favor of a slice")
	fmt.Println("  show       Show details for a conflicted file")
}
