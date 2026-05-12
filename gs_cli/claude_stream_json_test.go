package gscli

import "testing"

func TestClaudeTextUpdateHandlesCumulativeAndChunkedText(t *testing.T) {
	current, delta := claudeTextUpdate("", "Hello")
	if current != "Hello" || delta != "Hello" {
		t.Fatalf("first update = (%q, %q), want (Hello, Hello)", current, delta)
	}

	current, delta = claudeTextUpdate(current, "Hello world")
	if current != "Hello world" || delta != " world" {
		t.Fatalf("cumulative update = (%q, %q), want (Hello world, ' world')", current, delta)
	}

	current, delta = claudeTextUpdate(current, "\nDone")
	if current != "Hello world\nDone" || delta != "\nDone" {
		t.Fatalf("chunk update = (%q, %q), want appended chunk", current, delta)
	}
}

func TestDecodeClaudeStreamMessage(t *testing.T) {
	msg, err := decodeClaudeStreamMessage([]byte(`{"type":"assistant","session_id":"sess-1","message":{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"pwd"}}]}}`))
	if err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if msg.Type != "assistant" || msg.SessionID != "sess-1" {
		t.Fatalf("decoded header = (%q, %q)", msg.Type, msg.SessionID)
	}
	if len(msg.Message.Content) != 2 {
		t.Fatalf("decoded %d content blocks, want 2", len(msg.Message.Content))
	}
	if msg.Message.Content[1].Type != "tool_use" || msg.Message.Content[1].Name != "Bash" {
		t.Fatalf("decoded tool block = %#v", msg.Message.Content[1])
	}
}

func TestClaudeToolResultText(t *testing.T) {
	got := claudeToolResultText([]byte(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`))
	if got != "one\ntwo" {
		t.Fatalf("tool result text = %q, want one\\ntwo", got)
	}
}
