package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/config"
	"github.com/niczy/gitslice/internal/gateway"
	"github.com/niczy/gitslice/internal/storage"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	accountservice "github.com/niczy/gitslice/services/account"
	adminservice "github.com/niczy/gitslice/services/admin"
	fileservice "github.com/niczy/gitslice/services/file"
	filesystemservice "github.com/niczy/gitslice/services/filesystem"
	sliceservice "github.com/niczy/gitslice/services/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestBuildCombinedCoreHandlerServesHTTPAndGRPCOnSamePort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	st := storage.NewInMemoryStorage()
	if err := st.InitializeRootSlice(ctx); err != nil {
		t.Fatalf("InitializeRootSlice failed: %v", err)
	}

	grpcServer := grpc.NewServer()
	sliceservice.RegisterGRPCServer(grpcServer, st)
	fileservice.RegisterGRPCServer(grpcServer, st)
	filesystemservice.RegisterGRPCServer(grpcServer, st)
	adminservice.RegisterGRPCServer(grpcServer, st)
	accountservice.RegisterGRPCServer(grpcServer, st)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer lis.Close()

	gatewayMux, closeConns, err := gateway.NewMux(ctx, lis.Addr().String())
	if err != nil {
		t.Fatalf("NewMux failed: %v", err)
	}
	defer closeConns()

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/health", common.HealthCheckHandler("test-core"))
	httpMux.HandleFunc("/health/db", common.DependencyHealthCheckHandler("test-core", "database", func(context.Context) error {
		return st.PingMetadata(ctx)
	}))
	httpMux.Handle("/", gateway.WithNoBodyWriteGuard(gateway.WithCORS(gateway.SlicePathCompatHandler(gatewayMux))))

	server := &http.Server{Handler: buildCombinedCoreHandler(grpcServer, httpMux)}
	defer server.Close()

	go func() {
		_ = server.Serve(lis)
	}()

	baseURL := "http://" + lis.Addr().String()
	if err := waitForHTTPStatus(baseURL+"/health", http.StatusOK, 2*time.Second); err != nil {
		t.Fatalf("health endpoint did not become ready: %v", err)
	}
	if err := waitForHTTPStatus(baseURL+"/health/db", http.StatusOK, 2*time.Second); err != nil {
		t.Fatalf("db health endpoint did not become ready: %v", err)
	}

	resp, err := http.Get(baseURL + "/v1/global/state")
	if err != nil {
		t.Fatalf("gateway request failed: %v", err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read gateway response: %v", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected gateway status %d: %s", resp.StatusCode, string(body))
	}
	var gatewayState map[string]any
	if err := json.Unmarshal(body, &gatewayState); err != nil {
		t.Fatalf("decode gateway response: %v\nbody=%s", err, string(body))
	}
	globalCommitHash, _ := gatewayState["globalCommitHash"].(string)
	if globalCommitHash == "" {
		t.Fatalf("expected gateway state to include global commit hash, got: %+v", gatewayState)
	}

	conn, err := grpc.DialContext(ctx, lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("grpc dial failed: %v", err)
	}
	defer conn.Close()

	authCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "User test"))
	rootResp, err := slicev1.NewSliceServiceClient(conn).GetRootSlice(authCtx, &slicev1.GetRootSliceRequest{})
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if rootResp.GetSliceId() != "root" {
		t.Fatalf("unexpected root slice response: %+v", rootResp)
	}
}

type fakeStartupPinger struct {
	pingErr         error
	pingMetadataErr error
	pingCalls       int
	metadataCalls   int
}

func (p *fakeStartupPinger) Ping(ctx context.Context) error {
	p.pingCalls++
	return p.pingErr
}

func (p *fakeStartupPinger) PingMetadata(ctx context.Context) error {
	p.metadataCalls++
	return p.pingMetadataErr
}

func TestVerifyStartupDependenciesSkipsMemoryStorage(t *testing.T) {
	pinger := &fakeStartupPinger{
		pingErr:         errors.New("should not ping storage"),
		pingMetadataErr: errors.New("should not ping metadata"),
	}
	err := verifyStartupDependencies(context.Background(), &config.Config{StorageType: "memory"}, pinger)
	if err != nil {
		t.Fatalf("verifyStartupDependencies returned error for memory storage: %v", err)
	}
	if pinger.metadataCalls != 0 || pinger.pingCalls != 0 {
		t.Fatalf("expected memory storage to skip dependency pings, got metadata=%d storage=%d", pinger.metadataCalls, pinger.pingCalls)
	}
}

func TestVerifyStartupDependenciesFailsWhenPostgresMetadataUnavailable(t *testing.T) {
	pinger := &fakeStartupPinger{pingMetadataErr: errors.New("dial tcp refused")}
	err := verifyStartupDependencies(context.Background(), &config.Config{StorageType: "postgres"}, pinger)
	if err == nil {
		t.Fatal("expected metadata dependency failure")
	}
	if !strings.Contains(err.Error(), "database dependency unavailable") {
		t.Fatalf("expected database dependency context, got %v", err)
	}
	if pinger.metadataCalls != 1 {
		t.Fatalf("expected one metadata ping, got %d", pinger.metadataCalls)
	}
	if pinger.pingCalls != 0 {
		t.Fatalf("expected storage ping to be skipped after metadata failure, got %d", pinger.pingCalls)
	}
}

func TestVerifyStartupDependenciesFailsWhenPostgresObjectStoreUnavailable(t *testing.T) {
	pinger := &fakeStartupPinger{pingErr: errors.New("object store unavailable")}
	err := verifyStartupDependencies(context.Background(), &config.Config{StorageType: "postgres"}, pinger)
	if err == nil {
		t.Fatal("expected storage dependency failure")
	}
	if !strings.Contains(err.Error(), "storage dependency unavailable") {
		t.Fatalf("expected storage dependency context, got %v", err)
	}
	if pinger.metadataCalls != 1 {
		t.Fatalf("expected one metadata ping, got %d", pinger.metadataCalls)
	}
	if pinger.pingCalls != 1 {
		t.Fatalf("expected one storage ping, got %d", pinger.pingCalls)
	}
}

func waitForHTTPStatus(url string, wantStatus int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == wantStatus {
				return nil
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
