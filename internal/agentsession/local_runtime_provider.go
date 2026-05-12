package agentsession

import (
	"context"
	"fmt"
	"strings"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type localRuntimeProvider struct {
	appendEvent func(context.Context, *models.AgentSessionEvent) error
}

func NewLocalRuntimeProvider(svc *Service) RuntimeProvider {
	if svc == nil {
		return &localRuntimeProvider{}
	}
	return &localRuntimeProvider{appendEvent: svc.AppendEvent}
}

func (p *localRuntimeProvider) Start(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error) {
	if session == nil {
		return nil, storage.ErrInvalidInput
	}
	if err := waitWithContext(ctx, 0); err != nil {
		return nil, &RuntimeError{Code: "LOCAL_START_CANCELLED", Message: "local runtime start cancelled", Err: err}
	}
	if p.appendEvent != nil {
		_ = p.appendEvent(ctx, &models.AgentSessionEvent{
			SessionID: session.SessionID,
			Stream:    EventStreamStatus,
			Type:      "local_runner_waiting",
			Payload:   statePayload("waiting_for_local_agent"),
		})
	}
	return &RuntimeStartResult{
		Provider:  RuntimeProviderLocal,
		SessionID: session.SessionID,
		Endpoint:  fmt.Sprintf("local://%s", session.SessionID),
		Status:    "waiting_for_local_agent",
	}, nil
}

func (p *localRuntimeProvider) Stop(ctx context.Context, session *models.AgentSession, reason string) error {
	if session == nil {
		return nil
	}
	if p.appendEvent == nil {
		return nil
	}
	return p.appendEvent(ctx, &models.AgentSessionEvent{
		SessionID: session.SessionID,
		Stream:    EventStreamAgent,
		Type:      EventTypeInterrupt,
		Payload:   interruptPayload(firstNonEmpty(strings.TrimSpace(reason), "session stopped")),
	})
}

func (p *localRuntimeProvider) SendInput(ctx context.Context, session *models.AgentSession, text string) error {
	if session == nil || strings.TrimSpace(text) == "" {
		return storage.ErrInvalidInput
	}
	if p.appendEvent == nil {
		return &RuntimeError{Code: "LOCAL_RUNTIME_UNAVAILABLE", Message: "local runtime event sink is not configured"}
	}
	return p.appendEvent(ctx, &models.AgentSessionEvent{
		SessionID: session.SessionID,
		Stream:    EventStreamAgent,
		Type:      EventTypeInput,
		Payload:   inputPayload(text),
	})
}

func (p *localRuntimeProvider) SendInterrupt(ctx context.Context, session *models.AgentSession, reason string) error {
	if session == nil {
		return storage.ErrInvalidInput
	}
	if p.appendEvent == nil {
		return &RuntimeError{Code: "LOCAL_RUNTIME_UNAVAILABLE", Message: "local runtime event sink is not configured"}
	}
	return p.appendEvent(ctx, &models.AgentSessionEvent{
		SessionID: session.SessionID,
		Stream:    EventStreamAgent,
		Type:      EventTypeInterrupt,
		Payload:   interruptPayload(reason),
	})
}

func (p *localRuntimeProvider) HealthCheck(ctx context.Context) error {
	_ = ctx
	return nil
}
