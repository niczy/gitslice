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

func TestListRunnersMarksStaleHeartbeatOffline(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	now := time.Now().UTC()
	staleHeartbeat := now.Add(-runnerOnlineTTL - time.Second)
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-stale",
		UserID:          "alice",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		LastHeartbeatAt: staleHeartbeat,
		CreatedAt:       staleHeartbeat,
		UpdatedAt:       staleHeartbeat,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}

	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}
	listResp, err := srv.ListRunners(ctx, &agentv1.ListRunnersRequest{IncludeOffline: true})
	if err != nil {
		t.Fatalf("ListRunners failed: %v", err)
	}
	if len(listResp.GetRunners()) != 1 {
		t.Fatalf("expected stale runner to be listed with include_offline, got %#v", listResp.GetRunners())
	}
	if listResp.GetRunners()[0].GetStatus() != string(models.AgentRunnerStatusOffline) {
		t.Fatalf("expected stale runner to be returned offline, got %#v", listResp.GetRunners()[0])
	}
	stored, err := st.GetAgentRunner(context.Background(), "runner-stale")
	if err != nil {
		t.Fatalf("GetAgentRunner failed: %v", err)
	}
	if stored.Status != models.AgentRunnerStatusOffline {
		t.Fatalf("expected stale runner status to be persisted offline, got %q", stored.Status)
	}
	if !stored.LastHeartbeatAt.Equal(staleHeartbeat) {
		t.Fatalf("expected last heartbeat to be preserved, got %s want %s", stored.LastHeartbeatAt, staleHeartbeat)
	}

	onlineResp, err := srv.ListRunners(ctx, &agentv1.ListRunnersRequest{})
	if err != nil {
		t.Fatalf("ListRunners online-only failed: %v", err)
	}
	if len(onlineResp.GetRunners()) != 0 {
		t.Fatalf("expected stale runner hidden from online-only list, got %#v", onlineResp.GetRunners())
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

	secondResp, err := srv.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:  "slice-runner-service",
		RunnerId: "runner-online",
	})
	if err != nil {
		t.Fatalf("CreateSession second active session failed: %v", err)
	}
	if secondResp.GetSessionId() == "" || secondResp.GetSessionId() == createResp.GetSessionId() {
		t.Fatalf("expected distinct second session id, got %#v", secondResp)
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

func TestAgentSessionCreateMarksStaleRunnerOffline(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(context.Background(), &models.Slice{
		ID:        "slice-stale-runner",
		Name:      "Stale Runner Slice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	now := time.Now().UTC()
	staleHeartbeat := now.Add(-runnerOnlineTTL - time.Second)
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-stale-create",
		UserID:          "alice",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		LastHeartbeatAt: staleHeartbeat,
		CreatedAt:       staleHeartbeat,
		UpdatedAt:       staleHeartbeat,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}

	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}
	if _, err := srv.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:  "slice-stale-runner",
		RunnerId: "runner-stale-create",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected stale runner rejection, got %v", err)
	}
	stored, err := st.GetAgentRunner(context.Background(), "runner-stale-create")
	if err != nil {
		t.Fatalf("GetAgentRunner failed: %v", err)
	}
	if stored.Status != models.AgentRunnerStatusOffline {
		t.Fatalf("expected stale runner status to be persisted offline, got %q", stored.Status)
	}
}
