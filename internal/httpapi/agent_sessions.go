package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type AgentSessionsAPI struct {
	st  storage.Storage
	svc *agentsession.Service
}

func NewAgentSessionsAPI(st storage.Storage, svc *agentsession.Service) *AgentSessionsAPI {
	return &AgentSessionsAPI{st: st, svc: svc}
}

func (a *AgentSessionsAPI) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	username := auth.UsernameFromHTTPRequest(r)
	if username == "" {
		writeError(w, http.StatusUnauthorized, "login required")
		return "", false
	}
	if _, err := a.st.EnsureUser(r.Context(), username); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user")
		return "", false
	}
	return username, true
}

type createAgentSessionRequest struct {
	SliceID        string            `json:"sliceId"`
	Provider       string            `json:"provider"`
	E2BTemplateID  string            `json:"e2bTemplateId"`
	E2BRegion      string            `json:"e2bRegion"`
	IdleTimeoutSec int               `json:"idleTimeoutSec"`
	TTLSec         int               `json:"ttlSec"`
	Env            map[string]string `json:"env"`
}

type wsConnectResponse struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

type createAgentSessionResponse struct {
	SessionID      string            `json:"sessionId"`
	SliceID        string            `json:"sliceId"`
	Provider       string            `json:"provider"`
	E2BTemplateID  string            `json:"e2bTemplateId"`
	State          string            `json:"state"`
	WS             wsConnectResponse `json:"ws"`
	CreatedAt      string            `json:"createdAt"`
	IdleTimeoutSec int               `json:"idleTimeoutSec"`
	TTLSec         int               `json:"ttlSec"`
}

type getAgentSessionResponse struct {
	SessionID      string `json:"sessionId"`
	SliceID        string `json:"sliceId"`
	Provider       string `json:"provider"`
	E2BSandboxID   string `json:"e2bSandboxId,omitempty"`
	State          string `json:"state"`
	LastActivityAt string `json:"lastActivityAt,omitempty"`
	IdleTimeoutSec int    `json:"idleTimeoutSec"`
	TTLSec         int    `json:"ttlSec"`
	CreatedAt      string `json:"createdAt"`
	Runtime        *struct {
		Endpoint string `json:"endpoint"`
	} `json:"runtime,omitempty"`
}

type stopAgentSessionRequest struct {
	Reason string `json:"reason"`
}

type stopAgentSessionResponse struct {
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
}

type tokenResponse struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

type eventEnvelopeResponse struct {
	Seq     uint64          `json:"seq"`
	TS      string          `json:"ts"`
	Stream  string          `json:"stream"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type listEventsResponse struct {
	Events  []eventEnvelopeResponse `json:"events"`
	NextSeq uint64                  `json:"nextSeq"`
}

func (a *AgentSessionsAPI) HandleCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	var req createAgentSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.SliceID = strings.TrimSpace(req.SliceID)
	if req.SliceID == "" {
		writeError(w, http.StatusBadRequest, "sliceId is required")
		return
	}

	slice, err := a.st.GetSlice(r.Context(), req.SliceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slice not found")
		return
	}
	if !authz.HasSliceViewAccess(slice, userID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Fall back to the slice's configured default when the request
	// does not specify one explicitly.
	e2bTemplateID := req.E2BTemplateID
	if strings.TrimSpace(e2bTemplateID) == "" {
		e2bTemplateID = slice.Environment
	}

	session, token, err := a.svc.CreateSession(r.Context(), userID, agentsession.CreateRequest{
		SliceID:        req.SliceID,
		Provider:       req.Provider,
		E2BTemplateID:  e2bTemplateID,
		E2BRegion:      req.E2BRegion,
		IdleTimeoutSec: req.IdleTimeoutSec,
		TTLSec:         req.TTLSec,
		Env:            req.Env,
	})
	if err != nil {
		switch err {
		case storage.ErrAgentSessionConflict:
			writeError(w, http.StatusConflict, "active session already exists for slice")
		case storage.ErrInvalidInput:
			writeError(w, http.StatusBadRequest, "invalid request")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create session")
		}
		return
	}

	writeJSON(w, http.StatusCreated, createAgentSessionResponse{
		SessionID:     session.SessionID,
		SliceID:       session.SliceID,
		Provider:      session.Provider,
		E2BTemplateID: session.E2BTemplateID,
		State:         string(session.State),
		WS: wsConnectResponse{
			URL:       buildWSURL(r, session.SessionID),
			Token:     token.Token,
			ExpiresAt: token.ExpiresAt.Format(timeRFC3339Micro),
		},
		CreatedAt:      session.CreatedAt.Format(timeRFC3339Micro),
		IdleTimeoutSec: session.IdleTimeoutSec,
		TTLSec:         session.TTLSec,
	})
}

func (a *AgentSessionsAPI) HandleItem(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/agent-sessions/")
	path = strings.Trim(path, "/")
	if path == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	parts := strings.Split(path, "/")
	sessionID := parts[0]
	if sessionID == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.getSession(w, r, sessionID)
		return
	}

	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch parts[1] {
	case "stop":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.stopSession(w, r, sessionID)
	case "token":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.mintToken(w, r, sessionID)
	case "events":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.listEvents(w, r, sessionID)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (a *AgentSessionsAPI) getSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	session, err := a.svc.GetSessionForUser(r.Context(), userID, sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	resp := getAgentSessionResponse{
		SessionID:      session.SessionID,
		SliceID:        session.SliceID,
		Provider:       session.Provider,
		E2BSandboxID:   session.E2BSandboxID,
		State:          string(session.State),
		IdleTimeoutSec: session.IdleTimeoutSec,
		TTLSec:         session.TTLSec,
		CreatedAt:      session.CreatedAt.Format(timeRFC3339Micro),
	}
	if session.LastActivityAt != nil {
		resp.LastActivityAt = session.LastActivityAt.Format(timeRFC3339Micro)
	}
	if r.Header.Get("X-Internal-Caller") == "1" && strings.TrimSpace(session.RuntimeEndpoint) != "" {
		resp.Runtime = &struct {
			Endpoint string `json:"endpoint"`
		}{
			Endpoint: session.RuntimeEndpoint,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *AgentSessionsAPI) stopSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	var req stopAgentSessionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	session, err := a.svc.StopSessionForUser(r.Context(), userID, sessionID, req.Reason)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	status := http.StatusAccepted
	if session.State == models.AgentSessionStateStopped || session.State == models.AgentSessionStateFailed {
		status = http.StatusOK
	}
	writeJSON(w, status, stopAgentSessionResponse{
		SessionID: session.SessionID,
		State:     string(session.State),
	})
}

func (a *AgentSessionsAPI) mintToken(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	token, err := a.svc.MintTokenForUser(r.Context(), userID, sessionID)
	if err != nil {
		if err == storage.ErrAgentSessionConflict {
			writeError(w, http.StatusConflict, "session is not connectable")
			return
		}
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		URL:       buildWSURL(r, sessionID),
		Token:     token.Token,
		ExpiresAt: token.ExpiresAt.Format(timeRFC3339Micro),
	})
}

func (a *AgentSessionsAPI) listEvents(w http.ResponseWriter, r *http.Request, sessionID string) {
	userID, ok := a.requireUser(w, r)
	if !ok {
		return
	}
	sinceSeq := uint64(0)
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("sinceSeq")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid sinceSeq")
			return
		}
		sinceSeq = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	events, nextSeq, err := a.svc.ListEventsForUser(r.Context(), userID, sessionID, sinceSeq, limit)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	out := make([]eventEnvelopeResponse, 0, len(events))
	for _, event := range events {
		out = append(out, eventEnvelopeResponse{
			Seq:     event.Seq,
			TS:      event.TS.Format(timeRFC3339Micro),
			Stream:  event.Stream,
			Type:    event.Type,
			Payload: event.Payload,
		})
	}
	writeJSON(w, http.StatusOK, listEventsResponse{
		Events:  out,
		NextSeq: nextSeq,
	})
}

func buildWSURL(r *http.Request, sessionID string) string {
	scheme := "ws"
	if r.TLS != nil {
		scheme = "wss"
	}
	if forwardedProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwardedProto != "" {
		if strings.EqualFold(forwardedProto, "https") {
			scheme = "wss"
		} else if strings.EqualFold(forwardedProto, "http") {
			scheme = "ws"
		}
	}
	return scheme + "://" + r.Host + "/ws/sessions/" + sessionID
}

const timeRFC3339Micro = "2006-01-02T15:04:05.000000Z07:00"
