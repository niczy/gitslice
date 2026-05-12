package gscli

import (
	"context"
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
	"syscall"
	"time"

	agentv1 "github.com/niczy/gitslice/proto/agent"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

const agentWorkspaceStateDir = ".gitslice-agent"

var safeAgentCheckoutNamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type localAgentSupervisorConfig struct {
	RootDir      string
	AgentType    string
	CodexMode    string
	ClaudeMode   string
	Command      []string
	PollInterval time.Duration
	Once         bool
	LogFile      string
}

type agentSupervisorStartOutput struct {
	Status  string `json:"status"`
	PID     int    `json:"pid"`
	CWD     string `json:"cwd"`
	LogFile string `json:"log_file"`
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
		Status:  "started",
		PID:     pid,
		CWD:     rootDir,
		LogFile: logFile,
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
	if strings.HasPrefix(strings.TrimSpace(authConfig.Authorization), "Bearer ") {
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

func runAgentSupervisor(ctx context.Context, cli *CLI, cfg localAgentSupervisorConfig) (int, error) {
	rootDir, err := resolveAgentWorkspaceRoot(cfg.RootDir)
	if err != nil {
		return 0, err
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	cfg.RootDir = rootDir
	managed := map[string]*managedAgentSession{}
	completed := 0
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

		sessions, discoverErr := discoverLocalAgentSessions(ctx, cli)
		if discoverErr != nil {
			log.Printf("Warning: failed to discover local agent sessions: %v", discoverErr)
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
			runCfg, checkoutErr := localRunConfigForDiscoveredSession(ctx, cli, cfg, discovered)
			if checkoutErr != nil {
				_ = appendAgentError(ctx, cli, sessionID, "LOCAL_AGENT_CHECKOUT_FAILED", checkoutErr.Error())
				log.Printf("Warning: failed to checkout slice for agent session %s: %v", sessionID, checkoutErr)
				continue
			}
			if err := appendLocalRunnerAttached(ctx, cli, cfg, runCfg); err != nil {
				log.Printf("Warning: failed to append local runner metadata for agent session %s: %v", sessionID, err)
			}
			childCtx, cancel := context.WithCancel(ctx)
			done := make(chan agentSessionRunResult, 1)
			managed[sessionID] = &managedAgentSession{cancel: cancel, done: done}
			go func() {
				n, runErr := runAgentBridge(childCtx, cli, runCfg)
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

func discoverLocalAgentSessions(ctx context.Context, cli *CLI) ([]discoveredAgentSession, error) {
	const pageSize = 200
	var out []discoveredAgentSession
	for offset := int32(0); ; offset += pageSize {
		resp, err := cli.sliceClient.ListSlices(ctx, &slicev1.ListSlicesRequest{Limit: pageSize, Offset: offset})
		if err != nil {
			return out, err
		}
		for _, slice := range resp.GetSlices() {
			sessions, err := cli.agentClient.ListSessions(ctx, &agentv1.ListSessionsRequest{SliceId: slice.GetSliceId(), Limit: 20})
			if err != nil {
				log.Printf("Warning: failed to list agent sessions for slice %s: %v", slice.GetSliceId(), err)
				continue
			}
			for _, session := range sessions.GetSessions() {
				if session.GetProvider() == "local" && agentSessionStateActive(session.GetState()) {
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
		"host_name":      strings.TrimSpace(hostName),
		"pid":            os.Getpid(),
		"workspace_root": strings.TrimSpace(supervisorCfg.RootDir),
		"running_dir":    strings.TrimSpace(runCfg.CWD),
		"checkout_dir":   strings.TrimSpace(runCfg.CWD),
		"agent_type":     strings.TrimSpace(runCfg.AgentType),
		"codex_mode":     strings.TrimSpace(runCfg.CodexMode),
		"claude_mode":    strings.TrimSpace(runCfg.ClaudeMode),
		"attached_at":    time.Now().UTC().Format(time.RFC3339),
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
	if len(sessionID) > 12 {
		sessionID = sessionID[:12]
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
