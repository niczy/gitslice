package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
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

	rootResp, err := slicev1.NewSliceServiceClient(conn).GetRootSlice(ctx, &slicev1.GetRootSliceRequest{})
	if err != nil {
		t.Fatalf("GetRootSlice failed: %v", err)
	}
	if rootResp.GetSliceId() != "root_slice" {
		t.Fatalf("unexpected root slice response: %+v", rootResp)
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
