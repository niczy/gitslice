package authresolver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func OptionalHTTPIdentity(ctx context.Context, st storage.Storage, r *http.Request) (*Identity, error) {
	if token := auth.TokenFromHTTPRequest(r); token != "" {
		return resolveSessionToken(ctx, st, token)
	}
	if token := auth.TokenFromHTTPBasicPassword(r); token != "" {
		return resolveSessionToken(ctx, st, token)
	}
	if username := auth.UsernameFromHTTPRequest(r); username != "" {
		return &Identity{Username: username, AuthSource: "local"}, nil
	}
	return nil, nil
}

func RequireHTTPIdentity(ctx context.Context, st storage.Storage, r *http.Request) (*Identity, error) {
	identity, err := OptionalHTTPIdentity(ctx, st, r)
	if err != nil {
		return nil, err
	}
	if identity == nil || identity.Username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}
	return identity, nil
}

func resolveSessionToken(ctx context.Context, st storage.Storage, token string) (*Identity, error) {
	session, err := st.GetAuthSessionByToken(ctx, token)
	if err != nil {
		if errors.Is(err, storage.ErrEntryNotFound) {
			return nil, status.Error(codes.Unauthenticated, "invalid session token")
		}
		return nil, status.Error(codes.Internal, fmt.Sprintf("failed to resolve session token: %v", err))
	}
	_ = st.TouchAuthSession(ctx, session.SessionID, time.Now())
	return &Identity{
		Username:   session.Username,
		SessionID:  session.SessionID,
		AuthSource: "local",
	}, nil
}
