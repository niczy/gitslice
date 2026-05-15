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

func TestRunnerHeartbeatReactivatesStoppedLocalSessions(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(context.Background(), &models.Slice{
		ID:        "slice-runner-reactivate",
		Name:      "Runner Reactivate Slice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	now := time.Now().UTC()
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-reactivate",
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
	session := &models.AgentSession{
		SessionID:      "sess-runner-reactivate",
		SliceID:        "slice-runner-reactivate",
		RunnerID:       "runner-reactivate",
		UserID:         "alice",
		State:          models.AgentSessionStateStopped,
		Provider:       agentsession.RuntimeProviderLocal,
		AgentType:      "codex",
		IdleTimeoutSec: 1800,
		TTLSec:         14400,
		RuntimeStatus:  "stopped",
		CreatedAt:      now,
		UpdatedAt:      now,
		StoppedAt:      &now,
	}
	if err := st.CreateAgentSession(context.Background(), session); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}

	srv := &agentServiceServer{st: st, svc: svc}
	if _, err := srv.HeartbeatRunner(ctx, &agentv1.HeartbeatRunnerRequest{RunnerId: "runner-reactivate"}); err != nil {
		t.Fatalf("HeartbeatRunner failed: %v", err)
	}
	got, err := svc.GetSession(context.Background(), session.SessionID)
	if err != nil {
		t.Fatalf("GetSession after heartbeat failed: %v", err)
	}
	if got.State != models.AgentSessionStateIdle {
		t.Fatalf("expected stopped local session to become idle, got %s", got.State)
	}
	if got.StoppedAt != nil {
		t.Fatalf("expected stopped_at to be cleared")
	}
}

func TestLocalSessionResponsesExposeLocalAvailability(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(context.Background(), &models.Slice{
		ID:        "slice-local-response-state",
		Name:      "Local Response State",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	now := time.Now().UTC()
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-local-response",
		UserID:          "alice",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		Capabilities:    []byte(`{"local_sessions_reported":true,"local_session_ids":[]}`),
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}
	if err := st.CreateAgentSession(context.Background(), &models.AgentSession{
		SessionID:      "sess-local-stopped-response",
		SliceID:        "slice-local-response-state",
		RunnerID:       "runner-local-response",
		UserID:         "alice",
		State:          models.AgentSessionStateStopped,
		Provider:       agentsession.RuntimeProviderLocal,
		AgentType:      "codex",
		IdleTimeoutSec: 1800,
		TTLSec:         14400,
		RuntimeStatus:  "stopped",
		CreatedAt:      now,
		UpdatedAt:      now,
		StoppedAt:      &now,
	}); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	if err := st.AppendAgentSessionEvent(context.Background(), &models.AgentSessionEvent{
		SessionID: "sess-local-stopped-response",
		Seq:       1,
		Stream:    agentsession.EventStreamStatus,
		Type:      "local_runner_attached",
		Payload:   []byte(`{}`),
		TS:        now,
	}); err != nil {
		t.Fatalf("AppendAgentSessionEvent failed: %v", err)
	}

	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}
	getResp, err := srv.GetSession(ctx, &agentv1.GetSessionRequest{SessionId: "sess-local-stopped-response"})
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if getResp.GetState() != "" {
		t.Fatalf("expected lifecycle state to be hidden, got %q", getResp.GetState())
	}
	if getResp.GetAvailability() != agentsession.SessionAvailabilityCloudOnly {
		t.Fatalf("expected cloud-only availability, got %q", getResp.GetAvailability())
	}

	listResp, err := srv.ListSessions(ctx, &agentv1.ListSessionsRequest{SliceId: "slice-local-response-state"})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(listResp.GetSessions()) != 1 {
		t.Fatalf("expected one session, got %#v", listResp.GetSessions())
	}
	if listResp.GetSessions()[0].GetState() != "" {
		t.Fatalf("expected lifecycle state to be hidden in list response, got %q", listResp.GetSessions()[0].GetState())
	}
	if listResp.GetSessions()[0].GetAvailability() != agentsession.SessionAvailabilityCloudOnly {
		t.Fatalf("expected list response to mark cloud-only, got %q", listResp.GetSessions()[0].GetAvailability())
	}
}

func TestLocalSessionResponsesSupportLegacyRunnerDiscovery(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(context.Background(), &models.Slice{
		ID:        "slice-legacy-runner",
		Name:      "Legacy Runner Slice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	now := time.Now().UTC()
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-legacy",
		UserID:          "alice",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		Capabilities:    []byte(`{"agent_type":"codex","checkout_per_session":true,"local_sessions_reported":true,"local_session_ids":null}`),
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}
	if err := st.CreateAgentSession(context.Background(), &models.AgentSession{
		SessionID:      "sess-legacy",
		SliceID:        "slice-legacy-runner",
		RunnerID:       "runner-legacy",
		UserID:         "alice",
		State:          models.AgentSessionStateRunning,
		Provider:       agentsession.RuntimeProviderLocal,
		AgentType:      "codex",
		IdleTimeoutSec: 1800,
		TTLSec:         14400,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}

	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}
	listResp, err := srv.ListSessions(ctx, &agentv1.ListSessionsRequest{SliceId: "slice-legacy-runner"})
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(listResp.GetSessions()) != 1 {
		t.Fatalf("expected one session, got %#v", listResp.GetSessions())
	}
	if listResp.GetSessions()[0].GetState() != string(models.AgentSessionStateRunning) {
		t.Fatalf("expected active state for legacy runner discovery, got %q", listResp.GetSessions()[0].GetState())
	}
	if listResp.GetSessions()[0].GetAvailability() != agentsession.SessionAvailabilityPendingLocal {
		t.Fatalf("expected pending local availability before attach, got %q", listResp.GetSessions()[0].GetAvailability())
	}

	if err := st.AppendAgentSessionEvent(context.Background(), &models.AgentSessionEvent{
		SessionID: "sess-legacy",
		Seq:       1,
		Stream:    agentsession.EventStreamStatus,
		Type:      "local_runner_attached",
		Payload:   []byte(`{}`),
		TS:        now,
	}); err != nil {
		t.Fatalf("AppendAgentSessionEvent failed: %v", err)
	}
	listResp, err = srv.ListSessions(ctx, &agentv1.ListSessionsRequest{SliceId: "slice-legacy-runner"})
	if err != nil {
		t.Fatalf("ListSessions after attach failed: %v", err)
	}
	if listResp.GetSessions()[0].GetAvailability() != agentsession.SessionAvailabilityLocal {
		t.Fatalf("expected local availability after attach for legacy runner, got %q", listResp.GetSessions()[0].GetAvailability())
	}
}

func TestRunnerLocalSessionIDsRequiresUsableList(t *testing.T) {
	if ids, reported := runnerLocalSessionIDs([]byte(`{"local_sessions_reported":true,"local_session_ids":null}`)); reported || len(ids) != 0 {
		t.Fatalf("expected null local_session_ids to be unusable, got reported=%v ids=%#v", reported, ids)
	}
	if ids, reported := runnerLocalSessionIDs([]byte(`{"local_sessions_reported":true,"local_session_ids":[]}`)); !reported || len(ids) != 0 {
		t.Fatalf("expected empty array to be reported, got reported=%v ids=%#v", reported, ids)
	}
	if ids, reported := runnerLocalSessionIDs([]byte(`{"local_session_ids":["sess-a","sess-a","sess-b"]}`)); !reported || len(ids) != 2 {
		t.Fatalf("expected unique ids from array, got reported=%v ids=%#v", reported, ids)
	}
}

func TestListRunnersUsesStableIdentityOrder(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	now := time.Now().UTC()
	for _, runner := range []*models.AgentRunner{
		{
			RunnerID:        "runner-codex",
			UserID:          "alice",
			Provider:        agentsession.RuntimeProviderLocal,
			AgentType:       "codex",
			Status:          models.AgentRunnerStatusOnline,
			HostName:        "zeta-host",
			WorkspaceRoot:   "/tmp/zeta",
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
			HostName:        "alpha-host",
			WorkspaceRoot:   "/tmp/alpha",
			LastHeartbeatAt: now.Add(-time.Second),
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		{
			RunnerID:        "runner-offline",
			UserID:          "alice",
			Provider:        agentsession.RuntimeProviderLocal,
			AgentType:       "aider",
			Status:          models.AgentRunnerStatusOffline,
			HostName:        "alpha-host",
			WorkspaceRoot:   "/tmp/offline",
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
	listResp, err := srv.ListRunners(ctx, &agentv1.ListRunnersRequest{IncludeOffline: true})
	if err != nil {
		t.Fatalf("ListRunners failed: %v", err)
	}
	got := make([]string, 0, len(listResp.GetRunners()))
	for _, runner := range listResp.GetRunners() {
		got = append(got, runner.GetRunnerId())
	}
	want := []string{"runner-claude", "runner-codex", "runner-offline"}
	if len(got) != len(want) {
		t.Fatalf("runner count = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("runner order = %#v, want %#v", got, want)
		}
	}
}

func TestListEventsTailReturnsLatestEvents(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	svc := agentsession.NewService(st, "test-secret")
	srv := &agentServiceServer{st: st, svc: svc}

	if err := st.CreateAgentSession(ctx, &models.AgentSession{
		SessionID: "sess-tail",
		SliceID:   "slice-tail",
		RunnerID:  "runner-tail",
		UserID:    "alice",
		State:     models.AgentSessionStateRunning,
		Provider:  agentsession.RuntimeProviderLocal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateAgentSession failed: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if err := svc.AppendEvent(ctx, &models.AgentSessionEvent{
			SessionID: "sess-tail",
			Stream:    agentsession.EventStreamAgent,
			Type:      agentsession.EventTypeOutputDelta,
			Payload:   []byte(`{"text":"delta"}`),
		}); err != nil {
			t.Fatalf("AppendEvent %d failed: %v", i, err)
		}
	}

	resp, err := srv.ListEvents(ctx, &agentv1.ListEventsRequest{
		SessionId: "sess-tail",
		Tail:      2,
	})
	if err != nil {
		t.Fatalf("ListEvents tail failed: %v", err)
	}
	if got, want := len(resp.GetEvents()), 2; got != want {
		t.Fatalf("expected %d tail events, got %d", want, got)
	}
	if got := []uint64{resp.GetEvents()[0].GetSeq(), resp.GetEvents()[1].GetSeq()}; got[0] != 4 || got[1] != 5 {
		t.Fatalf("expected latest events [4 5], got %#v", got)
	}
	if resp.GetNextSeq() != 6 {
		t.Fatalf("expected next_seq 6, got %d", resp.GetNextSeq())
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

func TestAgentSessionCreateAllowsRunnerSupportedAgentType(t *testing.T) {
	ctx := agentTestContext("alice")
	st := storage.NewInMemoryStorage()
	if err := st.CreateSlice(context.Background(), &models.Slice{
		ID:        "slice-runner-multi-agent",
		Name:      "Runner Multi Agent Slice",
		Owners:    []string{"alice"},
		CreatedBy: "alice",
	}); err != nil {
		t.Fatalf("CreateSlice failed: %v", err)
	}
	now := time.Now().UTC()
	if err := st.UpsertAgentRunner(context.Background(), &models.AgentRunner{
		RunnerID:        "runner-multi-agent",
		UserID:          "alice",
		Provider:        agentsession.RuntimeProviderLocal,
		AgentType:       "codex",
		Status:          models.AgentRunnerStatusOnline,
		Capabilities:    []byte(`{"default_agent_type":"codex","supported_agent_types":["codex","claude"]}`),
		LastHeartbeatAt: now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("UpsertAgentRunner failed: %v", err)
	}

	srv := &agentServiceServer{st: st, svc: agentsession.NewService(st, "test-secret")}
	createResp, err := srv.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:   "slice-runner-multi-agent",
		RunnerId:  "runner-multi-agent",
		AgentType: "claude",
	})
	if err != nil {
		t.Fatalf("CreateSession with runner-supported claude failed: %v", err)
	}
	if createResp.GetAgentType() != "claude" {
		t.Fatalf("expected claude session, got %#v", createResp)
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
