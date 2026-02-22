package agentsession

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const (
	envAgentValidateCLIBinary = "AGENT_VALIDATE_CLI_BINARY"
	envCodexCLIBinary         = "CODEX_CLI_BINARY"
)

func validateAgentBinaryForSession(session *models.AgentSession) error {
	if session == nil {
		return &RuntimeError{
			Code:    "AGENT_BINARY_EXEC_FAILED",
			Message: "missing session metadata",
		}
	}
	if strings.TrimSpace(session.AgentType) != "codex" {
		return nil
	}
	if strings.TrimSpace(os.Getenv(envAgentValidateCLIBinary)) == "" {
		return nil
	}

	binary := strings.TrimSpace(os.Getenv(envCodexCLIBinary))
	if binary == "" {
		binary = "codex"
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return &RuntimeError{
			Code:    "AGENT_BINARY_MISSING",
			Message: "codex binary is missing",
			Err:     err,
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		return &RuntimeError{
			Code:    "AGENT_BINARY_EXEC_FAILED",
			Message: "failed to stat codex binary",
			Err:     err,
		}
	}
	if info.Mode()&0o111 == 0 {
		return &RuntimeError{
			Code:    "AGENT_BINARY_EXEC_FAILED",
			Message: "codex binary is not executable",
		}
	}
	return nil
}

func (s *Service) HandleAgentInput(ctx context.Context, sessionID, text string) error {
	sessionID = strings.TrimSpace(sessionID)
	text = strings.TrimSpace(text)
	if sessionID == "" || text == "" {
		return storage.ErrInvalidInput
	}

	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.State != models.AgentSessionStateRunning && session.State != models.AgentSessionStateIdle {
		return storage.ErrAgentSessionConflict
	}
	if strings.TrimSpace(session.AgentType) != "codex" {
		return storage.ErrInvalidInput
	}

	toolID := makeNonceID("tool")
	outputDeltas := []string{
		"Codex: analyzing request",
		"Codex: planning workspace actions",
	}
	for _, delta := range outputDeltas {
		payload, _ := json.Marshal(map[string]string{"text": delta})
		if err := s.AppendEvent(ctx, &models.AgentSessionEvent{
			SessionID: sessionID,
			Stream:    "agent",
			Type:      "output_delta",
			Payload:   payload,
		}); err != nil {
			return err
		}
	}

	toolStart, _ := json.Marshal(map[string]string{"tool": "codex.exec", "id": toolID})
	if err := s.AppendEvent(ctx, &models.AgentSessionEvent{
		SessionID: sessionID,
		Stream:    "tool",
		Type:      "start",
		Payload:   toolStart,
	}); err != nil {
		return err
	}
	toolOut, _ := json.Marshal(map[string]string{"id": toolID, "text": "codex processed input"})
	if err := s.AppendEvent(ctx, &models.AgentSessionEvent{
		SessionID: sessionID,
		Stream:    "tool",
		Type:      "output",
		Payload:   toolOut,
	}); err != nil {
		return err
	}
	toolEnd, _ := json.Marshal(map[string]string{"id": toolID, "status": "ok"})
	if err := s.AppendEvent(ctx, &models.AgentSessionEvent{
		SessionID: sessionID,
		Stream:    "tool",
		Type:      "end",
		Payload:   toolEnd,
	}); err != nil {
		return err
	}

	finalPayload, _ := json.Marshal(map[string]string{
		"text": "Codex completed request: " + text,
	})
	if err := s.AppendEvent(ctx, &models.AgentSessionEvent{
		SessionID: sessionID,
		Stream:    "agent",
		Type:      "output_final",
		Payload:   finalPayload,
	}); err != nil {
		return err
	}
	_ = s.RecordActivity(ctx, sessionID)
	_ = s.AddAudit(ctx, sessionID, session.UserID, "agent_input", map[string]any{
		"agentType": session.AgentType,
	})
	return nil
}

func (s *Service) HandleAgentInterrupt(ctx context.Context, sessionID, reason string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return storage.ErrInvalidInput
	}
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.State != models.AgentSessionStateRunning && session.State != models.AgentSessionStateIdle {
		return storage.ErrAgentSessionConflict
	}

	payload, _ := json.Marshal(map[string]string{
		"code":    "USER_INTERRUPT",
		"message": strings.TrimSpace(reason),
	})
	if err := s.AppendEvent(ctx, &models.AgentSessionEvent{
		SessionID: sessionID,
		Stream:    "control",
		Type:      "error",
		Payload:   payload,
	}); err != nil {
		return err
	}
	_ = s.RecordActivity(ctx, sessionID)
	_ = s.AddAudit(ctx, sessionID, session.UserID, "agent_interrupt", map[string]any{
		"agentType": session.AgentType,
	})
	return nil
}
