package gscli

import "testing"

func TestExitCodeForCLIError(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		retryable bool
		want      int
	}{
		{name: "invalid argument", code: "INVALID_ARGUMENT", want: cliExitUsage},
		{name: "interactive required", code: "INTERACTIVE_REQUIRED", want: cliExitUsage},
		{name: "auth error", code: "AUTH_LOGIN_FAILED", want: cliExitAuth},
		{name: "not found", code: "REPO_BINDING_NOT_FOUND", want: cliExitNotFound},
		{name: "local state", code: "CHECKOUT_METADATA_MISSING", want: cliExitState},
		{name: "retryable backend", code: "FS_LIST_FAILED", retryable: true, want: cliExitRetryable},
		{name: "generic fallback", code: "FS_LIST_FAILED", want: cliExitGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeForCLIError(tt.code, tt.retryable); got != tt.want {
				t.Fatalf("exitCodeForCLIError(%q, %t) = %d, want %d", tt.code, tt.retryable, got, tt.want)
			}
		})
	}
}
