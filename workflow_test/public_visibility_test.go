package workflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
)

type gatewayFileEnvelope struct {
	File gatewayPublicFile `json:"file"`
}

type gatewayPublicFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func TestPublicGatewayRoutesRespectVisibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if testStorage == nil {
		t.Fatalf("expected integration storage")
	}

	sliceID := fmt.Sprintf("public-vis-%d", time.Now().UnixNano())
	slice := &models.Slice{
		ID:        sliceID,
		Name:      sliceID,
		Owners:    []string{"tester"},
		CreatedBy: "tester",
		Files: []string{
			"docs/public.txt",
			"docs/private.txt",
		},
	}
	if err := testStorage.CreateSlice(ctx, slice); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	for filePath, content := range map[string]string{
		"docs/public.txt":  "hello public",
		"docs/private.txt": "hidden",
	} {
		mustWriteSliceManifest(t, ctx, testStorage, sliceID, filePath, []byte(content))
		if err := testStorage.AddEntry(ctx, &models.DirectoryEntry{
			ID:       common.GenerateEntryID(sliceID, filePath),
			Path:     filePath,
			Type:     "file",
			ParentID: sliceID,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("AddEntry(%s) failed: %v", filePath, err)
		}
		if err := testStorage.AddFileToSlice(ctx, filePath, sliceID); err != nil {
			t.Fatalf("AddFileToSlice(%s) failed: %v", filePath, err)
		}
	}
	if err := testStorage.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs",
		EntryType:  models.PathVisibilityEntryTypeDirectory,
		Visibility: models.VisibilityPublic,
		UpdatedBy:  "tester",
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule(dir) failed: %v", err)
	}
	if err := testStorage.UpsertPathVisibilityRule(ctx, &models.PathVisibilityRule{
		Path:       "/docs/private.txt",
		EntryType:  models.PathVisibilityEntryTypeFile,
		Visibility: models.VisibilityPrivate,
		UpdatedBy:  "tester",
	}); err != nil {
		t.Fatalf("UpsertPathVisibilityRule(file) failed: %v", err)
	}

	entries := fetchPublicGatewayEntries(t, fmt.Sprintf("%s/v1/public/entries/docs?slice_id=%s", gatewayServiceURL, sliceID))
	assertGatewayEntryNames(t, entries.Entries, "public.txt")

	file := fetchPublicGatewayFile(t, fmt.Sprintf("%s/v1/public/files/docs/public.txt?slice_id=%s", gatewayServiceURL, sliceID))
	raw, err := base64.StdEncoding.DecodeString(file.File.Content)
	if err != nil {
		t.Fatalf("DecodeString failed: %v", err)
	}
	if got := string(raw); got != "hello public" {
		t.Fatalf("public file content = %q, want %q", got, "hello public")
	}

	assertGatewayStatus(t, fmt.Sprintf("%s/v1/public/files/docs/private.txt?slice_id=%s", gatewayServiceURL, sliceID), http.StatusNotFound)
}

func fetchPublicGatewayEntries(t *testing.T, url string) gatewayEntriesResponse {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("public gateway request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("public gateway status %d: %s", resp.StatusCode, string(body))
	}

	var payload gatewayEntriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("gateway decode failed: %v", err)
	}
	return payload
}

func fetchPublicGatewayFile(t *testing.T, url string) gatewayFileEnvelope {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("public file request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("public file status %d: %s", resp.StatusCode, string(body))
	}

	var payload gatewayFileEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("file decode failed: %v", err)
	}
	return payload
}

func assertGatewayStatus(t *testing.T, url string, expected int) {
	t.Helper()

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, expected, string(body))
	}
}
