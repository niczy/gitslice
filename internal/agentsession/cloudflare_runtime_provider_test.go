package agentsession

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

func TestCloudflareRuntimeProviderStartStopAndHealth(t *testing.T) {
	t.Parallel()

	startCalled := false
	stopCalled := false
	healthCalled := false
	inputCalled := false
	interruptCalled := false
	streamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == cfcStartPath:
			startCalled = true
			if got := r.Header.Get("CF-Access-Client-Id"); got != "svc-id" {
				t.Fatalf("expected CF-Access-Client-Id header, got %q", got)
			}
			if got := r.Header.Get("CF-Access-Client-Secret"); got != "svc-secret" {
				t.Fatalf("expected CF-Access-Client-Secret header, got %q", got)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if got := payload["profileId"]; got != "cfc-profile-node20" {
				t.Fatalf("expected profileId cfc-profile-node20, got %v", got)
			}
			envVars, _ := payload["envVars"].(map[string]any)
			if got := envVars["OPENAI_API_KEY"]; got != "openai-test-key" {
				t.Fatalf("expected OPENAI_API_KEY to be injected, got %v", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"runtimeSessionId":"runtime_123","streamEndpoint":"wss://edge.example.internal/stream/runtime_123","status":"ready"}`))
		case r.Method == http.MethodDelete && r.URL.Path == cfcStartPath+"/runtime_123":
			stopCalled = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == cfcStartPath+"/runtime_123/input":
			inputCalled = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		case r.Method == http.MethodPost && r.URL.Path == cfcStartPath+"/runtime_123/interrupt":
			interruptCalled = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		case r.Method == http.MethodGet && r.URL.Path == cfcStartPath+"/runtime_123/stream":
			streamCalled = true
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("id: 1\nevent: output_delta\ndata: {\"seq\":1,\"stream\":\"agent\",\"type\":\"output_delta\",\"payload\":{\"text\":\"delta\"}}\n\n"))
			_, _ = w.Write([]byte("id: 2\nevent: output_final\ndata: {\"seq\":2,\"stream\":\"agent\",\"type\":\"output_final\",\"payload\":{\"text\":\"done\"}}\n\n"))
			_, _ = w.Write([]byte("event: snapshot_end\ndata: {\"latestSeq\":2}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == cfcHealthPath:
			healthCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewCloudflareRuntimeProvider(CloudflareRuntimeProviderConfig{
		ControlBaseURL:     server.URL,
		ControlAudience:    "gitslice-control",
		ServiceTokenID:     "svc-id",
		ServiceTokenSecret: "svc-secret",
		CodexAPIKey:        "openai-test-key",
		ClaudeAPIKey:       "anthropic-test-key",
		RequestTimeout:     2 * time.Second,
	})

	session := &models.AgentSession{
		SessionID:     "session_123",
		SliceID:       "slice_123",
		UserID:        "alice",
		AgentType:     "codex",
		E2BTemplateID: "cfc-profile-node20",
		E2BRegion:     "us-east-1",
		TTLSec:        900,
	}
	result, err := provider.Start(context.Background(), session)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if result.Provider != RuntimeProviderCloudflareContainers {
		t.Fatalf("unexpected provider %q", result.Provider)
	}
	if result.SessionID != "runtime_123" {
		t.Fatalf("unexpected session id %q", result.SessionID)
	}
	if want := "wss://edge.example.internal/stream/runtime_123"; result.Endpoint != want {
		t.Fatalf("unexpected endpoint %q, want %q", result.Endpoint, want)
	}
	session.RuntimeSessionID = result.SessionID

	if err := provider.Stop(context.Background(), session, ""); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	inputProvider, ok := provider.(RuntimeInputProvider)
	if !ok {
		t.Fatalf("expected RuntimeInputProvider implementation")
	}
	if err := inputProvider.SendInput(context.Background(), session, "hello"); err != nil {
		t.Fatalf("SendInput failed: %v", err)
	}
	interruptProvider, ok := provider.(RuntimeInterruptProvider)
	if !ok {
		t.Fatalf("expected RuntimeInterruptProvider implementation")
	}
	if err := interruptProvider.SendInterrupt(context.Background(), session, "user requested"); err != nil {
		t.Fatalf("SendInterrupt failed: %v", err)
	}
	streamProvider, ok := provider.(RuntimeStreamProvider)
	if !ok {
		t.Fatalf("expected RuntimeStreamProvider implementation")
	}
	events, nextSeq, err := streamProvider.StreamEvents(context.Background(), session, 0, 100)
	if err != nil {
		t.Fatalf("StreamEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 stream events, got %d", len(events))
	}
	if nextSeq != 2 {
		t.Fatalf("expected nextSeq=2, got %d", nextSeq)
	}
	if got := events[0].Stream; got != "agent" {
		t.Fatalf("expected stream agent, got %q", got)
	}
	if got := events[0].Type; got != "output_delta" {
		t.Fatalf("expected output_delta, got %q", got)
	}

	healthProvider, ok := provider.(RuntimeHealthProvider)
	if !ok {
		t.Fatalf("expected RuntimeHealthProvider implementation")
	}
	if err := healthProvider.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if !startCalled || !stopCalled || !healthCalled || !inputCalled || !interruptCalled || !streamCalled {
		t.Fatalf("expected start/stop/health/input/interrupt/stream calls to be made")
	}
}

func TestCloudflareRuntimeProviderStartRequiresAuth(t *testing.T) {
	t.Parallel()

	provider := NewCloudflareRuntimeProvider(CloudflareRuntimeProviderConfig{
		ControlBaseURL: "https://example.workers.dev",
		CodexAPIKey:    "openai-test-key",
	})
	_, err := provider.Start(context.Background(), &models.AgentSession{
		SessionID:     "session_123",
		E2BTemplateID: "cfc-profile",
		AgentType:     "codex",
	})
	if err == nil {
		t.Fatalf("expected missing credentials error")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if runtimeErr.Code != "CFC_AUTH_MISSING" {
		t.Fatalf("unexpected code %q", runtimeErr.Code)
	}
}

func TestCloudflareRuntimeProviderStartMapsUnauthorized(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer server.Close()

	provider := NewCloudflareRuntimeProvider(CloudflareRuntimeProviderConfig{
		ControlBaseURL:     server.URL,
		ServiceTokenID:     "svc-id",
		ServiceTokenSecret: "svc-secret",
		CodexAPIKey:        "openai-test-key",
	})
	_, err := provider.Start(context.Background(), &models.AgentSession{
		SessionID:     "session_123",
		E2BTemplateID: "cfc-profile",
		AgentType:     "codex",
	})
	if err == nil {
		t.Fatalf("expected unauthorized error")
	}
	if !strings.Contains(err.Error(), "status=401") {
		t.Fatalf("expected status details, got %v", err)
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if runtimeErr.Code != "CFC_START_UNAUTHORIZED" {
		t.Fatalf("unexpected code %q", runtimeErr.Code)
	}
}

func TestCloudflareRuntimeProviderHealthCheckRequiresAgentCredential(t *testing.T) {
	t.Parallel()

	provider := NewCloudflareRuntimeProvider(CloudflareRuntimeProviderConfig{
		ControlBaseURL:     "https://example.workers.dev",
		ServiceTokenID:     "svc-id",
		ServiceTokenSecret: "svc-secret",
	})
	healthProvider, ok := provider.(RuntimeHealthProvider)
	if !ok {
		t.Fatalf("expected RuntimeHealthProvider implementation")
	}
	err := healthProvider.HealthCheck(context.Background())
	if err == nil {
		t.Fatalf("expected missing runtime credential error")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if runtimeErr.Code != "AGENT_CREDENTIAL_MISSING" {
		t.Fatalf("unexpected code %q", runtimeErr.Code)
	}
}
