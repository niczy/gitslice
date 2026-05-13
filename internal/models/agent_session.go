package models

import (
	"encoding/json"
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
	Payload   json.RawMessage `json:"payload"`
}

type AgentSessionAudit struct {
	ID          int64           `json:"id"`
	SessionID   string          `json:"session_id"`
	ActorUserID string          `json:"actor_user_id,omitempty"`
	Action      string          `json:"action"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}
