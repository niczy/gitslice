package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminservice "github.com/niczy/gitslice/services/admin"
	fileservice "github.com/niczy/gitslice/services/file"
	sliceservice "github.com/niczy/gitslice/services/slice"
	"google.golang.org/grpc"
)

func TestGatewayListEntries(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	rootSlice, err := st.GetRootSlice(ctx)
	if err != nil {
		t.Fatalf("load root slice: %v", err)
	}

	files := []struct {
		id      string
		path    string
		content string
	}{
		{id: "README.md", path: "README.md", content: "root file"},
		{id: "docs/readme.md", path: "docs/readme.md", content: "nested file"},
	}
	for _, file := range files {
		if err := st.AddFileContent(ctx, &models.FileContent{
			FileID:  file.id,
			Path:    file.path,
			Content: []byte(file.content),
		}); err != nil {
			t.Fatalf("add file content: %v", err)
		}
	}

	fileIDs := []string{}
	for _, file := range files {
		fileIDs = append(fileIDs, file.id)
	}
	if err := st.SetSliceFiles(ctx, rootSlice.ID, fileIDs); err != nil && err != storage.ErrSliceFilesImmutable {
		t.Fatalf("set slice files: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)

	resp := fetchEntries(t, fmt.Sprintf("%s/v1/files/entries", gatewayURL))
	names := map[string]bool{}
	for _, entry := range resp.Entries {
		names[entry.Name] = true
	}
	if !names["README.md"] || !names["docs"] {
		t.Fatalf("unexpected entries: %#v", resp.Entries)
	}

	resp = fetchEntries(t, fmt.Sprintf("%s/v1/files/entries/docs", gatewayURL))
	if len(resp.Entries) != 1 || resp.Entries[0].Name != "readme.md" {
		t.Fatalf("unexpected nested entries: %#v", resp.Entries)
	}
}

type entriesResponse struct {
	Entries []entryResponse `json:"entries"`
}

type entryResponse struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type any    `json:"type"`
}

func fetchEntries(t *testing.T, url string) entriesResponse {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}

		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(body))
		}

		var payload entriesResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return payload
	}
	t.Fatalf("request failed: %v", lastErr)
	return entriesResponse{}
}

func startGRPCServer(t *testing.T, st storage.Storage) string {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := grpc.NewServer()
	sliceservice.RegisterGRPCServer(srv, st)
	fileservice.RegisterGRPCServer(srv, st)
	adminservice.RegisterGRPCServer(srv, st)

	go func() {
		if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("grpc serve: %v", err)
		}
	}()

	t.Cleanup(func() {
		srv.GracefulStop()
	})

	return lis.Addr().String()
}

func startGatewayServer(t *testing.T, grpcAddr string) string {
	t.Helper()

	ctx := context.Background()
	gatewayMux, closeConns, err := NewMux(ctx, grpcAddr)
	if err != nil {
		t.Fatalf("register gateway mux: %v", err)
	}

	server := &http.Server{Handler: WithCORS(gatewayMux)}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen gateway: %v", err)
	}

	go func() {
		if err := server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Logf("gateway serve: %v", err)
		}
	}()

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		closeConns()
	})

	return "http://" + lis.Addr().String()
}
