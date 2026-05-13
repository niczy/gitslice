package gscli

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	accountv1 "github.com/niczy/gitslice/proto/account"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPendingAgentInputsKeepsOnlyUnansweredTurns(t *testing.T) {
	events := []*agentv1.EventEnvelope{
		testAgentInputEvent(t, 1, "old request"),
		testAgentOutputFinalEvent(t, 2),
		testAgentInputEvent(t, 3, "pending request"),
	}

	got := pendingAgentInputs(events)
	want := []string{"pending request"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pendingAgentInputs() = %#v, want %#v", got, want)
	}
}

func TestPendingAgentInputsClearsOnErrorAndInactiveState(t *testing.T) {
	cases := []struct {
		name   string
		events []*agentv1.EventEnvelope
	}{
		{
			name: "control error",
			events: []*agentv1.EventEnvelope{
				testAgentInputEvent(t, 1, "request"),
				{Seq: 2, Stream: "control", Type: "error", Payload: []byte(`{"message":"failed"}`)},
			},
		},
		{
			name: "stopped state",
			events: []*agentv1.EventEnvelope{
				testAgentInputEvent(t, 1, "request"),
				testAgentStateEvent(t, 2, "stopped"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pendingAgentInputs(tc.events); len(got) != 0 {
				t.Fatalf("pendingAgentInputs() = %#v, want no pending inputs", got)
			}
		})
	}
}

func TestParseLocalRunnerRestartRequest(t *testing.T) {
	got := parseLocalRunnerRestartRequest([]byte(`{"upgrade":true,"reason":"web_ui"}`))
	if !got.Upgrade || got.Reason != "web_ui" {
		t.Fatalf("parseLocalRunnerRestartRequest() = %#v", got)
	}
}

func TestBackgroundAgentEnvDoesNotPinStoredAccessToken(t *testing.T) {
	t.Setenv("GS_API_KEY", "outer-token")
	env := backgroundAgentEnv(cliAuth{
		Authorization:   "Bearer stored-access",
		Username:        "alice",
		Source:          "~/.gitslice/credentials.json",
		CredentialStore: true,
	})
	if envHasKey(env, "GS_API_KEY") {
		t.Fatalf("backgroundAgentEnv pinned GS_API_KEY for credential-store auth: %#v", env)
	}
	if got := envValue(env, "GS_USERNAME"); got != "alice" {
		t.Fatalf("expected GS_USERNAME=alice, got %q", got)
	}
}

func TestAgentSupervisorAuthRefresherRefreshesStoredCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()
	if err := writeCredentialsConfig(credentialsConfig{
		Username:              "alice",
		AccessToken:           "expired-access",
		RefreshToken:          "refresh-token",
		AccessTokenExpiresAt:  now.Add(-time.Minute).Format(time.RFC3339),
		RefreshTokenExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("writeCredentialsConfig failed: %v", err)
	}

	refresher := newAgentSupervisorAuthRefresher(&CLI{
		accountClient: &fakeAccountServiceClient{
			accessToken:  "fresh-access",
			refreshToken: "fresh-refresh",
		},
	}, cliAuth{
		Authorization:   "Bearer expired-access",
		Username:        "alice",
		Source:          "~/.gitslice/credentials.json",
		CredentialStore: true,
	})
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer expired-access")
	authCtx, err := refresher.context(ctx)
	if err != nil {
		t.Fatalf("refresher.context failed: %v", err)
	}
	md, ok := metadata.FromOutgoingContext(authCtx)
	if !ok {
		t.Fatalf("expected outgoing metadata")
	}
	if got := strings.Join(md.Get("authorization"), ","); got != "Bearer fresh-access" {
		t.Fatalf("expected refreshed authorization, got %q", got)
	}
}

func TestHeartbeatOrRegisterLocalAgentRunnerReregistersMissingRunner(t *testing.T) {
	client := &fakeAgentServiceClient{
		heartbeatErrs: []error{status.Error(codes.NotFound, "runner not found")},
	}
	cli := &CLI{agentClient: client}

	resp, reRegistered, err := heartbeatOrRegisterLocalAgentRunner(context.Background(), cli, localAgentSupervisorConfig{
		RootDir:   t.TempDir(),
		RunnerID:  "runner-test",
		AgentType: "codex",
	})
	if err != nil {
		t.Fatalf("heartbeatOrRegisterLocalAgentRunner failed: %v", err)
	}
	if !reRegistered {
		t.Fatalf("expected missing runner to trigger re-registration")
	}
	if client.registerCalls != 1 {
		t.Fatalf("expected one register call, got %d", client.registerCalls)
	}
	if client.heartbeatCalls != 2 {
		t.Fatalf("expected heartbeat retry after register, got %d calls", client.heartbeatCalls)
	}
	if resp.GetRunner().GetStatus() != "online" {
		t.Fatalf("expected online runner response, got %#v", resp.GetRunner())
	}
}

func TestHeartbeatOrRegisterLocalAgentRunnerDoesNotReregisterUnavailable(t *testing.T) {
	client := &fakeAgentServiceClient{
		heartbeatErrs: []error{status.Error(codes.Unavailable, "server unavailable")},
	}
	cli := &CLI{agentClient: client}

	_, reRegistered, err := heartbeatOrRegisterLocalAgentRunner(context.Background(), cli, localAgentSupervisorConfig{
		RootDir:   t.TempDir(),
		RunnerID:  "runner-test",
		AgentType: "codex",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("expected unavailable heartbeat error, got %v", err)
	}
	if reRegistered {
		t.Fatalf("did not expect re-registration for unavailable server")
	}
	if client.registerCalls != 0 {
		t.Fatalf("expected no register calls, got %d", client.registerCalls)
	}
}

func TestAgentSessionCheckoutDirNameIncludesFullSessionID(t *testing.T) {
	discovered := discoveredAgentSession{
		session: &agentv1.AgentSessionSummary{
			SessionId: "sess_abcdefghijklmnop123456",
			SliceId:   "slice-1",
		},
		slice: &slicev1.SliceInfo{Slug: "alice/demo slice"},
	}
	got := agentSessionCheckoutDirName(discovered)
	if !strings.HasPrefix(got, "alice-demo-slice-") {
		t.Fatalf("expected checkout dir to include sanitized slice label, got %q", got)
	}
	if !strings.Contains(got, "abcdefghijklmnop123456") {
		t.Fatalf("expected checkout dir to include full session id suffix, got %q", got)
	}
}

func testAgentInputEvent(t *testing.T, seq uint64, text string) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentInputPayload{Text: text})
	if err != nil {
		t.Fatalf("marshal input payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "agent", Type: "input", Payload: payload}
}

func testAgentOutputFinalEvent(t *testing.T, seq uint64) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentOutputPayload{Text: "done", Channel: "stdout"})
	if err != nil {
		t.Fatalf("marshal output payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "agent", Type: "output_final", Payload: payload}
}

func testAgentStateEvent(t *testing.T, seq uint64, state string) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentStatePayload{State: state})
	if err != nil {
		t.Fatalf("marshal state payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "status", Type: "state", Payload: payload}
}

func envHasKey(env []string, key string) bool {
	return envValue(env, key) != ""
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

type fakeAccountServiceClient struct {
	accountv1.AccountServiceClient
	accessToken  string
	refreshToken string
}

func (f *fakeAccountServiceClient) RefreshAccessToken(ctx context.Context, req *accountv1.RefreshAccessTokenRequest, opts ...grpc.CallOption) (*accountv1.AuthResponse, error) {
	_ = ctx
	_ = opts
	if req.GetRefreshToken() != "refresh-token" {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}
	return &accountv1.AuthResponse{
		AccessToken:           f.accessToken,
		RefreshToken:          f.refreshToken,
		AccessTokenExpiresAt:  time.Now().Add(15 * time.Minute).Format(time.RFC3339),
		RefreshTokenExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
	}, nil
}

type fakeAgentServiceClient struct {
	agentv1.AgentServiceClient
	registerCalls  int
	heartbeatCalls int
	heartbeatErrs  []error
}

func (f *fakeAgentServiceClient) RegisterRunner(ctx context.Context, req *agentv1.RegisterRunnerRequest, opts ...grpc.CallOption) (*agentv1.RegisterRunnerResponse, error) {
	_ = ctx
	_ = opts
	f.registerCalls++
	return &agentv1.RegisterRunnerResponse{
		Runner: &agentv1.AgentRunner{
			RunnerId:      req.GetRunnerId(),
			Provider:      req.GetProvider(),
			AgentType:     req.GetAgentType(),
			Status:        "online",
			HostName:      req.GetHostName(),
			Pid:           req.GetPid(),
			WorkspaceRoot: req.GetWorkspaceRoot(),
		},
		HeartbeatIntervalSec: 10,
	}, nil
}

func (f *fakeAgentServiceClient) HeartbeatRunner(ctx context.Context, req *agentv1.HeartbeatRunnerRequest, opts ...grpc.CallOption) (*agentv1.HeartbeatRunnerResponse, error) {
	_ = ctx
	_ = opts
	f.heartbeatCalls++
	if len(f.heartbeatErrs) > 0 {
		err := f.heartbeatErrs[0]
		f.heartbeatErrs = f.heartbeatErrs[1:]
		return nil, err
	}
	return &agentv1.HeartbeatRunnerResponse{
		Runner: &agentv1.AgentRunner{
			RunnerId:      req.GetRunnerId(),
			Provider:      "local",
			AgentType:     "codex",
			Status:        "online",
			HostName:      req.GetHostName(),
			Pid:           req.GetPid(),
			WorkspaceRoot: req.GetWorkspaceRoot(),
		},
		HeartbeatIntervalSec: 10,
	}, nil
}
