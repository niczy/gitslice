package agentservice

import (
	"context"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func agentTestContext(user string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "User "+user))
}

func TestAgentRunnerRegistrationLifecycle(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}

	registerResp, err := srv.RegisterRunner(ctx, &agentv1.RegisterRunnerRequest{
		RunnerId:      "runner-service",
		Provider:      agentsession.RuntimeProviderLocal,
		AgentType:     "codex",
		HostName:      "devbox",
		Pid:           4242,
		WorkspaceRoot: "/tmp/agents",
		Capabilities:  []byte(`{"codex":{"remoteControl":true}}`),
	})
	if err != nil {
		t.Fatalf("RegisterRunner failed: %v", err)
	}
	if registerResp.GetRunner().GetRunnerId() != "runner-service" {
		t.Fatalf("unexpected runner id: %#v", registerResp.GetRunner())
	}
	if registerResp.GetRunner().GetStatus() != string(models.AgentRunnerStatusOnline) {
		t.Fatalf("expected online runner, got %#v", registerResp.GetRunner())
	}

	listResp, err := srv.ListRunners(ctx, &agentv1.ListRunnersRequest{})
	if err != nil {
		t.Fatalf("ListRunners failed: %v", err)
	}
	if len(listResp.GetRunners()) != 1 {
		t.Fatalf("expected one online runner, got %#v", listResp.GetRunners())
	}

	heartbeatResp, err := srv.HeartbeatRunner(ctx, &agentv1.HeartbeatRunnerRequest{
		RunnerId: "runner-service",
		Pid:      5252,
	})
	if err != nil {
		t.Fatalf("HeartbeatRunner failed: %v", err)
	}
	if heartbeatResp.GetRunner().GetPid() != 5252 {
		t.Fatalf("expected pid update, got %#v", heartbeatResp.GetRunner())
	}

	unregisterResp, err := srv.UnregisterRunner(ctx, &agentv1.UnregisterRunnerRequest{RunnerId: "runner-service"})
	if err != nil {
		t.Fatalf("UnregisterRunner failed: %v", err)
	}
	if unregisterResp.GetRunner().GetStatus() != string(models.AgentRunnerStatusOffline) {
		t.Fatalf("expected offline unregister, got %#v", unregisterResp.GetRunner())
	}

	listResp, err = srv.ListRunners(ctx, &agentv1.ListRunnersRequest{})
	if err != nil {
		t.Fatalf("ListRunners after unregister failed: %v", err)
	}
	if len(listResp.GetRunners()) != 0 {
		t.Fatalf("expected offline runner to be hidden, got %#v", listResp.GetRunners())
	}
}

func TestRegisterRunnerRejectsCrossUserRunnerID(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	now := time.Now().UTC()
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-owned-by-bob",
		UserID:          "bob",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}
	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}
	if _, err := srv.RegisterRunner(ctx, &agentv1.RegisterRunnerRequest{
		RunnerId:  "runner-owned-by-bob",
		Provider:  agentsession.RuntimeProviderLocal,
		AgentType: "codex",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected permission denied for cross-user runner id, got %v", err)
	}
}

func TestAgentSessionCreateRequiresOnlineRunner(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(context.Background(), &models.Slice{
		ID:        "slice-runner-service",
		Name:      "Runner Service Slice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	now := time.Now().UTC()
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-online",
		UserID:          "alice",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}

	svc := agentsession.NewService(st, "test-secret")
	srv := &agentServiceServer{st: st, svc: svc}
	createResp, err := srv.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:  "slice-runner-service",
		RunnerId: "runner-online",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if createResp.GetRunnerId() != "runner-online" {
		t.Fatalf("expected runner id in response, got %#v", createResp)
	}

	if _, err := srv.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:  "slice-runner-service",
		RunnerId: "runner-online",
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected active session conflict, got %v", err)
	}
}

func TestAgentSessionCreateRejectsOfflineOrMismatchedRunner(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(context.Background(), &models.Slice{
		ID:        "slice-runner-reject",
		Name:      "Runner Reject Slice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	now := time.Now().UTC()
	for _, runner := range []*models.AgentRunner{
		{
			RunnerID:        "runner-offline",
			UserID:          "alice",
			Provider:        agentsession.RuntimeProviderLocal,
			AgentType:       "codex",
			Status:          models.AgentRunnerStatusOffline,
			LastHeartbeatAt: now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			RunnerID:        "runner-claude",
			UserID:          "alice",
			Provider:        agentsession.RuntimeProviderLocal,
			AgentType:       "claude",
			Status:          models.AgentRunnerStatusOnline,
			LastHeartbeatAt: now,
			CreatedAt:       now,
			UpdatedAt:       now,
		},
	} {
		if err := st.UpsertAgentRunner(context.Background(), runner); err != nil {
			t.Fatalf("UpsertAgentRunner failed: %v", err)
		}
	}

	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}
	if _, err := srv.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:  "slice-runner-reject",
		RunnerId: "runner-offline",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected offline runner rejection, got %v", err)
	}
	if _, err := srv.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:   "slice-runner-reject",
		RunnerId:  "runner-claude",
		AgentType: "codex",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected agent type mismatch, got %v", err)
	}
}
