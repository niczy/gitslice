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
	"flag"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
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
	benchGRPCAddr         string
	benchGRPCConn         *grpc.ClientConn
	benchServer           *grpc.Server
	benchStorage          storage.Storage
	benchPromotionStorage storage.Storage

	benchSliceClient slicev1.SliceServiceClient
	benchFileClient  filev1.FileServiceClient
	benchAdminClient adminv1.AdminServiceClient

	benchPromotionWaiter promotionWaiter
)

type promotionWaiter interface {
	WaitForQueuedPromotions(ctx context.Context) error
}

// TestMain initialises an in-process gRPC server backed by the selected
// benchmark storage, wires up shared clients, runs all tests, then shuts
// everything down.
func TestMain(m *testing.M) {
	flag.Parse()

	ctx := context.Background()
	st, promotionSt, cleanup, err := newBenchmarkStorage(ctx)
	if err != nil {
		fmt.Printf("Failed to initialize benchmark storage: %v\n", err)
		os.Exit(1)
	}
	benchStorage = st
	benchPromotionStorage = promotionSt

	if err = common.EnsureRootSliceInitialized(ctx, benchStorage); err != nil {
		fmt.Printf("Failed to initialize root slice: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}
	if err = seedBenchmarkRootFolders(ctx, benchStorage); err != nil {
		fmt.Printf("Failed to seed benchmark root folders: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}

	benchGRPCAddr, benchServer, err = startBenchGRPCServer(benchStorage, benchPromotionStorage)
	if err != nil {
		fmt.Printf("Failed to start gRPC server: %v\n", err)
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}

	benchGRPCConn, err = grpc.Dial(benchGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Printf("Failed to dial gRPC server: %v\n", err)
		benchServer.GracefulStop()
		if cleanup != nil {
			cleanup()
		}
		os.Exit(1)
	}

	benchSliceClient = slicev1.NewSliceServiceClient(benchGRPCConn)
	benchFileClient = filev1.NewFileServiceClient(benchGRPCConn)
	benchAdminClient = adminv1.NewAdminServiceClient(benchGRPCConn)

	code := m.Run()

	if benchPromotionWaiter != nil {
		waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := benchPromotionWaiter.WaitForQueuedPromotions(waitCtx); err != nil {
			fmt.Printf("Warning: timed out waiting for queued promotions: %v\n", err)
		}
		cancel()
	}
	_ = benchGRPCConn.Close()
	benchServer.GracefulStop()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}

func newBenchmarkStorage(ctx context.Context) (storage.Storage, storage.Storage, func(), error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("BENCHMARK_STORAGE")))
	switch backend {
	case "", "memory", "in-memory":
		st := storage.NewInMemoryStorage()
		return st, st, nil, nil
	case "postgres", "postgres-native":
		dsn := strings.TrimSpace(os.Getenv("BENCHMARK_POSTGRES_DSN"))
		if dsn == "" {
			dsn = strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
		}
		if dsn == "" {
			return nil, nil, nil, fmt.Errorf("BENCHMARK_POSTGRES_DSN or TEST_POSTGRES_DSN is required for BENCHMARK_STORAGE=postgres")
		}
		namespace := strings.TrimSpace(os.Getenv("BENCHMARK_POSTGRES_NAMESPACE"))
		if namespace == "" {
			namespace = fmt.Sprintf("benchmark-%d", time.Now().UnixNano())
		}
		options, err := benchmarkPostgresOptions()
		if err != nil {
			return nil, nil, nil, err
		}
		objectStore := storage.NewInMemoryObjectStore()
		st, err := storage.NewPostgresNativeStorageWithOptions(ctx, dsn, objectStore, namespace, options)
		if err != nil {
			return nil, nil, nil, err
		}
		promotionSt := storage.Storage(st)
		promotionOptions, ok, err := benchmarkPostgresPromotionOptions()
		if err != nil {
			_ = st.Close()
			return nil, nil, nil, err
		}
		if ok {
			promo, err := storage.NewPostgresNativeStorageWithOptions(ctx, dsn, objectStore, namespace, promotionOptions)
			if err != nil {
				_ = st.Close()
				return nil, nil, nil, err
			}
			promotionSt = promo
		}
		return st, promotionSt, func() {
			if promotionSt != st {
				if closer, ok := promotionSt.(interface{ Close() error }); ok {
					_ = closer.Close()
				}
			}
			_ = st.Close()
		}, nil
	default:
		return nil, nil, nil, fmt.Errorf("unsupported BENCHMARK_STORAGE %q", backend)
	}
}

func benchmarkPostgresOptions() (storage.PostgresNativeStorageOptions, error) {
	var options storage.PostgresNativeStorageOptions
	rawMaxConns := strings.TrimSpace(os.Getenv("BENCHMARK_POSTGRES_MAX_CONNS"))
	if rawMaxConns == "" {
		return options, nil
	}
	maxConns, err := strconv.Atoi(rawMaxConns)
	if err != nil || maxConns <= 0 {
		return options, fmt.Errorf("BENCHMARK_POSTGRES_MAX_CONNS must be a positive integer, got %q", rawMaxConns)
	}
	options.MaxConns = int32(maxConns)
	return options, nil
}

func benchmarkPostgresPromotionOptions() (storage.PostgresNativeStorageOptions, bool, error) {
	var options storage.PostgresNativeStorageOptions
	rawMaxConns := strings.TrimSpace(os.Getenv("BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS"))
	if rawMaxConns == "" {
		return options, false, nil
	}
	maxConns, err := strconv.Atoi(rawMaxConns)
	if err != nil || maxConns <= 0 {
		return options, false, fmt.Errorf("BENCHMARK_POSTGRES_PROMOTION_MAX_CONNS must be a positive integer, got %q", rawMaxConns)
	}
	options.MaxConns = int32(maxConns)
	return options, true, nil
}

func benchmarkUserCountFromEnv() (int, error) {
	numUsers := 100_000
	if v := os.Getenv("BENCHMARK_USERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("BENCHMARK_USERS must be a positive integer, got %q", v)
		}
		numUsers = n
	}
	return numUsers, nil
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
	if !shouldSeedBenchmarkUserFolders() {
		return nil
	}
	numUsers, err := benchmarkUserCountFromEnv()
	if err != nil {
		return err
	}
	if testing.Short() && numUsers > 200 {
		numUsers = 200
	}
	homeShards, err := benchmarkHomeShardCountFromEnv()
	if err != nil {
		return err
	}
	if homeShards > 1 {
		for i := 0; i < homeShards; i++ {
			username := benchmarkUsername(i)
			if _, err := homeslice.EnsureUserHomeSlice(ctx, st, username); err != nil {
				return err
			}
			if err := seedBenchmarkRootDirectory(ctx, st, username); err != nil {
				return err
			}
			if err := seedBenchmarkRootDirectory(ctx, st, username+"/bench"); err != nil {
				return err
			}
		}
	}
	for i := 0; i < numUsers; i++ {
		folder := benchmarkFolderPath(i)
		if err := seedBenchmarkRootDirectory(ctx, st, folder); err != nil {
			return err
		}
	}
	return nil
}

func seedBenchmarkRootDirectory(ctx context.Context, st storage.Storage, folder string) error {
	folder = common.CleanRelativePath(folder)
	if folder == "" {
		return nil
	}
	parent := ""
	if idx := strings.LastIndex(folder, "/"); idx >= 0 {
		parent = folder[:idx]
	}
	return st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("root", folder),
		Path:     folder,
		Type:     "directory",
		ParentID: common.GenerateEntryID("root", parent),
	})
}

func shouldSeedBenchmarkUserFolders() bool {
	runFlag := flag.Lookup("test.run")
	if runFlag == nil {
		return true
	}
	pattern := strings.TrimSpace(runFlag.Value.String())
	if pattern == "" {
		return true
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return true
	}
	return re.MatchString("TestSimulate100kUsers")
}

func shouldUseAcceptanceOnlyProjectionMode() bool {
	runFlag := flag.Lookup("test.run")
	if runFlag == nil {
		return false
	}
	pattern := strings.TrimSpace(runFlag.Value.String())
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString("TestMergeAcceptanceThroughput")
}

func startBenchGRPCServer(st storage.Storage, promotionSt storage.Storage) (string, *grpc.Server, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	srv := grpc.NewServer()
	sliceServer := sliceservice.NewInternalServiceWithPromotionStorage(st, promotionSt)
	if shouldUseAcceptanceOnlyProjectionMode() {
		sliceServer.EnableDurableProjectionModeForTesting()
	}
	benchPromotionWaiter = sliceServer
	slicev1.RegisterSliceServiceServer(srv, sliceServer)
	fileservice.RegisterGRPCServer(srv, st)
	adminservice.RegisterGRPCServer(srv, st)

	go srv.Serve(lis)
	return lis.Addr().String(), srv, nil
}

// userCtx returns a context carrying the default benchmark authorization header.
// BENCHMARK_HOME_SHARDS can spread sessions across multiple home roots when
// measuring home-scoped promotion throughput.
func userCtx(parent context.Context) context.Context {
	return userCtxForIndex(parent, 0)
}

func userCtxForIndex(parent context.Context, i int) context.Context {
	return metadata.AppendToOutgoingContext(parent, "authorization", "User "+benchmarkUsername(i))
}

func benchmarkUsername(i int) string {
	shards, err := benchmarkHomeShardCountFromEnv()
	if err != nil || shards <= 1 {
		return "bench-worker"
	}
	return fmt.Sprintf("bench-worker-%04d", i%shards)
}

// sliceID returns a deterministic, validation-safe slice ID for user i.
func sliceID(i int) string {
	return fmt.Sprintf("bench-user-%07d", i)
}

// fileID returns a deterministic, validation-safe file path for user i.
// The path uses a subdirectory per user so directory listings are isolated.
func fileID(i int) string {
	return fmt.Sprintf("%s/main.go", benchmarkFolderPath(i))
}

func benchmarkFolderPath(i int) string {
	username := benchmarkUsername(i)
	if username != "bench-worker" {
		return fmt.Sprintf("%s/bench/%07d", username, i)
	}
	return fmt.Sprintf("bench/%07d", i)
}

func benchmarkHomeShardCountFromEnv() (int, error) {
	raw := strings.TrimSpace(os.Getenv("BENCHMARK_HOME_SHARDS"))
	if raw == "" {
		return 1, nil
	}
	shards, err := strconv.Atoi(raw)
	if err != nil || shards <= 0 {
		return 0, fmt.Errorf("BENCHMARK_HOME_SHARDS must be a positive integer, got %q", raw)
	}
	return shards, nil
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
