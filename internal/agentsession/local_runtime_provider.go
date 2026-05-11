package agentsession

import (
	"context"

	"github.com/niczy/gitslice/internal/models"
)

type localRuntimeProvider struct{}

func NewLocalRuntimeProvider() RuntimeProvider {
	return &localRuntimeProvider{}
}

func (p *localRuntimeProvider) Start(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error) {
	return &RuntimeStartResult{
		Provider:  RuntimeProviderLocal,
		SessionID: session.SessionID,
		Endpoint:  "",
		Status:    "ready",
	}, nil
}

func (p *localRuntimeProvider) Stop(ctx context.Context, session *models.AgentSession, reason string) error {
	return nil
}
