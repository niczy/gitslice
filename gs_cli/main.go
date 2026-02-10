package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"time"

	adminv1 "github.com/niczy/gitslice/proto/admin"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	coreServerAddr  = flag.String("addr", "", "Core gRPC service address (overrides slice-addr/admin-addr)")
	sliceServerAddr = flag.String("slice-addr", "localhost:50051", "Slice service address")
	adminServerAddr = flag.String("admin-addr", "localhost:50051", "Admin service address")
	useTLS          = flag.Bool("tls", false, "Use TLS for gRPC connections")
)

// CLI holds the gRPC connections and clients for interacting with gitslice services.
type CLI struct {
	sliceConn   *grpc.ClientConn
	adminConn   *grpc.ClientConn
	sliceClient slicev1.SliceServiceClient
	adminClient adminv1.AdminServiceClient
}

func main() {
	flag.Parse()

	if *coreServerAddr != "" {
		*sliceServerAddr = *coreServerAddr
		*adminServerAddr = *coreServerAddr
	}

	cli, err := NewCLI(*sliceServerAddr, *adminServerAddr, *useTLS)
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
	case "root":
		handleRootSlice(ctx, cli)
	case "fork":
		handleForkSlice(ctx, cli, args[1:])
	case "import":
		handleImportCommand(ctx, cli, args[1:])
	default:
		log.Printf("Unknown command: %s", args[0])
		printHelp()
	}
}

// NewCLI creates a new CLI instance with connections to the gitslice services.
func NewCLI(sliceAddr, adminAddr string, tlsEnabled bool) (*CLI, error) {
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

	return &CLI{
		sliceConn:   sliceConn,
		adminConn:   adminConn,
		sliceClient: slicev1.NewSliceServiceClient(sliceConn),
		adminClient: adminv1.NewAdminServiceClient(adminConn),
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
}
