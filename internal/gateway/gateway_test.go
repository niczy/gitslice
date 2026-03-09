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

func TestGatewayFilesystemSnapshots(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}

	createReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/fs/workspaces", strings.NewReader(`{"workspaceId":"gw-snap","name":"Gateway Snapshots"}`))
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

	writeV1Req, err := http.NewRequest(
		http.MethodPut,
		gatewayURL+"/v1/fs/workspaces/gw-snap/files/README.md",
		strings.NewReader(`{"workspaceId":"gw-snap","path":"README.md","content":"djEK"}`),
	)
	if err != nil {
		t.Fatalf("new write v1 request: %v", err)
	}
	writeV1Req.Header.Set("Authorization", "User tester")
	writeV1Req.Header.Set("Content-Type", "application/json")
	writeV1Resp, err := client.Do(writeV1Req)
	if err != nil {
		t.Fatalf("write v1 request failed: %v", err)
	}
	if writeV1Resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(writeV1Resp.Body)
		writeV1Resp.Body.Close()
		t.Fatalf("unexpected write v1 status %d: %s", writeV1Resp.StatusCode, string(body))
	}
	writeV1Resp.Body.Close()

	snapshotReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-snap/snapshot",
		strings.NewReader(`{"workspaceId":"gw-snap","message":"checkpoint one"}`),
	)
	if err != nil {
		t.Fatalf("new snapshot request: %v", err)
	}
	snapshotReq.Header.Set("Authorization", "User tester")
	snapshotReq.Header.Set("Content-Type", "application/json")
	snapshotResp, err := client.Do(snapshotReq)
	if err != nil {
		t.Fatalf("snapshot request failed: %v", err)
	}
	if snapshotResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(snapshotResp.Body)
		snapshotResp.Body.Close()
		t.Fatalf("unexpected snapshot status %d: %s", snapshotResp.StatusCode, string(body))
	}

	var snapshotPayload struct {
		Snapshot struct {
			SnapshotID string `json:"snapshotId"`
			Message    string `json:"message"`
			FileCount  int    `json:"fileCount"`
		} `json:"snapshot"`
	}
	if err := json.NewDecoder(snapshotResp.Body).Decode(&snapshotPayload); err != nil {
		snapshotResp.Body.Close()
		t.Fatalf("decode snapshot response: %v", err)
	}
	snapshotResp.Body.Close()
	if snapshotPayload.Snapshot.SnapshotID == "" || snapshotPayload.Snapshot.Message != "checkpoint one" || snapshotPayload.Snapshot.FileCount != 1 {
		t.Fatalf("unexpected snapshot payload: %#v", snapshotPayload)
	}

	writeV2Req, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-snap:writeFiles",
		strings.NewReader(`{"workspaceId":"gw-snap","files":[{"path":"README.md","content":"djIK"},{"path":"notes/todo.txt","content":"bGF0ZXIK"}]}`),
	)
	if err != nil {
		t.Fatalf("new write v2 request: %v", err)
	}
	writeV2Req.Header.Set("Authorization", "User tester")
	writeV2Req.Header.Set("Content-Type", "application/json")
	writeV2Resp, err := client.Do(writeV2Req)
	if err != nil {
		t.Fatalf("write v2 request failed: %v", err)
	}
	if writeV2Resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(writeV2Resp.Body)
		writeV2Resp.Body.Close()
		t.Fatalf("unexpected write v2 status %d: %s", writeV2Resp.StatusCode, string(body))
	}
	writeV2Resp.Body.Close()

	listReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-snap/snapshots?limit=3", nil)
	if err != nil {
		t.Fatalf("new list snapshots request: %v", err)
	}
	listReq.Header.Set("Authorization", "User tester")
	listResp, err := client.Do(listReq)
	if err != nil {
		t.Fatalf("list snapshots request failed: %v", err)
	}
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		listResp.Body.Close()
		t.Fatalf("unexpected list snapshots status %d: %s", listResp.StatusCode, string(body))
	}

	var listPayload struct {
		Snapshots []struct {
			SnapshotID string `json:"snapshotId"`
			Message    string `json:"message"`
			FileCount  int    `json:"fileCount"`
		} `json:"snapshots"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listPayload); err != nil {
		listResp.Body.Close()
		t.Fatalf("decode list snapshots response: %v", err)
	}
	listResp.Body.Close()
	if len(listPayload.Snapshots) != 3 {
		t.Fatalf("expected 3 snapshots, got %#v", listPayload)
	}
	if listPayload.Snapshots[1].SnapshotID != snapshotPayload.Snapshot.SnapshotID {
		t.Fatalf("expected explicit snapshot in list, got %#v", listPayload)
	}

	restoreReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-snap/restore/"+snapshotPayload.Snapshot.SnapshotID,
		strings.NewReader(`{"workspaceId":"gw-snap","snapshotId":"`+snapshotPayload.Snapshot.SnapshotID+`"}`),
	)
	if err != nil {
		t.Fatalf("new restore request: %v", err)
	}
	restoreReq.Header.Set("Authorization", "User tester")
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreResp, err := client.Do(restoreReq)
	if err != nil {
		t.Fatalf("restore request failed: %v", err)
	}
	if restoreResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(restoreResp.Body)
		restoreResp.Body.Close()
		t.Fatalf("unexpected restore status %d: %s", restoreResp.StatusCode, string(body))
	}
	restoreResp.Body.Close()

	readReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-snap/files/README.md", nil)
	if err != nil {
		t.Fatalf("new read restored file request: %v", err)
	}
	readReq.Header.Set("Authorization", "User tester")
	readResp, err := client.Do(readReq)
	if err != nil {
		t.Fatalf("read restored file request failed: %v", err)
	}
	if readResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readResp.Body)
		readResp.Body.Close()
		t.Fatalf("unexpected restored read status %d: %s", readResp.StatusCode, string(body))
	}

	var readPayload struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(readResp.Body).Decode(&readPayload); err != nil {
		readResp.Body.Close()
		t.Fatalf("decode restored read response: %v", err)
	}
	readResp.Body.Close()
	if readPayload.Content != "djEK" {
		t.Fatalf("unexpected restored file content: %#v", readPayload)
	}

	missingReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-snap/files/notes/todo.txt", nil)
	if err != nil {
		t.Fatalf("new missing file request: %v", err)
	}
	missingReq.Header.Set("Authorization", "User tester")
	missingResp, err := client.Do(missingReq)
	if err != nil {
		t.Fatalf("missing file request failed: %v", err)
	}
	defer missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(missingResp.Body)
		t.Fatalf("expected restored extra file to be missing, got %d: %s", missingResp.StatusCode, string(body))
	}
}

func TestGatewayFilesystemDiff(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}

	createReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/fs/workspaces", strings.NewReader(`{"workspaceId":"gw-diff","name":"Gateway Diff"}`))
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

	writeV1Req, err := http.NewRequest(
		http.MethodPut,
		gatewayURL+"/v1/fs/workspaces/gw-diff/files/README.md",
		strings.NewReader(`{"workspaceId":"gw-diff","path":"README.md","content":"djEK"}`),
	)
	if err != nil {
		t.Fatalf("new write v1 request: %v", err)
	}
	writeV1Req.Header.Set("Authorization", "User tester")
	writeV1Req.Header.Set("Content-Type", "application/json")
	writeV1Resp, err := client.Do(writeV1Req)
	if err != nil {
		t.Fatalf("write v1 request failed: %v", err)
	}
	if writeV1Resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(writeV1Resp.Body)
		writeV1Resp.Body.Close()
		t.Fatalf("unexpected write v1 status %d: %s", writeV1Resp.StatusCode, string(body))
	}
	writeV1Resp.Body.Close()

	snapshotReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-diff/snapshot",
		strings.NewReader(`{"workspaceId":"gw-diff","message":"checkpoint one"}`),
	)
	if err != nil {
		t.Fatalf("new snapshot request: %v", err)
	}
	snapshotReq.Header.Set("Authorization", "User tester")
	snapshotReq.Header.Set("Content-Type", "application/json")
	snapshotResp, err := client.Do(snapshotReq)
	if err != nil {
		t.Fatalf("snapshot request failed: %v", err)
	}
	if snapshotResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(snapshotResp.Body)
		snapshotResp.Body.Close()
		t.Fatalf("unexpected snapshot status %d: %s", snapshotResp.StatusCode, string(body))
	}

	var snapshotPayload struct {
		Snapshot struct {
			SnapshotID string `json:"snapshotId"`
		} `json:"snapshot"`
	}
	if err := json.NewDecoder(snapshotResp.Body).Decode(&snapshotPayload); err != nil {
		snapshotResp.Body.Close()
		t.Fatalf("decode snapshot response: %v", err)
	}
	snapshotResp.Body.Close()

	writeV2Req, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-diff:writeFiles",
		strings.NewReader(`{"workspaceId":"gw-diff","files":[{"path":"README.md","content":"djIK"},{"path":"notes/todo.txt","content":"bGF0ZXIK"}]}`),
	)
	if err != nil {
		t.Fatalf("new write v2 request: %v", err)
	}
	writeV2Req.Header.Set("Authorization", "User tester")
	writeV2Req.Header.Set("Content-Type", "application/json")
	writeV2Resp, err := client.Do(writeV2Req)
	if err != nil {
		t.Fatalf("write v2 request failed: %v", err)
	}
	if writeV2Resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(writeV2Resp.Body)
		writeV2Resp.Body.Close()
		t.Fatalf("unexpected write v2 status %d: %s", writeV2Resp.StatusCode, string(body))
	}

	var writeV2Payload struct {
		CommitHash string `json:"commitHash"`
	}
	if err := json.NewDecoder(writeV2Resp.Body).Decode(&writeV2Payload); err != nil {
		writeV2Resp.Body.Close()
		t.Fatalf("decode write v2 response: %v", err)
	}
	writeV2Resp.Body.Close()

	diffReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-diff:diff",
		strings.NewReader(`{"workspaceId":"gw-diff","fromSnapshotId":"`+snapshotPayload.Snapshot.SnapshotID+`","toSnapshotId":"`+writeV2Payload.CommitHash+`","includePatches":true}`),
	)
	if err != nil {
		t.Fatalf("new diff request: %v", err)
	}
	diffReq.Header.Set("Authorization", "User tester")
	diffReq.Header.Set("Content-Type", "application/json")
	diffResp, err := client.Do(diffReq)
	if err != nil {
		t.Fatalf("diff request failed: %v", err)
	}
	defer diffResp.Body.Close()
	if diffResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(diffResp.Body)
		t.Fatalf("unexpected diff status %d: %s", diffResp.StatusCode, string(body))
	}

	var diffPayload struct {
		Summary struct {
			FilesAdded    int `json:"filesAdded"`
			FilesModified int `json:"filesModified"`
			FilesDeleted  int `json:"filesDeleted"`
			LinesAdded    int `json:"linesAdded"`
			LinesDeleted  int `json:"linesDeleted"`
		} `json:"summary"`
		Files []struct {
			Path       string `json:"path"`
			ChangeType string `json:"changeType"`
			Patch      string `json:"patch"`
		} `json:"files"`
	}
	if err := json.NewDecoder(diffResp.Body).Decode(&diffPayload); err != nil {
		t.Fatalf("decode diff response: %v", err)
	}
	if diffPayload.Summary.FilesAdded != 1 || diffPayload.Summary.FilesModified != 1 || diffPayload.Summary.FilesDeleted != 0 {
		t.Fatalf("unexpected diff summary counts: %#v", diffPayload)
	}
	if diffPayload.Summary.LinesAdded != 2 || diffPayload.Summary.LinesDeleted != 1 {
		t.Fatalf("unexpected diff line summary: %#v", diffPayload)
	}
	if len(diffPayload.Files) != 2 {
		t.Fatalf("expected 2 diff files, got %#v", diffPayload)
	}

	filesByPath := make(map[string]struct {
		ChangeType string
		Patch      string
	}, len(diffPayload.Files))
	for _, file := range diffPayload.Files {
		filesByPath[file.Path] = struct {
			ChangeType string
			Patch      string
		}{
			ChangeType: file.ChangeType,
			Patch:      file.Patch,
		}
	}
	if filesByPath["README.md"].ChangeType != "DIFF_CHANGE_TYPE_MODIFY" || !strings.Contains(filesByPath["README.md"].Patch, "-v1") || !strings.Contains(filesByPath["README.md"].Patch, "+v2") {
		t.Fatalf("unexpected README diff payload: %#v", filesByPath["README.md"])
	}
	if filesByPath["notes/todo.txt"].ChangeType != "DIFF_CHANGE_TYPE_ADD" || !strings.Contains(filesByPath["notes/todo.txt"].Patch, "+later") {
		t.Fatalf("unexpected notes diff payload: %#v", filesByPath["notes/todo.txt"])
	}
}

func TestGatewayFilesystemFork(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}

	createReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/fs/workspaces", strings.NewReader(`{"workspaceId":"gw-source","name":"Gateway Source"}`))
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
		gatewayURL+"/v1/fs/workspaces/gw-source:writeFiles",
		strings.NewReader(`{"workspaceId":"gw-source","files":[{"path":"README.md","content":"djEK"},{"path":"docs/info.txt","content":"c291cmNlIGluZm8K"}]}`),
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

	var writePayload struct {
		CommitHash string `json:"commitHash"`
	}
	if err := json.NewDecoder(writeResp.Body).Decode(&writePayload); err != nil {
		writeResp.Body.Close()
		t.Fatalf("decode write response: %v", err)
	}
	writeResp.Body.Close()

	forkReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-source/fork",
		strings.NewReader(`{"workspaceId":"gw-source","forkWorkspaceId":"gw-fork","name":"Gateway Fork"}`),
	)
	if err != nil {
		t.Fatalf("new fork request: %v", err)
	}
	forkReq.Header.Set("Authorization", "User tester")
	forkReq.Header.Set("Content-Type", "application/json")
	forkResp, err := client.Do(forkReq)
	if err != nil {
		t.Fatalf("fork request failed: %v", err)
	}
	if forkResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(forkResp.Body)
		forkResp.Body.Close()
		t.Fatalf("unexpected fork status %d: %s", forkResp.StatusCode, string(body))
	}

	var forkPayload struct {
		Workspace struct {
			WorkspaceID string `json:"workspaceId"`
			FileCount   int    `json:"fileCount"`
		} `json:"workspace"`
		SourceWorkspaceID string `json:"sourceWorkspaceId"`
		SourceSnapshotID  string `json:"sourceSnapshotId"`
	}
	if err := json.NewDecoder(forkResp.Body).Decode(&forkPayload); err != nil {
		forkResp.Body.Close()
		t.Fatalf("decode fork response: %v", err)
	}
	forkResp.Body.Close()
	if forkPayload.Workspace.WorkspaceID != "gw-fork" || forkPayload.Workspace.FileCount != 2 {
		t.Fatalf("unexpected fork payload: %#v", forkPayload)
	}
	if forkPayload.SourceWorkspaceID != "gw-source" || forkPayload.SourceSnapshotID != writePayload.CommitHash {
		t.Fatalf("unexpected fork source metadata: %#v", forkPayload)
	}

	readForkReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-fork/files/README.md", nil)
	if err != nil {
		t.Fatalf("new read fork request: %v", err)
	}
	readForkReq.Header.Set("Authorization", "User tester")
	readForkResp, err := client.Do(readForkReq)
	if err != nil {
		t.Fatalf("read fork request failed: %v", err)
	}
	if readForkResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readForkResp.Body)
		readForkResp.Body.Close()
		t.Fatalf("unexpected fork read status %d: %s", readForkResp.StatusCode, string(body))
	}

	var readForkPayload struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(readForkResp.Body).Decode(&readForkPayload); err != nil {
		readForkResp.Body.Close()
		t.Fatalf("decode fork read response: %v", err)
	}
	readForkResp.Body.Close()
	if readForkPayload.Content != "djEK" {
		t.Fatalf("unexpected fork README content: %#v", readForkPayload)
	}

	updateSourceReq, err := http.NewRequest(
		http.MethodPut,
		gatewayURL+"/v1/fs/workspaces/gw-source/files/README.md",
		strings.NewReader(`{"workspaceId":"gw-source","path":"README.md","content":"djIK"}`),
	)
	if err != nil {
		t.Fatalf("new update source request: %v", err)
	}
	updateSourceReq.Header.Set("Authorization", "User tester")
	updateSourceReq.Header.Set("Content-Type", "application/json")
	updateSourceResp, err := client.Do(updateSourceReq)
	if err != nil {
		t.Fatalf("update source request failed: %v", err)
	}
	if updateSourceResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateSourceResp.Body)
		updateSourceResp.Body.Close()
		t.Fatalf("unexpected source update status %d: %s", updateSourceResp.StatusCode, string(body))
	}
	updateSourceResp.Body.Close()

	readForkAfterReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-fork/files/README.md", nil)
	if err != nil {
		t.Fatalf("new read fork after update request: %v", err)
	}
	readForkAfterReq.Header.Set("Authorization", "User tester")
	readForkAfterResp, err := client.Do(readForkAfterReq)
	if err != nil {
		t.Fatalf("read fork after update request failed: %v", err)
	}
	if readForkAfterResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readForkAfterResp.Body)
		readForkAfterResp.Body.Close()
		t.Fatalf("unexpected fork read after update status %d: %s", readForkAfterResp.StatusCode, string(body))
	}

	var readForkAfterPayload struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(readForkAfterResp.Body).Decode(&readForkAfterPayload); err != nil {
		readForkAfterResp.Body.Close()
		t.Fatalf("decode fork read after update response: %v", err)
	}
	readForkAfterResp.Body.Close()
	if readForkAfterPayload.Content != "djEK" {
		t.Fatalf("fork README should remain unchanged after source update: %#v", readForkAfterPayload)
	}
}

func TestGatewayFilesystemMerge(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}

	createReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/fs/workspaces", strings.NewReader(`{"workspaceId":"gw-merge-main","name":"Gateway Merge Main"}`))
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

	baseWriteReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-merge-main:writeFiles",
		strings.NewReader(`{"workspaceId":"gw-merge-main","files":[{"path":"README.md","content":"djEK"},{"path":"docs/info.txt","content":"YmFzZSBpbmZvCg=="}]}`),
	)
	if err != nil {
		t.Fatalf("new base write request: %v", err)
	}
	baseWriteReq.Header.Set("Authorization", "User tester")
	baseWriteReq.Header.Set("Content-Type", "application/json")
	baseWriteResp, err := client.Do(baseWriteReq)
	if err != nil {
		t.Fatalf("base write request failed: %v", err)
	}
	if baseWriteResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(baseWriteResp.Body)
		baseWriteResp.Body.Close()
		t.Fatalf("unexpected base write status %d: %s", baseWriteResp.StatusCode, string(body))
	}

	var baseWritePayload struct {
		CommitHash string `json:"commitHash"`
	}
	if err := json.NewDecoder(baseWriteResp.Body).Decode(&baseWritePayload); err != nil {
		baseWriteResp.Body.Close()
		t.Fatalf("decode base write response: %v", err)
	}
	baseWriteResp.Body.Close()

	forkReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-merge-main/fork",
		strings.NewReader(`{"workspaceId":"gw-merge-main","forkWorkspaceId":"gw-merge-fork","name":"Gateway Merge Fork"}`),
	)
	if err != nil {
		t.Fatalf("new fork request: %v", err)
	}
	forkReq.Header.Set("Authorization", "User tester")
	forkReq.Header.Set("Content-Type", "application/json")
	forkResp, err := client.Do(forkReq)
	if err != nil {
		t.Fatalf("fork request failed: %v", err)
	}
	if forkResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(forkResp.Body)
		forkResp.Body.Close()
		t.Fatalf("unexpected fork status %d: %s", forkResp.StatusCode, string(body))
	}
	forkResp.Body.Close()

	targetOnlyReq, err := http.NewRequest(
		http.MethodPut,
		gatewayURL+"/v1/fs/workspaces/gw-merge-main/files/LOCAL.md",
		strings.NewReader(`{"workspaceId":"gw-merge-main","path":"LOCAL.md","content":"a2VlcCBtZQo="}`),
	)
	if err != nil {
		t.Fatalf("new target-only write request: %v", err)
	}
	targetOnlyReq.Header.Set("Authorization", "User tester")
	targetOnlyReq.Header.Set("Content-Type", "application/json")
	targetOnlyResp, err := client.Do(targetOnlyReq)
	if err != nil {
		t.Fatalf("target-only write request failed: %v", err)
	}
	if targetOnlyResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(targetOnlyResp.Body)
		targetOnlyResp.Body.Close()
		t.Fatalf("unexpected target-only write status %d: %s", targetOnlyResp.StatusCode, string(body))
	}

	var targetOnlyPayload struct {
		CommitHash string `json:"commitHash"`
	}
	if err := json.NewDecoder(targetOnlyResp.Body).Decode(&targetOnlyPayload); err != nil {
		targetOnlyResp.Body.Close()
		t.Fatalf("decode target-only write response: %v", err)
	}
	targetOnlyResp.Body.Close()

	sourceWriteReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-merge-fork:writeFiles",
		strings.NewReader(`{"workspaceId":"gw-merge-fork","files":[{"path":"README.md","content":"djIK"},{"path":"notes/todo.txt","content":"bGF0ZXIK"}]}`),
	)
	if err != nil {
		t.Fatalf("new source write request: %v", err)
	}
	sourceWriteReq.Header.Set("Authorization", "User tester")
	sourceWriteReq.Header.Set("Content-Type", "application/json")
	sourceWriteResp, err := client.Do(sourceWriteReq)
	if err != nil {
		t.Fatalf("source write request failed: %v", err)
	}
	if sourceWriteResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sourceWriteResp.Body)
		sourceWriteResp.Body.Close()
		t.Fatalf("unexpected source write status %d: %s", sourceWriteResp.StatusCode, string(body))
	}

	var sourceWritePayload struct {
		CommitHash string `json:"commitHash"`
	}
	if err := json.NewDecoder(sourceWriteResp.Body).Decode(&sourceWritePayload); err != nil {
		sourceWriteResp.Body.Close()
		t.Fatalf("decode source write response: %v", err)
	}
	sourceWriteResp.Body.Close()

	mergeReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-merge-main/merge",
		strings.NewReader(`{"workspaceId":"gw-merge-main","sourceWorkspaceId":"gw-merge-fork"}`),
	)
	if err != nil {
		t.Fatalf("new merge request: %v", err)
	}
	mergeReq.Header.Set("Authorization", "User tester")
	mergeReq.Header.Set("Content-Type", "application/json")
	mergeResp, err := client.Do(mergeReq)
	if err != nil {
		t.Fatalf("merge request failed: %v", err)
	}
	if mergeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(mergeResp.Body)
		mergeResp.Body.Close()
		t.Fatalf("unexpected merge status %d: %s", mergeResp.StatusCode, string(body))
	}

	var mergePayload struct {
		BaseWorkspaceID  string   `json:"baseWorkspaceId"`
		BaseSnapshotID   string   `json:"baseSnapshotId"`
		SourceSnapshotID string   `json:"sourceSnapshotId"`
		TargetSnapshotID string   `json:"targetSnapshotId"`
		Status           string   `json:"status"`
		MergedPaths      []string `json:"mergedPaths"`
		Summary          struct {
			FilesAdded    int `json:"filesAdded"`
			FilesModified int `json:"filesModified"`
			FilesDeleted  int `json:"filesDeleted"`
			LinesAdded    int `json:"linesAdded"`
			LinesDeleted  int `json:"linesDeleted"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(mergeResp.Body).Decode(&mergePayload); err != nil {
		mergeResp.Body.Close()
		t.Fatalf("decode merge response: %v", err)
	}
	mergeResp.Body.Close()
	if mergePayload.Status != "MERGE_STATUS_SUCCESS" {
		t.Fatalf("unexpected merge payload status: %#v", mergePayload)
	}
	if mergePayload.BaseWorkspaceID != "gw-merge-main" || mergePayload.BaseSnapshotID != baseWritePayload.CommitHash {
		t.Fatalf("unexpected merge base payload: %#v", mergePayload)
	}
	if mergePayload.TargetSnapshotID != targetOnlyPayload.CommitHash || mergePayload.SourceSnapshotID != sourceWritePayload.CommitHash {
		t.Fatalf("unexpected merge snapshot payload: %#v", mergePayload)
	}
	if mergePayload.Summary.FilesAdded != 1 || mergePayload.Summary.FilesModified != 1 || mergePayload.Summary.FilesDeleted != 0 {
		t.Fatalf("unexpected merge summary counts: %#v", mergePayload)
	}
	if mergePayload.Summary.LinesAdded != 2 || mergePayload.Summary.LinesDeleted != 1 {
		t.Fatalf("unexpected merge summary lines: %#v", mergePayload)
	}
	if len(mergePayload.MergedPaths) != 2 {
		t.Fatalf("expected 2 merged paths, got %#v", mergePayload)
	}

	readReadmeReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-merge-main/files/README.md", nil)
	if err != nil {
		t.Fatalf("new merged README request: %v", err)
	}
	readReadmeReq.Header.Set("Authorization", "User tester")
	readReadmeResp, err := client.Do(readReadmeReq)
	if err != nil {
		t.Fatalf("merged README request failed: %v", err)
	}
	if readReadmeResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readReadmeResp.Body)
		readReadmeResp.Body.Close()
		t.Fatalf("unexpected merged README status %d: %s", readReadmeResp.StatusCode, string(body))
	}

	var readReadmePayload struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(readReadmeResp.Body).Decode(&readReadmePayload); err != nil {
		readReadmeResp.Body.Close()
		t.Fatalf("decode merged README response: %v", err)
	}
	readReadmeResp.Body.Close()
	if readReadmePayload.Content != "djIK" {
		t.Fatalf("unexpected merged README content: %#v", readReadmePayload)
	}

	readLocalReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-merge-main/files/LOCAL.md", nil)
	if err != nil {
		t.Fatalf("new LOCAL.md request: %v", err)
	}
	readLocalReq.Header.Set("Authorization", "User tester")
	readLocalResp, err := client.Do(readLocalReq)
	if err != nil {
		t.Fatalf("LOCAL.md request failed: %v", err)
	}
	if readLocalResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readLocalResp.Body)
		readLocalResp.Body.Close()
		t.Fatalf("unexpected LOCAL.md status %d: %s", readLocalResp.StatusCode, string(body))
	}

	var readLocalPayload struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(readLocalResp.Body).Decode(&readLocalPayload); err != nil {
		readLocalResp.Body.Close()
		t.Fatalf("decode LOCAL.md response: %v", err)
	}
	readLocalResp.Body.Close()
	if readLocalPayload.Content != "a2VlcCBtZQo=" {
		t.Fatalf("unexpected LOCAL.md content after merge: %#v", readLocalPayload)
	}
}

func TestGatewayFilesystemConflicts(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := common.EnsureRootSliceInitialized(ctx, st); err != nil {
		t.Fatalf("init root slice: %v", err)
	}

	grpcAddr := startGRPCServer(t, st)
	gatewayURL := startGatewayServer(t, grpcAddr)
	client := &http.Client{Timeout: 2 * time.Second}

	createReq, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/fs/workspaces", strings.NewReader(`{"workspaceId":"gw-conflict-main","name":"Gateway Conflict Main"}`))
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
		gatewayURL+"/v1/fs/workspaces/gw-conflict-main/files/README.md",
		strings.NewReader(`{"workspaceId":"gw-conflict-main","path":"README.md","content":"c2hhcmVkCg=="}`),
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

	forkReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-conflict-main/fork",
		strings.NewReader(`{"workspaceId":"gw-conflict-main","forkWorkspaceId":"gw-conflict-fork","name":"Gateway Conflict Fork"}`),
	)
	if err != nil {
		t.Fatalf("new fork request: %v", err)
	}
	forkReq.Header.Set("Authorization", "User tester")
	forkReq.Header.Set("Content-Type", "application/json")
	forkResp, err := client.Do(forkReq)
	if err != nil {
		t.Fatalf("fork request failed: %v", err)
	}
	if forkResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(forkResp.Body)
		forkResp.Body.Close()
		t.Fatalf("unexpected fork status %d: %s", forkResp.StatusCode, string(body))
	}
	forkResp.Body.Close()

	listReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-conflict-main/conflicts", nil)
	if err != nil {
		t.Fatalf("new list conflicts request: %v", err)
	}
	listReq.Header.Set("Authorization", "User tester")
	listResp, err := client.Do(listReq)
	if err != nil {
		t.Fatalf("list conflicts request failed: %v", err)
	}
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		listResp.Body.Close()
		t.Fatalf("unexpected list conflicts status %d: %s", listResp.StatusCode, string(body))
	}

	var listPayload struct {
		Conflicts []struct {
			Path         string   `json:"path"`
			WorkspaceIDs []string `json:"workspaceIds"`
		} `json:"conflicts"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listPayload); err != nil {
		listResp.Body.Close()
		t.Fatalf("decode list conflicts response: %v", err)
	}
	listResp.Body.Close()
	if len(listPayload.Conflicts) != 1 || listPayload.Conflicts[0].Path != "README.md" {
		t.Fatalf("unexpected list conflicts payload: %#v", listPayload)
	}

	resolveReq, err := http.NewRequest(
		http.MethodPost,
		gatewayURL+"/v1/fs/workspaces/gw-conflict-main/conflicts:resolve",
		strings.NewReader(`{"workspaceId":"gw-conflict-main","path":"README.md","preferredWorkspaceId":"gw-conflict-main"}`),
	)
	if err != nil {
		t.Fatalf("new resolve conflict request: %v", err)
	}
	resolveReq.Header.Set("Authorization", "User tester")
	resolveReq.Header.Set("Content-Type", "application/json")
	resolveResp, err := client.Do(resolveReq)
	if err != nil {
		t.Fatalf("resolve conflict request failed: %v", err)
	}
	if resolveResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resolveResp.Body)
		resolveResp.Body.Close()
		t.Fatalf("unexpected resolve conflict status %d: %s", resolveResp.StatusCode, string(body))
	}

	var resolvePayload struct {
		Conflict struct {
			Path         string   `json:"path"`
			WorkspaceIDs []string `json:"workspaceIds"`
		} `json:"conflict"`
	}
	if err := json.NewDecoder(resolveResp.Body).Decode(&resolvePayload); err != nil {
		resolveResp.Body.Close()
		t.Fatalf("decode resolve conflict response: %v", err)
	}
	resolveResp.Body.Close()
	if resolvePayload.Conflict.Path != "README.md" || len(resolvePayload.Conflict.WorkspaceIDs) != 1 || resolvePayload.Conflict.WorkspaceIDs[0] != "gw-conflict-main" {
		t.Fatalf("unexpected resolve conflict payload: %#v", resolvePayload)
	}

	listAfterReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-conflict-main/conflicts", nil)
	if err != nil {
		t.Fatalf("new list-after request: %v", err)
	}
	listAfterReq.Header.Set("Authorization", "User tester")
	listAfterResp, err := client.Do(listAfterReq)
	if err != nil {
		t.Fatalf("list-after request failed: %v", err)
	}
	if listAfterResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listAfterResp.Body)
		listAfterResp.Body.Close()
		t.Fatalf("unexpected list-after status %d: %s", listAfterResp.StatusCode, string(body))
	}

	var listAfterPayload struct {
		Conflicts []struct {
			Path string `json:"path"`
		} `json:"conflicts"`
	}
	if err := json.NewDecoder(listAfterResp.Body).Decode(&listAfterPayload); err != nil {
		listAfterResp.Body.Close()
		t.Fatalf("decode list-after response: %v", err)
	}
	listAfterResp.Body.Close()
	if len(listAfterPayload.Conflicts) != 0 {
		t.Fatalf("expected conflicts to be resolved, got %#v", listAfterPayload)
	}

	readForkReq, err := http.NewRequest(http.MethodGet, gatewayURL+"/v1/fs/workspaces/gw-conflict-fork/files/README.md", nil)
	if err != nil {
		t.Fatalf("new fork read request: %v", err)
	}
	readForkReq.Header.Set("Authorization", "User tester")
	readForkResp, err := client.Do(readForkReq)
	if err != nil {
		t.Fatalf("fork read request failed: %v", err)
	}
	if readForkResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(readForkResp.Body)
		readForkResp.Body.Close()
		t.Fatalf("unexpected fork read status %d: %s", readForkResp.StatusCode, string(body))
	}

	var readForkPayload struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(readForkResp.Body).Decode(&readForkPayload); err != nil {
		readForkResp.Body.Close()
		t.Fatalf("decode fork read response: %v", err)
	}
	readForkResp.Body.Close()
	if readForkPayload.Content != "c2hhcmVkCg==" {
		t.Fatalf("unexpected fork content after resolve: %#v", readForkPayload)
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
