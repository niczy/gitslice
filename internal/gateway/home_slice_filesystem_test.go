package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/storage"
)

func TestGatewayFilesystemHomeSliceAbsolutePaths(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}
	homeID := homeslice.IDForUsername("tester")

	writeReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/"+url.PathEscape(homeID)+":writeFiles",
		strings.NewReader(`{"workspaceId":"`+homeID+`","files":[{"path":"/tester/docs/readme.md","content":"aGVsbG8K"},{"path":"/tester/src/main.py","content":"cHJpbnQoJ2hpJykK"}]}`),
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

	readReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/"+url.PathEscape(homeID)+":readFiles",
		strings.NewReader(`{"workspaceId":"`+homeID+`","paths":["/tester/docs/readme.md","/tester/missing.md"]}`),
	)
	if err != nil {
		t.Fatalf("new read request: %v", err)
	}
	readReq.Header.Set("Authorization", "User tester")
	readReq.Header.Set("Content-Type", "application/json")
	readResp, err := client.Do(readReq)
	if err != nil {
		t.Fatalf("read request failed: %v", err)
	}
	if readResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readResp.Body)
		readResp.Body.Close()
		t.Fatalf("unexpected read status %d: %s", readResp.StatusCode, string(body))
	}

	var readPayload struct {
		Files []struct {
			Path  string `json:"path"`
			Found bool   `json:"found"`
		} `json:"files"`
	}
	if err := json.NewDecoder(readResp.Body).Decode(&readPayload); err != nil {
		readResp.Body.Close()
		t.Fatalf("decode read response: %v", err)
	}
	readResp.Body.Close()
	if len(readPayload.Files) != 2 || readPayload.Files[0].Path != "/tester/docs/readme.md" || !readPayload.Files[0].Found || readPayload.Files[1].Found {
		t.Fatalf("unexpected read payload: %#v", readPayload)
	}

	searchValues := url.Values{}
	searchValues.Set("query", "hello")
	searchValues.Set("glob", "/tester/**/*.md")
	searchReq, err := http.NewRequest(
		http.MethodGet,
		gatewayURL+"/v1/fs/workspaces/"+url.PathEscape(homeID)+":search?"+searchValues.Encode(),
		nil,
	)
	if err != nil {
		t.Fatalf("new search request: %v", err)
	}
	searchReq.Header.Set("Authorization", "User tester")
	searchResp, err := client.Do(searchReq)
	if err != nil {
		t.Fatalf("search request failed: %v", err)
	}
	if searchResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(searchResp.Body)
		searchResp.Body.Close()
		t.Fatalf("unexpected search status %d: %s", searchResp.StatusCode, string(body))
	}

	var searchPayload struct {
		Glob    string `json:"glob"`
		Matches []struct {
			Path string `json:"path"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(searchResp.Body).Decode(&searchPayload); err != nil {
		searchResp.Body.Close()
		t.Fatalf("decode search response: %v", err)
	}
	searchResp.Body.Close()
	if searchPayload.Glob != "/tester/**/*.md" || len(searchPayload.Matches) != 1 || searchPayload.Matches[0].Path != "/tester/docs/readme.md" {
		t.Fatalf("unexpected search payload: %#v", searchPayload)
	}

	rejectReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/"+url.PathEscape(homeID)+":writeFiles",
		strings.NewReader(`{"workspaceId":"`+homeID+`","files":[{"path":"/other/secret.md","content":"bm9wZQo="}]}`),
	)
	if err != nil {
		t.Fatalf("new reject request: %v", err)
	}
	rejectReq.Header.Set("Authorization", "User tester")
	rejectReq.Header.Set("Content-Type", "application/json")
	rejectResp, err := client.Do(rejectReq)
	if err != nil {
		t.Fatalf("reject request failed: %v", err)
	}
	if rejectResp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(rejectResp.Body)
		rejectResp.Body.Close()
		t.Fatalf("expected 403 for foreign path, got %d: %s", rejectResp.StatusCode, string(body))
	}
	rejectResp.Body.Close()
}
