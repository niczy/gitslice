package agentservice

import (
	"testing"

	"github.com/niczy/gitslice/internal/models"
)

func TestResolveAgentTypeFromEnvironmentRejectsDisallowed(t *testing.T) {
	t.Parallel()

	env := &models.Environment{
		DefaultAgentType:  "codex",
		AllowedAgentTypes: []string{"codex"},
	}
	_, err := resolveAgentTypeFromEnvironment("claude", env)
	if err == nil {
		t.Fatalf("expected disallowed agent type error")
	}
}

func TestResolveAgentTypeFromEnvironmentUsesDefaultWhenAllowed(t *testing.T) {
	t.Parallel()

	env := &models.Environment{
		DefaultAgentType:  "claude",
		AllowedAgentTypes: []string{"codex", "claude"},
	}
	got, err := resolveAgentTypeFromEnvironment("", env)
	if err != nil {
		t.Fatalf("resolveAgentTypeFromEnvironment returned error: %v", err)
	}
	if got != "claude" {
		t.Fatalf("expected claude default, got %q", got)
	}
}
