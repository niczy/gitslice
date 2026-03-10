package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"time"

	adminv1 "github.com/niczy/gitslice/proto/admin"
	filev1 "github.com/niczy/gitslice/proto/file"
	filesystemv1 "github.com/niczy/gitslice/proto/filesystem"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	coreServerAddr  = flag.String("addr", "", "Core gRPC service address (overrides slice-addr/admin-addr/file-addr)")
	sliceServerAddr = flag.String("slice-addr", "localhost:50051", "Slice service address")
	adminServerAddr = flag.String("admin-addr", "localhost:50051", "Admin service address")
	fileServerAddr  = flag.String("file-addr", "localhost:50051", "File service address")
	fsServerAddr    = flag.String("fs-addr", "localhost:50051", "Filesystem service address")
	useTLS          = flag.Bool("tls", false, "Use TLS for gRPC connections")
	apiKeyFlag      = flag.String("api-key", "", "Bearer API key or access token (overrides GS_API_KEY and ~/.gitslice/credentials.json)")
	userFlag        = flag.String("user", "", "Legacy username auth for dev use (overrides GS_USERNAME and ~/.gitslice/user after bearer auth sources)")
)

// CLI holds the gRPC connections and clients for interacting with gitslice services.
type CLI struct {
	sliceConn        *grpc.ClientConn
	adminConn        *grpc.ClientConn
	fileConn         *grpc.ClientConn
	filesystemConn   *grpc.ClientConn
	sliceClient      slicev1.SliceServiceClient
	adminClient      adminv1.AdminServiceClient
	fileClient       filev1.FileServiceClient
	filesystemClient filesystemv1.FilesystemServiceClient
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		printHelp()
		return
	}

	if args[0] == "login" {
		authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
		if err != nil && len(args) == 1 {
			log.Fatalf("Failed to resolve current auth: %v", err)
		}
		handleLogin(authConfig, args[1:])
		return
	}

	if *coreServerAddr != "" {
		*sliceServerAddr = *coreServerAddr
		*adminServerAddr = *coreServerAddr
		*fileServerAddr = *coreServerAddr
		*fsServerAddr = *coreServerAddr
	}

	cli, err := NewCLI(*sliceServerAddr, *adminServerAddr, *fileServerAddr, *fsServerAddr, *useTLS)
	if err != nil {
		log.Fatalf("Failed to initialize CLI: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	authConfig, err := resolveAuthConfig(*apiKeyFlag, *userFlag)
	if err != nil {
		log.Fatalf("Failed to resolve auth: %v", err)
	}
	ctx = withCLIAuth(ctx, authConfig)

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
	case "root":
		handleRootSlice(ctx, cli)
	case "fork":
		handleForkSlice(ctx, cli, args[1:])
	case "import":
		handleImportCommand(ctx, cli, args[1:])
	case "file":
		handleFileCommand(ctx, cli, args[1:])
	case "fs":
		handleFilesystemCommand(ctx, cli, args[1:])
	default:
		log.Printf("Unknown command: %s", args[0])
		printHelp()
	}
}

// NewCLI creates a new CLI instance with connections to the gitslice services.
func NewCLI(sliceAddr, adminAddr, fileAddr, filesystemAddr string, tlsEnabled bool) (*CLI, error) {
	transportCreds := insecure.NewCredentials()
	if tlsEnabled {
		transportCreds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	sliceConn, err := grpc.Dial(sliceAddr, grpc.WithTransportCredentials(transportCreds))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to slice service: %w", err)
	}

	adminConn, err := grpc.Dial(adminAddr, grpc.WithTransportCredentials(transportCreds))
	if err != nil {
		sliceConn.Close()
		return nil, fmt.Errorf("failed to connect to admin service: %w", err)
	}

	fileConn, err := grpc.Dial(fileAddr, grpc.WithTransportCredentials(transportCreds))
	if err != nil {
		sliceConn.Close()
		adminConn.Close()
		return nil, fmt.Errorf("failed to connect to file service: %w", err)
	}

	filesystemConn, err := grpc.Dial(filesystemAddr, grpc.WithTransportCredentials(transportCreds))
	if err != nil {
		sliceConn.Close()
		adminConn.Close()
		fileConn.Close()
		return nil, fmt.Errorf("failed to connect to filesystem service: %w", err)
	}

	return &CLI{
		sliceConn:        sliceConn,
		adminConn:        adminConn,
		fileConn:         fileConn,
		filesystemConn:   filesystemConn,
		sliceClient:      slicev1.NewSliceServiceClient(sliceConn),
		adminClient:      adminv1.NewAdminServiceClient(adminConn),
		fileClient:       filev1.NewFileServiceClient(fileConn),
		filesystemClient: filesystemv1.NewFilesystemServiceClient(filesystemConn),
	}, nil
}

// Close closes all gRPC connections.
func (c *CLI) Close() {
	if c.sliceConn != nil {
		c.sliceConn.Close()
	}
	if c.adminConn != nil {
		c.adminConn.Close()
	}
	if c.fileConn != nil {
		c.fileConn.Close()
	}
	if c.filesystemConn != nil {
		c.filesystemConn.Close()
	}
}
