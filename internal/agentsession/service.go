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
	defaultIdleTimeoutSec = 1800
	defaultTTLSec         = 14400
	wsTokenAudience       = "agent-ws"
	wsTokenTTL            = 60 * time.Second
	defaultLifecycleTick  = 1 * time.Second
	defaultStartupTimeout = 90 * time.Second
	defaultAgentType      = "codex"
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

	bootstrapDelay time.Duration
	stopDelay      time.Duration
	lifecycleTick  time.Duration
	startupTimeout time.Duration
	runLoopOnce    sync.Once
}

func NewService(st storage.Storage, wsTokenSecret string) *Service {
	if strings.TrimSpace(wsTokenSecret) == "" {
		wsTokenSecret = "dev-insecure-agent-secret"
	}
	return &Service{
		st:             st,
		wsTokenSecret:  []byte(wsTokenSecret),
		seqHead:        make(map[string]uint64),
		usedNonces:     make(map[string]time.Time),
		replaySeqs:     make(map[string][]uint64),
		maxReplayFrame: 10000,
		bootstrapDelay: 50 * time.Millisecond,
		stopDelay:      50 * time.Millisecond,
		lifecycleTick:  defaultLifecycleTick,
		startupTimeout: defaultStartupTimeout,
	}
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
	req.Provider = strings.TrimSpace(req.Provider)
	if req.Provider == "" {
		req.Provider = "e2b"
	}
	if req.Provider != "e2b" {
		return nil, nil, storage.ErrInvalidInput
	}
	req.E2BTemplateID = strings.TrimSpace(req.E2BTemplateID)
	if req.E2BTemplateID == "" {
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

	go s.bootstrapSession(session.SessionID)

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
		"reason": strings.TrimSpace(reason),
	})
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateStopping)

	go s.finalizeStop(sessionID, userID)
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
					"ttlSec": ttlSec,
				})
				_ = s.AppendStateEvent(ctx, session.SessionID, models.AgentSessionStateStopping)
			}
		}
		go s.finalizeStop(session.SessionID, "system")
		return
	}

	if (session.State == models.AgentSessionStateCreating || session.State == models.AgentSessionStateStarting) &&
		now.Sub(session.CreatedAt) > s.startupTimeout {
		session.State = models.AgentSessionStateFailed
		session.FailureCode = "START_TIMEOUT"
		session.FailureMessage = "session startup timed out"
		session.UpdatedAt = now
		if err := s.st.UpdateAgentSession(ctx, session); err == nil {
			_ = s.AddAudit(ctx, session.SessionID, "system", "session_failed", map[string]any{
				"failureCode": session.FailureCode,
			})
			_ = s.AppendStateEvent(ctx, session.SessionID, models.AgentSessionStateFailed)
		}
		return
	}

	if session.State == models.AgentSessionStateStopping {
		go s.finalizeStop(session.SessionID, "system")
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
	})
	_ = s.AppendStateEvent(ctx, session.SessionID, models.AgentSessionStateIdle)
}

func (s *Service) bootstrapSession(sessionID string) {
	time.Sleep(s.bootstrapDelay)
	ctx := context.Background()

	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	if !session.State.IsActive() {
		return
	}

	now := time.Now().UTC()
	session.State = models.AgentSessionStateStarting
	session.UpdatedAt = now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return
	}
	_ = s.AddAudit(ctx, sessionID, session.UserID, "session_starting", map[string]any{})
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateStarting)

	time.Sleep(s.bootstrapDelay)
	session, err = s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	if !session.State.IsActive() || session.State == models.AgentSessionStateStopping {
		return
	}

	now = time.Now().UTC()
	session.State = models.AgentSessionStateRunning
	session.RuntimeEndpoint = fmt.Sprintf("runtime://%s", sessionID)
	session.StartedAt = &now
	session.LastActivityAt = &now
	session.UpdatedAt = now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return
	}
	_ = s.AddAudit(ctx, sessionID, session.UserID, "session_running", map[string]any{})
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateRunning)
}

func (s *Service) finalizeStop(sessionID, userID string) {
	time.Sleep(s.stopDelay)
	ctx := context.Background()

	session, err := s.st.GetAgentSession(ctx, sessionID)
	if err != nil {
		return
	}
	if session.State == models.AgentSessionStateStopped {
		return
	}

	now := time.Now().UTC()
	session.State = models.AgentSessionStateStopped
	session.UpdatedAt = now
	session.StoppedAt = &now
	if err := s.st.UpdateAgentSession(ctx, session); err != nil {
		return
	}
	_ = s.AddAudit(ctx, sessionID, userID, "session_stopped", map[string]any{})
	_ = s.AppendStateEvent(ctx, sessionID, models.AgentSessionStateStopped)
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
