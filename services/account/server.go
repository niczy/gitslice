package accountservice

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	accountv1 "github.com/niczy/gitslice/proto/account"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	passwordMinLen = 8
	timeRFC3339    = time.RFC3339
)

type accountServiceServer struct {
	accountv1.UnimplementedAccountServiceServer
	st storage.Storage
}

type authIdentity struct {
	username  string
	sessionID string
}

// RegisterGRPCServer registers the account service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	accountv1.RegisterAccountServiceServer(srv, &accountServiceServer{st: st})
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateEmail(email string) bool {
	email = normalizeEmail(email)
	if email == "" {
		return false
	}
	at := strings.Index(email, "@")
	return at > 0 && at < len(email)-1
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func verifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func randomToken(prefix string, bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

func deviceInfoFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, key := range []string{"x-device-info", "user-agent"} {
		vals := md.Get(key)
		if len(vals) > 0 {
			return strings.TrimSpace(vals[0])
		}
	}
	return ""
}

func userToProto(user *models.User) *accountv1.User {
	if user == nil {
		return nil
	}
	return &accountv1.User{
		Id:           user.Username,
		Username:     user.Username,
		Name:         user.Name,
		PrimaryEmail: user.PrimaryEmail,
		CreatedAt:    user.CreatedAt.Format(timeRFC3339),
	}
}

func sessionToProto(session *models.AuthSession, current bool) *accountv1.Session {
	if session == nil {
		return nil
	}
	return &accountv1.Session{
		Id:         session.SessionID,
		UserId:     session.Username,
		LastSeenAt: session.LastSeenAt.Format(timeRFC3339),
		DeviceInfo: session.DeviceInfo,
		Current:    current,
	}
}

func orgToProto(org *models.Organization) *accountv1.Organization {
	if org == nil {
		return nil
	}
	return &accountv1.Organization{
		Id:          org.Slug,
		Slug:        org.Slug,
		Name:        org.Name,
		OwnerUserId: org.CreatedBy,
		CreatedAt:   org.CreatedAt.Format(timeRFC3339),
		UpdatedAt:   org.UpdatedAt.Format(timeRFC3339),
	}
}

func (s *accountServiceServer) resolveIdentity(ctx context.Context) (*authIdentity, error) {
	if token := auth.TokenFromGRPCContext(ctx); token != "" {
		session, err := s.st.GetAuthSessionByToken(ctx, token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid session token")
		}
		_ = s.st.TouchAuthSession(ctx, session.SessionID, time.Now())
		return &authIdentity{username: session.Username, sessionID: session.SessionID}, nil
	}

	username := auth.UsernameFromGRPCContext(ctx)
	if username == "" {
		return nil, status.Error(codes.Unauthenticated, "login required")
	}
	if _, err := s.st.EnsureUser(ctx, username); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid user")
	}
	return &authIdentity{username: username}, nil
}

func (s *accountServiceServer) createSession(ctx context.Context, username string) (*models.AuthSession, string, error) {
	deviceInfo := deviceInfoFromContext(ctx)
	for i := 0; i < 3; i++ {
		sessionID, err := randomToken("sess_", 16)
		if err != nil {
			return nil, "", err
		}
		token, err := randomToken("gs_", 24)
		if err != nil {
			return nil, "", err
		}
		session := &models.AuthSession{
			SessionID:  sessionID,
			Username:   username,
			Token:      token,
			DeviceInfo: deviceInfo,
		}
		err = s.st.CreateAuthSession(ctx, session)
		if err == nil {
			return session, token, nil
		}
		if err != storage.ErrEntryExists {
			return nil, "", err
		}
	}
	return nil, "", status.Error(codes.Aborted, "failed to create session")
}

func (s *accountServiceServer) buildAuthResponse(ctx context.Context, user *models.User, session *models.AuthSession, token string) (*accountv1.AuthResponse, error) {
	orgs, err := s.st.ListOrganizationsForUser(ctx, user.Username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load organizations")
	}
	outOrgs := make([]*accountv1.Organization, 0, len(orgs))
	for _, org := range orgs {
		outOrgs = append(outOrgs, orgToProto(org))
	}
	return &accountv1.AuthResponse{
		User:          userToProto(user),
		Organizations: outOrgs,
		Session:       sessionToProto(session, true),
		AccessToken:   token,
	}, nil
}

func (s *accountServiceServer) Signup(ctx context.Context, req *accountv1.SignupRequest) (*accountv1.AuthResponse, error) {
	username := strings.TrimSpace(req.GetUsername())
	email := normalizeEmail(req.GetEmail())
	password := req.GetPassword()
	name := strings.TrimSpace(req.GetName())

	if !auth.ValidateUsername(username) {
		return nil, status.Error(codes.InvalidArgument, "invalid username")
	}
	if !validateEmail(email) {
		return nil, status.Error(codes.InvalidArgument, "invalid email")
	}
	if len(password) < passwordMinLen {
		return nil, status.Error(codes.InvalidArgument, "password too short")
	}

	hash, err := hashPassword(password)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to hash password")
	}
	if err := s.st.CreateUser(ctx, &models.User{
		Username:     username,
		Name:         name,
		PrimaryEmail: email,
		PasswordHash: hash,
	}); err != nil {
		switch err {
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "account already exists")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid signup request")
		default:
			return nil, status.Error(codes.Internal, "failed to create account")
		}
	}

	user, err := s.st.GetUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	session, token, err := s.createSession(ctx, username)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create session")
	}
	return s.buildAuthResponse(ctx, user, session, token)
}

func (s *accountServiceServer) Login(ctx context.Context, req *accountv1.LoginRequest) (*accountv1.AuthResponse, error) {
	username := strings.TrimSpace(req.GetUsername())
	email := normalizeEmail(req.GetEmail())
	password := req.GetPassword()

	var (
		user *models.User
		err  error
	)

	if password == "" {
		if !auth.ValidateUsername(username) {
			return nil, status.Error(codes.InvalidArgument, "username is required")
		}
		user, err = s.st.EnsureUser(ctx, username)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid user")
		}
	} else {
		switch {
		case username != "":
			user, err = s.st.GetUser(ctx, username)
		case email != "":
			user, err = s.st.GetUserByEmail(ctx, email)
		default:
			return nil, status.Error(codes.InvalidArgument, "username or email is required")
		}
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.Unauthenticated, "invalid credentials")
			}
			return nil, status.Error(codes.Internal, "failed to load account")
		}
		if !verifyPassword(user.PasswordHash, password) {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
	}

	session, token, err := s.createSession(ctx, user.Username)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create session")
	}

	return s.buildAuthResponse(ctx, user, session, token)
}

func (s *accountServiceServer) Logout(ctx context.Context, req *accountv1.LogoutRequest) (*emptypb.Empty, error) {
	sessionID := strings.TrimSpace(req.GetSessionId())
	if sessionID != "" {
		identity, err := s.resolveIdentity(ctx)
		if err != nil {
			return nil, err
		}
		err = s.st.RevokeAuthSession(ctx, identity.username, sessionID)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.NotFound, "session not found")
			}
			return nil, status.Error(codes.Internal, "failed to revoke session")
		}
		return &emptypb.Empty{}, nil
	}

	if token := auth.TokenFromGRPCContext(ctx); token != "" {
		err := s.st.RevokeAuthSessionByToken(ctx, token)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.NotFound, "session not found")
			}
			return nil, status.Error(codes.Internal, "failed to revoke session")
		}
		return &emptypb.Empty{}, nil
	}

	// Explicit dev fallback: keep no-op logout behavior for User headers.
	if _, err := s.resolveIdentity(ctx); err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *accountServiceServer) ResetPassword(ctx context.Context, req *accountv1.ResetPasswordRequest) (*emptypb.Empty, error) {
	newPassword := req.GetNewPassword()
	if len(newPassword) < passwordMinLen {
		return nil, status.Error(codes.InvalidArgument, "password too short")
	}

	username := strings.TrimSpace(req.GetUsername())
	email := normalizeEmail(req.GetEmail())
	var (
		user *models.User
		err  error
	)
	if username != "" {
		user, err = s.st.GetUser(ctx, username)
	} else if email != "" {
		user, err = s.st.GetUserByEmail(ctx, email)
	} else {
		return nil, status.Error(codes.InvalidArgument, "username or email is required")
	}
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "account not found")
		}
		return nil, status.Error(codes.Internal, "failed to load account")
	}

	hash, err := hashPassword(newPassword)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to hash password")
	}
	user.PasswordHash = hash
	if user.PrimaryEmail == "" && validateEmail(email) {
		user.PrimaryEmail = email
	}
	if err := s.st.UpdateUser(ctx, user); err != nil {
		switch err {
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "account not found")
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "email already in use")
		default:
			return nil, status.Error(codes.Internal, "failed to update password")
		}
	}

	// Reset invalidates active sessions.
	sessions, err := s.st.ListAuthSessionsByUser(ctx, user.Username)
	if err == nil {
		for _, session := range sessions {
			_ = s.st.RevokeAuthSession(ctx, user.Username, session.SessionID)
		}
	}

	return &emptypb.Empty{}, nil
}

func (s *accountServiceServer) ListSessions(ctx context.Context, req *accountv1.ListSessionsRequest) (*accountv1.ListSessionsResponse, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := s.st.ListAuthSessionsByUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list sessions")
	}
	out := make([]*accountv1.Session, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, sessionToProto(session, session.SessionID == identity.sessionID))
	}
	return &accountv1.ListSessionsResponse{Sessions: out}, nil
}

func (s *accountServiceServer) DeleteSession(ctx context.Context, req *accountv1.DeleteSessionRequest) (*emptypb.Empty, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(req.GetSessionId())
	if sessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	if err := s.st.RevokeAuthSession(ctx, identity.username, sessionID); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "session not found")
		}
		return nil, status.Error(codes.Internal, "failed to revoke session")
	}
	return &emptypb.Empty{}, nil
}
