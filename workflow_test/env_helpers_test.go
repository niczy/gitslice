package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type workflowTestEnvironment struct {
	Username string
	HomeDir  string
}

var (
	workflowTestEnvs    sync.Map
	workflowEnvSequence uint64
)

func workflowEnvForTest(t *testing.T) *workflowTestEnvironment {
	t.Helper()

	key := t.Name()
	if env, ok := workflowTestEnvs.Load(key); ok {
		return env.(*workflowTestEnvironment)
	}

	homeDir := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir workflow home dir: %v", err)
	}

	sequence := atomic.AddUint64(&workflowEnvSequence, 1)
	base := sanitizeWorkflowTestName(t.Name())
	if len(base) > 24 {
		base = base[:24]
	}

	env := &workflowTestEnvironment{
		Username: fmt.Sprintf("wf-%s-%d", base, sequence),
		HomeDir:  homeDir,
	}
	workflowTestEnvs.Store(key, env)
	t.Cleanup(func() {
		workflowTestEnvs.Delete(key)
	})
	return env
}

func workflowUsername(t *testing.T) string {
	t.Helper()
	return workflowEnvForTest(t).Username
}

func workflowProcessEnv(t *testing.T, extra map[string]string) map[string]string {
	t.Helper()

	env := workflowEnvForTest(t)
	merged := map[string]string{
		"HOME": env.HomeDir,
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}

func withWorkflowUser(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	return withUsername(ctx, workflowUsername(t))
}

func sanitizeWorkflowTestName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	sanitized = strings.ReplaceAll(sanitized, "--", "-")
	if sanitized == "" {
		return "test"
	}
	return sanitized
}
