package agentsession

import (
	"context"
	"crypto/rand"
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
	eventSubscriberBuffer    = 1024
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
	RunnerID        string
	EnvironmentName string
	AgentType       string
	Provider        string
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

	eventSubMu          sync.Mutex
	eventSubscribers    map[string]map[chan *models.AgentSessionEvent]struct{}
	eventNotifyLoopOnce sync.Once

	runnerSubMu       sync.Mutex
	runnerSubscribers map[string]map[chan struct{}]struct{}
	runnerNotifyOnce  sync.Once

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
	svc := &Service{
		st:                       st,
		wsTokenSecret:            []byte(wsTokenSecret),
		seqHead:                  make(map[string]uint64),
		usedNonces:               make(map[string]time.Time),
		replaySeqs:               make(map[string][]uint64),
		maxReplayFrame:           10000,
		eventSubscribers:         make(map[string]map[chan *models.AgentSessionEvent]struct{}),
		runnerSubscribers:        make(map[string]map[chan struct{}]struct{}),
		runtimeProviders:         make(map[string]RuntimeProvider),
		defaultRuntimeProvider:   RuntimeProviderLocal,
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
	svc.runtimeProviders[RuntimeProviderLocal] = NewLocalRuntimeProvider(svc)
	return svc
}

func (s *Service) SetRuntimeProvider(provider RuntimeProvider) {
	s.SetRuntimeProviderFor(RuntimeProviderLocal, provider)
}

func (s *Service) SetRuntimeProviderFor(providerName string, provider RuntimeProvider) {
	providerName = normalizeRuntimeProvider(providerName)
	if providerName == "" {
		providerName = RuntimeProviderLocal
	}
	if !isSupportedRuntimeProvider(providerName) {
		return
	}
	if provider == nil {
		provider = NewLocalRuntimeProvider(nil)
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
	req.RunnerID = strings.TrimSpace(req.RunnerID)
	if req.SliceID == "" {
		return nil, nil, storage.ErrInvalidInput
	}
	if req.RunnerID == "" {
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
		req.Provider = RuntimeProviderLocal
	}
	if !isSupportedRuntimeProvider(req.Provider) {
		return nil, nil, storage.ErrInvalidInput
	}
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
		RunnerID:        req.RunnerID,
		EnvironmentName: req.EnvironmentName,
		AgentType:       req.AgentType,
		UserID:          userID,
		State:           models.AgentSessionStateCreating,
		Provider:        req.Provider,
		IdleTimeoutSec:  req.IdleTimeoutSec,
		TTLSec:          req.TTLSec,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.st.CreateAgentSession(ctx, session); err != nil {
		return nil, nil, err
	}

	_ = s.AddAudit(ctx, session.SessionID, userID, "session_created", map[string]any{
		"sliceId":   session.SliceID,
		"runnerId":  session.RunnerID,
		"agentType": session.AgentType,
		"provider":  session.Provider,
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
	if isDurableLocalSession(session) {
		return s.interruptDurableLocalSession(ctx, session, userID, reason)
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
	case models.AgentSessionStateStopping, models.AgentSessionStateStopped:
		if !isDurableLocalSession(session) {
			return nil, storage.ErrAgentSessionConflict
		}
		if _, err := s.reactivateDurableLocalSessionAs(ctx, session, "mint_token", models.AgentSessionStateIdle); err != nil {
			return nil, err
		}
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
	eventCopy.Payload = cloneRawMessage(event.Payload)
	eventCopy.Seq = s.nextSeq(event.SessionID)
	eventCopy.TS = time.Now().UTC()
	if err := s.applyRuntimeSessionEvent(ctx, &eventCopy); err != nil {
		return err
	}
	if err := s.st.AppendAgentSessionEvent(ctx, &eventCopy); err != nil {
		return err
	}
	if err := s.applyChangesetExportEvent(ctx, &eventCopy); err != nil {
		return err
	}
	event.Seq = eventCopy.Seq
	event.TS = eventCopy.TS
	event.Kind = eventCopy.Kind
	s.rememberSeq(eventCopy.SessionID, eventCopy.Seq)
	s.publishEvent(&eventCopy)
	return nil
}

type runtimeSessionEventPayload struct {
	RuntimeProvider       string `json:"runtimeProvider"`
	RuntimeProviderSnake  string `json:"runtime_provider"`
	RuntimeSessionID      string `json:"runtimeSessionId"`
	RuntimeSessionIDSnake string `json:"runtime_session_id"`
	RuntimeEndpoint       string `json:"runtimeEndpoint"`
	RuntimeEndpointSnake  string `json:"runtime_endpoint"`
	RuntimeStatus         string `json:"runtimeStatus"`
	RuntimeStatusSnake    string `json:"runtime_status"`
	RuntimeErrorCode      string `json:"runtimeErrorCode"`
	RuntimeErrorCodeSnake string `json:"runtime_error_code"`
	Provider              string `json:"provider"`
	SessionID             string `json:"sessionId"`
	ThreadID              string `json:"threadId"`
	CodexThreadID         string `json:"codexThreadId"`
	Endpoint              string `json:"endpoint"`
	Status                string `json:"status"`
	ErrorCode             string `json:"errorCode"`
}

func (s *Service) applyRuntimeSessionEvent(ctx context.Context, event *models.AgentSessionEvent) error {
	if event == nil || event.Stream != EventStreamControl || event.Type != EventTypeRuntime {
		return nil
	}
	var payload runtimeSessionEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return storage.ErrInvalidInput
	}
	runtimeSessionID := firstNonEmpty(
		payload.RuntimeSessionID,
		payload.RuntimeSessionIDSnake,
		payload.CodexThreadID,
		payload.ThreadID,
		payload.SessionID,
	)
	if runtimeSessionID == "" {
		return storage.ErrInvalidInput
	}

	session, err := s.st.GetAgentSession(ctx, event.SessionID)
	if err != nil {
		return err
	}
	if !session.State.IsActive() {
		return storage.ErrAgentSessionConflict
	}

	if provider := firstNonEmpty(payload.RuntimeProvider, payload.RuntimeProviderSnake, payload.Provider); provider != "" {
		session.RuntimeProvider = provider
	}
	session.RuntimeSessionID = runtimeSessionID
	if endpoint := firstNonEmpty(payload.RuntimeEndpoint, payload.RuntimeEndpointSnake, payload.Endpoint); endpoint != "" {
		session.RuntimeEndpoint = endpoint
	}
	if status := firstNonEmpty(payload.RuntimeStatus, payload.RuntimeStatusSnake, payload.Status); status != "" {
		session.RuntimeStatus = status
	}
	session.RuntimeErrorCode = firstNonEmpty(payload.RuntimeErrorCode, payload.RuntimeErrorCodeSnake, payload.ErrorCode)
	session.UpdatedAt = time.Now().UTC()
	return s.st.UpdateAgentSession(ctx, session)
}

type changesetExportCompletedPayload struct {
	ChangesetID          string `json:"changesetId"`
	ChangesetIDSnake     string `json:"changeset_id"`
	ChangesetHash        string `json:"changesetHash"`
	ChangesetHashSnake   string `json:"changeset_hash"`
	SnapshotID           string `json:"snapshotId"`
	SnapshotIDSnake      string `json:"snapshot_id"`
	SnapshotVersion      int32  `json:"snapshotVersion"`
	SnapshotVersionSnake int32  `json:"snapshot_version"`
	SnapshotHash         string `json:"snapshotHash"`
	SnapshotHashSnake    string `json:"snapshot_hash"`
	BaseCommitHash       string `json:"baseCommitHash"`
	BaseCommitHashSnake  string `json:"base_commit_hash"`
	RunnerID             string `json:"runnerId"`
	RunnerIDSnake        string `json:"runner_id"`
	Source               string `json:"source"`
}

func (s *Service) applyChangesetExportEvent(ctx context.Context, event *models.AgentSessionEvent) error {
	if event == nil || event.Stream != EventStreamControl || event.Type != EventTypeChangesetExportCompleted {
		return nil
	}
	var payload changesetExportCompletedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return storage.ErrInvalidInput
	}
	changesetID := firstNonEmpty(payload.ChangesetID, payload.ChangesetIDSnake)
	snapshotID := firstNonEmpty(payload.SnapshotID, payload.SnapshotIDSnake)
	if changesetID == "" || snapshotID == "" {
		return storage.ErrInvalidInput
	}
	session, err := s.st.GetAgentSession(ctx, event.SessionID)
	if err != nil {
		return err
	}
	snapshotVersion := payload.SnapshotVersion
	if snapshotVersion == 0 {
		snapshotVersion = payload.SnapshotVersionSnake
	}
	return s.st.RecordAgentSessionChangeset(ctx, &models.AgentSessionChangeset{
		SessionID:       event.SessionID,
		ChangesetID:     changesetID,
		SnapshotID:      snapshotID,
		SnapshotVersion: snapshotVersion,
		SnapshotHash:    firstNonEmpty(payload.SnapshotHash, payload.SnapshotHashSnake, payload.ChangesetHash, payload.ChangesetHashSnake),
		BaseCommitHash:  firstNonEmpty(payload.BaseCommitHash, payload.BaseCommitHashSnake),
		ExportedFromSeq: event.Seq,
		RunnerID:        firstNonEmpty(payload.RunnerID, payload.RunnerIDSnake, session.RunnerID),
		Source:          firstNonEmpty(payload.Source, "local_export"),
		ExportedAt:      event.TS,
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

func (s *Service) StartEventNotificationLoop(ctx context.Context) {
	s.eventNotifyLoopOnce.Do(func() {
		listener, ok := s.st.(storage.AgentSessionEventListener)
		if !ok {
			return
		}
		go func() {
			err := listener.ListenAgentSessionEventNotifications(ctx, s.handleEventNotification)
			if err != nil && ctx.Err() == nil {
				log.Printf("component=agent_session phase=event_notify_loop error=%v", err)
			}
		}()
	})
}

func (s *Service) StartRunnerNotificationLoop(ctx context.Context) {
	s.runnerNotifyOnce.Do(func() {
		listener, ok := s.st.(storage.AgentRunnerListener)
		if !ok {
			return
		}
		go func() {
			err := listener.ListenAgentRunnerNotifications(ctx, s.handleRunnerNotification)
			if err != nil && ctx.Err() == nil {
				log.Printf("component=agent_runner phase=notify_loop error=%v", err)
			}
		}()
	})
}

func (s *Service) SubscribeEvents(sessionID string) (<-chan *models.AgentSessionEvent, func()) {
	sessionID = strings.TrimSpace(sessionID)
	ch := make(chan *models.AgentSessionEvent, eventSubscriberBuffer)
	if sessionID == "" {
		close(ch)
		return ch, func() {}
	}

	s.eventSubMu.Lock()
	if s.eventSubscribers == nil {
		s.eventSubscribers = make(map[string]map[chan *models.AgentSessionEvent]struct{})
	}
	if s.eventSubscribers[sessionID] == nil {
		s.eventSubscribers[sessionID] = make(map[chan *models.AgentSessionEvent]struct{})
	}
	s.eventSubscribers[sessionID][ch] = struct{}{}
	s.eventSubMu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.eventSubMu.Lock()
			if subscribers := s.eventSubscribers[sessionID]; subscribers != nil {
				if _, ok := subscribers[ch]; ok {
					delete(subscribers, ch)
					close(ch)
				}
				if len(subscribers) == 0 {
					delete(s.eventSubscribers, sessionID)
				}
			}
			s.eventSubMu.Unlock()
		})
	}
	return ch, unsubscribe
}

func (s *Service) SubscribeRunnerUpdates(userID string) (<-chan struct{}, func()) {
	userID = strings.TrimSpace(userID)
	ch := make(chan struct{}, 1)
	if userID == "" {
		close(ch)
		return ch, func() {}
	}

	s.runnerSubMu.Lock()
	if s.runnerSubscribers == nil {
		s.runnerSubscribers = make(map[string]map[chan struct{}]struct{})
	}
	if s.runnerSubscribers[userID] == nil {
		s.runnerSubscribers[userID] = make(map[chan struct{}]struct{})
	}
	s.runnerSubscribers[userID][ch] = struct{}{}
	s.runnerSubMu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.runnerSubMu.Lock()
			if subscribers := s.runnerSubscribers[userID]; subscribers != nil {
				if _, ok := subscribers[ch]; ok {
					delete(subscribers, ch)
					close(ch)
				}
				if len(subscribers) == 0 {
					delete(s.runnerSubscribers, userID)
				}
			}
			s.runnerSubMu.Unlock()
		})
	}
	return ch, unsubscribe
}

func (s *Service) PublishRunnerUpdate(userID string) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return
	}
	s.runnerSubMu.Lock()
	defer s.runnerSubMu.Unlock()
	subscribers := s.runnerSubscribers[userID]
	if len(subscribers) == 0 {
		return
	}
	for ch := range subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Service) NotifyRunnerUpdate(ctx context.Context, userID, runnerID string) {
	userID = strings.TrimSpace(userID)
	runnerID = strings.TrimSpace(runnerID)
	if userID == "" {
		return
	}
	if notifier, ok := s.st.(storage.AgentRunnerNotifier); ok {
		if err := notifier.NotifyAgentRunnerUpdate(ctx, userID, runnerID); err != nil && ctx.Err() == nil {
			log.Printf("component=agent_runner phase=notify user_id=%s runner_id=%s error=%v", userID, runnerID, err)
		}
	}
	s.PublishRunnerUpdate(userID)
}

func (s *Service) handleRunnerNotification(ctx context.Context, notification storage.AgentRunnerNotification) {
	_ = ctx
	if notification.UserID == "" {
		return
	}
	s.PublishRunnerUpdate(notification.UserID)
}

func (s *Service) handleEventNotification(ctx context.Context, notification storage.AgentSessionEventNotification) {
	if notification.SessionID == "" || notification.Seq == 0 {
		return
	}
	events, err := s.st.ListAgentSessionEvents(ctx, notification.SessionID, notification.Seq-1, 1)
	if err != nil {
		log.Printf("component=agent_session phase=event_notify_fetch session_id=%s seq=%d error=%v", notification.SessionID, notification.Seq, err)
		return
	}
	for _, event := range events {
		if event == nil || event.Seq != notification.Seq {
			continue
		}
		s.rememberSeq(event.SessionID, event.Seq)
		s.publishEvent(event)
		return
	}
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
	if session.State.IsActive() && !isDurableLocalSession(session) && now.Sub(session.CreatedAt) > time.Duration(ttlSec)*time.Second {
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

func isDurableLocalSession(session *models.AgentSession) bool {
	if session == nil {
		return false
	}
	return normalizeRuntimeProvider(firstNonEmpty(session.Provider, session.RuntimeProvider)) == RuntimeProviderLocal
}

func (s *Service) reactivateDurableLocalSession(ctx context.Context, session *models.AgentSession, reason string) (*models.AgentSession, error) {
	return s.reactivateDurableLocalSessionAs(ctx, session, reason, models.AgentSessionStateRunning)
}

func (s *Service) reactivateDurableLocalSessionAs(ctx context.Context, session *models.AgentSession, reason string, targetState models.AgentSessionState) (*models.AgentSession, error) {
	if session == nil {
		return nil, storage.ErrAgentSessionNotFound
	}
	if !isDurableLocalSession(session) {
		return session, nil
	}
	if targetState != models.AgentSessionStateRunning && targetState != models.AgentSessionStateIdle {
		targetState = models.AgentSessionStateRunning
	}
	switch session.State {
	case models.AgentSessionStateRunning, models.AgentSessionStateIdle:
		return session, nil
	case models.AgentSessionStateStopping, models.AgentSessionStateStopped:
	default:
		return session, storage.ErrAgentSessionConflict
	}

	now := time.Now().UTC()
	session.State = targetState
	runtimeStatus := strings.TrimSpace(session.RuntimeStatus)
	if runtimeStatus == "" || runtimeStatus == "stopped" {
		runtimeStatus = "waiting_for_local_agent"
	}
	session.RuntimeStatus = runtimeStatus
	session.UpdatedAt = now
	session.LastActivityAt = &now
	session.StoppedAt = nil
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return nil, err
	}
	_ = s.AddAudit(ctx, session.SessionID, session.UserID, "session_reactivated", map[string]any{
		"reason":          strings.TrimSpace(reason),
		"runtimeProvider": session.RuntimeProvider,
		"agentType":       session.AgentType,
		"environment":     session.EnvironmentName,
	})
	_ = s.AppendStateEvent(ctx, session.SessionID, targetState)
	s.enqueueRuntimeBridge(session.SessionID)
	return session, nil
}

func (s *Service) ReactivateLocalSessionsForRunner(ctx context.Context, userID, runnerID string) error {
	userID = strings.TrimSpace(userID)
	runnerID = strings.TrimSpace(runnerID)
	if userID == "" || runnerID == "" {
		return storage.ErrInvalidInput
	}
	sessions, err := s.st.ListAgentSessionsByState(ctx, []models.AgentSessionState{
		models.AgentSessionStateStopping,
		models.AgentSessionStateStopped,
	}, 5000)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session == nil ||
			strings.TrimSpace(session.UserID) != userID ||
			strings.TrimSpace(session.RunnerID) != runnerID ||
			!isDurableLocalSession(session) {
			continue
		}
		if _, err := s.reactivateDurableLocalSessionAs(ctx, session, "runner_online", models.AgentSessionStateIdle); err != nil && err != storage.ErrAgentSessionConflict {
			return err
		}
	}
	return nil
}

func (s *Service) interruptDurableLocalSession(ctx context.Context, session *models.AgentSession, userID, reason string) (*models.AgentSession, error) {
	if session == nil {
		return nil, storage.ErrAgentSessionNotFound
	}
	if session.State == models.AgentSessionStateFailed {
		return session, nil
	}
	if session.State == models.AgentSessionStateStopped || session.State == models.AgentSessionStateStopping {
		reactivated, err := s.reactivateDurableLocalSession(ctx, session, "stop_interrupt")
		if err != nil {
			return nil, err
		}
		session = reactivated
	}
	if session.State != models.AgentSessionStateRunning && session.State != models.AgentSessionStateIdle {
		return nil, storage.ErrAgentSessionConflict
	}

	runtimeProvider, selectedProvider, providerErr := s.runtimeProviderForSession(session)
	if providerErr != nil {
		if selectedProvider != "" {
			session.RuntimeProvider = selectedProvider
			_ = s.st.UpdateAgentSession(ctx, session)
		}
		return nil, providerErr
	}
	if strings.TrimSpace(session.RuntimeProvider) == "" {
		session.RuntimeProvider = selectedProvider
	}
	if err := runtimeProvider.Stop(ctx, session, firstNonEmpty(strings.TrimSpace(reason), "user stop")); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	session.UpdatedAt = now
	session.LastActivityAt = &now
	if session.State == models.AgentSessionStateIdle {
		session.State = models.AgentSessionStateRunning
	}
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return nil, err
	}
	_ = s.AddAudit(ctx, session.SessionID, actorForAudit(userID), "session_interrupt_requested", map[string]any{
		"reason":          strings.TrimSpace(reason),
		"runtimeProvider": session.RuntimeProvider,
		"agentType":       session.AgentType,
		"environment":     session.EnvironmentName,
	})
	return session, nil
}

func (s *Service) stopSessionRuntime(sessionID, actorUserID, reason string) {
	ctx := context.Background()
	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	if isDurableLocalSession(session) {
		_, _ = s.interruptDurableLocalSession(ctx, session, actorForAudit(actorUserID), reason)
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
	providerName := RuntimeProviderLocal
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
		providerName = RuntimeProviderLocal
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
			RuntimeProviderLocal: NewLocalRuntimeProvider(s),
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
		return RuntimeProviderLocal
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

func (s *Service) publishEvent(event *models.AgentSessionEvent) {
	if event == nil || strings.TrimSpace(event.SessionID) == "" {
		return
	}
	s.eventSubMu.Lock()
	defer s.eventSubMu.Unlock()
	subscribers := s.eventSubscribers[event.SessionID]
	if len(subscribers) == 0 {
		return
	}
	for ch := range subscribers {
		select {
		case ch <- cloneAgentSessionEvent(event):
		default:
			close(ch)
			delete(subscribers, ch)
		}
	}
	if len(subscribers) == 0 {
		delete(s.eventSubscribers, event.SessionID)
	}
}

func cloneAgentSessionEvent(event *models.AgentSessionEvent) *models.AgentSessionEvent {
	if event == nil {
		return nil
	}
	out := *event
	out.Payload = cloneRawMessage(event.Payload)
	return &out
}

func cloneRawMessage(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
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
