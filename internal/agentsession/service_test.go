package agentsession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type mockRuntimeProvider struct {
	mu       sync.Mutex
	createFn func(ctx context.Context, req RuntimeProvisionRequest) (*RuntimeProvisionResult, error)
	killFn   func(ctx context.Context, sandboxID string) error
}

func (m *mockRuntimeProvider) CreateSandbox(ctx context.Context, req RuntimeProvisionRequest) (*RuntimeProvisionResult, error) {
	m.mu.Lock()
	fn := m.createFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	return &RuntimeProvisionResult{
		SandboxID:       "sbx_mock",
		RuntimeEndpoint: "runtime://mock",
	}, nil
}

func (m *mockRuntimeProvider) KillSandbox(ctx context.Context, sandboxID string) error {
	m.mu.Lock()
	fn := m.killFn
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, sandboxID)
	}
	return nil
}

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
		deadline := time.Now().Add(2 * time.Second)
		for len(events) < 3 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			events, nextSeq, err = svc.ListEventsForUser(ctx, "alice", session.SessionID, 0, 100)
			if err != nil {
				t.Fatalf("ListEventsForUser retry failed: %v", err)
			}
		}
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
	svc.bootstrapDelay = 10 * time.Millisecond
	svc.stopDelay = 10 * time.Millisecond
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

func TestServiceCreateFailureMarksSessionFailed(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-fail",
		Name:      "Slice fail",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	provider := &mockRuntimeProvider{
		createFn: func(ctx context.Context, req RuntimeProvisionRequest) (*RuntimeProvisionResult, error) {
			return nil, errors.New("provider unavailable")
		},
	}
	svc := NewServiceWithRuntimeProvider(st, "test-secret", provider)
	svc.bootstrapDelay = 10 * time.Millisecond

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-fail",
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
	if got.FailureCode != "RUNTIME_CREATE_FAILED" {
		t.Fatalf("unexpected failure code %q", got.FailureCode)
	}
	if got.FailureMessage == "" {
		t.Fatalf("expected failure message")
	}
}

func TestServiceStopTriggersSandboxCleanup(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-stop-cleanup",
		Name:      "Slice stop cleanup",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	killCalls := make(chan string, 2)
	provider := &mockRuntimeProvider{
		createFn: func(ctx context.Context, req RuntimeProvisionRequest) (*RuntimeProvisionResult, error) {
			return &RuntimeProvisionResult{
				SandboxID:       "sbx_cleanup_1",
				RuntimeEndpoint: "runtime://cleanup",
			}, nil
		},
		killFn: func(ctx context.Context, sandboxID string) error {
			killCalls <- sandboxID
			return nil
		},
	}

	svc := NewServiceWithRuntimeProvider(st, "test-secret", provider)
	svc.bootstrapDelay = 10 * time.Millisecond
	svc.stopDelay = 10 * time.Millisecond

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-stop-cleanup",
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
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateStopped, 2*time.Second)

	select {
	case sandboxID := <-killCalls:
		if sandboxID != "sbx_cleanup_1" {
			t.Fatalf("unexpected kill sandbox id %q", sandboxID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected sandbox cleanup call")
	}
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
