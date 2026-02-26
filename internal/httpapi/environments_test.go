package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/niczy/gitslice/internal/storage"
)

func TestEnvironmentsAPI_CRUD(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	api := NewEnvironmentsAPI(st)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/environments", api.HandleCollection)
	mux.HandleFunc("/v1/environments/", api.HandleItem)
	server := httptest.NewServer(mux)
	defer server.Close()

	createRaw := []byte(`{"name":"node20","displayName":"Node.js 20","provider":"e2b","providerId":"tmpl-node20","providerConfig":{"runtime_ws_path":"/ws"},"region":"us-west-2"}`)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/environments", bytes.NewReader(createRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create environment failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response failed: %v", err)
	}
	_ = resp.Body.Close()
	if created["name"] != "node20" {
		t.Fatalf("expected name node20, got %v", created["name"])
	}
	if created["provider"] != nil || created["providerId"] != nil || created["providerConfig"] != nil {
		t.Fatalf("provider fields must be hidden by default: %#v", created)
	}
	if created["createdAt"] == "" || created["updatedAt"] == "" {
		t.Fatalf("expected timestamps in response: %#v", created)
	}

	// Duplicate create should fail with conflict.
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/environments", bytes.NewReader(createRaw))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("duplicate create failed: %v", err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate create, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Default get should hide provider internals.
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/environments/node20", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get environment failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from get, got %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response failed: %v", err)
	}
	_ = resp.Body.Close()
	if got["provider"] != nil || got["providerId"] != nil || got["providerConfig"] != nil {
		t.Fatalf("provider fields must be hidden by default: %#v", got)
	}

	// Internal caller get includes provider internals.
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/environments/node20", nil)
	req.Header.Set("Authorization", "User alice")
	req.Header.Set("X-Internal-Caller", "1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("internal get failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from internal get, got %d", resp.StatusCode)
	}
	var gotInternal map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&gotInternal); err != nil {
		t.Fatalf("decode internal get response failed: %v", err)
	}
	_ = resp.Body.Close()
	if gotInternal["provider"] != "e2b" || gotInternal["providerId"] != "tmpl-node20" {
		t.Fatalf("internal response should include provider internals: %#v", gotInternal)
	}
	if providerConfig, ok := gotInternal["providerConfig"].(map[string]any); !ok || providerConfig["runtime_ws_path"] != "/ws" {
		t.Fatalf("internal response should include provider config: %#v", gotInternal)
	}

	// List with pagination.
	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/environments?limit=10&offset=0", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list environments failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from list, got %d", resp.StatusCode)
	}
	var listed struct {
		Environments []map[string]any `json:"environments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response failed: %v", err)
	}
	_ = resp.Body.Close()
	if len(listed.Environments) != 1 {
		t.Fatalf("expected exactly one environment, got %d", len(listed.Environments))
	}

	// Update.
	updateRaw := []byte(`{"displayName":"Node.js 20 LTS","provider":"cloudflare_containers","providerId":"cfc-profile","providerConfig":{"worker_base_url":"https://edge.example.internal"},"region":"us-east-1"}`)
	req, _ = http.NewRequest(http.MethodPut, server.URL+"/v1/environments/node20", bytes.NewReader(updateRaw))
	req.Header.Set("Authorization", "User alice")
	req.Header.Set("X-Internal-Caller", "1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update environment failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from update, got %d", resp.StatusCode)
	}
	var updated map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response failed: %v", err)
	}
	_ = resp.Body.Close()
	if updated["displayName"] != "Node.js 20 LTS" || updated["providerId"] != "cfc-profile" || updated["region"] != "us-east-1" || updated["provider"] != "cloudflare_containers" {
		t.Fatalf("update response mismatch: %#v", updated)
	}
	if providerConfig, ok := updated["providerConfig"].(map[string]any); !ok || providerConfig["worker_base_url"] != "https://edge.example.internal" {
		t.Fatalf("update response should include provider config: %#v", updated)
	}

	// Delete.
	req, _ = http.NewRequest(http.MethodDelete, server.URL+"/v1/environments/node20", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete environment failed: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from delete, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/environments/node20", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get deleted environment failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for deleted environment, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	_ = st.Ping(ctx)
}

func TestEnvironmentsAPI_AuthAndValidation(t *testing.T) {
	st := storage.NewInMemoryStorage()
	api := NewEnvironmentsAPI(st)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/environments", api.HandleCollection)
	mux.HandleFunc("/v1/environments/", api.HandleItem)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Unauthorized list
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/environments", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unauthorized list request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthorized list, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Invalid create payload
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/environments", bytes.NewReader([]byte(`{"name":"Bad Name","providerId":""}`)))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("invalid create request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid create, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Unsupported provider should fail validation.
	req, _ = http.NewRequest(http.MethodPost, server.URL+"/v1/environments", bytes.NewReader([]byte(`{"name":"invalid-provider","provider":"foo","providerId":"tmpl-x"}`)))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unsupported provider create failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsupported provider, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Missing environment on update/delete
	req, _ = http.NewRequest(http.MethodPut, server.URL+"/v1/environments/missing-env", bytes.NewReader([]byte(`{"displayName":"x","provider":"e2b","providerId":"tmpl-x"}`)))
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update missing request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on updating missing env, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodDelete, server.URL+"/v1/environments/missing-env", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete missing request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on deleting missing env, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
