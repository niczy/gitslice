package common

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// HealthStatus represents the health status of a service.
type HealthStatus struct {
	Status     string    `json:"status"`
	Service    string    `json:"service"`
	Timestamp  time.Time `json:"timestamp"`
	Uptime     string    `json:"uptime,omitempty"`
	Dependency string    `json:"dependency,omitempty"`
	Error      string    `json:"error,omitempty"`
}

var serviceStartTime = time.Now()

// HealthCheckHandler returns an HTTP handler for health checks.
func HealthCheckHandler(serviceName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		uptime := time.Since(serviceStartTime).Round(time.Second).String()
		health := HealthStatus{
			Status:    "healthy",
			Service:   serviceName,
			Timestamp: time.Now(),
			Uptime:    uptime,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(health)
	}
}

// DependencyHealthCheckHandler returns a handler for checking a named dependency.
func DependencyHealthCheckHandler(serviceName, dependency string, check func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		status := "healthy"
		statusCode := http.StatusOK
		errMessage := ""
		if check != nil {
			if err := check(ctx); err != nil {
				status = "unhealthy"
				statusCode = http.StatusServiceUnavailable
				errMessage = err.Error()
			}
		}

		health := HealthStatus{
			Status:     status,
			Service:    serviceName,
			Dependency: dependency,
			Error:      errMessage,
			Timestamp:  time.Now(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(health)
	}
}

// ReadyCheckHandler returns a handler for readiness checks.
// It accepts a function that determines if the service is ready.
func ReadyCheckHandler(serviceName string, isReady func(context.Context) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		ready := isReady(ctx)
		status := "ready"
		statusCode := http.StatusOK

		if !ready {
			status = "not_ready"
			statusCode = http.StatusServiceUnavailable
		}

		health := HealthStatus{
			Status:    status,
			Service:   serviceName,
			Timestamp: time.Now(),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(health)
	}
}
