package agentsession

import (
	"context"
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
