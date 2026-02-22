package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func seedEnvironment(t *testing.T, ctx context.Context, st storage.Storage, name, providerID, region string) {
	t.Helper()
	err := st.CreateEnvironment(ctx, &models.Environment{
		Name:        name,
		DisplayName: name,
		Provider:    "e2b",
		ProviderID:  providerID,
		Region:      region,
		CreatedBy:   "alice",
	})
	if err != nil && err != storage.ErrEntryExists {
		t.Fatalf("CreateEnvironment failed: %v", err)
	}
}

func TestAgentSessionsAPI(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	seedEnvironment(t, ctx, st, "node20", "tmpl-v1", "us-west-2")
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
	mux.HandleFunc("/ws/sessions/", api.HandleWS)
	server := httptest.NewServer(mux)
	defer server.Close()

	createBody := map[string]any{
		"sliceId":     "slice-http",
		"environment": "node20",
		"agentType":   "claude",
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
		SessionID     string  `json:"sessionId"`
		Environment   string  `json:"environment"`
		AgentType     string  `json:"agentType"`
		Provider      *string `json:"provider"`
		E2BTemplateID *string `json:"e2bTemplateId"`
		E2BSandboxID  *string `json:"e2bSandboxId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	if createResp.SessionID == "" {
		t.Fatalf("missing sessionId in create response")
	}
	if createResp.Environment != "node20" {
		t.Fatalf("expected environment node20, got %q", createResp.Environment)
	}
	if createResp.AgentType != "claude" {
		t.Fatalf("expected agentType claude, got %q", createResp.AgentType)
	}
	if createResp.Provider != nil || createResp.E2BTemplateID != nil || createResp.E2BSandboxID != nil {
		t.Fatalf("create response should not expose provider/e2b fields: %#v", createResp)
	}

	waitForHTTPState(t, server.URL, createResp.SessionID, "alice", "running", 2*time.Second)

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/agent-sessions/"+createResp.SessionID, nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET session failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected get session 200, got %d", resp.StatusCode)
	}
	var getResp struct {
		SessionID    string  `json:"sessionId"`
		Environment  string  `json:"environment"`
		AgentType    string  `json:"agentType"`
		Provider     *string `json:"provider"`
		E2BSandboxID *string `json:"e2bSandboxId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response failed: %v", err)
	}
	_ = resp.Body.Close()
	if getResp.Environment != "node20" {
		t.Fatalf("expected get environment node20, got %q", getResp.Environment)
	}
	if getResp.AgentType != "claude" {
		t.Fatalf("expected get agentType claude, got %q", getResp.AgentType)
	}
	if getResp.Provider != nil || getResp.E2BSandboxID != nil {
		t.Fatalf("get response should not expose provider/e2bSandboxId: %#v", getResp)
	}

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
	seedEnvironment(t, ctx, st, "node20", "tmpl-v1", "us-west-2")
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
	mux.HandleFunc("/ws/sessions/", api.HandleWS)
	server := httptest.NewServer(mux)
	defer server.Close()

	createRaw := []byte(`{"sliceId":"slice-http-authz","environment":"node20"}`)
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

func TestAgentSessionsCapabilities(t *testing.T) {
	st := storage.NewInMemoryStorage()
	svc := agentsession.NewService(st, "test-secret")
	api := NewAgentSessionsAPI(st, svc)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent-sessions", api.HandleCollection)
	mux.HandleFunc("/v1/agent-sessions/", api.HandleItem)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/agent-sessions/capabilities", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("capabilities request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var out struct {
		SupportedAgentTypes []string `json:"supportedAgentTypes"`
		DefaultAgentType    string   `json:"defaultAgentType"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if out.DefaultAgentType != "codex" {
		t.Fatalf("expected default codex, got %q", out.DefaultAgentType)
	}
	if len(out.SupportedAgentTypes) != 2 || out.SupportedAgentTypes[0] != "codex" || out.SupportedAgentTypes[1] != "claude" {
		t.Fatalf("unexpected supported agent types: %#v", out.SupportedAgentTypes)
	}
}

func TestAgentSessionsWSFlowAndTokenReuse(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	seedEnvironment(t, ctx, st, "node20", "tmpl-v1", "us-west-2")
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http-ws",
		Name:      "Slice HTTP WS",
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
	mux.HandleFunc("/ws/sessions/", api.HandleWS)
	server := httptest.NewServer(mux)
	defer server.Close()

	createRaw := []byte(`{"sliceId":"slice-http-ws","environment":"node20"}`)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(createRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	var createResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	_ = resp.Body.Close()
	if createResp.SessionID == "" {
		t.Fatalf("missing session id")
	}
	waitForHTTPState(t, server.URL, createResp.SessionID, "alice", "running", 2*time.Second)

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions/"+createResp.SessionID+"/token", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request failed: %v", err)
	}
	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token response failed: %v", err)
	}
	_ = resp.Body.Close()
	if tokenResp.Token == "" {
		t.Fatalf("missing token")
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/sessions/" + createResp.SessionID + "?token=" + url.QueryEscape(tokenResp.Token) + "&lastSeq=0"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	if err := conn.WriteJSON(map[string]any{
		"stream": "control",
		"type":   "ping",
		"payload": map[string]string{
			"nonce": "abc123",
		},
	}); err != nil {
		t.Fatalf("write ping failed: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"stream": "pty",
		"type":   "stdin",
		"payload": map[string]string{
			"data": "echo hello\n",
		},
	}); err != nil {
		t.Fatalf("write stdin failed: %v", err)
	}

	gotPong := false
	gotStdout := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (!gotPong || !gotStdout) {
		var frame struct {
			Stream  string                 `json:"stream"`
			Type    string                 `json:"type"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("read ws frame failed: %v", err)
		}
		if frame.Stream == "control" && frame.Type == "pong" {
			if nonce, ok := frame.Payload["nonce"].(string); ok && nonce == "abc123" {
				gotPong = true
			}
		}
		if frame.Stream == "pty" && frame.Type == "stdout" {
			if data, ok := frame.Payload["data"].(string); ok && strings.Contains(data, "echo hello") {
				gotStdout = true
			}
		}
	}
	if !gotPong {
		t.Fatalf("did not receive pong frame")
	}
	if !gotStdout {
		t.Fatalf("did not receive pty stdout frame")
	}
	_ = conn.Close()

	if _, _, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		t.Fatalf("expected reused token websocket dial to fail")
	}
}

func TestAgentSessionsAPIUnknownEnvironment(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http-unknown-env",
		Name:      "Slice Unknown Env",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := agentsession.NewService(st, "test-secret")
	api := NewAgentSessionsAPI(st, svc)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent-sessions", api.HandleCollection)
	server := httptest.NewServer(mux)
	defer server.Close()

	createRaw := []byte(`{"sliceId":"slice-http-unknown-env","environment":"missing-env"}`)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(createRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestAgentSessionsAPIEnvironmentFallback(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	seedEnvironment(t, ctx, st, "node20", "tmpl-v1", "us-west-2")
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          "slice-http-fallback",
		Name:        "Slice Fallback",
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		Environment: "node20",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http-no-default",
		Name:      "Slice No Default",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := agentsession.NewService(st, "test-secret")
	api := NewAgentSessionsAPI(st, svc)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent-sessions", api.HandleCollection)
	server := httptest.NewServer(mux)
	defer server.Close()

	fallbackRaw := []byte(`{"sliceId":"slice-http-fallback"}`)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(fallbackRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fallback create failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for fallback create, got %d", resp.StatusCode)
	}
	var created struct {
		Environment string `json:"environment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode fallback create response failed: %v", err)
	}
	_ = resp.Body.Close()
	if created.Environment != "node20" {
		t.Fatalf("expected fallback environment node20, got %q", created.Environment)
	}

	missingRaw := []byte(`{"sliceId":"slice-http-no-default"}`)
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(missingRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("missing-default create failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without request/slice environment, got %d", resp.StatusCode)
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
