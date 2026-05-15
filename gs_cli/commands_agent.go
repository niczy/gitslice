package gscli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/niczy/gitslice/internal/agentsession"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

func newAgentCommand() *cobra.Command {
	cmd := newAuthenticatedCobraCommand("agent <command> [options]", "Start and run local coding agents", 0, func(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
		handleAgentCommand(ctx, cli, authConfig, args)
	})
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printAgentHelp()
	})
	return cmd
}

func handleAgentCommand(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	if len(args) == 0 {
		printAgentHelp()
		return
	}
	switch args[0] {
	case "start":
		handleAgentStart(ctx, cli, authConfig, args[1:])
	case "run":
		handleAgentRun(ctx, cli, authConfig, args[1:])
	case "list":
		handleAgentList(ctx, cli, args[1:])
	case "input":
		handleAgentInput(ctx, cli, args[1:])
	case "interrupt":
		handleAgentInterrupt(ctx, cli, args[1:])
	case "stop":
		handleAgentStop(ctx, cli, args[1:])
	default:
		commandFatal("INVALID_ARGUMENT", fmt.Sprintf("Unknown agent command: %s", args[0]), false, "gs agent --help")
	}
}

func handleAgentStart(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("agent start")
	agentType := fs.String("agent", "all", "Agent type to run: codex, claude, or all")
	cwd := fs.String("cwd", ".", "Working directory for tracked agent sessions")
	dir := fs.String("dir", "", "Alias for --cwd")
	codexMode := fs.String("codex-mode", "app-server", "Codex runner mode: app-server")
	claudeMode := fs.String("claude-mode", "auto", "Claude runner mode: auto or stream-json")
	pollIntervalRaw := fs.String("poll-interval", "1s", "Event polling interval")
	logFile := fs.String("log-file", "", "Background runner log file")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	pollInterval, err := time.ParseDuration(strings.TrimSpace(*pollIntervalRaw))
	if err != nil || pollInterval <= 0 {
		commandFatal("INVALID_ARGUMENT", "--poll-interval must be a positive duration", false, "")
		return
	}
	codexModeValue := normalizedCodexMode(*codexMode)
	if codexModeValue == "" {
		commandFatal("INVALID_ARGUMENT", "--codex-mode must be app-server", false, "")
		return
	}
	agentTypes, agentCLIStatus, err := resolveRunnableLocalAgentTypes(*agentType, fs.Args())
	if err != nil {
		commandFatal("INVALID_ARGUMENT", err.Error(), false, "")
		return
	}
	rootDir := *cwd
	if strings.TrimSpace(*dir) != "" {
		rootDir = *dir
	}
	result, err := startAgentSupervisorBackground(ctx, cli, authConfig, localAgentSupervisorConfig{
		RootDir:      strings.TrimSpace(rootDir),
		AgentType:    formatLocalAgentTypes(agentTypes),
		CodexMode:    codexModeValue,
		ClaudeMode:   strings.TrimSpace(*claudeMode),
		Command:      append([]string(nil), fs.Args()...),
		PollInterval: pollInterval,
		LogFile:      strings.TrimSpace(*logFile),
	})
	if err != nil {
		commandFatalf("AGENT_START_FAILED", true, "", "Failed to start local agent runner: %v", err)
	}
	result.AgentTypes = append([]string(nil), agentTypes...)
	result.DefaultAgentType = defaultLocalAgentType(formatLocalAgentTypes(agentTypes))
	result.AgentCLIStatus = agentCLIStatus
	if jsonEnabled {
		writeJSONOutput(result)
		return
	}
	fmt.Printf("Agent runner started: pid %d\n", result.PID)
	fmt.Printf("Runner: %s\n", result.RunnerID)
	fmt.Printf("Agent types: %s (default %s)\n", strings.Join(agentTypes, ", "), defaultLocalAgentType(formatLocalAgentTypes(agentTypes)))
	printLocalAgentCLIStatus(agentCLIStatus)
	fmt.Printf("Workspace: %s\n", result.CWD)
	fmt.Printf("Log: %s\n", result.LogFile)
}

func handleAgentRun(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, once := consumeBoolFlag(args, "once")
	fs := newCommandFlagSet("agent run")
	agentType := fs.String("agent", "all", "Agent type to run: codex, claude, or all")
	cwd := fs.String("cwd", ".", "Working directory for tracked agent sessions")
	dir := fs.String("dir", "", "Alias for --cwd")
	codexMode := fs.String("codex-mode", "app-server", "Codex runner mode: app-server")
	claudeMode := fs.String("claude-mode", "auto", "Claude runner mode: auto or stream-json")
	pollIntervalRaw := fs.String("poll-interval", "1s", "Event polling interval")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	commandArgs := append([]string(nil), fs.Args()...)
	pollInterval, err := time.ParseDuration(strings.TrimSpace(*pollIntervalRaw))
	if err != nil || pollInterval <= 0 {
		commandFatal("INVALID_ARGUMENT", "--poll-interval must be a positive duration", false, "")
		return
	}
	codexModeValue := normalizedCodexMode(*codexMode)
	if codexModeValue == "" {
		commandFatal("INVALID_ARGUMENT", "--codex-mode must be app-server", false, "")
		return
	}
	agentTypes, agentCLIStatus, err := resolveRunnableLocalAgentTypes(*agentType, commandArgs)
	if err != nil {
		commandFatal("INVALID_ARGUMENT", err.Error(), false, "")
		return
	}
	rootDir := *cwd
	if strings.TrimSpace(*dir) != "" {
		rootDir = *dir
	}
	rootDir, err = resolveAgentWorkspaceRoot(rootDir)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid working directory: %v", err)
	}
	runnerID, err := ensureAgentRunnerID(rootDir)
	if err != nil {
		commandFatalf("AGENT_RUN_FAILED", true, "", "Failed to initialize local runner id: %v", err)
	}
	if !jsonEnabled {
		fmt.Printf("Tracking local agent sessions in %s. Press Ctrl-C to stop the local runner.\n", rootDir)
		fmt.Printf("Runner: %s\n", runnerID)
		fmt.Printf("Agent types: %s (default %s)\n", strings.Join(agentTypes, ", "), defaultLocalAgentType(formatLocalAgentTypes(agentTypes)))
		printLocalAgentCLIStatus(agentCLIStatus)
	}
	completed, err := runAgentSupervisor(ctx, cli, authConfig, localAgentSupervisorConfig{
		RootDir:      rootDir,
		RunnerID:     runnerID,
		AgentType:    formatLocalAgentTypes(agentTypes),
		CodexMode:    codexModeValue,
		ClaudeMode:   strings.TrimSpace(*claudeMode),
		Command:      commandArgs,
		PollInterval: pollInterval,
		Once:         once,
	})
	if err != nil {
		commandFatalf("AGENT_RUN_FAILED", true, "", "Local agent runner failed: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(map[string]any{
			"cwd":              rootDir,
			"runner_id":        runnerID,
			"agent_type":       defaultLocalAgentType(formatLocalAgentTypes(agentTypes)),
			"agent_types":      agentTypes,
			"agent_cli_status": agentCLIStatus,
			"completed_inputs": completed,
		})
	}
}

func handleAgentList(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("agent list")
	includeOffline := fs.Bool("all", false, "Include offline local agent runners")
	includeOfflineAlias := fs.Bool("include-offline", false, "Include offline local agent runners")
	limit := fs.Int("limit", 50, "Maximum runners")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	if fs.NArg() != 0 {
		commandUsage("Usage: gs agent list [--all] [--limit 50] [--json]")
		return
	}
	resp, err := cli.agentClient.ListRunners(ctx, &agentv1.ListRunnersRequest{
		Limit:          int32(*limit),
		IncludeOffline: *includeOffline || *includeOfflineAlias,
	})
	if err != nil {
		commandFatalf("AGENT_LIST_FAILED", true, "", "Failed to list local agent runners: %v", err)
	}
	if jsonRequested || *jsonOutput {
		writeJSONOutput(resp)
		return
	}
	runners := resp.GetRunners()
	if len(runners) == 0 {
		fmt.Println("No local agent runners found.")
		return
	}
	for _, runner := range runners {
		fmt.Printf(
			"%s  %s  agent=%s  host=%s  pid=%d  workspace=%s  last_heartbeat=%s\n",
			runner.GetRunnerId(),
			runner.GetStatus(),
			firstNonEmpty(runner.GetAgentType(), "agent"),
			firstNonEmpty(runner.GetHostName(), "local"),
			runner.GetPid(),
			firstNonEmpty(runner.GetWorkspaceRoot(), "-"),
			firstNonEmpty(runner.GetLastHeartbeatAt(), "-"),
		)
	}
}

func handleAgentInput(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("agent input")
	parseCommandFlags(fs, args)
	if fs.NArg() < 2 {
		commandUsage("Usage: gs agent input <session-id> <text>")
		return
	}
	resp, err := cli.agentClient.SendInput(ctx, &agentv1.SendInputRequest{
		SessionId: strings.TrimSpace(fs.Arg(0)),
		Text:      strings.TrimSpace(strings.Join(fs.Args()[1:], " ")),
	})
	if err != nil {
		commandFatalf("AGENT_INPUT_FAILED", true, "", "Failed to send agent input: %v", err)
	}
	writeJSONOutput(resp)
}

func handleAgentInterrupt(ctx context.Context, cli *CLI, args []string) {
	fs := newCommandFlagSet("agent interrupt")
	reason := fs.String("reason", "user interrupt", "Interrupt reason")
	parseCommandFlags(fs, args)
	if fs.NArg() != 1 {
		commandUsage("Usage: gs agent interrupt <session-id> [--reason <reason>]")
		return
	}
	resp, err := cli.agentClient.SendInterrupt(ctx, &agentv1.SendInterruptRequest{SessionId: strings.TrimSpace(fs.Arg(0)), Reason: strings.TrimSpace(*reason)})
	if err != nil {
		commandFatalf("AGENT_INTERRUPT_FAILED", true, "", "Failed to interrupt agent: %v", err)
	}
	writeJSONOutput(resp)
}

func handleAgentStop(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("agent stop")
	reason := fs.String("reason", "user stop", "Stop reason")
	runnerID := fs.String("runner", "", "Local agent runner ID to stop")
	cwd := fs.String("cwd", "", "Workspace directory for the local agent runner")
	dir := fs.String("dir", "", "Alias for --cwd")
	stopAll := fs.Bool("all", false, "Stop all online local agent runners on this host")
	force := fs.Bool("force", false, "Force-kill the runner process if it does not exit after SIGTERM")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() > 1 {
		commandUsage("Usage: gs agent stop [<session-id>|<runner-id>] [--runner <runner-id>] [--cwd <path>] [--all] [--reason <reason>] [--json]")
		return
	}
	rootDir := strings.TrimSpace(*cwd)
	if strings.TrimSpace(*dir) != "" {
		rootDir = strings.TrimSpace(*dir)
	}
	hasRunnerSelector := *stopAll || strings.TrimSpace(*runnerID) != "" || rootDir != ""
	if fs.NArg() == 1 {
		arg := strings.TrimSpace(fs.Arg(0))
		if hasRunnerSelector {
			commandUsage("Usage: gs agent stop [<session-id>|<runner-id>] [--runner <runner-id>] [--cwd <path>] [--all] [--reason <reason>] [--json]")
			return
		}
		if strings.HasPrefix(arg, "agr_") {
			*runnerID = arg
			hasRunnerSelector = true
		} else {
			resp, err := stopAgentSession(ctx, cli, arg, *reason)
			if err != nil {
				commandFatalf("AGENT_STOP_FAILED", true, "", "Failed to stop agent session: %v", err)
			}
			writeJSONOutput(resp)
			return
		}
	}
	if *stopAll && (strings.TrimSpace(*runnerID) != "" || rootDir != "") {
		commandUsage("Usage: gs agent stop --all [--force] [--reason <reason>] [--json]")
		return
	}
	if !hasRunnerSelector {
		rootDir = "."
	}
	output, err := stopLocalAgentRunners(ctx, cli, localAgentRunnerStopOptions{
		RunnerID: strings.TrimSpace(*runnerID),
		RootDir:  rootDir,
		All:      *stopAll,
		Force:    *force,
		Reason:   strings.TrimSpace(*reason),
	})
	if err != nil {
		commandFatalf("AGENT_STOP_FAILED", true, "", "Failed to stop local agent runner: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(output)
		return
	}
	if len(output.Runners) == 0 {
		fmt.Println("No local agent runners matched.")
		return
	}
	for _, runner := range output.Runners {
		fmt.Printf("%s  %s  pid=%d  workspace=%s\n", runner.RunnerID, runner.Status, runner.PID, firstNonEmpty(runner.WorkspaceRoot, "-"))
	}
}

type localAgentRunnerStopOptions struct {
	RunnerID string
	RootDir  string
	All      bool
	Force    bool
	Reason   string
}

type localAgentRunnerStopTarget struct {
	RunnerID      string
	PID           int
	WorkspaceRoot string
	HostName      string
	Status        string
}

type localAgentRunnerStopOutput struct {
	Status  string                       `json:"status"`
	Runners []localAgentRunnerStopResult `json:"runners"`
}

type localAgentRunnerStopResult struct {
	RunnerID       string `json:"runner_id"`
	Status         string `json:"status"`
	PID            int    `json:"pid,omitempty"`
	WorkspaceRoot  string `json:"workspace_root,omitempty"`
	ProcessStopped bool   `json:"process_stopped"`
	Unregistered   bool   `json:"unregistered"`
	Message        string `json:"message,omitempty"`
}

func stopLocalAgentRunners(ctx context.Context, cli *CLI, opts localAgentRunnerStopOptions) (*localAgentRunnerStopOutput, error) {
	targets, err := resolveLocalAgentRunnerStopTargets(ctx, cli, opts)
	if err != nil {
		return nil, err
	}
	output := &localAgentRunnerStopOutput{Status: "stopped", Runners: make([]localAgentRunnerStopResult, 0, len(targets))}
	for _, target := range targets {
		result := localAgentRunnerStopResult{
			RunnerID:      target.RunnerID,
			PID:           target.PID,
			WorkspaceRoot: target.WorkspaceRoot,
		}
		processStopped, message, err := stopAgentSupervisorProcess(target.PID, opts.Force)
		result.ProcessStopped = processStopped
		result.Message = message
		if err != nil {
			result.Status = "failed"
			if result.Message == "" {
				result.Message = err.Error()
			}
			output.Status = "failed"
			output.Runners = append(output.Runners, result)
			return output, err
		}
		if strings.TrimSpace(target.WorkspaceRoot) != "" && target.PID > 0 {
			clearAgentSupervisorPIDFileIfMatches(target.WorkspaceRoot, target.PID)
		}
		if strings.TrimSpace(target.RunnerID) != "" {
			unregisterReason := opts.Reason
			if unregisterReason == "" {
				unregisterReason = "local runner stopped"
			}
			if err := unregisterLocalAgentRunnerWithReason(ctx, cli, target.RunnerID, unregisterReason); err != nil {
				result.Status = "failed"
				result.Message = err.Error()
				output.Status = "failed"
				output.Runners = append(output.Runners, result)
				return output, err
			}
			result.Unregistered = true
		}
		result.Status = "stopped"
		if result.Message == "" {
			result.Message = "stopped"
		}
		output.Runners = append(output.Runners, result)
	}
	if len(output.Runners) == 0 {
		output.Status = "no_matches"
	}
	return output, nil
}

func resolveLocalAgentRunnerStopTargets(ctx context.Context, cli *CLI, opts localAgentRunnerStopOptions) ([]localAgentRunnerStopTarget, error) {
	resp, err := cli.agentClient.ListRunners(ctx, &agentv1.ListRunnersRequest{Limit: 200, IncludeOffline: true})
	if err != nil {
		return nil, err
	}
	runners := resp.GetRunners()
	hostName, _ := os.Hostname()
	if opts.All {
		targets := make([]localAgentRunnerStopTarget, 0, len(runners))
		for _, runner := range runners {
			if !agentRunnerIsLocalProvider(runner) || !agentRunnerHostMatches(runner, hostName) || !strings.EqualFold(runner.GetStatus(), "online") {
				continue
			}
			targets = append(targets, localAgentRunnerStopTargetFromRunner(runner))
		}
		return targets, nil
	}
	rootDir := strings.TrimSpace(opts.RootDir)
	if rootDir != "" {
		resolvedRoot, err := resolveAgentWorkspaceRoot(rootDir)
		if err != nil {
			return nil, err
		}
		target := localAgentRunnerStopTarget{WorkspaceRoot: resolvedRoot}
		if pid, err := readExistingAgentSupervisorPID(resolvedRoot); err == nil {
			target.PID = pid
		}
		if runnerID, err := readExistingAgentRunnerID(resolvedRoot); err == nil {
			target.RunnerID = runnerID
		}
		if opts.RunnerID != "" {
			if target.RunnerID != "" && target.RunnerID != opts.RunnerID {
				return nil, fmt.Errorf("workspace %s belongs to runner %s, not %s", resolvedRoot, target.RunnerID, opts.RunnerID)
			}
			target.RunnerID = opts.RunnerID
		}
		if runner := findAgentRunnerByID(runners, target.RunnerID); runner != nil {
			if !agentRunnerHostMatches(runner, hostName) {
				return nil, fmt.Errorf("runner %s is registered on host %s, not %s", runner.GetRunnerId(), runner.GetHostName(), hostName)
			}
			target = mergeLocalAgentRunnerStopTarget(target, localAgentRunnerStopTargetFromRunner(runner))
		}
		if target.RunnerID == "" && target.PID == 0 {
			return nil, fmt.Errorf("no local agent runner metadata found in %s", resolvedRoot)
		}
		return []localAgentRunnerStopTarget{target}, nil
	}
	if opts.RunnerID == "" {
		return nil, fmt.Errorf("runner id or workspace is required")
	}
	runner := findAgentRunnerByID(runners, opts.RunnerID)
	if runner == nil {
		return nil, fmt.Errorf("runner %s was not found", opts.RunnerID)
	}
	if !agentRunnerHostMatches(runner, hostName) {
		return nil, fmt.Errorf("runner %s is registered on host %s, not %s", runner.GetRunnerId(), runner.GetHostName(), hostName)
	}
	return []localAgentRunnerStopTarget{localAgentRunnerStopTargetFromRunner(runner)}, nil
}

func findAgentRunnerByID(runners []*agentv1.AgentRunner, runnerID string) *agentv1.AgentRunner {
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil
	}
	for _, runner := range runners {
		if strings.TrimSpace(runner.GetRunnerId()) == runnerID {
			return runner
		}
	}
	return nil
}

func agentRunnerIsLocalProvider(runner *agentv1.AgentRunner) bool {
	provider := strings.TrimSpace(runner.GetProvider())
	return provider == "" || strings.EqualFold(provider, "local")
}

func agentRunnerHostMatches(runner *agentv1.AgentRunner, hostName string) bool {
	runnerHost := strings.TrimSpace(runner.GetHostName())
	localHost := strings.TrimSpace(hostName)
	return runnerHost == "" || localHost == "" || strings.EqualFold(runnerHost, localHost)
}

func localAgentRunnerStopTargetFromRunner(runner *agentv1.AgentRunner) localAgentRunnerStopTarget {
	return localAgentRunnerStopTarget{
		RunnerID:      strings.TrimSpace(runner.GetRunnerId()),
		PID:           int(runner.GetPid()),
		WorkspaceRoot: strings.TrimSpace(runner.GetWorkspaceRoot()),
		HostName:      strings.TrimSpace(runner.GetHostName()),
		Status:        strings.TrimSpace(runner.GetStatus()),
	}
}

func mergeLocalAgentRunnerStopTarget(local, remote localAgentRunnerStopTarget) localAgentRunnerStopTarget {
	if local.RunnerID == "" {
		local.RunnerID = remote.RunnerID
	}
	if local.PID == 0 {
		local.PID = remote.PID
	}
	if local.WorkspaceRoot == "" {
		local.WorkspaceRoot = remote.WorkspaceRoot
	}
	if local.HostName == "" {
		local.HostName = remote.HostName
	}
	if local.Status == "" {
		local.Status = remote.Status
	}
	return local
}

func readExistingAgentRunnerID(rootDir string) (string, error) {
	root, err := filepath.Abs(strings.TrimSpace(rootDir))
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(root, agentWorkspaceStateDir, "runner_id"))
	if err != nil {
		return "", err
	}
	runnerID := strings.TrimSpace(string(raw))
	if runnerID == "" {
		return "", fmt.Errorf("runner id file is empty")
	}
	return runnerID, nil
}

func readExistingAgentSupervisorPID(rootDir string) (int, error) {
	root, err := filepath.Abs(strings.TrimSpace(rootDir))
	if err != nil {
		return 0, err
	}
	return readAgentSupervisorPIDFilePath(filepath.Join(root, agentWorkspaceStateDir, "agent.pid"))
}

func stopAgentSupervisorProcess(pid int, force bool) (bool, string, error) {
	if pid <= 0 {
		return false, "no local process id recorded", nil
	}
	if !agentSupervisorProcessAlive(pid) {
		return false, "process is not running", nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, "", err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, "process is not running", nil
		}
		return false, "", err
	}
	if waitForAgentSupervisorExit(pid, 5*time.Second) {
		return true, "stopped", nil
	}
	if !force {
		return true, "sent SIGTERM but process is still running", fmt.Errorf("runner process %d did not exit; retry with --force", pid)
	}
	if err := process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return true, "", err
	}
	if waitForAgentSupervisorExit(pid, 5*time.Second) {
		return true, "force-stopped", nil
	}
	return true, "sent SIGKILL but process is still running", fmt.Errorf("runner process %d did not exit after SIGKILL", pid)
}

func waitForAgentSupervisorExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !agentSupervisorProcessAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !agentSupervisorProcessAlive(pid)
}

func unregisterLocalAgentRunnerWithReason(ctx context.Context, cli *CLI, runnerID, reason string) error {
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "local runner stopped"
	}
	_, err := cli.agentClient.UnregisterRunner(ctx, &agentv1.UnregisterRunnerRequest{
		RunnerId: runnerID,
		Reason:   strings.TrimSpace(reason),
	})
	return err
}

func stopAgentSession(ctx context.Context, cli *CLI, sessionID, reason string) (*agentv1.StopSessionResponse, error) {
	return cli.agentClient.StopSession(ctx, &agentv1.StopSessionRequest{SessionId: strings.TrimSpace(sessionID), Reason: strings.TrimSpace(reason)})
}

type localAgentRunConfig struct {
	SessionID    string
	SliceID      string
	AgentType    string
	RootDir      string
	CWD          string
	CodexMode    string
	ClaudeMode   string
	Command      []string
	PollInterval time.Duration
	Once         bool
	AuthContext  func(context.Context) (context.Context, error)
}

type localAgentTurnRunner interface {
	RunTurn(ctx context.Context, prompt string) error
	Interrupt(ctx context.Context, reason string) error
	Close() error
}

type commandAgentTurnRunner struct {
	cli *CLI
	cfg localAgentRunConfig
}

func (r commandAgentTurnRunner) RunTurn(ctx context.Context, prompt string) error {
	return runLocalAgentCommand(ctx, r.cli, r.cfg, prompt)
}

func (r commandAgentTurnRunner) Interrupt(context.Context, string) error {
	return nil
}

func (r commandAgentTurnRunner) Close() error {
	return nil
}

type activeAgentTurn struct {
	done              chan error
	cancel            context.CancelFunc
	interrupt         func(context.Context, string) error
	cancelOnInterrupt bool
	afterFinish       func(error)
}

func runAgentBridge(ctx context.Context, cli *CLI, cfg localAgentRunConfig) (int, error) {
	if cfg.SessionID == "" {
		return 0, fmt.Errorf("session id is required")
	}
	codexMode := normalizedCodexMode(cfg.CodexMode)
	if codexMode == "" {
		return 0, fmt.Errorf("--codex-mode must be app-server")
	}
	claudeMode := normalizedClaudeMode(cfg.ClaudeMode)
	if claudeMode == "" {
		return 0, fmt.Errorf("--claude-mode must be auto or stream-json")
	}
	nextSeq, queuedInputs, pendingLocalChanges, err := initialAgentBridgeState(ctx, cli, cfg.SessionID)
	if err != nil {
		return 0, err
	}
	completed := 0
	var codexRunner localAgentTurnRunner
	var claudeRunner *claudeStreamJSONRunner
	var active *activeAgentTurn
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	defer func() {
		if active != nil {
			active.cancel()
		}
		if codexRunner != nil {
			_ = codexRunner.Close()
		}
		if claudeRunner != nil {
			_ = claudeRunner.Close()
		}
	}()

	if pendingLocalChanges.Seq > 0 {
		if err := handleLocalAgentChangesRequest(ctx, cli, cfg, pendingLocalChanges.Seq, pendingLocalChanges.Request); err != nil {
			_ = appendAgentError(ctx, cli, cfg.SessionID, "LOCAL_CHANGES_STATUS_FAILED", err.Error())
		}
	}

	startNext := func() error {
		if active != nil || len(queuedInputs) == 0 {
			return nil
		}
		prompt := queuedInputs[0]
		queuedInputs = queuedInputs[1:]

		var runner localAgentTurnRunner
		cancelOnInterrupt := false
		var afterFinish func(error)
		if len(cfg.Command) > 0 {
			runner = commandAgentTurnRunner{cli: cli, cfg: cfg}
			cancelOnInterrupt = true
		} else if shouldUseCodexAppServer(cfg, codexMode) {
			if codexRunner == nil {
				var err error
				codexRunner, err = newCodexAppServerRunner(ctx, cli, cfg)
				if err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "CODEX_APP_SERVER_UNAVAILABLE", err.Error())
					return err
				}
			}
			if codexRunner != nil {
				runner = codexRunner
			}
		} else if shouldUseClaudeStreamJSON(cfg, claudeMode) {
			if claudeRunner == nil {
				var err error
				claudeRunner, err = newClaudeStreamJSONRunner(ctx, cli, cfg)
				if err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "CLAUDE_STREAM_JSON_UNAVAILABLE", err.Error())
					return err
				}
			}
			if claudeRunner != nil {
				runner = claudeRunner
				afterFinish = func(err error) {
					if claudeRunner != nil && claudeRunner.isDone() {
						_ = claudeRunner.Close()
						claudeRunner = nil
					}
				}
			}
		} else {
			return fmt.Errorf("unsupported local agent type %q", strings.TrimSpace(cfg.AgentType))
		}
		if runner == nil {
			return fmt.Errorf("no local agent runner available for %q", strings.TrimSpace(cfg.AgentType))
		}

		turnCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		active = &activeAgentTurn{
			done:              done,
			cancel:            cancel,
			interrupt:         runner.Interrupt,
			cancelOnInterrupt: cancelOnInterrupt,
			afterFinish:       afterFinish,
		}
		go func() {
			done <- runner.RunTurn(turnCtx, prompt)
		}()
		return nil
	}

	if err := startNext(); err != nil {
		return completed, err
	}

	finishActive := func(err error) (bool, error) {
		if active != nil {
			active.cancel()
			if active.afterFinish != nil {
				active.afterFinish(err)
			}
			active = nil
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			_ = appendAgentError(ctx, cli, cfg.SessionID, "LOCAL_AGENT_COMMAND_FAILED", err.Error())
		}
		completed++
		if cfg.Once {
			return true, nil
		}
		return false, startNext()
	}

	for {
		if active != nil {
			select {
			case err := <-active.done:
				done, finishErr := finishActive(err)
				if done || finishErr != nil {
					return completed, finishErr
				}
			default:
			}
		}
		resp, err := cli.agentClient.ListEvents(ctx, &agentv1.ListEventsRequest{SessionId: cfg.SessionID, SinceSeq: nextSeq, Limit: 200})
		if err != nil {
			return completed, err
		}
		for _, event := range resp.GetEvents() {
			if event.GetSeq() > nextSeq {
				nextSeq = event.GetSeq()
			}
			switch event.GetStream() + "/" + event.GetType() {
			case "agent/input":
				text := agentInputText(event.GetPayload())
				if strings.TrimSpace(text) == "" {
					continue
				}
				queuedInputs = append(queuedInputs, text)
				if err := startNext(); err != nil {
					return completed, err
				}
			case "agent/interrupt":
				reason := agentInterruptReason(event.GetPayload())
				if active != nil {
					if err := active.interrupt(ctx, reason); err != nil {
						_ = appendAgentError(ctx, cli, cfg.SessionID, "LOCAL_AGENT_INTERRUPT_FAILED", err.Error())
					}
					if active.cancelOnInterrupt {
						active.cancel()
					}
				}
			case "control/local_runner_restart_requested":
				request := parseLocalRunnerRestartRequest(event.GetPayload())
				err := requestLocalAgentSupervisorRestart(ctx, cli, cfg.SessionID, cfg.RootDir, event.GetSeq(), request)
				if errors.Is(err, errAgentSupervisorRestarting) {
					return completed, err
				}
				if err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "LOCAL_AGENT_RESTART_FAILED", err.Error())
				}
			case agentsession.EventStreamControl + "/" + agentsession.EventTypeLocalChangesRequested:
				request := parseLocalAgentChangesRequest(event.GetPayload())
				if err := handleLocalAgentChangesRequest(ctx, cli, cfg, event.GetSeq(), request); err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "LOCAL_CHANGES_STATUS_FAILED", err.Error())
				}
			case agentsession.EventStreamControl + "/" + agentsession.EventTypeChangesetExportRequested:
				request := parseLocalAgentChangesetExportRequest(event.GetPayload())
				if active != nil {
					_ = appendAgentJSONEvent(ctx, cli, cfg.SessionID, agentsession.EventStreamControl, agentsession.EventTypeChangesetExportFailed, map[string]any{
						"status":        "failed",
						"request_id":    localAgentRequestID(request.RequestID, request.RequestIDSnake),
						"requested_seq": event.GetSeq(),
						"code":          "LOCAL_AGENT_BUSY",
						"message":       "agent is currently responding; try exporting after the turn finishes",
						"failed_at":     time.Now().UTC().Format(time.RFC3339),
					})
					continue
				}
				if err := handleLocalAgentChangesetExportRequest(ctx, cli, cfg, event.GetSeq(), request); err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "LOCAL_CHANGESET_EXPORT_FAILED", err.Error())
				}
			}
		}
		select {
		case err := <-activeDone(active):
			done, finishErr := finishActive(err)
			if done || finishErr != nil {
				return completed, finishErr
			}
		case <-ctx.Done():
			return completed, ctx.Err()
		case <-ticker.C:
		}
	}
}

func initialAgentBridgeState(ctx context.Context, cli *CLI, sessionID string) (uint64, []string, pendingLocalAgentChangesRequest, error) {
	const pageSize = 500
	var events []*agentv1.EventEnvelope
	nextSeq := uint64(0)
	for {
		resp, err := cli.agentClient.ListEvents(ctx, &agentv1.ListEventsRequest{SessionId: sessionID, SinceSeq: nextSeq, Limit: pageSize})
		if err != nil {
			return 0, nil, pendingLocalAgentChangesRequest{}, err
		}
		pageEvents := resp.GetEvents()
		events = append(events, pageEvents...)
		if len(pageEvents) == 0 {
			break
		}
		nextSeq = pageEvents[len(pageEvents)-1].GetSeq()
		if len(pageEvents) < pageSize {
			break
		}
	}
	pendingLocalChanges, _ := latestPendingLocalAgentChangesRequest(events)
	return nextSeq, pendingAgentInputs(events), pendingLocalChanges, nil
}

func pendingAgentInputs(events []*agentv1.EventEnvelope) []string {
	var pending []string
	for _, event := range events {
		switch event.GetStream() + "/" + event.GetType() {
		case "agent/input":
			text := agentInputText(event.GetPayload())
			if strings.TrimSpace(text) != "" {
				pending = append(pending, text)
			}
		case "agent/output_final":
			pending = nil
		case "control/error":
			if agentControlErrorIsTerminal(event.GetPayload()) {
				pending = nil
			}
		case "status/state":
			if !agentSessionStateActive(agentState(event.GetPayload())) {
				pending = nil
			}
		}
	}
	return pending
}

func activeDone(active *activeAgentTurn) <-chan error {
	if active == nil {
		return nil
	}
	return active.done
}

func normalizedCodexMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "app-server"
	case "app-server", "remote-control":
		return "app-server"
	default:
		return ""
	}
}

func normalizedClaudeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "stream-json"
	case "stream-json", "stream", "json":
		return "stream-json"
	default:
		return ""
	}
}

type localAgentCLIStatus struct {
	AgentType string `json:"agent_type"`
	Command   string `json:"command"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Message   string `json:"message,omitempty"`
}

func parseLocalAgentTypes(raw string) ([]string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" || value == "all" || value == "*" {
		return agentsession.SupportedAgentTypes(), nil
	}
	seen := map[string]struct{}{}
	for _, part := range strings.Split(value, ",") {
		agentType := agentsession.NormalizeAgentType(part)
		if agentType == "" {
			continue
		}
		if agentType == "all" || agentType == "*" {
			return agentsession.SupportedAgentTypes(), nil
		}
		if !agentsession.IsSupportedAgentType(agentType) {
			return nil, fmt.Errorf("--agent must be codex, claude, all, or a comma-separated list of supported agent types")
		}
		seen[agentType] = struct{}{}
	}
	if len(seen) == 0 {
		return agentsession.SupportedAgentTypes(), nil
	}
	out := make([]string, 0, len(seen))
	for _, agentType := range agentsession.SupportedAgentTypes() {
		if _, ok := seen[agentType]; ok {
			out = append(out, agentType)
		}
	}
	return out, nil
}

func resolveRunnableLocalAgentTypes(raw string, command []string) ([]string, []localAgentCLIStatus, error) {
	agentTypes, err := parseLocalAgentTypes(raw)
	if err != nil {
		return nil, nil, err
	}
	statuses := inspectLocalAgentCLIs(agentTypes, command)
	if len(command) > 0 {
		if len(statuses) > 0 && !statuses[0].Available {
			return nil, statuses, fmt.Errorf("custom agent command unavailable: %s", statuses[0].Message)
		}
		return agentTypes, statuses, nil
	}

	allowPartial := localAgentSelectionAllowsPartial(raw)
	available := make([]string, 0, len(agentTypes))
	missing := make([]string, 0)
	for _, status := range statuses {
		if status.Available {
			available = append(available, status.AgentType)
			continue
		}
		if !allowPartial {
			missing = append(missing, fmt.Sprintf("%s (%s)", status.AgentType, status.Message))
		}
	}
	if len(missing) > 0 {
		return nil, statuses, fmt.Errorf("requested agent CLI unavailable: %s", strings.Join(missing, ", "))
	}
	if len(available) == 0 {
		return nil, statuses, fmt.Errorf("no supported agent CLIs found on PATH; install codex or claude")
	}
	return available, statuses, nil
}

func localAgentSelectionAllowsPartial(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return value == "" || value == "all" || value == "*"
}

func inspectLocalAgentCLIs(agentTypes []string, command []string) []localAgentCLIStatus {
	if len(command) > 0 {
		status := localAgentCLIStatus{
			AgentType: "custom",
			Command:   command[0],
			Available: true,
		}
		if path, err := exec.LookPath(command[0]); err == nil {
			status.Path = path
		} else {
			status.Available = false
			status.Message = "custom command not found on PATH"
		}
		return []localAgentCLIStatus{status}
	}
	statuses := make([]localAgentCLIStatus, 0, len(agentTypes))
	for _, agentType := range agentTypes {
		commandName := localAgentCLICommand(agentType)
		status := localAgentCLIStatus{
			AgentType: agentType,
			Command:   commandName,
			Available: true,
		}
		if path, err := exec.LookPath(commandName); err == nil {
			status.Path = path
		} else {
			status.Available = false
			status.Message = commandName + " not found on PATH"
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func localAgentCLICommand(agentType string) string {
	switch agentsession.NormalizeAgentType(agentType) {
	case "claude":
		return "claude"
	default:
		return "codex"
	}
}

func printLocalAgentCLIStatus(statuses []localAgentCLIStatus) {
	available := make([]string, 0, len(statuses))
	unavailable := make([]string, 0)
	for _, status := range statuses {
		if status.Available {
			value := status.AgentType
			if status.Path != "" {
				value += "=" + status.Path
			}
			available = append(available, value)
			continue
		}
		unavailable = append(unavailable, fmt.Sprintf("%s (%s)", status.AgentType, status.Message))
	}
	if len(available) > 0 {
		fmt.Printf("Available agent CLIs: %s\n", strings.Join(available, ", "))
	}
	if len(unavailable) > 0 {
		fmt.Printf("Unavailable agent CLIs: %s\n", strings.Join(unavailable, ", "))
	}
}

func formatLocalAgentTypes(agentTypes []string) string {
	normalized, err := parseLocalAgentTypes(strings.Join(agentTypes, ","))
	if err != nil {
		return agentsession.DefaultAgentType()
	}
	return strings.Join(normalized, ",")
}

func supportedLocalAgentTypes(raw string) []string {
	agentTypes, err := parseLocalAgentTypes(raw)
	if err != nil || len(agentTypes) == 0 {
		return []string{agentsession.DefaultAgentType()}
	}
	return agentTypes
}

func defaultLocalAgentType(raw string) string {
	agentTypes := supportedLocalAgentTypes(raw)
	for _, agentType := range agentTypes {
		if agentType == agentsession.DefaultAgentType() {
			return agentType
		}
	}
	return agentTypes[0]
}

func shouldUseCodexAppServer(cfg localAgentRunConfig, codexMode string) bool {
	if len(cfg.Command) > 0 || !strings.EqualFold(strings.TrimSpace(cfg.AgentType), "codex") {
		return false
	}
	return codexMode == "app-server" || codexMode == "auto"
}

func shouldUseClaudeStreamJSON(cfg localAgentRunConfig, claudeMode string) bool {
	if len(cfg.Command) > 0 || !strings.EqualFold(strings.TrimSpace(cfg.AgentType), "claude") {
		return false
	}
	return claudeMode == "stream-json" || claudeMode == "auto"
}

func runLocalAgentCommand(ctx context.Context, cli *CLI, cfg localAgentRunConfig, prompt string) error {
	name, args, stdinPrompt := localAgentCommand(cfg.AgentType, cfg.Command, prompt)
	if name == "" {
		return fmt.Errorf("custom agent command is required")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(cfg.CWD) != "" {
		cmd.Dir = cfg.CWD
	}
	if stdinPrompt {
		cmd.Stdin = strings.NewReader(prompt)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	var outputMu sync.Mutex
	var stdoutOutput bytes.Buffer
	var stderrOutput bytes.Buffer
	stream := func(channel string, reader io.Reader, output *bytes.Buffer) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			outputMu.Lock()
			output.WriteString(line)
			output.WriteByte('\n')
			outputMu.Unlock()
			_ = appendAgentOutput(ctx, cli, cfg.SessionID, line, channel, "output_delta", 0)
		}
	}
	wg.Add(2)
	go stream("stdout", stdout, &stdoutOutput)
	go stream("stderr", stderr, &stderrOutput)
	wg.Wait()
	waitErr := cmd.Wait()
	exitCode := int32(0)
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
		} else {
			return waitErr
		}
	}
	outputMu.Lock()
	finalText := strings.TrimSpace(stdoutOutput.String())
	finalChannel := "stdout"
	if finalText == "" {
		finalText = strings.TrimSpace(stderrOutput.String())
		finalChannel = "stderr"
	}
	outputMu.Unlock()
	return appendAgentOutput(ctx, cli, cfg.SessionID, finalText, finalChannel, "output_final", exitCode)
}

func localAgentCommand(agentType string, command []string, prompt string) (string, []string, bool) {
	if len(command) > 0 {
		return command[0], append([]string(nil), command[1:]...), true
	}
	_ = agentType
	_ = prompt
	return "", nil, false
}

func appendAgentOutput(ctx context.Context, cli *CLI, sessionID, text, channel, eventType string, exitCode int32) error {
	payload, _ := protojson.Marshal(&agentv1.AgentOutputPayload{Text: text, Channel: channel, ExitCode: exitCode})
	_, err := cli.agentClient.AppendEvent(ctx, &agentv1.AppendEventRequest{
		SessionId: sessionID,
		Stream:    "agent",
		Type:      eventType,
		Payload:   payload,
	})
	return err
}

func appendAgentThinking(ctx context.Context, cli *CLI, sessionID, text, channel, turnID, itemID string) error {
	payload, _ := protojson.Marshal(&agentv1.AgentThinkingPayload{
		Text:    text,
		Channel: channel,
		TurnId:  turnID,
		ItemId:  itemID,
	})
	_, err := cli.agentClient.AppendEvent(ctx, &agentv1.AppendEventRequest{
		SessionId: sessionID,
		Stream:    "agent",
		Type:      "thinking_delta",
		Payload:   payload,
	})
	return err
}

func appendAgentError(ctx context.Context, cli *CLI, sessionID, code, message string) error {
	payload, _ := protojson.Marshal(&agentv1.AgentErrorPayload{Code: code, Message: message})
	_, err := cli.agentClient.AppendEvent(ctx, &agentv1.AppendEventRequest{
		SessionId: sessionID,
		Stream:    "control",
		Type:      "error",
		Payload:   payload,
	})
	return err
}

func appendAgentWarning(ctx context.Context, cli *CLI, sessionID, code, message string) error {
	payload, _ := protojson.Marshal(&agentv1.AgentErrorPayload{Code: code, Message: message})
	_, err := cli.agentClient.AppendEvent(ctx, &agentv1.AppendEventRequest{
		SessionId: sessionID,
		Stream:    "control",
		Type:      "warning",
		Payload:   payload,
	})
	return err
}

func agentControlErrorIsTerminal(payload []byte) bool {
	var msg agentv1.AgentErrorPayload
	if err := protojson.Unmarshal(payload, &msg); err == nil && strings.TrimSpace(msg.GetCode()) != "" {
		return !isNonTerminalAgentControlErrorCode(msg.GetCode())
	}
	return true
}

func isNonTerminalAgentControlErrorCode(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "CODEX_CONFIG_WARNING":
		return true
	default:
		return false
	}
}

func agentInputText(payload []byte) string {
	var msg agentv1.AgentInputPayload
	if err := protojson.Unmarshal(payload, &msg); err == nil {
		return strings.TrimSpace(msg.GetText())
	}
	return strings.TrimSpace(string(payload))
}

func agentState(payload []byte) string {
	var msg agentv1.AgentStatePayload
	if err := protojson.Unmarshal(payload, &msg); err == nil {
		return strings.TrimSpace(msg.GetState())
	}
	return strings.TrimSpace(string(payload))
}
