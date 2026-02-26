package storage

import (
	"strings"
	"testing"
)

func TestValidateEnvironmentProviderConfig(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		config   map[string]string
		wantErr  string
	}{
		{
			name:     "e2b allows empty config",
			provider: "e2b",
			config:   nil,
		},
		{
			name:     "invalid runtime ws path",
			provider: "e2b",
			config: map[string]string{
				"runtime_ws_path": "ws",
			},
			wantErr: "runtime_ws_path",
		},
		{
			name:     "cloudflare requires worker base url",
			provider: "cloudflare_containers",
			config: map[string]string{
				"container_class": "sandbox",
				"instance_type":   "basic",
			},
			wantErr: "worker_base_url",
		},
		{
			name:     "cloudflare requires container class",
			provider: "cloudflare_containers",
			config: map[string]string{
				"worker_base_url": "https://edge.example.internal",
				"instance_type":   "basic",
			},
			wantErr: "container_class",
		},
		{
			name:     "cloudflare requires instance type",
			provider: "cloudflare_containers",
			config: map[string]string{
				"worker_base_url": "https://edge.example.internal",
				"container_class": "sandbox",
			},
			wantErr: "instance_type",
		},
		{
			name:     "cloudflare validates stream path",
			provider: "cloudflare_containers",
			config: map[string]string{
				"worker_base_url":     "https://edge.example.internal",
				"container_class":     "sandbox",
				"instance_type":       "basic",
				"runtime_stream_path": "events",
			},
			wantErr: "runtime_stream_path",
		},
		{
			name:     "cloudflare validates startup timeout",
			provider: "cloudflare_containers",
			config: map[string]string{
				"worker_base_url":     "https://edge.example.internal",
				"container_class":     "sandbox",
				"instance_type":       "basic",
				"startup_timeout_sec": "0",
			},
			wantErr: "startup_timeout_sec",
		},
		{
			name:     "cloudflare accepts valid config",
			provider: "cloudflare_containers",
			config: map[string]string{
				"worker_base_url":     "https://edge.example.internal",
				"container_class":     "sandbox",
				"instance_type":       "basic",
				"runtime_stream_path": "/internal/runtime/sessions/{id}/stream",
				"startup_timeout_sec": "30",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEnvironmentProviderConfig(tc.provider, tc.config)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error %q to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
