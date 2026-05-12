package agentsession

import (
	"encoding/json"

	agentv1 "github.com/niczy/gitslice/proto/agent"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	EventStreamAgent   = "agent"
	EventStreamControl = "control"
	EventStreamPTY     = "pty"
	EventStreamStatus  = "status"
	EventStreamTool    = "tool"

	EventTypeInput       = "input"
	EventTypeInterrupt   = "interrupt"
	EventTypeOutputDelta = "output_delta"
	EventTypeOutputFinal = "output_final"
	EventTypeError       = "error"
	EventTypeState       = "state"
)

func marshalProtocolPayload(msg proto.Message) json.RawMessage {
	if msg == nil {
		return json.RawMessage(`{}`)
	}
	data, err := protojson.MarshalOptions{EmitUnpopulated: false}.Marshal(msg)
	if err != nil || len(data) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(data)
}

func inputPayload(text string) json.RawMessage {
	return marshalProtocolPayload(&agentv1.AgentInputPayload{Text: text})
}

func interruptPayload(reason string) json.RawMessage {
	return marshalProtocolPayload(&agentv1.AgentInterruptPayload{Reason: reason})
}

func outputPayload(text, channel string, exitCode int32) json.RawMessage {
	return marshalProtocolPayload(&agentv1.AgentOutputPayload{Text: text, Channel: channel, ExitCode: exitCode})
}

func errorPayload(code, message string) json.RawMessage {
	return marshalProtocolPayload(&agentv1.AgentErrorPayload{Code: code, Message: message})
}

func statePayload(state string) json.RawMessage {
	return marshalProtocolPayload(&agentv1.AgentStatePayload{State: state})
}
