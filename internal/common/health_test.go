package common

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDependencyHealthCheckHandlerHealthy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/db", nil)
	rec := httptest.NewRecorder()

	DependencyHealthCheckHandler("core-server", "database", func(context.Context) error {
		return nil
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var status HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if status.Status != "healthy" || status.Dependency != "database" {
		t.Fatalf("unexpected health payload: %+v", status)
	}
}

func TestDependencyHealthCheckHandlerUnhealthy(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/db", nil)
	rec := httptest.NewRecorder()

	DependencyHealthCheckHandler("core-server", "database", func(context.Context) error {
		return errors.New("db unreachable")
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	var status HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if status.Status != "unhealthy" || status.Error == "" {
		t.Fatalf("unexpected health payload: %+v", status)
	}
}
