package gscli

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	accountv1 "github.com/niczy/gitslice/proto/account"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	filev1 "github.com/niczy/gitslice/proto/file"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	coreServerAddr    = flag.String("addr", "", "Core gRPC service address (overrides account-addr/slice-addr/admin-addr/file-addr/fs-addr)")
	accountServerAddr = flag.String("account-addr", "localhost:50051", "Account service address")
	sliceServerAddr   = flag.String("slice-addr", "localhost:50051", "Slice service address")
	adminServerAddr   = flag.String("admin-addr", "localhost:50051", "Admin service address")
	fileServerAddr    = flag.String("file-addr", "localhost:50051", "File service address")
	fsServerAddr      = flag.String("fs-addr", "localhost:50051", "Filesystem service address")
	useTLS            = flag.Bool("tls", false, "Use TLS for gRPC connections")
	nonInteractive    = flag.Bool("non-interactive", false, "Fail instead of opening interactive flows (also GS_NON_INTERACTIVE=1)")
	apiKeyFlag        = flag.String("api-key", "", "Bearer API key or access token (overrides GS_API_KEY, GS_API_KEY_FILE, and ~/.gitslice/credentials.json)")
	userFlag          = flag.String("user", "", "Legacy username auth for dev use (overrides GS_USERNAME and ~/.gitslice/user after bearer auth sources)")
)

const (
	grpcMaxCallRecvBytes = 64 << 20
	grpcMaxCallSendBytes = 64 << 20
)

// CLI holds the gRPC connections and clients for interacting with gitslice services.
type CLI struct {
	accountConn      *grpc.ClientConn
	sliceConn        *grpc.ClientConn
	adminConn        *grpc.ClientConn
	fileConn         *grpc.ClientConn
	filesystemConn   *grpc.ClientConn
	conns            []*grpc.ClientConn
	accountClient    accountv1.AccountServiceClient
	sliceClient      slicev1.SliceServiceClient
	adminClient      adminv1.AdminServiceClient
	fileClient       filev1.FileServiceClient
	filesystemClient filesystemv1.FilesystemServiceClient
}

func Main() {
	cmd := NewRootCommand(os.Args[1:])
	if err := cmd.Execute(); err != nil {
		commandFatalf("CLI_EXEC_FAILED", false, "", "%v", err)
	}
}

func runLegacyCommand(args []string) {
	args = configureCLIBehavior(args)
	if len(args) < 1 {
		printHelp()
		return
	}
	configureCLIOutputMode(args)

	if args[0] == "cache" {
		handleCacheCommand(args[1:])
		return
	}
	if args[0] == "jobs" {
		handleJobsCommand(args[1:])
		return
	}
	if args[0] == "__watch-checkout" {
		handleCheckoutWatcher(args[1:])
		return
	}
	if args[0] == "__run-job" {
		handleDetachedJobRunner(args[1:])
		return
	}
	if args[0] == "slice" && len(args) > 1 && args[1] == "checkouts" {
		handleSliceCheckouts(args[2:])
		return
	}

	cli, err := newCLIFromFlags()
	if err != nil {
		commandFatalf("CLI_INIT_FAILED", true, "", "Failed to initialize CLI: %v", err)
	}
	defer cli.Close()

	if args[0] == "login" {
		authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
		if err != nil && len(args) == 1 {
			commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve current auth: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		handleLogin(ctx, cli, authConfig, args[1:])
		return
	}
	if args[0] == "logout" {
		authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
		if err != nil {
			commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve current auth: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		handleLogout(ctx, cli, authConfig, args[1:])
		return
	}
	if args[0] == "auth" {
		runAuthCommandWithCLI(cli, args[1:])
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
	if err != nil {
		commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve auth: %v", err)
	}
	authConfig, err = ensureCLIAuthReady(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("AUTH_REFRESH_FAILED", true, "", "Failed to refresh stored auth: %v", err)
	}
	ctx = withCLIAuth(ctx, authConfig)

	switch args[0] {
	case "slice":
		handleSliceCommand(ctx, cli, args[1:])
	case "changeset":
		handleChangesetCommand(ctx, cli, args[1:])
	case "status":
		handleStatus(ctx, cli, args[1:])
	case "init":
		handleInit(ctx, cli, args[1:])
	case "log":
		handleLog(ctx, cli, args[1:])
	case "conflict":
		handleConflictCommand(ctx, cli, args[1:])
	case "root":
		handleRootSlice(ctx, cli)
	case "import":
		handleImportCommand(ctx, cli, args[1:])
	case "repo":
		handleRepoCommand(ctx, cli, args[1:])
	case "file":
		handleFileCommand(ctx, cli, args[1:])
	case "fs":
		handleFilesystemCommand(ctx, cli, authConfig, args[1:])
	case "cache":
		handleCacheCommand(args[1:])
	case "jobs":
		handleJobsCommand(args[1:])
	case "doctor":
		handleDoctor(ctx, cli, authConfig, args[1:])
	case "context":
		handleContext(ctx, cli, authConfig, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown command: %s", args[0]), false, "gs --help")
	}
}

func newCLIFromFlags() (*CLI, error) {
	if *coreServerAddr != "" {
		*accountServerAddr = *coreServerAddr
		*sliceServerAddr = *coreServerAddr
		*adminServerAddr = *coreServerAddr
		*fileServerAddr = *coreServerAddr
		*fsServerAddr = *coreServerAddr
	}
	return NewCLI(*accountServerAddr, *sliceServerAddr, *adminServerAddr, *fileServerAddr, *fsServerAddr, *useTLS)
}

type authenticatedCLIHandler func(ctx context.Context, cli *CLI, authConfig cliAuth, args []string)

func runAuthenticatedCLICommand(args []string, timeout time.Duration, handler authenticatedCLIHandler) {
	args = configureCLIBehavior(args)
	configureCLIOutputMode(args)

	cli, err := newCLIFromFlags()
	if err != nil {
		commandFatalf("CLI_INIT_FAILED", true, "", "Failed to initialize CLI: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
	if err != nil {
		commandFatalf("AUTH_RESOLUTION_FAILED", false, "", "Failed to resolve auth: %v", err)
	}
	authConfig, err = ensureCLIAuthReady(ctx, cli, authConfig)
	if err != nil {
		commandFatalf("AUTH_REFRESH_FAILED", true, "", "Failed to refresh stored auth: %v", err)
	}
	handler(withCLIAuth(ctx, authConfig), cli, authConfig, args)
}

func handleDetachedJobRunner(args []string) {
	if len(args) < 3 || strings.TrimSpace(args[0]) == "" || args[1] != "--" {
		commandFatal("INVALID_ARGUMENT", "Usage: gs __run-job <job-id> -- <command...>", false, "")
		return
	}
	os.Exit(runDetachedCLIJob(strings.TrimSpace(args[0]), args[2:]))
}

func newDetachedJobRunnerCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "__run-job <job-id> -- <command...>",
		Hidden:             true,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			handleDetachedJobRunner(args)
		},
	}
}

func configureCLIBehavior(args []string) []string {
	remaining, requested := consumeBoolFlag(args, "non-interactive")
	cliNonInteractive = *nonInteractive || requested || envFlagEnabled(os.Getenv("GS_NON_INTERACTIVE"))
	return remaining
}

// NewCLI creates a new CLI instance with connections to the gitslice services.
func NewCLI(accountAddr, sliceAddr, adminAddr, fileAddr, filesystemAddr string, tlsEnabled bool) (*CLI, error) {
	transportCreds := insecure.NewCredentials()
	if tlsEnabled {
		transportCreds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	connsByAddr := make(map[string]*grpc.ClientConn, 5)
	uniqueConns := make([]*grpc.ClientConn, 0, 5)
	closeConns := func() {
		for _, conn := range uniqueConns {
			if conn != nil {
				_ = conn.Close()
			}
		}
	}
	dial := func(addr string) (*grpc.ClientConn, error) {
		if conn, ok := connsByAddr[addr]; ok {
			return conn, nil
		}
		conn, err := grpc.Dial(
			addr,
			grpc.WithTransportCredentials(transportCreds),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(grpcMaxCallRecvBytes),
				grpc.MaxCallSendMsgSize(grpcMaxCallSendBytes),
			),
		)
		if err != nil {
			return nil, err
		}
		connsByAddr[addr] = conn
		uniqueConns = append(uniqueConns, conn)
		return conn, nil
	}

	accountConn, err := dial(accountAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to account service: %w", err)
	}

	sliceConn, err := dial(sliceAddr)
	if err != nil {
		closeConns()
		return nil, fmt.Errorf("failed to connect to slice service: %w", err)
	}

	adminConn, err := dial(adminAddr)
	if err != nil {
		closeConns()
		return nil, fmt.Errorf("failed to connect to admin service: %w", err)
	}

	fileConn, err := dial(fileAddr)
	if err != nil {
		closeConns()
		return nil, fmt.Errorf("failed to connect to file service: %w", err)
	}

	filesystemConn, err := dial(filesystemAddr)
	if err != nil {
		closeConns()
		return nil, fmt.Errorf("failed to connect to filesystem service: %w", err)
	}

	return &CLI{
		accountConn:      accountConn,
		sliceConn:        sliceConn,
		adminConn:        adminConn,
		fileConn:         fileConn,
		filesystemConn:   filesystemConn,
		conns:            uniqueConns,
		accountClient:    accountv1.NewAccountServiceClient(accountConn),
		sliceClient:      slicev1.NewSliceServiceClient(sliceConn),
		adminClient:      adminv1.NewAdminServiceClient(adminConn),
		fileClient:       filev1.NewFileServiceClient(fileConn),
		filesystemClient: filesystemv1.NewFilesystemServiceClient(filesystemConn),
	}, nil
}

// Close closes all gRPC connections.
func (c *CLI) Close() {
	for _, conn := range c.conns {
		if conn != nil {
			_ = conn.Close()
		}
	}
}
