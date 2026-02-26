package agentsession

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
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

func TestServiceRuntimeStartRetriesTransientFailures(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-start-retry",
		Name:      "Slice Start Retry",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	attempts := 0
	svc := NewService(st, "test-secret")
	svc.runtimeStartMaxRetries = 2
	svc.runtimeStartRetryBackoff = 5 * time.Millisecond
	svc.SetRuntimeProvider(&stubRuntimeProvider{
		startFn: func(_ context.Context, _ *models.AgentSession) (*RuntimeStartResult, error) {
			attempts++
			if attempts == 1 {
				return nil, &RuntimeError{
					Code:    "E2B_START_UNAVAILABLE",
					Message: "temporary backend outage",
				}
			}
			return &RuntimeStartResult{
				Provider:  "e2b",
				SessionID: "runtime-retry-1",
				Endpoint:  "runtime://retry",
				Status:    "ready",
			}, nil
		},
		stopFn: func(_ context.Context, _ *models.AgentSession, _ string) error { return nil },
	})

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-start-retry",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)
	if attempts != 2 {
		t.Fatalf("expected 2 runtime start attempts, got %d", attempts)
	}
}

func TestServiceRuntimeStartDoesNotRetryNonTransientFailures(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-start-no-retry",
		Name:      "Slice Start No Retry",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	attempts := 0
	svc := NewService(st, "test-secret")
	svc.runtimeStartMaxRetries = 3
	svc.runtimeStartRetryBackoff = 5 * time.Millisecond
	svc.SetRuntimeProvider(&stubRuntimeProvider{
		startFn: func(_ context.Context, _ *models.AgentSession) (*RuntimeStartResult, error) {
			attempts++
			return nil, &RuntimeError{
				Code:    "AGENT_CREDENTIAL_MISSING",
				Message: "missing credential",
			}
		},
		stopFn: func(_ context.Context, _ *models.AgentSession, _ string) error { return nil },
	})

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-start-no-retry",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateFailed, 2*time.Second)
	if attempts != 1 {
		t.Fatalf("expected 1 runtime start attempt, got %d", attempts)
	}
}

func TestServiceCodexBinaryValidationFailure(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-codex-missing-bin",
		Name:      "Slice Codex Missing Binary",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	t.Setenv(envAgentValidateCLIBinary, "1")
	t.Setenv(envCodexCLIBinary, "/tmp/path/that/does/not/exist/codex")

	svc := NewService(st, "test-secret")
	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-codex-missing-bin",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
		AgentType:     "codex",
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

func TestServiceHandleAgentInputProducesCodexEvents(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-agent-input",
		Name:      "Slice Agent Input",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	// Keep binary validation disabled for this flow test.
	_ = os.Unsetenv(envAgentValidateCLIBinary)

	svc := NewService(st, "test-secret")
	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-agent-input",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
		AgentType:     "codex",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	if err := svc.HandleAgentInput(ctx, session.SessionID, "Refactor this function"); err != nil {
		t.Fatalf("HandleAgentInput failed: %v", err)
	}

	events, _, err := svc.ListEventsForUser(ctx, "alice", session.SessionID, 0, 200)
	if err != nil {
		t.Fatalf("ListEventsForUser failed: %v", err)
	}
	foundFinal := false
	foundToolStart := false
	for _, event := range events {
		if event.Stream == "agent" && event.Type == "output_final" {
			foundFinal = true
		}
		if event.Stream == "tool" && event.Type == "start" {
			foundToolStart = true
		}
	}
	if !foundFinal {
		t.Fatalf("expected agent/output_final event")
	}
	if !foundToolStart {
		t.Fatalf("expected tool/start event")
	}
}

func TestServiceClaudeBinaryValidationFailure(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-claude-missing-bin",
		Name:      "Slice Claude Missing Binary",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	t.Setenv(envAgentValidateCLIBinary, "1")
	t.Setenv(envClaudeCLIBinary, "/tmp/path/that/does/not/exist/claude")

	svc := NewService(st, "test-secret")
	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-claude-missing-bin",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
		AgentType:     "claude",
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

func TestServiceHandleAgentInputProducesClaudeEvents(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-agent-input-claude",
		Name:      "Slice Agent Input Claude",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-agent-input-claude",
		Provider:      "e2b",
		E2BTemplateID: "tmpl-v1",
		AgentType:     "claude",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	if err := svc.HandleAgentInput(ctx, session.SessionID, "Explain this diff"); err != nil {
		t.Fatalf("HandleAgentInput failed: %v", err)
	}

	events, _, err := svc.ListEventsForUser(ctx, "alice", session.SessionID, 0, 200)
	if err != nil {
		t.Fatalf("ListEventsForUser failed: %v", err)
	}
	foundFinal := false
	for _, event := range events {
		if event.Stream == "agent" && event.Type == "output_final" && strings.Contains(string(event.Payload), "Claude completed request") {
			foundFinal = true
		}
	}
	if !foundFinal {
		t.Fatalf("expected Claude agent/output_final event")
	}
}

func TestServiceHandleAgentInputUsesRuntimeBridgeProvider(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-runtime-bridge-input",
		Name:      "Slice Runtime Bridge Input",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	inputCalls := 0
	svc := NewService(st, "test-secret")
	svc.SetRuntimeProviderFor(RuntimeProviderCloudflareContainers, &stubRuntimeProvider{
		startFn: func(_ context.Context, _ *models.AgentSession) (*RuntimeStartResult, error) {
			return &RuntimeStartResult{
				Provider:  RuntimeProviderCloudflareContainers,
				SessionID: "runtime-bridge-1",
				Endpoint:  "https://edge.internal/runtime-bridge-1",
				Status:    "ready",
			}, nil
		},
		stopFn: func(_ context.Context, _ *models.AgentSession, _ string) error { return nil },
		inputFn: func(_ context.Context, _ *models.AgentSession, text string) error {
			inputCalls++
			if text != "Bridge this input" {
				t.Fatalf("unexpected input text %q", text)
			}
			return nil
		},
		streamFn: func(_ context.Context, _ *models.AgentSession, sinceSeq uint64, limit int) ([]RuntimeBridgeEvent, uint64, error) {
			if sinceSeq >= 2 {
				return nil, sinceSeq, nil
			}
			_ = limit
			return []RuntimeBridgeEvent{
				{
					RuntimeSeq: 1,
					Stream:     "agent",
					Type:       "output_delta",
					Payload:    []byte(`{"text":"runtime delta"}`),
				},
				{
					RuntimeSeq: 2,
					Stream:     "agent",
					Type:       "output_final",
					Payload:    []byte(`{"text":"runtime final"}`),
				},
			}, 2, nil
		},
	})

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-runtime-bridge-input",
		Provider:      RuntimeProviderCloudflareContainers,
		E2BTemplateID: "cfc-profile",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	if err := svc.HandleAgentInput(ctx, session.SessionID, "Bridge this input"); err != nil {
		t.Fatalf("HandleAgentInput failed: %v", err)
	}
	if inputCalls != 1 {
		t.Fatalf("expected input provider call once, got %d", inputCalls)
	}

	events, _, err := svc.ListEventsForUser(ctx, "alice", session.SessionID, 0, 200)
	if err != nil {
		t.Fatalf("ListEventsForUser failed: %v", err)
	}
	foundRuntimeFinal := false
	for _, event := range events {
		if event.Stream == "agent" && event.Type == "output_final" && strings.Contains(string(event.Payload), "runtime final") {
			foundRuntimeFinal = true
		}
	}
	if !foundRuntimeFinal {
		t.Fatalf("expected bridged runtime output_final event")
	}
}

func TestServiceHandleAgentInterruptUsesRuntimeBridgeProvider(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-runtime-bridge-interrupt",
		Name:      "Slice Runtime Bridge Interrupt",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	interruptCalls := 0
	svc := NewService(st, "test-secret")
	svc.SetRuntimeProviderFor(RuntimeProviderCloudflareContainers, &stubRuntimeProvider{
		startFn: func(_ context.Context, _ *models.AgentSession) (*RuntimeStartResult, error) {
			return &RuntimeStartResult{
				Provider:  RuntimeProviderCloudflareContainers,
				SessionID: "runtime-bridge-2",
				Endpoint:  "https://edge.internal/runtime-bridge-2",
				Status:    "ready",
			}, nil
		},
		stopFn: func(_ context.Context, _ *models.AgentSession, _ string) error { return nil },
		interruptFn: func(_ context.Context, _ *models.AgentSession, reason string) error {
			interruptCalls++
			if reason != "Stop now" {
				t.Fatalf("unexpected interrupt reason %q", reason)
			}
			return nil
		},
		streamFn: func(_ context.Context, _ *models.AgentSession, sinceSeq uint64, limit int) ([]RuntimeBridgeEvent, uint64, error) {
			if sinceSeq >= 1 {
				return nil, sinceSeq, nil
			}
			_ = limit
			return []RuntimeBridgeEvent{
				{
					RuntimeSeq: 1,
					Stream:     "control",
					Type:       "error",
					Payload:    []byte(`{"code":"USER_INTERRUPT","message":"Stop now"}`),
				},
			}, 1, nil
		},
	})

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-runtime-bridge-interrupt",
		Provider:      RuntimeProviderCloudflareContainers,
		E2BTemplateID: "cfc-profile",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	if err := svc.HandleAgentInterrupt(ctx, session.SessionID, "Stop now"); err != nil {
		t.Fatalf("HandleAgentInterrupt failed: %v", err)
	}
	if interruptCalls != 1 {
		t.Fatalf("expected interrupt provider call once, got %d", interruptCalls)
	}

	events, _, err := svc.ListEventsForUser(ctx, "alice", session.SessionID, 0, 200)
	if err != nil {
		t.Fatalf("ListEventsForUser failed: %v", err)
	}
	foundRuntimeInterrupt := false
	for _, event := range events {
		if event.Stream == "control" && event.Type == "error" && strings.Contains(string(event.Payload), "USER_INTERRUPT") {
			foundRuntimeInterrupt = true
		}
	}
	if !foundRuntimeInterrupt {
		t.Fatalf("expected bridged runtime interrupt event")
	}
}

func TestServiceSyncRuntimeEventsSkipsConcurrentSync(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	now := time.Now().UTC()
	session := &models.AgentSession{
		SessionID:      "session-runtime-sync-race",
		SliceID:        "slice-runtime-sync-race",
		UserID:         "alice",
		State:          models.AgentSessionStateRunning,
		Provider:       RuntimeProviderCloudflareContainers,
		E2BTemplateID:  "cfc-profile",
		IdleTimeoutSec: 60,
		TTLSec:         600,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := st.CreateAgentSession(ctx, session); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}

	var streamCalls int32
	started := make(chan struct{})
	release := make(chan struct{})
	svc := NewService(st, "test-secret")
	provider := &stubRuntimeProvider{
		streamFn: func(_ context.Context, _ *models.AgentSession, sinceSeq uint64, _ int) ([]RuntimeBridgeEvent, uint64, error) {
			call := atomic.AddInt32(&streamCalls, 1)
			if call == 1 {
				close(started)
				<-release
			}
			return []RuntimeBridgeEvent{
				{
					RuntimeSeq: 1,
					Stream:     "agent",
					Type:       "output_final",
					Payload:    []byte(`{"text":"runtime final"}`),
				},
			}, 1, nil
		},
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- svc.syncRuntimeEvents(ctx, session, provider)
	}()
	<-started
	go func() {
		errCh <- svc.syncRuntimeEvents(ctx, session, provider)
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("syncRuntimeEvents failed: %v", err)
		}
	}
	if got := atomic.LoadInt32(&streamCalls); got != 1 {
		t.Fatalf("expected a single stream call during concurrent sync, got %d", got)
	}

	events, _, err := svc.ListEventsForUser(ctx, "alice", session.SessionID, 0, 50)
	if err != nil {
		t.Fatalf("ListEventsForUser failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly one bridged event, got %d", len(events))
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

func TestServiceSupportsCloudflareProviderRouting(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-cfc-provider",
		Name:      "Slice CFC Provider",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	svc.SetRuntimeProviderFor(RuntimeProviderCloudflareContainers, &stubRuntimeProvider{
		startFn: func(_ context.Context, session *models.AgentSession) (*RuntimeStartResult, error) {
			if got := strings.TrimSpace(session.Provider); got != RuntimeProviderCloudflareContainers {
				t.Fatalf("expected provider %q, got %q", RuntimeProviderCloudflareContainers, got)
			}
			if got := strings.TrimSpace(session.E2BTemplateID); got != "cfc-profile" {
				t.Fatalf("expected profile id cfc-profile, got %q", got)
			}
			return &RuntimeStartResult{
				Provider:  RuntimeProviderCloudflareContainers,
				SessionID: "cfc-runtime-1",
				Endpoint:  "wss://edge.internal/cfc-runtime-1",
				Status:    "ready",
			}, nil
		},
		stopFn: func(_ context.Context, _ *models.AgentSession, _ string) error { return nil },
	})

	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-cfc-provider",
		Provider:      RuntimeProviderCloudflareContainers,
		E2BTemplateID: "cfc-profile",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateRunning, 2*time.Second)

	got, err := svc.GetSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.RuntimeProvider != RuntimeProviderCloudflareContainers {
		t.Fatalf("expected runtime provider %q, got %q", RuntimeProviderCloudflareContainers, got.RuntimeProvider)
	}
	if got.RuntimeSessionID != "cfc-runtime-1" {
		t.Fatalf("expected runtime session id cfc-runtime-1, got %q", got.RuntimeSessionID)
	}
}

func TestServiceFailsWhenRuntimeProviderMissing(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(ctx, &models.Slice{
		ID:        "slice-missing-provider",
		Name:      "Slice Missing Provider",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}

	svc := NewService(st, "test-secret")
	session, _, err := svc.CreateSession(ctx, "alice", CreateRequest{
		SliceID:       "slice-missing-provider",
		Provider:      RuntimeProviderCloudflareContainers,
		E2BTemplateID: "cfc-profile",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	waitForSessionState(t, svc, session.SessionID, models.AgentSessionStateFailed, 2*time.Second)

	got, err := svc.GetSession(ctx, session.SessionID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.FailureCode != "RUNTIME_PROVIDER_UNAVAILABLE" {
		t.Fatalf("expected RUNTIME_PROVIDER_UNAVAILABLE, got %q", got.FailureCode)
	}
}

func TestServiceRuntimeHealthChecksByProvider(t *testing.T) {
	ctx := context.Background()
	st := storage.NewInMemoryStorage()
	svc := NewService(st, "test-secret")
	svc.SetRuntimeProviderFor(RuntimeProviderCloudflareContainers, &stubRuntimeProvider{
		healthFn: func(context.Context) error {
			return &RuntimeError{
				Code:    "CFC_RUNTIME_UNAVAILABLE",
				Message: "control plane unavailable",
			}
		},
	})

	checks := svc.RuntimeHealthChecks(ctx)
	if checks[RuntimeProviderE2B] != nil {
		t.Fatalf("expected healthy e2b runtime, got %v", checks[RuntimeProviderE2B])
	}
	if checks[RuntimeProviderCloudflareContainers] == nil {
		t.Fatalf("expected unhealthy cloudflare runtime")
	}
	if err := svc.SetDefaultRuntimeProvider(RuntimeProviderCloudflareContainers); err != nil {
		t.Fatalf("SetDefaultRuntimeProvider failed: %v", err)
	}
	if err := svc.RuntimeHealthCheck(ctx); err == nil {
		t.Fatalf("expected unhealthy default runtime")
	}
}

type stubRuntimeProvider struct {
	startFn     func(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error)
	stopFn      func(ctx context.Context, session *models.AgentSession, reason string) error
	healthFn    func(ctx context.Context) error
	inputFn     func(ctx context.Context, session *models.AgentSession, text string) error
	interruptFn func(ctx context.Context, session *models.AgentSession, reason string) error
	streamFn    func(ctx context.Context, session *models.AgentSession, sinceSeq uint64, limit int) ([]RuntimeBridgeEvent, uint64, error)
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

func (p *stubRuntimeProvider) HealthCheck(ctx context.Context) error {
	if p.healthFn == nil {
		return nil
	}
	return p.healthFn(ctx)
}

func (p *stubRuntimeProvider) SendInput(ctx context.Context, session *models.AgentSession, text string) error {
	if p.inputFn == nil {
		return nil
	}
	return p.inputFn(ctx, session, text)
}

func (p *stubRuntimeProvider) SendInterrupt(ctx context.Context, session *models.AgentSession, reason string) error {
	if p.interruptFn == nil {
		return nil
	}
	return p.interruptFn(ctx, session, reason)
}

func (p *stubRuntimeProvider) StreamEvents(ctx context.Context, session *models.AgentSession, sinceSeq uint64, limit int) ([]RuntimeBridgeEvent, uint64, error) {
	if p.streamFn == nil {
		return nil, sinceSeq, nil
	}
	return p.streamFn(ctx, session, sinceSeq, limit)
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
