package gscli

import (
	"reflect"
	"testing"

	agentv1 "github.com/niczy/gitslice/proto/agent"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPendingAgentInputsKeepsOnlyUnansweredTurns(t *testing.T) {
	events := []*agentv1.EventEnvelope{
		testAgentInputEvent(t, 1, "old request"),
		testAgentOutputFinalEvent(t, 2),
		testAgentInputEvent(t, 3, "pending request"),
	}

	got := pendingAgentInputs(events)
	want := []string{"pending request"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pendingAgentInputs() = %#v, want %#v", got, want)
	}
}

func TestPendingAgentInputsClearsOnErrorAndInactiveState(t *testing.T) {
	cases := []struct {
		name   string
		events []*agentv1.EventEnvelope
	}{
		{
			name: "control error",
			events: []*agentv1.EventEnvelope{
				testAgentInputEvent(t, 1, "request"),
				{Seq: 2, Stream: "control", Type: "error", Payload: []byte(`{"message":"failed"}`)},
			},
		},
		{
			name: "stopped state",
			events: []*agentv1.EventEnvelope{
				testAgentInputEvent(t, 1, "request"),
				testAgentStateEvent(t, 2, "stopped"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pendingAgentInputs(tc.events); len(got) != 0 {
				t.Fatalf("pendingAgentInputs() = %#v, want no pending inputs", got)
			}
		})
	}
}

func TestParseLocalRunnerRestartRequest(t *testing.T) {
	got := parseLocalRunnerRestartRequest([]byte(`{"upgrade":true,"reason":"web_ui"}`))
	if !got.Upgrade || got.Reason != "web_ui" {
		t.Fatalf("parseLocalRunnerRestartRequest() = %#v", got)
	}
}

func testAgentInputEvent(t *testing.T, seq uint64, text string) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentInputPayload{Text: text})
	if err != nil {
		t.Fatalf("marshal input payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "agent", Type: "input", Payload: payload}
}

func testAgentOutputFinalEvent(t *testing.T, seq uint64) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentOutputPayload{Text: "done", Channel: "stdout"})
	if err != nil {
		t.Fatalf("marshal output payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "agent", Type: "output_final", Payload: payload}
}

func testAgentStateEvent(t *testing.T, seq uint64, state string) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentStatePayload{State: state})
	if err != nil {
		t.Fatalf("marshal state payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "status", Type: "state", Payload: payload}
}
