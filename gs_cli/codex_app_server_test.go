package gscli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
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

func TestCodexThreadStartParamsRequestFullAccess(t *testing.T) {
	cwd := t.TempDir()
	got, err := codexThreadStartParams(cwd)
	if err != nil {
		t.Fatalf("codexThreadStartParams failed: %v", err)
	}
	if got["approvalPolicy"] != "never" {
		t.Fatalf("approvalPolicy = %#v, want never", got["approvalPolicy"])
	}
	if got["sandbox"] != "danger-full-access" {
		t.Fatalf("sandbox = %#v, want danger-full-access", got["sandbox"])
	}
	if got["cwd"] != cwd {
		t.Fatalf("cwd = %#v, want %q", got["cwd"], cwd)
	}
}

func TestCodexTurnStartParamsRequestFullAccess(t *testing.T) {
	cwd := t.TempDir()
	got, err := codexTurnStartParams("thread-1", "run server", cwd)
	if err != nil {
		t.Fatalf("codexTurnStartParams failed: %v", err)
	}
	if got["threadId"] != "thread-1" {
		t.Fatalf("threadId = %#v, want thread-1", got["threadId"])
	}
	if got["approvalPolicy"] != "never" {
		t.Fatalf("approvalPolicy = %#v, want never", got["approvalPolicy"])
	}
	if got["cwd"] != cwd {
		t.Fatalf("cwd = %#v, want %q", got["cwd"], cwd)
	}
	sandbox, ok := got["sandboxPolicy"].(map[string]any)
	if !ok || sandbox["type"] != "dangerFullAccess" {
		t.Fatalf("sandboxPolicy = %#v, want dangerFullAccess policy", got["sandboxPolicy"])
	}
	input, ok := got["input"].([]map[string]any)
	if !ok || len(input) != 1 || input[0]["text"] != "run server" {
		t.Fatalf("input = %#v, want prompt payload", got["input"])
	}
}

func TestCodexPermissionApprovalResultGrantsSessionNetwork(t *testing.T) {
	cwd := t.TempDir()
	got := codexPermissionApprovalResult(cwd)
	if got["scope"] != "session" {
		t.Fatalf("scope = %#v, want session", got["scope"])
	}
	permissions, ok := got["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions = %#v, want map", got["permissions"])
	}
	network, ok := permissions["network"].(map[string]any)
	if !ok || network["enabled"] != true {
		t.Fatalf("network permissions = %#v, want enabled", permissions["network"])
	}
	fileSystem, ok := permissions["fileSystem"].(map[string]any)
	if !ok {
		t.Fatalf("fileSystem permissions = %#v, want map", permissions["fileSystem"])
	}
	wantCWD, err := filepath.Abs(cwd)
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	if !reflect.DeepEqual(fileSystem["read"], []string{wantCWD}) || !reflect.DeepEqual(fileSystem["write"], []string{wantCWD}) {
		t.Fatalf("fileSystem permissions = %#v, want read/write cwd", fileSystem)
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

func TestCodexAppServerRespondsToPermissionApprovalRequest(t *testing.T) {
	var out bytes.Buffer
	cwd := t.TempDir()
	r := &codexAppServerRunner{
		cfg:     localAgentRunConfig{CWD: cwd},
		encoder: json.NewEncoder(&out),
	}

	done, _, _, err := r.handleNotification(context.Background(), "turn-1", "", codexRPCMessage{
		ID:     json.RawMessage(`"srv-2"`),
		Method: "item/permissions/requestApproval",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1"}`),
	})
	if err != nil {
		t.Fatalf("handleNotification failed: %v", err)
	}
	if done {
		t.Fatalf("permission approval should not complete the turn")
	}

	var got struct {
		ID     string `json:"id"`
		Result struct {
			Scope       string `json:"scope"`
			Permissions struct {
				Network struct {
					Enabled bool `json:"enabled"`
				} `json:"network"`
			} `json:"permissions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != "srv-2" || got.Result.Scope != "session" || !got.Result.Permissions.Network.Enabled {
		t.Fatalf("unexpected permission response: %#v", got)
	}
}
