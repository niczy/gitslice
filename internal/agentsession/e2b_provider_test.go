package agentsession

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestE2BProviderCreateAndKill(t *testing.T) {
	t.Parallel()

	postCalled := false
	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			postCalled = true
			if got := r.Header.Get("X-API-KEY"); got != "e2b_test_key" {
				t.Fatalf("expected X-API-KEY header, got %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request failed: %v", err)
			}
			if body["templateID"] != "tmpl-v1" {
				t.Fatalf("expected templateID=tmpl-v1, got %v", body["templateID"])
			}
			envVars, _ := body["envVars"].(map[string]any)
			if envVars["FEATURE_FLAGS"] != "tool_replay_v1" {
				t.Fatalf("expected FEATURE_FLAGS env var")
			}
			if envVars["GS_SESSION_ID"] != "sess_123" {
				t.Fatalf("expected GS_SESSION_ID env var")
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

	provider := NewE2BProvider(E2BProviderConfig{
		APIURL:         server.URL,
		Domain:         "sandbox.e2b.dev",
		APIKey:         "e2b_test_key",
		RuntimeWSPort:  9000,
		RuntimeWSPath:  "/ws",
		RequestTimeout: 2 * time.Second,
	})

	result, err := provider.CreateSandbox(context.Background(), RuntimeProvisionRequest{
		SessionID:  "sess_123",
		SliceID:    "slice_123",
		UserID:     "alice",
		TemplateID: "tmpl-v1",
		Region:     "us-west-2",
		TTLSec:     900,
		EnvironmentVars: map[string]string{
			"FEATURE_FLAGS": "tool_replay_v1",
		},
	})
	if err != nil {
		t.Fatalf("CreateSandbox failed: %v", err)
	}
	if result.SandboxID != "sbx_123" {
		t.Fatalf("unexpected sandbox id %q", result.SandboxID)
	}
	if want := "wss://9000-sbx_123.sandbox.e2b.dev/ws"; result.RuntimeEndpoint != want {
		t.Fatalf("unexpected runtime endpoint %q, want %q", result.RuntimeEndpoint, want)
	}

	if err := provider.KillSandbox(context.Background(), result.SandboxID); err != nil {
		t.Fatalf("KillSandbox failed: %v", err)
	}
	if !postCalled || !deleteCalled {
		t.Fatalf("expected create and kill calls to be made")
	}
}

func TestE2BProviderCreateRequiresCredentials(t *testing.T) {
	t.Parallel()

	provider := NewE2BProvider(E2BProviderConfig{
		APIURL: "https://api.e2b.app",
	})
	_, err := provider.CreateSandbox(context.Background(), RuntimeProvisionRequest{
		SessionID:  "sess_123",
		TemplateID: "tmpl-v1",
	})
	if err == nil {
		t.Fatalf("expected missing credentials error")
	}
	if !strings.Contains(err.Error(), "missing E2B credentials") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestE2BProviderCreateErrorSurface(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized, bad token"}`))
	}))
	defer server.Close()

	provider := NewE2BProvider(E2BProviderConfig{
		APIURL:      server.URL,
		AccessToken: "token123",
	})
	_, err := provider.CreateSandbox(context.Background(), RuntimeProvisionRequest{
		SessionID:  "sess_123",
		TemplateID: "tmpl-v1",
	})
	if err == nil {
		t.Fatalf("expected create error")
	}
	if !strings.Contains(err.Error(), "status=401") || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("unexpected create error: %v", err)
	}
}
