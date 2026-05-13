package gscli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	agentv1 "github.com/niczy/gitslice/proto/agent"
	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const agentWorkspaceStateDir = ".gitslice-agent"

const agentSupervisorAuthCheckInterval = 15 * time.Second

var safeAgentCheckoutNamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type localAgentSupervisorConfig struct {
	RootDir      string
	RunnerID     string
	AgentType    string
	CodexMode    string
	ClaudeMode   string
	Command      []string
	PollInterval time.Duration
	Once         bool
	LogFile      string
}

type agentSupervisorStartOutput struct {
	Status   string `json:"status"`
	RunnerID string `json:"runner_id"`
	PID      int    `json:"pid"`
	CWD      string `json:"cwd"`
	LogFile  string `json:"log_file"`
}

type discoveredAgentSession struct {
	session *agentv1.AgentSessionSummary
	slice   *slicev1.SliceInfo
}

type managedAgentSession struct {
	cancel context.CancelFunc
	done   chan agentSessionRunResult
}

type agentSessionRunResult struct {
	sessionID string
	completed int
	err       error
}

var errAgentSupervisorRestarting = errors.New("agent supervisor restarting")

type localRunnerRestartRequest struct {
	Upgrade bool   `json:"upgrade"`
	Reason  string `json:"reason"`
}

func startAgentSupervisorBackground(ctx context.Context, cli *CLI, authConfig cliAuth, cfg localAgentSupervisorConfig) (*agentSupervisorStartOutput, error) {
	_ = ctx
	_ = cli

	rootDir, err := resolveAgentWorkspaceRoot(cfg.RootDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(rootDir, agentWorkspaceStateDir), 0o755); err != nil {
		return nil, err
	}
	runnerID, err := ensureAgentRunnerID(rootDir)
	if err != nil {
		return nil, err
	}
	logFile := strings.TrimSpace(cfg.LogFile)
	if logFile == "" {
		logFile = filepath.Join(rootDir, agentWorkspaceStateDir, "agent.log")
	}
	if !filepath.IsAbs(logFile) {
		logFile = filepath.Join(rootDir, logFile)
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return nil, err
	}
	logHandle, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer logHandle.Close()

	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}

	cfg.RootDir = rootDir
	cfg.RunnerID = runnerID
	logAgentRunnerStartup(authConfig, cfg)
	log.Printf("Starting background local agent runner: executable=%s log_file=%s", executable, logFile)

	args := append(backgroundEndpointArgs(), "agent", "run", "--cwd", rootDir)
	if agentType := strings.TrimSpace(cfg.AgentType); agentType != "" {
		args = append(args, "--agent", agentType)
	}
	if codexMode := strings.TrimSpace(cfg.CodexMode); codexMode != "" {
		args = append(args, "--codex-mode", codexMode)
	}
	if claudeMode := strings.TrimSpace(cfg.ClaudeMode); claudeMode != "" {
		args = append(args, "--claude-mode", claudeMode)
	}
	if cfg.PollInterval > 0 {
		args = append(args, "--poll-interval", cfg.PollInterval.String())
	}
	if len(cfg.Command) > 0 {
		args = append(args, "--")
		args = append(args, cfg.Command...)
	}

	cmd := exec.Command(executable, args...)
	cmd.Dir = rootDir
	cmd.Stdout = logHandle
	cmd.Stderr = logHandle
	cmd.Env = backgroundAgentEnv(authConfig)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		log.Printf("Warning: failed to release agent runner process handle: %v", err)
	}

	writeAgentSupervisorPIDFile(rootDir, pid)
	return &agentSupervisorStartOutput{
		Status:   "started",
		RunnerID: runnerID,
		PID:      pid,
		CWD:      rootDir,
		LogFile:  logFile,
	}, nil
}

func backgroundEndpointArgs() []string {
	settings, err := resolveEndpointSettings()
	if err != nil {
		return nil
	}
	args := []string{
		"--account-addr", settings.AccountAddr,
		"--slice-addr", settings.SliceAddr,
		"--admin-addr", settings.AdminAddr,
		"--file-addr", settings.FileAddr,
		"--fs-addr", settings.FSAddr,
	}
	if settings.TLS {
		args = append(args, "--tls")
	}
	return args
}

func backgroundAgentEnv(authConfig cliAuth) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch key {
		case "GS_USERNAME", "GS_API_KEY", "GS_API_KEY_FILE":
			continue
		}
		env = append(env, item)
	}
	if strings.TrimSpace(authConfig.Username) != "" {
		env = append(env, "GS_USERNAME="+strings.TrimSpace(authConfig.Username))
	}
	if !authConfig.CredentialStore && strings.HasPrefix(strings.TrimSpace(authConfig.Authorization), "Bearer ") {
		env = append(env, "GS_API_KEY="+strings.TrimSpace(strings.TrimPrefix(authConfig.Authorization, "Bearer ")))
	}
	return env
}

func resolveAgentWorkspaceRoot(raw string) (string, error) {
	root := strings.TrimSpace(raw)
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

func logAgentRunnerStartup(authConfig cliAuth, cfg localAgentSupervisorConfig) {
	settings, err := resolveEndpointSettings()
	if err != nil {
		log.Printf("Agent runner endpoint: failed to resolve endpoint settings: %v", err)
	} else {
		log.Printf(
			"Agent runner endpoint: agent_addr=%s account_addr=%s file_addr=%s fs_addr=%s tls=%t address_source=%s tls_source=%s",
			settings.SliceAddr,
			settings.AccountAddr,
			settings.FileAddr,
			settings.FSAddr,
			settings.TLS,
			logValue(settings.AddressSource, "unknown"),
			logValue(settings.TLSSource, "unknown"),
		)
	}
	log.Printf(
		"Agent runner auth: username=%s source=%s credential_store=%t auth_scheme=%s",
		logValue(authConfig.Username, "unknown"),
		logValue(authConfig.Source, "unknown"),
		authConfig.CredentialStore,
		agentAuthSchemeForLog(authConfig.Authorization),
	)
	log.Printf(
		"Agent runner config: runner_id=%s workspace=%s agent_type=%s codex_mode=%s claude_mode=%s poll_interval=%s once=%t custom_command=%t command_args=%d pid=%d version=%s",
		logValue(cfg.RunnerID, "pending"),
		logValue(cfg.RootDir, "unknown"),
		firstNonEmpty(strings.TrimSpace(cfg.AgentType), "codex"),
		logValue(cfg.CodexMode, "auto"),
		logValue(cfg.ClaudeMode, "auto"),
		cfg.PollInterval,
		cfg.Once,
		len(cfg.Command) > 0,
		len(cfg.Command),
		os.Getpid(),
		versionString(),
	)
}

func logValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func agentAuthSchemeForLog(authorization string) string {
	authorization = strings.TrimSpace(authorization)
	switch {
	case authorization == "":
		return "none"
	case strings.HasPrefix(strings.ToLower(authorization), "bearer "):
		return "bearer"
	case strings.HasPrefix(strings.ToLower(authorization), "user "):
		return "user"
	default:
		return "custom"
	}
}

type agentSupervisorAuthRefresher struct {
	mu        sync.Mutex
	cli       *CLI
	auth      cliAuth
	nextCheck time.Time
}

func newAgentSupervisorAuthRefresher(cli *CLI, authConfig cliAuth) *agentSupervisorAuthRefresher {
	return &agentSupervisorAuthRefresher{cli: cli, auth: authConfig}
}

func (r *agentSupervisorAuthRefresher) context(ctx context.Context) (context.Context, error) {
	if r == nil {
		return ctx, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	authConfig := r.auth
	if authConfig.CredentialStore {
		now := time.Now()
		if r.nextCheck.IsZero() || !now.Before(r.nextCheck) {
			refreshed, err := ensureCLIAuthReady(replaceCLIAuth(ctx, cliAuth{}), r.cli, authConfig)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(refreshed.Authorization) != strings.TrimSpace(authConfig.Authorization) {
				log.Printf("Refreshed stored auth for local agent runner")
			}
			r.auth = refreshed
			authConfig = refreshed
			r.nextCheck = now.Add(agentSupervisorAuthCheckInterval)
		}
	}
	return replaceCLIAuth(ctx, authConfig), nil
}

type refreshingAgentServiceClient struct {
	agentv1.AgentServiceClient
	auth *agentSupervisorAuthRefresher
}

func refreshingAgentCall[T any](c *refreshingAgentServiceClient, ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	var zero T
	callCtx, err := c.auth.context(ctx)
	if err != nil {
		return zero, err
	}
	return call(callCtx)
}

func (c *refreshingAgentServiceClient) RegisterRunner(ctx context.Context, req *agentv1.RegisterRunnerRequest, opts ...grpc.CallOption) (*agentv1.RegisterRunnerResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.RegisterRunnerResponse, error) {
		return c.AgentServiceClient.RegisterRunner(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) HeartbeatRunner(ctx context.Context, req *agentv1.HeartbeatRunnerRequest, opts ...grpc.CallOption) (*agentv1.HeartbeatRunnerResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.HeartbeatRunnerResponse, error) {
		return c.AgentServiceClient.HeartbeatRunner(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) ListRunners(ctx context.Context, req *agentv1.ListRunnersRequest, opts ...grpc.CallOption) (*agentv1.ListRunnersResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.ListRunnersResponse, error) {
		return c.AgentServiceClient.ListRunners(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) UnregisterRunner(ctx context.Context, req *agentv1.UnregisterRunnerRequest, opts ...grpc.CallOption) (*agentv1.UnregisterRunnerResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.UnregisterRunnerResponse, error) {
		return c.AgentServiceClient.UnregisterRunner(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) ListSessions(ctx context.Context, req *agentv1.ListSessionsRequest, opts ...grpc.CallOption) (*agentv1.ListSessionsResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.ListSessionsResponse, error) {
		return c.AgentServiceClient.ListSessions(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) CreateSession(ctx context.Context, req *agentv1.CreateSessionRequest, opts ...grpc.CallOption) (*agentv1.CreateSessionResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.CreateSessionResponse, error) {
		return c.AgentServiceClient.CreateSession(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) GetSession(ctx context.Context, req *agentv1.GetSessionRequest, opts ...grpc.CallOption) (*agentv1.GetSessionResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.GetSessionResponse, error) {
		return c.AgentServiceClient.GetSession(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) StopSession(ctx context.Context, req *agentv1.StopSessionRequest, opts ...grpc.CallOption) (*agentv1.StopSessionResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.StopSessionResponse, error) {
		return c.AgentServiceClient.StopSession(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) MintToken(ctx context.Context, req *agentv1.MintTokenRequest, opts ...grpc.CallOption) (*agentv1.MintTokenResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.MintTokenResponse, error) {
		return c.AgentServiceClient.MintToken(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) ListEvents(ctx context.Context, req *agentv1.ListEventsRequest, opts ...grpc.CallOption) (*agentv1.ListEventsResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.ListEventsResponse, error) {
		return c.AgentServiceClient.ListEvents(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) AppendEvent(ctx context.Context, req *agentv1.AppendEventRequest, opts ...grpc.CallOption) (*agentv1.AppendEventResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.AppendEventResponse, error) {
		return c.AgentServiceClient.AppendEvent(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) SendInput(ctx context.Context, req *agentv1.SendInputRequest, opts ...grpc.CallOption) (*agentv1.SendInputResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.SendInputResponse, error) {
		return c.AgentServiceClient.SendInput(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) SendInterrupt(ctx context.Context, req *agentv1.SendInterruptRequest, opts ...grpc.CallOption) (*agentv1.SendInterruptResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.SendInterruptResponse, error) {
		return c.AgentServiceClient.SendInterrupt(callCtx, req, opts...)
	})
}

func (c *refreshingAgentServiceClient) ListCapabilities(ctx context.Context, req *agentv1.ListCapabilitiesRequest, opts ...grpc.CallOption) (*agentv1.ListCapabilitiesResponse, error) {
	return refreshingAgentCall(c, ctx, func(callCtx context.Context) (*agentv1.ListCapabilitiesResponse, error) {
		return c.AgentServiceClient.ListCapabilities(callCtx, req, opts...)
	})
}

func runAgentSupervisor(ctx context.Context, cli *CLI, authConfig cliAuth, cfg localAgentSupervisorConfig) (int, error) {
	rootDir, err := resolveAgentWorkspaceRoot(cfg.RootDir)
	if err != nil {
		return 0, err
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	cfg.RootDir = rootDir
	if cfg.RunnerID == "" {
		runnerID, err := ensureAgentRunnerID(rootDir)
		if err != nil {
			return 0, err
		}
		cfg.RunnerID = runnerID
	}
	authRefresher := newAgentSupervisorAuthRefresher(cli, authConfig)
	supervisorCLI := *cli
	supervisorCLI.agentClient = &refreshingAgentServiceClient{
		AgentServiceClient: cli.agentClient,
		auth:               authRefresher,
	}
	authCtx, err := authRefresher.context(ctx)
	if err != nil {
		return 0, err
	}
	logAgentRunnerStartup(authConfig, cfg)
	if err := registerLocalAgentRunner(authCtx, &supervisorCLI, cfg); err != nil {
		return 0, err
	}
	defer func() {
		unregisterCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		authUnregisterCtx, err := authRefresher.context(unregisterCtx)
		if err != nil {
			log.Printf("Warning: failed to refresh auth while unregistering local agent runner %s: %v", cfg.RunnerID, err)
			authUnregisterCtx = unregisterCtx
		}
		if err := unregisterLocalAgentRunner(authUnregisterCtx, &supervisorCLI, cfg.RunnerID); err != nil {
			log.Printf("Warning: failed to unregister local agent runner %s: %v", cfg.RunnerID, err)
		} else {
			log.Printf("Unregistered local agent runner: runner_id=%s", cfg.RunnerID)
		}
	}()

	log.Printf("Polling for assigned agent sessions: runner_id=%s interval=%s", cfg.RunnerID, cfg.PollInterval)
	managed := map[string]*managedAgentSession{}
	completed := 0
	heartbeatOKLogged := false
	lastDiscoveredCount := -1
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	defer func() {
		for _, session := range managed {
			session.cancel()
		}
	}()

	for {
		reaped, reapErr := reapManagedAgentSessions(managed)
		completed += reaped
		if errors.Is(reapErr, errAgentSupervisorRestarting) {
			for _, session := range managed {
				session.cancel()
			}
			return completed, nil
		}
		if cfg.Once && completed > 0 {
			return completed, nil
		}

		authCtx, authErr := authRefresher.context(ctx)
		if authErr != nil {
			log.Printf("Warning: failed to refresh auth for local agent runner %s: %v", cfg.RunnerID, authErr)
			heartbeatOKLogged = false
			select {
			case <-ctx.Done():
				return completed, ctx.Err()
			case <-ticker.C:
				continue
			}
		}

		if heartbeatResp, reRegistered, err := heartbeatOrRegisterLocalAgentRunner(authCtx, &supervisorCLI, cfg); err != nil {
			log.Printf("Warning: failed to heartbeat local agent runner %s: %v", cfg.RunnerID, err)
			heartbeatOKLogged = false
		} else if reRegistered || !heartbeatOKLogged {
			log.Printf("Local agent runner heartbeat accepted: runner_id=%s status=%s heartbeat_interval=%ds", cfg.RunnerID, heartbeatResp.GetRunner().GetStatus(), heartbeatResp.GetHeartbeatIntervalSec())
			heartbeatOKLogged = true
		}

		sessions, discoverErr := discoverLocalAgentSessions(authCtx, &supervisorCLI, cfg.RunnerID)
		if discoverErr != nil {
			log.Printf("Warning: failed to discover local agent sessions: %v", discoverErr)
		} else if len(sessions) != lastDiscoveredCount {
			log.Printf("Discovered assigned active agent sessions: runner_id=%s count=%d", cfg.RunnerID, len(sessions))
			lastDiscoveredCount = len(sessions)
		}

		active := make(map[string]struct{}, len(sessions))
		for _, discovered := range sessions {
			session := discovered.session
			if session == nil || !agentSessionStateActive(session.GetState()) {
				continue
			}
			sessionID := strings.TrimSpace(session.GetSessionId())
			if sessionID == "" || session.GetProvider() != "local" {
				continue
			}
			active[sessionID] = struct{}{}
			if _, ok := managed[sessionID]; ok {
				continue
			}
			runCfg, checkoutErr := localRunConfigForDiscoveredSession(authCtx, &supervisorCLI, cfg, discovered)
			if checkoutErr != nil {
				_ = appendAgentError(authCtx, &supervisorCLI, sessionID, "LOCAL_AGENT_CHECKOUT_FAILED", checkoutErr.Error())
				log.Printf("Warning: failed to checkout slice for agent session %s: %v", sessionID, checkoutErr)
				continue
			}
			if err := appendLocalRunnerAttached(authCtx, &supervisorCLI, cfg, runCfg); err != nil {
				log.Printf("Warning: failed to append local runner metadata for agent session %s: %v", sessionID, err)
			}
			childCtx, cancel := context.WithCancel(ctx)
			done := make(chan agentSessionRunResult, 1)
			managed[sessionID] = &managedAgentSession{cancel: cancel, done: done}
			go func() {
				n, runErr := runAgentBridge(childCtx, &supervisorCLI, runCfg)
				done <- agentSessionRunResult{sessionID: runCfg.SessionID, completed: n, err: runErr}
			}()
			log.Printf("Tracking agent session %s in %s", sessionID, runCfg.CWD)
		}

		for sessionID, session := range managed {
			if _, ok := active[sessionID]; !ok {
				session.cancel()
			}
		}

		select {
		case <-ctx.Done():
			return completed, ctx.Err()
		case <-ticker.C:
		}
	}
}

func reapManagedAgentSessions(managed map[string]*managedAgentSession) (int, error) {
	completed := 0
	for sessionID, session := range managed {
		select {
		case result := <-session.done:
			if errors.Is(result.err, errAgentSupervisorRestarting) {
				completed += result.completed
				delete(managed, sessionID)
				return completed, errAgentSupervisorRestarting
			}
			if result.err != nil && !errors.Is(result.err, context.Canceled) {
				log.Printf("Warning: agent session %s runner exited: %v", sessionID, result.err)
			}
			completed += result.completed
			delete(managed, sessionID)
		default:
		}
	}
	return completed, nil
}

func discoverLocalAgentSessions(ctx context.Context, cli *CLI, runnerID string) ([]discoveredAgentSession, error) {
	const pageSize = 200
	runnerID = strings.TrimSpace(runnerID)
	var out []discoveredAgentSession
	for offset := int32(0); ; offset += pageSize {
		resp, err := cli.sliceClient.ListSlices(ctx, &slicev1.ListSlicesRequest{Limit: pageSize, Offset: offset})
		if err != nil {
			return out, err
		}
		for _, slice := range resp.GetSlices() {
			sessions, err := cli.agentClient.ListSessions(ctx, &agentv1.ListSessionsRequest{SliceId: slice.GetSliceId(), Limit: 200})
			if err != nil {
				log.Printf("Warning: failed to list agent sessions for slice %s: %v", slice.GetSliceId(), err)
				continue
			}
			for _, session := range sessions.GetSessions() {
				if session.GetProvider() == "local" && agentSessionStateActive(session.GetState()) && strings.TrimSpace(session.GetRunnerId()) == runnerID {
					out = append(out, discoveredAgentSession{session: session, slice: slice})
				}
			}
		}
		if len(resp.GetSlices()) == 0 || offset+int32(len(resp.GetSlices())) >= resp.GetTotal() {
			break
		}
	}
	return out, nil
}

func appendLocalRunnerAttached(ctx context.Context, cli *CLI, supervisorCfg localAgentSupervisorConfig, runCfg localAgentRunConfig) error {
	hostName, _ := os.Hostname()
	payload := map[string]any{
		"status":         "attached",
		"runner_id":      strings.TrimSpace(supervisorCfg.RunnerID),
		"host_name":      strings.TrimSpace(hostName),
		"pid":            os.Getpid(),
		"workspace_root": strings.TrimSpace(supervisorCfg.RootDir),
		"running_dir":    strings.TrimSpace(runCfg.CWD),
		"checkout_dir":   strings.TrimSpace(runCfg.CWD),
		"checkout_command": fmt.Sprintf(
			"gs slice checkout %s --here",
			strings.TrimSpace(runCfg.SliceID),
		),
		"agent_type":  strings.TrimSpace(runCfg.AgentType),
		"codex_mode":  strings.TrimSpace(runCfg.CodexMode),
		"claude_mode": strings.TrimSpace(runCfg.ClaudeMode),
		"attached_at": time.Now().UTC().Format(time.RFC3339),
	}
	if len(runCfg.Command) > 0 {
		payload["command"] = append([]string(nil), runCfg.Command...)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = cli.agentClient.AppendEvent(ctx, &agentv1.AppendEventRequest{
		SessionId: runCfg.SessionID,
		Stream:    "status",
		Type:      "local_runner_attached",
		Payload:   raw,
	})
	return err
}

func parseLocalRunnerRestartRequest(payload []byte) localRunnerRestartRequest {
	var request localRunnerRestartRequest
	if len(payload) == 0 {
		return request
	}
	_ = json.Unmarshal(payload, &request)
	return request
}

func requestLocalAgentSupervisorRestart(ctx context.Context, cli *CLI, sessionID, rootDir string, requestedSeq uint64, request localRunnerRestartRequest) error {
	action := "restart"
	if request.Upgrade {
		action = "upgrade_restart"
	}
	startPayload := map[string]any{
		"status":        "started",
		"action":        action,
		"upgrade":       request.Upgrade,
		"pid":           os.Getpid(),
		"requested_seq": requestedSeq,
		"started_at":    time.Now().UTC().Format(time.RFC3339),
	}
	if reason := strings.TrimSpace(request.Reason); reason != "" {
		startPayload["reason"] = reason
	}
	_ = appendLocalRunnerControlEvent(ctx, cli, sessionID, "local_runner_restart_started", startPayload)

	if request.Upgrade {
		if err := runLocalAgentUpgrade(ctx); err != nil {
			_ = appendLocalRunnerControlEvent(ctx, cli, sessionID, "local_runner_restart_failed", map[string]any{
				"status":        "failed",
				"action":        action,
				"upgrade":       true,
				"requested_seq": requestedSeq,
				"message":       err.Error(),
				"failed_at":     time.Now().UTC().Format(time.RFC3339),
			})
			return err
		}
		_ = appendLocalRunnerControlEvent(ctx, cli, sessionID, "local_runner_upgrade_completed", map[string]any{
			"status":        "updated",
			"action":        action,
			"requested_seq": requestedSeq,
			"completed_at":  time.Now().UTC().Format(time.RFC3339),
		})
	}

	replacementPID, err := startLocalAgentReplacement(rootDir)
	if err != nil {
		_ = appendLocalRunnerControlEvent(ctx, cli, sessionID, "local_runner_restart_failed", map[string]any{
			"status":        "failed",
			"action":        action,
			"upgrade":       request.Upgrade,
			"requested_seq": requestedSeq,
			"message":       err.Error(),
			"failed_at":     time.Now().UTC().Format(time.RFC3339),
		})
		return err
	}

	_ = appendLocalRunnerControlEvent(ctx, cli, sessionID, "local_runner_restart_spawned", map[string]any{
		"status":          "spawned",
		"action":          action,
		"upgrade":         request.Upgrade,
		"pid":             os.Getpid(),
		"replacement_pid": replacementPID,
		"requested_seq":   requestedSeq,
		"spawned_at":      time.Now().UTC().Format(time.RFC3339),
	})
	return errAgentSupervisorRestarting
}

func appendLocalRunnerControlEvent(ctx context.Context, cli *CLI, sessionID, eventType string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = cli.agentClient.AppendEvent(ctx, &agentv1.AppendEventRequest{
		SessionId: sessionID,
		Stream:    "control",
		Type:      eventType,
		Payload:   raw,
	})
	return err
}

func runLocalAgentUpgrade(ctx context.Context) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	installDir, err := currentExecutableDir()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, "update", "--install-dir", installDir, "--json")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func startLocalAgentReplacement(rootDir string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(executable, os.Args[1:]...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		return 0, err
	}
	writeAgentSupervisorPIDFile(rootDir, pid)
	return pid, nil
}

func writeAgentSupervisorPIDFile(rootDir string, pid int) {
	root := strings.TrimSpace(rootDir)
	if root == "" || pid <= 0 {
		return
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		log.Printf("Warning: failed to resolve agent runner pid directory: %v", err)
		return
	}
	pidDir := filepath.Join(abs, agentWorkspaceStateDir)
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		log.Printf("Warning: failed to create agent runner pid directory: %v", err)
		return
	}
	pidFile := filepath.Join(pidDir, "agent.pid")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		log.Printf("Warning: failed to write agent pid file: %v", err)
	}
}

func ensureAgentRunnerID(rootDir string) (string, error) {
	stateDir := filepath.Join(rootDir, agentWorkspaceStateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(stateDir, "runner_id")
	if raw, err := os.ReadFile(path); err == nil {
		if runnerID := strings.TrimSpace(string(raw)); runnerID != "" {
			return runnerID, nil
		}
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	runnerID := "agr_" + hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(runnerID+"\n"), 0o600); err != nil {
		return "", err
	}
	return runnerID, nil
}

func registerLocalAgentRunner(ctx context.Context, cli *CLI, cfg localAgentSupervisorConfig) error {
	hostName, _ := os.Hostname()
	capabilities, err := localAgentRunnerCapabilities(cfg)
	if err != nil {
		return err
	}
	log.Printf(
		"Registering local agent runner: runner_id=%s provider=local agent_type=%s host=%s pid=%d workspace=%s",
		logValue(cfg.RunnerID, "pending"),
		firstNonEmpty(strings.TrimSpace(cfg.AgentType), "codex"),
		logValue(hostName, "unknown"),
		os.Getpid(),
		logValue(cfg.RootDir, "unknown"),
	)
	resp, err := cli.agentClient.RegisterRunner(ctx, &agentv1.RegisterRunnerRequest{
		RunnerId:      strings.TrimSpace(cfg.RunnerID),
		Provider:      "local",
		AgentType:     firstNonEmpty(strings.TrimSpace(cfg.AgentType), "codex"),
		HostName:      strings.TrimSpace(hostName),
		Pid:           int32(os.Getpid()),
		WorkspaceRoot: strings.TrimSpace(cfg.RootDir),
		Version:       versionString(),
		Capabilities:  capabilities,
	})
	if err != nil {
		return err
	}
	if resp.GetRunner().GetRunnerId() != "" && strings.TrimSpace(cfg.RunnerID) != resp.GetRunner().GetRunnerId() {
		return fmt.Errorf("server returned different runner id %s", resp.GetRunner().GetRunnerId())
	}
	runner := resp.GetRunner()
	log.Printf(
		"Registered local agent runner: runner_id=%s provider=%s agent_type=%s status=%s host=%s pid=%d workspace=%s heartbeat_interval=%ds",
		logValue(runner.GetRunnerId(), cfg.RunnerID),
		logValue(runner.GetProvider(), "local"),
		logValue(runner.GetAgentType(), firstNonEmpty(strings.TrimSpace(cfg.AgentType), "codex")),
		logValue(runner.GetStatus(), "unknown"),
		logValue(runner.GetHostName(), hostName),
		runner.GetPid(),
		logValue(runner.GetWorkspaceRoot(), cfg.RootDir),
		resp.GetHeartbeatIntervalSec(),
	)
	return nil
}

func heartbeatLocalAgentRunner(ctx context.Context, cli *CLI, cfg localAgentSupervisorConfig) (*agentv1.HeartbeatRunnerResponse, error) {
	hostName, _ := os.Hostname()
	capabilities, err := localAgentRunnerCapabilities(cfg)
	if err != nil {
		return nil, err
	}
	resp, err := cli.agentClient.HeartbeatRunner(ctx, &agentv1.HeartbeatRunnerRequest{
		RunnerId:      strings.TrimSpace(cfg.RunnerID),
		Status:        "online",
		HostName:      strings.TrimSpace(hostName),
		Pid:           int32(os.Getpid()),
		WorkspaceRoot: strings.TrimSpace(cfg.RootDir),
		Capabilities:  capabilities,
	})
	return resp, err
}

func heartbeatOrRegisterLocalAgentRunner(ctx context.Context, cli *CLI, cfg localAgentSupervisorConfig) (*agentv1.HeartbeatRunnerResponse, bool, error) {
	resp, err := heartbeatLocalAgentRunner(ctx, cli, cfg)
	if err == nil {
		return resp, false, nil
	}
	if status.Code(err) != codes.NotFound {
		return nil, false, err
	}

	log.Printf("Local agent runner %s is missing on the server; re-registering", cfg.RunnerID)
	if registerErr := registerLocalAgentRunner(ctx, cli, cfg); registerErr != nil {
		return nil, true, fmt.Errorf("runner missing on server; re-register failed: %w", registerErr)
	}
	resp, err = heartbeatLocalAgentRunner(ctx, cli, cfg)
	if err != nil {
		return nil, true, fmt.Errorf("runner re-registered but heartbeat failed: %w", err)
	}
	return resp, true, nil
}

func unregisterLocalAgentRunner(ctx context.Context, cli *CLI, runnerID string) error {
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil
	}
	_, err := cli.agentClient.UnregisterRunner(ctx, &agentv1.UnregisterRunnerRequest{
		RunnerId: runnerID,
		Reason:   "local runner stopped",
	})
	return err
}

func localAgentRunnerCapabilities(cfg localAgentSupervisorConfig) ([]byte, error) {
	payload := map[string]any{
		"agent_type":                  firstNonEmpty(strings.TrimSpace(cfg.AgentType), "codex"),
		"codex_mode":                  strings.TrimSpace(cfg.CodexMode),
		"claude_mode":                 strings.TrimSpace(cfg.ClaudeMode),
		"concurrent_sessions":         true,
		"checkout_per_session":        true,
		"session_checkout_strategy":   "slice_checkout",
		"session_checkout_dir_format": "<slice>-<session>",
	}
	if len(cfg.Command) > 0 {
		payload["command"] = append([]string(nil), cfg.Command...)
	}
	return json.Marshal(payload)
}

func localRunConfigForDiscoveredSession(ctx context.Context, cli *CLI, cfg localAgentSupervisorConfig, discovered discoveredAgentSession) (localAgentRunConfig, error) {
	session := discovered.session
	if session == nil {
		return localAgentRunConfig{}, fmt.Errorf("session is required")
	}
	checkoutDir, err := ensureAgentSessionCheckout(ctx, cli, cfg.RootDir, discovered)
	if err != nil {
		return localAgentRunConfig{}, err
	}
	agentType := firstNonEmpty(strings.TrimSpace(session.GetAgentType()), strings.TrimSpace(cfg.AgentType), "codex")
	return localAgentRunConfig{
		SessionID:    strings.TrimSpace(session.GetSessionId()),
		SliceID:      strings.TrimSpace(session.GetSliceId()),
		AgentType:    agentType,
		RootDir:      strings.TrimSpace(cfg.RootDir),
		CWD:          checkoutDir,
		CodexMode:    strings.TrimSpace(cfg.CodexMode),
		ClaudeMode:   strings.TrimSpace(cfg.ClaudeMode),
		Command:      append([]string(nil), cfg.Command...),
		PollInterval: cfg.PollInterval,
		Once:         cfg.Once,
	}, nil
}

func ensureAgentSessionCheckout(ctx context.Context, cli *CLI, rootDir string, discovered discoveredAgentSession) (string, error) {
	session := discovered.session
	if session == nil {
		return "", fmt.Errorf("session is required")
	}
	targetRoot := filepath.Join(rootDir, agentSessionCheckoutDirName(discovered))
	if index, err := readCheckoutIndex(targetRoot); err == nil && index != nil && strings.TrimSpace(index.SliceID) == strings.TrimSpace(session.GetSliceId()) {
		return targetRoot, nil
	}
	if err := prepareCheckoutTargetRoot(targetRoot); err != nil {
		return "", err
	}
	log.Printf(
		"Checking out slice for agent session: session_id=%s slice_id=%s target_dir=%s command=%q",
		strings.TrimSpace(session.GetSessionId()),
		strings.TrimSpace(session.GetSliceId()),
		targetRoot,
		fmt.Sprintf("gs slice checkout %s --here", strings.TrimSpace(session.GetSliceId())),
	)
	checkoutResult, err := fetchAndMaterializeSliceCheckout(ctx, cli, session.GetSliceId(), "HEAD", targetRoot, false, nil)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(targetRoot, ".gs"), 0o755); err != nil {
		return "", err
	}
	if err := writeSliceIDConfigAt(targetRoot, session.GetSliceId()); err != nil {
		return "", err
	}
	nextCheckoutIndex, err := buildCheckoutIndex(targetRoot, session.GetSliceId(), checkoutResult.Manifest)
	if err != nil {
		return "", err
	}
	if err := writeCheckoutIndex(targetRoot, nextCheckoutIndex); err != nil {
		return "", err
	}
	if err := ensureLocalSliceSearchArtifact(ctx, cli, targetRoot, session.GetSliceId(), checkoutResult.Manifest); err != nil {
		log.Printf("Warning: failed to prepare local slice search artifact: %v", err)
	}
	if err := resetDirtyTracker(targetRoot, nextCheckoutIndex); err != nil {
		log.Printf("Warning: failed to start dirty tracker: %v", err)
	}
	if err := registerCheckout(targetRoot, session.GetSliceId(), checkoutResult.Manifest.CommitHash); err != nil {
		log.Printf("Warning: failed to register checkout path: %v", err)
	}
	return targetRoot, nil
}

func agentSessionCheckoutDirName(discovered discoveredAgentSession) string {
	label := ""
	if discovered.slice != nil {
		label = firstNonEmpty(discovered.slice.GetSlug(), discovered.slice.GetName())
	}
	session := discovered.session
	if session != nil {
		label = firstNonEmpty(label, session.GetSliceId())
	}
	label = safeAgentCheckoutNamePattern.ReplaceAllString(strings.TrimSpace(label), "-")
	label = strings.Trim(strings.TrimSpace(label), ".-")
	if label == "" {
		label = "slice"
	}
	sessionID := ""
	if session != nil {
		sessionID = strings.TrimPrefix(strings.TrimSpace(session.GetSessionId()), "sess_")
	}
	sessionID = safeAgentCheckoutNamePattern.ReplaceAllString(sessionID, "-")
	if sessionID == "" {
		sessionID = strconv.FormatInt(time.Now().Unix(), 10)
	}
	return label + "-" + sessionID
}

func agentSessionStateActive(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "creating", "starting", "running", "idle", "stopping":
		return true
	default:
		return false
	}
}
