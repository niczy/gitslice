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
			name:     "local allows empty config",
			provider: "local",
			config:   nil,
		},
		{
			name:     "invalid runtime ws path",
			provider: "local",
			config: map[string]string{
				"runtime_ws_path": "ws",
			},
			wantErr: "runtime_ws_path",
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
