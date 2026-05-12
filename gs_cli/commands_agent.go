package gscli

import (
	"bufio"
	"bytes"
	"context"
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
	cmd := newAuthenticatedCobraCommand("agent <command> [options]", "Start and run local coding agents", 24*time.Hour, func(ctx context.Context, cli *CLI, authConfig cliAuth, args []string) {
		handleAgentCommand(ctx, cli, args)
	})
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printAgentHelp()
	})
	return cmd
}

func handleAgentCommand(ctx context.Context, cli *CLI, args []string) {
	if len(args) == 0 {
		printAgentHelp()
		return
	}
	switch args[0] {
	case "start":
		handleAgentStart(ctx, cli, args[1:])
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

func handleAgentStart(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("agent start")
	sliceFlag := fs.String("slice", "", "Slice ID")
	agentType := fs.String("agent", "codex", "Agent type")
	idleTimeout := fs.Int("idle-timeout", 1800, "Idle timeout in seconds")
	ttl := fs.Int("ttl", 14400, "Session TTL in seconds")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseCommandFlags(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 0 {
		commandUsage("Usage: gs agent start [--slice <slice-id>] [--agent codex|claude] [--idle-timeout <sec>] [--ttl <sec>] [--json]")
		return
	}
	sliceID := resolveAgentSliceID(*sliceFlag)
	resp, err := cli.agentClient.CreateSession(ctx, &agentv1.CreateSessionRequest{
		SliceId:        sliceID,
		Provider:       "local",
		AgentType:      strings.TrimSpace(*agentType),
		IdleTimeoutSec: int32(*idleTimeout),
		TtlSec:         int32(*ttl),
	})
	if err != nil {
		commandFatalf("AGENT_START_FAILED", true, "", "Failed to start local agent session: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(resp)
		return
	}
	fmt.Printf("Agent session: %s\n", resp.GetSessionId())
	fmt.Printf("Slice: %s\n", resp.GetSliceId())
	fmt.Printf("Agent: %s\n", resp.GetAgentType())
}

func handleAgentRun(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	args, once := consumeBoolFlag(args, "once")
	fs := newCommandFlagSet("agent run")
	sessionID := fs.String("session", "", "Existing agent session ID")
	sliceFlag := fs.String("slice", "", "Slice ID when creating a session")
	agentType := fs.String("agent", "codex", "Agent type")
	prompt := fs.String("prompt", "", "Initial prompt to send after the session is ready")
	cwd := fs.String("cwd", ".", "Working directory for the local agent command")
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

	created := false
	createdSliceID := ""
	if strings.TrimSpace(*sessionID) == "" {
		sliceID := resolveAgentSliceID(*sliceFlag)
		resp, err := cli.agentClient.CreateSession(ctx, &agentv1.CreateSessionRequest{
			SliceId:   sliceID,
			Provider:  "local",
			AgentType: strings.TrimSpace(*agentType),
		})
		if err != nil {
			commandFatalf("AGENT_START_FAILED", true, "", "Failed to start local agent session: %v", err)
		}
		*sessionID = resp.GetSessionId()
		created = true
		createdSliceID = resp.GetSliceId()
		if !jsonEnabled {
			fmt.Printf("Agent session: %s\n", resp.GetSessionId())
		}
	}
	if err := waitForAgentSessionRunning(ctx, cli, *sessionID); err != nil {
		commandFatalf("AGENT_SESSION_NOT_READY", true, "", "Agent session is not ready: %v", err)
	}
	if strings.TrimSpace(*prompt) != "" {
		if _, err := cli.agentClient.SendInput(ctx, &agentv1.SendInputRequest{SessionId: strings.TrimSpace(*sessionID), Text: strings.TrimSpace(*prompt)}); err != nil {
			commandFatalf("AGENT_INPUT_FAILED", true, "", "Failed to send initial prompt: %v", err)
		}
	}
	if !jsonEnabled {
		if created {
			fmt.Println("Waiting for web or CLI input. Press Ctrl-C to stop the local runner.")
		} else {
			fmt.Printf("Attached to agent session %s\n", strings.TrimSpace(*sessionID))
		}
	}
	completed, err := runAgentBridge(ctx, cli, localAgentRunConfig{
		SessionID:    strings.TrimSpace(*sessionID),
		AgentType:    strings.TrimSpace(*agentType),
		CWD:          strings.TrimSpace(*cwd),
		Command:      commandArgs,
		PollInterval: pollInterval,
		Once:         once,
	})
	if err != nil {
		commandFatalf("AGENT_RUN_FAILED", true, "", "Local agent runner failed: %v", err)
	}
	if jsonEnabled {
		writeJSONOutput(map[string]any{
			"session_id":       strings.TrimSpace(*sessionID),
			"slice_id":         createdSliceID,
			"agent_type":       strings.TrimSpace(*agentType),
			"created":          created,
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
	CWD          string
	Command      []string
	PollInterval time.Duration
	Once         bool
}

func runAgentBridge(ctx context.Context, cli *CLI, cfg localAgentRunConfig) (int, error) {
	if cfg.SessionID == "" {
		return 0, fmt.Errorf("session id is required")
	}
	nextSeq := uint64(0)
	completed := 0
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
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
				if err := runLocalAgentCommand(ctx, cli, cfg, text); err != nil {
					_ = appendAgentError(ctx, cli, cfg.SessionID, "LOCAL_AGENT_COMMAND_FAILED", err.Error())
				}
				completed++
				if cfg.Once {
					return completed, nil
				}
			case "agent/interrupt":
				return completed, nil
			}
		}
		select {
		case <-ctx.Done():
			return completed, ctx.Err()
		case <-ticker.C:
		}
	}
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
	var output bytes.Buffer
	stream := func(channel string, reader io.Reader) {
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
	go stream("stdout", stdout)
	go stream("stderr", stderr)
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
	finalText := strings.TrimSpace(output.String())
	outputMu.Unlock()
	return appendAgentOutput(ctx, cli, cfg.SessionID, finalText, "stdout", "output_final", exitCode)
}

func localAgentCommand(agentType string, command []string, prompt string) (string, []string, bool) {
	if len(command) > 0 {
		return command[0], append([]string(nil), command[1:]...), true
	}
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "claude":
		return "claude", []string{"-p", prompt}, false
	default:
		return "codex", []string{"exec", prompt}, false
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

func waitForAgentSessionRunning(ctx context.Context, cli *CLI, sessionID string) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := cli.agentClient.GetSession(ctx, &agentv1.GetSessionRequest{SessionId: strings.TrimSpace(sessionID)})
		if err != nil {
			return err
		}
		switch resp.GetState() {
		case "running", "idle":
			return nil
		case "failed", "stopped":
			return fmt.Errorf("session state is %s", resp.GetState())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for running state")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func resolveAgentSliceID(raw string) string {
	if sliceID := strings.TrimSpace(raw); sliceID != "" {
		normalized, err := normalizeSliceID(sliceID)
		if err != nil {
			commandFatalf("INVALID_SLICE_REFERENCE", false, "", "Invalid slice ID: %v", err)
		}
		return normalized
	}
	sliceID, err := sliceIDFromConfig()
	if err != nil {
		commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read current slice binding: %v", err)
	}
	return sliceID
}
