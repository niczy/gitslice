package gscli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/storage"
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

func TestPendingAgentInputsKeepsPendingAfterConfigWarning(t *testing.T) {
	events := []*agentv1.EventEnvelope{
		testAgentInputEvent(t, 1, "request"),
		testAgentControlErrorEvent(t, 2, "CODEX_CONFIG_WARNING", "bubblewrap missing"),
	}

	got := pendingAgentInputs(events)
	want := []string{"request"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pendingAgentInputs() = %#v, want %#v", got, want)
	}
}

func TestParseLocalRunnerRestartRequest(t *testing.T) {
	got := parseLocalRunnerRestartRequest([]byte(`{"upgrade":true,"reason":"web_ui"}`))
	if !got.Upgrade || got.Reason != "web_ui" {
		t.Fatalf("parseLocalRunnerRestartRequest() = %#v", got)
	}
}

func TestParseLocalAgentChangesetExportRequest(t *testing.T) {
	got := parseLocalAgentChangesetExportRequest([]byte(`{"requestId":"req-1","message":"ship it","files":["README.md"]}`))
	if got.RequestID != "req-1" || got.Message != "ship it" || !reflect.DeepEqual(got.Files, []string{"README.md"}) {
		t.Fatalf("parseLocalAgentChangesetExportRequest() = %#v", got)
	}
}

func TestLatestPendingLocalAgentChangesRequest(t *testing.T) {
	events := []*agentv1.EventEnvelope{
		testAgentControlJSONEvent(t, 1, agentsession.EventTypeLocalChangesRequested, map[string]any{"requestId": "old"}),
		testAgentStatusJSONEvent(t, 2, agentsession.EventTypeLocalChanges, map[string]any{"request_id": "old"}),
		testAgentControlJSONEvent(t, 3, agentsession.EventTypeLocalChangesRequested, map[string]any{"requestId": "pending", "limit": 25}),
	}

	got, ok := latestPendingLocalAgentChangesRequest(events)
	if !ok {
		t.Fatalf("expected pending local changes request")
	}
	if got.Seq != 3 || got.Request.RequestID != "pending" || got.Request.Limit != 25 {
		t.Fatalf("latestPendingLocalAgentChangesRequest() = %#v", got)
	}
}

func TestLatestPendingLocalAgentChangesRequestClearsByRequestedSeq(t *testing.T) {
	events := []*agentv1.EventEnvelope{
		testAgentControlJSONEvent(t, 8, agentsession.EventTypeLocalChangesRequested, map[string]any{"limit": 25}),
		testAgentControlJSONEvent(t, 9, agentsession.EventTypeLocalChangesFailed, map[string]any{"requested_seq": 8}),
	}

	if got, ok := latestPendingLocalAgentChangesRequest(events); ok {
		t.Fatalf("latestPendingLocalAgentChangesRequest() = %#v, want no pending request", got)
	}
}

func TestBuildLocalAgentChangesPayloadReportsDirtyFiles(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir .gs: %v", err)
	}
	if err := writeSliceIDConfigAt(workdir, "home_alice"); err != nil {
		t.Fatalf("write slice config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	readmeInfo, err := os.Lstat(filepath.Join(workdir, "README.md"))
	if err != nil {
		t.Fatalf("stat README: %v", err)
	}
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    "home_alice",
		CommitHash: "cmt_base",
		Files: []checkoutTrackedFile{
			{
				Path:                 "README.md",
				Hash:                 storage.HashFileManifestContent([]byte("before\n"), false, ""),
				Size:                 readmeInfo.Size(),
				ModifiedTimeUnixNano: readmeInfo.ModTime().UnixNano(),
				ChangeTimeUnixNano:   fileChangeTimeUnixNano(readmeInfo),
			},
		},
	}
	addTestDirectoryRecords(t, workdir, index, "")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	payload, err := buildLocalAgentChangesPayload(workdir, "sess_abcdef1234567890", 42, localAgentChangesRequest{RequestID: "req-1", Limit: 1})
	if err != nil {
		t.Fatalf("buildLocalAgentChangesPayload failed: %v", err)
	}
	if payload["slice_id"] != "home_alice" || payload["checkout_base"] != "cmt_base" || payload["working_tree"] != "dirty" {
		t.Fatalf("unexpected payload identity/status: %#v", payload)
	}
	if payload["path_count"] != 2 || payload["truncated"] != true {
		t.Fatalf("expected two paths with truncation, got %#v", payload)
	}
	changes, ok := payload["changes"].(map[string]any)
	if !ok {
		t.Fatalf("changes payload has unexpected type: %#v", payload["changes"])
	}
	if changes["added"] != 1 || changes["modified"] != 1 || changes["deleted"] != 0 {
		t.Fatalf("unexpected changes summary: %#v", changes)
	}
	paths, ok := payload["paths"].([]map[string]any)
	if !ok || len(paths) != 1 {
		t.Fatalf("expected one printed path, got %#v", payload["paths"])
	}
}

func TestLocalAgentChangesetExportRefreshesSliceAuth(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir .gs: %v", err)
	}
	if err := writeSliceIDConfigAt(workdir, "home_alice"); err != nil {
		t.Fatalf("write slice config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	readmeInfo, err := os.Lstat(filepath.Join(workdir, "README.md"))
	if err != nil {
		t.Fatalf("stat README: %v", err)
	}
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    "home_alice",
		CommitHash: "cmt_base",
		Files: []checkoutTrackedFile{
			{
				Path:                 "README.md",
				Hash:                 storage.HashFileManifestContent([]byte("before\n"), false, ""),
				Size:                 readmeInfo.Size(),
				ModifiedTimeUnixNano: readmeInfo.ModTime().UnixNano(),
				ChangeTimeUnixNano:   fileChangeTimeUnixNano(readmeInfo),
			},
		},
	}
	addTestDirectoryRecords(t, workdir, index, "")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "README.md"), []byte("after\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}

	sliceClient := &authCheckingSliceServiceClient{t: t, expectedAuth: "Bearer fresh-access"}
	agentClient := &fakeAgentServiceClient{}
	cfg := localAgentRunConfig{
		SessionID: "sess_export",
		CWD:       workdir,
		AuthContext: func(ctx context.Context) (context.Context, error) {
			return replaceCLIAuth(ctx, cliAuth{Authorization: "Bearer fresh-access"}), nil
		},
	}
	staleCtx := replaceCLIAuth(context.Background(), cliAuth{Authorization: "Bearer stale-access"})
	if err := handleLocalAgentChangesetExportRequest(staleCtx, &CLI{sliceClient: sliceClient, agentClient: agentClient}, cfg, 9, localAgentChangesetExportRequest{
		RequestID: "export-1",
		Message:   "ship it",
	}); err != nil {
		t.Fatalf("handleLocalAgentChangesetExportRequest failed: %v", err)
	}
	if sliceClient.createCalls != 1 || sliceClient.reviewCalls != 1 {
		t.Fatalf("expected one create and one review call, got create=%d review=%d", sliceClient.createCalls, sliceClient.reviewCalls)
	}
	if len(agentClient.appendedEvents) != 3 {
		t.Fatalf("expected started/completed/local changes events, got %d", len(agentClient.appendedEvents))
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

func TestClaimAgentSupervisorPIDFileRejectsLiveRunner(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("sh", "-c", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	writeAgentSupervisorPIDFile(root, cmd.Process.Pid)
	err := claimAgentSupervisorPIDFile(root, os.Getpid(), 0)
	var runningErr *agentSupervisorAlreadyRunningError
	if !errors.As(err, &runningErr) {
		t.Fatalf("expected already-running error, got %v", err)
	}
	if runningErr.PID != cmd.Process.Pid {
		t.Fatalf("expected running pid %d, got %d", cmd.Process.Pid, runningErr.PID)
	}
}

func TestClaimAgentSupervisorPIDFileReplacesStalePID(t *testing.T) {
	root := t.TempDir()
	pidFile, err := agentSupervisorPIDFile(root)
	if err != nil {
		t.Fatalf("agentSupervisorPIDFile failed: %v", err)
	}
	if err := os.WriteFile(pidFile, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if err := claimAgentSupervisorPIDFile(root, os.Getpid(), 0); err != nil {
		t.Fatalf("claimAgentSupervisorPIDFile failed: %v", err)
	}
	got, err := readAgentSupervisorPIDFilePath(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	if got != os.Getpid() {
		t.Fatalf("expected pid file to contain %d, got %d", os.Getpid(), got)
	}
}

func TestClearAgentSupervisorPIDFileOnlyClearsMatchingPID(t *testing.T) {
	root := t.TempDir()
	writeAgentSupervisorPIDFile(root, os.Getpid()+1)
	clearAgentSupervisorPIDFileIfMatches(root, os.Getpid())
	pidFile, err := agentSupervisorPIDFile(root)
	if err != nil {
		t.Fatalf("agentSupervisorPIDFile failed: %v", err)
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("expected mismatched pid file to remain: %v", err)
	}
	writeAgentSupervisorPIDFile(root, os.Getpid())
	clearAgentSupervisorPIDFileIfMatches(root, os.Getpid())
	if _, err := os.Stat(pidFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected matching pid file to be removed, got %v", err)
	}
}

func TestLocalAgentRunnerCapabilitiesReportsLocalSessionIDs(t *testing.T) {
	root := t.TempDir()
	checkoutDir := filepath.Join(root, "alice-demo-sess-local")
	if err := os.MkdirAll(checkoutDir, 0o755); err != nil {
		t.Fatalf("mkdir checkout: %v", err)
	}
	discovered := discoveredAgentSession{
		session: &agentv1.AgentSessionSummary{
			SessionId: "sess-local",
			SliceId:   "slice-local",
			RunnerId:  "runner-local",
			AgentType: "codex",
		},
	}
	if err := writeLocalAgentSessionMarker(root, discovered, checkoutDir); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	raw, err := localAgentRunnerCapabilities(localAgentSupervisorConfig{
		RootDir:   root,
		AgentType: "codex",
	})
	if err != nil {
		t.Fatalf("localAgentRunnerCapabilities failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	if payload["local_sessions_reported"] != true {
		t.Fatalf("expected local_sessions_reported=true, got %#v", payload["local_sessions_reported"])
	}
	values, ok := payload["local_session_ids"].([]any)
	if !ok || len(values) != 1 || values[0] != "sess-local" {
		t.Fatalf("expected local_session_ids to include sess-local, got %#v", payload["local_session_ids"])
	}
}

func TestLocalAgentRunnerCapabilitiesReportsEmptyLocalSessionIDs(t *testing.T) {
	root := t.TempDir()
	raw, err := localAgentRunnerCapabilities(localAgentSupervisorConfig{
		RootDir:   root,
		AgentType: "codex",
	})
	if err != nil {
		t.Fatalf("localAgentRunnerCapabilities failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	values, ok := payload["local_session_ids"].([]any)
	if !ok || len(values) != 0 {
		t.Fatalf("expected local_session_ids to be an empty array, got %#v", payload["local_session_ids"])
	}
}

func TestAgentSessionShouldRunLocallyByAvailability(t *testing.T) {
	cases := []struct {
		name         string
		availability string
		want         bool
	}{
		{name: "local", availability: "local", want: true},
		{name: "pending local", availability: "pending_local", want: true},
		{name: "cloud only", availability: "cloud_only", want: false},
		{name: "failed", availability: "failed", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := agentSessionShouldRunLocally(&agentv1.AgentSessionSummary{
				SessionId:    "sess-test",
				Provider:     "local",
				Availability: tc.availability,
			})
			if got != tc.want {
				t.Fatalf("agentSessionShouldRunLocally() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEnsureAgentInstructionFilesAreLocalOnly(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir .gs: %v", err)
	}
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    "slice-test",
		CommitHash: "commit-1",
	}
	addTestDirectoryRecords(t, workdir, index, "")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}

	if err := ensureAgentInstructionFiles(workdir); err != nil {
		t.Fatalf("ensure agent instruction files: %v", err)
	}
	for _, name := range agentInstructionFileNames {
		content, err := os.ReadFile(filepath.Join(workdir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(content), agentInstructionFileMarker) {
			t.Fatalf("%s missing generated marker", name)
		}
	}

	entries, err := collectNoGitWorkingTreeStatusFullScan(workdir, index)
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected generated instruction files to be ignored, got %#v", entries)
	}

	lookup := newCheckoutIndexLookup(index)
	entries, remaining, err := collectNoGitWorkingTreeStatusFromCandidates(workdir, lookup, agentInstructionFileNames)
	if err != nil {
		t.Fatalf("collect candidate status: %v", err)
	}
	if len(entries) != 0 || len(remaining) != 0 {
		t.Fatalf("expected generated instruction candidates to be ignored, got entries=%#v remaining=%#v", entries, remaining)
	}
}

func TestEnsureAgentInstructionFilesDoesNotHideUserFiles(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".gs"), 0o755); err != nil {
		t.Fatalf("mkdir .gs: %v", err)
	}
	index := &localCheckoutIndex{
		Version:    checkoutIndexVersion,
		SliceID:    "slice-test",
		CommitHash: "commit-1",
	}
	addTestDirectoryRecords(t, workdir, index, "")
	if err := writeCheckoutIndex(workdir, index); err != nil {
		t.Fatalf("write checkout index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "AGENTS.md"), []byte("project-specific notes\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	entries, err := collectNoGitWorkingTreeStatusFullScan(workdir, index)
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	got := collectWorkingTreeStatusPaths(entries)
	want := []string{"AGENTS.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected status paths:\n got %#v\nwant %#v", got, want)
	}
}

func TestEnsureAgentInstructionFilesDoesNotOverwriteExistingFiles(t *testing.T) {
	workdir := t.TempDir()
	existing := []byte("project-specific notes\n")
	if err := os.WriteFile(filepath.Join(workdir, "AGENTS.md"), existing, 0o644); err != nil {
		t.Fatalf("write existing AGENTS.md: %v", err)
	}

	if err := ensureAgentInstructionFiles(workdir); err != nil {
		t.Fatalf("ensure agent instruction files: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workdir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(content) != string(existing) {
		t.Fatalf("expected existing AGENTS.md to be preserved, got %q", string(content))
	}
	if _, err := os.Stat(filepath.Join(workdir, "CLAUDE.md")); err != nil {
		t.Fatalf("expected CLAUDE.md to be created: %v", err)
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

func testAgentControlErrorEvent(t *testing.T, seq uint64, code, message string) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentErrorPayload{Code: code, Message: message})
	if err != nil {
		t.Fatalf("marshal error payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "control", Type: "error", Payload: payload}
}

func testAgentStateEvent(t *testing.T, seq uint64, state string) *agentv1.EventEnvelope {
	t.Helper()
	payload, err := protojson.Marshal(&agentv1.AgentStatePayload{State: state})
	if err != nil {
		t.Fatalf("marshal state payload: %v", err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: "status", Type: "state", Payload: payload}
}

func testAgentControlJSONEvent(t *testing.T, seq uint64, eventType string, payload any) *agentv1.EventEnvelope {
	t.Helper()
	return testAgentJSONEvent(t, seq, "control", eventType, payload)
}

func testAgentStatusJSONEvent(t *testing.T, seq uint64, eventType string, payload any) *agentv1.EventEnvelope {
	t.Helper()
	return testAgentJSONEvent(t, seq, "status", eventType, payload)
}

func testAgentJSONEvent(t *testing.T, seq uint64, stream, eventType string, payload any) *agentv1.EventEnvelope {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s/%s payload: %v", stream, eventType, err)
	}
	return &agentv1.EventEnvelope{Seq: seq, Stream: stream, Type: eventType, Payload: payloadBytes}
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
	accessToken     string
	refreshToken    string
	authContext     *accountv1.GetAuthContextResponse
	authContextErr  error
	authContextSeen bool
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

func (f *fakeAccountServiceClient) GetAuthContext(ctx context.Context, req *accountv1.GetAuthContextRequest, opts ...grpc.CallOption) (*accountv1.GetAuthContextResponse, error) {
	_ = ctx
	_ = req
	_ = opts
	f.authContextSeen = true
	if f.authContextErr != nil {
		return nil, f.authContextErr
	}
	if f.authContext != nil {
		return f.authContext, nil
	}
	return &accountv1.GetAuthContextResponse{
		Authenticated: true,
		Username:      "stored-user",
		AuthSource:    "local_session",
	}, nil
}

type fakeAgentServiceClient struct {
	agentv1.AgentServiceClient
	registerCalls  int
	heartbeatCalls int
	heartbeatErrs  []error
	appendedEvents []*agentv1.AppendEventRequest
}

func (f *fakeAgentServiceClient) AppendEvent(ctx context.Context, req *agentv1.AppendEventRequest, opts ...grpc.CallOption) (*agentv1.AppendEventResponse, error) {
	_ = ctx
	_ = opts
	f.appendedEvents = append(f.appendedEvents, req)
	return &agentv1.AppendEventResponse{Event: &agentv1.EventEnvelope{
		Seq:     uint64(len(f.appendedEvents)),
		Stream:  req.GetStream(),
		Type:    req.GetType(),
		Payload: req.GetPayload(),
	}}, nil
}

type authCheckingSliceServiceClient struct {
	slicev1.SliceServiceClient
	t            *testing.T
	expectedAuth string
	createCalls  int
	reviewCalls  int
}

func (f *authCheckingSliceServiceClient) CreateChangeset(ctx context.Context, req *slicev1.CreateChangesetRequest, opts ...grpc.CallOption) (*slicev1.CreateChangesetResponse, error) {
	_ = opts
	f.createCalls++
	f.requireAuth(ctx)
	if req.GetSliceId() != "home_alice" || req.GetBaseCommitHash() != "cmt_base" || !reflect.DeepEqual(req.GetModifiedFiles(), []string{"README.md"}) {
		f.t.Fatalf("unexpected CreateChangeset request: %#v", req)
	}
	return &slicev1.CreateChangesetResponse{
		ChangesetId:   "cs_export",
		ChangesetHash: "hash_export",
		Status:        slicev1.ChangesetStatus_PENDING,
	}, nil
}

func (f *authCheckingSliceServiceClient) ReviewChangeset(ctx context.Context, req *slicev1.ReviewChangesetRequest, opts ...grpc.CallOption) (*slicev1.ReviewChangesetResponse, error) {
	_ = opts
	f.reviewCalls++
	f.requireAuth(ctx)
	if req.GetChangesetId() != "cs_export" {
		f.t.Fatalf("unexpected ReviewChangeset request: %#v", req)
	}
	return &slicev1.ReviewChangesetResponse{
		Snapshot: &slicev1.ChangesetSnapshotInfo{
			SnapshotId:     "snap_export",
			ChangesetId:    "cs_export",
			Version:        1,
			Hash:           "snap_hash",
			BaseCommitHash: "cmt_base",
			ModifiedFiles:  []string{"README.md"},
		},
	}, nil
}

func (f *authCheckingSliceServiceClient) requireAuth(ctx context.Context) {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		f.t.Fatalf("missing outgoing metadata")
	}
	if got := strings.Join(md.Get("authorization"), ","); got != f.expectedAuth {
		f.t.Fatalf("expected authorization %q, got %q", f.expectedAuth, got)
	}
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
