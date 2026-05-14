package gscli

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
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

func TestCodexAppServerCommandArgsUseFullPermissions(t *testing.T) {
	got := codexAppServerCommandArgs()
	want := []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"app-server",
		"--listen", "stdio://",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexAppServerCommandArgs() = %#v, want %#v", got, want)
	}
}

func TestCodexAppServerReadLoopRoutesServerRequestsWithIDs(t *testing.T) {
	r := &codexAppServerRunner{
		pending:       make(map[string]chan codexRPCMessage),
		notifications: make(chan codexRPCMessage, 1),
		done:          make(chan struct{}),
	}

	r.readLoop(strings.NewReader(`{"id":"srv-1","method":"item/fileChange/requestApproval","params":{"turnId":"turn-1"}}` + "\n"))

	select {
	case msg := <-r.notifications:
		if msg.Method != "item/fileChange/requestApproval" || codexMessageID(msg.ID) != "srv-1" {
			t.Fatalf("unexpected notification: %#v", msg)
		}
	default:
		t.Fatalf("expected server request to be routed to notifications")
	}
}

func TestCodexAppServerRespondsToFileChangeApprovalRequest(t *testing.T) {
	var out bytes.Buffer
	r := &codexAppServerRunner{encoder: json.NewEncoder(&out)}

	err := r.respondToServerRequest(context.Background(), codexRPCMessage{
		ID:     json.RawMessage(`"srv-1"`),
		Method: "item/fileChange/requestApproval",
	}, map[string]any{"decision": "accept"})
	if err != nil {
		t.Fatalf("respondToServerRequest failed: %v", err)
	}

	var got struct {
		ID     string `json:"id"`
		Result struct {
			Decision string `json:"decision"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != "srv-1" || got.Result.Decision != "accept" {
		t.Fatalf("unexpected response: %#v", got)
	}
}
