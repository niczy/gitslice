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

func seedAgentRunner(t *testing.T, ctx context.Context, st storage.Storage, runnerID, userID, agentType string) {
	t.Helper()
	now := time.Now().UTC()
	err := st.UpsertAgentRunner(ctx, &models.AgentRunner{
		RunnerID:        runnerID,
		UserID:          userID,
		Provider:        "local",
		AgentType:       agentType,
		Status:          models.AgentRunnerStatusOnline,
		HostName:        "test-host",
		PID:             1234,
		WorkspaceRoot:   "/tmp/gitslice-agent",
		Capabilities:    []byte(`{"local_sessions_reported":true,"local_session_ids":[]}`),
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}
}

func TestAgentSessionsAPI(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	seedAgentRunner(t, ctx, st, "runner-http", "alice", "claude")
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
		"sliceId":   "slice-http",
		"runnerId":  "runner-http",
		"agentType": "claude",
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
		RunnerID      string  `json:"runnerId"`
		AgentType     string  `json:"agentType"`
		Provider      *string `json:"provider"`
		E2BTemplateID *string `json:"e2bTemplateId"`
		E2BSandboxID  *string `json:"e2bSandboxId"`
		Availability  string  `json:"availability"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	if createResp.SessionID == "" {
		t.Fatalf("missing sessionId in create response")
	}
	if createResp.RunnerID != "runner-http" {
		t.Fatalf("expected runner runner-http, got %q", createResp.RunnerID)
	}
	if createResp.AgentType != "claude" {
		t.Fatalf("expected agentType claude, got %q", createResp.AgentType)
	}
	if createResp.Provider != nil || createResp.E2BTemplateID != nil || createResp.E2BSandboxID != nil {
		t.Fatalf("create response should not expose legacy runtime fields: %#v", createResp)
	}
	if createResp.Availability != agentsession.SessionAvailabilityPendingLocal {
		t.Fatalf("expected pending local availability, got %q", createResp.Availability)
	}

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
		RunnerID     string  `json:"runnerId"`
		AgentType    string  `json:"agentType"`
		Provider     *string `json:"provider"`
		E2BSandboxID *string `json:"e2bSandboxId"`
		Availability string  `json:"availability"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response failed: %v", err)
	}
	_ = resp.Body.Close()
	if getResp.RunnerID != "runner-http" {
		t.Fatalf("expected get runner runner-http, got %q", getResp.RunnerID)
	}
	if getResp.AgentType != "claude" {
		t.Fatalf("expected get agentType claude, got %q", getResp.AgentType)
	}
	if getResp.Provider != nil || getResp.E2BSandboxID != nil {
		t.Fatalf("get response should not expose legacy runtime fields: %#v", getResp)
	}
	if getResp.Availability != agentsession.SessionAvailabilityPendingLocal {
		t.Fatalf("expected pending local availability, got %q", getResp.Availability)
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
	var stopResp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stopResp); err != nil {
		t.Fatalf("decode stop response failed: %v", err)
	}
	_ = resp.Body.Close()
	if stopResp.SessionID != createResp.SessionID {
		t.Fatalf("expected stop response session id %s, got %s", createResp.SessionID, stopResp.SessionID)
	}

	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions/"+createResp.SessionID+"/token", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token after stop failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected token after local stop 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestAgentSessionsAPIRejectsCrossUserAccess(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	seedAgentRunner(t, ctx, st, "runner-http-authz", "alice", "codex")
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

	createRaw := []byte(`{"sliceId":"slice-http-authz","runnerId":"runner-http-authz"}`)
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

func TestAgentSessionsAPIMarksCloudOnlyLocalSession(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	now := time.Now().UTC()
	if err := st.UpsertAgentRunner(ctx, &models.AgentRunner{
		RunnerID:        "runner-http-local-stopped",
		UserID:          "alice",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		Capabilities:    []byte(`{"local_sessions_reported":true,"local_session_ids":[]}`),
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}
	if err := st.CreateAgentSession(ctx, &models.AgentSession{
		SessionID:      "sess-http-local-stopped",
		SliceID:        "slice-http-local-stopped",
		RunnerID:       "runner-http-local-stopped",
		UserID:         "alice",
		State:          models.AgentSessionStateStopped,
		Provider:       agentsession.RuntimeProviderLocal,
		AgentType:      "codex",
		IdleTimeoutSec: 1800,
		TTLSec:         14400,
		RuntimeStatus:  "stopped",
		CreatedAt:      now,
		UpdatedAt:      now,
		StoppedAt:      &now,
	}); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	if err := st.AppendAgentSessionEvent(ctx, &models.AgentSessionEvent{
		SessionID: "sess-http-local-stopped",
		Seq:       1,
		Stream:    agentsession.EventStreamStatus,
		Type:      "local_runner_attached",
		Payload:   []byte(`{}`),
		TS:        now,
	}); err != nil {
		t.Fatalf("AppendAgentSessionEvent failed: %v", err)
	}

	api := NewAgentSessionsAPI(st, agentsession.NewService(st, "test-secret"))
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent-sessions/", api.HandleItem)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/agent-sessions/sess-http-local-stopped", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET session failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		State        string `json:"state"`
		Availability string `json:"availability"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if body.State != "" {
		t.Fatalf("expected lifecycle state to be hidden, got %q", body.State)
	}
	if body.Availability != agentsession.SessionAvailabilityCloudOnly {
		t.Fatalf("expected cloud-only availability, got %q", body.Availability)
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
	seedAgentRunner(t, ctx, st, "runner-http-ws", "alice", "codex")
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

	createRaw := []byte(`{"sliceId":"slice-http-ws","runnerId":"runner-http-ws"}`)
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
	if err := conn.WriteJSON(map[string]any{
		"stream": "agent",
		"type":   "input",
		"payload": map[string]string{
			"text": "Summarize latest changes",
		},
	}); err != nil {
		t.Fatalf("write agent input failed: %v", err)
	}

	gotPong := false
	gotStdout := false
	gotAgentInput := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (!gotPong || !gotStdout || !gotAgentInput) {
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
		if frame.Stream == "agent" && frame.Type == "input" {
			if text, ok := frame.Payload["text"].(string); ok && strings.Contains(text, "Summarize latest changes") {
				gotAgentInput = true
			}
		}
	}
	if !gotPong {
		t.Fatalf("did not receive pong frame")
	}
	if !gotStdout {
		t.Fatalf("did not receive pty stdout frame")
	}
	if !gotAgentInput {
		t.Fatalf("did not receive agent input frame")
	}
	_ = conn.Close()

	if _, _, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		t.Fatalf("expected reused token websocket dial to fail")
	}
}

func TestAgentSessionsAPIUnknownRunner(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http-unknown-runner",
		Name:      "Slice Unknown Runner",
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

	createRaw := []byte(`{"sliceId":"slice-http-unknown-runner","runnerId":"missing-runner"}`)
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

func TestAgentSessionsAPIDisallowedAgentType(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	seedAgentRunner(t, ctx, st, "runner-codex-only", "alice", "codex")
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http-disallowed-agent",
		Name:      "Slice Disallowed Agent",
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

	createRaw := []byte(`{"sliceId":"slice-http-disallowed-agent","runnerId":"runner-codex-only","agentType":"claude"}`)
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

func TestAgentSessionsAPIRequiresRunner(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	seedAgentRunner(t, ctx, st, "runner-required-test", "alice", "codex")
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-http-runner-required",
		Name:      "Slice Runner Required",
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

	missingRaw := []byte(`{"sliceId":"slice-http-runner-required"}`)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(missingRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("missing-runner create failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without runner id, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	validRaw := []byte(`{"sliceId":"slice-http-runner-required","runnerId":"runner-required-test"}`)
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/agent-sessions", bytes.NewReader(validRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("runner create failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 with runner id, got %d", resp.StatusCode)
	}
}
