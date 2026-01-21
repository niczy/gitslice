package workflow

import "testing"

func TestGlobalCommandsReportUnsupported(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"global status", []string{"global", "status"}},
		{"global log", []string{"global", "log"}},
		{"global show", []string{"global", "show"}},
		{"global stats", []string{"global", "stats"}},
		{"global merge", []string{"global", "merge"}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assertUnsupportedCommand(t, tt.args...)
		})
	}
}
