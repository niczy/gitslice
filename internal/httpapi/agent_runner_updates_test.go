package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/storage"
)

func TestAgentRunnerUpdatesWait(t *testing.T) {
	st := storage.NewInMemoryStorage()
	svc := agentsession.NewService(st, "test-secret")
	api := NewAgentSessionsAPI(st, svc)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent-runners", api.HandleRunnerUpdates)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/ws/agent-runners?timeoutMs=10", nil)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ws/agent-runners failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	var timeoutPayload runnerUpdateWaitResponse
	if err := json.NewDecoder(resp.Body).Decode(&timeoutPayload); err != nil {
		t.Fatalf("Decode timeout response failed: %v", err)
	}
	if timeoutPayload.Changed {
		t.Fatalf("expected timeout response to report unchanged")
	}

	resultCh := make(chan runnerUpdateWaitResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/ws/agent-runners?timeoutMs=1000", nil)
		if err != nil {
			errCh <- err
			return
		}
		req.Header.Set("Authorization", "User alice")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		var payload runnerUpdateWaitResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			errCh <- err
			return
		}
		resultCh <- payload
	}()

	time.Sleep(50 * time.Millisecond)
	svc.PublishRunnerUpdate("alice")

	select {
	case err := <-errCh:
		t.Fatalf("runner update request failed: %v", err)
	case payload := <-resultCh:
		if !payload.Changed {
			t.Fatalf("expected runner update response to report changed")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for runner update response")
	}
}
