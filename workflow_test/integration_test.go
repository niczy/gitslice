package workflow

import (
	"bytes"
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

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/niczy/gitslice/internal/models"
	adminservice "github.com/niczy/gitslice/internal/services/admin"
	fileservice "github.com/niczy/gitslice/internal/services/file"
	sliceservice "github.com/niczy/gitslice/internal/services/slice"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	filev1 "github.com/niczy/gitslice/proto/file"
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
	testStorage *storage.RedisStorage
)

// TestMain sets up and tears down services for all tests
func TestMain(m *testing.M) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		fmt.Println("Skipping integration tests. Set RUN_INTEGRATION_TESTS=1 to run.")
		os.Exit(0)
	}

	mr, err := miniredis.Run()
	if err != nil {
		fmt.Printf("Failed to start mock redis: %v\n", err)
		os.Exit(1)
	}

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	objectStore := storage.NewInMemoryObjectStore()
	st := storage.NewRedisStorage(client, objectStore, "integration")
	testStorage = st
	defer func() {
		_ = client.Close()
		mr.Close()
	}()

	// Initialize root slice
	if err := st.InitializeRootSlice(nil); err != nil {
		fmt.Printf("Warning: Failed to initialize root slice: %v\n", err)
	}

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
	filev1.RegisterFileServiceServer(srv, fileservice.NewService(st))
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

func runGitOrFail(t *testing.T, workdir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = workdir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git command failed: %v\nOutput:\n%s", err, output)
	}

	return strings.TrimSpace(string(output))
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

func newFileClient(t *testing.T) filev1.FileServiceClient {
	t.Helper()

	conn, err := grpc.Dial(sliceServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial slice service for file client: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return filev1.NewFileServiceClient(conn)
}

func assertEntryNames(t *testing.T, entries []*filev1.DirectoryEntry, expected ...string) {
	t.Helper()

	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		seen[entry.Name] = true
	}

	for _, name := range expected {
		if !seen[name] {
			t.Fatalf("expected entry %q in list, got %#v", name, entries)
		}
	}
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

func TestCheckoutInitializesGitRepo(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-git-%d", time.Now().UnixNano())

	output := runCLIOrFail(t, workdir, "slice", "create", sliceID, "--files", "git_file.txt")
	if !strings.Contains(output, "Slice created") {
		t.Fatalf("Expected slice creation output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "slice", "checkout", sliceID)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("Expected checkout output, got: %s", output)
	}

	insideRepo := runGitOrFail(t, workdir, "rev-parse", "--is-inside-work-tree")
	if insideRepo != "true" {
		t.Fatalf("expected git repo, got %q", insideRepo)
	}

	branch := runGitOrFail(t, workdir, "symbolic-ref", "--short", "HEAD")
	if branch != "main" {
		t.Fatalf("expected main branch after checkout, got %q", branch)
	}

	latestMessage := runGitOrFail(t, workdir, "log", "-1", "--pretty=%s")
	if !strings.Contains(latestMessage, "gitslice checkout") {
		t.Fatalf("expected checkout commit message, got %q", latestMessage)
	}

	status := runGitOrFail(t, workdir, "status", "--porcelain")
	if status != "" {
		t.Fatalf("expected clean git status after checkout, got %q", status)
	}
}

func TestChangesetCreateRequiresMainBranch(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-branch-%d", time.Now().UnixNano())

	output := runCLIOrFail(t, workdir, "slice", "create", sliceID, "--files", "branch_file.txt")
	if !strings.Contains(output, "Slice created") {
		t.Fatalf("Expected slice creation output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "init", sliceID)
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("Expected init output, got: %s", output)
	}

	runGitOrFail(t, workdir, "checkout", "-b", "feature/test")

	output, err := runCLIWithDir(workdir, "changeset", "create", "--message", "branch change", "--files", "branch_file.txt")
	if err == nil {
		t.Fatalf("expected changeset creation to fail on non-main branch, output: %s", output)
	}
	if !strings.Contains(output, "main branch") {
		t.Fatalf("expected main branch error, got: %s", output)
	}
}

func TestRootSliceEndToEndWorkflow(t *testing.T) {
	workdir := t.TempDir()

	output := runCLIOrFail(t, workdir, "root")
	if !strings.Contains(output, "Root Slice ID: root_slice") {
		t.Fatalf("expected root slice info, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "init", "root_slice")
	if !strings.Contains(output, "Initialized empty gitslice repository") {
		t.Fatalf("expected init output, got: %s", output)
	}

	rootFolders := []string{"apps", "services", "docs"}
	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "Add root folders", "--files", strings.Join(rootFolders, ","))
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected merge success, got: %s", output)
	}

	rootCommit := extractCommitHash(output)
	if rootCommit == "" {
		t.Fatalf("expected root commit hash from merge output, got: %s", output)
	}

	sliceID := fmt.Sprintf("slice-apps-%d", time.Now().UnixNano())
	output = runCLIOrFail(t, workdir, "fork", sliceID, "apps", "--parent", "root_slice")
	if !strings.Contains(output, "Created slice: "+sliceID) {
		t.Fatalf("expected slice creation output, got: %s", output)
	}

	sliceWorkdir := t.TempDir()
	output = runCLIOrFail(t, sliceWorkdir, "slice", "checkout", sliceID)
	if !strings.Contains(output, "Checked out slice: "+sliceID) {
		t.Fatalf("expected checkout output, got: %s", output)
	}
	if !strings.Contains(output, "apps (0 bytes)") {
		t.Fatalf("expected apps folder in checkout output, got: %s", output)
	}

	output = runCLIOrFail(t, sliceWorkdir, "changeset", "create", "--message", "Add apps readme", "--files", "apps/readme.md")
	changesetID = extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, sliceWorkdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected merge success, got: %s", output)
	}

	sliceCommit := extractCommitHash(output)
	if sliceCommit == "" {
		t.Fatalf("expected slice commit hash from merge output, got: %s", output)
	}

	updatedSliceWorkdir := t.TempDir()
	output = runCLIOrFail(t, updatedSliceWorkdir, "slice", "checkout", sliceID)
	if !strings.Contains(output, "Commit: "+sliceCommit) {
		t.Fatalf("expected latest slice commit in checkout, got: %s", output)
	}
	if !strings.Contains(output, "apps/readme.md") {
		t.Fatalf("expected apps/readme.md in slice checkout, got: %s", output)
	}

	rootCheckoutDir := t.TempDir()
	output = runCLIOrFail(t, rootCheckoutDir, "slice", "checkout", "root_slice")
	if !strings.Contains(output, "Commit: "+sliceCommit) {
		t.Fatalf("expected root slice to promote latest commit, got: %s", output)
	}
	if !strings.Contains(output, "apps/readme.md") {
		t.Fatalf("expected apps/readme.md in root checkout, got: %s", output)
	}
	if !strings.Contains(output, "apps (0 bytes)") || !strings.Contains(output, "services (0 bytes)") || !strings.Contains(output, "docs (0 bytes)") {
		t.Fatalf("expected root folders in root checkout output, got: %s", output)
	}
	if rootCommit == sliceCommit {
		t.Fatalf("expected root commit to advance after slice merge, got same commit %s", rootCommit)
	}
}

func TestFileBrowserIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if testStorage == nil {
		t.Fatalf("expected test storage to be initialized")
	}

	sliceID := fmt.Sprintf("slice-browser-%d", time.Now().UnixNano())
	files := []string{
		"apps/readme.md",
		"apps/components/button.jsx",
		"docs/guide.md",
	}

	adminClient := newAdminClient(t)
	if _, err := adminClient.CreateSlice(ctx, &adminv1.CreateSliceRequest{SliceId: sliceID, Name: "Browser", Files: files}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	readmeContent := []byte("# Hello\\nFile browser test.")
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       "apps/readme.md",
		Path:     "apps/readme.md",
		Type:     "file",
		ParentID: sliceID,
		Content:  readmeContent,
		Size:     int64(len(readmeContent)),
	}); err != nil {
		t.Fatalf("failed to add entry metadata: %v", err)
	}
	if err := testStorage.AddFileContent(ctx, &models.FileContent{
		FileID:  "apps/readme.md",
		Path:    "apps/readme.md",
		Content: readmeContent,
		Size:    int64(len(readmeContent)),
	}); err != nil {
		t.Fatalf("failed to add file content: %v", err)
	}

	fileClient := newFileClient(t)

	rootEntries, err := fileClient.ListEntries(ctx, &filev1.ListEntriesRequest{SliceId: sliceID})
	if err != nil {
		t.Fatalf("failed to list root entries: %v", err)
	}
	assertEntryNames(t, rootEntries.Entries, "apps", "docs")

	appEntries, err := fileClient.ListEntries(ctx, &filev1.ListEntriesRequest{SliceId: sliceID, Path: "apps"})
	if err != nil {
		t.Fatalf("failed to list apps entries: %v", err)
	}
	assertEntryNames(t, appEntries.Entries, "readme.md", "components")

	fileResp, err := fileClient.GetFile(ctx, &filev1.GetFileRequest{SliceId: sliceID, Path: "apps/readme.md"})
	if err != nil {
		t.Fatalf("failed to fetch file: %v", err)
	}
	if fileResp.File == nil || fileResp.File.Path != "apps/readme.md" {
		t.Fatalf("expected readme file response, got %#v", fileResp.File)
	}
	if !bytes.Equal(fileResp.File.Content, readmeContent) {
		t.Fatalf("unexpected file content: %q", string(fileResp.File.Content))
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

func TestRedisRestartRebuildsEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mr := miniredis.RunT(t)
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	objectStore := storage.NewInMemoryObjectStore()
	st := storage.NewRedisStorage(client, objectStore, "restart-e2e")
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to init root slice: %v", err)
	}

	sliceAddr, sliceSrv, err := startSliceService(st)
	if err != nil {
		t.Fatalf("failed to start slice service: %v", err)
	}
	defer sliceSrv.GracefulStop()

	adminAddr, adminSrv, err := startAdminService(st)
	if err != nil {
		t.Fatalf("failed to start admin service: %v", err)
	}
	defer adminSrv.GracefulStop()

	sliceConn, err := grpc.DialContext(ctx, sliceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial slice service: %v", err)
	}
	defer sliceConn.Close()
	adminConn, err := grpc.DialContext(ctx, adminAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial admin service: %v", err)
	}
	defer adminConn.Close()

	sliceClient := slicev1.NewSliceServiceClient(sliceConn)
	adminClient := adminv1.NewAdminServiceClient(adminConn)

	sliceID := fmt.Sprintf("restart-slice-%d", time.Now().UnixNano())
	fileID := fmt.Sprintf("persist-%d.txt", time.Now().UnixNano())
	if _, err := adminClient.CreateSlice(ctx, &adminv1.CreateSliceRequest{SliceId: sliceID, Name: "Persist", Files: []string{fileID}}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	cs, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{SliceId: sliceID, ModifiedFiles: []string{fileID}, Message: "persist"})
	if err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	mergeResp, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: cs.ChangesetId})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	mr.FlushAll()
	if err := st.RebuildIndexes(ctx); err != nil {
		t.Fatalf("rebuild after flush failed: %v", err)
	}

	globalState, err := adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
	if err != nil {
		t.Fatalf("failed to read global state after rebuild: %v", err)
	}
	if globalState.GlobalCommitHash != mergeResp.NewCommitHash {
		t.Fatalf("expected global commit %s after rebuild, got %s", mergeResp.NewCommitHash, globalState.GlobalCommitHash)
	}

	sliceState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sliceID})
	if err != nil {
		t.Fatalf("failed to read slice state after rebuild: %v", err)
	}
	if sliceState.LatestCommitHash != mergeResp.NewCommitHash {
		t.Fatalf("expected slice head %s after rebuild, got %s", mergeResp.NewCommitHash, sliceState.LatestCommitHash)
	}
}

func TestSlicePushLocksAndAutoPromotion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	adminClient := newAdminClient(t)
	sliceClient := newSliceClient(t)

	sharedFile := fmt.Sprintf("lock-shared-%d.txt", time.Now().UnixNano())
	sliceA := fmt.Sprintf("lock-a-%d", time.Now().UnixNano())
	sliceB := fmt.Sprintf("lock-b-%d", time.Now().UnixNano())

	if _, err := adminClient.CreateSlice(ctx, &adminv1.CreateSliceRequest{SliceId: sliceA, Name: "LockA", Files: []string{sharedFile}}); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if _, err := adminClient.CreateSlice(ctx, &adminv1.CreateSliceRequest{SliceId: sliceB, Name: "LockB", Files: []string{sharedFile}}); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	if _, err := adminClient.ResolveConflict(ctx, &adminv1.ResolveConflictRequest{FileId: sharedFile, PreferredSliceId: sliceA}); err != nil {
		t.Fatalf("failed to resolve conflict to slice A: %v", err)
	}

	changeset, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        sliceA,
		BaseCommitHash: "",
		ModifiedFiles:  []string{sharedFile},
		Author:         "tester",
		Message:        "lock test",
	})
	if err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}

	mergeResp, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: changeset.ChangesetId})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if mergeResp.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
		t.Fatalf("expected merge success, got status %v", mergeResp.Status)
	}
	if mergeResp.NewCommitHash == "" {
		t.Fatalf("expected new commit hash from merge")
	}

	stateResp, err := adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
	if err != nil {
		t.Fatalf("failed to read global state: %v", err)
	}
	if stateResp.GlobalCommitHash != mergeResp.NewCommitHash {
		t.Fatalf("expected global head %s to reflect slice promotion, got %s", mergeResp.NewCommitHash, stateResp.GlobalCommitHash)
	}

	promoted := false
	if len(stateResp.History) > 0 {
		for _, id := range stateResp.History[0].MergedSliceIds {
			if id == sliceA {
				promoted = true
				break
			}
		}
	}
	if !promoted {
		t.Fatalf("expected promoted slice %s to appear in global history", sliceA)
	}

	rootState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: "root_slice"})
	if err != nil {
		t.Fatalf("failed to fetch root slice state: %v", err)
	}
	if rootState.LatestCommitHash != mergeResp.NewCommitHash {
		t.Fatalf("expected root slice head %s, got %s", mergeResp.NewCommitHash, rootState.LatestCommitHash)
	}

	otherChange, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{SliceId: sliceB, ModifiedFiles: []string{sharedFile}, Message: "should conflict"})
	if err != nil {
		t.Fatalf("failed to create conflicting changeset: %v", err)
	}
	conflictResp, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: otherChange.ChangesetId})
	if err != nil {
		t.Fatalf("expected merge response despite lock, got error: %v", err)
	}
	if conflictResp.Status != slicev1.MergeStatus_MERGE_STATUS_CONFLICT {
		t.Fatalf("expected conflict status for locked file merge, got %v", conflictResp.Status)
	}
}

func TestConcurrentSlicePushesPromoteHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminClient := newAdminClient(t)
	sliceClient := newSliceClient(t)

	initialState, err := adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
	if err != nil {
		t.Fatalf("failed to read initial global state: %v", err)
	}

	const mergeCount = 5
	slices := make([]string, 0, mergeCount)
	changesets := make([]string, 0, mergeCount)
	commits := make(map[string]string)

	for i := 0; i < mergeCount; i++ {
		file := fmt.Sprintf("concurrency-%d-%d.txt", i, time.Now().UnixNano())
		sliceID := fmt.Sprintf("concurrency-slice-%d-%d", i, time.Now().UnixNano())
		slices = append(slices, sliceID)

		if _, err := adminClient.CreateSlice(ctx, &adminv1.CreateSliceRequest{SliceId: sliceID, Name: fmt.Sprintf("Concurrent-%d", i), Files: []string{file}}); err != nil {
			t.Fatalf("failed to create slice %d: %v", i, err)
		}

		cs, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{SliceId: sliceID, ModifiedFiles: []string{file}, Message: fmt.Sprintf("change-%d", i)})
		if err != nil {
			t.Fatalf("failed to create changeset for slice %d: %v", i, err)
		}
		changesets = append(changesets, cs.ChangesetId)
	}

	start := make(chan struct{})
	results := make(chan struct {
		sliceID string
		resp    *slicev1.MergeChangesetResponse
		err     error
	}, mergeCount)

	mergeSlice := func(sliceID, changesetID string) {
		<-start
		resp, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: changesetID})
		results <- struct {
			sliceID string
			resp    *slicev1.MergeChangesetResponse
			err     error
		}{sliceID: sliceID, resp: resp, err: err}
	}

	for i := 0; i < mergeCount; i++ {
		go mergeSlice(slices[i], changesets[i])
	}
	close(start)

	for i := 0; i < mergeCount; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("merge failed for %s: %v", result.sliceID, result.err)
		}
		if result.resp.Status != slicev1.MergeStatus_MERGE_STATUS_SUCCESS {
			t.Fatalf("expected merge success for %s, got %v", result.sliceID, result.resp.Status)
		}
		commits[result.sliceID] = result.resp.NewCommitHash
	}

	for _, sliceID := range slices {
		if commits[sliceID] == "" {
			t.Fatalf("expected commit hash for %s", sliceID)
		}
	}

	globalState, err := adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
	if err != nil {
		t.Fatalf("failed to read global state after merges: %v", err)
	}

	if len(globalState.History) < len(initialState.History)+mergeCount {
		t.Fatalf("expected at least %d history entries after concurrent merges, got %d", len(initialState.History)+mergeCount, len(globalState.History))
	}

	rootState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: "root_slice"})
	if err != nil {
		t.Fatalf("failed to fetch root slice state: %v", err)
	}
	if rootState.LatestCommitHash != globalState.GlobalCommitHash {
		t.Fatalf("expected root slice head %s to match global %s", rootState.LatestCommitHash, globalState.GlobalCommitHash)
	}

	historyCommits := make(map[string][]string)
	for _, entry := range globalState.History {
		historyCommits[entry.CommitHash] = entry.MergedSliceIds
	}

	for sliceID, commitHash := range commits {
		mergedSlices, ok := historyCommits[commitHash]
		if !ok {
			t.Fatalf("expected commit %s for slice %s to appear in global history", commitHash, sliceID)
		}
		foundSlice := false
		for _, id := range mergedSlices {
			if id == sliceID {
				foundSlice = true
				break
			}
		}
		if !foundSlice {
			t.Fatalf("expected slice %s to be recorded in history entry %s", sliceID, commitHash)
		}
	}

	for sliceID, expectedCommit := range commits {
		state, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sliceID})
		if err != nil {
			t.Fatalf("failed to fetch slice %s state: %v", sliceID, err)
		}
		if state.LatestCommitHash != expectedCommit {
			t.Fatalf("expected slice %s head %s, got %s", sliceID, expectedCommit, state.LatestCommitHash)
		}
	}
}
