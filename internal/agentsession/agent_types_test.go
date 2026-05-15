package agentsession

import (
	"testing"

	"github.com/niczy/gitslice/internal/models"
)

func TestRunnerSupportedAgentTypesFromCapabilities(t *testing.T) {
	runner := &models.AgentRunner{
		AgentType:    "codex",
		Capabilities: []byte(`{"default_agent_type":"codex","supported_agent_types":["claude","codex","unknown"]}`),
	}
	got := RunnerSupportedAgentTypes(runner)
	want := []string{"codex", "claude"}
	if len(got) != len(want) {
		t.Fatalf("supported agent type count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("supported agent types = %#v, want %#v", got, want)
		}
	}
	if !RunnerSupportsAgentType(runner, "claude") {
		t.Fatalf("expected runner to support claude")
	}
	if RunnerSupportsAgentType(runner, "unknown") {
		t.Fatalf("expected runner to reject unknown agent type")
	}
	if got := RunnerDefaultAgentType(runner); got != "codex" {
		t.Fatalf("default agent type = %q, want codex", got)
	}
}

func TestRunnerSupportedAgentTypesFallbackToRunnerAgentType(t *testing.T) {
	runner := &models.AgentRunner{AgentType: "claude"}
	if got := RunnerSupportedAgentTypes(runner); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("expected claude-only fallback, got %#v", got)
	}
}
