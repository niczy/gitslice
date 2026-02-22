package agentsession

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

func TestServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-1",
		Name:      "Slice 1",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	session, token, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-1",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
		E2BRegion:     "us-west-2",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if session.State != models.AgentSessionStateCreating {
		t.Fatalf("expected creating state, got %s", session.State)
	}
	if token == nil || token.Token == "" {
		t.Fatalf("expected ws token")
	}

	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	events, nextSeq, err := svc.ListEventsForUser(ctx, "alice", session.SessionID, 0, 100)
	if err != nil {
		t.Fatalf("ListEventsForUser failed: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected >=3 lifecycle events, got %d", len(events))
	}
	if nextSeq <= 1 {
		t.Fatalf("expected nextSeq > 1, got %d", nextSeq)
	}

	if _, err := svc.MintTokenForUser(ctx, "alice", session.SessionID); err != nil {
		t.Fatalf("MintTokenForUser failed in running state: %v", err)
	}
	if _, err := svc.MintTokenForUser(ctx, "bob", session.SessionID); err != storage.ErrAgentSessionNotFound {
		t.Fatalf("expected ErrAgentSessionNotFound for non-owner, got %v", err)
	}

	stopResp, err := svc.StopSessionForUser(ctx, "alice", session.SessionID, "test")
	if err != nil {
		t.Fatalf("StopSessionForUser failed: %v", err)
	}
	if stopResp.State != models.AgentSessionStateStopping {
		t.Fatalf("expected stopping state, got %s", stopResp.State)
	}

	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateStopped, 2*time.Second)
	if _, err := svc.MintTokenForUser(ctx, "alice", session.SessionID); err != storage.ErrAgentSessionConflict {
		t.Fatalf("expected ErrAgentSessionConflict for stopped session, got %v", err)
	}
}

func TestServiceOneActiveSessionPerSlice(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-2",
		Name:      "Slice 2",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	if _, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-2",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
	}); err != nil {
		t.Fatalf("CreateSession first failed: %v", err)
	}
	if _, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-2",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
	}); err != storage.ErrAgentSessionConflict {
		t.Fatalf("expected ErrAgentSessionConflict, got %v", err)
	}
}

func TestValidateAndConsumeWSToken(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-token",
		Name:      "Slice token",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	created, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-token",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, created.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	token, err := svc.MintTokenForUser(ctx, "alice", created.SessionID)
	if err != nil {
		t.Fatalf("MintTokenForUser failed: %v", err)
	}
	userID, err := svc.ValidateAndConsumeWSToken(token.Token, created.SessionID)
	if err != nil {
		t.Fatalf("ValidateAndConsumeWSToken failed: %v", err)
	}
	if userID != "alice" {
		t.Fatalf("expected alice, got %s", userID)
	}
	if _, err := svc.ValidateAndConsumeWSToken(token.Token, created.SessionID); err != storage.ErrAgentSessionConflict {
		t.Fatalf("expected ErrAgentSessionConflict on nonce replay, got %v", err)
	}
	if _, err := svc.ValidateAndConsumeWSToken(token.Token, "another-session"); err == nil {
		t.Fatalf("expected session mismatch validation error")
	}
}

func TestServiceLifecycleIdleAndTTL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-lifecycle",
		Name:      "Slice lifecycle",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	svc.SetRuntimeProvider(newSimulatedRuntimeProvider(10*time.Millisecond, 10*time.Millisecond))
	svc.lifecycleTick = 20 * time.Millisecond
	svc.StartLifecycleLoop(ctx)

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:        "slice-lifecycle",
		Provider:       "e2b",
		E2BTemplateID:  "tmpl-v1",
		IdleTimeoutSec: 1,
		TTLSec:         2,
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateIdle, 3*time.Second)

	if err := svc.RecordActivity(ctx, session.SessionID); err != nil {
		t.Fatalf("RecordActivity failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateStopped, 4*time.Second)
}

func TestServiceRuntimeStartFailure(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-start-fail",
		Name:      "Slice Start Fail",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	svc.SetRuntimeProvider(&stubRuntimeProvider{
		startFn: func(_ context.Context, _ *models.AgentSession) (*RuntimeStartResult, error) {
			return nil, &RuntimeError{
				Code:    "AGENT_BINARY_MISSING",
				Message: "codex binary is missing",
				Err:     errors.New("binary not found"),
			}
		},
		stopFn: func(_ context.Context, _ *models.AgentSession, _ string) error { return nil },
	})

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-start-fail",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateFailed, 2*time.Second)

	got, err := svc.GetSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.FailureCode != "AGENT_BINARY_MISSING" {
		t.Fatalf("expected AGENT_BINARY_MISSING failure code, got %s", got.FailureCode)
	}
}

func TestServiceRuntimeStopFailure(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-stop-fail",
		Name:      "Slice Stop Fail",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	svc.SetRuntimeProvider(&stubRuntimeProvider{
		startFn: func(_ context.Context, _ *models.AgentSession) (*RuntimeStartResult, error) {
			return &RuntimeStartResult{
				Provider:  "e2b",
				SessionID: "runtime-stop-fail",
				Endpoint:  "runtime://stop-fail",
				Status:    "ready",
			}, nil
		},
		stopFn: func(_ context.Context, _ *models.AgentSession, _ string) error {
			return &RuntimeError{
				Code:    "STOP_BACKEND_UNAVAILABLE",
				Message: "runtime backend unavailable",
				Err:     errors.New("backend down"),
			}
		},
	})

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-stop-fail",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	if _, err := svc.StopSessionForUser(ctx, "alice", session.SessionID, "test"); err != nil {
		t.Fatalf("StopSessionForUser failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateFailed, 2*time.Second)

	got, err := svc.GetSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.FailureCode != "STOP_BACKEND_UNAVAILABLE" {
		t.Fatalf("expected STOP_BACKEND_UNAVAILABLE failure code, got %s", got.FailureCode)
	}
}

type stubRuntimeProvider struct {
	startFn func(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error)
	stopFn  func(ctx context.Context, session *models.AgentSession, reason string) error
}

func (p *stubRuntimeProvider) Start(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error) {
	if p.startFn == nil {
		return &RuntimeStartResult{
			Provider:  "e2b",
			SessionID: "stub-runtime",
			Endpoint:  "runtime://stub",
			Status:    "ready",
		}, nil
	}
	return p.startFn(ctx, session)
}

func (p *stubRuntimeProvider) Stop(ctx context.Context, session *models.AgentSession, reason string) error {
	if p.stopFn == nil {
		return nil
	}
	return p.stopFn(ctx, session, reason)
}

func waitForSessionState(t *testing.T, svc *Service, sessionID string, want models.AgentSessionState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		session, err := svc.GetSession(context.Background(), sessionID)
		if err == nil && session.State == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	session, _ := svc.GetSession(context.Background(), sessionID)
	if session != nil {
		t.Fatalf("timed out waiting for state %s, got %s", want, session.State)
	}
	t.Fatalf("timed out waiting for state %s", want)
}
