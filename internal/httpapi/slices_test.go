package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestSlicesAPI_EnvironmentCRUD(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:          "slice-env",
		Name:        "Slice Env",
		Owners:      []string{"alice"},
		CreatedBy:   "alice",
		Environment: "node20",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	api := NewSlicesAPI(st)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/slices/", api.HandleEnvironment)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/slices/slice-env/environment", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET environment failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response failed: %v", err)
	}
	_ = resp.Body.Close()
	if got["environment"] != "node20" {
		t.Fatalf("expected node20 environment, got %#v", got)
	}

	updateRaw := []byte(`{"environment":"python311-ml"}`)
	req, _ = http.NewRequest(http.MethodPut, server.URL+"/v1/slices/slice-env/environment", bytes.NewReader(updateRaw))
	req.Header.Set("Authorization", "User alice")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT environment failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from put, got %d", resp.StatusCode)
	}
	var updated map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatalf("decode put response failed: %v", err)
	}
	_ = resp.Body.Close()
	if updated["environment"] != "python311-ml" {
		t.Fatalf("expected updated environment, got %#v", updated)
	}

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/slices/slice-env/environment", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET environment after update failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after update, got %d", resp.StatusCode)
	}
	got = map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response after update failed: %v", err)
	}
	_ = resp.Body.Close()
	if got["environment"] != "python311-ml" {
		t.Fatalf("expected updated environment in GET, got %#v", got)
	}
}

func TestSlicesAPI_EnvironmentAuthAndMissing(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-auth",
		Name:      "Slice Auth",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	api := NewSlicesAPI(st)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/slices/", api.HandleEnvironment)
	server := httptest.NewServer(mux)
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/slices/slice-auth/environment", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unauthorized get failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/slices/slice-auth/environment", nil)
	req.Header.Set("Authorization", "User bob")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("forbidden get failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/slices/missing/environment", nil)
	req.Header.Set("Authorization", "User alice")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("missing get failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
