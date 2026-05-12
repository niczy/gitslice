package gscli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

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
		handleAgentRun(ctx, cli, args[1:])
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
	agentType := fs.String("agent", "codex", "Agent type")
	cwd := fs.String("cwd", ".", "Working directory for tracked agent sessions")
	dir := fs.String("dir", "", "Alias for --cwd")
	codexMode := fs.String("codex-mode", "auto", "Codex runner mode: auto, app-server, or exec")
	claudeMode := fs.String("claude-mode", "auto", "Claude runner mode: auto, stream-json, or print")
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
	rootDir := *cwd
	if strings.TrimSpace(*dir) != "" {
		rootDir = *dir
	}
	result, err := startAgentSupervisorBackground(ctx, cli, authConfig, localAgentSupervisorConfig{
		RootDir:      strings.TrimSpace(rootDir),
		AgentType:    strings.TrimSpace(*agentType),
		CodexMode:    strings.TrimSpace(*codexMode),
		ClaudeMode:   strings.TrimSpace(*claudeMode),
		Command:      append([]string(nil), fs.Args()...),
		PollInterval: pollInterval,
		LogFile:      strings.TrimSpace(*logFile),
	})
	if err != nil {
		commandFatalf("AGENT_START_FAILED", true, "", "Failed to start local agent runner: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(result)
		return
	}
	fmt.Printf("Agent runner started: pid %d\n", result.PID)
	fmt.Printf("Workspace: %s\n", result.CWD)
	fmt.Printf("Log: %s\n", result.LogFile)
}

func handleAgentRun(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, once := consumeBoolFlag(args, "once")
	fs := newCommandFlagSet("agent run")
	agentType := fs.String("agent", "codex", "Agent type")
	cwd := fs.String("cwd", ".", "Working directory for tracked agent sessions")
	dir := fs.String("dir", "", "Alias for --cwd")
	codexMode := fs.String("codex-mode", "auto", "Codex runner mode: auto, app-server, or exec")
	claudeMode := fs.String("claude-mode", "auto", "Claude runner mode: auto, stream-json, or print")
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
	rootDir := *cwd
	if strings.TrimSpace(*dir) != "" {
		rootDir = *dir
	}
	rootDir, err = resolveAgentWorkspaceRoot(rootDir)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid working directory: %v", err)
	}
	if !jsonEnabled {
		fmt.Printf("Tracking local agent sessions in %s. Press Ctrl-C to stop the local runner.\n", rootDir)
	}
	completed, err := runAgentSupervisor(ctx, cli, localAgentSupervisorConfig{
		RootDir:      rootDir,
		AgentType:    strings.TrimSpace(*agentType),
		CodexMode:    strings.TrimSpace(*codexMode),
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
			"agent_type":       strings.TrimSpace(*agentType),
			"completed_inputs": completed,
		})
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
	fs := newCommandFlagSet("agent stop")
	reason := fs.String("reason", "user stop", "Stop reason")
	parseCommandFlags(fs, args)
	if fs.NArg() != 1 {
		commandUsage("Usage: gs agent stop <session-id> [--reason <reason>]")
		return
	}
	resp, err := cli.agentClient.StopSession(ctx, &agentv1.StopSessionRequest{SessionId: strings.TrimSpace(fs.Arg(0)), Reason: strings.TrimSpace(*reason)})
	if err != nil {
		commandFatalf("AGENT_STOP_FAILED", true, "", "Failed to stop agent session: %v", err)
	}
	writeJSONOutput(resp)
}

type localAgentRunConfig struct {
	SessionID    string
	AgentType    string
	RootDir      string
	CWD          string
	CodexMode    string
	ClaudeMode   string
	Command      []string
	PollInterval time.Duration
	Once         bool
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
		return 0, fmt.Errorf("--codex-mode must be auto, app-server, or exec")
	}
	claudeMode := normalizedClaudeMode(cfg.ClaudeMode)
	if claudeMode == "" {
		return 0, fmt.Errorf("--claude-mode must be auto, stream-json, or print")
	}
	nextSeq, queuedInputs, err := initialAgentBridgeState(ctx, cli, cfg.SessionID)
	if err != nil {
		return 0, err
	}
	completed := 0
	var codexRunner localAgentTurnRunner
	var claudeRunner *claudeStreamJSONRunner
	var codexUnavailable bool
	var claudeUnavailable bool
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

	startNext := func() error {
		if active != nil || len(queuedInputs) == 0 {
			return nil
		}
		prompt := queuedInputs[0]
		queuedInputs = queuedInputs[1:]

		runner := localAgentTurnRunner(commandAgentTurnRunner{cli: cli, cfg: cfg})
		cancelOnInterrupt := true
		var afterFinish func(error)
		if shouldUseCodexAppServer(cfg, codexMode) && !codexUnavailable {
			if codexRunner == nil {
				var err error
				codexRunner, err = newCodexAppServerRunner(ctx, cli, cfg)
				if err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "CODEX_APP_SERVER_UNAVAILABLE", err.Error())
					if codexMode == "app-server" {
						return err
					}
					codexUnavailable = true
				}
			}
			if codexRunner != nil {
				runner = codexRunner
				cancelOnInterrupt = false
			}
		}
		if shouldUseClaudeStreamJSON(cfg, claudeMode) && !claudeUnavailable {
			if claudeRunner == nil {
				var err error
				claudeRunner, err = newClaudeStreamJSONRunner(ctx, cli, cfg)
				if err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "CLAUDE_STREAM_JSON_UNAVAILABLE", err.Error())
					if claudeMode == "stream-json" {
						return err
					}
					claudeUnavailable = true
				}
			}
			if claudeRunner != nil {
				runner = claudeRunner
				cancelOnInterrupt = false
				afterFinish = func(err error) {
					if claudeRunner != nil && claudeRunner.isDone() {
						_ = claudeRunner.Close()
						claudeRunner = nil
						if err != nil && claudeMode == "auto" {
							claudeUnavailable = true
						}
					}
				}
			}
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

func initialAgentBridgeState(ctx context.Context, cli *CLI, sessionID string) (uint64, []string, error) {
	const pageSize = 500
	var events []*agentv1.EventEnvelope
	nextSeq := uint64(0)
	for {
		resp, err := cli.agentClient.ListEvents(ctx, &agentv1.ListEventsRequest{SessionId: sessionID, SinceSeq: nextSeq, Limit: pageSize})
		if err != nil {
			return 0, nil, err
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
	return nextSeq, pendingAgentInputs(events), nil
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
		case "agent/output_final", "control/error":
			pending = nil
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
		return "auto"
	case "app-server", "remote-control":
		return "app-server"
	case "exec":
		return "exec"
	default:
		return ""
	}
}

func normalizedClaudeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "auto"
	case "stream-json", "stream", "json":
		return "stream-json"
	case "print", "exec":
		return "print"
	default:
		return ""
	}
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
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude":
		return "claude", []string{"-p", prompt}, false
	default:
		return "codex", []string{"exec", "--skip-git-repo-check", prompt}, false
	}
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
