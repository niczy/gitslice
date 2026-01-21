package workflow

import "testing"

func TestCacheCommandsReportUnsupported(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"cache stats", []string{"cache", "stats"}},
		{"cache clear", []string{"cache", "clear"}},
		{"cache clear slice", []string{"cache", "clear", "--slice", "example"}},
		{"cache prefetch", []string{"cache", "prefetch", "example"}},
		{"cache verify", []string{"cache", "verify"}},
		{"cache manifest hit", []string{"cache", "manifest", "hit"}},
		{"cache manifest miss", []string{"cache", "manifest", "miss"}},
		{"cache object hit", []string{"cache", "object", "hit"}},
		{"cache object miss", []string{"cache", "object", "miss"}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assertUnsupportedCommand(t, tt.args...)
		})
	}
}
