package accountservice

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/homeslice"
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
	passwordMinLen          = 8
	timeRFC3339             = time.RFC3339
	accessTokenTTL          = 15 * time.Minute
	refreshTokenTTL         = 30 * 24 * time.Hour
	deviceAuthorizationTTL  = 10 * time.Minute
	agentKeyChallengeTTL    = 5 * time.Minute
	devicePollInterval      = 5 * time.Second
	defaultPublicWebBaseURL = "http://localhost:4173"
)

var orgSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)
var deviceUserCodeAlphabet = []byte("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")

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

func slugifyOrg(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.TrimRight(out[:40], "-")
	}
	return out
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

func randomUserCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = deviceUserCodeAlphabet[int(buf[i])%len(deviceUserCodeAlphabet)]
	}
	return string(buf[:4]) + "-" + string(buf[4:]), nil
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

func publicWebBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("PUBLIC_WEB_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return defaultPublicWebBaseURL
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.Format(timeRFC3339)
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

func deviceAuthorizationStatusToProto(status models.DeviceAuthorizationStatus, expiresAt time.Time) accountv1.DeviceAuthorizationStatus {
	if !expiresAt.IsZero() && !expiresAt.After(time.Now()) {
		return accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_EXPIRED
	}
	switch status {
	case models.DeviceAuthorizationApproved:
		return accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_APPROVED
	case models.DeviceAuthorizationDenied:
		return accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_DENIED
	default:
		return accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_PENDING
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

func normalizeOrganizationRole(role models.OrganizationRole) models.OrganizationRole {
	return models.OrganizationRole(strings.ToLower(strings.TrimSpace(string(role))))
}

func isOrganizationAdminRole(role models.OrganizationRole) bool {
	switch normalizeOrganizationRole(role) {
	case models.OrganizationRoleOwner, models.OrganizationRoleAdmin:
		return true
	default:
		return false
	}
}

func membershipRoleToProto(role models.OrganizationRole) accountv1.Role {
	if isOrganizationAdminRole(role) {
		return accountv1.Role_ROLE_ADMIN
	}
	return accountv1.Role_ROLE_USER
}

func membershipRoleFromProto(role accountv1.Role) (models.OrganizationRole, error) {
	switch role {
	case accountv1.Role_ROLE_USER:
		return models.OrganizationRoleMember, nil
	case accountv1.Role_ROLE_ADMIN:
		return models.OrganizationRoleAdmin, nil
	default:
		return "", status.Error(codes.InvalidArgument, "invalid role")
	}
}

func membershipToProto(member *models.OrganizationMember) *accountv1.Membership {
	if member == nil {
		return nil
	}
	return &accountv1.Membership{
		OrgId:  member.OrgSlug,
		UserId: member.Username,
		Role:   membershipRoleToProto(member.Role),
	}
}

func inviteStatusToProto(status models.OrganizationInviteStatus) accountv1.InviteStatus {
	switch status {
	case models.OrganizationInviteAccepted:
		return accountv1.InviteStatus_INVITE_STATUS_ACCEPTED
	case models.OrganizationInviteDeclined:
		return accountv1.InviteStatus_INVITE_STATUS_DECLINED
	default:
		return accountv1.InviteStatus_INVITE_STATUS_PENDING
	}
}

func inviteToProto(invite *models.OrganizationInvite) *accountv1.Invite {
	if invite == nil {
		return nil
	}
	return &accountv1.Invite{
		Id:          invite.InviteID,
		OrgId:       invite.OrgSlug,
		TargetEmail: invite.TargetEmail,
		Role:        membershipRoleToProto(invite.Role),
		Status:      inviteStatusToProto(invite.Status),
		CreatedAt:   invite.CreatedAt.Format(timeRFC3339),
	}
}

func teamToProto(team *models.Team) *accountv1.Team {
	if team == nil {
		return nil
	}
	return &accountv1.Team{
		Id:        team.TeamID,
		OrgId:     team.OrgSlug,
		Name:      team.Name,
		CreatedAt: team.CreatedAt.Format(timeRFC3339),
		UpdatedAt: team.UpdatedAt.Format(timeRFC3339),
	}
}

func teamMemberToProto(member *models.TeamMember) *accountv1.TeamMember {
	if member == nil {
		return nil
	}
	return &accountv1.TeamMember{
		TeamId:  member.TeamID,
		UserId:  member.Username,
		AddedAt: member.AddedAt.Format(timeRFC3339),
	}
}

func agentKeyToProto(key *models.AgentKey) *accountv1.AgentKey {
	if key == nil {
		return nil
	}
	return &accountv1.AgentKey{
		Id:          key.KeyID,
		UserId:      key.Username,
		Name:        key.Name,
		Algorithm:   key.Algorithm,
		Fingerprint: key.Fingerprint,
		CreatedAt:   key.CreatedAt.Format(timeRFC3339),
		UpdatedAt:   key.UpdatedAt.Format(timeRFC3339),
		LastUsedAt:  formatOptionalTime(key.LastUsedAt),
		RevokedAt:   formatOptionalTime(key.RevokedAt),
		Revoked:     key.RevokedAt != nil || key.State == models.AgentKeyStateRevoked,
	}
}

func normalizeAgentKeyAlgorithm(algorithm string) string {
	return strings.ToLower(strings.TrimSpace(algorithm))
}

func validateAgentPublicKey(algorithm string, publicKey []byte) error {
	switch normalizeAgentKeyAlgorithm(algorithm) {
	case "ed25519":
		if len(publicKey) != ed25519.PublicKeySize {
			return status.Error(codes.InvalidArgument, "invalid ed25519 public key")
		}
		return nil
	default:
		return status.Error(codes.InvalidArgument, "unsupported agent key algorithm")
	}
}

func agentKeyFingerprint(algorithm string, publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return normalizeAgentKeyAlgorithm(algorithm) + ":" + hex.EncodeToString(sum[:])
}

func activeAgentKey(key *models.AgentKey) bool {
	if key == nil {
		return false
	}
	if key.RevokedAt != nil {
		return false
	}
	return key.State != models.AgentKeyStateRevoked
}

func (s *accountServiceServer) resolveAgentKeyForLogin(ctx context.Context, req *accountv1.StartAgentKeyLoginRequest) (*models.AgentKey, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	var (
		key *models.AgentKey
		err error
	)
	switch {
	case strings.TrimSpace(req.GetKeyId()) != "":
		key, err = s.st.GetAgentKey(ctx, strings.TrimSpace(req.GetKeyId()))
	case strings.TrimSpace(req.GetFingerprint()) != "":
		key, err = s.st.GetAgentKeyByFingerprint(ctx, strings.TrimSpace(req.GetFingerprint()))
	case len(req.GetPublicKey()) > 0:
		if err := validateAgentPublicKey("ed25519", req.GetPublicKey()); err != nil {
			return nil, err
		}
		key, err = s.st.GetAgentKeyByFingerprint(ctx, agentKeyFingerprint("ed25519", req.GetPublicKey()))
	default:
		return nil, status.Error(codes.InvalidArgument, "key_id, fingerprint, or public_key is required")
	}
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.Unauthenticated, "agent key not found")
		}
		return nil, status.Error(codes.Internal, "failed to load agent key")
	}
	if !activeAgentKey(key) {
		return nil, status.Error(codes.FailedPrecondition, "agent key is revoked")
	}
	return key, nil
}

func buildAgentKeyChallengePayload(challengeID, keyID, username string, expiresAt time.Time) ([]byte, error) {
	nonce, err := randomToken("nonce_", 16)
	if err != nil {
		return nil, err
	}
	payload := strings.Join([]string{
		"gitslice-agent-login-v1",
		strings.TrimSpace(challengeID),
		strings.TrimSpace(keyID),
		strings.TrimSpace(username),
		expiresAt.UTC().Format(time.RFC3339Nano),
		nonce,
	}, "\n")
	return []byte(payload), nil
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

func (s *accountServiceServer) createSession(ctx context.Context, username string) (*models.AuthSession, error) {
	deviceInfo := deviceInfoFromContext(ctx)
	for i := 0; i < 3; i++ {
		sessionID, err := randomToken("sess_", 16)
		if err != nil {
			return nil, err
		}
		token, err := randomToken("gs_", 24)
		if err != nil {
			return nil, err
		}
		session := &models.AuthSession{
			SessionID:  sessionID,
			Username:   username,
			Token:      token,
			DeviceInfo: deviceInfo,
		}
		err = s.st.CreateAuthSession(ctx, session)
		if err == nil {
			return session, nil
		}
		if err != storage.ErrEntryExists {
			return nil, err
		}
	}
	return nil, status.Error(codes.Aborted, "failed to create session")
}

func (s *accountServiceServer) createRefreshableSession(ctx context.Context, username, agentKeyID string) (*models.AuthSession, error) {
	deviceInfo := deviceInfoFromContext(ctx)
	now := time.Now()
	accessTokenExpiresAt := now.Add(accessTokenTTL)
	refreshTokenExpiresAt := now.Add(refreshTokenTTL)
	for i := 0; i < 3; i++ {
		sessionID, err := randomToken("sess_", 16)
		if err != nil {
			return nil, err
		}
		accessToken, err := randomToken("gs_", 24)
		if err != nil {
			return nil, err
		}
		refreshToken, err := randomToken("gsr_", 24)
		if err != nil {
			return nil, err
		}
		session := &models.AuthSession{
			SessionID:             sessionID,
			Username:              username,
			AgentKeyID:            strings.TrimSpace(agentKeyID),
			Token:                 accessToken,
			RefreshToken:          refreshToken,
			DeviceInfo:            deviceInfo,
			AccessTokenExpiresAt:  &accessTokenExpiresAt,
			RefreshTokenExpiresAt: &refreshTokenExpiresAt,
		}
		if err := s.st.CreateAuthSession(ctx, session); err == nil {
			return session, nil
		} else if err != storage.ErrEntryExists {
			return nil, err
		}
	}
	return nil, status.Error(codes.Aborted, "failed to create refreshable session")
}

func (s *accountServiceServer) rotateAccessToken(ctx context.Context, session *models.AuthSession) (*models.AuthSession, error) {
	if session == nil || strings.TrimSpace(session.SessionID) == "" {
		return nil, status.Error(codes.InvalidArgument, "session is required")
	}
	for i := 0; i < 3; i++ {
		accessToken, err := randomToken("gs_", 24)
		if err != nil {
			return nil, err
		}
		accessTokenExpiresAt := time.Now().Add(accessTokenTTL)
		err = s.st.UpdateAuthSessionTokens(ctx, session.SessionID, accessToken, &accessTokenExpiresAt, session.RefreshToken, session.RefreshTokenExpiresAt)
		if err == nil {
			sessionCopy := *session
			sessionCopy.Token = accessToken
			sessionCopy.AccessTokenExpiresAt = &accessTokenExpiresAt
			return &sessionCopy, nil
		}
		if err != storage.ErrEntryExists {
			return nil, err
		}
	}
	return nil, status.Error(codes.Aborted, "failed to rotate access token")
}

func (s *accountServiceServer) buildAuthResponse(ctx context.Context, user *models.User, session *models.AuthSession) (*accountv1.AuthResponse, error) {
	orgs, err := s.st.ListOrganizationsForUser(ctx, user.Username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load organizations")
	}
	outOrgs := make([]*accountv1.Organization, 0, len(orgs))
	for _, org := range orgs {
		outOrgs = append(outOrgs, orgToProto(org))
	}
	return &accountv1.AuthResponse{
		User:                  userToProto(user),
		Organizations:         outOrgs,
		Session:               sessionToProto(session, true),
		AccessToken:           session.Token,
		RefreshToken:          session.RefreshToken,
		AccessTokenExpiresAt:  formatOptionalTime(session.AccessTokenExpiresAt),
		RefreshTokenExpiresAt: formatOptionalTime(session.RefreshTokenExpiresAt),
		TokenType:             "Bearer",
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
	if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, username); err != nil {
		return nil, status.Error(codes.Internal, "failed to provision home slice")
	}

	user, err := s.st.GetUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	session, err := s.createSession(ctx, username)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create session")
	}
	return s.buildAuthResponse(ctx, user, session)
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
	if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, user.Username); err != nil {
		return nil, status.Error(codes.Internal, "failed to provision home slice")
	}

	session, err := s.createSession(ctx, user.Username)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create session")
	}

	return s.buildAuthResponse(ctx, user, session)
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

func (s *accountServiceServer) StartDeviceAuthorization(ctx context.Context, req *accountv1.StartDeviceAuthorizationRequest) (*accountv1.StartDeviceAuthorizationResponse, error) {
	deviceInfo := deviceInfoFromContext(ctx)
	expiresAt := time.Now().Add(deviceAuthorizationTTL)
	for i := 0; i < 5; i++ {
		deviceCode, err := randomToken("dev_", 24)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to create device code")
		}
		userCode, err := randomUserCode()
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to create user code")
		}
		record := &models.DeviceAuthorization{
			DeviceCode: deviceCode,
			UserCode:   userCode,
			DeviceInfo: deviceInfo,
			Status:     models.DeviceAuthorizationPending,
			ExpiresAt:  expiresAt,
		}
		if err := s.st.CreateDeviceAuthorization(ctx, record); err != nil {
			if err == storage.ErrEntryExists {
				continue
			}
			return nil, status.Error(codes.Internal, "failed to store device authorization")
		}
		verificationURI := publicWebBaseURL() + "/auth/device"
		return &accountv1.StartDeviceAuthorizationResponse{
			DeviceCode:              deviceCode,
			UserCode:                userCode,
			VerificationUri:         verificationURI,
			VerificationUriComplete: verificationURI + "?user_code=" + url.QueryEscape(userCode),
			PollIntervalSeconds:     int32(devicePollInterval / time.Second),
			ExpiresAt:               expiresAt.Format(timeRFC3339),
		}, nil
	}
	return nil, status.Error(codes.Aborted, "failed to create device authorization")
}

func (s *accountServiceServer) ApproveDeviceAuthorization(ctx context.Context, req *accountv1.ApproveDeviceAuthorizationRequest) (*accountv1.ApproveDeviceAuthorizationResponse, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	userCode := strings.ToUpper(strings.TrimSpace(req.GetUserCode()))
	if userCode == "" {
		return nil, status.Error(codes.InvalidArgument, "user_code is required")
	}
	record, err := s.st.GetDeviceAuthorizationByUserCode(ctx, userCode)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "device authorization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load device authorization")
	}
	if !record.ExpiresAt.After(time.Now()) {
		return nil, status.Error(codes.FailedPrecondition, "device authorization expired")
	}
	if record.Status == models.DeviceAuthorizationDenied {
		return nil, status.Error(codes.FailedPrecondition, "device authorization denied")
	}
	if record.Status == models.DeviceAuthorizationApproved {
		if record.Username != "" && record.Username != identity.username {
			return nil, status.Error(codes.PermissionDenied, "device authorization already approved by another user")
		}
		if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, identity.username); err != nil {
			return nil, status.Error(codes.Internal, "failed to provision home slice")
		}
		user, err := s.st.GetUser(ctx, identity.username)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to load user")
		}
		return &accountv1.ApproveDeviceAuthorizationResponse{
			User:       userToProto(user),
			ApprovedAt: formatOptionalTime(record.ApprovedAt),
		}, nil
	}
	if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, identity.username); err != nil {
		return nil, status.Error(codes.Internal, "failed to provision home slice")
	}

	session, err := s.createRefreshableSession(ctx, identity.username, "")
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create session")
	}
	approvedAt := time.Now()
	record.Username = identity.username
	record.SessionID = session.SessionID
	record.Status = models.DeviceAuthorizationApproved
	record.ApprovedAt = &approvedAt
	record.DeniedAt = nil
	if err := s.st.UpdateDeviceAuthorization(ctx, record); err != nil {
		return nil, status.Error(codes.Internal, "failed to update device authorization")
	}
	user, err := s.st.GetUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	return &accountv1.ApproveDeviceAuthorizationResponse{
		User:       userToProto(user),
		ApprovedAt: approvedAt.Format(timeRFC3339),
	}, nil
}

func (s *accountServiceServer) PollDeviceAuthorization(ctx context.Context, req *accountv1.PollDeviceAuthorizationRequest) (*accountv1.PollDeviceAuthorizationResponse, error) {
	deviceCode := strings.TrimSpace(req.GetDeviceCode())
	if deviceCode == "" {
		return nil, status.Error(codes.InvalidArgument, "device_code is required")
	}
	record, err := s.st.GetDeviceAuthorizationByDeviceCode(ctx, deviceCode)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "device authorization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load device authorization")
	}

	statusValue := deviceAuthorizationStatusToProto(record.Status, record.ExpiresAt)
	response := &accountv1.PollDeviceAuthorizationResponse{
		Status:    statusValue,
		ExpiresAt: record.ExpiresAt.Format(timeRFC3339),
	}
	if statusValue != accountv1.DeviceAuthorizationStatus_DEVICE_AUTHORIZATION_STATUS_APPROVED {
		return response, nil
	}
	if strings.TrimSpace(record.SessionID) == "" {
		return nil, status.Error(codes.Internal, "approved device authorization missing session")
	}
	session, err := s.st.GetAuthSession(ctx, record.SessionID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.Unauthenticated, "authorized session no longer exists")
		}
		return nil, status.Error(codes.Internal, "failed to load authorized session")
	}
	user, err := s.st.GetUser(ctx, session.Username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	authResp, err := s.buildAuthResponse(ctx, user, session)
	if err != nil {
		return nil, err
	}
	response.Auth = authResp
	return response, nil
}

func (s *accountServiceServer) RefreshAccessToken(ctx context.Context, req *accountv1.RefreshAccessTokenRequest) (*accountv1.AuthResponse, error) {
	refreshToken := strings.TrimSpace(req.GetRefreshToken())
	if refreshToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}
	session, err := s.st.GetAuthSessionByRefreshToken(ctx, refreshToken)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
		return nil, status.Error(codes.Internal, "failed to resolve refresh token")
	}
	session, err = s.rotateAccessToken(ctx, session)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to refresh access token")
	}
	user, err := s.st.GetUser(ctx, session.Username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	return s.buildAuthResponse(ctx, user, session)
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

func (s *accountServiceServer) ListAgentKeys(ctx context.Context, req *accountv1.ListAgentKeysRequest) (*accountv1.ListAgentKeysResponse, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.st.ListAgentKeysByUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list agent keys")
	}
	out := make([]*accountv1.AgentKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, agentKeyToProto(key))
	}
	return &accountv1.ListAgentKeysResponse{Keys: out}, nil
}

func (s *accountServiceServer) CreateAgentKey(ctx context.Context, req *accountv1.CreateAgentKeyRequest) (*accountv1.AgentKey, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.GetName())
	algorithm := normalizeAgentKeyAlgorithm(req.GetAlgorithm())
	publicKey := append([]byte(nil), req.GetPublicKey()...)
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if err := validateAgentPublicKey(algorithm, publicKey); err != nil {
		return nil, err
	}

	fingerprint := agentKeyFingerprint(algorithm, publicKey)
	var created *models.AgentKey
	for i := 0; i < 3; i++ {
		keyID, err := randomToken("agk_", 16)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to create agent key id")
		}
		key := &models.AgentKey{
			KeyID:       keyID,
			Username:    identity.username,
			Name:        name,
			Algorithm:   algorithm,
			PublicKey:   publicKey,
			Fingerprint: fingerprint,
			State:       models.AgentKeyStateActive,
		}
		err = s.st.CreateAgentKey(ctx, key)
		if err == nil {
			created, err = s.st.GetAgentKey(ctx, keyID)
			if err != nil {
				return nil, status.Error(codes.Internal, "failed to load created agent key")
			}
			return agentKeyToProto(created), nil
		}
		if err == storage.ErrEntryExists {
			existing, lookupErr := s.st.GetAgentKeyByFingerprint(ctx, fingerprint)
			if lookupErr == nil && existing != nil {
				return nil, status.Error(codes.AlreadyExists, "agent key already exists")
			}
			continue
		}
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		if err == storage.ErrInvalidInput {
			return nil, status.Error(codes.InvalidArgument, "invalid agent key")
		}
		return nil, status.Error(codes.Internal, "failed to create agent key")
	}
	return nil, status.Error(codes.Aborted, "failed to create agent key")
}

func (s *accountServiceServer) DeleteAgentKey(ctx context.Context, req *accountv1.DeleteAgentKeyRequest) (*emptypb.Empty, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	keyID := strings.TrimSpace(req.GetKeyId())
	if keyID == "" {
		return nil, status.Error(codes.InvalidArgument, "key_id is required")
	}
	if err := s.st.RevokeAgentKey(ctx, identity.username, keyID, time.Now()); err != nil {
		switch err {
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "agent key not found")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid agent key id")
		default:
			return nil, status.Error(codes.Internal, "failed to revoke agent key")
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *accountServiceServer) StartAgentKeyLogin(ctx context.Context, req *accountv1.StartAgentKeyLoginRequest) (*accountv1.StartAgentKeyLoginResponse, error) {
	key, err := s.resolveAgentKeyForLogin(ctx, req)
	if err != nil {
		return nil, err
	}

	for i := 0; i < 3; i++ {
		challengeID, err := randomToken("agc_", 16)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to create challenge")
		}
		expiresAt := time.Now().Add(agentKeyChallengeTTL)
		challengeBytes, err := buildAgentKeyChallengePayload(challengeID, key.KeyID, key.Username, expiresAt)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to create challenge")
		}

		record := &models.AgentKeyChallenge{
			ChallengeID: challengeID,
			AgentKeyID:  key.KeyID,
			Username:    key.Username,
			Challenge:   challengeBytes,
			DeviceInfo:  deviceInfoFromContext(ctx),
			ExpiresAt:   expiresAt,
		}
		if err := s.st.CreateAgentKeyChallenge(ctx, record); err != nil {
			if err == storage.ErrEntryExists {
				continue
			}
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.Unauthenticated, "agent key not found")
			}
			return nil, status.Error(codes.Internal, "failed to create challenge")
		}

		return &accountv1.StartAgentKeyLoginResponse{
			ChallengeId: challengeID,
			Challenge:   append([]byte(nil), challengeBytes...),
			ExpiresAt:   expiresAt.Format(timeRFC3339),
		}, nil
	}

	return nil, status.Error(codes.Aborted, "failed to create challenge")
}

func (s *accountServiceServer) CompleteAgentKeyLogin(ctx context.Context, req *accountv1.CompleteAgentKeyLoginRequest) (*accountv1.AuthResponse, error) {
	challengeID := strings.TrimSpace(req.GetChallengeId())
	if challengeID == "" || len(req.GetSignature()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "challenge_id and signature are required")
	}

	challenge, err := s.st.GetAgentKeyChallenge(ctx, challengeID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "agent key challenge not found")
		}
		return nil, status.Error(codes.Internal, "failed to load challenge")
	}
	if challenge.UsedAt != nil {
		return nil, status.Error(codes.FailedPrecondition, "agent key challenge already used")
	}
	if !challenge.ExpiresAt.After(time.Now()) {
		return nil, status.Error(codes.FailedPrecondition, "agent key challenge expired")
	}

	key, err := s.st.GetAgentKey(ctx, challenge.AgentKeyID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.Unauthenticated, "agent key not found")
		}
		return nil, status.Error(codes.Internal, "failed to load agent key")
	}
	if !activeAgentKey(key) {
		return nil, status.Error(codes.FailedPrecondition, "agent key is revoked")
	}
	if err := validateAgentPublicKey(key.Algorithm, key.PublicKey); err != nil {
		return nil, err
	}
	if !ed25519.Verify(ed25519.PublicKey(key.PublicKey), challenge.Challenge, req.GetSignature()) {
		return nil, status.Error(codes.Unauthenticated, "invalid agent key signature")
	}
	if err := s.st.MarkAgentKeyChallengeUsed(ctx, challengeID, time.Now()); err != nil {
		switch err {
		case storage.ErrEntryExists:
			return nil, status.Error(codes.FailedPrecondition, "agent key challenge already used")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "agent key challenge not found")
		default:
			return nil, status.Error(codes.Internal, "failed to consume challenge")
		}
	}
	if err := s.st.TouchAgentKey(ctx, key.KeyID, time.Now()); err != nil && err != storage.ErrEntryNotFound {
		return nil, status.Error(codes.Internal, "failed to update agent key usage")
	}
	if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, key.Username); err != nil {
		return nil, status.Error(codes.Internal, "failed to provision home slice")
	}

	user, err := s.st.GetUser(ctx, key.Username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	session, err := s.createRefreshableSession(ctx, key.Username, key.KeyID)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create session")
	}
	return s.buildAuthResponse(ctx, user, session)
}

func (s *accountServiceServer) GetMe(ctx context.Context, req *accountv1.GetMeRequest) (*accountv1.User, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.st.GetUser(ctx, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	return userToProto(user), nil
}

func (s *accountServiceServer) UpdateMe(ctx context.Context, req *accountv1.UpdateMeRequest) (*accountv1.User, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	user, err := s.st.GetUser(ctx, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}

	updated := false
	if name := strings.TrimSpace(req.GetName()); name != "" && name != user.Name {
		user.Name = name
		updated = true
	}
	if email := normalizeEmail(req.GetPrimaryEmail()); email != "" {
		if !validateEmail(email) {
			return nil, status.Error(codes.InvalidArgument, "invalid email")
		}
		if email != user.PrimaryEmail {
			user.PrimaryEmail = email
			updated = true
		}
	}
	if !updated {
		return userToProto(user), nil
	}

	if err := s.st.UpdateUser(ctx, user); err != nil {
		switch err {
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "user not found")
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "email already in use")
		default:
			return nil, status.Error(codes.Internal, "failed to update user")
		}
	}
	updatedUser, err := s.st.GetUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load updated user")
	}
	return userToProto(updatedUser), nil
}

func (s *accountServiceServer) DeleteMe(ctx context.Context, req *accountv1.DeleteMeRequest) (*emptypb.Empty, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgs, err := s.st.ListOrganizationsForUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to validate user organizations")
	}
	for _, org := range orgs {
		if org != nil && org.CreatedBy == identity.username {
			return nil, status.Error(codes.FailedPrecondition, "cannot delete account while owning organizations")
		}
	}

	sessions, err := s.st.ListAuthSessionsByUser(ctx, identity.username)
	if err == nil {
		for _, session := range sessions {
			_ = s.st.RevokeAuthSession(ctx, identity.username, session.SessionID)
		}
	}

	if err := s.st.DeleteUser(ctx, identity.username); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to delete user")
	}
	return &emptypb.Empty{}, nil
}

func (s *accountServiceServer) GetUser(ctx context.Context, req *accountv1.GetUserRequest) (*accountv1.User, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	userID := strings.TrimSpace(req.GetUserId())
	if !auth.ValidateUsername(userID) {
		return nil, status.Error(codes.InvalidArgument, "invalid user id")
	}

	user, err := s.st.GetUser(ctx, userID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	if identity.username != userID {
		user.PrimaryEmail = ""
	}
	return userToProto(user), nil
}

func (s *accountServiceServer) CreateOrganization(ctx context.Context, req *accountv1.CreateOrganizationRequest) (*accountv1.Organization, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" || len(name) > 80 {
		return nil, status.Error(codes.InvalidArgument, "invalid org name")
	}
	slug := strings.TrimSpace(req.GetSlug())
	if slug == "" {
		slug = slugifyOrg(name)
	}
	if !orgSlugRE.MatchString(slug) {
		return nil, status.Error(codes.InvalidArgument, "invalid org slug")
	}

	org := &models.Organization{
		Slug:      slug,
		Name:      name,
		CreatedBy: identity.username,
	}
	if err := s.st.CreateOrganization(ctx, org); err != nil {
		switch err {
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "organization slug is unavailable")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid organization")
		default:
			return nil, status.Error(codes.Internal, "failed to create organization")
		}
	}

	if err := s.st.AddOrganizationMember(ctx, &models.OrganizationMember{
		OrgSlug:  slug,
		Username: identity.username,
		Role:     models.OrganizationRoleOwner,
	}); err != nil {
		_ = s.st.DeleteOrganization(ctx, slug)
		return nil, status.Error(codes.Internal, "failed to initialize organization owner membership")
	}

	created, err := s.st.GetOrganization(ctx, slug)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	return orgToProto(created), nil
}

func (s *accountServiceServer) ListOrganizations(ctx context.Context, req *accountv1.ListOrganizationsRequest) (*accountv1.ListOrganizationsResponse, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	orgs, err := s.st.ListOrganizationsForUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to list organizations")
	}
	out := make([]*accountv1.Organization, 0, len(orgs))
	for _, org := range orgs {
		out = append(out, orgToProto(org))
	}
	return &accountv1.ListOrganizationsResponse{Organizations: out}, nil
}

func (s *accountServiceServer) GetOrganization(ctx context.Context, req *accountv1.GetOrganizationRequest) (*accountv1.Organization, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	slug := strings.TrimSpace(req.GetOrgId())
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "org_id is required")
	}

	org, err := s.st.GetOrganization(ctx, slug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}

	orgs, err := s.st.ListOrganizationsForUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to authorize organization access")
	}
	allowed := false
	for _, item := range orgs {
		if item != nil && item.Slug == slug {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	return orgToProto(org), nil
}

func (s *accountServiceServer) UpdateOrganization(ctx context.Context, req *accountv1.UpdateOrganizationRequest) (*accountv1.Organization, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	slug := strings.TrimSpace(req.GetOrgId())
	name := strings.TrimSpace(req.GetName())
	if slug == "" || name == "" || len(name) > 80 {
		return nil, status.Error(codes.InvalidArgument, "invalid organization update")
	}

	org, err := s.st.GetOrganization(ctx, slug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	if org.CreatedBy != identity.username {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}
	org.Name = name

	if err := s.st.UpdateOrganization(ctx, org); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to update organization")
	}
	updated, err := s.st.GetOrganization(ctx, slug)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load updated organization")
	}
	return orgToProto(updated), nil
}

func (s *accountServiceServer) DeleteOrganization(ctx context.Context, req *accountv1.DeleteOrganizationRequest) (*emptypb.Empty, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}
	slug := strings.TrimSpace(req.GetOrgId())
	if slug == "" {
		return nil, status.Error(codes.InvalidArgument, "org_id is required")
	}

	org, err := s.st.GetOrganization(ctx, slug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	if org.CreatedBy != identity.username {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}
	if err := s.st.DeleteOrganization(ctx, slug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to delete organization")
	}
	return &emptypb.Empty{}, nil
}

func (s *accountServiceServer) CreateInvite(ctx context.Context, req *accountv1.CreateInviteRequest) (*accountv1.Invite, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	targetEmail := normalizeEmail(req.GetTargetEmail())
	if !auth.ValidateUsername(orgSlug) || !validateEmail(targetEmail) {
		return nil, status.Error(codes.InvalidArgument, "invalid invite request")
	}
	role, roleErr := membershipRoleFromProto(req.GetRole())
	if roleErr != nil {
		return nil, roleErr
	}

	org, err := s.st.GetOrganization(ctx, orgSlug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize invite creation")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	inviteID, err := randomToken("inv_", 16)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate invite id")
	}
	invite := &models.OrganizationInvite{
		InviteID:    inviteID,
		OrgSlug:     org.Slug,
		TargetEmail: targetEmail,
		Role:        role,
		Status:      models.OrganizationInvitePending,
		CreatedBy:   identity.username,
	}
	if err := s.st.CreateOrganizationInvite(ctx, invite); err != nil {
		switch err {
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "an active invite already exists for this email")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "organization not found")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid invite request")
		default:
			return nil, status.Error(codes.Internal, "failed to create invite")
		}
	}
	created, err := s.st.GetOrganizationInvite(ctx, orgSlug, inviteID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load invite")
	}
	return inviteToProto(created), nil
}

func (s *accountServiceServer) AcceptInvite(ctx context.Context, req *accountv1.AcceptInviteRequest) (*accountv1.Invite, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	inviteID := strings.TrimSpace(req.GetInviteId())
	if !auth.ValidateUsername(orgSlug) || inviteID == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid invite request")
	}

	invite, err := s.st.GetOrganizationInvite(ctx, orgSlug, inviteID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "invite not found")
		}
		return nil, status.Error(codes.Internal, "failed to load invite")
	}
	if invite.Status != models.OrganizationInvitePending {
		return nil, status.Error(codes.FailedPrecondition, "invite is no longer pending")
	}

	user, err := s.st.GetUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load account")
	}
	userEmail := normalizeEmail(user.PrimaryEmail)
	if !validateEmail(userEmail) {
		return nil, status.Error(codes.FailedPrecondition, "account must have a valid primary email to accept invites")
	}
	if userEmail != normalizeEmail(invite.TargetEmail) {
		return nil, status.Error(codes.PermissionDenied, "invite does not match current account email")
	}

	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}

	grantedRole := normalizeOrganizationRole(invite.Role)
	if grantedRole != models.OrganizationRoleAdmin {
		grantedRole = models.OrganizationRoleMember
	}
	member, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	switch err {
	case nil:
		if !isOrganizationAdminRole(member.Role) && grantedRole == models.OrganizationRoleAdmin {
			member.Role = models.OrganizationRoleAdmin
			if err := s.st.UpdateOrganizationMember(ctx, member); err != nil {
				return nil, status.Error(codes.Internal, "failed to apply invite role")
			}
		}
	case storage.ErrEntryNotFound:
		if err := s.st.AddOrganizationMember(ctx, &models.OrganizationMember{
			OrgSlug:  orgSlug,
			Username: identity.username,
			Role:     grantedRole,
		}); err != nil {
			return nil, status.Error(codes.Internal, "failed to add organization member")
		}
	default:
		return nil, status.Error(codes.Internal, "failed to apply invite")
	}

	invite.Status = models.OrganizationInviteAccepted
	if err := s.st.UpdateOrganizationInvite(ctx, invite); err != nil {
		return nil, status.Error(codes.Internal, "failed to update invite status")
	}
	updated, err := s.st.GetOrganizationInvite(ctx, orgSlug, inviteID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load updated invite")
	}
	return inviteToProto(updated), nil
}

func (s *accountServiceServer) DeclineInvite(ctx context.Context, req *accountv1.DeclineInviteRequest) (*accountv1.Invite, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	inviteID := strings.TrimSpace(req.GetInviteId())
	if !auth.ValidateUsername(orgSlug) || inviteID == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid invite request")
	}

	invite, err := s.st.GetOrganizationInvite(ctx, orgSlug, inviteID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "invite not found")
		}
		return nil, status.Error(codes.Internal, "failed to load invite")
	}
	if invite.Status != models.OrganizationInvitePending {
		return nil, status.Error(codes.FailedPrecondition, "invite is no longer pending")
	}

	user, err := s.st.GetUser(ctx, identity.username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load account")
	}
	userEmail := normalizeEmail(user.PrimaryEmail)
	if !validateEmail(userEmail) {
		return nil, status.Error(codes.FailedPrecondition, "account must have a valid primary email to decline invites")
	}
	if userEmail != normalizeEmail(invite.TargetEmail) {
		return nil, status.Error(codes.PermissionDenied, "invite does not match current account email")
	}

	invite.Status = models.OrganizationInviteDeclined
	if err := s.st.UpdateOrganizationInvite(ctx, invite); err != nil {
		return nil, status.Error(codes.Internal, "failed to update invite status")
	}
	updated, err := s.st.GetOrganizationInvite(ctx, orgSlug, inviteID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load updated invite")
	}
	return inviteToProto(updated), nil
}

func (s *accountServiceServer) ListMembers(ctx context.Context, req *accountv1.ListMembersRequest) (*accountv1.ListMembersResponse, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	if !auth.ValidateUsername(orgSlug) {
		return nil, status.Error(codes.InvalidArgument, "org_id is required")
	}
	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize membership access")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	members, err := s.st.ListOrganizationMembers(ctx, orgSlug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to list members")
	}
	out := make([]*accountv1.Membership, 0, len(members))
	for _, member := range members {
		out = append(out, membershipToProto(member))
	}
	return &accountv1.ListMembersResponse{Members: out}, nil
}

func (s *accountServiceServer) UpdateMember(ctx context.Context, req *accountv1.UpdateMemberRequest) (*accountv1.Membership, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	memberID := strings.TrimSpace(req.GetMemberId())
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(memberID) {
		return nil, status.Error(codes.InvalidArgument, "invalid membership update")
	}
	desiredRole, roleErr := membershipRoleFromProto(req.GetRole())
	if roleErr != nil {
		return nil, roleErr
	}

	org, err := s.st.GetOrganization(ctx, orgSlug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize membership update")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	member, err := s.st.GetOrganizationMember(ctx, orgSlug, memberID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to load member")
	}

	if member.Username == org.CreatedBy {
		return nil, status.Error(codes.FailedPrecondition, "cannot change organization owner role")
	}

	members, err := s.st.ListOrganizationMembers(ctx, orgSlug)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to validate admin constraints")
	}
	adminCount := 0
	for _, item := range members {
		if item != nil && isOrganizationAdminRole(item.Role) {
			adminCount++
		}
	}
	if isOrganizationAdminRole(member.Role) && !isOrganizationAdminRole(desiredRole) && adminCount <= 1 {
		return nil, status.Error(codes.FailedPrecondition, "cannot demote the last organization admin")
	}

	member.Role = desiredRole
	if err := s.st.UpdateOrganizationMember(ctx, member); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to update member")
	}
	return membershipToProto(member), nil
}

func (s *accountServiceServer) DeleteMember(ctx context.Context, req *accountv1.DeleteMemberRequest) (*emptypb.Empty, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	memberID := strings.TrimSpace(req.GetMemberId())
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(memberID) {
		return nil, status.Error(codes.InvalidArgument, "invalid member delete request")
	}

	org, err := s.st.GetOrganization(ctx, orgSlug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize member delete")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	member, err := s.st.GetOrganizationMember(ctx, orgSlug, memberID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to load member")
	}
	if member.Username == org.CreatedBy {
		return nil, status.Error(codes.FailedPrecondition, "cannot remove organization owner")
	}

	members, err := s.st.ListOrganizationMembers(ctx, orgSlug)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to validate admin constraints")
	}
	adminCount := 0
	for _, item := range members {
		if item != nil && isOrganizationAdminRole(item.Role) {
			adminCount++
		}
	}
	if isOrganizationAdminRole(member.Role) && adminCount <= 1 {
		return nil, status.Error(codes.FailedPrecondition, "cannot remove the last organization admin")
	}

	if err := s.st.RemoveOrganizationMember(ctx, orgSlug, memberID); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "member not found")
		}
		return nil, status.Error(codes.Internal, "failed to remove member")
	}
	return &emptypb.Empty{}, nil
}

func (s *accountServiceServer) CreateTeam(ctx context.Context, req *accountv1.CreateTeamRequest) (*accountv1.Team, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	name := strings.TrimSpace(req.GetName())
	if !auth.ValidateUsername(orgSlug) || name == "" || len(name) > 80 {
		return nil, status.Error(codes.InvalidArgument, "invalid team request")
	}

	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize team creation")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	teamID, err := randomToken("team_", 16)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to generate team id")
	}
	team := &models.Team{
		TeamID:    teamID,
		OrgSlug:   orgSlug,
		Name:      name,
		CreatedBy: identity.username,
	}
	if err := s.st.CreateTeam(ctx, team); err != nil {
		switch err {
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "team already exists")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "organization not found")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid team request")
		default:
			return nil, status.Error(codes.Internal, "failed to create team")
		}
	}
	created, err := s.st.GetTeam(ctx, teamID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load created team")
	}
	return teamToProto(created), nil
}

func (s *accountServiceServer) ListTeams(ctx context.Context, req *accountv1.ListTeamsRequest) (*accountv1.ListTeamsResponse, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	if !auth.ValidateUsername(orgSlug) {
		return nil, status.Error(codes.InvalidArgument, "org_id is required")
	}

	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize team listing")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	teams, err := s.st.ListTeams(ctx, orgSlug)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to list teams")
	}
	out := make([]*accountv1.Team, 0, len(teams))
	for _, team := range teams {
		out = append(out, teamToProto(team))
	}
	return &accountv1.ListTeamsResponse{Teams: out}, nil
}

func (s *accountServiceServer) UpdateTeam(ctx context.Context, req *accountv1.UpdateTeamRequest) (*accountv1.Team, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	teamID := strings.TrimSpace(req.GetTeamId())
	name := strings.TrimSpace(req.GetName())
	if !auth.ValidateUsername(orgSlug) || teamID == "" || name == "" || len(name) > 80 {
		return nil, status.Error(codes.InvalidArgument, "invalid team update request")
	}

	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize team update")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	team, err := s.st.GetTeam(ctx, teamID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "team not found")
		}
		return nil, status.Error(codes.Internal, "failed to load team")
	}
	if team.OrgSlug != orgSlug {
		return nil, status.Error(codes.NotFound, "team not found")
	}
	team.Name = name
	if err := s.st.UpdateTeam(ctx, team); err != nil {
		switch err {
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "team not found")
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "team already exists")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid team update request")
		default:
			return nil, status.Error(codes.Internal, "failed to update team")
		}
	}
	updated, err := s.st.GetTeam(ctx, teamID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load updated team")
	}
	return teamToProto(updated), nil
}

func (s *accountServiceServer) DeleteTeam(ctx context.Context, req *accountv1.DeleteTeamRequest) (*emptypb.Empty, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	teamID := strings.TrimSpace(req.GetTeamId())
	if !auth.ValidateUsername(orgSlug) || teamID == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid team delete request")
	}

	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize team delete")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	if err := s.st.DeleteTeam(ctx, orgSlug, teamID); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "team not found")
		}
		return nil, status.Error(codes.Internal, "failed to delete team")
	}
	return &emptypb.Empty{}, nil
}

func (s *accountServiceServer) AddTeamMember(ctx context.Context, req *accountv1.AddTeamMemberRequest) (*accountv1.TeamMember, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	teamID := strings.TrimSpace(req.GetTeamId())
	memberID := strings.TrimSpace(req.GetMemberId())
	if !auth.ValidateUsername(orgSlug) || teamID == "" || !auth.ValidateUsername(memberID) {
		return nil, status.Error(codes.InvalidArgument, "invalid team member request")
	}

	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize team membership update")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	team, err := s.st.GetTeam(ctx, teamID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "team not found")
		}
		return nil, status.Error(codes.Internal, "failed to load team")
	}
	if team.OrgSlug != orgSlug {
		return nil, status.Error(codes.NotFound, "team not found")
	}

	member := &models.TeamMember{
		TeamID:   teamID,
		Username: memberID,
	}
	if err := s.st.AddTeamMember(ctx, member); err != nil {
		switch err {
		case storage.ErrEntryExists:
			return nil, status.Error(codes.AlreadyExists, "team member already exists")
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "team or organization member not found")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid team member request")
		default:
			return nil, status.Error(codes.Internal, "failed to add team member")
		}
	}
	return teamMemberToProto(member), nil
}

func (s *accountServiceServer) DeleteTeamMember(ctx context.Context, req *accountv1.DeleteTeamMemberRequest) (*emptypb.Empty, error) {
	identity, err := s.resolveIdentity(ctx)
	if err != nil {
		return nil, err
	}

	orgSlug := strings.TrimSpace(req.GetOrgId())
	teamID := strings.TrimSpace(req.GetTeamId())
	memberID := strings.TrimSpace(req.GetMemberId())
	if !auth.ValidateUsername(orgSlug) || teamID == "" || !auth.ValidateUsername(memberID) {
		return nil, status.Error(codes.InvalidArgument, "invalid team member delete request")
	}

	if _, err := s.st.GetOrganization(ctx, orgSlug); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "organization not found")
		}
		return nil, status.Error(codes.Internal, "failed to load organization")
	}
	requesterMember, err := s.st.GetOrganizationMember(ctx, orgSlug, identity.username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.PermissionDenied, "organization admin required")
		}
		return nil, status.Error(codes.Internal, "failed to authorize team membership delete")
	}
	if !isOrganizationAdminRole(requesterMember.Role) {
		return nil, status.Error(codes.PermissionDenied, "organization admin required")
	}

	if err := s.st.DeleteTeamMember(ctx, orgSlug, teamID, memberID); err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.NotFound, "team member not found")
		}
		return nil, status.Error(codes.Internal, "failed to delete team member")
	}
	return &emptypb.Empty{}, nil
}
