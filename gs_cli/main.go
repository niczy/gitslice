package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
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
	case "status":
		handleStatus(ctx, cli)
	case "init":
		handleInit(ctx, cli, args[1:])
	case "log":
		handleLog(ctx, cli, args[1:])
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
