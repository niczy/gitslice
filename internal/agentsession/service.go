package agentsession

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const (
	defaultIdleTimeoutSec    = 1800
	defaultTTLSec            = 14400
	wsTokenAudience          = "agent-ws"
	wsTokenTTL               = 60 * time.Second
	defaultLifecycleTick     = 1 * time.Second
	defaultStartupTimeout    = 90 * time.Second
	defaultStartMaxRetries   = 2
	defaultStartRetryBackoff = 1 * time.Second
	defaultBridgePollTick    = 750 * time.Millisecond
	defaultAgentType         = "codex"
)

var supportedAgentTypes = map[string]struct{}{
	"codex":  {},
	"claude": {},
}

type CreateRequest struct {
	SliceID         string
	EnvironmentName string
	AgentType       string
	Provider        string
	E2BTemplateID   string
	E2BRegion       string
	IdleTimeoutSec  int
	TTLSec          int
	Env             map[string]string
}

type WSToken struct {
	Token     string
	ExpiresAt time.Time
}

type Service struct {
	st            storage.Storage
	wsTokenSecret []byte

	mu      sync.Mutex
	seqHead map[string]uint64

	nonceMu    sync.Mutex
	usedNonces map[string]time.Time

	replayMu       sync.Mutex
	replaySeqs     map[string][]uint64
	maxReplayFrame int

	runtimeMu              sync.Mutex
	runtimeProviders       map[string]RuntimeProvider
	defaultRuntimeProvider string
	runtimeStartInFlight   map[string]struct{}
	runtimeStopInFlight    map[string]struct{}
	runtimeBridgeInFlight  map[string]struct{}
	runtimeBridgeSyncing   map[string]struct{}
	runtimeBridgeSeq       map[string]uint64

	lifecycleTick            time.Duration
	startupTimeout           time.Duration
	runtimeStopTimeout       time.Duration
	runtimeStartMaxRetries   int
	runtimeStartRetryBackoff time.Duration
	runtimeBridgePollTick    time.Duration
	runLoopOnce              sync.Once
}

func NewService(st storage.Storage, wsTokenSecret string) *Service {
	if strings.TrimSpace(wsTokenSecret) == "" {
		wsTokenSecret = "dev-insecure-agent-secret"
	}
	simulated := newSimulatedRuntimeProvider(50*time.Millisecond, 50*time.Millisecond)
	return &Service{
		st:                       st,
		wsTokenSecret:            []byte(wsTokenSecret),
		seqHead:                  make(map[string]uint64),
		usedNonces:               make(map[string]time.Time),
		replaySeqs:               make(map[string][]uint64),
		maxReplayFrame:           10000,
		runtimeProviders:         map[string]RuntimeProvider{RuntimeProviderE2B: simulated},
		defaultRuntimeProvider:   RuntimeProviderE2B,
		runtimeStartInFlight:     make(map[string]struct{}),
		runtimeStopInFlight:      make(map[string]struct{}),
		runtimeBridgeInFlight:    make(map[string]struct{}),
		runtimeBridgeSyncing:     make(map[string]struct{}),
		runtimeBridgeSeq:         make(map[string]uint64),
		lifecycleTick:            defaultLifecycleTick,
		startupTimeout:           defaultStartupTimeout,
		runtimeStopTimeout:       30 * time.Second,
		runtimeStartMaxRetries:   defaultStartMaxRetries,
		runtimeStartRetryBackoff: defaultStartRetryBackoff,
		runtimeBridgePollTick:    defaultBridgePollTick,
	}
}

func (s *Service) SetRuntimeProvider(provider RuntimeProvider) {
	s.SetRuntimeProviderFor(RuntimeProviderE2B, provider)
}

func (s *Service) SetRuntimeProviderFor(providerName string, provider RuntimeProvider) {
	providerName = normalizeRuntimeProvider(providerName)
	if providerName == "" {
		providerName = RuntimeProviderE2B
	}
	if provider == nil {
		provider = newSimulatedRuntimeProvider(50*time.Millisecond, 50*time.Millisecond)
	}
	s.runtimeMu.Lock()
	if s.runtimeProviders == nil {
		s.runtimeProviders = make(map[string]RuntimeProvider)
	}
	s.runtimeProviders[providerName] = provider
	if s.defaultRuntimeProvider == "" {
		s.defaultRuntimeProvider = providerName
	}
	s.runtimeMu.Unlock()
}

func (s *Service) SetDefaultRuntimeProvider(providerName string) error {
	providerName = normalizeRuntimeProvider(providerName)
	if providerName == "" {
		return storage.ErrInvalidInput
	}
	if !isSupportedRuntimeProvider(providerName) {
		return storage.ErrInvalidInput
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if _, ok := s.runtimeProviders[providerName]; !ok {
		return storage.ErrInvalidInput
	}
	s.defaultRuntimeProvider = providerName
	return nil
}

func SupportedAgentTypes() []string {
	return []string{"codex", "claude"}
}

func DefaultAgentType() string {
	return defaultAgentType
}

func (s *Service) CreateSession(ctx context.Context, userID string, req CreateRequest) (*models.AgentSession, *WSToken, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil, storage.ErrInvalidInput
	}
	req.SliceID = strings.TrimSpace(req.SliceID)
	if req.SliceID == "" {
		return nil, nil, storage.ErrInvalidInput
	}
	req.EnvironmentName = strings.TrimSpace(req.EnvironmentName)
	req.AgentType = strings.ToLower(strings.TrimSpace(req.AgentType))
	if req.AgentType == "" {
		req.AgentType = defaultAgentType
	}
	if _, ok := supportedAgentTypes[req.AgentType]; !ok {
		return nil, nil, storage.ErrInvalidInput
	}
	req.Provider = normalizeRuntimeProvider(req.Provider)
	if req.Provider == "" {
		req.Provider = RuntimeProviderE2B
	}
	if !isSupportedRuntimeProvider(req.Provider) {
		return nil, nil, storage.ErrInvalidInput
	}
	req.E2BTemplateID = strings.TrimSpace(req.E2BTemplateID)
	if req.Provider != RuntimeProviderLocal && req.E2BTemplateID == "" {
		return nil, nil, storage.ErrInvalidInput
	}
	req.E2BRegion = strings.TrimSpace(req.E2BRegion)
	if req.IdleTimeoutSec <= 0 {
		req.IdleTimeoutSec = defaultIdleTimeoutSec
	}
	if req.TTLSec <= 0 {
		req.TTLSec = defaultTTLSec
	}

	now := time.Now().UTC()
	session := &models.AgentSession{
		SessionID:       makeSessionID(),
		SliceID:         req.SliceID,
		EnvironmentName: req.EnvironmentName,
		AgentType:       req.AgentType,
		UserID:          userID,
		State:           models.AgentSessionStateCreating,
		Provider:        req.Provider,
		E2BTemplateID:   req.E2BTemplateID,
		E2BRegion:       req.E2BRegion,
		IdleTimeoutSec:  req.IdleTimeoutSec,
		TTLSec:          req.TTLSec,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.st.CreateAgentSession(ctx, session); err != nil {
		return nil, nil, err
	}

	_ = s.AddAudit(ctx, session.SessionID, userID, "session_created", map[string]any{
		"sliceId":       session.SliceID,
		"agentType":     session.AgentType,
		"provider":      session.Provider,
		"e2bTemplateId": session.E2BTemplateID,
		"e2bRegion":     session.E2BRegion,
	})
	_ = s.AppendStateEvent(ctx, session.SessionID, session.State)
	ObserveAgentSessionCreate(session.AgentType, string(session.State))

	s.enqueueStart(session.SessionID)

	token, err := s.MintToken(ctx, userID, session.SessionID)
	if err != nil {
		return nil, nil, err
	}
	return session, token, nil
}

func (s *Service) GetSession(ctx context.Context, sessionID string) (*models.AgentSession, error) {
	return s.st.GetAgentSession(ctx, sessionID)
}

func (s *Service) GetSessionForUser(ctx context.Context, userID, sessionID string) (*models.AgentSession, error) {
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.UserID != userID {
		return nil, storage.ErrAgentSessionNotFound
	}
	return session, nil
}

func (s *Service) StopSessionForUser(ctx context.Context, userID, sessionID, reason string) (*models.AgentSession, error) {
	session, err := s.GetSessionForUser(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	if session.State == models.AgentSessionStateStopped || session.State == models.AgentSessionStateFailed {
		return session, nil
	}
	if session.State == models.AgentSessionStateStopping {
		return session, nil
	}

	now := time.Now().UTC()
	session.State = models.AgentSessionStateStopping
	session.UpdatedAt = now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return nil, err
	}
	_ = s.AddAudit(ctx, sessionID, userID, "session_stop_requested", map[string]any{
		"reason":      strings.TrimSpace(reason),
		"agentType":   session.AgentType,
		"environment": session.EnvironmentName,
	})
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateStopping)

	s.enqueueStop(sessionID, userID, strings.TrimSpace(reason))
	return session, nil
}

func (s *Service) MintTokenForUser(ctx context.Context, userID, sessionID string) (*WSToken, error) {
	session, err := s.GetSessionForUser(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	switch session.State {
	case models.AgentSessionStateStarting, models.AgentSessionStateRunning, models.AgentSessionStateIdle:
	default:
		return nil, storage.ErrAgentSessionConflict
	}
	return s.MintToken(ctx, userID, sessionID)
}

func (s *Service) MintToken(ctx context.Context, userID, sessionID string) (*WSToken, error) {
	_ = ctx
	now := time.Now().UTC()
	exp := now.Add(wsTokenTTL)
	claims := jwt.MapClaims{
		"sub": userID,
		"sid": sessionID,
		"aud": wsTokenAudience,
		"exp": exp.Unix(),
		"iat": now.Unix(),
		"jti": makeNonceID("jti"),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.wsTokenSecret)
	if err != nil {
		return nil, err
	}
	return &WSToken{
		Token:     signed,
		ExpiresAt: exp,
	}, nil
}

func (s *Service) ValidateAndConsumeWSToken(tokenString, expectedSessionID string) (string, error) {
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return "", storage.ErrInvalidInput
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return s.wsTokenSecret, nil
	})
	if err != nil || token == nil || !token.Valid {
		return "", storage.ErrInvalidInput
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", storage.ErrInvalidInput
	}
	sub := claimString(claims, "sub")
	sid := claimString(claims, "sid")
	jti := claimString(claims, "jti")
	if sub == "" || sid == "" || jti == "" || sid != expectedSessionID {
		return "", storage.ErrInvalidInput
	}
	if !claimAudienceContains(claims["aud"], wsTokenAudience) {
		return "", storage.ErrInvalidInput
	}
	exp, ok := claimUnixTime(claims, "exp")
	if !ok || time.Now().After(exp) {
		return "", storage.ErrInvalidInput
	}
	if !s.consumeNonce(jti, exp) {
		return "", storage.ErrAgentSessionConflict
	}
	return sub, nil
}

func (s *Service) ListEventsForUser(ctx context.Context, userID, sessionID string, sinceSeq uint64, limit int) ([]*models.AgentSessionEvent, uint64, error) {
	if _, err := s.GetSessionForUser(ctx, userID, sessionID); err != nil {
		return nil, 0, err
	}
	events, err := s.st.ListAgentSessionEvents(ctx, sessionID, sinceSeq, limit)
	if err != nil {
		return nil, 0, err
	}
	nextSeq := sinceSeq + 1
	if len(events) > 0 {
		nextSeq = events[len(events)-1].Seq + 1
	}
	return events, nextSeq, nil
}

func (s *Service) AppendStateEvent(ctx context.Context, sessionID string, state models.AgentSessionState) error {
	payload, err := json.Marshal(map[string]string{"state": string(state)})
	if err != nil {
		return err
	}
	return s.AppendEvent(ctx, &models.AgentSessionEvent{
		SessionID: sessionID,
		Stream:    "status",
		Type:      "state",
		Payload:   payload,
	})
}

func (s *Service) AppendEvent(ctx context.Context, event *models.AgentSessionEvent) error {
	if event == nil {
		return storage.ErrInvalidInput
	}
	eventCopy := *event
	eventCopy.Seq = s.nextSeq(event.SessionID)
	eventCopy.TS = time.Now().UTC()
	if err := s.st.AppendAgentSessionEvent(ctx, &eventCopy); err != nil {
		return err
	}
	s.rememberSeq(eventCopy.SessionID, eventCopy.Seq)
	return nil
}

func (s *Service) AppendMessage(ctx context.Context, sessionID, role, text string) error {
	payload, _ := json.Marshal(map[string]string{"role": role, "text": text})
	return s.AppendEvent(ctx, &models.AgentSessionEvent{
		SessionID:   sessionID,
		Stream:      "session",
		Type:        "message",
		Payload:     payload,
		MessageRole: role,
	})
}

func (s *Service) AddAudit(ctx context.Context, sessionID, actorUserID, action string, metadata map[string]any) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return s.st.AddAgentSessionAudit(ctx, &models.AgentSessionAudit{
		SessionID:   sessionID,
		ActorUserID: actorUserID,
		Action:      action,
		Metadata:    data,
		CreatedAt:   time.Now().UTC(),
	})
}

func (s *Service) StartLifecycleLoop(ctx context.Context) {
	s.runLoopOnce.Do(func() {
		go s.lifecycleLoop(ctx)
	})
}

func (s *Service) lifecycleLoop(ctx context.Context) {
	ticker := time.NewTicker(s.lifecycleTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileLifecycle(ctx)
		}
	}
}

func (s *Service) reconcileLifecycle(ctx context.Context) {
	sessions, err := s.st.ListAgentSessionsByState(ctx, []models.AgentSessionState{
		models.AgentSessionStateCreating,
		models.AgentSessionStateStarting,
		models.AgentSessionStateRunning,
		models.AgentSessionStateIdle,
		models.AgentSessionStateStopping,
	}, 5000)
	if err != nil {
		log.Printf("component=agent_session phase=reconcile error=%v", err)
		return
	}
	now := time.Now().UTC()
	for _, session := range sessions {
		s.reconcileSession(ctx, now, session)
	}
}

func (s *Service) reconcileSession(ctx context.Context, now time.Time, session *models.AgentSession) {
	if session == nil {
		return
	}
	ttlSec := session.TTLSec
	if ttlSec <= 0 {
		ttlSec = defaultTTLSec
	}
	if session.State.IsActive() && now.Sub(session.CreatedAt) > time.Duration(ttlSec)*time.Second {
		if session.State != models.AgentSessionStateStopping {
			session.State = models.AgentSessionStateStopping
			session.UpdatedAt = now
			if err := s.st.UpdateAgentSession(ctx, session); err == nil {
				_ = s.AddAudit(ctx, session.SessionID, "system", "session_ttl_expired", map[string]any{
					"ttlSec":      ttlSec,
					"agentType":   session.AgentType,
					"environment": session.EnvironmentName,
				})
				_ = s.AppendStateEvent(ctx, session.SessionID, models.AgentSessionStateStopping)
			}
		}
		s.enqueueStop(session.SessionID, "system", "ttl_expired")
		return
	}

	if (session.State == models.AgentSessionStateCreating || session.State == models.AgentSessionStateStarting) &&
		now.Sub(session.CreatedAt) > s.startupTimeout {
		s.failSession(ctx, session, "system", "START_TIMEOUT", "session startup timed out")
		return
	}

	if session.State == models.AgentSessionStateStopping {
		s.enqueueStop(session.SessionID, "system", "")
		return
	}

	if session.State != models.AgentSessionStateRunning {
		return
	}
	idleSec := session.IdleTimeoutSec
	if idleSec <= 0 {
		idleSec = defaultIdleTimeoutSec
	}
	last := session.CreatedAt
	if session.LastActivityAt != nil {
		last = *session.LastActivityAt
	} else if session.StartedAt != nil {
		last = *session.StartedAt
	}
	if now.Sub(last) <= time.Duration(idleSec)*time.Second {
		return
	}
	session.State = models.AgentSessionStateIdle
	session.UpdatedAt = now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return
	}
	_ = s.AddAudit(ctx, session.SessionID, "system", "session_idle", map[string]any{
		"idleTimeoutSec": idleSec,
		"agentType":      session.AgentType,
		"environment":    session.EnvironmentName,
	})
	_ = s.AppendStateEvent(ctx, session.SessionID, models.AgentSessionStateIdle)
}

func (s *Service) enqueueStart(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !s.markRuntimeStartInFlight(sessionID) {
		return
	}
	go func() {
		defer s.clearRuntimeStartInFlight(sessionID)
		s.startSessionRuntime(sessionID)
	}()
}

func (s *Service) enqueueStop(sessionID, actorUserID, reason string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !s.markRuntimeStopInFlight(sessionID) {
		return
	}
	go func() {
		defer s.clearRuntimeStopInFlight(sessionID)
		s.stopSessionRuntime(sessionID, actorUserID, reason)
	}()
}

func (s *Service) enqueueRuntimeBridge(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !s.markRuntimeBridgeInFlight(sessionID) {
		return
	}
	go func() {
		defer s.clearRuntimeBridgeInFlight(sessionID)
		s.runtimeBridgeLoop(sessionID)
	}()
}

func (s *Service) runtimeBridgeLoop(sessionID string) {
	ticker := time.NewTicker(s.runtimeBridgePollTick)
	defer ticker.Stop()
	for {
		ctx := context.Background()
		session, err := s.st.GetAgentSession(ctx, sessionID)
		if err != nil {
			return
		}
		if session.State != models.AgentSessionStateRunning && session.State != models.AgentSessionStateIdle {
			return
		}

		runtimeProvider, _, providerErr := s.runtimeProviderForSession(session)
		if providerErr != nil {
			return
		}
		if _, ok := runtimeProvider.(RuntimeStreamProvider); !ok {
			return
		}

		syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.syncRuntimeEvents(syncCtx, session, runtimeProvider)
		cancel()
		if err != nil {
			log.Printf("component=agent_session phase=runtime_bridge_poll session_id=%s code=%s error=%q",
				sessionID, runtimeErrorCode(err, "RUNTIME_BRIDGE_SYNC_FAILED"), runtimeErrorMessage(err, "runtime bridge sync failed"))
		}
		<-ticker.C
	}
}

func (s *Service) startSessionRuntime(sessionID string) {
	ctx := context.Background()
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	if !session.State.IsActive() {
		return
	}

	if session.State == models.AgentSessionStateCreating {
		now := time.Now().UTC()
		session.State = models.AgentSessionStateStarting
		session.RuntimeStatus = "starting"
		session.UpdatedAt = now
		if err := s.st.UpdateAgentSession(ctx, session); err != nil {
			return
		}
		_ = s.AddAudit(ctx, sessionID, session.UserID, "session_starting", map[string]any{
			"runtimeProvider": session.Provider,
			"agentType":       session.AgentType,
			"environment":     session.EnvironmentName,
		})
		_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateStarting)
	} else if session.State != models.AgentSessionStateStarting {
		return
	}

	if session.Provider == RuntimeProviderLocal || session.RuntimeProvider == RuntimeProviderLocal {
		return
	}

	if err := validateAgentBinaryForSession(session); err != nil {
		log.Printf("component=agent_session phase=start_validate_binary session_id=%s slice_id=%s user_id=%s environment=%s agent_type=%s state=%s code=%s error=%q",
			session.SessionID, session.SliceID, session.UserID, session.EnvironmentName, session.AgentType, session.State, runtimeErrorCode(err, "AGENT_BINARY_EXEC_FAILED"), runtimeErrorMessage(err, "agent binary validation failed"))
		s.failSession(ctx, session, session.UserID, runtimeErrorCode(err, "AGENT_BINARY_EXEC_FAILED"), runtimeErrorMessage(err, "agent binary validation failed"))
		return
	}

	startedAt := time.Now().UTC()
	maxAttempts := s.runtimeStartMaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	runtimeProvider, selectedProvider, providerErr := s.runtimeProviderForSession(session)
	if providerErr != nil {
		if selectedProvider != "" {
			session.RuntimeProvider = selectedProvider
		}
		s.failSession(ctx, session, session.UserID, runtimeErrorCode(providerErr, "RUNTIME_PROVIDER_UNAVAILABLE"), runtimeErrorMessage(providerErr, "runtime provider is not configured"))
		return
	}
	var (
		result   *RuntimeStartResult
		startErr error
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		startCtx, cancel := context.WithTimeout(ctx, s.startupTimeout)
		result, startErr = runtimeProvider.Start(startCtx, session)
		cancel()
		if startErr == nil {
			ObserveAgentRuntimeRequest(session.AgentType, "success")
			break
		}
		ObserveAgentRuntimeRequest(session.AgentType, "failure")
		code := runtimeErrorCode(startErr, "START_FAILED")
		log.Printf("component=agent_session phase=start_runtime session_id=%s slice_id=%s user_id=%s environment=%s agent_type=%s state=%s attempt=%d code=%s error=%q",
			session.SessionID, session.SliceID, session.UserID, session.EnvironmentName, session.AgentType, session.State, attempt, code, runtimeErrorMessage(startErr, "session startup failed"))
		if attempt >= maxAttempts || !shouldRetryRuntimeStart(startErr) {
			break
		}
		backoff := s.runtimeStartRetryBackoff * time.Duration(1<<(attempt-1))
		if backoff <= 0 {
			backoff = defaultStartRetryBackoff
		}
		time.Sleep(backoff)

		updated, getErr := s.st.GetAgentSession(ctx, sessionID)
		if getErr != nil {
			return
		}
		if !updated.State.IsActive() || updated.State == models.AgentSessionStateStopping {
			return
		}
		session = updated
		runtimeProvider, selectedProvider, providerErr = s.runtimeProviderForSession(session)
		if providerErr != nil {
			startErr = providerErr
			break
		}
	}

	session, getErr := s.st.GetAgentSession(ctx, sessionID)
	if getErr != nil {
		return
	}
	if !session.State.IsActive() || session.State == models.AgentSessionStateStopping {
		return
	}
	if startErr != nil {
		s.failSession(ctx, session, session.UserID, runtimeErrorCode(startErr, "START_FAILED"), runtimeErrorMessage(startErr, "session startup failed"))
		return
	}

	now := time.Now().UTC()
	session.State = models.AgentSessionStateRunning
	session.StartedAt = &now
	session.LastActivityAt = &now
	session.UpdatedAt = now
	session.RuntimeStatus = "ready"
	if result != nil {
		if provider := strings.TrimSpace(result.Provider); provider != "" {
			session.RuntimeProvider = provider
		}
		if runtimeID := strings.TrimSpace(result.SessionID); runtimeID != "" {
			session.RuntimeSessionID = runtimeID
			if strings.TrimSpace(session.E2BSandboxID) == "" {
				session.E2BSandboxID = runtimeID
			}
		}
		if endpoint := strings.TrimSpace(result.Endpoint); endpoint != "" {
			session.RuntimeEndpoint = endpoint
		}
		if status := strings.TrimSpace(result.Status); status != "" {
			session.RuntimeStatus = status
		}
	}
	if strings.TrimSpace(session.RuntimeProvider) == "" {
		session.RuntimeProvider = selectedProvider
	}
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return
	}
	ObserveAgentSessionStartLatency(session.AgentType, now.Sub(startedAt))
	_ = s.AddAudit(ctx, sessionID, session.UserID, "session_running", map[string]any{
		"runtimeProvider":  session.RuntimeProvider,
		"runtimeSessionId": session.RuntimeSessionID,
		"agentType":        session.AgentType,
		"environment":      session.EnvironmentName,
	})
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateRunning)
	log.Printf("component=agent_session phase=session_running session_id=%s slice_id=%s user_id=%s environment=%s agent_type=%s state=%s runtime_provider=%s runtime_session_id=%s",
		session.SessionID, session.SliceID, session.UserID, session.EnvironmentName, session.AgentType, session.State, session.RuntimeProvider, session.RuntimeSessionID)

	s.enqueueRuntimeBridge(sessionID)
}

func (s *Service) stopSessionRuntime(sessionID, actorUserID, reason string) {
	ctx := context.Background()
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	if session.State == models.AgentSessionStateStopped || session.State == models.AgentSessionStateFailed {
		return
	}
	runtimeProvider, selectedProvider, providerErr := s.runtimeProviderForSession(session)
	if providerErr != nil {
		if selectedProvider != "" {
			session.RuntimeProvider = selectedProvider
		}
		log.Printf("component=agent_session phase=stop_runtime session_id=%s slice_id=%s user_id=%s environment=%s agent_type=%s state=%s code=%s error=%q",
			session.SessionID, session.SliceID, actorForAudit(actorUserID), session.EnvironmentName, session.AgentType, session.State, runtimeErrorCode(providerErr, "RUNTIME_PROVIDER_UNAVAILABLE"), runtimeErrorMessage(providerErr, "runtime provider is not configured"))
		s.failSession(ctx, session, actorForAudit(actorUserID), runtimeErrorCode(providerErr, "RUNTIME_PROVIDER_UNAVAILABLE"), runtimeErrorMessage(providerErr, "runtime provider is not configured"))
		return
	}
	if strings.TrimSpace(session.RuntimeProvider) == "" {
		session.RuntimeProvider = selectedProvider
	}

	stopCtx, cancel := context.WithTimeout(ctx, s.runtimeStopTimeout)
	err = runtimeProvider.Stop(stopCtx, session, reason)
	cancel()
	if err != nil {
		session, getErr := s.st.GetAgentSession(ctx, sessionID)
		if getErr != nil {
			return
		}
		if session.State == models.AgentSessionStateStopped || session.State == models.AgentSessionStateFailed {
			return
		}
		log.Printf("component=agent_session phase=stop_runtime session_id=%s slice_id=%s user_id=%s environment=%s agent_type=%s state=%s code=%s error=%q",
			session.SessionID, session.SliceID, actorForAudit(actorUserID), session.EnvironmentName, session.AgentType, session.State, runtimeErrorCode(err, "STOP_FAILED"), runtimeErrorMessage(err, "session stop failed"))
		s.failSession(ctx, session, actorForAudit(actorUserID), runtimeErrorCode(err, "STOP_FAILED"), runtimeErrorMessage(err, "session stop failed"))
		return
	}

	session, err = s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	if session.State == models.AgentSessionStateStopped || session.State == models.AgentSessionStateFailed {
		return
	}

	now := time.Now().UTC()
	session.State = models.AgentSessionStateStopped
	session.RuntimeStatus = "stopped"
	session.UpdatedAt = now
	session.StoppedAt = &now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return
	}
	metadata := map[string]any{}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		metadata["reason"] = trimmed
	}
	metadata["agentType"] = session.AgentType
	metadata["environment"] = session.EnvironmentName
	metadata["runtimeProvider"] = session.RuntimeProvider
	_ = s.AddAudit(ctx, sessionID, actorForAudit(actorUserID), "session_stopped", metadata)
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateStopped)
	log.Printf("component=agent_session phase=session_stopped session_id=%s slice_id=%s user_id=%s environment=%s agent_type=%s state=%s runtime_provider=%s reason=%q",
		session.SessionID, session.SliceID, actorForAudit(actorUserID), session.EnvironmentName, session.AgentType, session.State, session.RuntimeProvider, strings.TrimSpace(reason))
	s.clearRuntimeBridgeSeq(sessionID)
}

func (s *Service) failSession(ctx context.Context, session *models.AgentSession, actorUserID, failureCode, failureMessage string) {
	if session == nil {
		return
	}
	now := time.Now().UTC()
	session.State = models.AgentSessionStateFailed
	session.FailureCode = strings.TrimSpace(failureCode)
	session.FailureMessage = strings.TrimSpace(failureMessage)
	session.RuntimeStatus = "failed"
	session.RuntimeErrorCode = session.FailureCode
	session.UpdatedAt = now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return
	}
	ObserveAgentSessionRuntimeFailure(session.FailureCode)
	_ = s.AddAudit(ctx, session.SessionID, actorForAudit(actorUserID), "session_failed", map[string]any{
		"failureCode":     session.FailureCode,
		"runtimeProvider": session.RuntimeProvider,
		"agentType":       session.AgentType,
		"environment":     session.EnvironmentName,
	})
	_ = s.AppendStateEvent(ctx, session.SessionID, models.AgentSessionStateFailed)
	log.Printf("component=agent_session phase=session_failed session_id=%s slice_id=%s user_id=%s environment=%s agent_type=%s state=%s runtime_provider=%s code=%s error=%q",
		session.SessionID, session.SliceID, actorForAudit(actorUserID), session.EnvironmentName, session.AgentType, session.State, session.RuntimeProvider, session.FailureCode, session.FailureMessage)
	s.clearRuntimeBridgeSeq(session.SessionID)
}

func shouldRetryRuntimeStart(err error) bool {
	code := runtimeErrorCode(err, "")
	if code == "" {
		return false
	}
	for _, marker := range []string{"UNAVAILABLE", "RATE_LIMITED", "REQUEST_FAILED", "TIMEOUT"} {
		if strings.Contains(code, marker) {
			return true
		}
	}
	return false
}

func actorForAudit(actorUserID string) string {
	actorUserID = strings.TrimSpace(actorUserID)
	if actorUserID == "" {
		return "system"
	}
	return actorUserID
}

func (s *Service) runtimeProviderForSession(session *models.AgentSession) (RuntimeProvider, string, error) {
	providerName := RuntimeProviderE2B
	if session != nil {
		providerName = normalizeRuntimeProvider(session.RuntimeProvider)
		if providerName == "" {
			providerName = normalizeRuntimeProvider(session.Provider)
		}
	}
	return s.runtimeProviderByName(providerName)
}

func (s *Service) runtimeProviderByName(providerName string) (RuntimeProvider, string, error) {
	providerName = normalizeRuntimeProvider(providerName)
	s.runtimeMu.Lock()
	if providerName == "" {
		providerName = s.defaultRuntimeProvider
	}
	if providerName == "" {
		providerName = RuntimeProviderE2B
	}
	provider := s.runtimeProviders[providerName]
	s.runtimeMu.Unlock()

	if provider == nil {
		return nil, providerName, &RuntimeError{
			Code:    "RUNTIME_PROVIDER_UNAVAILABLE",
			Message: fmt.Sprintf("runtime provider %q is not configured", providerName),
		}
	}
	return provider, providerName, nil
}

func (s *Service) runtimeProviderSnapshot() map[string]RuntimeProvider {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if len(s.runtimeProviders) == 0 {
		return map[string]RuntimeProvider{
			RuntimeProviderE2B: newSimulatedRuntimeProvider(50*time.Millisecond, 50*time.Millisecond),
		}
	}
	out := make(map[string]RuntimeProvider, len(s.runtimeProviders))
	for provider, runtime := range s.runtimeProviders {
		out[provider] = runtime
	}
	return out
}

func (s *Service) DefaultRuntimeProviderName() string {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.defaultRuntimeProvider == "" {
		return RuntimeProviderE2B
	}
	return s.defaultRuntimeProvider
}

func (s *Service) RuntimeHealthChecks(ctx context.Context) map[string]error {
	providers := s.runtimeProviderSnapshot()
	health := make(map[string]error, len(providers))
	for providerName, runtime := range providers {
		healthProvider, ok := runtime.(RuntimeHealthProvider)
		if !ok {
			health[providerName] = nil
			continue
		}
		health[providerName] = healthProvider.HealthCheck(ctx)
	}
	return health
}

func (s *Service) RuntimeHealthCheck(ctx context.Context) error {
	health := s.RuntimeHealthChecks(ctx)
	defaultProvider := s.DefaultRuntimeProviderName()
	if err, ok := health[defaultProvider]; ok {
		return err
	}
	for _, err := range health {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) syncRuntimeEvents(ctx context.Context, session *models.AgentSession, provider RuntimeProvider) error {
	if session == nil || provider == nil {
		return nil
	}
	if !s.markRuntimeBridgeSyncing(session.SessionID) {
		return nil
	}
	defer s.clearRuntimeBridgeSyncing(session.SessionID)

	streamProvider, ok := provider.(RuntimeStreamProvider)
	if !ok {
		return nil
	}

	sinceSeq := s.runtimeBridgeSeqValue(session.SessionID)
	events, nextSeq, err := streamProvider.StreamEvents(ctx, session, sinceSeq, 200)
	if err != nil {
		return err
	}

	maxSeq := sinceSeq
	appended := 0
	for _, runtimeEvent := range events {
		if runtimeEvent.RuntimeSeq <= sinceSeq {
			continue
		}
		payload := runtimeEvent.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		stream := strings.TrimSpace(runtimeEvent.Stream)
		if stream == "" {
			stream = "runtime"
		}
		eventType := strings.TrimSpace(runtimeEvent.Type)
		if eventType == "" {
			eventType = "event"
		}

		if err := s.AppendEvent(ctx, &models.AgentSessionEvent{
			SessionID: session.SessionID,
			Stream:    stream,
			Type:      eventType,
			Payload:   payload,
		}); err != nil {
			return err
		}
		appended++
		if runtimeEvent.RuntimeSeq > maxSeq {
			maxSeq = runtimeEvent.RuntimeSeq
		}
	}
	if nextSeq > maxSeq {
		maxSeq = nextSeq
	}
	if maxSeq > sinceSeq {
		s.setRuntimeBridgeSeqValue(session.SessionID, maxSeq)
	}
	if appended > 0 {
		_ = s.RecordActivity(ctx, session.SessionID)
	}
	return nil
}

func (s *Service) markRuntimeStartInFlight(sessionID string) bool {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if _, exists := s.runtimeStartInFlight[sessionID]; exists {
		return false
	}
	s.runtimeStartInFlight[sessionID] = struct{}{}
	return true
}

func (s *Service) markRuntimeBridgeInFlight(sessionID string) bool {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if _, exists := s.runtimeBridgeInFlight[sessionID]; exists {
		return false
	}
	s.runtimeBridgeInFlight[sessionID] = struct{}{}
	return true
}

func (s *Service) markRuntimeBridgeSyncing(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeBridgeSyncing == nil {
		s.runtimeBridgeSyncing = make(map[string]struct{})
	}
	if _, exists := s.runtimeBridgeSyncing[sessionID]; exists {
		return false
	}
	s.runtimeBridgeSyncing[sessionID] = struct{}{}
	return true
}

func (s *Service) clearRuntimeBridgeSyncing(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.runtimeMu.Lock()
	delete(s.runtimeBridgeSyncing, sessionID)
	s.runtimeMu.Unlock()
}

func (s *Service) clearRuntimeBridgeInFlight(sessionID string) {
	s.runtimeMu.Lock()
	delete(s.runtimeBridgeInFlight, sessionID)
	s.runtimeMu.Unlock()
}

func (s *Service) runtimeBridgeSeqValue(sessionID string) uint64 {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	return s.runtimeBridgeSeq[sessionID]
}

func (s *Service) setRuntimeBridgeSeqValue(sessionID string, value uint64) {
	if value == 0 {
		return
	}
	s.runtimeMu.Lock()
	if s.runtimeBridgeSeq == nil {
		s.runtimeBridgeSeq = make(map[string]uint64)
	}
	if value > s.runtimeBridgeSeq[sessionID] {
		s.runtimeBridgeSeq[sessionID] = value
	}
	s.runtimeMu.Unlock()
}

func (s *Service) clearRuntimeBridgeSeq(sessionID string) {
	s.runtimeMu.Lock()
	delete(s.runtimeBridgeSeq, sessionID)
	s.runtimeMu.Unlock()
}

func (s *Service) clearRuntimeStartInFlight(sessionID string) {
	s.runtimeMu.Lock()
	delete(s.runtimeStartInFlight, sessionID)
	s.runtimeMu.Unlock()
}

func (s *Service) markRuntimeStopInFlight(sessionID string) bool {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if _, exists := s.runtimeStopInFlight[sessionID]; exists {
		return false
	}
	s.runtimeStopInFlight[sessionID] = struct{}{}
	return true
}

func (s *Service) clearRuntimeStopInFlight(sessionID string) {
	s.runtimeMu.Lock()
	delete(s.runtimeStopInFlight, sessionID)
	s.runtimeMu.Unlock()
}

func (s *Service) RecordActivity(ctx context.Context, sessionID string) error {
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	session.LastActivityAt = &now
	session.UpdatedAt = now
	stateChanged := false
	if session.State == models.AgentSessionStateIdle {
		session.State = models.AgentSessionStateRunning
		stateChanged = true
	}
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return err
	}
	if stateChanged {
		_ = s.AppendStateEvent(ctx, sessionID, session.State)
	}
	return nil
}

func (s *Service) MarkAgentReady(ctx context.Context, sessionID, version string) error {
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.State != models.AgentSessionStateStarting && session.State != models.AgentSessionStateRunning {
		return storage.ErrAgentSessionConflict
	}
	if session.CIRunnerID == "" {
		s.registerCIRunnerForSession(ctx, session)
	}
	now := time.Now().UTC()
	session.State = models.AgentSessionStateRunning
	session.RuntimeStatus = "ready"
	session.LastActivityAt = &now
	session.UpdatedAt = now
	session.StartedAt = &now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return err
	}
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateRunning)
	_ = s.AddAudit(ctx, sessionID, session.UserID, "session_running", map[string]any{
		"agent_version": version,
	})
	return nil
}

func (s *Service) registerCIRunnerForSession(ctx context.Context, session *models.AgentSession) {
	runnerToken := makeNonceID("gsrunner")
	runnerID := makeNonceID("ci_runner")
	tokenHash := sha256Hex(runnerToken)

	homeID := session.SliceID
	if !strings.HasPrefix(homeID, "home_") {
		homeID = "home_" + homeID
	}

	runner := &storage.CIRunner{
		ID:             runnerID,
		HomeID:         homeID,
		Name:           fmt.Sprintf("agent-%s-%s", session.AgentType, session.UserID),
		Pool:           "agents",
		Labels:         []string{"agent-type:" + session.AgentType, "slice:" + session.SliceID, "user:" + session.UserID},
		Executor:       "local-agent",
		Status:         "online",
		TokenHash:      tokenHash,
		Version:        "1.0",
		LastSeenAt:     ptrTime(time.Now()),
		AgentSessionID: session.SessionID,
	}
	if err := s.st.CreateCIRunner(ctx, runner); err != nil {
		log.Printf("component=agent_session phase=register_ci_runner session_id=%s error=%v", session.SessionID, err)
		return
	}
	session.CIRunnerID = runnerID
	log.Printf("component=agent_session phase=ci_runner_registered session_id=%s runner_id=%s pool=%s", session.SessionID, runnerID, runner.Pool)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func ptrTime(t time.Time) *time.Time { return &t }

func (s *Service) HandleSessionInput(ctx context.Context, sessionID, text string) error {
	sessionID = strings.TrimSpace(sessionID)
	text = strings.TrimSpace(text)
	if sessionID == "" || text == "" {
		return storage.ErrInvalidInput
	}
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.State != models.AgentSessionStateRunning && session.State != models.AgentSessionStateIdle {
		return storage.ErrAgentSessionConflict
	}

	runtimeProvider, _, providerErr := s.runtimeProviderForSession(session)
	if providerErr != nil {
		return providerErr
	}

	if _, isLocal := runtimeProvider.(*localRuntimeProvider); isLocal {
		_ = s.AppendMessage(ctx, sessionID, "user", text)
		_ = s.RecordActivity(ctx, sessionID)
		return nil
	}

	_ = s.AppendMessage(ctx, sessionID, "user", text)
	return s.HandleAgentInput(ctx, sessionID, text)
}

func (s *Service) ReplayBounds(sessionID string) (tail uint64, head uint64, ok bool) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	seqs := s.replaySeqs[sessionID]
	if len(seqs) == 0 {
		return 0, 0, false
	}
	return seqs[0], seqs[len(seqs)-1], true
}

func (s *Service) nextSeq(sessionID string) uint64 {
	s.mu.Lock()
	if seq, ok := s.seqHead[sessionID]; ok {
		seq++
		s.seqHead[sessionID] = seq
		s.mu.Unlock()
		return seq
	}
	s.mu.Unlock()

	seed := s.seedSeqHead(sessionID)

	s.mu.Lock()
	if existing, ok := s.seqHead[sessionID]; ok {
		seed = existing
	}
	seed++
	s.seqHead[sessionID] = seed
	s.mu.Unlock()
	return seed
}

func (s *Service) seedSeqHead(sessionID string) uint64 {
	events, err := s.st.ListAgentSessionEvents(context.Background(), sessionID, 0, s.maxReplayFrame)
	if err != nil || len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

func (s *Service) consumeNonce(jti string, exp time.Time) bool {
	now := time.Now()
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	for key, expiresAt := range s.usedNonces {
		if expiresAt.Before(now) {
			delete(s.usedNonces, key)
		}
	}
	if _, exists := s.usedNonces[jti]; exists {
		return false
	}
	s.usedNonces[jti] = exp
	return true
}

func (s *Service) rememberSeq(sessionID string, seq uint64) {
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	seqs := append(s.replaySeqs[sessionID], seq)
	if len(seqs) > s.maxReplayFrame {
		seqs = seqs[len(seqs)-s.maxReplayFrame:]
	}
	s.replaySeqs[sessionID] = seqs
}

func claimString(claims jwt.MapClaims, key string) string {
	raw, ok := claims[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func claimAudienceContains(raw any, want string) bool {
	switch v := raw.(type) {
	case string:
		return strings.EqualFold(v, want)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.EqualFold(s, want) {
				return true
			}
		}
	case []string:
		for _, item := range v {
			if strings.EqualFold(item, want) {
				return true
			}
		}
	}
	return false
}

func claimUnixTime(claims jwt.MapClaims, key string) (time.Time, bool) {
	raw, ok := claims[key]
	if !ok {
		return time.Time{}, false
	}
	switch v := raw.(type) {
	case float64:
		return time.Unix(int64(v), 0), true
	case int64:
		return time.Unix(v, 0), true
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(i, 0), true
	}
	return time.Time{}, false
}

func makeSessionID() string {
	return makeNonceID("sess")
}

func makeNonceID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
}
