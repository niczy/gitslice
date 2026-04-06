package authresolver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Identity struct {
	Username       string
	SessionID      string
	AuthSource     string
	WorkOSUserID   string
	OrganizationID string
}

func OptionalGRPCIdentity(ctx context.Context, st storage.Storage) (*Identity, error) {
	if token := auth.TokenFromGRPCContext(ctx); token != "" {
		session, err := st.GetAuthSessionByToken(ctx, token)
		if err != nil {
			if errors.Is(err, storage.ErrEntryNotFound) {
				workOSIdentity, workOSErr := resolveWorkOSIdentity(ctx, st, token)
				if workOSErr == nil {
					return workOSIdentity, nil
				}
				if status.Code(workOSErr) != codes.Unimplemented {
					return nil, workOSErr
				}
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

	if username := auth.UsernameFromGRPCContext(ctx); username != "" {
		return &Identity{Username: username, AuthSource: "local"}, nil
	}

	return nil, nil
}

func RequireGRPCIdentity(ctx context.Context, st storage.Storage) (*Identity, error) {
	identity, err := OptionalGRPCIdentity(ctx, st)
	if err != nil {
		return nil, err
	}
	if identity == nil || identity.Username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}
	return identity, nil
}
