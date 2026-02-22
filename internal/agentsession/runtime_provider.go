package agentsession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

type RuntimeStartResult struct {
	Provider  string
	SessionID string
	Endpoint  string
	Status    string
}

type RuntimeProvider interface {
	Start(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error)
	Stop(ctx context.Context, session *models.AgentSession, reason string) error
}

type RuntimeHealthProvider interface {
	HealthCheck(ctx context.Context) error
}

type RuntimeError struct {
	Code    string
	Message string
	Err     error
}

func (e *RuntimeError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if msg == "" {
		msg = "runtime provider error"
	}
	code := strings.TrimSpace(e.Code)
	if code == "" {
		return msg
	}
	return code + ": " + msg
}

func (e *RuntimeError) Unwrap() error {
	return e.Err
}

func runtimeErrorCode(err error, fallback string) string {
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		if code := strings.TrimSpace(runtimeErr.Code); code != "" {
			return code
		}
	}
	return fallback
}

func RuntimeErrorCode(err error, fallback string) string {
	return runtimeErrorCode(err, fallback)
}

func runtimeErrorMessage(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		if message := strings.TrimSpace(runtimeErr.Message); message != "" {
			return message
		}
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return fallback
	}
	return message
}

func RuntimeErrorMessage(err error, fallback string) string {
	return runtimeErrorMessage(err, fallback)
}

type simulatedRuntimeProvider struct {
	startDelay time.Duration
	stopDelay  time.Duration
}

func newSimulatedRuntimeProvider(startDelay, stopDelay time.Duration) RuntimeProvider {
	return &simulatedRuntimeProvider{
		startDelay: startDelay,
		stopDelay:  stopDelay,
	}
}

func (p *simulatedRuntimeProvider) Start(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error) {
	if err := waitWithContext(ctx, p.startDelay); err != nil {
		code := "START_CANCELLED"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "START_TIMEOUT"
		}
		return nil, &RuntimeError{
			Code:    code,
			Message: "runtime start cancelled",
			Err:     err,
		}
	}

	provider := strings.TrimSpace(session.Provider)
	if provider == "" {
		provider = "e2b"
	}
	runtimeSessionID := strings.TrimSpace(session.E2BSandboxID)
	if runtimeSessionID == "" {
		runtimeSessionID = fmt.Sprintf("runtime-%s", session.SessionID)
	}
	return &RuntimeStartResult{
		Provider:  provider,
		SessionID: runtimeSessionID,
		Endpoint:  fmt.Sprintf("runtime://%s", session.SessionID),
		Status:    "ready",
	}, nil
}

func (p *simulatedRuntimeProvider) Stop(ctx context.Context, session *models.AgentSession, reason string) error {
	_ = session
	_ = reason
	if err := waitWithContext(ctx, p.stopDelay); err != nil {
		code := "STOP_CANCELLED"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "STOP_TIMEOUT"
		}
		return &RuntimeError{
			Code:    code,
			Message: "runtime stop cancelled",
			Err:     err,
		}
	}
	return nil
}

func (p *simulatedRuntimeProvider) HealthCheck(ctx context.Context) error {
	_ = ctx
	return nil
}

func waitWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
