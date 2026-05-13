package models

import (
	"encoding/json"
	"strings"
	"time"
)

type AgentSessionState string

const (
	AgentSessionStateCreating AgentSessionState = "creating"
	AgentSessionStateStarting AgentSessionState = "starting"
	AgentSessionStateRunning  AgentSessionState = "running"
	AgentSessionStateIdle     AgentSessionState = "idle"
	AgentSessionStateStopping AgentSessionState = "stopping"
	AgentSessionStateStopped  AgentSessionState = "stopped"
	AgentSessionStateFailed   AgentSessionState = "failed"
)

// IsActive returns true while a session is still controlled by its runner.
func (s AgentSessionState) IsActive() bool {
	switch s {
	case AgentSessionStateCreating,
		AgentSessionStateStarting,
		AgentSessionStateRunning,
		AgentSessionStateIdle,
		AgentSessionStateStopping:
		return true
	default:
		return false
	}
}

type AgentSession struct {
	SessionID        string            `json:"session_id"`
	SliceID          string            `json:"slice_id"`
	RunnerID         string            `json:"runner_id,omitempty"`
	EnvironmentName  string            `json:"environment_name,omitempty"`
	AgentType        string            `json:"agent_type,omitempty"`
	UserID           string            `json:"user_id"`
	State            AgentSessionState `json:"state"`
	Provider         string            `json:"provider"`
	E2BTemplateID    string            `json:"e2b_template_id"`
	E2BSandboxID     string            `json:"e2b_sandbox_id,omitempty"`
	E2BRegion        string            `json:"e2b_region,omitempty"`
	IdleTimeoutSec   int               `json:"idle_timeout_sec"`
	TTLSec           int               `json:"ttl_sec"`
	RuntimeProvider  string            `json:"runtime_provider,omitempty"`
	RuntimeSessionID string            `json:"runtime_session_id,omitempty"`
	RuntimeStatus    string            `json:"runtime_status,omitempty"`
	RuntimeErrorCode string            `json:"runtime_error_code,omitempty"`
	RuntimeEndpoint  string            `json:"runtime_endpoint,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	StartedAt        *time.Time        `json:"started_at,omitempty"`
	LastActivityAt   *time.Time        `json:"last_activity_at,omitempty"`
	StoppedAt        *time.Time        `json:"stopped_at,omitempty"`
	FailureCode      string            `json:"failure_code,omitempty"`
	FailureMessage   string            `json:"failure_message,omitempty"`
}

type AgentRunnerStatus string

const (
	AgentRunnerStatusOnline  AgentRunnerStatus = "online"
	AgentRunnerStatusOffline AgentRunnerStatus = "offline"
)

type AgentRunner struct {
	RunnerID        string            `json:"runner_id"`
	UserID          string            `json:"user_id"`
	Provider        string            `json:"provider"`
	AgentType       string            `json:"agent_type"`
	Status          AgentRunnerStatus `json:"status"`
	HostName        string            `json:"host_name,omitempty"`
	PID             int               `json:"pid,omitempty"`
	WorkspaceRoot   string            `json:"workspace_root,omitempty"`
	Version         string            `json:"version,omitempty"`
	Capabilities    json.RawMessage   `json:"capabilities,omitempty"`
	LastHeartbeatAt time.Time         `json:"last_heartbeat_at"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type AgentSessionEvent struct {
	SessionID string          `json:"session_id"`
	Seq       uint64          `json:"seq"`
	TS        time.Time       `json:"ts"`
	Stream    string          `json:"stream"`
	Type      string          `json:"type"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
}

const (
	AgentSessionEventKindUserInput     = "user_input"
	AgentSessionEventKindThinking      = "thinking"
	AgentSessionEventKindToolCall      = "tool_call"
	AgentSessionEventKindToolResult    = "tool_result"
	AgentSessionEventKindModelResponse = "model_response"
	AgentSessionEventKindStatus        = "status"
	AgentSessionEventKindControl       = "control"
	AgentSessionEventKindError         = "error"
	AgentSessionEventKindEvent         = "event"
)

func NormalizeAgentSessionEventKind(stream, eventType, current string) string {
	stream = strings.ToLower(strings.TrimSpace(stream))
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	switch {
	case stream == "control" && eventType == "error":
		return AgentSessionEventKindError
	case stream == "agent" && eventType == "input":
		return AgentSessionEventKindUserInput
	case stream == "agent" && (eventType == "thinking_delta" || eventType == "reasoning_delta" || eventType == "reasoning_summary_delta"):
		return AgentSessionEventKindThinking
	case stream == "agent" && (eventType == "output_delta" || eventType == "output_final"):
		return AgentSessionEventKindModelResponse
	case stream == "tool" && (eventType == "start" || eventType == "call" || eventType == "request"):
		return AgentSessionEventKindToolCall
	case stream == "tool" && (eventType == "output" || eventType == "result" || eventType == "end"):
		return AgentSessionEventKindToolResult
	case stream == "status":
		return AgentSessionEventKindStatus
	case stream == "control":
		return AgentSessionEventKindControl
	}
	current = strings.ToLower(strings.TrimSpace(current))
	if current != "" {
		return current
	}
	return AgentSessionEventKindEvent
}

type AgentSessionAudit struct {
	ID          int64           `json:"id"`
	SessionID   string          `json:"session_id"`
	ActorUserID string          `json:"actor_user_id,omitempty"`
	Action      string          `json:"action"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

type AgentSessionChangeset struct {
	SessionID       string    `json:"session_id"`
	ChangesetID     string    `json:"changeset_id"`
	SnapshotID      string    `json:"snapshot_id"`
	SnapshotVersion int32     `json:"snapshot_version"`
	SnapshotHash    string    `json:"snapshot_hash"`
	BaseCommitHash  string    `json:"base_commit_hash"`
	ExportedFromSeq uint64    `json:"exported_from_seq"`
	RunnerID        string    `json:"runner_id,omitempty"`
	Source          string    `json:"source,omitempty"`
	ExportedAt      time.Time `json:"exported_at"`
}
