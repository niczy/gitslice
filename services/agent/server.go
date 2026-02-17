package agentservice

import (
	"context"
	"strings"

	"github.com/niczy/gitslice/internal/agentsession"
	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/storage"
	agentv1 "github.com/niczy/gitslice/proto/agent"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const timeRFC3339Micro = "2006-01-02T15:04:05.000000Z07:00"

type agentServiceServer struct {
	agentv1.UnimplementedAgentServiceServer
	st  storage.Storage
	svc *agentsession.Service
}

func RegisterGRPCServer(srv *grpc.Server, st storage.Storage, svc *agentsession.Service) {
	agentv1.RegisterAgentServiceServer(srv, &agentServiceServer{st: st, svc: svc})
}

func (s *agentServiceServer) requireUser(ctx context.Context) (string, error) {
	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return "", status.Error(codes.Unauthenticated, "login required")
	}
	if _, err := s.st.EnsureUser(ctx, username); err != nil {
		return "", status.Error(codes.InvalidArgument, "invalid user")
	}
	return username, nil
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

	// Fall back to the slice's configured E2B template when the request
	// does not specify one explicitly.
	e2bTemplateID := req.GetE2BTemplateId()
	if strings.TrimSpace(e2bTemplateID) == "" {
		e2bTemplateID = slice.E2BTemplateID
	}

	session, token, err := s.svc.CreateSession(ctx, userID, agentsession.CreateRequest{
		SliceID:        req.GetSliceId(),
		Provider:       req.GetProvider(),
		E2BTemplateID:  e2bTemplateID,
		E2BRegion:      req.GetE2BRegion(),
		IdleTimeoutSec: int(req.GetIdleTimeoutSec()),
		TTLSec:         int(req.GetTtlSec()),
		Env:            req.GetEnv(),
	})
	if err != nil {
		switch err {
		case storage.ErrAgentSessionConflict:
			return nil, status.Error(codes.AlreadyExists, "active session already exists for slice")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid request")
		default:
			return nil, status.Error(codes.Internal, "failed to create session")
		}
	}

	return &agentv1.CreateSessionResponse{
		SessionId:     session.SessionID,
		SliceId:       session.SliceID,
		Provider:      session.Provider,
		E2BTemplateId: session.E2BTemplateID,
		State:         string(session.State),
		Ws: &agentv1.WSConnectInfo{
			Url:       wsURLFromContext(ctx, session.SessionID),
			Token:     token.Token,
			ExpiresAt: token.ExpiresAt.Format(timeRFC3339Micro),
		},
		CreatedAt:      session.CreatedAt.Format(timeRFC3339Micro),
		IdleTimeoutSec: int32(session.IdleTimeoutSec),
		TtlSec:         int32(session.TTLSec),
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
	resp := &agentv1.GetSessionResponse{
		SessionId:      session.SessionID,
		SliceId:        session.SliceID,
		Provider:       session.Provider,
		E2BSandboxId:   session.E2BSandboxID,
		State:          string(session.State),
		IdleTimeoutSec: int32(session.IdleTimeoutSec),
		TtlSec:         int32(session.TTLSec),
		CreatedAt:      session.CreatedAt.Format(timeRFC3339Micro),
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
	return &agentv1.StopSessionResponse{SessionId: session.SessionID, State: string(session.State)}, nil
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
		})
	}
	return &agentv1.ListEventsResponse{Events: out, NextSeq: nextSeq}, nil
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
