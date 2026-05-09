package sliceservice

import (
	"os"
	"strings"
	"sync"
)

var (
	profileLoggingOnce    sync.Once
	profileLoggingEnabled bool
)

func shouldLogProfiles() bool {
	profileLoggingOnce.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("GITSLICE_PROFILE_LOGS"))) {
		case "1", "true", "yes", "on":
			profileLoggingEnabled = true
		}
	})
	return profileLoggingEnabled
}
