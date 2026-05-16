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
	"time"

	"github.com/niczy/gitslice/internal/homeslice"
)

type workflowTestEnvironment struct {
	Username string
	HomeDir  string
}

const (
	workflowRootAdminUsername = "workflow-root-admin"
	workflowRootAdminEmail    = "workflow-root-admin@example.test"
)

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

func workflowRootAdminUser(t *testing.T) string {
	t.Helper()
	t.Setenv("ADMIN_USER_EMAILS", workflowRootAdminEmail)
	if testStorage == nil {
		t.Fatal("expected test storage to be initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	user, err := testStorage.EnsureUser(ctx, workflowRootAdminUsername)
	if err != nil {
		t.Fatalf("ensure workflow root admin user: %v", err)
	}
	if strings.TrimSpace(user.PrimaryEmail) != workflowRootAdminEmail {
		user.PrimaryEmail = workflowRootAdminEmail
		user.Name = "Workflow Root Admin"
		user.AuthSource = "local"
		if err := testStorage.UpdateUser(ctx, user); err != nil {
			t.Fatalf("update workflow root admin user: %v", err)
		}
	}
	if _, err := homeslice.EnsureUserHomeSlice(ctx, testStorage, workflowRootAdminUsername); err != nil {
		t.Fatalf("ensure workflow root admin home slice: %v", err)
	}
	return workflowRootAdminUsername
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
