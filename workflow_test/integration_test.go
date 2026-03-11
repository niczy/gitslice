package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/gateway"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/httpapi"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	accountv1 "github.com/niczy/gitslice/proto/account"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	filev1 "github.com/niczy/gitslice/proto/file"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	accountservice "github.com/niczy/gitslice/services/account"
	adminservice "github.com/niczy/gitslice/services/admin"
	agentservice "github.com/niczy/gitslice/services/agent"
	fileservice "github.com/niczy/gitslice/services/file"
	filesystemservice "github.com/niczy/gitslice/services/filesystem"
	sliceservice "github.com/niczy/gitslice/services/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var (
	grpcServiceAddr   string
	gatewayServiceURL string
	cliBinaryPath     string

	grpcServer      *grpc.Server
	gatewayServer   *http.Server
	gatewayListener net.Listener
	gatewayClose    func()
	testStorage     storage.Storage
	testAgentSvc    *agentsession.Service
)

func mustWriteSliceManifest(tb testing.TB, ctx context.Context, st storage.Storage, sliceID, filePath string, content []byte) string {
	tb.Helper()
	manifest, err := storage.WriteSliceFileManifest(ctx, st, sliceID, filePath, content)
	if err != nil {
		tb.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	return manifest.Hash
}

// TestMain sets up and tears down services for all tests
func TestMain(m *testing.M) {
	if os.Getenv("RUN_INTEGRATION_TESTS") == "" {
		fmt.Println("Skipping integration tests. Set RUN_INTEGRATION_TESTS=1 to run.")
		os.Exit(0)
	}

	var st storage.Storage
	var closeStorage func()
	var err error
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn != "" {
		objectStore := storage.NewInMemoryObjectStore()
		pg, err := storage.NewPostgresNativeStorage(context.Background(), dsn, objectStore, fmt.Sprintf("integration-%d", time.Now().UnixNano()))
		if err != nil {
			fmt.Printf("Failed to initialize postgres storage: %v\n", err)
			os.Exit(1)
		}
		st = pg
		closeStorage = func() { _ = pg.Close() }
	} else {
		st = storage.NewInMemoryStorage()
		closeStorage = func() {}
	}
	testStorage = st
	defer closeStorage()

	// Initialize root slice
	if err := st.InitializeRootSlice(nil); err != nil {
		fmt.Printf("Warning: Failed to initialize root slice: %v\n", err)
	}

	grpcServiceAddr, grpcServer, err = startGRPCServer(st)
	if err != nil {
		fmt.Printf("Failed to start gRPC services: %v\n", err)
		os.Exit(1)
	}

	gatewayServiceURL, gatewayServer, gatewayListener, gatewayClose, err = startGatewayServer(grpcServiceAddr, st)
	if err != nil {
		fmt.Printf("Failed to start gateway: %v\n", err)
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

func startGRPCServer(st storage.Storage) (string, *grpc.Server, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	srv := grpc.NewServer()
	accountservice.RegisterGRPCServer(srv, st)
	sliceservice.RegisterGRPCServer(srv, st)
	fileservice.RegisterGRPCServer(srv, st)
	filesystemservice.RegisterGRPCServer(srv, st)
	adminservice.RegisterGRPCServer(srv, st)
	testAgentSvc = agentsession.NewService(st, "test-agent-ws-secret")
	testAgentSvc.StartLifecycleLoop(context.Background())
	agentservice.RegisterGRPCServer(srv, st, testAgentSvc)

	go srv.Serve(lis)

	return lis.Addr().String(), srv, nil
}

func startGatewayServer(grpcAddr string, st storage.Storage) (string, *http.Server, net.Listener, func(), error) {
	ctx := context.Background()
	gatewayMux, closeConns, err := gateway.NewMux(ctx, grpcAddr)
	if err != nil {
		return "", nil, nil, nil, err
	}

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", common.HealthCheckHandler("test-gateway"))
	httpMux.HandleFunc("/ready", common.ReadyCheckHandler("test-gateway", func(ctx context.Context) bool {
		return gateway.GRPCReady(ctx, grpcAddr)
	}))
	agentSessionsAPI := httpapi.NewAgentSessionsAPI(st, testAgentSvc)
	httpMux.Handle("/ws/sessions/", http.HandlerFunc(agentSessionsAPI.HandleWS))
	httpMux.Handle("/", gateway.WithCORS(gatewayMux))

	server := &http.Server{Handler: httpMux}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		closeConns()
		return "", nil, nil, nil, err
	}

	go func() {
		if err := server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("gateway serve failed: %v\n", err)
		}
	}()

	url := "http://" + lis.Addr().String()
	if err := waitForHTTP(url+"/health", 5*time.Second); err != nil {
		_ = server.Close()
		_ = lis.Close()
		closeConns()
		return "", nil, nil, nil, err
	}
	return url, server, lis, closeConns, nil
}

func stopServers() {
	if grpcServer != nil {
		grpcServer.GracefulStop()
	}
	if gatewayServer != nil {
		_ = gatewayServer.Close()
	}
	if gatewayListener != nil {
		_ = gatewayListener.Close()
	}
	if gatewayClose != nil {
		gatewayClose()
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

func waitForHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func waitForCondition(timeout, interval time.Duration, condition func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := condition()
		if err != nil {
			lastErr = err
		}
		if ok {
			return nil
		}
		time.Sleep(interval)
	}
	if lastErr != nil {
		return fmt.Errorf("condition not met before timeout: %w", lastErr)
	}
	return errors.New("condition not met before timeout")
}

// runCLI executes a CLI command in the current working directory.
func runCLI(args ...string) (string, error) {
	return runCLIWithDir("", args...)
}

// runCLIWithDir executes a CLI command from the provided working directory.
func runCLIWithDir(workdir string, args ...string) (string, error) {
	return runCLIWithDirInput(workdir, "", args...)
}

func runCLIWithDirInput(workdir, input string, args ...string) (string, error) {
	return runCLIWithDirInputEnv(workdir, input, nil, args...)
}

func runCLIWithDirInputEnv(workdir, input string, env map[string]string, args ...string) (string, error) {
	return runCLIWithDirInputEnvLegacy(workdir, input, env, true, args...)
}

func runCLIWithDirInputEnvNoLegacyUser(workdir, input string, env map[string]string, args ...string) (string, error) {
	return runCLIWithDirInputEnvLegacy(workdir, input, env, false, args...)
}

func runCLIWithDirInputEnvLegacy(workdir, input string, env map[string]string, includeLegacyUser bool, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fullArgs := []string{
		"--account-addr", grpcServiceAddr,
		"--slice-addr", grpcServiceAddr,
		"--admin-addr", grpcServiceAddr,
		"--file-addr", grpcServiceAddr,
		"--fs-addr", grpcServiceAddr,
	}
	if includeLegacyUser {
		fullArgs = append(fullArgs, "--user", testUsername)
	}
	fullArgs = append(fullArgs, args...)
	cmd := exec.CommandContext(ctx, cliBinaryPath, fullArgs...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	if env != nil {
		cmd.Env = os.Environ()
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
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

func extractCreatedSliceID(output string) string {
	re := regexp.MustCompile(`\(id: ([^)]+)\)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func extractSnapshotID(output string) string {
	re := regexp.MustCompile(`Snapshot created: ([^\n]+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func extractFilesystemCommitHash(output string) string {
	re := regexp.MustCompile(`Commit: ([^\n]+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func newSliceClient(t *testing.T) slicev1.SliceServiceClient {
	t.Helper()

	conn, err := grpc.Dial(grpcServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	conn, err := grpc.Dial(grpcServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

	conn, err := grpc.Dial(grpcServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial slice service for file client: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return filev1.NewFileServiceClient(conn)
}

func newFilesystemClient(t *testing.T) filesystemv1.FilesystemServiceClient {
	t.Helper()

	conn, err := grpc.Dial(grpcServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial filesystem service: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return filesystemv1.NewFilesystemServiceClient(conn)
}

func newAccountClient(t *testing.T) accountv1.AccountServiceClient {
	t.Helper()

	conn, err := grpc.Dial(grpcServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to dial account service: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return accountv1.NewAccountServiceClient(conn)
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

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "init", sliceArg)
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

	rootSliceArg := sliceIDArg("root_slice")
	output = runCLIOrFail(t, workdir, "init", rootSliceArg)
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
	newSliceID = extractCreatedSliceID(output)
	if newSliceID == "" {
		t.Fatalf("failed to extract created slice ID from output: %s", output)
	}

	newSliceWorkdir := t.TempDir()
	newSliceArg := sliceIDArg(newSliceID)
	output = runCLIOrFail(t, newSliceWorkdir, "init", newSliceArg)
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

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "slice", "checkout", sliceArg)
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

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "init", sliceArg)
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

	rootSliceArg := sliceIDArg("root_slice")
	output = runCLIOrFail(t, workdir, "init", rootSliceArg)
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
	sliceID = extractCreatedSliceID(output)
	if sliceID == "" {
		t.Fatalf("failed to extract created slice ID from output: %s", output)
	}
	sliceArg := sliceIDArg(sliceID)

	sliceWorkdir := t.TempDir()
	output = runCLIOrFail(t, sliceWorkdir, "slice", "checkout", sliceArg)
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
	output = runCLIOrFail(t, updatedSliceWorkdir, "slice", "checkout", sliceArg)
	if !strings.Contains(output, "Commit: "+sliceCommit) {
		t.Fatalf("expected latest slice commit in checkout, got: %s", output)
	}
	if !strings.Contains(output, "apps (0 bytes)") {
		t.Fatalf("expected apps folder in slice checkout, got: %s", output)
	}

	rootCheckoutArg := sliceIDArg("root_slice")
	if err := waitForCondition(2*time.Second, 50*time.Millisecond, func() (bool, error) {
		rootCheckoutDir := t.TempDir()
		var err error
		output, err = runCLIWithDir(rootCheckoutDir, "slice", "checkout", rootCheckoutArg)
		if err != nil {
			return false, nil
		}
		return strings.Contains(output, "Commit: "+sliceCommit), nil
	}); err != nil {
		t.Fatalf("expected root slice to promote latest commit (%s): %v\nOutput:\n%s", sliceCommit, err, output)
	}
	if !strings.Contains(output, "Commit: "+sliceCommit) {
		t.Fatalf("expected root slice to promote latest commit, got: %s", output)
	}
	if !strings.Contains(output, "apps (0 bytes)") || !strings.Contains(output, "services (0 bytes)") || !strings.Contains(output, "docs (0 bytes)") {
		t.Fatalf("expected root folders in root checkout output, got: %s", output)
	}
	if rootCommit == sliceCommit {
		t.Fatalf("expected root commit to advance after slice merge, got same commit %s", rootCommit)
	}
}

func TestFilesystemCLIWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	remoteDir := fmt.Sprintf("/%s/fs-cli-%d", testUsername, time.Now().UnixNano())
	remoteFile := remoteDir + "/README.md"

	localFile := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(localFile, []byte("hello from fs cli\n"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	output := runCLIOrFail(t, "", "fs", "mkdir", remoteDir)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected mkdir commit output, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "write", remoteFile, "-f", localFile)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected write commit output, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "cat", remoteFile)
	if output != "hello from fs cli\n" {
		t.Fatalf("unexpected cat output: %q", output)
	}

	output = runCLIOrFail(t, "", "fs", "ls", remoteDir)
	if !strings.Contains(output, "README.md") {
		t.Fatalf("expected README.md in listing, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "stat", remoteFile)
	if !strings.Contains(output, "Type: file") {
		t.Fatalf("expected file stat output, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "snapshot", "-m", "initial")
	snapshotID := extractSnapshotID(output)
	if snapshotID == "" {
		t.Fatalf("failed to extract snapshot id from output: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "snapshots")
	if !strings.Contains(output, snapshotID) {
		t.Fatalf("expected snapshots output to include %s, got: %s", snapshotID, output)
	}

	if err := os.WriteFile(localFile, []byte("hello from fs cli v2\n"), 0o600); err != nil {
		t.Fatalf("rewrite local file: %v", err)
	}

	output = runCLIOrFail(t, "", "fs", "write", remoteFile, "-f", localFile)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected second write commit output, got: %s", output)
	}
	secondWriteCommit := extractFilesystemCommitHash(output)
	if secondWriteCommit == "" {
		t.Fatalf("failed to extract fs commit from output: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "cat", remoteFile)
	if output != "hello from fs cli v2\n" {
		t.Fatalf("unexpected cat output after second write: %q", output)
	}

	output = runCLIOrFail(t, "", "fs", "log", "--limit", "3")
	if !strings.Contains(output, "commit "+secondWriteCommit) {
		t.Fatalf("expected fs log to include %s, got: %s", secondWriteCommit, output)
	}
	if !strings.Contains(output, "write "+remoteFile) {
		t.Fatalf("expected fs log to show visible write path, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "show", secondWriteCommit)
	if !strings.Contains(output, "MODIFY "+remoteFile) {
		t.Fatalf("expected fs show to include modified visible path, got: %s", output)
	}
	if !strings.Contains(output, "-hello from fs cli") || !strings.Contains(output, "+hello from fs cli v2") {
		t.Fatalf("expected fs show patch output, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "diff", snapshotID)
	if !strings.Contains(output, "README.md") || !strings.Contains(output, "MODIFY") {
		t.Fatalf("expected diff output for README.md, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "restore", snapshotID)
	if !strings.Contains(output, "Restored to "+snapshotID) {
		t.Fatalf("expected restore output, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "cat", remoteFile)
	if output != "hello from fs cli\n" {
		t.Fatalf("unexpected cat output after restore: %q", output)
	}

	output = runCLIOrFail(t, "", "fs", "rm", remoteFile)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected rm commit output, got: %s", output)
	}

	client := newFilesystemClient(t)
	_, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeslice.IDForUsername(testUsername),
		Path:        remoteFile,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected remote file to be deleted, got err=%v", err)
	}
}

func TestFilesystemCLIUsesAPIKeyEnvOverLegacyUser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	apiUsername := fmt.Sprintf("api-key-user-%d", time.Now().UnixNano())
	if _, err := testStorage.EnsureUser(ctx, apiUsername); err != nil {
		t.Fatalf("EnsureUser failed: %v", err)
	}
	token := fmt.Sprintf("gs_test_%d", time.Now().UnixNano())
	if err := testStorage.CreateAuthSession(ctx, &models.AuthSession{
		SessionID: fmt.Sprintf("sess-%d", time.Now().UnixNano()),
		Username:  apiUsername,
		Token:     token,
	}); err != nil {
		t.Fatalf("CreateAuthSession failed: %v", err)
	}

	remoteDir := fmt.Sprintf("/%s/fs-cli-auth-%d", apiUsername, time.Now().UnixNano())
	output, err := runCLIWithDirInputEnv("", "", map[string]string{"GS_API_KEY": token}, "fs", "mkdir", remoteDir)
	if err != nil {
		t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Created directory") {
		t.Fatalf("expected directory creation output, got: %s", output)
	}

	client := newFilesystemClient(t)
	authCtx := withBearerToken(ctx, token)
	statResp, err := client.Stat(authCtx, &filesystemv1.StatRequest{
		WorkspaceId: homeslice.IDForUsername(apiUsername),
		Path:        remoteDir,
	})
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !statResp.GetExists() || statResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
		t.Fatalf("expected remote directory to exist, got %#v", statResp)
	}
}

func TestCLILoginAndLogoutUseStoredBearerCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	homeDir := t.TempDir()
	loginUsername := fmt.Sprintf("login-user-%d", time.Now().UnixNano())
	env := map[string]string{"HOME": homeDir}

	output, err := runCLIWithDirInputEnv("", "", env, "login", loginUsername)
	if err != nil {
		t.Fatalf("login command failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Logged in as: "+loginUsername) {
		t.Fatalf("unexpected login output: %s", output)
	}

	credentialsPath := filepath.Join(homeDir, ".gitslice", "credentials.json")
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatalf("ReadFile credentials failed: %v", err)
	}
	var creds struct {
		AccessToken string `json:"access_token"`
		Username    string `json:"username"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		t.Fatalf("Unmarshal credentials failed: %v", err)
	}
	if creds.AccessToken == "" || creds.Username != loginUsername {
		t.Fatalf("unexpected credentials contents: %+v", creds)
	}

	output, err = runCLIWithDirInputEnv("", "", env, "login")
	if err != nil {
		t.Fatalf("login status command failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Logged in as: "+loginUsername) {
		t.Fatalf("unexpected login status output: %s", output)
	}

	client := newFilesystemClient(t)
	authCtx := withBearerToken(ctx, creds.AccessToken)
	if _, err := client.ListWorkspaces(authCtx, &filesystemv1.ListWorkspacesRequest{}); err != nil {
		t.Fatalf("stored bearer token was not accepted before fs mkdir: %v", err)
	}

	remoteDir := fmt.Sprintf("/%s/login-smoke-%d", loginUsername, time.Now().UnixNano())
	output, err = runCLIWithDirInputEnv("", "", env, "fs", "mkdir", remoteDir)
	if err != nil {
		t.Fatalf("fs mkdir failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Created directory") {
		t.Fatalf("unexpected fs mkdir output: %s", output)
	}

	statResp, err := client.Stat(authCtx, &filesystemv1.StatRequest{
		WorkspaceId: homeslice.IDForUsername(loginUsername),
		Path:        remoteDir,
	})
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !statResp.GetExists() || statResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
		t.Fatalf("expected remote directory to exist, got %#v", statResp)
	}

	output, err = runCLIWithDirInputEnv("", "", env, "logout")
	if err != nil {
		t.Fatalf("logout command failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Logged out.") {
		t.Fatalf("unexpected logout output: %s", output)
	}
	if _, err := os.Stat(credentialsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected credentials file to be removed, stat err=%v", err)
	}
}

func TestCLIDeviceLoginAndRefreshesStoredCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	homeDir := t.TempDir()
	loginUsername := fmt.Sprintf("device-login-%d", time.Now().UnixNano())
	browserScript := filepath.Join(t.TempDir(), "approve-device-login.sh")
	script := `#!/usr/bin/env bash
set -euo pipefail
url="$1"
user_code="${url##*user_code=}"
curl -sf -X POST "$GS_DEVICE_APPROVE_BASE/v1/auth/device/approve" \
  -H "Authorization: User $GS_DEVICE_APPROVE_USER" \
  -H "Content-Type: application/json" \
  -d "{\"userCode\":\"${user_code}\"}" >/dev/null
`
	if err := os.WriteFile(browserScript, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile browser script failed: %v", err)
	}

	env := map[string]string{
		"HOME":                   homeDir,
		"GS_BROWSER_COMMAND":     browserScript,
		"GS_DEVICE_APPROVE_BASE": gatewayServiceURL,
		"GS_DEVICE_APPROVE_USER": loginUsername,
	}

	output, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "login")
	if err != nil {
		t.Fatalf("device login command failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "Enter code:") || !strings.Contains(output, "Logged in as "+loginUsername) {
		t.Fatalf("unexpected device login output: %s", output)
	}

	credentialsPath := filepath.Join(homeDir, ".gitslice", "credentials.json")
	data, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatalf("ReadFile credentials failed: %v", err)
	}
	var creds struct {
		AccessToken           string `json:"access_token"`
		RefreshToken          string `json:"refresh_token"`
		AccessTokenExpiresAt  string `json:"access_token_expires_at"`
		RefreshTokenExpiresAt string `json:"refresh_token_expires_at"`
		Username              string `json:"username"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		t.Fatalf("Unmarshal credentials failed: %v", err)
	}
	if creds.AccessToken == "" || creds.RefreshToken == "" || creds.AccessTokenExpiresAt == "" || creds.RefreshTokenExpiresAt == "" {
		t.Fatalf("unexpected device credentials contents: %+v", creds)
	}
	if creds.Username != loginUsername {
		t.Fatalf("expected device login username %q, got %+v", loginUsername, creds)
	}

	statusOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "login", "status")
	if err != nil {
		t.Fatalf("login status command failed: %v\nOutput:\n%s", err, statusOutput)
	}
	if !strings.Contains(statusOutput, "Logged in as: "+loginUsername) {
		t.Fatalf("unexpected login status output: %s", statusOutput)
	}

	originalAccessToken := creds.AccessToken
	creds.AccessTokenExpiresAt = time.Now().Add(-1 * time.Minute).Format(time.RFC3339)
	updatedData, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		t.Fatalf("Marshal credentials failed: %v", err)
	}
	updatedData = append(updatedData, '\n')
	if err := os.WriteFile(credentialsPath, updatedData, 0o600); err != nil {
		t.Fatalf("WriteFile refreshed credentials failed: %v", err)
	}

	if _, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "fs", "ls", "/"+loginUsername); err != nil {
		t.Fatalf("fs ls with expired stored access token failed: %v", err)
	}

	data, err = os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatalf("ReadFile refreshed credentials failed: %v", err)
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		t.Fatalf("Unmarshal refreshed credentials failed: %v", err)
	}
	if creds.AccessToken == originalAccessToken {
		t.Fatalf("expected CLI to refresh access token, still have %q", creds.AccessToken)
	}

	accountClient := newAccountClient(t)
	if _, err := accountClient.ListSessions(withBearerToken(ctx, originalAccessToken), &accountv1.ListSessionsRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected old access token to be invalid after refresh, got %v", err)
	}
	if _, err := accountClient.ListSessions(withBearerToken(ctx, creds.AccessToken), &accountv1.ListSessionsRequest{}); err != nil {
		t.Fatalf("refreshed access token should be accepted, got %v", err)
	}
}

func TestFilesystemShellWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	remoteRoot := fmt.Sprintf("/%s/fs-shell-%d", testUsername, time.Now().UnixNano())

	script := strings.Join([]string{
		"mkdir src",
		"cd src",
		`echo "print('hello world')" > main.py`,
		"ls",
		"cat main.py",
		`snapshot "added main"`,
		"history",
		"pwd",
		"exit",
	}, "\n") + "\n"

	output, err := runCLIWithDirInput("", script, "fs", "shell", remoteRoot)
	if err != nil {
		t.Fatalf("shell command failed: %v\nOutput:\n%s", err, output)
	}
	if !strings.Contains(output, "main.py") {
		t.Fatalf("expected ls output to include main.py, got: %s", output)
	}
	if !strings.Contains(output, "print('hello world')") {
		t.Fatalf("expected cat output in shell transcript, got: %s", output)
	}
	if !strings.Contains(output, `Snapshot created: `) || !strings.Contains(output, `"added main"`) {
		t.Fatalf("expected snapshot history in shell transcript, got: %s", output)
	}
	if !strings.Contains(output, remoteRoot+"/src") {
		t.Fatalf("expected pwd output in shell transcript, got: %s", output)
	}

	client := newFilesystemClient(t)
	readResp, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeslice.IDForUsername(testUsername),
		Path:        remoteRoot + "/src/main.py",
	})
	if err != nil {
		t.Fatalf("failed to read shell-written file: %v", err)
	}
	if got := string(readResp.GetContent()); got != "print('hello world')\n" {
		t.Fatalf("unexpected shell-written file content: %q", got)
	}
}

func TestFilesystemTransferWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	remoteProjectRoot := fmt.Sprintf("/%s/fs-transfer-%d/project", testUsername, time.Now().UnixNano())

	uploadRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(uploadRoot, "src"), 0o755); err != nil {
		t.Fatalf("create src dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(uploadRoot, "docs", "empty"), 0o755); err != nil {
		t.Fatalf("create empty dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadRoot, "README.md"), []byte("transfer root\n"), 0o600); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadRoot, "src", "main.py"), []byte("print('transfer')\n"), 0o600); err != nil {
		t.Fatalf("write src/main.py: %v", err)
	}

	output := runCLIOrFail(t, "", "fs", "upload", uploadRoot, remoteProjectRoot)
	if !strings.Contains(output, "Uploaded 2 files and 3 directories") {
		t.Fatalf("expected upload summary, got: %s", output)
	}

	client := newFilesystemClient(t)
	readmeResp, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeslice.IDForUsername(testUsername),
		Path:        remoteProjectRoot + "/README.md",
	})
	if err != nil {
		t.Fatalf("failed to read uploaded README.md: %v", err)
	}
	if got := string(readmeResp.GetContent()); got != "transfer root\n" {
		t.Fatalf("unexpected uploaded README.md content: %q", got)
	}

	mainResp, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeslice.IDForUsername(testUsername),
		Path:        remoteProjectRoot + "/src/main.py",
	})
	if err != nil {
		t.Fatalf("failed to read uploaded src/main.py: %v", err)
	}
	if got := string(mainResp.GetContent()); got != "print('transfer')\n" {
		t.Fatalf("unexpected uploaded src/main.py content: %q", got)
	}

	emptyDirResp, err := client.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: homeslice.IDForUsername(testUsername),
		Path:        remoteProjectRoot + "/docs/empty",
	})
	if err != nil {
		t.Fatalf("failed to stat uploaded empty dir: %v", err)
	}
	if !emptyDirResp.GetExists() || emptyDirResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
		t.Fatalf("expected uploaded empty dir to exist, got: %#v", emptyDirResp)
	}

	downloadRoot := t.TempDir()
	output = runCLIOrFail(t, "", "fs", "download", remoteProjectRoot, downloadRoot)
	if !strings.Contains(output, "Downloaded 2 files and 3 directories") {
		t.Fatalf("expected download summary, got: %s", output)
	}

	downloadedReadme, err := os.ReadFile(filepath.Join(downloadRoot, "README.md"))
	if err != nil {
		t.Fatalf("read downloaded README.md: %v", err)
	}
	if got := string(downloadedReadme); got != "transfer root\n" {
		t.Fatalf("unexpected downloaded README.md content: %q", got)
	}

	downloadedMain, err := os.ReadFile(filepath.Join(downloadRoot, "src", "main.py"))
	if err != nil {
		t.Fatalf("read downloaded src/main.py: %v", err)
	}
	if got := string(downloadedMain); got != "print('transfer')\n" {
		t.Fatalf("unexpected downloaded src/main.py content: %q", got)
	}

	downloadedEmptyDir := filepath.Join(downloadRoot, "docs", "empty")
	info, err := os.Stat(downloadedEmptyDir)
	if err != nil {
		t.Fatalf("stat downloaded empty dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected downloaded empty path to be a directory: %s", downloadedEmptyDir)
	}
}

func TestRootSliceGenesisPathsNormalized(t *testing.T) {
	t.Setenv("RUN_INTEGRATION_TESTS", "")
	t.Setenv("SKIP_GIT_POPULATION", "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}
	if err := sliceservice.RunGenesisInit(ctx, st); err != nil {
		t.Fatalf("failed to run genesis init: %v", err)
	}

	repoRoot := runGitOrFail(t, ".", "rev-parse", "--show-toplevel")
	readmePath := filepath.Join(repoRoot, "README.md")
	expectedContent, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("failed to read README.md: %v", err)
	}

	fileClient := fileservice.NewService(st)
	resp, err := fileClient.GetFile(ctx, &filev1.GetFileRequest{
		Path: "/o/genesis/projects/gitslice/README.md",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: "root_slice"},
		},
	})
	if err != nil {
		t.Fatalf("failed to fetch root slice file: %v", err)
	}
	if resp.File == nil {
		t.Fatalf("expected file response, got nil")
	}
	if resp.File.Path != "o/genesis/projects/gitslice/README.md" {
		t.Fatalf("expected normalized path, got %q", resp.File.Path)
	}
	if !bytes.Equal(resp.File.Content, expectedContent) {
		t.Fatalf("unexpected root slice content: %q", string(resp.File.Content))
	}
}

func TestGenesisCreatesFileChangeRecords(t *testing.T) {
	t.Setenv("RUN_INTEGRATION_TESTS", "")
	t.Setenv("SKIP_GIT_POPULATION", "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}
	if err := sliceservice.RunGenesisInit(ctx, st); err != nil {
		t.Fatalf("failed to run genesis init: %v", err)
	}

	// Get root slice to find head commit
	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to get root slice: %v", err)
	}

	metadata, err := st.GetSliceMetadata(ctx, rootSlice.ID)
	if err != nil {
		t.Fatalf("failed to get slice metadata: %v", err)
	}

	if metadata.HeadCommitHash == "" {
		t.Fatalf("expected head commit hash after genesis, got empty")
	}

	// Verify commit changes were recorded
	changes, err := st.GetCommitChanges(ctx, metadata.HeadCommitHash)
	if err != nil {
		t.Fatalf("failed to get commit changes: %v", err)
	}

	if len(changes) == 0 {
		t.Fatalf("expected file change records from genesis commit, got none")
	}

	// Verify all changes are of type "add" since genesis is the first commit
	for _, change := range changes {
		if change.ChangeType != models.ChangeTypeAdd {
			t.Errorf("expected change type 'add' for genesis file %s, got %q", change.Path, change.ChangeType)
		}
		if change.Author != "system" {
			t.Errorf("expected author 'system' for genesis file %s, got %q", change.Path, change.Author)
		}
		if change.Message != "Genesis: initialize repository files" {
			t.Errorf("expected genesis message for file %s, got %q", change.Path, change.Message)
		}
		if change.CommitHash != metadata.HeadCommitHash {
			t.Errorf("expected commit hash %s for file %s, got %s", metadata.HeadCommitHash, change.Path, change.CommitHash)
		}
	}

	// Verify we can also query file history for a specific file
	fileClient := fileservice.NewService(st)
	histResp, err := fileClient.GetFileHistory(ctx, &filev1.GetFileHistoryRequest{
		Path: "o/genesis/projects/gitslice/README.md",
	})
	if err != nil {
		t.Fatalf("failed to get file history for README.md: %v", err)
	}
	if len(histResp.Changes) == 0 {
		t.Fatalf("expected history entries for README.md, got none")
	}
	if histResp.Changes[0].ChangeType != filev1.ChangeType_CHANGE_TYPE_ADD {
		t.Errorf("expected CHANGE_TYPE_ADD for README.md genesis entry, got %v", histResp.Changes[0].ChangeType)
	}

	t.Logf("Genesis created %d file change records with commit %s", len(changes), metadata.HeadCommitHash)
}

func TestFileBrowserIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	if testStorage == nil {
		t.Fatalf("expected test storage to be initialized")
	}

	sliceID := fmt.Sprintf("slice-browser-%d", time.Now().UnixNano())
	files := []string{
		"apps/readme.md",
		"apps/components/button.jsx",
		"docs/guide.md",
	}

	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Browser",
		Files:     files,
		Owners:    []string{testUsername},
		CreatedBy: testUsername,
	}); err != nil {
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
	mustWriteSliceManifest(t, ctx, testStorage, sliceID, "apps/readme.md", readmeContent)

	fileClient := newFileClient(t)

	rootEntries, err := fileClient.ListEntries(ctx, &filev1.ListEntriesRequest{
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: sliceID},
		},
	})
	if err != nil {
		t.Fatalf("failed to list root entries: %v", err)
	}
	assertEntryNames(t, rootEntries.Entries, "apps", "docs")

	appEntries, err := fileClient.ListEntries(ctx, &filev1.ListEntriesRequest{
		Path: "apps",
		Version: &filev1.ListEntriesRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: sliceID},
		},
	})
	if err != nil {
		t.Fatalf("failed to list apps entries: %v", err)
	}
	assertEntryNames(t, appEntries.Entries, "readme.md", "components")

	fileResp, err := fileClient.GetFile(ctx, &filev1.GetFileRequest{
		Path: "apps/readme.md",
		Version: &filev1.GetFileRequest_SliceVersion{
			SliceVersion: &filev1.SliceVersion{SliceId: sliceID},
		},
	})
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

func TestGatewayHTTPListEntriesIntegration(t *testing.T) {
	if gatewayServiceURL == "" {
		t.Skip("gateway service not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if testStorage == nil {
		t.Fatalf("expected test storage to be initialized")
	}

	rootSlice, err := testStorage.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("failed to load root slice: %v", err)
	}

	files := []struct {
		path    string
		content []byte
	}{
		{path: "gateway/readme.md", content: []byte("# Gateway")},
		{path: "gateway/docs/guide.md", content: []byte("Guide")},
	}

	for _, file := range files {
		mustWriteSliceManifest(t, ctx, testStorage, rootSlice.ID, file.path, file.content)
		if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(rootSlice.ID, file.path),
			Path:     file.path,
			Type:     "file",
			ParentID: rootSlice.ID,
			Size:     int64(len(file.content)),
		}); err != nil {
			t.Fatalf("failed to add directory entry: %v", err)
		}
		if err := testStorage.AddFileToSlice(ctx, file.path, rootSlice.ID); err != nil {
			t.Fatalf("failed to add file to root slice: %v", err)
		}
	}

	rootEntries := fetchGatewayEntries(t, gatewayServiceURL+"/v1/files/entries")
	assertGatewayEntryNames(t, rootEntries.Entries, "gateway")

	gatewayEntries := fetchGatewayEntries(t, gatewayServiceURL+"/v1/files/entries/gateway")
	assertGatewayEntryNames(t, gatewayEntries.Entries, "readme.md", "docs")
}

type gatewayEntriesResponse struct {
	Entries []gatewayEntry `json:"entries"`
}

type gatewayEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type any    `json:"type"`
}

func fetchGatewayEntries(t *testing.T, url string) gatewayEntriesResponse {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("gateway request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("gateway status %d: %s", resp.StatusCode, string(body))
	}

	var payload gatewayEntriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("gateway decode failed: %v", err)
	}
	return payload
}

func assertGatewayEntryNames(t *testing.T, entries []gatewayEntry, expected ...string) {
	t.Helper()

	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		seen[entry.Name] = true
	}

	for _, name := range expected {
		if !seen[name] {
			t.Fatalf("expected gateway entry %q in list, got %#v", name, entries)
		}
	}
}

func TestBatchMergeClearsConflictsAndPromotesFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping batch merge integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(nil); err != nil {
		t.Fatalf("failed to initialize root slice: %v", err)
	}

	addr, srv, err := startGRPCServer(st)
	if err != nil {
		t.Fatalf("failed to start gRPC services: %v", err)
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

	if err := st.CreateSlice(ctx, &models.Slice{ID: sliceA, Name: "Batch A", Files: []string{"file-a"}, Owners: []string{testUsername}, CreatedBy: testUsername}); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if err := st.CreateSlice(ctx, &models.Slice{ID: sliceB, Name: "Batch B", Files: []string{"file-b"}, Owners: []string{testUsername}, CreatedBy: testUsername}); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	mergeResp, err := client.BatchMerge(ctx, &adminv1.BatchMergeRequest{})
	if err != nil {
		t.Fatalf("batch merge failed: %v", err)
	}
	if mergeResp.MergedSliceCount != 2 {
		t.Fatalf("expected 2 merged slices, got %d", mergeResp.MergedSliceCount)
	}

	rootMetadata, err := st.GetSliceMetadata(ctx, "root_slice")
	if err != nil {
		t.Fatalf("failed to load root metadata: %v", err)
	}
	if rootMetadata.HeadCommitHash != mergeResp.GlobalCommitHash {
		t.Fatalf("expected root commit %s, got %s", mergeResp.GlobalCommitHash, rootMetadata.HeadCommitHash)
	}
	if rootMetadata.ModifiedFilesCount != 2 {
		t.Fatalf("expected 2 modified files in root metadata, got %d", rootMetadata.ModifiedFilesCount)
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

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	output := runCLIOrFail(t, workdir, "init", sliceArg)
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
	ctx = withTestUser(ctx)

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
	sliceID := fmt.Sprintf("slice-global-%d", time.Now().UnixNano())

	createSliceFromRoot(t, sliceID, "")

	adminClient := newAdminClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

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

func TestPostgresRestartPersistsEndToEnd(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres restart persistence test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	objectStore := storage.NewInMemoryObjectStore()
	namespace := fmt.Sprintf("restart-e2e-%d", time.Now().UnixNano())
	st, err := storage.NewPostgresNativeStorage(ctx, dsn, objectStore, namespace)
	if err != nil {
		t.Fatalf("failed to create postgres storage: %v", err)
	}

	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("failed to init root slice: %v", err)
	}

	addr, srv, err := startGRPCServer(st)
	if err != nil {
		t.Fatalf("failed to start gRPC services: %v", err)
	}
	defer srv.GracefulStop()

	sliceConn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial slice service: %v", err)
	}
	defer sliceConn.Close()

	sliceClient := slicev1.NewSliceServiceClient(sliceConn)

	sliceID := fmt.Sprintf("restart-slice-%d", time.Now().UnixNano())
	fileID := fmt.Sprintf("persist-%d.txt", time.Now().UnixNano())
	if err := st.CreateSlice(ctx, &models.Slice{ID: sliceID, Name: "Persist", Files: []string{fileID}, Owners: []string{testUsername}, CreatedBy: testUsername}); err != nil {
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

	if err := st.Close(); err != nil {
		t.Fatalf("failed closing initial storage: %v", err)
	}

	st, err = storage.NewPostgresNativeStorage(ctx, dsn, objectStore, namespace)
	if err != nil {
		t.Fatalf("failed to reopen postgres storage: %v", err)
	}
	defer st.Close()

	addr2, srv2, err := startGRPCServer(st)
	if err != nil {
		t.Fatalf("failed to start gRPC services after restart: %v", err)
	}
	defer srv2.GracefulStop()

	sliceConn2, err := grpc.DialContext(ctx, addr2, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial restarted slice service: %v", err)
	}
	defer sliceConn2.Close()
	adminConn2, err := grpc.DialContext(ctx, addr2, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial restarted admin service: %v", err)
	}
	defer adminConn2.Close()

	sliceClient2 := slicev1.NewSliceServiceClient(sliceConn2)
	adminClient2 := adminv1.NewAdminServiceClient(adminConn2)

	globalState, err := adminClient2.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
	if err != nil {
		t.Fatalf("failed to read global state after restart: %v", err)
	}
	if globalState.GlobalCommitHash != mergeResp.NewCommitHash {
		t.Fatalf("expected global commit %s after restart, got %s", mergeResp.NewCommitHash, globalState.GlobalCommitHash)
	}

	sliceState, err := sliceClient2.GetSliceState(ctx, &slicev1.StateRequest{SliceId: sliceID})
	if err != nil {
		t.Fatalf("failed to read slice state after restart: %v", err)
	}
	if sliceState.LatestCommitHash != mergeResp.NewCommitHash {
		t.Fatalf("expected slice head %s after restart, got %s", mergeResp.NewCommitHash, sliceState.LatestCommitHash)
	}
}

func TestSlicePushLocksAndAutoPromotion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	adminClient := newAdminClient(t)
	sliceClient := newSliceClient(t)

	sharedFile := fmt.Sprintf("lock-shared-%d.txt", time.Now().UnixNano())
	sliceA := fmt.Sprintf("lock-a-%d", time.Now().UnixNano())
	sliceB := fmt.Sprintf("lock-b-%d", time.Now().UnixNano())

	if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceA, Name: "LockA", Files: []string{sharedFile}, Owners: []string{testUsername}, CreatedBy: testUsername}); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceB, Name: "LockB", Files: []string{sharedFile}, Owners: []string{testUsername}, CreatedBy: testUsername}); err != nil {
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

	var stateResp *adminv1.GlobalStateResponse
	if err := waitForCondition(2*time.Second, 25*time.Millisecond, func() (bool, error) {
		resp, err := adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
		if err != nil {
			return false, err
		}
		stateResp = resp
		if resp.GlobalCommitHash != mergeResp.NewCommitHash {
			return false, nil
		}
		for _, entry := range resp.History {
			if entry.CommitHash != mergeResp.NewCommitHash {
				continue
			}
			for _, id := range entry.MergedSliceIds {
				if id == sliceA {
					return true, nil
				}
			}
		}
		return false, nil
	}); err != nil {
		gotHead := ""
		if stateResp != nil {
			gotHead = stateResp.GlobalCommitHash
		}
		t.Fatalf("expected promoted commit %s and slice %s in global state, got head=%s: %v", mergeResp.NewCommitHash, sliceA, gotHead, err)
	}

	if err := waitForCondition(2*time.Second, 25*time.Millisecond, func() (bool, error) {
		rootState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: "root_slice"})
		if err != nil {
			return false, err
		}
		return rootState.LatestCommitHash == mergeResp.NewCommitHash, nil
	}); err != nil {
		t.Fatalf("expected root slice head %s after promotion: %v", mergeResp.NewCommitHash, err)
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
	ctx = withTestUser(ctx)

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

		if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceID, Name: fmt.Sprintf("Concurrent-%d", i), Files: []string{file}, Owners: []string{testUsername}, CreatedBy: testUsername}); err != nil {
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

	var globalState *adminv1.GlobalStateResponse
	if err := waitForCondition(3*time.Second, 25*time.Millisecond, func() (bool, error) {
		resp, err := adminClient.GetGlobalState(ctx, &adminv1.GlobalStateRequest{IncludeHistory: true})
		if err != nil {
			return false, err
		}
		globalState = resp
		if len(resp.History) < len(initialState.History)+mergeCount {
			return false, nil
		}
		historyCommits := make(map[string][]string, len(resp.History))
		for _, entry := range resp.History {
			historyCommits[entry.CommitHash] = entry.MergedSliceIds
		}
		for sliceID, commitHash := range commits {
			mergedSlices, ok := historyCommits[commitHash]
			if !ok {
				return false, nil
			}
			found := false
			for _, id := range mergedSlices {
				if id == sliceID {
					found = true
					break
				}
			}
			if !found {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		gotLen := 0
		if globalState != nil {
			gotLen = len(globalState.History)
		}
		t.Fatalf("expected %d new promoted history entries after concurrent merges, got %d: %v", mergeCount, gotLen-len(initialState.History), err)
	}

	rootState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: "root_slice"})
	if err != nil {
		t.Fatalf("failed to fetch root slice state: %v", err)
	}
	if rootState.LatestCommitHash != globalState.GlobalCommitHash {
		t.Fatalf("expected root slice head %s to match global %s", rootState.LatestCommitHash, globalState.GlobalCommitHash)
	}

	historyCommits := make(map[string][]string, len(globalState.History))
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

func TestAgentSessionLifecycleAndWSReplayIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	sliceID := fmt.Sprintf("agent-ws-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent WS",
		Files:     []string{},
		Owners:    []string{testUsername},
		CreatedBy: testUsername,
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	sessionID := createAgentSessionViaHTTP(t, sliceID, "integration-env", "codex")
	waitForAgentSessionState(t, sessionID, "running", 4*time.Second)

	token := mintAgentTokenViaHTTP(t, sessionID)
	wsURL := buildAgentWSURL(sessionID, token, 0)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect websocket: %v", err)
	}
	maxSeq := uint64(0)
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	if err := conn.WriteJSON(map[string]any{
		"stream": "control",
		"type":   "ping",
		"payload": map[string]string{
			"nonce": "it-nonce",
		},
	}); err != nil {
		t.Fatalf("write ping failed: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"stream": "pty",
		"type":   "stdin",
		"payload": map[string]string{
			"data": "echo replay-test\n",
		},
	}); err != nil {
		t.Fatalf("write stdin failed: %v", err)
	}

	gotPong := false
	gotStdout := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && (!gotPong || !gotStdout) {
		var frame struct {
			Seq     uint64                 `json:"seq"`
			Stream  string                 `json:"stream"`
			Type    string                 `json:"type"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("read websocket frame failed: %v", err)
		}
		if frame.Seq > maxSeq {
			maxSeq = frame.Seq
		}
		if frame.Stream == "control" && frame.Type == "pong" {
			if nonce, ok := frame.Payload["nonce"].(string); ok && nonce == "it-nonce" {
				gotPong = true
			}
		}
		if frame.Stream == "pty" && frame.Type == "stdout" {
			if data, ok := frame.Payload["data"].(string); ok && strings.Contains(data, "replay-test") {
				gotStdout = true
			}
		}
	}
	if !gotPong || !gotStdout {
		t.Fatalf("expected pong and stdout frames, got pong=%v stdout=%v", gotPong, gotStdout)
	}
	_ = conn.Close()

	if maxSeq < 2 {
		t.Fatalf("expected replayable seq values, got %d", maxSeq)
	}

	token2 := mintAgentTokenViaHTTP(t, sessionID)
	replayURL := buildAgentWSURL(sessionID, token2, maxSeq-1)
	replayConn, _, err := websocket.DefaultDialer.Dial(replayURL, nil)
	if err != nil {
		t.Fatalf("failed to reconnect websocket: %v", err)
	}
	defer replayConn.Close()
	_ = replayConn.SetReadDeadline(time.Now().Add(3 * time.Second))

	var replayFrame struct {
		Seq    uint64 `json:"seq"`
		Stream string `json:"stream"`
		Type   string `json:"type"`
	}
	if err := replayConn.ReadJSON(&replayFrame); err != nil {
		t.Fatalf("failed to read replay frame: %v", err)
	}
	if replayFrame.Seq <= maxSeq-1 {
		t.Fatalf("expected replay seq > %d, got %d", maxSeq-1, replayFrame.Seq)
	}

	stopAgentSessionViaHTTP(t, sessionID, "integration_done")
	waitForAgentSessionState(t, sessionID, "stopped", 4*time.Second)
}

func TestAgentSessionTokenReuseRejectedIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	sliceID := fmt.Sprintf("agent-token-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Token",
		Files:     []string{},
		Owners:    []string{testUsername},
		CreatedBy: testUsername,
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	sessionID := createAgentSessionViaHTTP(t, sliceID, "integration-env", "codex")
	waitForAgentSessionState(t, sessionID, "running", 4*time.Second)

	token := mintAgentTokenViaHTTP(t, sessionID)
	wsURL := buildAgentWSURL(sessionID, token, 0)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first websocket dial failed: %v", err)
	}
	_ = conn.Close()

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatalf("expected second websocket dial with same token to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on token reuse, got resp=%v err=%v", resp, err)
	}

	stopAgentSessionViaHTTP(t, sessionID, "integration_done")
	waitForAgentSessionState(t, sessionID, "stopped", 4*time.Second)
}

func TestAgentSessionClaudeStreamIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	sliceID := fmt.Sprintf("agent-claude-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Claude",
		Files:     []string{},
		Owners:    []string{testUsername},
		CreatedBy: testUsername,
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	sessionID := createAgentSessionViaHTTP(t, sliceID, "integration-env", "claude")
	waitForAgentSessionState(t, sessionID, "running", 4*time.Second)

	token := mintAgentTokenViaHTTP(t, sessionID)
	wsURL := buildAgentWSURL(sessionID, token, 0)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect websocket: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))

	if err := conn.WriteJSON(map[string]any{
		"stream": "agent",
		"type":   "input",
		"payload": map[string]string{
			"text": "Explain this diff",
		},
	}); err != nil {
		t.Fatalf("write agent/input failed: %v", err)
	}

	gotClaudeFinal := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && !gotClaudeFinal {
		var frame struct {
			Stream  string                 `json:"stream"`
			Type    string                 `json:"type"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("read websocket frame failed: %v", err)
		}
		if frame.Stream == "agent" && frame.Type == "output_final" {
			text, _ := frame.Payload["text"].(string)
			if strings.Contains(text, "Claude completed request") {
				gotClaudeFinal = true
			}
		}
	}
	if !gotClaudeFinal {
		t.Fatalf("expected claude output_final frame")
	}

	stopAgentSessionViaHTTP(t, sessionID, "integration_done")
	waitForAgentSessionState(t, sessionID, "stopped", 4*time.Second)
}

func TestAgentSessionCreateUnknownEnvironmentIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	sliceID := fmt.Sprintf("agent-env-missing-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Missing Env",
		Files:     []string{},
		Owners:    []string{testUsername},
		CreatedBy: testUsername,
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	body := map[string]any{
		"sliceId":     sliceID,
		"environment": "missing-integration-env",
		"agentType":   "codex",
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+testUsername)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 for unknown environment, got %d body=%s", resp.StatusCode, string(data))
	}
}

func TestAgentSessionCreateDisallowedAgentTypeIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withTestUser(ctx)

	if err := testStorage.CreateEnvironment(ctx, &models.Environment{
		Name:              "integration-codex-only",
		DisplayName:       "Integration Codex Only",
		Provider:          "e2b",
		ProviderID:        "tmpl-integration",
		Region:            "us-west-2",
		DefaultAgentType:  "codex",
		AllowedAgentTypes: []string{"codex"},
		CreatedBy:         testUsername,
	}); err != nil && err != storage.ErrEntryExists {
		t.Fatalf("failed to create codex-only environment: %v", err)
	}

	sliceID := fmt.Sprintf("agent-disallowed-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Disallowed Type",
		Files:     []string{},
		Owners:    []string{testUsername},
		CreatedBy: testUsername,
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	body := map[string]any{
		"sliceId":     sliceID,
		"environment": "integration-codex-only",
		"agentType":   "claude",
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+testUsername)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 for disallowed agent type, got %d body=%s", resp.StatusCode, string(data))
	}
}

func createAgentSessionViaHTTP(t *testing.T, sliceID, environment, agentType string) string {
	t.Helper()
	if strings.TrimSpace(environment) != "" {
		ensureIntegrationEnvironment(t, environment)
	}
	if strings.TrimSpace(agentType) == "" {
		agentType = "codex"
	}
	body := map[string]any{
		"sliceId":     sliceID,
		"environment": environment,
		"agentType":   agentType,
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+testUsername)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected create status 201/200, got %d body=%s", resp.StatusCode, string(data))
	}
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	if out.SessionID == "" {
		t.Fatalf("missing sessionId")
	}
	return out.SessionID
}

func ensureIntegrationEnvironment(t *testing.T, name string) {
	t.Helper()
	ctx := withTestUser(context.Background())
	err := testStorage.CreateEnvironment(ctx, &models.Environment{
		Name:        name,
		DisplayName: "Integration Environment",
		Provider:    "e2b",
		ProviderID:  "tmpl-integration",
		Region:      "us-west-2",
		CreatedBy:   testUsername,
	})
	if err != nil && err != storage.ErrEntryExists {
		t.Fatalf("failed to create integration environment: %v", err)
	}
}

func mintAgentTokenViaHTTP(t *testing.T, sessionID string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions/"+sessionID+"/token", nil)
	req.Header.Set("Authorization", "User "+testUsername)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected token status 200, got %d body=%s", resp.StatusCode, string(data))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode token response failed: %v", err)
	}
	if out.Token == "" {
		t.Fatalf("missing token")
	}
	return out.Token
}

func stopAgentSessionViaHTTP(t *testing.T, sessionID string, reason string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"reason": reason})
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions/"+sessionID+"/stop", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+testUsername)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stop request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected stop status 202/200, got %d body=%s", resp.StatusCode, string(data))
	}
}

func waitForAgentSessionState(t *testing.T, sessionID string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, gatewayServiceURL+"/v1/agent-sessions/"+sessionID, nil)
		req.Header.Set("Authorization", "User "+testUsername)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var out struct {
				State string `json:"state"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&out)
			_ = resp.Body.Close()
			if out.State == want {
				return
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for session %s state %s", sessionID, want)
}

func buildAgentWSURL(sessionID string, token string, lastSeq uint64) string {
	base, _ := url.Parse(gatewayServiceURL)
	base.Scheme = "ws"
	base.Path = "/ws/sessions/" + sessionID
	q := base.Query()
	q.Set("token", token)
	q.Set("lastSeq", fmt.Sprintf("%d", lastSeq))
	base.RawQuery = q.Encode()
	return base.String()
}
