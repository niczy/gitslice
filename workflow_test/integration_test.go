package workflow

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	adminservice "github.com/niczy/gitslice/internal/services/admin"
	sliceservice "github.com/niczy/gitslice/internal/services/slice"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	sliceServiceAddr string
	adminServiceAddr string
	cliBinaryPath    string

	sliceServer *grpc.Server
	adminServer *grpc.Server
)

// TestMain sets up and tears down services for all tests
func TestMain(m *testing.M) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		fmt.Println("Skipping integration tests. Set RUN_INTEGRATION_TESTS=1 to run.")
		os.Exit(0)
	}

	st := storage.NewInMemoryStorage()

	// Initialize root slice
	if err := st.InitializeRootSlice(nil); err != nil {
		fmt.Printf("Warning: Failed to initialize root slice: %v\n", err)
	}

	var err error
	sliceServiceAddr, sliceServer, err = startSliceService(st)
	if err != nil {
		fmt.Printf("Failed to start slice service: %v\n", err)
		os.Exit(1)
	}

	adminServiceAddr, adminServer, err = startAdminService(st)
	if err != nil {
		fmt.Printf("Failed to start admin service: %v\n", err)
		stopServers()
		os.Exit(1)
	}

	cliBinaryPath, err = buildCLIBinary()
	if err != nil {
		fmt.Printf("Failed to build CLI: %v\n", err)
		stopServers()
		os.Exit(1)
	}

	// Allow servers to bind before running tests
	time.Sleep(100 * time.Millisecond)

	code := m.Run()

	stopServers()
	os.Exit(code)
}

func startSliceService(st storage.Storage) (string, *grpc.Server, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	srv := sliceservice.NewGRPCServer(st)
	go srv.Serve(lis)

	return lis.Addr().String(), srv, nil
}

func startAdminService(st storage.Storage) (string, *grpc.Server, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	srv := adminservice.NewGRPCServer(st)
	go srv.Serve(lis)

	return lis.Addr().String(), srv, nil
}

func stopServers() {
	if sliceServer != nil {
		sliceServer.GracefulStop()
	}
	if adminServer != nil {
		adminServer.GracefulStop()
	}
	if cliBinaryPath != "" {
		_ = os.RemoveAll(filepath.Dir(cliBinaryPath))
	}
}

func buildCLIBinary() (string, error) {
	tmpDir, err := os.MkdirTemp("", "gs-cli-bin-")
	if err != nil {
		return "", err
	}

	binaryPath := filepath.Join(tmpDir, "gs_cli")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./gs_cli")
	cmd.Dir = ".."
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build failed: %w\nOutput:\n%s", err, string(output))
	}

	return binaryPath, nil
}

// runCLI executes a CLI command in the current working directory.
func runCLI(args ...string) (string, error) {
	return runCLIWithDir("", args...)
}

// runCLIWithDir executes a CLI command from the provided working directory.
func runCLIWithDir(workdir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fullArgs := append([]string{"--slice-addr", sliceServiceAddr, "--admin-addr", adminServiceAddr}, args...)
	cmd := exec.CommandContext(ctx, cliBinaryPath, fullArgs...)
	if workdir != "" {
		cmd.Dir = workdir
	}

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func runCLIOrFail(t *testing.T, workdir string, args ...string) string {
	t.Helper()

	output, err := runCLIWithDir(workdir, args...)
	if err != nil {
		t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
	}

	return output
}

func extractChangesetID(output string) string {
	re := regexp.MustCompile(`Created changeset ([^ ]+) `)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

func extractCommitHash(output string) string {
	re := regexp.MustCompile(`New commit: ([^\n]+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func newSliceClient(t *testing.T) slicev1.SliceServiceClient {
	t.Helper()

	conn, err := grpc.Dial(sliceServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial slice service: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return slicev1.NewSliceServiceClient(conn)
}

func newAdminClient(t *testing.T) adminv1.AdminServiceClient {
	t.Helper()

	conn, err := grpc.Dial(adminServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial admin service: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return adminv1.NewAdminServiceClient(conn)
}

func resolveAllConflicts(ctx context.Context, t *testing.T, client adminv1.AdminServiceClient) {
	resp, err := client.GetConflicts(ctx, &adminv1.ConflictsRequest{})
	if err != nil {
		t.Fatalf("failed to list conflicts: %v", err)
	}

	for _, conflict := range resp.Conflicts {
		preferred := ""
		if len(conflict.ConflictingSliceIds) > 0 {
			preferred = conflict.ConflictingSliceIds[0]
		}

		if _, err := client.ResolveConflict(ctx, &adminv1.ResolveConflictRequest{FileId: conflict.FileId, PreferredSliceId: preferred}); err != nil {
			t.Fatalf("failed to resolve conflict for %s: %v", conflict.FileId, err)
		}
	}
}

func TestChangesetWorkflowEndToEnd(t *testing.T) {
	workdir := t.TempDir()
	sliceID := "slice-integration"

	output := runCLIOrFail(t, workdir, "slice", "create", sliceID, "--description", "integration slice")
	if !strings.Contains(output, "Slice created") {
		t.Fatalf("Expected slice creation output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "init", sliceID)
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("Expected init output, got: %s", output)
	}

	// Use unique file names to avoid conflicts with other tests
	uniqueFile := fmt.Sprintf("integration_%d.go", time.Now().UnixNano())
	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "initial change", "--files", uniqueFile)
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("Failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "review", changesetID)
	if !strings.Contains(output, "Changeset: "+changesetID) {
		t.Fatalf("Expected review output to include changeset ID, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("Expected merge success, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "list", "--status", "merged")
	if !strings.Contains(output, changesetID) {
		t.Fatalf("Expected merged changeset in list output, got: %s", output)
	}
}

func TestRootSliceAndForkWorkflow(t *testing.T) {
	workdir := t.TempDir()

	output := runCLIOrFail(t, workdir, "root")
	if !strings.Contains(output, "Root Slice ID: root_slice") {
		t.Fatalf("Expected root slice info, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "init", "root_slice")
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("Expected init output, got: %s", output)
	}

	srcFolder := fmt.Sprintf("src_%d", time.Now().UnixNano())
	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "Create src folder", "--files", srcFolder)
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("Failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("Expected merge success, got: %s", output)
	}

	newSliceID := fmt.Sprintf("slice-fork-%d", time.Now().UnixNano())
	output = runCLIOrFail(t, workdir, "fork", newSliceID, srcFolder, "--parent", "root_slice")
	if !strings.Contains(output, "Created slice: "+newSliceID) {
		t.Fatalf("Expected slice creation output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "slice", "info", newSliceID)
	if !strings.Contains(output, "Slice: "+newSliceID) {
		t.Fatalf("Expected slice info output, got: %s", output)
	}

	newSliceWorkdir := t.TempDir()
	output = runCLIOrFail(t, newSliceWorkdir, "init", newSliceID)
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("Expected init output, got: %s", output)
	}

	subFolder := fmt.Sprintf("components_%d", time.Now().UnixNano())
	output = runCLIOrFail(t, newSliceWorkdir, "changeset", "create", "--message", "Create components subfolder", "--files", subFolder)
	changesetID = extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("Failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, newSliceWorkdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("Expected merge success for subfolder, got: %s", output)
	}
}

func TestBatchMergeClearsConflictsAndPromotesFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping batch merge integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(nil); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	addr, srv, err := startAdminService(st)
	if err != nil {
		t.Fatalf("failed to start admin service: %v", err)
	}
	defer srv.GracefulStop()

	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial admin service: %v", err)
	}
	defer conn.Close()

	client := adminv1.NewAdminServiceClient(conn)
	sliceA := fmt.Sprintf("batch-merge-a-%d", time.Now().UnixNano())
	sliceB := fmt.Sprintf("batch-merge-b-%d", time.Now().UnixNano())

	if _, err := client.CreateSlice(ctx, &adminv1.CreateSliceRequest{SliceId: sliceA, Name: "Batch A", Files: []string{"file-a"}}); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if _, err := client.CreateSlice(ctx, &adminv1.CreateSliceRequest{SliceId: sliceB, Name: "Batch B", Files: []string{"file-b"}}); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	mergeResp, err := client.BatchMerge(ctx, &adminv1.BatchMergeRequest{})
	if err != nil {
		t.Fatalf("batch merge failed: %v", err)
	}
	if mergeResp.MergedSliceCount != 2 {
		t.Fatalf("expected 2 merged slices, got %d", mergeResp.MergedSliceCount)
	}

	listResp, err := client.ListSlices(ctx, &adminv1.ListSlicesRequest{Limit: 50})
	if err != nil {
		t.Fatalf("list slices failed: %v", err)
	}

	var rootInfo *adminv1.SliceInfo
	for _, info := range listResp.Slices {
		if info.SliceId == "root_slice" {
			rootInfo = info
			break
		}
	}

	if rootInfo == nil {
		t.Fatalf("root slice not found in list response")
	}
	if rootInfo.LatestCommitHash != mergeResp.GlobalCommitHash {
		t.Fatalf("expected root commit %s, got %s", mergeResp.GlobalCommitHash, rootInfo.LatestCommitHash)
	}
	if rootInfo.ModifiedFilesCount != 2 {
		t.Fatalf("expected 2 modified files in root metadata, got %d", rootInfo.ModifiedFilesCount)
	}

	conflictsResp, err := client.GetConflicts(ctx, &adminv1.ConflictsRequest{})
	if err != nil {
		t.Fatalf("get conflicts failed: %v", err)
	}
	if conflictsResp.TotalConflicts != 0 {
		t.Fatalf("expected no conflicts after batch merge, found %d", conflictsResp.TotalConflicts)
	}
}

func TestSliceCommitHistoryIntegration(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-history-%d", time.Now().UnixNano())

	output := runCLIOrFail(t, workdir, "slice", "create", sliceID, "--files", "history_file.txt")
	if !strings.Contains(output, "Slice created") {
		t.Fatalf("expected slice creation output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "init", sliceID)
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("expected init output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "history change", "--files", "history_file.txt")
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected merge success, got: %s", output)
	}

	commitHash := extractCommitHash(output)
	if commitHash == "" {
		t.Fatalf("expected commit hash in merge output, got: %s", output)
	}

	sliceClient := newSliceClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	historyResp, err := sliceClient.GetSliceCommits(ctx, &slicev1.CommitHistoryRequest{SliceId: sliceID, Limit: 5})
	if err != nil {
		t.Fatalf("failed to fetch slice commits: %v", err)
	}

	if len(historyResp.Commits) == 0 {
		t.Fatalf("expected at least one commit in history")
	}

	if historyResp.Commits[0].CommitHash != commitHash {
		t.Fatalf("expected latest commit %s, got %s", commitHash, historyResp.Commits[0].CommitHash)
	}
	if historyResp.Commits[0].Message != "history change" {
		t.Fatalf("expected commit message 'history change', got %q", historyResp.Commits[0].Message)
	}
}

func TestGlobalStateTrackingIntegration(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-global-%d", time.Now().UnixNano())

	output := runCLIOrFail(t, workdir, "slice", "create", sliceID, "--files", "global_state.txt")
	if !strings.Contains(output, "Slice created") {
		t.Fatalf("expected slice creation output, got: %s", output)
	}

	adminClient := newAdminClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resolveAllConflicts(ctx, t, adminClient)

	mergeResp, err := adminClient.BatchMerge(ctx, &adminv1.BatchMergeRequest{})
	if err != nil {
		t.Fatalf("batch merge failed: %v", err)
	}

	stateResp, err := adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
	if err != nil {
		t.Fatalf("failed to get global state: %v", err)
	}

	if stateResp.GlobalCommitHash != mergeResp.GlobalCommitHash {
		t.Fatalf("expected global commit hash %s, got %s", mergeResp.GlobalCommitHash, stateResp.GlobalCommitHash)
	}
	if len(stateResp.History) == 0 {
		t.Fatalf("expected global history to include merge commit")
	}
	if stateResp.History[0].CommitHash != mergeResp.GlobalCommitHash {
		t.Fatalf("expected latest history commit %s, got %s", mergeResp.GlobalCommitHash, stateResp.History[0].CommitHash)
	}

	foundSlice := false
	for _, id := range stateResp.History[0].MergedSliceIds {
		if id == sliceID {
			foundSlice = true
			break
		}
	}
	if !foundSlice {
		t.Fatalf("expected merged slice %s to be recorded in history", sliceID)
	}

	sliceClient := newSliceClient(t)
	rootState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: "root_slice"})
	if err != nil {
		t.Fatalf("failed to get root slice state: %v", err)
	}

	if rootState.LatestCommitHash != mergeResp.GlobalCommitHash {
		t.Fatalf("expected root head to match global commit hash %s, got %s", mergeResp.GlobalCommitHash, rootState.LatestCommitHash)
	}
}
