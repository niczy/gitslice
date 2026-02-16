package agentsession

import (
	"context"
	"fmt"
	"strings"
)

// RuntimeProvider provisions and tears down agent runtimes for sessions.
type RuntimeProvider interface {
	CreateSandbox(ctx context.Context, req RuntimeProvisionRequest) (*RuntimeProvisionResult, error)
	KillSandbox(ctx context.Context, sandboxID string) error
}

type RuntimeProvisionRequest struct {
	SessionID       string
	SliceID         string
	UserID          string
	TemplateID      string
	Region          string
	IdleTimeoutSec  int
	TTLSec          int
	EnvironmentVars map[string]string
}

type RuntimeProvisionResult struct {
	SandboxID       string
	RuntimeEndpoint string
}

type stubRuntimeProvider struct{}

func newStubRuntimeProvider() RuntimeProvider {
	return &stubRuntimeProvider{}
}

func (p *stubRuntimeProvider) CreateSandbox(_ context.Context, req RuntimeProvisionRequest) (*RuntimeProvisionResult, error) {
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	return &RuntimeProvisionResult{
		SandboxID:       "stub_" + sessionID,
		RuntimeEndpoint: fmt.Sprintf("runtime://%s", sessionID),
	}, nil
}

func (p *stubRuntimeProvider) KillSandbox(_ context.Context, _ string) error {
	return nil
}
