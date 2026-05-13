package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	RunnerID       string            `json:"runnerId"`
	Environment    string            `json:"environment"`
	AgentType      string            `json:"agentType"`
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
	RunnerID       string            `json:"runnerId"`
	Environment    string            `json:"environment"`
	AgentType      string            `json:"agentType"`
	Availability   string            `json:"availability"`
	WS             wsConnectResponse `json:"ws"`
	CreatedAt      string            `json:"createdAt"`
	IdleTimeoutSec int               `json:"idleTimeoutSec"`
	TTLSec         int               `json:"ttlSec"`
}

type getAgentSessionResponse struct {
	SessionID      string `json:"sessionId"`
	SliceID        string `json:"sliceId"`
	RunnerID       string `json:"runnerId"`
	Environment    string `json:"environment"`
	AgentType      string `json:"agentType"`
	Availability   string `json:"availability"`
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
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type listEventsResponse struct {
	Events  []eventEnvelopeResponse `json:"events"`
	NextSeq uint64                  `json:"nextSeq"`
}

type listAgentCapabilitiesResponse struct {
	SupportedAgentTypes []string `json:"supportedAgentTypes"`
	DefaultAgentType    string   `json:"defaultAgentType"`
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

	runnerID := strings.TrimSpace(req.RunnerID)
	if runnerID == "" {
		writeError(w, http.StatusBadRequest, "runnerId is required")
		return
	}
	runner, err := a.st.GetAgentRunner(r.Context(), runnerID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown runner")
		return
	}
	if runner.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !httpAgentRunnerOnline(runner, time.Now().UTC()) {
		writeError(w, http.StatusPreconditionFailed, "runner is offline")
		return
	}
	agentType := strings.ToLower(strings.TrimSpace(req.AgentType))
	if agentType == "" {
		agentType = strings.ToLower(strings.TrimSpace(runner.AgentType))
	}
	if agentType == "" {
		agentType = agentsession.DefaultAgentType()
	}
	if runnerAgentType := strings.ToLower(strings.TrimSpace(runner.AgentType)); runnerAgentType != "" && runnerAgentType != agentType {
		writeError(w, http.StatusBadRequest, "agent type does not match runner")
		return
	}

	session, token, err := a.svc.CreateSession(r.Context(), userID, agentsession.CreateRequest{
		SliceID:        req.SliceID,
		RunnerID:       runner.RunnerID,
		AgentType:      agentType,
		Provider:       agentsession.RuntimeProviderLocal,
		IdleTimeoutSec: req.IdleTimeoutSec,
		TTLSec:         req.TTLSec,
		Env:            req.Env,
	})
	if err != nil {
		switch err {
		case storage.ErrAgentSessionConflict:
			writeError(w, http.StatusConflict, "agent session already exists")
		case storage.ErrInvalidInput:
			writeError(w, http.StatusBadRequest, "invalid request")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create session")
		}
		return
	}

	writeJSON(w, http.StatusCreated, createAgentSessionResponse{
		SessionID:   session.SessionID,
		SliceID:     session.SliceID,
		RunnerID:    session.RunnerID,
		Environment: session.EnvironmentName,
		AgentType:   session.AgentType,
		Availability: a.httpAgentSessionAvailability(
			r.Context(),
			session,
			runner,
			time.Now().UTC(),
		),
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

func httpAgentRunnerOnline(runner *models.AgentRunner, now time.Time) bool {
	if runner == nil || runner.Status != models.AgentRunnerStatusOnline || runner.LastHeartbeatAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.Sub(runner.LastHeartbeatAt) <= 30*time.Second
}

func httpAgentSessionIsLocal(session *models.AgentSession) bool {
	if session == nil {
		return false
	}
	provider := strings.TrimSpace(session.Provider)
	if provider == "" {
		provider = strings.TrimSpace(session.RuntimeProvider)
	}
	return strings.EqualFold(provider, agentsession.RuntimeProviderLocal)
}

func (a *AgentSessionsAPI) httpAgentSessionAvailability(ctx context.Context, session *models.AgentSession, runner *models.AgentRunner, now time.Time) string {
	if session == nil {
		return agentsession.SessionAvailabilityUnknown
	}
	if session.State == models.AgentSessionStateFailed {
		return agentsession.SessionAvailabilityFailed
	}
	if !httpAgentSessionIsLocal(session) {
		return agentsession.SessionAvailabilityUnknown
	}
	if runner == nil || !httpAgentRunnerOnline(runner, now) {
		return agentsession.SessionAvailabilityRunnerOffline
	}
	localIDs, reported := httpRunnerLocalSessionIDs(runner.Capabilities)
	if !reported {
		return agentsession.SessionAvailabilityUnknown
	}
	if _, ok := localIDs[session.SessionID]; ok {
		return agentsession.SessionAvailabilityLocal
	}
	if a.httpAgentSessionHasLocalRunnerAttached(ctx, session.SessionID) {
		return agentsession.SessionAvailabilityCloudOnly
	}
	return agentsession.SessionAvailabilityPendingLocal
}

func (a *AgentSessionsAPI) httpAgentSessionHasLocalRunnerAttached(ctx context.Context, sessionID string) bool {
	events, err := a.st.ListAgentSessionEvents(ctx, sessionID, 0, 1000)
	if err != nil {
		return false
	}
	for _, event := range events {
		if event != nil && event.Stream == agentsession.EventStreamStatus && event.Type == "local_runner_attached" {
			return true
		}
	}
	return false
}

func httpRunnerLocalSessionIDs(raw json.RawMessage) (map[string]struct{}, bool) {
	ids := map[string]struct{}{}
	if len(raw) == 0 {
		return ids, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ids, false
	}
	reported := httpJSONBool(payload[agentsession.RunnerCapabilityLocalSessionsReported]) || httpJSONBool(payload["localSessionsReported"])
	for _, key := range []string{agentsession.RunnerCapabilityLocalSessionIDs, "localSessionIds"} {
		values, ok := payload[key]
		if !ok {
			continue
		}
		reported = true
		for _, id := range httpJSONStringList(values) {
			ids[id] = struct{}{}
		}
	}
	return ids, reported
}

func httpJSONBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value bool
	return json.Unmarshal(raw, &value) == nil && value
}

func httpJSONStringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
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
	if sessionID == "capabilities" {
		if len(parts) == 1 && r.Method == http.MethodGet {
			a.listCapabilities(w, r)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
		RunnerID:       session.RunnerID,
		Environment:    session.EnvironmentName,
		AgentType:      session.AgentType,
		IdleTimeoutSec: session.IdleTimeoutSec,
		TTLSec:         session.TTLSec,
		CreatedAt:      session.CreatedAt.Format(timeRFC3339Micro),
	}
	if runner, err := a.st.GetAgentRunner(r.Context(), strings.TrimSpace(session.RunnerID)); err == nil && runner.UserID == userID {
		resp.Availability = a.httpAgentSessionAvailability(r.Context(), session, runner, time.Now().UTC())
	} else {
		resp.Availability = a.httpAgentSessionAvailability(r.Context(), session, nil, time.Now().UTC())
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
			Kind:    event.Kind,
			Payload: event.Payload,
		})
	}
	writeJSON(w, http.StatusOK, listEventsResponse{
		Events:  out,
		NextSeq: nextSeq,
	})
}

func (a *AgentSessionsAPI) listCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireUser(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, listAgentCapabilitiesResponse{
		SupportedAgentTypes: agentsession.SupportedAgentTypes(),
		DefaultAgentType:    agentsession.DefaultAgentType(),
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
