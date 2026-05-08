// Package benchmarksuite provides a comprehensive benchmark and load test suite
// for the gitslice file service and slice service.
//
// The suite simulates large numbers of concurrent users performing changeset
// workflows, tests conflict detection under load, and verifies system integrity
// after high-concurrency operations.
//
// See README.md in this directory for usage instructions.
package benchmarksuite

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/storage"
	adminv1 "github.com/niczy/gitslice/proto/admin"
	filev1 "github.com/niczy/gitslice/proto/file"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	adminservice "github.com/niczy/gitslice/services/admin"
	fileservice "github.com/niczy/gitslice/services/file"
	sliceservice "github.com/niczy/gitslice/services/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var (
	benchGRPCAddr string
	benchGRPCConn *grpc.ClientConn
	benchServer   *grpc.Server
	benchStorage  storage.Storage

	benchSliceClient slicev1.SliceServiceClient
	benchFileClient  filev1.FileServiceClient
	benchAdminClient adminv1.AdminServiceClient
)

// TestMain initialises an in-process gRPC server backed by InMemoryStorage,
// wires up shared clients, runs all tests, then shuts everything down.
func TestMain(m *testing.M) {
	var err error
	benchStorage = storage.NewInMemoryStorage()

	ctx := context.Background()
	if err = common.EnsureRootSliceInitialized(ctx, benchStorage); err != nil {
		fmt.Printf("Failed to initialize root slice: %v\n", err)
		os.Exit(1)
	}
	if err = seedBenchmarkRootFolders(ctx, benchStorage); err != nil {
		fmt.Printf("Failed to seed benchmark root folders: %v\n", err)
		os.Exit(1)
	}

	benchGRPCAddr, benchServer, err = startBenchGRPCServer(benchStorage)
	if err != nil {
		fmt.Printf("Failed to start gRPC server: %v\n", err)
		os.Exit(1)
	}

	benchGRPCConn, err = grpc.Dial(benchGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Printf("Failed to dial gRPC server: %v\n", err)
		benchServer.GracefulStop()
		os.Exit(1)
	}

	benchSliceClient = slicev1.NewSliceServiceClient(benchGRPCConn)
	benchFileClient = filev1.NewFileServiceClient(benchGRPCConn)
	benchAdminClient = adminv1.NewAdminServiceClient(benchGRPCConn)

	code := m.Run()

	_ = benchGRPCConn.Close()
	benchServer.GracefulStop()
	os.Exit(code)
}

func seedBenchmarkRootFolders(ctx context.Context, st storage.Storage) error {
	for _, folder := range []string{"bench", "cc", "conflict", "fsread", "hotfile", "integrity"} {
		seedPath := fmt.Sprintf("%s/.seed", folder)
		if _, err := storage.WriteSliceFileManifest(ctx, st, "root", seedPath, []byte("seed\n")); err != nil {
			return err
		}
		if err := st.AddFileToSlice(ctx, seedPath, "root"); err != nil {
			return err
		}
	}
	return nil
}

func startBenchGRPCServer(st storage.Storage) (string, *grpc.Server, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	srv := grpc.NewServer()
	sliceservice.RegisterGRPCServer(srv, st)
	fileservice.RegisterGRPCServer(srv, st)
	adminservice.RegisterGRPCServer(srv, st)

	go srv.Serve(lis)
	return lis.Addr().String(), srv, nil
}

// userCtx returns a context carrying the "bench-worker" authorization header.
// All benchmark workers share a single username to avoid per-user metadata
// overhead; unique identity is encoded in slice and file IDs instead.
func userCtx(parent context.Context) context.Context {
	return metadata.AppendToOutgoingContext(parent, "authorization", "User bench-worker")
}

// sliceID returns a deterministic, validation-safe slice ID for user i.
func sliceID(i int) string {
	return fmt.Sprintf("bench-user-%07d", i)
}

// fileID returns a deterministic, validation-safe file path for user i.
// The path uses a subdirectory per user so directory listings are isolated.
func fileID(i int) string {
	return fmt.Sprintf("bench/%07d/main.go", i)
}

// percentile returns the p-th percentile value (0–100) from a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p / 100.0)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// skippedInShortMode calls t.Skip if -test.short is set and n exceeds the
// short-mode limit.
func skippedInShortMode(t *testing.T, n int) {
	t.Helper()
	if testing.Short() && n > 200 {
		t.Skipf("skipping large load test in short mode (use -count=1 without -short to run %d users)", n)
	}
}
