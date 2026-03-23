package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/models"
)

const (
	testCFCServiceTokenID     = "integration-cfc-token-id"
	testCFCServiceTokenSecret = "integration-cfc-token-secret"
)

func TestAgentSessionCloudflareRuntimeBridgeIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	ctx = withWorkflowUser(t, ctx)

	fakeControl := newFakeCloudflareControlPlane()
	server := httptest.NewServer(fakeControl)
	defer server.Close()

	testAgentSvc.SetRuntimeProviderFor(agentsession.RuntimeProviderCloudflareContainers, agentsession.NewCloudflareRuntimeProvider(agentsession.CloudflareRuntimeProviderConfig{
		ControlBaseURL:     server.URL,
		ServiceTokenID:     testCFCServiceTokenID,
		ServiceTokenSecret: testCFCServiceTokenSecret,
		ControlAudience:    "integration",
		CodexAPIKey:        "integration-codex-key",
		ClaudeAPIKey:       "integration-claude-key",
		RequestTimeout:     2 * time.Second,
	}))

	envName := fmt.Sprintf("integration-cfc-%d", time.Now().UnixNano())
	err := testStorage.CreateEnvironment(ctx, &models.Environment{
		Name:        envName,
		DisplayName: "Integration Cloudflare",
		Provider:    agentsession.RuntimeProviderCloudflareContainers,
		ProviderID:  "cfc-profile-integration",
		Region:      "us-east-1",
		ProviderConfig: map[string]string{
			"worker_base_url": server.URL,
			"container_class": "sandbox",
			"instance_type":   "basic",
		},
		CreatedBy: workflowUsername(t),
	})
	if err != nil {
		t.Fatalf("CreateEnvironment failed: %v", err)
	}

	sliceID := fmt.Sprintf("agent-cfc-%d", time.Now().UnixNano())
	if err := testStorage.CreateSlice(ctx, &models.Slice{
		ID:        sliceID,
		Name:      "Agent Cloudflare",
		Files:     []string{},
		Owners:    []string{workflowUsername(t)},
		CreatedBy: workflowUsername(t),
	}); err != nil {
		t.Fatalf("failed to create slice: %v", err)
	}

	sessionID := createAgentSessionWithEnvironmentViaHTTP(t, sliceID, envName, "codex")
	waitForAgentSessionState(t, sessionID, "running", 5*time.Second)

	token := mintAgentTokenViaHTTP(t, sessionID)
	wsURL := buildAgentWSURL(sessionID, token, 0)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect websocket: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	if err := conn.WriteJSON(map[string]any{
		"stream": "agent",
		"type":   "input",
		"payload": map[string]string{
			"text": "Run cloudflare integration check",
		},
	}); err != nil {
		t.Fatalf("write agent/input failed: %v", err)
	}

	gotRuntimeFinal := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !gotRuntimeFinal {
		var frame struct {
			Stream  string                 `json:"stream"`
			Type    string                 `json:"type"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("read websocket frame failed: %v", err)
		}
		if frame.Stream == "agent" && frame.Type == "output_final" {
			text, _ := frame.Payload["text"].(string)
			if strings.Contains(text, "runtime completed request: Run cloudflare integration check") {
				gotRuntimeFinal = true
			}
		}
	}
	if !gotRuntimeFinal {
		t.Fatalf("expected runtime output_final frame from cloudflare bridge")
	}

	stopAgentSessionViaHTTP(t, sessionID, "integration_done")
	waitForAgentSessionState(t, sessionID, "stopped", 5*time.Second)
}

func createAgentSessionWithEnvironmentViaHTTP(t *testing.T, sliceID, environment, agentType string) string {
	t.Helper()
	body := map[string]any{
		"sliceId":     sliceID,
		"environment": environment,
		"agentType":   agentType,
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, gatewayServiceURL+"/v1/agent-sessions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "User "+workflowUsername(t))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected create status 201/200, got %d body=%s", resp.StatusCode, string(data))
	}
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	if out.SessionID == "" {
		t.Fatalf("missing sessionId")
	}
	return out.SessionID
}

type fakeCloudflareControlPlane struct {
	mu       sync.Mutex
	sessions map[string]*fakeCloudflareRuntimeSession
}

type fakeCloudflareRuntimeSession struct {
	runtimeSessionID string
	seq              uint64
	events           []fakeCloudflareEvent
}

type fakeCloudflareEvent struct {
	Seq     uint64
	TS      string
	Stream  string
	Type    string
	Payload map[string]any
}

func newFakeCloudflareControlPlane() *fakeCloudflareControlPlane {
	return &fakeCloudflareControlPlane{
		sessions: make(map[string]*fakeCloudflareRuntimeSession),
	}
}

func (f *fakeCloudflareControlPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("CF-Access-Client-Id") != testCFCServiceTokenID || r.Header.Get("CF-Access-Client-Secret") != testCFCServiceTokenSecret {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == "/internal/runtime/sessions" {
		f.handleStart(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/internal/runtime/health" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}

	const basePath = "/internal/runtime/sessions/"
	if !strings.HasPrefix(r.URL.Path, basePath) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, basePath)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	runtimeSessionID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodDelete && action == "":
		f.handleStop(w, runtimeSessionID)
	case r.Method == http.MethodPost && action == "input":
		f.handleInput(w, r, runtimeSessionID)
	case r.Method == http.MethodPost && action == "interrupt":
		f.handleInterrupt(w, r, runtimeSessionID)
	case r.Method == http.MethodGet && action == "stream":
		f.handleStream(w, r, runtimeSessionID)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCloudflareControlPlane) handleStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.SessionID) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"sessionId is required"}`))
		return
	}

	runtimeSessionID := "runtime_" + strings.TrimSpace(req.SessionID)
	f.mu.Lock()
	session := f.sessions[runtimeSessionID]
	if session == nil {
		session = &fakeCloudflareRuntimeSession{runtimeSessionID: runtimeSessionID}
		f.sessions[runtimeSessionID] = session
	}
	f.appendEventLocked(session, "status", "state", map[string]any{"state": "running"})
	f.mu.Unlock()

	resp := map[string]any{
		"runtimeSessionId": runtimeSessionID,
		"status":           "ready",
		"provider":         agentsession.RuntimeProviderCloudflareContainers,
		"streamEndpoint":   "/internal/runtime/sessions/" + runtimeSessionID + "/stream",
	}
	writeJSONResponse(w, http.StatusCreated, resp)
}

func (f *fakeCloudflareControlPlane) handleStop(w http.ResponseWriter, runtimeSessionID string) {
	f.mu.Lock()
	session := f.sessions[runtimeSessionID]
	if session == nil {
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"session not found"}`))
		return
	}
	f.appendEventLocked(session, "status", "state", map[string]any{"state": "stopped"})
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeCloudflareControlPlane) handleInput(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	var req struct {
		Text string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	f.mu.Lock()
	session := f.sessions[runtimeSessionID]
	if session == nil {
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"session not found"}`))
		return
	}
	f.appendEventLocked(session, "agent", "output_delta", map[string]any{"text": "runtime shim received input"})
	f.appendEventLocked(session, "agent", "output_final", map[string]any{"text": "runtime completed request: " + strings.TrimSpace(req.Text)})
	f.mu.Unlock()

	writeJSONResponse(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (f *fakeCloudflareControlPlane) handleInterrupt(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	f.mu.Lock()
	session := f.sessions[runtimeSessionID]
	if session == nil {
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"session not found"}`))
		return
	}
	f.appendEventLocked(session, "control", "error", map[string]any{
		"code":    "USER_INTERRUPT",
		"message": strings.TrimSpace(req.Reason),
	})
	f.mu.Unlock()

	writeJSONResponse(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (f *fakeCloudflareControlPlane) handleStream(w http.ResponseWriter, r *http.Request, runtimeSessionID string) {
	sinceSeq := uint64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("sinceSeq")); raw != "" {
		if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
			sinceSeq = parsed
		}
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	f.mu.Lock()
	session := f.sessions[runtimeSessionID]
	if session == nil {
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"session not found"}`))
		return
	}
	events := make([]fakeCloudflareEvent, 0, len(session.events))
	for _, event := range session.events {
		if event.Seq <= sinceSeq {
			continue
		}
		events = append(events, event)
		if len(events) >= limit {
			break
		}
	}
	latestSeq := session.seq
	f.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, event := range events {
		data, _ := json.Marshal(map[string]any{
			"seq":     event.Seq,
			"ts":      event.TS,
			"stream":  event.Stream,
			"type":    event.Type,
			"payload": event.Payload,
		})
		_, _ = fmt.Fprintf(w, "id: %d\n", event.Seq)
		_, _ = fmt.Fprint(w, "event: runtime\n")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", string(data))
	}
	marker, _ := json.Marshal(map[string]any{"latestSeq": latestSeq})
	_, _ = fmt.Fprint(w, "event: snapshot_end\n")
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(marker))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (f *fakeCloudflareControlPlane) appendEventLocked(session *fakeCloudflareRuntimeSession, stream, eventType string, payload map[string]any) {
	session.seq++
	session.events = append(session.events, fakeCloudflareEvent{
		Seq:     session.seq,
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		Stream:  stream,
		Type:    eventType,
		Payload: payload,
	})
}

func writeJSONResponse(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
