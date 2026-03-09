package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	agentsession "github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	adminservice "github.com/niczy/gitslice/services/admin"
	agentservice "github.com/niczy/gitslice/services/agent"
	fileservice "github.com/niczy/gitslice/services/file"
	filesystemservice "github.com/niczy/gitslice/services/filesystem"
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

func TestGatewayGetFileETagAndConditional304(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "gateway",
		Name:      "gateway",
		Owners:    []string{"system"},
		CreatedBy: "system",
		Files:     []string{"README.md"},
	}); err != nil {
		t.Fatalf("create slice: %v", err)
	}

	const (
		filePath = "README.md"
		fileHash = "gateway-hash"
	)
	if err := st.AddFileContent(ctx, &models.FileContent{
		FileID:  filePath,
		Path:    filePath,
		Content: []byte("root file"),
		Size:    9,
		Hash:    fileHash,
	}); err != nil {
		t.Fatalf("add file content: %v", err)
	}
	if err := st.AddEntry(ctx, &models.DirectoryEntry{
		ID:       common.GenerateEntryID("gateway", filePath),
		Path:     filePath,
		Type:     "file",
		ParentID: "gateway",
		Size:     9,
	}); err != nil {
		t.Fatalf("add entry: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)

	client := &http.Client{Timeout: 2 * time.Second}
	firstReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/slices/gateway/files/%s", gatewayURL, filePath), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	firstReq.Header.Set("Authorization", "User system")
	firstResp, err := client.Do(firstReq)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	if firstResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(firstResp.Body)
		firstResp.Body.Close()
		t.Fatalf("unexpected first status %d: %s", firstResp.StatusCode, string(body))
	}
	etag := firstResp.Header.Get("ETag")
	if etag != `"`+fileHash+`"` {
		firstResp.Body.Close()
		t.Fatalf("expected ETag %q, got %q", `"`+fileHash+`"`, etag)
	}
	firstResp.Body.Close()

	secondReq, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/v1/slices/gateway/files/%s", gatewayURL, filePath), nil)
	if err != nil {
		t.Fatalf("new conditional request: %v", err)
	}
	secondReq.Header.Set("Authorization", "User system")
	secondReq.Header.Set("If-None-Match", etag)
	secondResp, err := client.Do(secondReq)
	if err != nil {
		t.Fatalf("conditional request failed: %v", err)
	}
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusNotModified {
		body, _ := io.ReadAll(secondResp.Body)
		t.Fatalf("expected 304, got %d: %s", secondResp.StatusCode, string(body))
	}
	if got := secondResp.Header.Get("ETag"); got != etag {
		t.Fatalf("expected ETag %q on 304, got %q", etag, got)
	}
	body, _ := io.ReadAll(secondResp.Body)
	if len(body) != 0 {
		t.Fatalf("expected empty body on 304, got %q", string(body))
	}
}

func TestGatewayFilesystemWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}

	createReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/fs/workspaces", strings.NewReader(`{"workspaceId":"gw-ws","name":"Gateway Workspace"}`))
	if err != nil {
		t.Fatalf("new create request: %v", err)
	}
	createReq.Header.Set("Authorization", "User tester")
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("unexpected create status %d: %s", createResp.StatusCode, string(body))
	}
	createResp.Body.Close()

	writeReq, err := http.NewRequest(
		http.MethodPut,
		gatewayURL+"/v1/fs/workspaces/gw-ws/files/docs/readme.md",
		strings.NewReader(`{"workspaceId":"gw-ws","path":"docs/readme.md","content":"aGVsbG8gd29ybGQK"}`),
	)
	if err != nil {
		t.Fatalf("new write request: %v", err)
	}
	writeReq.Header.Set("Authorization", "User tester")
	writeReq.Header.Set("Content-Type", "application/json")
	writeResp, err := client.Do(writeReq)
	if err != nil {
		t.Fatalf("write request failed: %v", err)
	}
	if writeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(writeResp.Body)
		writeResp.Body.Close()
		t.Fatalf("unexpected write status %d: %s", writeResp.StatusCode, string(body))
	}
	writeResp.Body.Close()

	readReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-ws/files/docs/readme.md", nil)
	if err != nil {
		t.Fatalf("new read request: %v", err)
	}
	readReq.Header.Set("Authorization", "User tester")
	readResp, err := client.Do(readReq)
	if err != nil {
		t.Fatalf("read request failed: %v", err)
	}
	defer readResp.Body.Close()
	if readResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readResp.Body)
		t.Fatalf("unexpected read status %d: %s", readResp.StatusCode, string(body))
	}

	var payload struct {
		WorkspaceID string `json:"workspaceId"`
		Path        string `json:"path"`
		Content     string `json:"content"`
	}
	if err := json.NewDecoder(readResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if payload.WorkspaceID != "gw-ws" || payload.Path != "docs/readme.md" {
		t.Fatalf("unexpected read payload: %#v", payload)
	}
	if payload.Content != "aGVsbG8gd29ybGQK" {
		t.Fatalf("unexpected read content: %q", payload.Content)
	}
}

func TestGatewayFilesystemBatchOperations(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}

	createReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/fs/workspaces", strings.NewReader(`{"workspaceId":"gw-batch","name":"Gateway Batch"}`))
	if err != nil {
		t.Fatalf("new create request: %v", err)
	}
	createReq.Header.Set("Authorization", "User tester")
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatalf("create request failed: %v", err)
	}
	if createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		createResp.Body.Close()
		t.Fatalf("unexpected create status %d: %s", createResp.StatusCode, string(body))
	}
	createResp.Body.Close()

	writeReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-batch:writeFiles",
		strings.NewReader(`{"workspaceId":"gw-batch","files":[{"path":"docs/readme.md","content":"aGVsbG8K"},{"path":"src/app.go","content":"cGFja2FnZSBtYWluCg=="}]}`),
	)
	if err != nil {
		t.Fatalf("new batch write request: %v", err)
	}
	writeReq.Header.Set("Authorization", "User tester")
	writeReq.Header.Set("Content-Type", "application/json")
	writeResp, err := client.Do(writeReq)
	if err != nil {
		t.Fatalf("batch write request failed: %v", err)
	}
	if writeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(writeResp.Body)
		writeResp.Body.Close()
		t.Fatalf("unexpected batch write status %d: %s", writeResp.StatusCode, string(body))
	}
	writeResp.Body.Close()

	readReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-batch:readFiles",
		strings.NewReader(`{"workspaceId":"gw-batch","paths":["docs/readme.md","missing.md"]}`),
	)
	if err != nil {
		t.Fatalf("new batch read request: %v", err)
	}
	readReq.Header.Set("Authorization", "User tester")
	readReq.Header.Set("Content-Type", "application/json")
	readResp, err := client.Do(readReq)
	if err != nil {
		t.Fatalf("batch read request failed: %v", err)
	}
	if readResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readResp.Body)
		readResp.Body.Close()
		t.Fatalf("unexpected batch read status %d: %s", readResp.StatusCode, string(body))
	}

	var readPayload struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Found   bool   `json:"found"`
			Error   string `json:"error"`
		} `json:"files"`
	}
	if err := json.NewDecoder(readResp.Body).Decode(&readPayload); err != nil {
		readResp.Body.Close()
		t.Fatalf("decode batch read response: %v", err)
	}
	readResp.Body.Close()
	if len(readPayload.Files) != 2 {
		t.Fatalf("expected 2 batch read results, got %#v", readPayload.Files)
	}
	if !readPayload.Files[0].Found || readPayload.Files[0].Path != "docs/readme.md" || readPayload.Files[0].Content != "aGVsbG8K" {
		t.Fatalf("unexpected first batch read result: %#v", readPayload.Files[0])
	}
	if readPayload.Files[1].Found || readPayload.Files[1].Error != "file not found" {
		t.Fatalf("unexpected second batch read result: %#v", readPayload.Files[1])
	}

	searchValues := url.Values{}
	searchValues.Set("query", "hello")
	searchValues.Set("glob", "**/*.md")
	searchReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-batch:search?"+searchValues.Encode(), nil)
	if err != nil {
		t.Fatalf("new search request: %v", err)
	}
	searchReq.Header.Set("Authorization", "User tester")
	searchResp, err := client.Do(searchReq)
	if err != nil {
		t.Fatalf("search request failed: %v", err)
	}
	defer searchResp.Body.Close()
	if searchResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(searchResp.Body)
		t.Fatalf("unexpected search status %d: %s", searchResp.StatusCode, string(body))
	}

	var searchPayload struct {
		Matches []struct {
			Path string `json:"path"`
			Line string `json:"line"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(searchResp.Body).Decode(&searchPayload); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if len(searchPayload.Matches) != 1 || searchPayload.Matches[0].Path != "docs/readme.md" || searchPayload.Matches[0].Line != "hello" {
		t.Fatalf("unexpected search payload: %#v", searchPayload)
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
	filesystemservice.RegisterGRPCServer(srv, st)
	adminservice.RegisterGRPCServer(srv, st)
	agentservice.RegisterGRPCServer(srv, st, agentsession.NewService(st, "test-secret"))

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
