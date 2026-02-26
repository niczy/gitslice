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
	healthProvider, ok := provider.(RuntimeHealthProvider)
	if !ok {
		t.Fatalf("expected RuntimeHealthProvider implementation")
	}
	if err := healthProvider.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
	if !startCalled || !stopCalled || !healthCalled {
		t.Fatalf("expected start, stop, and health calls to be made")
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
