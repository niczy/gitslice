package gscli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/niczy/gitslice/proto/agent"
	"google.golang.org/protobuf/encoding/protojson"
)

const codexAppServerInitTimeout = 15 * time.Second

type codexRPCMessage struct {
	ID     json.RawMessage    `json:"id,omitempty"`
	Method string             `json:"method,omitempty"`
	Params json.RawMessage    `json:"params,omitempty"`
	Result json.RawMessage    `json:"result,omitempty"`
	Error  *codexRPCErrorBody `json:"error,omitempty"`
}

type codexRPCErrorBody struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *codexRPCErrorBody) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

type codexAppServerRunner struct {
	cli       *CLI
	cfg       localAgentRunConfig
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	encoder   *json.Encoder
	stderrLog *limitedBuffer

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan codexRPCMessage
	seq     int64

	threadID      string
	currentTurnID string

	notifications chan codexRPCMessage
	done          chan struct{}
	doneOnce      sync.Once
	doneErr       error
}

func newCodexAppServerRunner(ctx context.Context, cli *CLI, cfg localAgentRunConfig) (*codexAppServerRunner, error) {
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, "codex", codexAppServerCommandArgs()...)
	if strings.TrimSpace(cfg.CWD) != "" {
		cmd.Dir = cfg.CWD
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	r := &codexAppServerRunner{
		cli:           cli,
		cfg:           cfg,
		cancel:        cancel,
		cmd:           cmd,
		stdin:         stdin,
		encoder:       json.NewEncoder(stdin),
		stderrLog:     newLimitedBuffer(64 * 1024),
		pending:       make(map[string]chan codexRPCMessage),
		notifications: make(chan codexRPCMessage, 2048),
		done:          make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go r.readLoop(stdout)
	go r.captureStderr(stderr)

	initCtx, initCancel := context.WithTimeout(ctx, codexAppServerInitTimeout)
	defer initCancel()
	if _, err := r.call(initCtx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "gitslice",
			"title":   "Gitslice",
			"version": "dev",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		_ = r.Close()
		return nil, r.withStderr("initialize codex app-server", err)
	}
	if err := r.notify(initCtx, "initialized", nil); err != nil {
		_ = r.Close()
		return nil, r.withStderr("notify codex app-server initialized", err)
	}

	threadParams := map[string]any{
		"experimentalRawEvents":  false,
		"persistExtendedHistory": false,
	}
	if cwd := strings.TrimSpace(cfg.CWD); cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			_ = r.Close()
			return nil, err
		}
		threadParams["cwd"] = abs
	}
	result, err := r.call(initCtx, "thread/start", threadParams)
	if err != nil {
		_ = r.Close()
		return nil, r.withStderr("start codex thread", err)
	}
	var threadResp struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &threadResp); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("decode codex thread/start response: %w", err)
	}
	if strings.TrimSpace(threadResp.Thread.ID) == "" {
		_ = r.Close()
		return nil, errors.New("codex thread/start response did not include thread id")
	}
	r.mu.Lock()
	r.threadID = threadResp.Thread.ID
	r.mu.Unlock()
	if err := appendCodexRuntimeSession(ctx, cli, cfg.SessionID, threadResp.Thread.ID); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("store codex runtime session metadata: %w", err)
	}
	return r, nil
}

func codexAppServerCommandArgs() []string {
	return []string{
		"--sandbox", "workspace-write",
		"--ask-for-approval", "never",
		"app-server",
		"--listen", "stdio://",
	}
}

func (r *codexAppServerRunner) RunTurn(ctx context.Context, prompt string) error {
	r.mu.Lock()
	threadID := r.threadID
	r.mu.Unlock()
	if threadID == "" {
		return errors.New("codex thread is not initialized")
	}
	result, err := r.call(ctx, "turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{
				"type":          "text",
				"text":          prompt,
				"text_elements": []any{},
			},
		},
	})
	if err != nil {
		return err
	}
	var turnResp struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(result, &turnResp); err != nil {
		return fmt.Errorf("decode codex turn/start response: %w", err)
	}
	if turnResp.Turn.ID == "" {
		return errors.New("codex turn/start response did not include turn id")
	}

	r.mu.Lock()
	r.currentTurnID = turnResp.Turn.ID
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.currentTurnID == turnResp.Turn.ID {
			r.currentTurnID = ""
		}
		r.mu.Unlock()
	}()

	finalText := ""
	for {
		select {
		case msg := <-r.notifications:
			done, nextFinalText, exitCode, err := r.handleNotification(ctx, turnResp.Turn.ID, finalText, msg)
			finalText = nextFinalText
			if done {
				if appendErr := appendAgentOutput(ctx, r.cli, r.cfg.SessionID, strings.TrimSpace(finalText), "assistant", "output_final", exitCode); appendErr != nil {
					return appendErr
				}
				return err
			}
		case <-r.done:
			if err := r.getDoneErr(); err != nil {
				return r.withStderr("codex app-server exited", err)
			}
			return errors.New("codex app-server exited")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (r *codexAppServerRunner) Interrupt(ctx context.Context, reason string) error {
	r.mu.Lock()
	threadID := r.threadID
	turnID := r.currentTurnID
	r.mu.Unlock()
	if threadID == "" || turnID == "" {
		return nil
	}
	_, err := r.call(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	})
	return err
}

func (r *codexAppServerRunner) Close() error {
	r.cancel()
	if r.stdin != nil {
		_ = r.stdin.Close()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.cmd != nil {
		_ = r.cmd.Wait()
	}
	return nil
}

func (r *codexAppServerRunner) handleNotification(ctx context.Context, turnID, finalText string, msg codexRPCMessage) (bool, string, int32, error) {
	switch msg.Method {
	case "item/agentMessage/delta":
		var params struct {
			TurnID string `json:"turnId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && params.TurnID == turnID && params.Delta != "" {
			_ = appendAgentOutput(ctx, r.cli, r.cfg.SessionID, params.Delta, "assistant", "output_delta", 0)
			finalText += params.Delta
		}
	case "item/reasoning/textDelta":
		r.appendReasoningDelta(ctx, turnID, "reasoning", msg.Params)
	case "item/reasoning/summaryTextDelta":
		r.appendReasoningDelta(ctx, turnID, "reasoning_summary", msg.Params)
	case "item/commandExecution/outputDelta":
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && params.TurnID == turnID && params.Delta != "" {
			_ = appendAgentJSONEvent(ctx, r.cli, r.cfg.SessionID, "tool", "output", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   params.TurnID,
				"itemId":   params.ItemID,
				"text":     params.Delta,
			})
		}
	case "item/started":
		r.appendToolLifecycleEvent(ctx, turnID, "start", msg.Params)
	case "item/completed":
		var params struct {
			TurnID string `json:"turnId"`
			Item   struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && params.TurnID == turnID && params.Item.Type == "agentMessage" && params.Item.Text != "" {
			finalText = params.Item.Text
		}
		r.appendToolLifecycleEvent(ctx, turnID, "end", msg.Params)
	case "turn/completed":
		var params struct {
			Turn struct {
				ID     string          `json:"id"`
				Status string          `json:"status"`
				Error  json.RawMessage `json:"error"`
			} `json:"turn"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && params.Turn.ID == turnID {
			switch params.Turn.Status {
			case "failed":
				return true, finalText, 1, codexTurnError(params.Turn.Error)
			case "interrupted":
				return true, finalText, 130, nil
			default:
				return true, finalText, 0, nil
			}
		}
	case "configWarning":
		var params struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && strings.TrimSpace(params.Summary) != "" {
			_ = appendAgentWarning(ctx, r.cli, r.cfg.SessionID, "CODEX_CONFIG_WARNING", params.Summary)
		}
	case "error":
		var params struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && strings.TrimSpace(params.Message) != "" {
			_ = appendAgentError(ctx, r.cli, r.cfg.SessionID, "CODEX_APP_SERVER_ERROR", params.Message)
		}
	}
	return false, finalText, 0, nil
}

func (r *codexAppServerRunner) appendReasoningDelta(ctx context.Context, turnID, channel string, payload json.RawMessage) {
	delta, parsedTurnID, itemID := codexReasoningDelta(payload, turnID)
	if delta == "" {
		return
	}
	_ = appendAgentThinking(ctx, r.cli, r.cfg.SessionID, delta, channel, parsedTurnID, itemID)
}

func codexReasoningDelta(payload json.RawMessage, expectedTurnID string) (string, string, string) {
	var params struct {
		TurnID string `json:"turnId"`
		ItemID string `json:"itemId"`
		Delta  string `json:"delta"`
	}
	if json.Unmarshal(payload, &params) != nil || params.TurnID != expectedTurnID || params.Delta == "" {
		return "", "", ""
	}
	return params.Delta, params.TurnID, params.ItemID
}

func (r *codexAppServerRunner) appendToolLifecycleEvent(ctx context.Context, turnID, eventType string, payload json.RawMessage) {
	var params struct {
		TurnID string `json:"turnId"`
		Item   struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if json.Unmarshal(payload, &params) != nil || params.TurnID != turnID {
		return
	}
	switch params.Item.Type {
	case "commandExecution", "mcpToolCall", "dynamicToolCall", "fileChange":
		_ = appendAgentJSONEvent(ctx, r.cli, r.cfg.SessionID, "tool", eventType, json.RawMessage(payload))
	}
}

func (r *codexAppServerRunner) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := r.nextID()
	ch := make(chan codexRPCMessage, 1)
	r.mu.Lock()
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
	}()

	req := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		req["params"] = params
	}
	if err := r.writeJSON(req); err != nil {
		return nil, err
	}

	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	case <-r.done:
		if err := r.getDoneErr(); err != nil {
			return nil, err
		}
		return nil, errors.New("codex app-server exited")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *codexAppServerRunner) notify(ctx context.Context, method string, params any) error {
	req := map[string]any{"method": method}
	if params != nil {
		req["params"] = params
	}
	errc := make(chan error, 1)
	go func() {
		errc <- r.writeJSON(req)
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *codexAppServerRunner) writeJSON(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return r.encoder.Encode(v)
}

func (r *codexAppServerRunner) readLoop(reader io.Reader) {
	decoder := json.NewDecoder(reader)
	for {
		var msg codexRPCMessage
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				r.finish(nil)
			} else {
				r.finish(err)
			}
			return
		}
		if id := codexMessageID(msg.ID); id != "" {
			r.mu.Lock()
			ch := r.pending[id]
			r.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		r.notifications <- msg
	}
}

func (r *codexAppServerRunner) captureStderr(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		r.stderrLog.WriteString(line + "\n")
	}
}

func (r *codexAppServerRunner) finish(err error) {
	r.doneOnce.Do(func() {
		r.mu.Lock()
		r.doneErr = err
		r.mu.Unlock()
		close(r.done)
	})
}

func (r *codexAppServerRunner) getDoneErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.doneErr
}

func (r *codexAppServerRunner) nextID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	return "gs-" + strconv.FormatInt(r.seq, 10)
}

func (r *codexAppServerRunner) withStderr(action string, err error) error {
	tail := strings.TrimSpace(r.stderrLog.String())
	if tail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, tail)
}

func codexMessageID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return strconv.FormatInt(n, 10)
	}
	return ""
}

func codexTurnError(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errors.New("codex turn failed")
	}
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &payload) == nil && strings.TrimSpace(payload.Message) != "" {
		return errors.New(payload.Message)
	}
	return fmt.Errorf("codex turn failed: %s", strings.TrimSpace(string(raw)))
}

func appendAgentJSONEvent(ctx context.Context, cli *CLI, sessionID, stream, eventType string, payload any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = cli.agentClient.AppendEvent(ctx, &agentv1.AppendEventRequest{
		SessionId: sessionID,
		Stream:    stream,
		Type:      eventType,
		Payload:   payloadBytes,
	})
	return err
}

func appendCodexRuntimeSession(ctx context.Context, cli *CLI, sessionID, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	return appendAgentJSONEvent(ctx, cli, sessionID, "control", "runtime_session", map[string]any{
		"runtimeProvider":  "local",
		"runtimeSessionId": threadID,
		"runtimeEndpoint":  "codex-app-server://" + threadID,
		"runtimeStatus":    "codex_app_server_ready",
		"agentProvider":    "codex_app_server",
		"codexThreadId":    threadID,
	})
}

func agentInterruptReason(payload []byte) string {
	var msg agentv1.AgentInterruptPayload
	if err := protojson.Unmarshal(payload, &msg); err == nil {
		return strings.TrimSpace(msg.GetReason())
	}
	return strings.TrimSpace(string(payload))
}

type limitedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) WriteString(s string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return
	}
	if len(s) >= b.limit {
		b.buf.Reset()
		b.buf.WriteString(s[len(s)-b.limit:])
		return
	}
	if b.buf.Len()+len(s) > b.limit {
		drop := b.buf.Len() + len(s) - b.limit
		current := b.buf.String()
		b.buf.Reset()
		if drop < len(current) {
			b.buf.WriteString(current[drop:])
		}
	}
	b.buf.WriteString(s)
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
