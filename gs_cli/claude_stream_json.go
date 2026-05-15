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
	"strings"
	"sync"
)

type claudeStreamJSONRunner struct {
	cli       *CLI
	cfg       localAgentRunConfig
	cancel    context.CancelFunc
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stderrLog *limitedBuffer

	writeMu sync.Mutex
	mu      sync.Mutex

	messages chan claudeStreamMessage
	done     chan struct{}
	doneOnce sync.Once
	doneErr  error

	sessionID     string
	runtimeStored bool
	interrupted   bool
	toolStarted   map[string]struct{}
}

type claudeStreamMessage struct {
	Type      string              `json:"type"`
	Subtype   string              `json:"subtype"`
	SessionID string              `json:"session_id"`
	IsError   bool                `json:"is_error"`
	Result    string              `json:"result"`
	Error     string              `json:"error"`
	Message   claudeStreamPayload `json:"message"`
	Raw       json.RawMessage     `json:"-"`
}

type claudeStreamPayload struct {
	ID      string               `json:"id"`
	Role    string               `json:"role"`
	Content []claudeContentBlock `json:"content"`
	Raw     json.RawMessage      `json:"-"`
	Extra   map[string]any       `json:"-"`
}

func (p *claudeStreamPayload) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.ID = raw.ID
	p.Role = raw.Role
	p.Raw = append(p.Raw[:0], data...)
	if len(raw.Content) == 0 || string(raw.Content) == "null" {
		return nil
	}
	var blocks []claudeContentBlock
	if err := json.Unmarshal(raw.Content, &blocks); err == nil {
		p.Content = blocks
		return nil
	}
	var text string
	if err := json.Unmarshal(raw.Content, &text); err == nil {
		p.Content = []claudeContentBlock{{Type: "text", Text: text}}
		return nil
	}
	return fmt.Errorf("decode claude message content: %s", strings.TrimSpace(string(raw.Content)))
}

type claudeContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

func newClaudeStreamJSONRunner(ctx context.Context, cli *CLI, cfg localAgentRunConfig) (*claudeStreamJSONRunner, error) {
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, "claude", claudeStreamJSONArgs()...)
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
	r := &claudeStreamJSONRunner{
		cli:         cli,
		cfg:         cfg,
		cancel:      cancel,
		cmd:         cmd,
		stdin:       stdin,
		stderrLog:   newLimitedBuffer(64 * 1024),
		messages:    make(chan claudeStreamMessage, 2048),
		done:        make(chan struct{}),
		toolStarted: make(map[string]struct{}),
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	go r.readLoop(stdout)
	go r.captureStderr(stderr)
	return r, nil
}

func claudeStreamJSONArgs() []string {
	return []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		"--permission-mode", "bypassPermissions",
	}
}

func (r *claudeStreamJSONRunner) RunTurn(ctx context.Context, prompt string) error {
	r.mu.Lock()
	r.interrupted = false
	r.toolStarted = make(map[string]struct{})
	r.mu.Unlock()

	if err := r.writeUserMessage(ctx, prompt); err != nil {
		return r.withStderr("write claude stream input", err)
	}

	finalText := ""
	for {
		select {
		case msg := <-r.messages:
			done, nextFinalText, exitCode, err := r.handleMessage(ctx, finalText, msg)
			finalText = nextFinalText
			if done {
				if appendErr := appendAgentOutput(ctx, r.cli, r.cfg.SessionID, strings.TrimSpace(finalText), "assistant", "output_final", exitCode); appendErr != nil {
					return appendErr
				}
				return err
			}
		case <-r.done:
			if r.wasInterrupted() {
				return appendAgentOutput(ctx, r.cli, r.cfg.SessionID, strings.TrimSpace(finalText), "assistant", "output_final", 130)
			}
			if err := r.getDoneErr(); err != nil {
				return r.withStderr("claude stream-json exited", err)
			}
			return r.withStderr("claude stream-json exited", errors.New("process exited before result"))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (r *claudeStreamJSONRunner) Interrupt(context.Context, string) error {
	r.mu.Lock()
	r.interrupted = true
	r.mu.Unlock()
	return r.Close()
}

func (r *claudeStreamJSONRunner) Close() error {
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

func (r *claudeStreamJSONRunner) handleMessage(ctx context.Context, finalText string, msg claudeStreamMessage) (bool, string, int32, error) {
	if err := r.maybeStoreRuntimeSession(ctx, msg.SessionID); err != nil {
		return true, finalText, 1, err
	}
	switch msg.Type {
	case "system":
		return false, finalText, 0, nil
	case "assistant":
		for _, block := range msg.Message.Content {
			switch block.Type {
			case "text":
				nextFinalText, delta := claudeTextUpdate(finalText, block.Text)
				finalText = nextFinalText
				if delta != "" {
					_ = appendAgentOutput(ctx, r.cli, r.cfg.SessionID, delta, "assistant", "output_delta", 0)
				}
			case "thinking", "thinking_delta":
				if thinking := firstNonEmpty(block.Thinking, block.Text); strings.TrimSpace(thinking) != "" {
					_ = appendAgentThinking(ctx, r.cli, r.cfg.SessionID, thinking, "thinking", "", block.ID)
				}
			case "tool_use":
				r.appendToolStart(ctx, block)
			}
		}
	case "user":
		for _, block := range msg.Message.Content {
			if block.Type == "tool_result" {
				r.appendToolResult(ctx, block)
			}
		}
	case "result":
		if err := r.maybeStoreRuntimeSession(ctx, msg.SessionID); err != nil {
			return true, finalText, 1, err
		}
		if strings.TrimSpace(msg.Result) != "" {
			finalText = msg.Result
		}
		if msg.IsError || msg.Subtype == "error" {
			return true, finalText, 1, claudeResultError(msg)
		}
		return true, finalText, 0, nil
	case "error":
		return true, finalText, 1, claudeResultError(msg)
	}
	return false, finalText, 0, nil
}

func (r *claudeStreamJSONRunner) appendToolStart(ctx context.Context, block claudeContentBlock) {
	id := strings.TrimSpace(block.ID)
	if id == "" {
		id = strings.TrimSpace(block.Name)
	}
	if id == "" {
		return
	}
	r.mu.Lock()
	if _, ok := r.toolStarted[id]; ok {
		r.mu.Unlock()
		return
	}
	r.toolStarted[id] = struct{}{}
	r.mu.Unlock()
	_ = appendAgentJSONEvent(ctx, r.cli, r.cfg.SessionID, "tool", "start", map[string]any{
		"id":    id,
		"tool":  block.Name,
		"input": json.RawMessage(block.Input),
	})
}

func (r *claudeStreamJSONRunner) appendToolResult(ctx context.Context, block claudeContentBlock) {
	id := strings.TrimSpace(block.ToolUseID)
	if id == "" {
		id = strings.TrimSpace(block.ID)
	}
	if id == "" {
		return
	}
	text := claudeToolResultText(block.Content)
	if text != "" {
		_ = appendAgentJSONEvent(ctx, r.cli, r.cfg.SessionID, "tool", "output", map[string]any{
			"id":   id,
			"text": text,
		})
	}
	_ = appendAgentJSONEvent(ctx, r.cli, r.cfg.SessionID, "tool", "end", map[string]any{
		"id":     id,
		"status": "ok",
	})
}

func (r *claudeStreamJSONRunner) writeUserMessage(ctx context.Context, prompt string) error {
	req := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "text",
					"text": prompt,
				},
			},
		},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	errc := make(chan error, 1)
	go func() {
		r.writeMu.Lock()
		defer r.writeMu.Unlock()
		_, err := r.stdin.Write(payload)
		errc <- err
	}()
	select {
	case err := <-errc:
		return err
	case <-r.done:
		if err := r.getDoneErr(); err != nil {
			return err
		}
		return errors.New("claude stream-json exited")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *claudeStreamJSONRunner) readLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		msg, err := decodeClaudeStreamMessage(line)
		if err != nil {
			r.finish(err)
			return
		}
		r.messages <- msg
	}
	if err := scanner.Err(); err != nil {
		r.finish(err)
		return
	}
	r.finish(nil)
}

func (r *claudeStreamJSONRunner) captureStderr(reader io.Reader) {
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

func (r *claudeStreamJSONRunner) maybeStoreRuntimeSession(ctx context.Context, claudeSessionID string) error {
	claudeSessionID = strings.TrimSpace(claudeSessionID)
	if claudeSessionID == "" {
		return nil
	}
	r.mu.Lock()
	if r.runtimeStored && r.sessionID == claudeSessionID {
		r.mu.Unlock()
		return nil
	}
	r.sessionID = claudeSessionID
	r.runtimeStored = true
	r.mu.Unlock()
	return appendClaudeRuntimeSession(ctx, r.cli, r.cfg.SessionID, claudeSessionID)
}

func (r *claudeStreamJSONRunner) finish(err error) {
	r.doneOnce.Do(func() {
		r.mu.Lock()
		r.doneErr = err
		r.mu.Unlock()
		close(r.done)
	})
}

func (r *claudeStreamJSONRunner) getDoneErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.doneErr
}

func (r *claudeStreamJSONRunner) wasInterrupted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.interrupted
}

func (r *claudeStreamJSONRunner) isDone() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

func (r *claudeStreamJSONRunner) withStderr(action string, err error) error {
	tail := strings.TrimSpace(r.stderrLog.String())
	if tail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, tail)
}

func decodeClaudeStreamMessage(line []byte) (claudeStreamMessage, error) {
	var msg claudeStreamMessage
	msg.Raw = append(msg.Raw[:0], line...)
	type alias claudeStreamMessage
	if err := json.Unmarshal(line, (*alias)(&msg)); err != nil {
		return msg, err
	}
	var raw struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(line, &raw) == nil && len(raw.Message) > 0 {
		msg.Message.Raw = append(msg.Message.Raw[:0], raw.Message...)
	}
	return msg, nil
}

func claudeTextUpdate(current, next string) (string, string) {
	if next == "" {
		return current, ""
	}
	if current == "" {
		return next, next
	}
	if strings.HasPrefix(next, current) {
		return next, strings.TrimPrefix(next, current)
	}
	return current + next, next
}

func claudeToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(strings.TrimSpace(block.Text))
			}
		}
		return b.String()
	}
	return strings.TrimSpace(string(raw))
}

func claudeResultError(msg claudeStreamMessage) error {
	if strings.TrimSpace(msg.Error) != "" {
		return errors.New(msg.Error)
	}
	if strings.TrimSpace(msg.Result) != "" {
		return errors.New(msg.Result)
	}
	if strings.TrimSpace(msg.Subtype) != "" {
		return fmt.Errorf("claude result failed: %s", msg.Subtype)
	}
	return errors.New("claude result failed")
}

func appendClaudeRuntimeSession(ctx context.Context, cli *CLI, sessionID, claudeSessionID string) error {
	claudeSessionID = strings.TrimSpace(claudeSessionID)
	if claudeSessionID == "" {
		return nil
	}
	return appendAgentJSONEvent(ctx, cli, sessionID, "control", "runtime_session", map[string]any{
		"runtimeProvider":  "local",
		"runtimeSessionId": claudeSessionID,
		"runtimeEndpoint":  "claude-stream-json://" + claudeSessionID,
		"runtimeStatus":    "claude_stream_json_ready",
		"agentProvider":    "claude_stream_json",
		"claudeSessionId":  claudeSessionID,
	})
}
