package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestAgentSessionsAPI(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http",
		Name:      "Slice HTTP",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := agentsession.NewService(st, "test-secret")
	api := NewAgentSessionsAPI(st, svc)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent-sessions", api.HandleCollection)
	mux.HandleFunc("/v1/agent-sessions/", api.HandleItem)
	server := httptest.NewServer(mux)
	defer server.Close()

	createBody := map[string]any{
		"sliceId":       "slice-http",
		"provider":      "e2b",
		"e2bTemplateId": "tmpl-v1",
		"e2bRegion":     "us-west-2",
	}
	createRaw, _ := json.Marshal(createBody)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(createRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/agent-sessions failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var createResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	if createResp.SessionID == "" {
		t.Fatalf("missing sessionId in create response")
	}

	waitForHTTPState(t, server.URL, createResp.SessionID, "alice", "running", 2*time.Second)

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions/"+createResp.SessionID+"/token", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected token 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/agent-sessions/"+createResp.SessionID+"/events?sinceSeq=0&limit=20", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected events 200, got %d", resp.StatusCode)
	}
	var eventsResp struct {
		Events []any `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&eventsResp); err != nil {
		t.Fatalf("decode events response failed: %v", err)
	}
	_ = resp.Body.Close()
	if len(eventsResp.Events) == 0 {
		t.Fatalf("expected non-empty events")
	}

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions/"+createResp.SessionID+"/stop", bytes.NewReader([]byte(`{"reason":"test"}`)))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST stop failed: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected stop status 202/200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	waitForHTTPState(t, server.URL, createResp.SessionID, "alice", "stopped", 2*time.Second)
}

func TestAgentSessionsAPIRejectsCrossUserAccess(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http-authz",
		Name:      "Slice HTTP Authz",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := agentsession.NewService(st, "test-secret")
	api := NewAgentSessionsAPI(st, svc)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent-sessions", api.HandleCollection)
	mux.HandleFunc("/v1/agent-sessions/", api.HandleItem)
	server := httptest.NewServer(mux)
	defer server.Close()

	createRaw := []byte(`{"sliceId":"slice-http-authz","provider":"e2b","e2bTemplateId":"tmpl-v1"}`)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(createRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	var createResp struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&createResp)
	_ = resp.Body.Close()
	if createResp.SessionID == "" {
		t.Fatalf("expected session id")
	}

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/agent-sessions/"+createResp.SessionID, nil)
	req.Header.Set("Authorization", "User bob")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cross-user get failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-user access, got %d", resp.StatusCode)
	}
}

func waitForHTTPState(t *testing.T, baseURL, sessionID, user, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/agent-sessions/"+sessionID, nil)
		req.Header.Set("Authorization", "User "+user)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var body struct {
				State string `json:"state"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			if body.State == want {
				return
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for state %s", want)
}
