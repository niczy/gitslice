package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const (
	maxInboundFrameBytes = 256 * 1024
	maxOutboundQueueSize = 8 * 1024 * 1024
	wsEventPollInterval  = 100 * time.Millisecond
	wsEventPollBatch     = 200
	wsReplayBatch        = 1000
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type wsIncomingFrame struct {
	Stream  string          `json:"stream"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type wsOutgoingFrame struct {
	Seq     uint64          `json:"seq,omitempty"`
	TS      string          `json:"ts,omitempty"`
	Stream  string          `json:"stream"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func (a *AgentSessionsAPI) HandleWS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/ws/sessions/")
	sessionID := strings.Trim(path, "/")
	if sessionID == "" {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	lastSeq := uint64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("lastSeq")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid lastSeq")
			return
		}
		lastSeq = parsed
	}

	userID, err := a.svc.ValidateAndConsumeWSToken(token, sessionID)
	if err != nil {
		if err == storage.ErrAgentSessionConflict {
			writeError(w, http.StatusUnauthorized, "token already used")
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	session, err := a.svc.GetSessionForUser(r.Context(), userID, sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	switch session.State {
	case models.AgentSessionStateStarting, models.AgentSessionStateRunning, models.AgentSessionStateIdle:
	default:
		writeError(w, http.StatusConflict, "session unavailable")
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(maxInboundFrameBytes)

	sender := newWSSender(conn)
	defer sender.Close()
	agentsession.ObserveAgentWSConnect()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	currentSeq := lastSeq
	if tail, head, ok := a.svc.ReplayBounds(sessionID); ok && lastSeq+1 < tail {
		agentsession.ObserveAgentWSReplayGap()
		_ = sender.Send(wsOutgoingFrame{
			Stream:  "control",
			Type:    "error",
			Payload: json.RawMessage(`{"code":"REPLAY_GAP","message":"lastSeq is outside replay window"}`),
		})
		currentSeq = head
	} else {
		replaySeq := lastSeq
		for {
			events, nextSeq, err := a.svc.ListEventsForUser(ctx, userID, sessionID, replaySeq, wsReplayBatch)
			if err != nil {
				break
			}
			for _, event := range events {
				if err := sender.Send(outgoingFromEvent(event)); err != nil {
					agentsession.ObserveAgentWSBackpressureClose()
					closeWithReason(conn, websocket.CloseTryAgainLater, "backpressure")
					return
				}
			}
			if len(events) == 0 {
				break
			}
			replaySeq = nextSeq - 1
			currentSeq = replaySeq
			if len(events) < wsReplayBatch {
				break
			}
		}
	}

	readErrCh := make(chan error, 1)
	go func() {
		readErrCh <- a.readWSLoop(ctx, conn, sessionID)
	}()

	ticker := time.NewTicker(wsEventPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-sender.Done():
			if err != nil {
				return
			}
		case err := <-readErrCh:
			if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				closeWithReason(conn, websocket.CloseInternalServerErr, "SESSION_UNAVAILABLE")
			}
			return
		case <-ticker.C:
			events, nextSeq, err := a.svc.ListEventsForUser(ctx, userID, sessionID, currentSeq, wsEventPollBatch)
			if err != nil {
				closeWithReason(conn, websocket.CloseInternalServerErr, "SESSION_UNAVAILABLE")
				return
			}
			for _, event := range events {
				if err := sender.Send(outgoingFromEvent(event)); err != nil {
					agentsession.ObserveAgentWSBackpressureClose()
					closeWithReason(conn, websocket.CloseTryAgainLater, "backpressure")
					return
				}
			}
			if len(events) > 0 {
				currentSeq = nextSeq - 1
			}
		}
	}
}

func (a *AgentSessionsAPI) readWSLoop(ctx context.Context, conn *websocket.Conn, sessionID string) error {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var frame wsIncomingFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		frame.Stream = strings.TrimSpace(frame.Stream)
		frame.Type = strings.TrimSpace(frame.Type)
		if frame.Stream == "" || frame.Type == "" {
			continue
		}
		if err := a.handleIncomingFrame(ctx, sessionID, frame); err != nil {
			return err
		}
	}
}

func (a *AgentSessionsAPI) handleIncomingFrame(ctx context.Context, sessionID string, frame wsIncomingFrame) error {
	switch frame.Stream + "/" + frame.Type {
	case "control/hello":
		return nil
	case "control/ping":
		var payload map[string]any
		_ = json.Unmarshal(frame.Payload, &payload)
		nonce := ""
		if raw, ok := payload["nonce"].(string); ok {
			nonce = raw
		}
		out, _ := json.Marshal(map[string]string{"nonce": nonce})
		return a.svc.AppendEvent(ctx, &models.AgentSessionEvent{
			SessionID: sessionID,
			Stream:    "control",
			Type:      "pong",
			Payload:   out,
		})
	case "pty/stdin":
		var payload struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(frame.Payload, &payload)
		_ = a.svc.RecordActivity(ctx, sessionID)
		out, _ := json.Marshal(map[string]string{"data": payload.Data})
		return a.svc.AppendEvent(ctx, &models.AgentSessionEvent{
			SessionID: sessionID,
			Stream:    "pty",
			Type:      "stdout",
			Payload:   out,
		})
	case "pty/resize":
		return a.svc.RecordActivity(ctx, sessionID)
	case "agent/ready":
		var payload struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(frame.Payload, &payload)
		return a.svc.MarkAgentReady(ctx, sessionID, payload.Version)
	case "agent/output":
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(frame.Payload, &payload)
		return a.svc.AppendMessage(ctx, sessionID, "agent", payload.Text)
	case "agent/output_final":
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(frame.Payload, &payload)
		return a.svc.AppendMessage(ctx, sessionID, "agent", payload.Text)
	case "agent/input":
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(frame.Payload, &payload)
		if err := a.svc.HandleSessionInput(ctx, sessionID, payload.Text); err != nil {
			errorPayload, _ := json.Marshal(map[string]string{
				"code":    "AGENT_INPUT_REJECTED",
				"message": err.Error(),
			})
			return a.svc.AppendEvent(ctx, &models.AgentSessionEvent{
				SessionID: sessionID,
				Stream:    "control",
				Type:      "error",
				Payload:   errorPayload,
			})
		}
		return nil
	case "agent/interrupt":
		var payload struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(frame.Payload, &payload)
		if err := a.svc.HandleAgentInterrupt(ctx, sessionID, payload.Reason); err != nil {
			errorPayload, _ := json.Marshal(map[string]string{
				"code":    "AGENT_INTERRUPT_REJECTED",
				"message": err.Error(),
			})
			return a.svc.AppendEvent(ctx, &models.AgentSessionEvent{
				SessionID: sessionID,
				Stream:    "control",
				Type:      "error",
				Payload:   errorPayload,
			})
		}
		return nil
	default:
		return a.svc.AppendEvent(ctx, &models.AgentSessionEvent{
			SessionID: sessionID,
			Stream:    "control",
			Type:      "error",
			Payload:   json.RawMessage(`{"code":"UNKNOWN_MESSAGE_TYPE"}`),
		})
	}
}

func outgoingFromEvent(event *models.AgentSessionEvent) wsOutgoingFrame {
	return wsOutgoingFrame{
		Seq:     event.Seq,
		TS:      event.TS.Format(timeRFC3339Micro),
		Stream:  event.Stream,
		Type:    event.Type,
		Payload: event.Payload,
	}
}

func closeWithReason(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(2*time.Second),
	)
}

type wsSender struct {
	conn *websocket.Conn
	ch   chan []byte

	doneCh chan error

	mu      sync.Mutex
	pending int
	closed  bool
}

func newWSSender(conn *websocket.Conn) *wsSender {
	s := &wsSender{
		conn:   conn,
		ch:     make(chan []byte, 256),
		doneCh: make(chan error, 1),
	}
	go s.writeLoop()
	return s
}

func (s *wsSender) Send(frame wsOutgoingFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("sender closed")
	}
	if s.pending+len(data) > maxOutboundQueueSize {
		s.mu.Unlock()
		return errors.New("backpressure")
	}
	s.pending += len(data)
	s.mu.Unlock()

	select {
	case s.ch <- data:
		return nil
	default:
		s.mu.Lock()
		s.pending -= len(data)
		s.mu.Unlock()
		return errors.New("backpressure")
	}
}

func (s *wsSender) writeLoop() {
	for payload := range s.ch {
		if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			s.doneCh <- err
			return
		}
		s.mu.Lock()
		s.pending -= len(payload)
		s.mu.Unlock()
	}
	s.doneCh <- nil
}

func (s *wsSender) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.ch)
	s.mu.Unlock()
}

func (s *wsSender) Done() <-chan error {
	return s.doneCh
}
