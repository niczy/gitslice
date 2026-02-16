package agentsession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
)

type CreateRequest struct {
	SliceID        string
	Provider       string
	E2BTemplateID  string
	E2BRegion      string
	IdleTimeoutSec int
	TTLSec         int
	Env            map[string]string
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

	bootstrapDelay time.Duration
	stopDelay      time.Duration
}

func NewService(st storage.Storage, wsTokenSecret string) *Service {
	if strings.TrimSpace(wsTokenSecret) == "" {
		wsTokenSecret = "dev-insecure-agent-secret"
	}
	return &Service{
		st:             st,
		wsTokenSecret:  []byte(wsTokenSecret),
		seqHead:        make(map[string]uint64),
		bootstrapDelay: 50 * time.Millisecond,
		stopDelay:      50 * time.Millisecond,
	}
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
		SessionID:      makeSessionID(),
		SliceID:        req.SliceID,
		UserID:         userID,
		State:          models.AgentSessionStateCreating,
		Provider:       req.Provider,
		E2BTemplateID:  req.E2BTemplateID,
		E2BRegion:      req.E2BRegion,
		IdleTimeoutSec: req.IdleTimeoutSec,
		TTLSec:         req.TTLSec,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.st.CreateAgentSession(ctx, session); err != nil {
		return nil, nil, err
	}

	_ = s.AddAudit(ctx, session.SessionID, userID, "session_created", map[string]any{
		"sliceId":       session.SliceID,
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
	return s.st.AppendAgentSessionEvent(ctx, &eventCopy)
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

func (s *Service) nextSeq(sessionID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.seqHead[sessionID] + 1
	s.seqHead[sessionID] = next
	return next
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
