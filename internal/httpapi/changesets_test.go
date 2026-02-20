package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestChangesetsAPI_RevertCommitChangeCreatesChangesetAndReturnsDiff(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-a",
		Name:      "Slice A",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.UpdateSliceMetadata(ctx, "slice-a", &models.SliceMetadata{
		SliceID:            "slice-a",
		HeadCommitHash:     "head-1",
		ModifiedFiles:      []string{},
		ModifiedFilesCount: 0,
		LastModified:       time.Now(),
	}); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}

	change := &models.FileChangeRecord{
		ID:           "commit-1-src/app.js",
		SliceID:      "slice-a",
		CommitHash:   "commit-1",
		Path:         "src/app.js",
		ChangeType:   models.ChangeTypeModify,
		LinesAdded:   12,
		LinesDeleted: 5,
		Author:       "alice",
		Message:      "feat: update app",
		Timestamp:    time.Now(),
	}
	if err := st.AddFileChange(ctx, change); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	api := NewChangesetsAPI(st)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/commits/", api.HandleCommitChangeRevert)
	mux.HandleFunc("/v1/changesets/", api.HandleChangesetDiff)
	server := httptest.NewServer(mux)
	defer server.Close()

	revertRaw := []byte(`{"changeId":"commit-1-src/app.js"}`)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/commits/commit-1/changes/revert", bytes.NewReader(revertRaw))
	req.Header.Set("Authorization", "User alice")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST revert failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from revert, got %d", resp.StatusCode)
	}

	var revertResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&revertResp); err != nil {
		t.Fatalf("decode revert response failed: %v", err)
	}
	_ = resp.Body.Close()
	changesetID, _ := revertResp["changesetId"].(string)
	if changesetID == "" {
		t.Fatalf("expected changesetId in revert response: %#v", revertResp)
	}

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/changesets/"+changesetID+"/diff", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET changeset diff failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from changeset diff, got %d", resp.StatusCode)
	}

	var diffResp struct {
		Changeset struct {
			ChangesetID   string   `json:"changesetId"`
			ModifiedFiles []string `json:"modifiedFiles"`
			Status        string   `json:"status"`
		} `json:"changeset"`
		Diff struct {
			FilesModified int `json:"filesModified"`
		} `json:"diff"`
		Changes []struct {
			Path       string `json:"path"`
			ChangeType string `json:"changeType"`
		} `json:"changes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&diffResp); err != nil {
		t.Fatalf("decode changeset diff failed: %v", err)
	}
	_ = resp.Body.Close()

	if diffResp.Changeset.ChangesetID != changesetID {
		t.Fatalf("expected changeset id %s, got %s", changesetID, diffResp.Changeset.ChangesetID)
	}
	if diffResp.Changeset.Status != "pending" {
		t.Fatalf("expected pending changeset status, got %s", diffResp.Changeset.Status)
	}
	if diffResp.Diff.FilesModified != 1 {
		t.Fatalf("expected filesModified=1, got %d", diffResp.Diff.FilesModified)
	}
	if len(diffResp.Changes) != 1 || diffResp.Changes[0].Path != "src/app.js" {
		t.Fatalf("unexpected changes payload: %#v", diffResp.Changes)
	}
}

func TestChangesetsAPI_RevertCommitChangeAuthzAndNotFound(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-authz",
		Name:      "Slice Authz",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.UpdateSliceMetadata(ctx, "slice-authz", &models.SliceMetadata{
		SliceID:            "slice-authz",
		HeadCommitHash:     "head-authz",
		ModifiedFiles:      []string{},
		ModifiedFilesCount: 0,
		LastModified:       time.Now(),
	}); err != nil {
		t.Fatalf("UpdateSliceMetadata failed: %v", err)
	}
	if err := st.AddFileChange(ctx, &models.FileChangeRecord{
		ID:         "commit-authz-file",
		SliceID:    "slice-authz",
		CommitHash: "commit-authz",
		Path:       "pkg/main.go",
		ChangeType: models.ChangeTypeModify,
		Author:     "alice",
		Message:    "touch file",
		Timestamp:  time.Now(),
	}); err != nil {
		t.Fatalf("AddFileChange failed: %v", err)
	}

	api := NewChangesetsAPI(st)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/commits/", api.HandleCommitChangeRevert)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/commits/commit-authz/changes/revert", bytes.NewReader([]byte(`{"changeId":"commit-authz-file"}`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unauthorized revert request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing auth, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/commits/commit-authz/changes/revert", bytes.NewReader([]byte(`{"changeId":"missing-change"}`)))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("missing change revert request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing change, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/commits/commit-authz/changes/revert", bytes.NewReader([]byte(`{"changeId":"commit-authz-file"}`)))
	req.Header.Set("Authorization", "User bob")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forbidden revert request failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for forbidden user, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
