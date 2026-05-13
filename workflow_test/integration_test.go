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
	"sort"
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
	ciservice "github.com/niczy/gitslice/services/ci"
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
	ciservice.RegisterGRPCServer(srv, st)
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

	binaryPath := filepath.Join(tmpDir, "gs")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./gs")
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

func waitForMergedChangesetMessage(ctx context.Context, st storage.Storage, sliceID, needle string, timeout, interval time.Duration) error {
	return waitForCondition(timeout, interval, func() (bool, error) {
		changesets, err := st.ListChangesets(ctx, sliceID, nil, 20)
		if err != nil {
			return false, err
		}
		for _, cs := range changesets {
			if cs == nil {
				continue
			}
			if cs.Status == models.ChangesetStatusMerged && strings.Contains(cs.Message, needle) {
				return true, nil
			}
		}
		return false, nil
	})
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
	return runCLIWithDirInputEnvLegacyUser(workdir, input, env, true, testUsername, args...)
}

func runCLIWithDirInputEnvNoLegacyUser(workdir, input string, env map[string]string, args ...string) (string, error) {
	return runCLIWithDirInputEnvLegacyUser(workdir, input, env, false, "", args...)
}

func runCLIWithDirInputEnvLegacy(workdir, input string, env map[string]string, includeLegacyUser bool, args ...string) (string, error) {
	return runCLIWithDirInputEnvLegacyUser(workdir, input, env, includeLegacyUser, testUsername, args...)
}

func runCLIWithDirInputEnvLegacyUser(workdir, input string, env map[string]string, includeLegacyUser bool, legacyUser string, args ...string) (string, error) {
	stdout, stderr, err := runCLIWithDirInputEnvLegacyUserStreams(workdir, input, env, includeLegacyUser, legacyUser, args...)
	return stdout + stderr, err
}

func runCLIWithDirInputEnvLegacyUserStreams(workdir, input string, env map[string]string, includeLegacyUser bool, legacyUser string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fullArgs := []string{
		"--tls=false",
		"--account-addr", grpcServiceAddr,
		"--slice-addr", grpcServiceAddr,
		"--admin-addr", grpcServiceAddr,
		"--file-addr", grpcServiceAddr,
		"--fs-addr", grpcServiceAddr,
	}
	if includeLegacyUser {
		fullArgs = append(fullArgs, "--user", legacyUser)
	}
	fullArgs = append(fullArgs, args...)
	cmd := exec.CommandContext(ctx, cliBinaryPath, fullArgs...)
	if workdir != "" {
		cmd.Dir = workdir
	}
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	cmd.Env = os.Environ()
	if env == nil {
		env = map[string]string{}
	}
	if _, ok := env["GS_DISABLE_DIRTY_TRACKER"]; !ok {
		env["GS_DISABLE_DIRTY_TRACKER"] = "1"
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
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

	output, err := runCLIWithDirForTest(t, workdir, args...)
	if err != nil {
		t.Fatalf("CLI command failed: %v\nOutput:\n%s\n%s", err, output, workflowFailureDiagnostics(t, workdir, args...))
	}

	return output
}

func runCLIWithDirForTest(t *testing.T, workdir string, args ...string) (string, error) {
	t.Helper()
	return runCLIWithDirInputEnvLegacyUser(workdir, "", workflowProcessEnv(t, nil), true, workflowUsername(t), args...)
}

func workflowFailureDiagnostics(t *testing.T, workdir string, args ...string) string {
	t.Helper()

	env := workflowEnvForTest(t)
	var b strings.Builder
	fmt.Fprintf(&b, "Diagnostics:\n")
	fmt.Fprintf(&b, "  Test user: %s\n", env.Username)
	fmt.Fprintf(&b, "  Home: %s\n", env.HomeDir)
	if workdir == "" {
		fmt.Fprintf(&b, "  Workdir: (process cwd)\n")
	} else {
		fmt.Fprintf(&b, "  Workdir: %s\n", workdir)
	}
	if len(args) > 0 {
		fmt.Fprintf(&b, "  Args: %s\n", strings.Join(args, " "))
	}
	appendDiagnosticSnapshot(&b, "Workdir snapshot", workdir)
	if workdir != "" {
		appendDiagnosticFile(&b, "dirty_state.json", filepath.Join(workdir, ".gs", "dirty_state.json"))
		appendDiagnosticFile(&b, "dirty_paths.json", filepath.Join(workdir, ".gs", "dirty_paths.json"))
		appendDiagnosticFile(&b, "dirty_watcher.log", filepath.Join(workdir, ".gs", "dirty_watcher.log"))
	}
	appendDiagnosticSnapshot(&b, "Home .gitslice snapshot", filepath.Join(env.HomeDir, ".gitslice"))
	return b.String()
}

func appendDiagnosticSnapshot(b *strings.Builder, label, root string) {
	if root == "" {
		return
	}
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(b, "  %s: missing\n", label)
			return
		}
		fmt.Fprintf(b, "  %s: stat failed: %v\n", label, err)
		return
	}
	if !info.IsDir() {
		fmt.Fprintf(b, "  %s: %s (%d bytes)\n", label, root, info.Size())
		return
	}
	fmt.Fprintf(b, "  %s:\n", label)
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(b, "    ! %s (%v)\n", path, err)
			return nil
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		if rel == "." {
			return nil
		}
		depth := strings.Count(rel, string(os.PathSeparator))
		if depth > 3 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= 40 {
			fmt.Fprintf(b, "    ...\n")
			return errors.New("limit reached")
		}
		suffix := ""
		if d.IsDir() {
			suffix = "/"
		} else if info, statErr := d.Info(); statErr == nil {
			suffix = fmt.Sprintf(" (%d bytes)", info.Size())
		}
		fmt.Fprintf(b, "    - %s%s\n", filepath.ToSlash(rel), suffix)
		count++
		return nil
	})
}

func appendDiagnosticFile(b *strings.Builder, label, path string) {
	if path == "" {
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(b, "  %s: read failed: %v\n", label, err)
		}
		return
	}
	if len(content) > 2048 {
		content = content[:2048]
	}
	fmt.Fprintf(b, "  %s:\n%s\n", label, string(content))
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

func extractCreatedSliceSlug(output string) string {
	re := regexp.MustCompile(`Slug: ([^\n]+)`)
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

func extractFilesystemBatchCommitHash(output string) string {
	re := regexp.MustCompile(`Batch commit: ([^\n]+)`)
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

func resolveAllConflicts(ctx context.Context, t *testing.T, client slicev1.SliceServiceClient) {
	resp, err := client.GetConflicts(ctx, &slicev1.ConflictsRequest{})
	if err != nil {
		t.Fatalf("failed to list conflicts: %v", err)
	}

	for _, conflict := range resp.Conflicts {
		preferred := ""
		if len(conflict.ConflictingSliceIds) > 0 {
			preferred = conflict.ConflictingSliceIds[0]
		}

		if _, err := client.ResolveConflict(ctx, &slicev1.ResolveConflictRequest{FileId: conflict.FileId, PreferredSliceId: preferred}); err != nil {
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
	if !strings.Contains(output, "Initialized empty gitslice checkout") {
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

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID, "--wait")
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("Expected merge success, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "list", "--status", "merged")
	if !strings.Contains(output, changesetID) {
		t.Fatalf("Expected merged changeset in list output, got: %s", output)
	}
}

func TestRootSliceAndSliceCreateWorkflow(t *testing.T) {
	workdir := t.TempDir()

	output := runCLIOrFail(t, workdir, "root")
	if !strings.Contains(output, "Root Slice ID: root") {
		t.Fatalf("Expected root slice info, got: %s", output)
	}

	rootSliceArg := sliceIDArg("root")
	output = runCLIOrFail(t, workdir, "init", rootSliceArg)
	if !strings.Contains(output, "Initialized empty gitslice checkout") {
		t.Fatalf("Expected init output, got: %s", output)
	}

	srcFolder := fmt.Sprintf("src_%d", time.Now().UnixNano())
	srcFile := filepath.Join(workdir, filepath.FromSlash(srcFolder+"/main.go"))
	if err := os.MkdirAll(filepath.Dir(srcFile), 0o755); err != nil {
		t.Fatalf("failed to create src folder: %v", err)
	}
	if err := os.WriteFile(srcFile, nil, 0o644); err != nil {
		t.Fatalf("failed to write src file: %v", err)
	}
	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "Create src folder", "--files", srcFolder+"/main.go")
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("Failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID, "--wait")
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("Expected merge success, got: %s", output)
	}

	newSliceID := fmt.Sprintf("slice-create-%d", time.Now().UnixNano())
	output = runCLIOrFail(t, workdir, "slice", "create", newSliceID, srcFolder)
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
	if !strings.Contains(output, "Initialized empty gitslice checkout") {
		t.Fatalf("Expected init output, got: %s", output)
	}

	subFolder := fmt.Sprintf("components_%d", time.Now().UnixNano())
	subFile := filepath.Join(newSliceWorkdir, filepath.FromSlash(subFolder+"/index.ts"))
	if err := os.MkdirAll(filepath.Dir(subFile), 0o755); err != nil {
		t.Fatalf("failed to create components folder: %v", err)
	}
	if err := os.WriteFile(subFile, nil, 0o644); err != nil {
		t.Fatalf("failed to write components file: %v", err)
	}
	output = runCLIOrFail(t, newSliceWorkdir, "changeset", "create", "--message", "Create components subfolder", "--files", subFolder+"/index.ts")
	changesetID = extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("Failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, newSliceWorkdir, "changeset", "merge", changesetID)
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("Expected merge success for subfolder, got: %s", output)
	}
}

func TestCheckoutWritesNoGitMetadata(t *testing.T) {
	workdir := t.TempDir()
	sliceID := fmt.Sprintf("slice-checkout-%d", time.Now().UnixNano())

	createSliceFromRoot(t, sliceID, "")
	sliceArg := sliceIDArg(sliceID)

	resp := runCLIJSONOrFail[sliceCheckoutJSON](t, workdir, "slice", "checkout", sliceArg, "--here")
	if resp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected checkout to skip git metadata, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".gs", "index")); err != nil {
		t.Fatalf("expected checkout index to be written, err=%v", err)
	}
}

func TestCheckoutReusesCachedBlocks(t *testing.T) {
	username := fmt.Sprintf("ccu%d", time.Now().UnixNano())
	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	ctx := context.Background()
	content := append([]byte{}, bytes.Repeat([]byte("A"), storage.DefaultFileBlockSize)...)
	content = append(content, bytes.Repeat([]byte("B"), storage.DefaultFileBlockSize)...)
	content = append(content, bytes.Repeat([]byte("C"), 97)...)
	homeSlice, err := homeslice.EnsureUserHomeSlice(ctx, testStorage, username)
	if err != nil {
		t.Fatalf("ensure home slice: %v", err)
	}
	storedDir := fmt.Sprintf("%s/checkout-cache-%d", username, time.Now().UnixNano())
	storedPath := storedDir + "/large.txt"
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID(homeSlice.ID, storedPath),
		Path:     storedPath,
		Type:     "file",
		ParentID: homeSlice.ID,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}
	manifest, err := storage.WriteSliceFileManifest(ctx, testStorage, homeSlice.ID, storedPath, content)
	if err != nil {
		t.Fatalf("WriteSliceFileManifest failed: %v", err)
	}
	if err := testStorage.AddFileToSlice(ctx, storedPath, homeSlice.ID); err != nil {
		t.Fatalf("AddFileToSlice failed: %v", err)
	}

	checkoutDir := filepath.Join(t.TempDir(), "checkout-1")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatalf("mkdir checkout dir: %v", err)
	}
	output := runCLIForUser(checkoutDir, "slice", "checkout", homeslice.IDForUsername(username), "--here", "--json")
	var checkoutResp sliceCheckoutJSON
	if err := json.Unmarshal([]byte(output), &checkoutResp); err != nil {
		t.Fatalf("decode first checkout JSON: %v\nOutput:\n%s", err, output)
	}
	if checkoutResp.SliceID != homeslice.IDForUsername(username) {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}

	cachePath := filepath.Join(homeDir, ".gitslice", "cache", "objects", manifest.Hash)
	if err := os.Remove(cachePath); err != nil {
		t.Fatalf("remove cached file hash %s: %v", manifest.Hash, err)
	}

	checkoutDir2 := filepath.Join(t.TempDir(), "checkout-2")
	if err := os.MkdirAll(checkoutDir2, 0o755); err != nil {
		t.Fatalf("mkdir second checkout dir: %v", err)
	}
	output = runCLIForUser(checkoutDir2, "slice", "checkout", homeslice.IDForUsername(username), "--here", "--json")
	if err := json.Unmarshal([]byte(output), &checkoutResp); err != nil {
		t.Fatalf("decode second checkout JSON: %v\nOutput:\n%s", err, output)
	}
	if checkoutResp.SliceID != homeslice.IDForUsername(username) {
		t.Fatalf("expected second checkout output, got: %+v", checkoutResp)
	}
	if checkoutResp.CacheHits != 3 {
		t.Fatalf("expected second checkout to report block cache hits, got: %+v", checkoutResp)
	}

	checkedOutPath := filepath.Join(checkoutDir2, storedPath)
	got, err := os.ReadFile(checkedOutPath)
	if err != nil {
		t.Fatalf("read checked out file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("checked out content mismatch")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected reconstructed full file to be cached again: %v", err)
	}
}

func TestSliceSyncNoGitUpdatesCurrentCheckout(t *testing.T) {
	ctx := context.Background()
	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	sliceID := fmt.Sprintf("slice-sync-nogit-%d", time.Now().UnixNano())
	createSliceFromRoot(t, sliceID, "")

	writeSliceFile := func(filePath string, content []byte) string {
		t.Helper()
		entry, err := testStorage.GetEntryByPath(ctx, sliceID, filePath)
		if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			t.Fatalf("get entry %s: %v", filePath, err)
		}
		if entry == nil {
			if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
				ID:       common.GenerateEntryID(sliceID, filePath),
				Path:     filePath,
				Type:     "file",
				ParentID: sliceID,
				Size:     int64(len(content)),
			}); err != nil {
				t.Fatalf("add entry %s: %v", filePath, err)
			}
		} else {
			entry.Size = int64(len(content))
			if err := testStorage.UpdateEntry(ctx, entry); err != nil {
				t.Fatalf("update entry %s: %v", filePath, err)
			}
		}
		manifest, err := storage.WriteSliceFileManifest(ctx, testStorage, sliceID, filePath, content)
		if err != nil {
			t.Fatalf("write manifest %s: %v", filePath, err)
		}
		if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
			t.Fatalf("add file to slice %s: %v", filePath, err)
		}
		return manifest.Hash
	}

	removeSliceFile := func(filePath string) {
		t.Helper()
		entry, err := testStorage.GetEntryByPath(ctx, sliceID, filePath)
		if err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			t.Fatalf("get entry for delete %s: %v", filePath, err)
		}
		if entry != nil {
			if err := testStorage.DeleteEntry(ctx, entry.ID); err != nil {
				t.Fatalf("DeleteEntry(%s) failed: %v", filePath, err)
			}
		}
		if err := testStorage.DeleteFileManifest(ctx, sliceID, filePath); err != nil && !errors.Is(err, storage.ErrEntryNotFound) {
			t.Fatalf("DeleteFileManifest(%s) failed: %v", filePath, err)
		}
		if err := testStorage.RemoveFileFromSlice(ctx, filePath, sliceID); err != nil {
			t.Fatalf("remove file from slice %s: %v", filePath, err)
		}
	}

	setSliceHead := func(commitHash, parentHash string, files map[string]string) {
		t.Helper()
		now := time.Now()
		if err := testStorage.AddSliceCommit(ctx, sliceID, &models.Commit{
			CommitHash: commitHash,
			ParentHash: parentHash,
			Timestamp:  now,
			Message:    "test commit " + commitHash,
		}); err != nil {
			t.Fatalf("AddSliceCommit(%s) failed: %v", commitHash, err)
		}
		if err := testStorage.SaveCommitSnapshot(ctx, &models.CommitSnapshot{
			CommitHash: commitHash,
			SliceID:    sliceID,
			Files:      files,
			Timestamp:  now,
		}); err != nil {
			t.Fatalf("SaveCommitSnapshot(%s) failed: %v", commitHash, err)
		}
		metadata, err := testStorage.GetSliceMetadata(ctx, sliceID)
		if err != nil {
			t.Fatalf("GetSliceMetadata(%s) failed: %v", sliceID, err)
		}
		metadata.HeadCommitHash = commitHash
		metadata.LastModified = now
		metadata.ModifiedFiles = make([]string, 0, len(files))
		for filePath := range files {
			metadata.ModifiedFiles = append(metadata.ModifiedFiles, filePath)
		}
		sort.Strings(metadata.ModifiedFiles)
		metadata.ModifiedFilesCount = len(metadata.ModifiedFiles)
		if err := testStorage.UpdateSliceMetadata(ctx, sliceID, metadata); err != nil {
			t.Fatalf("UpdateSliceMetadata(%s) failed: %v", sliceID, err)
		}
	}

	readmePath := "docs/readme.md"
	removedPath := "docs/stale.txt"
	readmeV1Hash := writeSliceFile(readmePath, []byte("nogit v1\n"))
	staleHash := writeSliceFile(removedPath, []byte("remove me\n"))
	setSliceHead("nogit-commit-1", "", map[string]string{
		readmePath:  readmeV1Hash,
		removedPath: staleHash,
	})

	checkoutDir := t.TempDir()
	checkoutResp := runCLIJSONWithEnvOrFail[sliceCheckoutJSON](t, checkoutDir, env, "slice", "checkout", sliceIDArg(sliceID), "--here")
	if checkoutResp.SliceID != sliceID {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected default checkout to skip git metadata, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, ".gs", "index")); err != nil {
		t.Fatalf("expected checkout index file, err=%v", err)
	}

	readmeV2Hash := writeSliceFile(readmePath, []byte("nogit v2\n"))
	removeSliceFile(removedPath)
	setSliceHead("nogit-commit-2", "nogit-commit-1", map[string]string{
		readmePath: readmeV2Hash,
	})

	syncResp := runCLIJSONWithEnvOrFail[sliceSyncJSON](t, checkoutDir, env, "slice", "sync")
	if syncResp.SliceID != sliceID || syncResp.Status != "updated" {
		t.Fatalf("expected updated sync output, got: %+v", syncResp)
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected sync to keep checkout in no-git mode, err=%v", err)
	}

	got, err := os.ReadFile(filepath.Join(checkoutDir, readmePath))
	if err != nil {
		t.Fatalf("read no-git synced file: %v", err)
	}
	if string(got) != "nogit v2\n" {
		t.Fatalf("unexpected no-git sync content: %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, removedPath)); !os.IsNotExist(err) {
		t.Fatalf("expected stale file removal for no-git sync, err=%v", err)
	}

	repeatSyncResp := runCLIJSONWithEnvOrFail[sliceSyncJSON](t, checkoutDir, env, "slice", "sync")
	if repeatSyncResp.Status != "up to date" {
		t.Fatalf("expected no-git sync to report up-to-date on repeat, got: %+v", repeatSyncResp)
	}
}

func TestRootSliceEndToEndWorkflow(t *testing.T) {
	workdir := t.TempDir()

	output := runCLIOrFail(t, workdir, "root")
	if !strings.Contains(output, "Root Slice ID: root") {
		t.Fatalf("expected root slice info, got: %s", output)
	}

	rootSliceArg := sliceIDArg("root")
	output = runCLIOrFail(t, workdir, "init", rootSliceArg)
	if !strings.Contains(output, "Initialized empty gitslice checkout") {
		t.Fatalf("expected init output, got: %s", output)
	}

	rootSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	appsFolder := "apps-" + rootSuffix
	servicesFolder := "services-" + rootSuffix
	docsFolder := "docs-" + rootSuffix
	rootFiles := []string{appsFolder + "/README.md", servicesFolder + "/README.md", docsFolder + "/README.md"}
	for _, filePath := range rootFiles {
		localPath := filepath.Join(workdir, filepath.FromSlash(filePath))
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			t.Fatalf("failed to create root folder for %s: %v", filePath, err)
		}
		if err := os.WriteFile(localPath, nil, 0o644); err != nil {
			t.Fatalf("failed to write root file %s: %v", filePath, err)
		}
	}
	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "Add root folders", "--files", strings.Join(rootFiles, ","))
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID, "--wait")
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected merge success, got: %s", output)
	}

	rootCommit := extractCommitHash(output)
	if rootCommit == "" {
		t.Fatalf("expected root commit hash from merge output, got: %s", output)
	}

	sliceID := fmt.Sprintf("slice-apps-%d", time.Now().UnixNano())
	output = runCLIOrFail(t, workdir, "slice", "create", sliceID, appsFolder)
	if !strings.Contains(output, "Created slice: "+sliceID) {
		t.Fatalf("expected slice creation output, got: %s", output)
	}
	sliceID = extractCreatedSliceID(output)
	if sliceID == "" {
		t.Fatalf("failed to extract created slice ID from output: %s", output)
	}
	sliceSlug := extractCreatedSliceSlug(output)
	if sliceSlug == "" {
		t.Fatalf("failed to extract created slice slug from output: %s", output)
	}
	sliceArg := sliceIDArg(sliceID)

	sliceWorkdir := t.TempDir()
	checkoutResp := runCLIJSONOrFail[sliceCheckoutJSON](t, sliceWorkdir, "slice", "checkout", sliceSlug, "--here", "--files")
	if checkoutResp.SliceID != sliceID || len(checkoutResp.Files) == 0 {
		t.Fatalf("expected checkout output, got: %+v", checkoutResp)
	}
	foundApps := false
	for _, file := range checkoutResp.Files {
		if file.Path == appsFolder+"/README.md" && file.Size == 0 {
			foundApps = true
			break
		}
	}
	if !foundApps {
		t.Fatalf("expected focused file under %q in checkout output, got: %+v", appsFolder, checkoutResp)
	}

	appsReadmePath := filepath.Join(sliceWorkdir, filepath.FromSlash(appsFolder+"/readme.md"))
	if err := os.MkdirAll(filepath.Dir(appsReadmePath), 0o755); err != nil {
		t.Fatalf("failed to create apps readme folder: %v", err)
	}
	if err := os.WriteFile(appsReadmePath, []byte("apps readme\n"), 0o644); err != nil {
		t.Fatalf("failed to write apps readme: %v", err)
	}
	output = runCLIOrFail(t, sliceWorkdir, "changeset", "create", "--message", "Add apps readme", "--files", appsFolder+"/readme.md")
	changesetID = extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, sliceWorkdir, "changeset", "merge", changesetID, "--wait")
	if !strings.Contains(output, "MERGE_STATUS_SUCCESS") {
		t.Fatalf("expected merge success, got: %s", output)
	}

	sliceCommit := extractCommitHash(output)
	if sliceCommit == "" {
		t.Fatalf("expected slice commit hash from merge output, got: %s", output)
	}

	updatedSliceWorkdir := t.TempDir()
	checkoutResp = runCLIJSONOrFail[sliceCheckoutJSON](t, updatedSliceWorkdir, "slice", "checkout", sliceArg, "--here", "--files")
	if checkoutResp.Commit != sliceCommit {
		t.Fatalf("expected latest slice commit in checkout, got: %+v", checkoutResp)
	}
	foundApps = false
	for _, file := range checkoutResp.Files {
		if file.Path == appsFolder+"/README.md" && file.Size == 0 {
			foundApps = true
			break
		}
	}
	if !foundApps {
		t.Fatalf("expected focused file under %q in slice checkout, got: %+v", appsFolder, checkoutResp)
	}

	rootCheckoutArg := sliceIDArg("root")
	var rootCheckoutResp sliceCheckoutJSON
	if err := waitForCondition(2*time.Second, 50*time.Millisecond, func() (bool, error) {
		rootCheckoutDir := t.TempDir()
		var err error
		output, err = runCLIWithDirForTest(t, rootCheckoutDir, "slice", "checkout", rootCheckoutArg, "--here", "--files", "--json")
		if err != nil {
			return false, nil
		}
		if err := json.Unmarshal([]byte(output), &rootCheckoutResp); err != nil {
			return false, err
		}
		return rootCheckoutResp.Commit == sliceCommit, nil
	}); err != nil {
		t.Fatalf("expected root slice to promote latest commit (%s): %v\nOutput:\n%s", sliceCommit, err, output)
	}
	if rootCheckoutResp.Commit != sliceCommit {
		t.Fatalf("expected root slice to promote latest commit, got: %+v", rootCheckoutResp)
	}
	if rootCommit == sliceCommit {
		t.Fatalf("expected root commit to advance after slice merge, got same commit %s", rootCommit)
	}
}

func TestFilesystemCLIWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	username := fmt.Sprintf("fs-cli-user-%d", time.Now().UnixNano())
	ctx = withUsername(ctx, username)
	sliceClient := newSliceClient(t)
	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir CLI home: %v", err)
	}
	env := map[string]string{"HOME": homeDir}

	runCLIForUser := func(workdir string, args ...string) string {
		t.Helper()
		output, err := runCLIWithDirInputEnvLegacyUser(workdir, "", env, true, username, args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v\nOutput:\n%s", err, output)
		}
		return output
	}

	remoteDir := fmt.Sprintf("/%s/fs-cli-%d", username, time.Now().UnixNano())
	remoteFile := remoteDir + "/README.md"

	localFile := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(localFile, []byte("hello from fs cli\n"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	output := runCLIForUser("", "fs", "mkdir", remoteDir)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected mkdir commit output, got: %s", output)
	}

	output = runCLIForUser("", "fs", "write", remoteFile, "-f", localFile)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected write commit output, got: %s", output)
	}

	output = runCLIForUser("", "fs", "cat", remoteFile)
	if output != "hello from fs cli\n" {
		t.Fatalf("unexpected cat output: %q", output)
	}

	output = runCLIForUser("", "fs", "ls", remoteDir)
	if !strings.Contains(output, "README.md") {
		t.Fatalf("expected README.md in listing, got: %s", output)
	}

	output = runCLIForUser("", "fs", "stat", remoteFile)
	if !strings.Contains(output, "Type: file") {
		t.Fatalf("expected file stat output, got: %s", output)
	}

	output = runCLIForUser("", "fs", "snapshot", "-m", "initial")
	snapshotID := extractSnapshotID(output)
	if snapshotID == "" {
		t.Fatalf("failed to extract snapshot id from output: %s", output)
	}

	output = runCLIForUser("", "fs", "snapshots")
	if !strings.Contains(output, snapshotID) {
		t.Fatalf("expected snapshots output to include %s, got: %s", snapshotID, output)
	}

	if err := os.WriteFile(localFile, []byte("hello from fs cli v2\n"), 0o600); err != nil {
		t.Fatalf("rewrite local file: %v", err)
	}

	output = runCLIForUser("", "fs", "write", remoteFile, "-f", localFile)
	if !strings.Contains(output, "Commit: ") {
		t.Fatalf("expected second write commit output, got: %s", output)
	}
	secondWriteCommit := extractFilesystemCommitHash(output)
	if secondWriteCommit == "" {
		t.Fatalf("failed to extract fs commit from output: %s", output)
	}

	output = runCLIForUser("", "fs", "cat", remoteFile)
	if output != "hello from fs cli v2\n" {
		t.Fatalf("unexpected cat output after second write: %q", output)
	}

	output = runCLIForUser("", "fs", "log", "--limit", "3")
	if !strings.Contains(output, "commit "+secondWriteCommit) {
		t.Fatalf("expected fs log to include %s, got: %s", secondWriteCommit, output)
	}
	if !strings.Contains(output, "write "+remoteFile) {
		t.Fatalf("expected fs log to show visible write path, got: %s", output)
	}

	output = runCLIForUser("", "fs", "show", secondWriteCommit)
	if !strings.Contains(output, "MODIFY "+remoteFile) {
		t.Fatalf("expected fs show to include modified visible path, got: %s", output)
	}
	if !strings.Contains(output, "-hello from fs cli") || !strings.Contains(output, "+hello from fs cli v2") {
		t.Fatalf("expected fs show patch output, got: %s", output)
	}

	if err := waitForMergedChangesetMessage(ctx, testStorage, homeslice.IDForUsername(username), "write "+remoteFile, 2*time.Second, 50*time.Millisecond); err != nil {
		t.Fatalf("expected fs publish to create a merged changeset: %v", err)
	}
	if err := waitForCondition(2*time.Second, 50*time.Millisecond, func() (bool, error) {
		state, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: homeslice.IDForUsername(username)})
		if err != nil {
			return false, err
		}
		return state.GetLatestCommitHash() == secondWriteCommit, nil
	}); err != nil {
		t.Fatalf("expected home slice head to reach %s before diff: %v", secondWriteCommit, err)
	}

	checkoutDir := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatalf("mkdir checkout dir: %v", err)
	}
	output = runCLIForUser(checkoutDir, "slice", "checkout", homeslice.IDForUsername(username), "--here", "--json")
	var homeCheckoutResp sliceCheckoutJSON
	if err := json.Unmarshal([]byte(output), &homeCheckoutResp); err != nil {
		t.Fatalf("decode home checkout JSON: %v\nOutput:\n%s", err, output)
	}
	if homeCheckoutResp.SliceID != homeslice.IDForUsername(username) {
		t.Fatalf("expected home slice checkout output, got: %+v", homeCheckoutResp)
	}
	output = runCLIForUser(checkoutDir, "changeset", "list", "--status", "merged")
	if !strings.Contains(output, "write "+remoteFile) {
		t.Fatalf("expected merged publish changeset in list output, got: %s", output)
	}

	output = runCLIForUser("", "fs", "diff", snapshotID)
	if !strings.Contains(output, "README.md") || !strings.Contains(output, "MODIFY") {
		t.Fatalf("expected diff output for README.md, got: %s", output)
	}

	output = runCLIForUser("", "fs", "restore", snapshotID)
	if !strings.Contains(output, "Restored to "+snapshotID) {
		t.Fatalf("expected restore output, got: %s", output)
	}

	output = runCLIForUser("", "fs", "cat", remoteFile)
	if output != "hello from fs cli\n" {
		t.Fatalf("unexpected cat output after restore: %q", output)
	}
}

func TestFilesystemCLIBatchWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)
	username := workflowUsername(t)

	remoteDir := fmt.Sprintf("/%s/fs-batch-cli-%d", username, time.Now().UnixNano())
	remoteFinal := remoteDir + "/FINAL.md"
	localFile := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(localFile, []byte("hello from batch\n"), 0o600); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	batchFile := filepath.Join(t.TempDir(), "ops.jsonl")
	batchSpec := fmt.Sprintf(
		"{\"op\":\"mkdir\",\"path\":%q}\n{\"op\":\"write\",\"path\":%q,\"from\":%q}\n{\"op\":\"copy\",\"source_path\":%q,\"destination_path\":%q}\n{\"op\":\"edit\",\"path\":%q,\"edits\":[{\"old_text\":\"hello\",\"new_text\":\"batch\"}]}\n{\"op\":\"move\",\"source_path\":%q,\"destination_path\":%q}\n{\"op\":\"delete\",\"path\":%q}\n",
		remoteDir,
		remoteDir+"/README.md",
		localFile,
		remoteDir+"/README.md",
		remoteDir+"/COPY.md",
		remoteDir+"/COPY.md",
		remoteDir+"/COPY.md",
		remoteFinal,
		remoteDir+"/README.md",
	)
	if err := os.WriteFile(batchFile, []byte(batchSpec), 0o600); err != nil {
		t.Fatalf("write batch file: %v", err)
	}

	output := runCLIOrFail(t, "", "fs", "batch", "-f", batchFile, "-m", "batch workflow")
	commitHash := extractFilesystemBatchCommitHash(output)
	if commitHash == "" {
		t.Fatalf("expected batch commit output, got: %s", output)
	}
	if !strings.Contains(output, "Operations: 6") {
		t.Fatalf("expected batch operation count, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "cat", remoteFinal)
	if output != "batch from batch\n" {
		t.Fatalf("unexpected final batch file content: %q", output)
	}

	client := newFilesystemClient(t)
	_, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeslice.IDForUsername(username),
		Path:        remoteDir + "/README.md",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("expected original file to be deleted, got err=%v", err)
	}

	output = runCLIOrFail(t, "", "fs", "log", "--limit", "2")
	if !strings.Contains(output, "batch workflow") {
		t.Fatalf("expected fs log to include batch message, got: %s", output)
	}

	output = runCLIOrFail(t, "", "fs", "show", commitHash)
	if !strings.Contains(output, "ADD "+remoteFinal) {
		t.Fatalf("expected batch commit to show final file add, got: %s", output)
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

func TestCLINonInteractiveLoginFailsFast(t *testing.T) {
	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	output, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "login", "--non-interactive", "--json")
	if err == nil {
		t.Fatalf("expected non-interactive login to fail\nOutput:\n%s", output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected login failure to be an exit error, got %T (%v)", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected login to exit 2, got %d\nOutput:\n%s", exitErr.ExitCode(), output)
	}

	var errResp cliErrorJSON
	if unmarshalErr := json.Unmarshal([]byte(output), &errResp); unmarshalErr != nil {
		t.Fatalf("Unmarshal login error output failed: %v\nOutput:\n%s", unmarshalErr, output)
	}
	if errResp.ErrorCode != "INTERACTIVE_REQUIRED" {
		t.Fatalf("unexpected login error response: %+v", errResp)
	}
	if errResp.SuggestedAction != "gs auth login --key <private-key-path>" {
		t.Fatalf("unexpected login suggested action: %+v", errResp)
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

func TestCLIAgentKeySignupLoginAndManageFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	privateKeyPath := filepath.Join(t.TempDir(), "agent_ed25519")
	keygenOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "keygen", "--out", privateKeyPath, "--json")
	if err != nil {
		t.Fatalf("auth keygen failed: %v\nOutput:\n%s", err, keygenOutput)
	}
	var keygenResp authKeygenJSON
	if err := json.Unmarshal([]byte(keygenOutput), &keygenResp); err != nil {
		t.Fatalf("Unmarshal keygen output failed: %v\nOutput:\n%s", err, keygenOutput)
	}
	if keygenResp.Status != "created" || keygenResp.Fingerprint == "" {
		t.Fatalf("unexpected keygen response: %+v", keygenResp)
	}

	username := fmt.Sprintf("agent-key-%d", time.Now().UnixNano())
	signupOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env,
		"auth", "signup",
		"--username", username,
		"--email", username+"@example.com",
		"--name", "Agent Key User",
		"--key", privateKeyPath,
		"--key-name", "primary",
		"--json",
	)
	if err != nil {
		t.Fatalf("auth signup failed: %v\nOutput:\n%s", err, signupOutput)
	}
	var signupResp authLoginJSON
	if err := json.Unmarshal([]byte(signupOutput), &signupResp); err != nil {
		t.Fatalf("Unmarshal signup output failed: %v\nOutput:\n%s", err, signupOutput)
	}
	if signupResp.Status != "authenticated" || signupResp.Username != username || signupResp.KeyFingerprint != keygenResp.Fingerprint {
		t.Fatalf("unexpected signup response: %+v", signupResp)
	}
	if signupResp.AuthMethod != "agent_key" || signupResp.AgentKeyID == "" {
		t.Fatalf("expected agent-key signup metadata, got: %+v", signupResp)
	}

	claimOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "claim-token", "--json")
	if err != nil {
		t.Fatalf("auth claim-token failed: %v\nOutput:\n%s", err, claimOutput)
	}
	var claimResp authClaimTokenJSON
	if err := json.Unmarshal([]byte(claimOutput), &claimResp); err != nil {
		t.Fatalf("Unmarshal claim-token output failed: %v\nOutput:\n%s", err, claimOutput)
	}
	if claimResp.AccountID == "" || claimResp.ClaimToken == "" || !strings.Contains(claimResp.ClaimURL, "/auth/claim-account?token=") {
		t.Fatalf("unexpected claim-token response: %+v", claimResp)
	}

	statusOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "status", "--json")
	if err != nil {
		t.Fatalf("auth status failed: %v\nOutput:\n%s", err, statusOutput)
	}
	var statusResp authStatusJSON
	if err := json.Unmarshal([]byte(statusOutput), &statusResp); err != nil {
		t.Fatalf("Unmarshal status output failed: %v\nOutput:\n%s", err, statusOutput)
	}
	if !statusResp.Authenticated || statusResp.Username != username || !statusResp.CredentialStore {
		t.Fatalf("unexpected auth status response: %+v", statusResp)
	}
	if statusResp.AuthMethod != "agent_key" || statusResp.AgentKeyID != signupResp.AgentKeyID || statusResp.KeyFingerprint != keygenResp.Fingerprint {
		t.Fatalf("expected stored agent-key auth metadata, got: %+v", statusResp)
	}

	contextOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "context", "--json")
	if err != nil {
		t.Fatalf("context failed: %v\nOutput:\n%s", err, contextOutput)
	}
	var contextResp contextJSON
	if err := json.Unmarshal([]byte(contextOutput), &contextResp); err != nil {
		t.Fatalf("Unmarshal context output failed: %v\nOutput:\n%s", err, contextOutput)
	}
	if contextResp.Auth.Username != username || contextResp.Auth.AuthMethod != "agent_key" || contextResp.Auth.AgentKeyID != signupResp.AgentKeyID || contextResp.Auth.KeyFingerprint != keygenResp.Fingerprint {
		t.Fatalf("unexpected context auth output: %+v", contextResp.Auth)
	}

	doctorOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor failed: %v\nOutput:\n%s", err, doctorOutput)
	}
	var doctorResp doctorJSON
	if err := json.Unmarshal([]byte(doctorOutput), &doctorResp); err != nil {
		t.Fatalf("Unmarshal doctor output failed: %v\nOutput:\n%s", err, doctorOutput)
	}
	if doctorResp.Auth.Username != username || doctorResp.Auth.AuthMethod != "agent_key" || doctorResp.Auth.AgentKeyID != signupResp.AgentKeyID || doctorResp.Auth.KeyFingerprint != keygenResp.Fingerprint || doctorResp.Auth.SessionID == "" {
		t.Fatalf("unexpected doctor auth output: %+v", doctorResp.Auth)
	}

	listOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "keys", "list", "--json")
	if err != nil {
		t.Fatalf("auth keys list failed: %v\nOutput:\n%s", err, listOutput)
	}
	var listResp authKeysListJSON
	if err := json.Unmarshal([]byte(listOutput), &listResp); err != nil {
		t.Fatalf("Unmarshal key list output failed: %v\nOutput:\n%s", err, listOutput)
	}
	if listResp.Total != 1 || listResp.Keys[0].Fingerprint != keygenResp.Fingerprint {
		t.Fatalf("unexpected key list response: %+v", listResp)
	}

	secondKeyPath := filepath.Join(t.TempDir(), "agent_secondary_ed25519")
	secondKeygenOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "keygen", "--out", secondKeyPath, "--json")
	if err != nil {
		t.Fatalf("second auth keygen failed: %v\nOutput:\n%s", err, secondKeygenOutput)
	}
	var secondKeygenResp authKeygenJSON
	if err := json.Unmarshal([]byte(secondKeygenOutput), &secondKeygenResp); err != nil {
		t.Fatalf("Unmarshal second keygen output failed: %v\nOutput:\n%s", err, secondKeygenOutput)
	}

	addOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env,
		"auth", "keys", "add",
		"--name", "secondary",
		"--public-key", secondKeyPath+".pub",
		"--json",
	)
	if err != nil {
		t.Fatalf("auth keys add failed: %v\nOutput:\n%s", err, addOutput)
	}
	var addResp authKeyJSON
	if err := json.Unmarshal([]byte(addOutput), &addResp); err != nil {
		t.Fatalf("Unmarshal key add output failed: %v\nOutput:\n%s", err, addOutput)
	}
	if addResp.ID == "" || addResp.Fingerprint != secondKeygenResp.Fingerprint {
		t.Fatalf("unexpected add key response: %+v", addResp)
	}

	revokeOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "keys", "revoke", addResp.ID, "--json")
	if err != nil {
		t.Fatalf("auth keys revoke failed: %v\nOutput:\n%s", err, revokeOutput)
	}
	var revokeResp authKeyRevokeJSON
	if err := json.Unmarshal([]byte(revokeOutput), &revokeResp); err != nil {
		t.Fatalf("Unmarshal key revoke output failed: %v\nOutput:\n%s", err, revokeOutput)
	}
	if revokeResp.Status != "revoked" || revokeResp.KeyID != addResp.ID {
		t.Fatalf("unexpected revoke response: %+v", revokeResp)
	}

	logoutOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "logout", "--json")
	if err != nil {
		t.Fatalf("auth logout failed: %v\nOutput:\n%s", err, logoutOutput)
	}
	var logoutResp struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(logoutOutput), &logoutResp); err != nil {
		t.Fatalf("Unmarshal auth logout output failed: %v\nOutput:\n%s", err, logoutOutput)
	}
	if logoutResp.Status != "logged_out" {
		t.Fatalf("unexpected logout response: %+v", logoutResp)
	}

	loginOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "login", "--key", privateKeyPath, "--json")
	if err != nil {
		t.Fatalf("auth key login failed: %v\nOutput:\n%s", err, loginOutput)
	}
	var loginResp authLoginJSON
	if err := json.Unmarshal([]byte(loginOutput), &loginResp); err != nil {
		t.Fatalf("Unmarshal login output failed: %v\nOutput:\n%s", err, loginOutput)
	}
	if loginResp.Status != "authenticated" || loginResp.Username != username {
		t.Fatalf("unexpected login response: %+v", loginResp)
	}
	if loginResp.AuthMethod != "agent_key" || loginResp.AgentKeyID == "" || loginResp.KeyFingerprint != keygenResp.Fingerprint {
		t.Fatalf("expected agent-key login metadata, got: %+v", loginResp)
	}

	loginErrorOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "login", "--key", secondKeyPath, "--json")
	if err == nil {
		t.Fatalf("expected revoked secondary key login to fail, output:\n%s", loginErrorOutput)
	}
	var loginErrResp cliErrorJSON
	if err := json.Unmarshal([]byte(loginErrorOutput), &loginErrResp); err != nil {
		t.Fatalf("Unmarshal login error output failed: %v\nOutput:\n%s", err, loginErrorOutput)
	}
	if loginErrResp.ErrorCode != "AUTH_LOGIN_FAILED" {
		t.Fatalf("unexpected login error response: %+v", loginErrResp)
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
	if creds.AccessToken == "" || creds.Username != username {
		t.Fatalf("unexpected credentials contents: %+v", creds)
	}

	remoteDir := fmt.Sprintf("/%s/agent-key-login-%d", username, time.Now().UnixNano())
	if _, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "fs", "mkdir", remoteDir); err != nil {
		t.Fatalf("fs mkdir after agent login failed: %v", err)
	}

	client := newFilesystemClient(t)
	statResp, err := client.Stat(withBearerToken(ctx, creds.AccessToken), &filesystemv1.StatRequest{
		WorkspaceId: homeslice.IDForUsername(username),
		Path:        remoteDir,
	})
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !statResp.GetExists() || statResp.GetEntry().GetType() != filesystemv1.EntryType_ENTRY_TYPE_DIRECTORY {
		t.Fatalf("expected remote directory to exist, got %#v", statResp)
	}
}

func TestCLIAuthEnsureWithAgentKey(t *testing.T) {
	homeDir := t.TempDir()
	env := map[string]string{"HOME": homeDir}

	privateKeyPath := filepath.Join(t.TempDir(), "agent_ed25519")
	if output, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "keygen", "--out", privateKeyPath, "--json"); err != nil {
		t.Fatalf("auth keygen failed: %v\nOutput:\n%s", err, output)
	}

	username := fmt.Sprintf("agent-ensure-%d", time.Now().UnixNano())
	signupOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env,
		"auth", "signup",
		"--username", username,
		"--email", username+"@example.com",
		"--name", "Agent Ensure User",
		"--key", privateKeyPath,
		"--json",
	)
	if err != nil {
		t.Fatalf("auth signup failed: %v\nOutput:\n%s", err, signupOutput)
	}
	var signupResp authLoginJSON
	if err := json.Unmarshal([]byte(signupOutput), &signupResp); err != nil {
		t.Fatalf("Unmarshal signup output failed: %v\nOutput:\n%s", err, signupOutput)
	}

	readyOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "ensure", "--key", privateKeyPath, "--json")
	if err != nil {
		t.Fatalf("auth ensure failed: %v\nOutput:\n%s", err, readyOutput)
	}
	var readyResp authEnsureJSON
	if err := json.Unmarshal([]byte(readyOutput), &readyResp); err != nil {
		t.Fatalf("Unmarshal auth ensure output failed: %v\nOutput:\n%s", err, readyOutput)
	}
	if !readyResp.Authenticated || readyResp.Ensured || readyResp.Username != username {
		t.Fatalf("unexpected ready auth ensure response: %+v", readyResp)
	}

	if output, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "logout", "--json"); err != nil {
		t.Fatalf("auth logout failed: %v\nOutput:\n%s", err, output)
	}

	ensuredOutput, err := runCLIWithDirInputEnvNoLegacyUser("", "", env, "auth", "ensure", "--key", privateKeyPath, "--json")
	if err != nil {
		t.Fatalf("auth ensure with key failed: %v\nOutput:\n%s", err, ensuredOutput)
	}
	var ensuredResp authEnsureJSON
	if err := json.Unmarshal([]byte(ensuredOutput), &ensuredResp); err != nil {
		t.Fatalf("Unmarshal ensured auth output failed: %v\nOutput:\n%s", err, ensuredOutput)
	}
	if !ensuredResp.Authenticated || !ensuredResp.Ensured || ensuredResp.Username != username {
		t.Fatalf("unexpected ensured auth response: %+v", ensuredResp)
	}
	if ensuredResp.AuthMethod != "agent_key" || ensuredResp.AgentKeyID != signupResp.AgentKeyID {
		t.Fatalf("expected agent-key metadata after ensure, got: %+v", ensuredResp)
	}
}

func TestFilesystemShellWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)
	username := workflowUsername(t)

	remoteRoot := fmt.Sprintf("/%s/fs-shell-%d", username, time.Now().UnixNano())

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

	output, err := runCLIWithDirInputEnvLegacyUser("", script, workflowProcessEnv(t, nil), true, username, "fs", "shell", remoteRoot)
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
		WorkspaceId: homeslice.IDForUsername(username),
		Path:        remoteRoot + "/src/main.py",
	})
	if err != nil {
		t.Fatalf("failed to read shell-written file: %v", err)
	}
	if got := string(readResp.GetContent()); got != "print('hello world')\n" {
		t.Fatalf("unexpected shell-written file content: %q", got)
	}
}

func TestCLINonInteractiveFilesystemShellFailsFast(t *testing.T) {
	username := workflowUsername(t)
	env := workflowProcessEnv(t, map[string]string{"GS_NON_INTERACTIVE": "1"})
	remoteRoot := fmt.Sprintf("/%s/fs-shell-non-interactive-%d", username, time.Now().UnixNano())

	output, err := runCLIWithDirInputEnvLegacyUser("", "", env, true, username, "fs", "shell", remoteRoot, "--json")
	if err == nil {
		t.Fatalf("expected non-interactive fs shell to fail\nOutput:\n%s", output)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected fs shell failure to be an exit error, got %T (%v)", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected fs shell to exit 2, got %d\nOutput:\n%s", exitErr.ExitCode(), output)
	}

	var errResp cliErrorJSON
	if unmarshalErr := json.Unmarshal([]byte(output), &errResp); unmarshalErr != nil {
		t.Fatalf("Unmarshal fs shell error output failed: %v\nOutput:\n%s", unmarshalErr, output)
	}
	if errResp.ErrorCode != "INTERACTIVE_REQUIRED" {
		t.Fatalf("unexpected fs shell error response: %+v", errResp)
	}
	if !strings.Contains(errResp.SuggestedAction, "gs fs ls") {
		t.Fatalf("unexpected fs shell suggested action: %+v", errResp)
	}
}

func TestFilesystemTransferWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)
	username := workflowUsername(t)

	remoteProjectRoot := fmt.Sprintf("/%s/fs-transfer-%d/project", username, time.Now().UnixNano())

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
	output = runCLIOrFail(t, "", "fs", "upload", uploadRoot, remoteProjectRoot)
	if !strings.Contains(output, "Uploaded 2 files and 3 directories") {
		t.Fatalf("expected repeat upload summary, got: %s", output)
	}

	client := newFilesystemClient(t)
	readmeResp, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeslice.IDForUsername(username),
		Path:        remoteProjectRoot + "/README.md",
	})
	if err != nil {
		t.Fatalf("failed to read uploaded README.md: %v", err)
	}
	if got := string(readmeResp.GetContent()); got != "transfer root\n" {
		t.Fatalf("unexpected uploaded README.md content: %q", got)
	}

	mainResp, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{
		WorkspaceId: homeslice.IDForUsername(username),
		Path:        remoteProjectRoot + "/src/main.py",
	})
	if err != nil {
		t.Fatalf("failed to read uploaded src/main.py: %v", err)
	}
	if got := string(mainResp.GetContent()); got != "print('transfer')\n" {
		t.Fatalf("unexpected uploaded src/main.py content: %q", got)
	}

	emptyDirResp, err := client.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: homeslice.IDForUsername(username),
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

func TestFilesystemDryRunAndIdempotentWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)
	username := workflowUsername(t)
	client := newFilesystemClient(t)
	workspaceID := homeslice.IDForUsername(username)
	basePath := fmt.Sprintf("/%s/fs-ops-%d", username, time.Now().UnixNano())

	filePath := basePath + "/delete-me.txt"
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        filePath,
		Content:     []byte("delete me\n"),
	}); err != nil {
		t.Fatalf("write delete target failed: %v", err)
	}

	rmPreview := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "rm", filePath, "--dry-run")
	if rmPreview.Status != "would_delete" || !rmPreview.DryRun || rmPreview.Path != filePath {
		t.Fatalf("unexpected rm dry-run response: %+v", rmPreview)
	}
	statResp, err := client.Stat(ctx, &filesystemv1.StatRequest{WorkspaceId: workspaceID, Path: filePath})
	if err != nil || !statResp.GetExists() {
		t.Fatalf("expected delete target to remain after dry-run, stat=%#v err=%v", statResp, err)
	}

	rmResp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "rm", filePath)
	if rmResp.Status != "deleted" || rmResp.CommitHash == "" {
		t.Fatalf("unexpected rm response: %+v", rmResp)
	}
	rmNoOp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "rm", filePath)
	if rmNoOp.Status != "no_op" {
		t.Fatalf("expected rm retry to no-op, got %+v", rmNoOp)
	}

	moveSource := basePath + "/move-source.txt"
	moveDest := basePath + "/move-dest.txt"
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        moveSource,
		Content:     []byte("move me\n"),
	}); err != nil {
		t.Fatalf("write move source failed: %v", err)
	}
	movePreview := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "mv", moveSource, moveDest, "--dry-run")
	if movePreview.Status != "would_move" || movePreview.SourcePath != moveSource || movePreview.DestinationPath != moveDest {
		t.Fatalf("unexpected move dry-run response: %+v", movePreview)
	}
	moveResp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "mv", moveSource, moveDest)
	if moveResp.Status != "moved" || moveResp.CommitHash == "" {
		t.Fatalf("unexpected move response: %+v", moveResp)
	}
	moveNoOp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "mv", moveSource, moveDest)
	if moveNoOp.Status != "no_op" {
		t.Fatalf("expected move retry to no-op, got %+v", moveNoOp)
	}

	copySource := basePath + "/copy-source.txt"
	copyDest := basePath + "/copy-dest.txt"
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        copySource,
		Content:     []byte("copy me\n"),
	}); err != nil {
		t.Fatalf("write copy source failed: %v", err)
	}
	copyPreview := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "cp", copySource, copyDest, "--dry-run")
	if copyPreview.Status != "would_copy" || copyPreview.SourcePath != copySource || copyPreview.DestinationPath != copyDest {
		t.Fatalf("unexpected copy dry-run response: %+v", copyPreview)
	}
	copyResp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "cp", copySource, copyDest)
	if copyResp.Status != "copied" || copyResp.CommitHash == "" {
		t.Fatalf("unexpected copy response: %+v", copyResp)
	}
	copyNoOp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "cp", copySource, copyDest)
	if copyNoOp.Status != "no_op" {
		t.Fatalf("expected copy retry to no-op, got %+v", copyNoOp)
	}

	restorePath := basePath + "/restore.txt"
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        restorePath,
		Content:     []byte("before restore\n"),
	}); err != nil {
		t.Fatalf("write restore seed failed: %v", err)
	}
	snapshotResp, err := client.Snapshot(ctx, &filesystemv1.SnapshotRequest{
		WorkspaceId: workspaceID,
		Message:     "before restore",
	})
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if _, err := client.WriteFile(ctx, &filesystemv1.WriteFileRequest{
		WorkspaceId: workspaceID,
		Path:        restorePath,
		Content:     []byte("after restore\n"),
	}); err != nil {
		t.Fatalf("write restore mutation failed: %v", err)
	}

	restorePreview := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "restore", snapshotResp.GetSnapshot().GetSnapshotId(), "--dry-run")
	if restorePreview.Status != "would_restore" || restorePreview.SnapshotID != snapshotResp.GetSnapshot().GetSnapshotId() || restorePreview.Summary == nil {
		t.Fatalf("unexpected restore dry-run response: %+v", restorePreview)
	}
	restoreResp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "restore", snapshotResp.GetSnapshot().GetSnapshotId())
	if restoreResp.Status != "restored" {
		t.Fatalf("unexpected restore response: %+v", restoreResp)
	}
	restoreNoOp := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "restore", snapshotResp.GetSnapshot().GetSnapshotId())
	if restoreNoOp.Status != "no_op" {
		t.Fatalf("expected restore retry to no-op, got %+v", restoreNoOp)
	}
	readResp, err := client.ReadFile(ctx, &filesystemv1.ReadFileRequest{WorkspaceId: workspaceID, Path: restorePath})
	if err != nil {
		t.Fatalf("read restored file failed: %v", err)
	}
	if got := string(readResp.GetContent()); got != "before restore\n" {
		t.Fatalf("unexpected restored file content: %q", got)
	}
}

func TestFilesystemTransferDryRunWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)
	username := workflowUsername(t)
	client := newFilesystemClient(t)
	remoteProjectRoot := fmt.Sprintf("/%s/fs-transfer-dry-run-%d/project", username, time.Now().UnixNano())

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

	uploadPreview := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "upload", uploadRoot, remoteProjectRoot, "--dry-run")
	if uploadPreview.Status != "would_upload" || uploadPreview.FileCount != 2 || uploadPreview.DirectoryCount != 3 {
		t.Fatalf("unexpected upload dry-run response: %+v", uploadPreview)
	}
	statResp, err := client.Stat(ctx, &filesystemv1.StatRequest{
		WorkspaceId: homeslice.IDForUsername(username),
		Path:        remoteProjectRoot,
	})
	if err != nil {
		t.Fatalf("stat remote upload root failed: %v", err)
	}
	if statResp.GetExists() {
		t.Fatalf("expected upload dry-run to leave remote path absent: %#v", statResp)
	}

	runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "sync", "--direction", "push", uploadRoot, remoteProjectRoot, "--dry-run")
	runCLIOrFail(t, "", "fs", "upload", uploadRoot, remoteProjectRoot)

	downloadRoot := filepath.Join(t.TempDir(), "download-preview")
	downloadPreview := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "download", remoteProjectRoot, downloadRoot, "--dry-run")
	if downloadPreview.Status != "would_download" || downloadPreview.FileCount != 2 || downloadPreview.DirectoryCount != 3 {
		t.Fatalf("unexpected download dry-run response: %+v", downloadPreview)
	}
	if _, err := os.Stat(downloadRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected dry-run download target to remain absent, stat err=%v", err)
	}

	syncPullPreview := runCLIJSONOrFail[filesystemActionJSON](t, "", "fs", "sync", "--direction", "pull", remoteProjectRoot, downloadRoot, "--dry-run")
	if syncPullPreview.Status != "would_download" || syncPullPreview.FileCount != 2 || syncPullPreview.DirectoryCount != 3 {
		t.Fatalf("unexpected sync pull dry-run response: %+v", syncPullPreview)
	}
}

func TestFileBrowserIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

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
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
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

	rootEntries := fetchGatewayEntries(t, gatewayServiceURL+"/v1/files/entries", "User "+workflowUsername(t))
	assertGatewayEntryNames(t, rootEntries.Entries, "gateway")

	gatewayEntries := fetchGatewayEntries(t, gatewayServiceURL+"/v1/files/entries/gateway", "User "+workflowUsername(t))
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

func fetchGatewayEntries(t *testing.T, url string, auth ...string) gatewayEntriesResponse {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("gateway request creation failed: %v", err)
	}
	if len(auth) > 0 && auth[0] != "" {
		req.Header.Set("Authorization", auth[0])
	}
	resp, err := client.Do(req)
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
	ctx = withWorkflowUser(t, ctx)

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
		t.Fatalf("failed to dial slice service: %v", err)
	}
	defer conn.Close()

	client := slicev1.NewSliceServiceClient(conn)
	sliceA := fmt.Sprintf("batch-merge-a-%d", time.Now().UnixNano())
	sliceB := fmt.Sprintf("batch-merge-b-%d", time.Now().UnixNano())

	if err := st.CreateSlice(ctx, &models.Slice{ID: sliceA, Name: "Batch A", Files: []string{"file-a"}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if err := st.CreateSlice(ctx, &models.Slice{ID: sliceB, Name: "Batch B", Files: []string{"file-b"}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	mergeResp, err := client.BatchMerge(ctx, &slicev1.BatchMergeRequest{})
	if err != nil {
		t.Fatalf("batch merge failed: %v", err)
	}
	if mergeResp.MergedSliceCount != 2 {
		t.Fatalf("expected 2 merged slices, got %d", mergeResp.MergedSliceCount)
	}

	rootMetadata, err := st.GetSliceMetadata(ctx, "root")
	if err != nil {
		t.Fatalf("failed to load root metadata: %v", err)
	}
	if rootMetadata.HeadCommitHash != mergeResp.GlobalCommitHash {
		t.Fatalf("expected root commit %s, got %s", mergeResp.GlobalCommitHash, rootMetadata.HeadCommitHash)
	}
	if rootMetadata.ModifiedFilesCount != 2 {
		t.Fatalf("expected 2 modified files in root metadata, got %d", rootMetadata.ModifiedFilesCount)
	}

	conflictsResp, err := client.GetConflicts(ctx, &slicev1.ConflictsRequest{})
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
	if !strings.Contains(output, "Initialized empty gitslice checkout") {
		t.Fatalf("expected init output, got: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "create", "--message", "history change", "--files", "history_file.txt")
	changesetID := extractChangesetID(output)
	if changesetID == "" {
		t.Fatalf("failed to extract changeset ID from output: %s", output)
	}

	output = runCLIOrFail(t, workdir, "changeset", "merge", changesetID, "--wait")
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
	ctx = withWorkflowUser(t, ctx)

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

	sliceClient := newSliceClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	resolveAllConflicts(ctx, t, sliceClient)

	mergeResp, err := sliceClient.BatchMerge(ctx, &slicev1.BatchMergeRequest{})
	if err != nil {
		t.Fatalf("batch merge failed: %v", err)
	}

	stateResp, err := sliceClient.GetGlobalState(ctx, &slicev1.GlobalStateRequest{IncludeHistory: true})
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

	rootState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: "root"})
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
	ctx = withWorkflowUser(t, ctx)

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
	if err := st.CreateSlice(ctx, &models.Slice{ID: sliceID, Name: "Persist", Files: []string{fileID}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
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
	sliceClient2 := slicev1.NewSliceServiceClient(sliceConn2)

	globalState, err := sliceClient2.GetGlobalState(ctx, &slicev1.GlobalStateRequest{IncludeHistory: true})
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
	ctx = withWorkflowUser(t, ctx)

	sliceClient := newSliceClient(t)

	sharedFile := fmt.Sprintf("lock-shared-%d.txt", time.Now().UnixNano())
	sliceA := fmt.Sprintf("lock-a-%d", time.Now().UnixNano())
	sliceB := fmt.Sprintf("lock-b-%d", time.Now().UnixNano())

	if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceA, Name: "LockA", Files: []string{sharedFile}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
		t.Fatalf("failed to create slice A: %v", err)
	}
	if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceB, Name: "LockB", Files: []string{sharedFile}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
		t.Fatalf("failed to create slice B: %v", err)
	}

	hashA := mustWriteSliceManifest(t, ctx, testStorage, sliceA, sharedFile, []byte("slice-a initial content"))
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       fmt.Sprintf("%s:%s", sliceA, sharedFile),
		Path:     sharedFile,
		Type:     "file",
		ParentID: sliceA,
		Hash:     hashA,
		Size:     int64(len("slice-a initial content")),
	}); err != nil && !errors.Is(err, storage.ErrEntryExists) {
		t.Fatalf("failed to add initial entry for slice A: %v", err)
	}
	hashB := mustWriteSliceManifest(t, ctx, testStorage, sliceB, sharedFile, []byte("slice-b initial content"))
	if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
		ID:       fmt.Sprintf("%s:%s", sliceB, sharedFile),
		Path:     sharedFile,
		Type:     "file",
		ParentID: sliceB,
		Hash:     hashB,
		Size:     int64(len("slice-b initial content")),
	}); err != nil && !errors.Is(err, storage.ErrEntryExists) {
		t.Fatalf("failed to add initial entry for slice B: %v", err)
	}

	if _, err := sliceClient.ResolveConflict(ctx, &slicev1.ResolveConflictRequest{FileId: sharedFile, PreferredSliceId: sliceA}); err != nil {
		t.Fatalf("failed to resolve conflict to slice A: %v", err)
	}

	otherChange, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{SliceId: sliceB, ModifiedFiles: []string{sharedFile}, Message: "stale after slice A merge"})
	if err != nil {
		t.Fatalf("failed to create stale changeset: %v", err)
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

	var stateResp *slicev1.GlobalStateResponse
	if err := waitForCondition(2*time.Second, 25*time.Millisecond, func() (bool, error) {
		resp, err := sliceClient.GetGlobalState(ctx, &slicev1.GlobalStateRequest{IncludeHistory: true})
		if err != nil {
			return false, err
		}
		stateResp = resp
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
		t.Fatalf("expected promoted commit %s and slice %s in global history, got head=%s: %v", mergeResp.NewCommitHash, sliceA, gotHead, err)
	}

	conflictResp, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: otherChange.ChangesetId})
	if err != nil {
		t.Fatalf("expected merge response for stale path-head changeset, got error: %v", err)
	}
	if conflictResp.Status != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("expected stale-base status for older path-head changeset, got %v", conflictResp.Status)
	}
}

func TestConcurrentSlicePushesPromoteHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceClient := newSliceClient(t)

	initialState, err := sliceClient.GetGlobalState(ctx, &slicev1.GlobalStateRequest{IncludeHistory: true})
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

		if err := testStorage.CreateSlice(ctx, &models.Slice{ID: sliceID, Name: fmt.Sprintf("Concurrent-%d", i), Files: []string{file}, Owners: []string{workflowUsername(t)}, CreatedBy: workflowUsername(t)}); err != nil {
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

	var globalState *slicev1.GlobalStateResponse
	if err := waitForCondition(3*time.Second, 25*time.Millisecond, func() (bool, error) {
		resp, err := sliceClient.GetGlobalState(ctx, &slicev1.GlobalStateRequest{IncludeHistory: true})
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

	rootState, err := sliceClient.GetSliceState(ctx, &slicev1.StateRequest{SliceId: "root"})
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

func TestChangesetMergeRequiresCurrentBaseCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceClient := newSliceClient(t)

	sliceID := fmt.Sprintf("stale-base-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "StaleBase",
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	meta, err := testStorage.GetSliceMetadata(ctx, sliceID)
	if err != nil {
		t.Fatalf("failed to get slice metadata: %v", err)
	}
	meta.HeadCommitHash = "head-current"
	if err := testStorage.UpdateSliceMetadata(ctx, sliceID, meta); err != nil {
		t.Fatalf("failed to update slice metadata: %v", err)
	}

	stalePath := workflowUsername(t) + "/README.md"
	createResp, err := sliceClient.CreateChangeset(ctx, &slicev1.CreateChangesetRequest{
		SliceId:        sliceID,
		BaseCommitHash: "head-old",
		ModifiedFiles:  []string{stalePath},
		Author:         "tester",
		Message:        "stale base",
	})
	if err != nil {
		t.Fatalf("failed to create changeset: %v", err)
	}
	if headStore, ok := testStorage.(storage.HomePathHeadStore); ok {
		if err := headStore.UpsertHomePathHeads(ctx, []*models.HomePathHead{{
			HomeID:       workflowUsername(t),
			Path:         stalePath,
			PathVersion:  1,
			ContentHash:  "head-current-readme",
			ManifestHash: "head-current-readme",
			UpdatedAt:    time.Now(),
		}}); err != nil {
			t.Fatalf("failed to seed path-head drift: %v", err)
		}
	}

	reviewResp, err := sliceClient.ReviewChangeset(ctx, &slicev1.ReviewChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("failed to review changeset: %v", err)
	}
	if reviewResp.GetReviewStatus() != slicev1.ReviewStatus_NEEDS_SYNC {
		t.Fatalf("expected NEEDS_SYNC, got %v", reviewResp.GetReviewStatus())
	}

	mergeResp, err := sliceClient.MergeChangeset(ctx, &slicev1.MergeChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("expected structured stale-base merge response, got %v", err)
	}
	if mergeResp.GetStatus() != slicev1.MergeStatus_MERGE_STATUS_STALE_BASE {
		t.Fatalf("expected STALE_BASE status for stale-base merge, got %v", mergeResp.GetStatus())
	}

	rebaseResp, err := sliceClient.RebaseChangeset(ctx, &slicev1.RebaseChangesetRequest{ChangesetId: createResp.GetChangesetId()})
	if err != nil {
		t.Fatalf("failed to rebase changeset: %v", err)
	}
	if rebaseResp.GetNewBaseCommitHash() != "head-current" {
		t.Fatalf("expected current head base after rebase, got %q", rebaseResp.GetNewBaseCommitHash())
	}
}

func TestAgentSessionLifecycleAndWSReplayIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceID := fmt.Sprintf("agent-ws-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent WS",
		Files:     []string{},
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
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

	assertLocalAgentSessionResumable(t, sessionID, stopAgentSessionViaHTTP(t, sessionID, "integration_done"))
}

func TestAgentSessionTokenReuseRejectedIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceID := fmt.Sprintf("agent-token-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Token",
		Files:     []string{},
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
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

	assertLocalAgentSessionResumable(t, sessionID, stopAgentSessionViaHTTP(t, sessionID, "integration_done"))
}

func TestAgentSessionClaudeStreamIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceID := fmt.Sprintf("agent-claude-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Claude",
		Files:     []string{},
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
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

	gotClaudeInput := false
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && !gotClaudeInput {
		var frame struct {
			Stream  string                 `json:"stream"`
			Type    string                 `json:"type"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("read websocket frame failed: %v", err)
		}
		if frame.Stream == "agent" && frame.Type == "input" {
			text, _ := frame.Payload["text"].(string)
			if strings.Contains(text, "Explain this diff") {
				gotClaudeInput = true
			}
		}
	}
	if !gotClaudeInput {
		t.Fatalf("expected queued claude agent/input frame")
	}

	assertLocalAgentSessionResumable(t, sessionID, stopAgentSessionViaHTTP(t, sessionID, "integration_done"))
}

func TestAgentSessionCreateUnknownRunnerIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceID := fmt.Sprintf("agent-env-missing-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Missing Env",
		Files:     []string{},
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	body := map[string]any{
		"sliceId":   sliceID,
		"runnerId":  "missing-integration-runner",
		"agentType": "codex",
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+workflowUsername(t))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400 for unknown runner, got %d body=%s", resp.StatusCode, string(data))
	}
}

func TestAgentSessionCreateDisallowedAgentTypeIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	sliceID := fmt.Sprintf("agent-disallowed-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Disallowed Type",
		Files:     []string{},
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}
	runnerID := fmt.Sprintf("runner-codex-only-%d", time.Now().UnixNano())
	seedIntegrationAgentRunner(t, runnerID, "codex")

	body := map[string]any{
		"sliceId":   sliceID,
		"runnerId":  runnerID,
		"agentType": "claude",
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+workflowUsername(t))
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
	_ = environment
	if strings.TrimSpace(agentType) == "" {
		agentType = "codex"
	}
	runnerID := fmt.Sprintf("runner-%s-%d", strings.ToLower(agentType), time.Now().UnixNano())
	seedIntegrationAgentRunner(t, runnerID, agentType)
	body := map[string]any{
		"sliceId":   sliceID,
		"runnerId":  runnerID,
		"agentType": agentType,
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+workflowUsername(t))
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

func seedIntegrationAgentRunner(t *testing.T, runnerID, agentType string) {
	t.Helper()
	ctx := withWorkflowUser(t, context.Background())
	now := time.Now().UTC()
	err := testStorage.UpsertAgentRunner(ctx, &models.AgentRunner{
		RunnerID:        runnerID,
		UserID:          workflowUsername(t),
		Provider:        "local",
		AgentType:       strings.ToLower(strings.TrimSpace(agentType)),
		Status:          models.AgentRunnerStatusOnline,
		HostName:        "integration-host",
		WorkspaceRoot:   "/tmp/gitslice-integration-agents",
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("failed to register integration agent runner: %v", err)
	}
}

func mintAgentTokenViaHTTP(t *testing.T, sessionID string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions/"+sessionID+"/token", nil)
	req.Header.Set("Authorization", "User "+workflowUsername(t))
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

func stopAgentSessionViaHTTP(t *testing.T, sessionID string, reason string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"reason": reason})
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions/"+sessionID+"/stop", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+workflowUsername(t))
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
	var out struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode stop response failed: %v", err)
	}
	return out.State
}

func assertLocalAgentSessionResumable(t *testing.T, sessionID, state string) {
	t.Helper()
	if state == "stopping" || state == "stopped" {
		t.Fatalf("expected local session %s to stay resumable after stop request, got state %s", sessionID, state)
	}
}

func waitForAgentSessionState(t *testing.T, sessionID string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, gatewayServiceURL+"/v1/agent-sessions/"+sessionID, nil)
		req.Header.Set("Authorization", "User "+workflowUsername(t))
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
