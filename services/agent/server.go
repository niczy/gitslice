package agentservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/authresolver"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/ids"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const timeRFC3339Micro = "2006-01-02T15:04:05.000000Z07:00"

const (
	runnerHeartbeatIntervalSec = 10
	runnerOnlineTTL            = 30 * time.Second
)

type agentServiceServer struct {
	agentv1.UnimplementedAgentServiceServer
	st  storage.Storage
	svc *agentsession.Service
}

func RegisterGRPCServer(srv *grpc.Server, st storage.Storage, svc *agentsession.Service) {
	agentv1.RegisterAgentServiceServer(srv, &agentServiceServer{st: st, svc: svc})
}

func (s *agentServiceServer) requireUser(ctx context.Context) (string, error) {
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return "", err
	}
	return identity.Username, nil
}

func (s *agentServiceServer) RegisterRunner(ctx context.Context, req *agentv1.RegisterRunnerRequest) (*agentv1.RegisterRunnerResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	runnerID := strings.TrimSpace(req.GetRunnerId())
	if runnerID == "" {
		runnerID = ids.GenerateAgentRunnerID()
	}
	if existing, err := s.st.GetAgentRunner(ctx, runnerID); err == nil && existing.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "runner id belongs to another user")
	} else if err != nil && err != storage.ErrEntryNotFound {
		return nil, status.Error(codes.Internal, "failed to load runner")
	}
	capabilities, err := normalizeRunnerCapabilities(req.GetCapabilities())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "capabilities must be valid json")
	}
	now := time.Now().UTC()
	runner := &models.AgentRunner{
		RunnerID:        runnerID,
		UserID:          userID,
		Provider:        firstNonEmpty(strings.TrimSpace(req.GetProvider()), agentsession.RuntimeProviderLocal),
		AgentType:       strings.ToLower(strings.TrimSpace(req.GetAgentType())),
		Status:          models.AgentRunnerStatusOnline,
		HostName:        strings.TrimSpace(req.GetHostName()),
		PID:             int(req.GetPid()),
		WorkspaceRoot:   strings.TrimSpace(req.GetWorkspaceRoot()),
		Version:         strings.TrimSpace(req.GetVersion()),
		Capabilities:    capabilities,
		LastHeartbeatAt: now,
		UpdatedAt:       now,
	}
	if err := s.st.UpsertAgentRunner(ctx, runner); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid runner")
	}
	stored, err := s.st.GetAgentRunner(ctx, runnerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load runner")
	}
	if err := s.svc.ReactivateLocalSessionsForRunner(ctx, userID, runnerID); err != nil {
		return nil, status.Error(codes.Internal, "failed to reactivate local sessions")
	}
	return &agentv1.RegisterRunnerResponse{
		Runner:               agentRunnerToProto(stored, time.Now().UTC()),
		HeartbeatIntervalSec: runnerHeartbeatIntervalSec,
	}, nil
}

func (s *agentServiceServer) HeartbeatRunner(ctx context.Context, req *agentv1.HeartbeatRunnerRequest) (*agentv1.HeartbeatRunnerResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	runnerID := strings.TrimSpace(req.GetRunnerId())
	if runnerID == "" {
		return nil, status.Error(codes.InvalidArgument, "runner_id is required")
	}
	runner, err := s.st.GetAgentRunner(ctx, runnerID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "runner not found")
	}
	if runner.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	capabilities, err := normalizeRunnerCapabilities(req.GetCapabilities())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "capabilities must be valid json")
	}
	now := time.Now().UTC()
	runner.Status = models.AgentRunnerStatusOnline
	if statusValue := strings.TrimSpace(req.GetStatus()); statusValue == string(models.AgentRunnerStatusOffline) {
		runner.Status = models.AgentRunnerStatusOffline
	}
	if hostName := strings.TrimSpace(req.GetHostName()); hostName != "" {
		runner.HostName = hostName
	}
	if pid := int(req.GetPid()); pid > 0 {
		runner.PID = pid
	}
	if workspaceRoot := strings.TrimSpace(req.GetWorkspaceRoot()); workspaceRoot != "" {
		runner.WorkspaceRoot = workspaceRoot
	}
	if len(capabilities) > 0 {
		runner.Capabilities = capabilities
	}
	runner.LastHeartbeatAt = now
	runner.UpdatedAt = now
	if err := s.st.UpdateAgentRunner(ctx, runner); err != nil {
		return nil, status.Error(codes.Internal, "failed to update runner")
	}
	if runner.Status == models.AgentRunnerStatusOnline {
		if err := s.svc.ReactivateLocalSessionsForRunner(ctx, userID, runnerID); err != nil {
			return nil, status.Error(codes.Internal, "failed to reactivate local sessions")
		}
	}
	return &agentv1.HeartbeatRunnerResponse{
		Runner:               agentRunnerToProto(runner, now),
		HeartbeatIntervalSec: runnerHeartbeatIntervalSec,
	}, nil
}

func (s *agentServiceServer) ListRunners(ctx context.Context, req *agentv1.ListRunnersRequest) (*agentv1.ListRunnersResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	runners, err := s.st.ListAgentRunnersByUser(ctx, userID, limit)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list runners")
	}
	now := time.Now().UTC()
	out := make([]*agentv1.AgentRunner, 0, len(runners))
	for _, runner := range runners {
		runner, err = s.markRunnerOfflineIfStale(ctx, runner, now)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to update runner")
		}
		if !req.GetIncludeOffline() && !agentRunnerOnline(runner, now) {
			continue
		}
		out = append(out, agentRunnerToProto(runner, now))
	}
	return &agentv1.ListRunnersResponse{Runners: out}, nil
}

func (s *agentServiceServer) UnregisterRunner(ctx context.Context, req *agentv1.UnregisterRunnerRequest) (*agentv1.UnregisterRunnerResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	runner, err := s.st.GetAgentRunner(ctx, strings.TrimSpace(req.GetRunnerId()))
	if err != nil {
		return nil, status.Error(codes.NotFound, "runner not found")
	}
	if runner.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	now := time.Now().UTC()
	runner.Status = models.AgentRunnerStatusOffline
	runner.UpdatedAt = now
	runner.LastHeartbeatAt = now
	if err := s.st.UpdateAgentRunner(ctx, runner); err != nil {
		return nil, status.Error(codes.Internal, "failed to unregister runner")
	}
	return &agentv1.UnregisterRunnerResponse{Runner: agentRunnerToProto(runner, now)}, nil
}

func agentSessionSummary(session *models.AgentSession, availability string) *agentv1.AgentSessionSummary {
	if session == nil {
		return nil
	}
	summary := &agentv1.AgentSessionSummary{
		SessionId:    session.SessionID,
		SliceId:      session.SliceID,
		Provider:     session.Provider,
		CreatedAt:    session.CreatedAt.Format(timeRFC3339Micro),
		Environment:  session.EnvironmentName,
		AgentType:    session.AgentType,
		RunnerId:     session.RunnerID,
		Availability: availability,
	}
	if state := agentSessionStateForRunnerDiscovery(session); state != "" {
		summary.State = state
	}
	if session.LastActivityAt != nil {
		summary.LastActivityAt = session.LastActivityAt.Format(timeRFC3339Micro)
	}
	return summary
}

func agentSessionStateForRunnerDiscovery(session *models.AgentSession) string {
	if session == nil || !agentSessionIsLocal(session) {
		return ""
	}
	if session.State.IsActive() {
		return string(session.State)
	}
	if session.State == models.AgentSessionStateFailed {
		return string(session.State)
	}
	return ""
}

func agentSessionIsLocal(session *models.AgentSession) bool {
	if session == nil {
		return false
	}
	provider := firstNonEmpty(session.Provider, session.RuntimeProvider)
	return strings.EqualFold(provider, agentsession.RuntimeProviderLocal)
}

func (s *agentServiceServer) agentSessionAvailability(ctx context.Context, session *models.AgentSession, runner *models.AgentRunner, now time.Time) string {
	if session == nil {
		return agentsession.SessionAvailabilityUnknown
	}
	if session.State == models.AgentSessionStateFailed {
		return agentsession.SessionAvailabilityFailed
	}
	if !agentSessionIsLocal(session) {
		return agentsession.SessionAvailabilityUnknown
	}
	if runner == nil || !agentRunnerOnline(runner, now) {
		return agentsession.SessionAvailabilityRunnerOffline
	}
	attached := s.agentSessionHasLocalRunnerAttached(ctx, session.SessionID)
	localIDs, reported := runnerLocalSessionIDs(runner.Capabilities)
	if !reported {
		if attached {
			return agentsession.SessionAvailabilityLocal
		}
		return agentsession.SessionAvailabilityPendingLocal
	}
	if _, ok := localIDs[session.SessionID]; ok {
		return agentsession.SessionAvailabilityLocal
	}
	if attached {
		return agentsession.SessionAvailabilityCloudOnly
	}
	return agentsession.SessionAvailabilityPendingLocal
}

func (s *agentServiceServer) agentSessionHasLocalRunnerAttached(ctx context.Context, sessionID string) bool {
	events, err := s.st.ListAgentSessionEvents(ctx, sessionID, 0, 1000)
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

func runnerLocalSessionIDs(raw json.RawMessage) (map[string]struct{}, bool) {
	ids := map[string]struct{}{}
	if len(raw) == 0 {
		return ids, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ids, false
	}
	reported := false
	if values, ok := payload[agentsession.RunnerCapabilityLocalSessionIDs]; ok {
		list, valid := jsonStringList(values)
		reported = valid
		for _, id := range list {
			ids[id] = struct{}{}
		}
	}
	if values, ok := payload["localSessionIds"]; ok {
		list, valid := jsonStringList(values)
		reported = reported || valid
		for _, id := range list {
			ids[id] = struct{}{}
		}
	}
	return ids, reported
}

func jsonStringList(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false
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
	return out, true
}

func agentRunnerOnline(runner *models.AgentRunner, now time.Time) bool {
	if runner == nil || runner.Status != models.AgentRunnerStatusOnline || runner.LastHeartbeatAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.Sub(runner.LastHeartbeatAt) <= runnerOnlineTTL
}

func (s *agentServiceServer) markRunnerOfflineIfStale(ctx context.Context, runner *models.AgentRunner, now time.Time) (*models.AgentRunner, error) {
	if runner == nil || runner.Status != models.AgentRunnerStatusOnline || agentRunnerOnline(runner, now) {
		return runner, nil
	}
	updated := *runner
	if runner.Capabilities != nil {
		updated.Capabilities = append([]byte(nil), runner.Capabilities...)
	}
	updated.Status = models.AgentRunnerStatusOffline
	updated.UpdatedAt = now
	if err := s.st.UpdateAgentRunner(ctx, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *agentServiceServer) runnerForSession(ctx context.Context, userID string, session *models.AgentSession, now time.Time) *models.AgentRunner {
	if session == nil || strings.TrimSpace(session.RunnerID) == "" {
		return nil
	}
	runner, err := s.st.GetAgentRunner(ctx, strings.TrimSpace(session.RunnerID))
	if err != nil || runner.UserID != userID {
		return nil
	}
	runner, err = s.markRunnerOfflineIfStale(ctx, runner, now)
	if err != nil || runner.UserID != userID {
		return nil
	}
	return runner
}

func agentRunnerToProto(runner *models.AgentRunner, now time.Time) *agentv1.AgentRunner {
	if runner == nil {
		return nil
	}
	statusValue := string(runner.Status)
	if !agentRunnerOnline(runner, now) {
		statusValue = string(models.AgentRunnerStatusOffline)
	}
	return &agentv1.AgentRunner{
		RunnerId:        runner.RunnerID,
		Provider:        runner.Provider,
		AgentType:       runner.AgentType,
		Status:          statusValue,
		HostName:        runner.HostName,
		Pid:             int32(runner.PID),
		WorkspaceRoot:   runner.WorkspaceRoot,
		Version:         runner.Version,
		Capabilities:    append([]byte(nil), runner.Capabilities...),
		LastHeartbeatAt: runner.LastHeartbeatAt.Format(timeRFC3339Micro),
		CreatedAt:       runner.CreatedAt.Format(timeRFC3339Micro),
		UpdatedAt:       runner.UpdatedAt.Format(timeRFC3339Micro),
	}
}

func normalizeRunnerCapabilities(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid json")
	}
	return append([]byte(nil), raw...), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *agentServiceServer) ListSessions(ctx context.Context, req *agentv1.ListSessionsRequest) (*agentv1.ListSessionsResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	sliceID := strings.TrimSpace(req.GetSliceId())
	if sliceID == "" {
		return nil, status.Error(codes.InvalidArgument, "slice_id is required")
	}
	slice, err := s.st.GetSlice(ctx, sliceID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "slice not found")
	}
	if !authz.HasSliceViewAccess(slice, userID) {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sessions, err := s.st.ListAgentSessionsBySlice(ctx, sliceID, limit)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list sessions")
	}
	now := time.Now().UTC()
	runners := make(map[string]*models.AgentRunner)
	for _, session := range sessions {
		runnerID := strings.TrimSpace(session.RunnerID)
		if runnerID == "" {
			continue
		}
		if _, ok := runners[runnerID]; ok {
			continue
		}
		runner, err := s.st.GetAgentRunner(ctx, runnerID)
		if err == nil && runner.UserID == userID {
			runner, err = s.markRunnerOfflineIfStale(ctx, runner, now)
		}
		if err == nil && runner.UserID == userID {
			runners[runnerID] = runner
		} else {
			runners[runnerID] = nil
		}
	}
	out := make([]*agentv1.AgentSessionSummary, 0, len(sessions))
	for _, session := range sessions {
		if session.UserID != userID {
			continue
		}
		availability := s.agentSessionAvailability(ctx, session, runners[strings.TrimSpace(session.RunnerID)], now)
		out = append(out, agentSessionSummary(session, availability))
	}
	return &agentv1.ListSessionsResponse{Sessions: out}, nil
}

func (s *agentServiceServer) CreateSession(ctx context.Context, req *agentv1.CreateSessionRequest) (*agentv1.CreateSessionResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	slice, err := s.st.GetSlice(ctx, strings.TrimSpace(req.GetSliceId()))
	if err != nil {
		return nil, status.Error(codes.NotFound, "slice not found")
	}
	if !authz.HasSliceViewAccess(slice, userID) {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}

	runnerID := strings.TrimSpace(req.GetRunnerId())
	if runnerID == "" {
		return nil, status.Error(codes.InvalidArgument, "runner_id is required")
	}
	runner, err := s.st.GetAgentRunner(ctx, runnerID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "unknown runner")
	}
	if runner.UserID != userID {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	now := time.Now().UTC()
	runner, err = s.markRunnerOfflineIfStale(ctx, runner, now)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to update runner")
	}
	if !agentRunnerOnline(runner, now) {
		return nil, status.Error(codes.FailedPrecondition, "runner is offline")
	}

	agentType := strings.ToLower(strings.TrimSpace(req.GetAgentType()))
	runnerAgentType := strings.ToLower(strings.TrimSpace(runner.AgentType))
	if agentType == "" {
		agentType = runnerAgentType
	}
	if agentType == "" {
		agentType = agentsession.DefaultAgentType()
	}
	if runnerAgentType != "" && agentType != runnerAgentType {
		return nil, status.Error(codes.InvalidArgument, "agent type does not match runner")
	}

	session, token, err := s.svc.CreateSession(ctx, userID, agentsession.CreateRequest{
		SliceID:        req.GetSliceId(),
		RunnerID:       runner.RunnerID,
		AgentType:      agentType,
		Provider:       agentsession.RuntimeProviderLocal,
		IdleTimeoutSec: int(req.GetIdleTimeoutSec()),
		TTLSec:         int(req.GetTtlSec()),
		Env:            req.GetEnv(),
	})
	if err != nil {
		switch err {
		case storage.ErrAgentSessionConflict:
			return nil, status.Error(codes.AlreadyExists, "agent session already exists")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid request")
		default:
			return nil, status.Error(codes.Internal, "failed to create session")
		}
	}

	return &agentv1.CreateSessionResponse{
		SessionId:    session.SessionID,
		SliceId:      session.SliceID,
		Provider:     session.Provider,
		RunnerId:     session.RunnerID,
		Availability: s.agentSessionAvailability(ctx, session, runner, now),
		Ws: &agentv1.WSConnectInfo{
			Url:       wsURLFromContext(ctx, session.SessionID),
			Token:     token.Token,
			ExpiresAt: token.ExpiresAt.Format(timeRFC3339Micro),
		},
		CreatedAt:      session.CreatedAt.Format(timeRFC3339Micro),
		IdleTimeoutSec: int32(session.IdleTimeoutSec),
		TtlSec:         int32(session.TTLSec),
		Environment:    session.EnvironmentName,
		AgentType:      session.AgentType,
	}, nil
}

func (s *agentServiceServer) GetSession(ctx context.Context, req *agentv1.GetSessionRequest) (*agentv1.GetSessionResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.svc.GetSessionForUser(ctx, userID, req.GetSessionId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	now := time.Now().UTC()
	runner := s.runnerForSession(ctx, userID, session, now)
	resp := &agentv1.GetSessionResponse{
		SessionId:      session.SessionID,
		SliceId:        session.SliceID,
		Provider:       session.Provider,
		IdleTimeoutSec: int32(session.IdleTimeoutSec),
		TtlSec:         int32(session.TTLSec),
		CreatedAt:      session.CreatedAt.Format(timeRFC3339Micro),
		Environment:    session.EnvironmentName,
		AgentType:      session.AgentType,
		RunnerId:       session.RunnerID,
		Availability:   s.agentSessionAvailability(ctx, session, runner, now),
	}
	if session.LastActivityAt != nil {
		resp.LastActivityAt = session.LastActivityAt.Format(timeRFC3339Micro)
	}
	if hasInternalCaller(ctx) && strings.TrimSpace(session.RuntimeEndpoint) != "" {
		resp.Runtime = &agentv1.RuntimeInfo{Endpoint: session.RuntimeEndpoint}
	}
	return resp, nil
}

func (s *agentServiceServer) StopSession(ctx context.Context, req *agentv1.StopSessionRequest) (*agentv1.StopSessionResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.svc.StopSessionForUser(ctx, userID, req.GetSessionId(), req.GetReason())
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	return &agentv1.StopSessionResponse{SessionId: session.SessionID}, nil
}

func (s *agentServiceServer) MintToken(ctx context.Context, req *agentv1.MintTokenRequest) (*agentv1.MintTokenResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	token, err := s.svc.MintTokenForUser(ctx, userID, req.GetSessionId())
	if err != nil {
		if err == storage.ErrAgentSessionConflict {
			return nil, status.Error(codes.FailedPrecondition, "session is not connectable")
		}
		return nil, status.Error(codes.NotFound, "session not found")
	}
	return &agentv1.MintTokenResponse{
		Url:       wsURLFromContext(ctx, req.GetSessionId()),
		Token:     token.Token,
		ExpiresAt: token.ExpiresAt.Format(timeRFC3339Micro),
	}, nil
}

func (s *agentServiceServer) ListEvents(ctx context.Context, req *agentv1.ListEventsRequest) (*agentv1.ListEventsResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 200
	}
	events, nextSeq, err := s.svc.ListEventsForUser(ctx, userID, req.GetSessionId(), req.GetSinceSeq(), limit)
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	out := make([]*agentv1.EventEnvelope, 0, len(events))
	for _, event := range events {
		out = append(out, &agentv1.EventEnvelope{
			Seq:     event.Seq,
			Ts:      event.TS.Format(timeRFC3339Micro),
			Stream:  event.Stream,
			Type:    event.Type,
			Payload: event.Payload,
			Kind:    event.Kind,
		})
	}
	return &agentv1.ListEventsResponse{Events: out, NextSeq: nextSeq}, nil
}

func (s *agentServiceServer) AppendEvent(ctx context.Context, req *agentv1.AppendEventRequest) (*agentv1.AppendEventResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.svc.GetSessionForUser(ctx, userID, req.GetSessionId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if !session.State.IsActive() {
		return nil, status.Error(codes.FailedPrecondition, "session is not active")
	}
	stream := strings.TrimSpace(req.GetStream())
	eventType := strings.TrimSpace(req.GetType())
	if stream == "" || eventType == "" {
		return nil, status.Error(codes.InvalidArgument, "stream and type are required")
	}
	payload := req.GetPayload()
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	event := &models.AgentSessionEvent{
		SessionID: session.SessionID,
		Stream:    stream,
		Type:      eventType,
		Kind:      strings.TrimSpace(req.GetKind()),
		Payload:   payload,
	}
	if err := s.svc.AppendEvent(ctx, event); err != nil {
		return nil, status.Error(codes.Internal, "failed to append event")
	}
	_ = s.svc.RecordActivity(ctx, session.SessionID)
	return &agentv1.AppendEventResponse{Event: eventEnvelopeFromModel(event)}, nil
}

func (s *agentServiceServer) SendInput(ctx context.Context, req *agentv1.SendInputRequest) (*agentv1.SendInputResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.svc.GetSessionForUser(ctx, userID, req.GetSessionId()); err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if err := s.svc.HandleAgentInput(ctx, req.GetSessionId(), req.GetText()); err != nil {
		if err == storage.ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, "invalid input")
		}
		if err == storage.ErrAgentSessionConflict {
			return nil, status.Error(codes.FailedPrecondition, "session is not accepting input")
		}
		return nil, status.Error(codes.Internal, "failed to send input")
	}
	return &agentv1.SendInputResponse{Accepted: true}, nil
}

func (s *agentServiceServer) SendInterrupt(ctx context.Context, req *agentv1.SendInterruptRequest) (*agentv1.SendInterruptResponse, error) {
	userID, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.svc.GetSessionForUser(ctx, userID, req.GetSessionId()); err != nil {
		return nil, status.Error(codes.NotFound, "session not found")
	}
	if err := s.svc.HandleAgentInterrupt(ctx, req.GetSessionId(), req.GetReason()); err != nil {
		if err == storage.ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, "invalid interrupt")
		}
		if err == storage.ErrAgentSessionConflict {
			return nil, status.Error(codes.FailedPrecondition, "session is not accepting interrupts")
		}
		return nil, status.Error(codes.Internal, "failed to send interrupt")
	}
	return &agentv1.SendInterruptResponse{Accepted: true}, nil
}

func (s *agentServiceServer) ListCapabilities(ctx context.Context, req *agentv1.ListCapabilitiesRequest) (*agentv1.ListCapabilitiesResponse, error) {
	if _, err := s.requireUser(ctx); err != nil {
		return nil, err
	}
	_ = req
	return &agentv1.ListCapabilitiesResponse{
		SupportedAgentTypes:       agentsession.SupportedAgentTypes(),
		DefaultAgentType:          agentsession.DefaultAgentType(),
		SupportedRuntimeProviders: agentsession.SupportedRuntimeProviders(),
		DefaultRuntimeProvider:    s.svc.DefaultRuntimeProviderName(),
	}, nil
}

func wsURLFromContext(ctx context.Context, sessionID string) string {
	scheme := "ws"
	host := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range []string{"x-forwarded-proto", "x-forwarded-protocol"} {
			if vals := md.Get(key); len(vals) > 0 && strings.EqualFold(vals[0], "https") {
				scheme = "wss"
				break
			}
		}
		for _, key := range []string{"x-forwarded-host", "host", ":authority"} {
			if vals := md.Get(key); len(vals) > 0 {
				host = strings.TrimSpace(vals[0])
				if host != "" {
					break
				}
			}
		}
	}
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host + "/ws/sessions/" + sessionID
}

func hasInternalCaller(ctx context.Context) bool {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		vals := md.Get("x-internal-caller")
		return len(vals) > 0 && vals[0] == "1"
	}
	return false
}

func eventEnvelopeFromModel(event *models.AgentSessionEvent) *agentv1.EventEnvelope {
	if event == nil {
		return nil
	}
	return &agentv1.EventEnvelope{
		Seq:     event.Seq,
		Ts:      event.TS.Format(timeRFC3339Micro),
		Stream:  event.Stream,
		Type:    event.Type,
		Payload: event.Payload,
		Kind:    event.Kind,
	}
}
