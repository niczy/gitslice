package gscli

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCodexReasoningDeltaRequiresMatchingTurn(t *testing.T) {
	payload := json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","delta":"checking files"}`)
	text, turnID, itemID := codexReasoningDelta(payload, "turn-1")
	if text != "checking files" || turnID != "turn-1" || itemID != "item-1" {
		t.Fatalf("codexReasoningDelta() = (%q, %q, %q), want parsed delta", text, turnID, itemID)
	}

	text, turnID, itemID = codexReasoningDelta(payload, "turn-2")
	if text != "" || turnID != "" || itemID != "" {
		t.Fatalf("codexReasoningDelta() for mismatched turn = (%q, %q, %q), want empty values", text, turnID, itemID)
	}
}

func TestCodexAppServerCommandArgsUseWritableSandbox(t *testing.T) {
	got := codexAppServerCommandArgs()
	want := []string{
		"--sandbox", "workspace-write",
		"--ask-for-approval", "never",
		"app-server",
		"--listen", "stdio://",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexAppServerCommandArgs() = %#v, want %#v", got, want)
	}
}
