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

func TestE2BRuntimeProviderStartAndStop(t *testing.T) {
	t.Parallel()

	postCalled := false
	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			postCalled = true
			if got := r.Header.Get("X-API-KEY"); got != "test_api_key" {
				t.Fatalf("expected X-API-KEY header, got %q", got)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if got := payload["templateID"]; got != "tmpl-node20" {
				t.Fatalf("expected templateID tmpl-node20, got %v", got)
			}
			envVars, _ := payload["envVars"].(map[string]any)
			if got := envVars["GS_SESSION_ID"]; got != "session_123" {
				t.Fatalf("expected GS_SESSION_ID=session_123, got %v", got)
			}
			if got := envVars["OPENAI_API_KEY"]; got != "openai-test-key" {
				t.Fatalf("expected OPENAI_API_KEY to be injected, got %v", got)
			}
			if got := envVars["GS_EGRESS_DEFAULT_DENY"]; got != "1" {
				t.Fatalf("expected GS_EGRESS_DEFAULT_DENY=1, got %v", got)
			}
			if got := envVars["GS_EGRESS_ALLOWLIST"]; got != "api.openai.com,github.com" {
				t.Fatalf("unexpected GS_EGRESS_ALLOWLIST %v", got)
			}
			if got := payload["region"]; got != "us-west-2" {
				t.Fatalf("expected region us-west-2, got %v", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"sandboxID":"sbx_123","domain":"sandbox.e2b.dev"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/sandboxes/sbx_123":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	provider := NewE2BRuntimeProvider(E2BRuntimeProviderConfig{
		APIURL:              server.URL,
		Domain:              "sandbox.e2b.dev",
		APIKey:              "test_api_key",
		CodexAPIKey:         "openai-test-key",
		EgressAllowlist:     []string{"api.openai.com", "github.com"},
		EgressDenyByDefault: true,
		RuntimeWSPort:       9000,
		RuntimeWSPath:       "/ws",
		RequestTimeout:      2 * time.Second,
	})

	session := &models.AgentSession{
		SessionID:     "session_123",
		SliceID:       "slice_123",
		UserID:        "alice",
		AgentType:     "codex",
		E2BTemplateID: "tmpl-node20",
		E2BRegion:     "us-west-2",
		TTLSec:        900,
	}
	result, err := provider.Start(context.Background(), session)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if result.Provider != "e2b" {
		t.Fatalf("unexpected provider %q", result.Provider)
	}
	if result.SessionID != "sbx_123" {
		t.Fatalf("unexpected session id %q", result.SessionID)
	}
	if want := "wss://9000-sbx_123.sandbox.e2b.dev/ws"; result.Endpoint != want {
		t.Fatalf("unexpected endpoint %q, want %q", result.Endpoint, want)
	}
	session.RuntimeSessionID = result.SessionID

	if err := provider.Stop(context.Background(), session, ""); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if !postCalled || !deleteCalled {
		t.Fatalf("expected create and delete calls to be made")
	}
}

func TestE2BRuntimeProviderStartRequiresCredentials(t *testing.T) {
	t.Parallel()

	provider := NewE2BRuntimeProvider(E2BRuntimeProviderConfig{
		APIURL: "https://api.e2b.app",
	})
	_, err := provider.Start(context.Background(), &models.AgentSession{
		SessionID:     "session_123",
		E2BTemplateID: "tmpl-node20",
	})
	if err == nil {
		t.Fatalf("expected missing credentials error")
	}
	if !strings.Contains(err.Error(), "missing E2B credentials") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestE2BRuntimeProviderStartMapsUnauthorized(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer server.Close()

	provider := NewE2BRuntimeProvider(E2BRuntimeProviderConfig{
		APIURL:      server.URL,
		AccessToken: "token123",
	})
	_, err := provider.Start(context.Background(), &models.AgentSession{
		SessionID:     "session_123",
		E2BTemplateID: "tmpl-node20",
	})
	if err == nil {
		t.Fatalf("expected unauthorized error")
	}
	var runtimeErr *RuntimeError
	if !strings.Contains(err.Error(), "status=401") {
		t.Fatalf("expected status details, got %v", err)
	}
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if runtimeErr.Code != "E2B_START_UNAUTHORIZED" {
		t.Fatalf("unexpected code %q", runtimeErr.Code)
	}
}

func TestE2BRuntimeProviderStartRequiresAgentCredential(t *testing.T) {
	t.Parallel()

	provider := NewE2BRuntimeProvider(E2BRuntimeProviderConfig{
		APIURL: "https://api.e2b.app",
		APIKey: "test_api_key",
		Domain: "e2b.app",
	})
	_, err := provider.Start(context.Background(), &models.AgentSession{
		SessionID:     "session_123",
		E2BTemplateID: "tmpl-node20",
		AgentType:     "codex",
	})
	if err == nil {
		t.Fatalf("expected missing agent credential error")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if runtimeErr.Code != "AGENT_CREDENTIAL_MISSING" {
		t.Fatalf("unexpected code %q", runtimeErr.Code)
	}
}

func TestE2BRuntimeProviderStartRequiresEgressAllowlistWhenDenyByDefault(t *testing.T) {
	t.Parallel()

	provider := NewE2BRuntimeProvider(E2BRuntimeProviderConfig{
		APIURL:              "https://api.e2b.app",
		APIKey:              "test_api_key",
		CodexAPIKey:         "openai-test-key",
		EgressDenyByDefault: true,
	})
	_, err := provider.Start(context.Background(), &models.AgentSession{
		SessionID:     "session_123",
		E2BTemplateID: "tmpl-node20",
		AgentType:     "codex",
	})
	if err == nil {
		t.Fatalf("expected egress policy error")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if runtimeErr.Code != "AGENT_EGRESS_POLICY_INVALID" {
		t.Fatalf("unexpected code %q", runtimeErr.Code)
	}
}

func TestE2BRuntimeProviderRedactsCredentialsInErrors(t *testing.T) {
	t.Parallel()

	leakedE2BKey := "test_e2b_secret"
	leakedCodexKey := "openai_super_secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad key test_e2b_secret and openai_super_secret"}`))
	}))
	defer server.Close()

	provider := NewE2BRuntimeProvider(E2BRuntimeProviderConfig{
		APIURL:      server.URL,
		APIKey:      leakedE2BKey,
		CodexAPIKey: leakedCodexKey,
	})
	_, err := provider.Start(context.Background(), &models.AgentSession{
		SessionID:     "session_123",
		E2BTemplateID: "tmpl-node20",
		AgentType:     "codex",
	})
	if err == nil {
		t.Fatalf("expected unauthorized error")
	}
	message := err.Error()
	if strings.Contains(message, leakedE2BKey) || strings.Contains(message, leakedCodexKey) {
		t.Fatalf("expected credentials to be redacted, got %q", message)
	}
	if !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("expected redaction marker in error, got %q", message)
	}
}

func TestE2BRuntimeProviderHealthCheckPolicyValidation(t *testing.T) {
	t.Parallel()

	provider := NewE2BRuntimeProvider(E2BRuntimeProviderConfig{
		APIURL:              "https://api.e2b.app",
		APIKey:              "test_api_key",
		CodexAPIKey:         "openai-test-key",
		EgressDenyByDefault: true,
	})
	healthProvider, ok := provider.(RuntimeHealthProvider)
	if !ok {
		t.Fatalf("expected RuntimeHealthProvider implementation")
	}
	err := healthProvider.HealthCheck(context.Background())
	if err == nil {
		t.Fatalf("expected health check error for missing egress allowlist")
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if runtimeErr.Code != "AGENT_EGRESS_POLICY_INVALID" {
		t.Fatalf("unexpected code %q", runtimeErr.Code)
	}
}
