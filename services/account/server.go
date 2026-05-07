package accountservice

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authresolver"
	"github.com/niczy/gitslice/internal/homeslice"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
	accountv1 "github.com/niczy/gitslice/proto/account"
	"golang.org/x/crypto/bcrypt"
	httpbody "google.golang.org/genproto/googleapis/api/httpbody"
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
	defaultClerkAPIBaseURL  = "https://api.clerk.com/v1"
	clerkWebhookTolerance   = 5 * time.Minute
	bridgeTokenMaxLifetime  = 15 * time.Minute
	bridgeTokenClockSkew    = 1 * time.Minute
)

var orgSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)
var deviceUserCodeAlphabet = []byte("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")

type accountServiceServer struct {
	accountv1.UnimplementedAccountServiceServer
	st              storage.Storage
	clerkHTTPClient *http.Client
	clerkAPIBaseURL string
}

type authIdentity struct {
	username   string
	sessionID  string
	authSource string
}

// RegisterGRPCServer registers the account service handlers on an existing gRPC server.
func RegisterGRPCServer(srv *grpc.Server, st storage.Storage) {
	accountv1.RegisterAccountServiceServer(srv, &accountServiceServer{st: st})
}

func (s *accountServiceServer) clerkClient() *http.Client {
	if s != nil && s.clerkHTTPClient != nil {
		return s.clerkHTTPClient
	}
	return http.DefaultClient
}

func (s *accountServiceServer) clerkBackendAPIBaseURL() string {
	if s != nil && strings.TrimSpace(s.clerkAPIBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(s.clerkAPIBaseURL), "/")
	}
	if value := strings.TrimSpace(os.Getenv("CLERK_API_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return defaultClerkAPIBaseURL
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

func clerkWebhookHeaderFromContext(ctx context.Context, names ...string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, name := range names {
		vals := md.Get(name)
		if len(vals) > 0 {
			return strings.TrimSpace(vals[0])
		}
	}
	return ""
}

func decodeClerkWebhookSecret(secret string) ([]byte, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, status.Error(codes.FailedPrecondition, "CLERK_WEBHOOK_SECRET is not configured")
	}
	secret = strings.TrimPrefix(secret, "whsec_")
	decoded, err := base64.StdEncoding.DecodeString(secret)
	if err == nil {
		return decoded, nil
	}
	decoded, rawErr := base64.RawStdEncoding.DecodeString(secret)
	if rawErr == nil {
		return decoded, nil
	}
	return nil, status.Error(codes.FailedPrecondition, "invalid CLERK_WEBHOOK_SECRET")
}

func verifyClerkWebhookSignature(payload []byte, idHeader, timestampHeader, sigHeader, secret string, now time.Time) error {
	if strings.TrimSpace(idHeader) == "" || strings.TrimSpace(timestampHeader) == "" || strings.TrimSpace(sigHeader) == "" {
		return status.Error(codes.Unauthenticated, "missing Clerk webhook signature headers")
	}
	key, err := decodeClerkWebhookSecret(secret)
	if err != nil {
		return err
	}
	timestamp, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return status.Error(codes.Unauthenticated, "invalid Clerk webhook timestamp")
	}
	signedAt := time.Unix(timestamp, 0)
	if now.Sub(signedAt) > clerkWebhookTolerance || signedAt.Sub(now) > clerkWebhookTolerance {
		return status.Error(codes.Unauthenticated, "Clerk webhook timestamp is outside tolerance")
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.TrimSpace(idHeader)))
	mac.Write([]byte("."))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	for _, versionedSignature := range strings.Fields(sigHeader) {
		version, signature, ok := strings.Cut(versionedSignature, ",")
		if !ok || version != "v1" {
			continue
		}
		if hmac.Equal([]byte(signature), []byte(expected)) {
			return nil
		}
	}
	return status.Error(codes.Unauthenticated, "invalid Clerk webhook signature")
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
		AgentKeyId: session.AgentKeyID,
	}
}

func authMethodToProto(id string, methodType accountv1.AuthMethodType, provider, email string, linkedAt time.Time) *accountv1.AuthMethod {
	return &accountv1.AuthMethod{
		Id:       id,
		Type:     methodType,
		Provider: provider,
		Email:    email,
		LinkedAt: linkedAt.Format(timeRFC3339),
	}
}

func authMethodsForUser(user *models.User) []*accountv1.AuthMethod {
	if user == nil {
		return nil
	}
	methods := make([]*accountv1.AuthMethod, 0, 3)
	linkedAt := user.UpdatedAt
	if linkedAt.IsZero() {
		linkedAt = user.CreatedAt
	}
	if strings.TrimSpace(user.PasswordHash) != "" {
		methods = append(methods, authMethodToProto(
			"password",
			accountv1.AuthMethodType_AUTH_METHOD_TYPE_PASSWORD,
			"password",
			user.PrimaryEmail,
			linkedAt,
		))
	}
	if strings.TrimSpace(user.ClerkUserID) != "" {
		methods = append(methods, authMethodToProto(
			"oauth:clerk",
			accountv1.AuthMethodType_AUTH_METHOD_TYPE_OAUTH,
			"clerk",
			user.PrimaryEmail,
			linkedAt,
		))
	}
	return methods
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
	identity, err := authresolver.RequireGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	return &authIdentity{
		username:   identity.Username,
		sessionID:  identity.SessionID,
		authSource: identity.AuthSource,
	}, nil
}

type providerLocalIdentityResult struct {
	user               *models.User
	account            *models.Account
	createdAccount     bool
	createdUser        bool
	linkedExistingUser bool
	claimedAccount     bool
	localSession       *models.AuthSession
}

type clerkWebhookEnvelope struct {
	Type string `json:"type"`
	Data struct {
		ID                    string `json:"id"`
		PrimaryEmailAddressID string `json:"primary_email_address_id"`
		EmailAddresses        []struct {
			ID           string `json:"id"`
			EmailAddress string `json:"email_address"`
		} `json:"email_addresses"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	} `json:"data"`
}

type clerkBridgeClaims struct {
	Provider          string `json:"provider"`
	UserID            string `json:"userId"`
	SessionID         string `json:"sessionId"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferredUsername"`
	ImageURL          string `json:"imageUrl"`
	IssuedAtMs        int64  `json:"issuedAtMs"`
	ExpiresAtMs       int64  `json:"expiresAtMs"`
}

func hashClaimToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sanitizeUsernameCandidate(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		raw = "user"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
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
	value := strings.Trim(b.String(), "-")
	if value == "" {
		value = "user"
	}
	if value[0] < 'a' || value[0] > 'z' {
		if value[0] < '0' || value[0] > '9' {
			value = "u-" + value
		}
	}
	if len(value) > 32 {
		value = strings.TrimRight(value[:32], "-")
	}
	if len(value) >= 3 && auth.ValidateUsername(value) {
		return value
	}
	for len(value) < 3 {
		value += "x"
	}
	if len(value) > 32 {
		value = value[:32]
	}
	if !auth.ValidateUsername(value) {
		return "user"
	}
	return value
}

func usernameCandidateWithSuffix(base string, suffix int) string {
	base = sanitizeUsernameCandidate(base)
	if suffix <= 0 {
		return base
	}
	suffixText := "-" + strconv.Itoa(suffix+1)
	maxBaseLen := 32 - len(suffixText)
	if maxBaseLen < 3 {
		maxBaseLen = 3
	}
	if len(base) > maxBaseLen {
		base = strings.TrimRight(base[:maxBaseLen], "-")
	}
	if len(base) < 3 {
		base = sanitizeUsernameCandidate(base + "xxx")
		if len(base) > maxBaseLen {
			base = base[:maxBaseLen]
		}
	}
	return base + suffixText
}

func (s *accountServiceServer) createHumanAttachedAccount(ctx context.Context) (*models.Account, bool, error) {
	for i := 0; i < 5; i++ {
		accountID, err := randomToken("acct_", 16)
		if err != nil {
			return nil, false, err
		}
		account := &models.Account{
			AccountID:  accountID,
			OwnerMode:  models.AccountOwnerModeHumanAttached,
			ClaimState: models.AccountClaimStateClaimed,
		}
		if err := s.st.CreateAccount(ctx, account); err == nil {
			created, err := s.st.GetAccount(ctx, accountID)
			return created, true, err
		} else if err != storage.ErrEntryExists {
			return nil, false, err
		}
	}
	return nil, false, status.Error(codes.Aborted, "failed to create account")
}

func (s *accountServiceServer) createAgentOnlyAccount(ctx context.Context) (*models.Account, bool, error) {
	for i := 0; i < 5; i++ {
		accountID, err := randomToken("acct_", 16)
		if err != nil {
			return nil, false, err
		}
		account := &models.Account{
			AccountID:  accountID,
			OwnerMode:  models.AccountOwnerModeAgentOnly,
			ClaimState: models.AccountClaimStateUnclaimed,
		}
		if err := s.st.CreateAccount(ctx, account); err == nil {
			created, err := s.st.GetAccount(ctx, accountID)
			return created, true, err
		} else if err != storage.ErrEntryExists {
			return nil, false, err
		}
	}
	return nil, false, status.Error(codes.Aborted, "failed to create claimable account")
}

func (s *accountServiceServer) ensureUserHasClaimableAccount(ctx context.Context, user *models.User) (*models.Account, bool, error) {
	createdAccount := false
	var (
		account *models.Account
		err     error
	)
	if strings.TrimSpace(user.AccountID) == "" {
		account, createdAccount, err = s.createAgentOnlyAccount(ctx)
		if err != nil {
			return nil, false, err
		}
		user.AccountID = account.AccountID
		if err := s.st.UpdateUser(ctx, user); err != nil {
			return nil, false, err
		}
	} else {
		account, err = s.st.GetAccount(ctx, user.AccountID)
		if err != nil {
			return nil, false, err
		}
	}
	return account, createdAccount, nil
}

func (s *accountServiceServer) ensureUserAccountLinkedToHuman(ctx context.Context, user *models.User) (*models.Account, bool, error) {
	createdAccount := false
	var account *models.Account
	var err error
	if strings.TrimSpace(user.AccountID) == "" {
		account, createdAccount, err = s.createHumanAttachedAccount(ctx)
		if err != nil {
			return nil, false, err
		}
		user.AccountID = account.AccountID
		if err := s.st.UpdateUser(ctx, user); err != nil {
			return nil, false, err
		}
	} else {
		account, err = s.st.GetAccount(ctx, user.AccountID)
		if err != nil {
			return nil, false, err
		}
	}

	needsUpdate := account.OwnerMode != models.AccountOwnerModeHumanAttached || account.ClaimState != models.AccountClaimStateClaimed
	if needsUpdate {
		account.OwnerMode = models.AccountOwnerModeHumanAttached
		account.ClaimState = models.AccountClaimStateClaimed
		account.ClaimTokenHash = ""
		if err := s.st.UpdateAccount(ctx, account); err != nil {
			return nil, false, err
		}
		account, err = s.st.GetAccount(ctx, account.AccountID)
		if err != nil {
			return nil, false, err
		}
	}
	return account, createdAccount, nil
}

func (s *accountServiceServer) clerkUserExists(ctx context.Context, clerkUserID string) (bool, error) {
	clerkUserID = strings.TrimSpace(clerkUserID)
	if clerkUserID == "" {
		return false, nil
	}
	secretKey := strings.TrimSpace(os.Getenv("CLERK_SECRET_KEY"))
	if secretKey == "" {
		// Keep the existing conflict behavior unless the server can verify the old Clerk id is gone.
		return true, nil
	}
	endpoint := s.clerkBackendAPIBaseURL() + "/users/" + url.PathEscape(clerkUserID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return true, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+secretKey)

	resp, err := s.clerkClient().Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound, http.StatusGone:
		return false, nil
	default:
		return true, nil
	}
}

func (s *accountServiceServer) canReplaceClerkUserID(ctx context.Context, oldClerkUserID, newClerkUserID string) (bool, error) {
	oldClerkUserID = strings.TrimSpace(oldClerkUserID)
	newClerkUserID = strings.TrimSpace(newClerkUserID)
	if oldClerkUserID == "" || oldClerkUserID == newClerkUserID {
		return true, nil
	}
	exists, err := s.clerkUserExists(ctx, oldClerkUserID)
	if err != nil {
		return false, err
	}
	return !exists, nil
}

func (s *accountServiceServer) usersForAccount(ctx context.Context, accountID string) ([]*models.User, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, storage.ErrInvalidInput
	}
	users, err := s.st.ListUsers(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]*models.User, 0, 1)
	for _, user := range users {
		if user != nil && strings.TrimSpace(user.AccountID) == accountID {
			out = append(out, user)
		}
	}
	return out, nil
}

func (s *accountServiceServer) revokeAuthSessionsForUser(ctx context.Context, username string) (int, error) {
	sessions, err := s.st.ListAuthSessionsByUser(ctx, username)
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, session := range sessions {
		if session == nil || strings.TrimSpace(session.SessionID) == "" {
			continue
		}
		if err := s.st.RevokeAuthSession(ctx, username, session.SessionID); err != nil && err != storage.ErrEntryNotFound {
			return revoked, err
		}
		revoked++
	}
	return revoked, nil
}

func applyClerkUserHints(user *models.User, clerkUserID, name, email string) bool {
	updated := false
	clerkUserID = strings.TrimSpace(clerkUserID)
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)

	if clerkUserID != "" && user.ClerkUserID != clerkUserID {
		user.ClerkUserID = clerkUserID
		updated = true
	}
	if user.AuthSource != "clerk" {
		user.AuthSource = "clerk"
		updated = true
	}
	if name != "" && user.Name == "" {
		user.Name = name
		updated = true
	}
	if email != "" && validateEmail(email) && user.PrimaryEmail == "" {
		user.PrimaryEmail = email
		updated = true
	}
	return updated
}

func verifySignedClerkClaims(rawValue string, now time.Time) (*clerkBridgeClaims, error) {
	secret := strings.TrimSpace(os.Getenv("AUTH_SECRET"))
	if secret == "" {
		return nil, status.Error(codes.FailedPrecondition, "AUTH_SECRET is not configured")
	}
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return nil, status.Error(codes.InvalidArgument, "signed_claims is required")
	}
	payloadPart, signaturePart, ok := strings.Cut(rawValue, ".")
	if !ok || strings.TrimSpace(payloadPart) == "" || strings.TrimSpace(signaturePart) == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid signed_claims format")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payloadPart))
	expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signaturePart), []byte(expectedSignature)) {
		return nil, status.Error(codes.Unauthenticated, "invalid signed Clerk claims signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid signed_claims payload")
	}
	var claims clerkBridgeClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid signed_claims payload")
	}
	if strings.ToLower(strings.TrimSpace(claims.Provider)) != "clerk" {
		return nil, status.Error(codes.InvalidArgument, "signed_claims provider must be clerk")
	}
	if strings.TrimSpace(claims.UserID) == "" {
		return nil, status.Error(codes.InvalidArgument, "signed_claims userId is required")
	}
	if claims.IssuedAtMs <= 0 || claims.ExpiresAtMs <= 0 {
		return nil, status.Error(codes.InvalidArgument, "signed_claims timestamps are required")
	}
	issuedAt := time.UnixMilli(claims.IssuedAtMs)
	expiresAt := time.UnixMilli(claims.ExpiresAtMs)
	if expiresAt.Before(issuedAt) {
		return nil, status.Error(codes.InvalidArgument, "signed_claims expiry is invalid")
	}
	if expiresAt.Sub(issuedAt) > bridgeTokenMaxLifetime {
		return nil, status.Error(codes.InvalidArgument, "signed_claims lifetime is too long")
	}
	if issuedAt.After(now.Add(bridgeTokenClockSkew)) {
		return nil, status.Error(codes.Unauthenticated, "signed Clerk claims are not valid yet")
	}
	if !expiresAt.After(now.Add(-bridgeTokenClockSkew)) {
		return nil, status.Error(codes.Unauthenticated, "signed Clerk claims have expired")
	}
	return &claims, nil
}

func clerkChosenUsername(req *accountv1.EnsureClerkLocalIdentityRequest) (string, error) {
	if req == nil || strings.TrimSpace(req.GetPreferredUsername()) == "" {
		return "", status.Error(codes.FailedPrecondition, "username required")
	}
	username := strings.ToLower(strings.TrimSpace(req.GetPreferredUsername()))
	if !auth.ValidateUsername(username) {
		return "", status.Error(codes.InvalidArgument, "invalid username")
	}
	return username, nil
}

func (s *accountServiceServer) ensureUsernameAvailable(ctx context.Context, username string) error {
	if _, err := s.st.GetUser(ctx, username); err == nil {
		return status.Error(codes.AlreadyExists, "username already taken")
	} else if err != storage.ErrEntryNotFound {
		return status.Error(codes.Internal, "failed to check username")
	}
	if _, err := s.st.GetOrganization(ctx, username); err == nil {
		return status.Error(codes.AlreadyExists, "username already taken")
	} else if err != storage.ErrEntryNotFound {
		return status.Error(codes.Internal, "failed to check username")
	}
	return nil
}

func (s *accountServiceServer) ensureClerkLocalIdentity(ctx context.Context, claims *clerkBridgeClaims, req *accountv1.EnsureClerkLocalIdentityRequest) (*providerLocalIdentityResult, error) {
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return nil, status.Error(codes.InvalidArgument, "missing Clerk user id")
	}

	displayName := strings.TrimSpace(claims.Name)
	primaryEmail := normalizeEmail(claims.Email)
	claimToken := ""
	if req != nil {
		claimToken = strings.TrimSpace(req.GetClaimToken())
	}

	if claimToken != "" {
		claimTokenHash := hashClaimToken(claimToken)
		account, err := s.st.GetAccountByClaimTokenHash(ctx, claimTokenHash)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.NotFound, "account claim token not found")
			}
			return nil, status.Error(codes.Internal, "failed to resolve account claim token")
		}
		accountUsers, err := s.usersForAccount(ctx, account.AccountID)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to list account users")
		}
		if len(accountUsers) != 1 {
			return nil, status.Error(codes.FailedPrecondition, "account claim requires exactly one local user")
		}
		targetUser := accountUsers[0]
		if linkedUser, err := s.st.GetUserByClerkUserID(ctx, claims.UserID); err == nil {
			if linkedUser.Username != targetUser.Username {
				return nil, status.Error(codes.AlreadyExists, "Clerk user is already linked to another account")
			}
		} else if err != storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, "failed to resolve linked Clerk user")
		}
		if targetUser.ClerkUserID != "" && targetUser.ClerkUserID != claims.UserID {
			canReplace, err := s.canReplaceClerkUserID(ctx, targetUser.ClerkUserID, claims.UserID)
			if err != nil {
				return nil, status.Error(codes.Internal, "failed to verify existing Clerk user")
			}
			if !canReplace {
				return nil, status.Error(codes.AlreadyExists, "account is already linked to another Clerk user")
			}
		}
		if applyClerkUserHints(targetUser, claims.UserID, displayName, primaryEmail) {
			if err := s.st.UpdateUser(ctx, targetUser); err != nil {
				if err == storage.ErrEntryExists {
					return nil, status.Error(codes.AlreadyExists, "email already in use")
				}
				return nil, status.Error(codes.Internal, "failed to attach Clerk identity")
			}
			targetUser, err = s.st.GetUser(ctx, targetUser.Username)
			if err != nil {
				return nil, status.Error(codes.Internal, "failed to reload claimed user")
			}
		}
		account.OwnerMode = models.AccountOwnerModeHumanAttached
		account.ClaimState = models.AccountClaimStateClaimed
		account.ClaimTokenHash = ""
		if err := s.st.UpdateAccount(ctx, account); err != nil {
			return nil, status.Error(codes.Internal, "failed to finalize claimed account")
		}
		account, err = s.st.GetAccount(ctx, account.AccountID)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to reload claimed account")
		}
		if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, targetUser.Username); err != nil {
			return nil, status.Error(codes.Internal, "failed to provision home slice")
		}
		return &providerLocalIdentityResult{
			user:               targetUser,
			account:            account,
			linkedExistingUser: true,
			claimedAccount:     true,
		}, nil
	}

	if linkedUser, err := s.st.GetUserByClerkUserID(ctx, claims.UserID); err == nil {
		account, createdAccount, err := s.ensureUserAccountLinkedToHuman(ctx, linkedUser)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to ensure linked account")
		}
		if applyClerkUserHints(linkedUser, claims.UserID, displayName, primaryEmail) {
			if err := s.st.UpdateUser(ctx, linkedUser); err != nil {
				if err == storage.ErrEntryExists {
					return nil, status.Error(codes.AlreadyExists, "email already in use")
				}
				return nil, status.Error(codes.Internal, "failed to update linked user")
			}
			linkedUser, err = s.st.GetUser(ctx, linkedUser.Username)
			if err != nil {
				return nil, status.Error(codes.Internal, "failed to load linked user")
			}
		}
		if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, linkedUser.Username); err != nil {
			return nil, status.Error(codes.Internal, "failed to provision home slice")
		}
		return &providerLocalIdentityResult{
			user:           linkedUser,
			account:        account,
			createdAccount: createdAccount,
		}, nil
	} else if err != storage.ErrEntryNotFound {
		return nil, status.Error(codes.Internal, "failed to resolve linked Clerk user")
	}

	if primaryEmail != "" && validateEmail(primaryEmail) {
		if existingUser, err := s.st.GetUserByEmail(ctx, primaryEmail); err == nil {
			if strings.TrimSpace(existingUser.AccountID) != "" {
				if existingAccount, accountErr := s.st.GetAccount(ctx, existingUser.AccountID); accountErr == nil {
					if existingAccount.OwnerMode == models.AccountOwnerModeAgentOnly {
						return nil, status.Error(codes.FailedPrecondition, "account claim token required to attach to an agent-created account")
					}
				} else if accountErr != storage.ErrEntryNotFound {
					return nil, status.Error(codes.Internal, "failed to resolve existing account")
				}
			}
			if existingUser.ClerkUserID != "" && existingUser.ClerkUserID != claims.UserID {
				canReplace, err := s.canReplaceClerkUserID(ctx, existingUser.ClerkUserID, claims.UserID)
				if err != nil {
					return nil, status.Error(codes.Internal, "failed to verify existing Clerk user")
				}
				if !canReplace {
					return nil, status.Error(codes.AlreadyExists, "email already linked to another Clerk user")
				}
			}
			account, createdAccount, err := s.ensureUserAccountLinkedToHuman(ctx, existingUser)
			if err != nil {
				return nil, status.Error(codes.Internal, "failed to ensure existing account")
			}
			if applyClerkUserHints(existingUser, claims.UserID, displayName, primaryEmail) {
				if err := s.st.UpdateUser(ctx, existingUser); err != nil {
					if err == storage.ErrEntryExists {
						return nil, status.Error(codes.AlreadyExists, "email already in use")
					}
					return nil, status.Error(codes.Internal, "failed to link existing user")
				}
				existingUser, err = s.st.GetUser(ctx, existingUser.Username)
				if err != nil {
					return nil, status.Error(codes.Internal, "failed to reload linked user")
				}
			}
			if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, existingUser.Username); err != nil {
				return nil, status.Error(codes.Internal, "failed to provision home slice")
			}
			return &providerLocalIdentityResult{
				user:               existingUser,
				account:            account,
				createdAccount:     createdAccount,
				linkedExistingUser: true,
			}, nil
		} else if err != storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, "failed to resolve local user by email")
		}
	}

	username, err := clerkChosenUsername(req)
	if err != nil {
		return nil, err
	}
	if err := s.ensureUsernameAvailable(ctx, username); err != nil {
		return nil, err
	}

	account, createdAccount, err := s.createHumanAttachedAccount(ctx)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create local account")
	}

	user := &models.User{
		Username:     username,
		AccountID:    account.AccountID,
		Name:         displayName,
		PrimaryEmail: primaryEmail,
		AuthSource:   "clerk",
		ClerkUserID:  claims.UserID,
	}
	if err := s.st.CreateUser(ctx, user); err != nil {
		if err == storage.ErrEntryExists {
			return nil, status.Error(codes.AlreadyExists, "username already taken")
		}
		if err == storage.ErrEntryNotFound {
			return nil, status.Error(codes.Internal, "failed to attach account to created user")
		}
		return nil, status.Error(codes.Internal, "failed to create local user")
	}
	if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, username); err != nil {
		return nil, status.Error(codes.Internal, "failed to provision home slice")
	}
	createdUser, err := s.st.GetUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load created user")
	}
	return &providerLocalIdentityResult{
		user:           createdUser,
		account:        account,
		createdAccount: createdAccount,
		createdUser:    true,
	}, nil
}

func ensureClerkLocalIdentityResultToProto(result *providerLocalIdentityResult) *accountv1.EnsureClerkLocalIdentityResponse {
	if result == nil {
		return nil
	}
	accountID := ""
	if result.account != nil {
		accountID = result.account.AccountID
	}
	return &accountv1.EnsureClerkLocalIdentityResponse{
		User:               userToProto(result.user),
		AccountId:          accountID,
		CreatedAccount:     result.createdAccount,
		CreatedUser:        result.createdUser,
		LinkedExistingUser: result.linkedExistingUser,
		ClaimedAccount:     result.claimedAccount,
	}
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

func (s *accountServiceServer) rotateSessionTokens(ctx context.Context, session *models.AuthSession) (*models.AuthSession, error) {
	if session == nil || strings.TrimSpace(session.SessionID) == "" {
		return nil, status.Error(codes.InvalidArgument, "session is required")
	}
	for i := 0; i < 3; i++ {
		accessToken, err := randomToken("gs_", 24)
		if err != nil {
			return nil, err
		}
		refreshToken, err := randomToken("gsr_", 24)
		if err != nil {
			return nil, err
		}
		accessTokenExpiresAt := time.Now().Add(accessTokenTTL)
		refreshTokenExpiresAt := time.Now().Add(refreshTokenTTL)
		err = s.st.UpdateAuthSessionTokens(ctx, session.SessionID, accessToken, &accessTokenExpiresAt, refreshToken, &refreshTokenExpiresAt)
		if err == nil {
			sessionCopy := *session
			sessionCopy.Token = accessToken
			sessionCopy.RefreshToken = refreshToken
			sessionCopy.AccessTokenExpiresAt = &accessTokenExpiresAt
			sessionCopy.RefreshTokenExpiresAt = &refreshTokenExpiresAt
			return &sessionCopy, nil
		}
		if err != storage.ErrEntryExists {
			return nil, err
		}
	}
	return nil, status.Error(codes.Aborted, "failed to rotate session tokens")
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

func (s *accountServiceServer) SignupWithAgentKey(ctx context.Context, req *accountv1.SignupWithAgentKeyRequest) (*accountv1.AuthResponse, error) {
	username := strings.TrimSpace(req.GetUsername())
	email := normalizeEmail(req.GetEmail())
	name := strings.TrimSpace(req.GetName())
	keyName := strings.TrimSpace(req.GetKeyName())
	algorithm := normalizeAgentKeyAlgorithm(req.GetAlgorithm())
	if algorithm == "" {
		algorithm = "ed25519"
	}
	publicKey := append([]byte(nil), req.GetPublicKey()...)

	if !auth.ValidateUsername(username) {
		return nil, status.Error(codes.InvalidArgument, "invalid username")
	}
	if !validateEmail(email) {
		return nil, status.Error(codes.InvalidArgument, "invalid email")
	}
	if keyName == "" {
		return nil, status.Error(codes.InvalidArgument, "key_name is required")
	}
	if err := validateAgentPublicKey(algorithm, publicKey); err != nil {
		return nil, err
	}

	fingerprint := agentKeyFingerprint(algorithm, publicKey)
	if _, err := s.st.GetAgentKeyByFingerprint(ctx, fingerprint); err == nil {
		return nil, status.Error(codes.AlreadyExists, "agent key already exists")
	} else if err != nil && err != storage.ErrEntryNotFound {
		return nil, status.Error(codes.Internal, "failed to check agent key")
	}

	if err := s.st.CreateUser(ctx, &models.User{
		Username:     username,
		Name:         name,
		PrimaryEmail: email,
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

	cleanupUser := true
	defer func() {
		if cleanupUser {
			_ = s.st.DeleteUser(ctx, username)
		}
	}()

	var createdKeyID string
	for i := 0; i < 3; i++ {
		keyID, err := randomToken("agk_", 16)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to create agent key id")
		}
		key := &models.AgentKey{
			KeyID:       keyID,
			Username:    username,
			Name:        keyName,
			Algorithm:   algorithm,
			PublicKey:   publicKey,
			Fingerprint: fingerprint,
			State:       models.AgentKeyStateActive,
		}
		err = s.st.CreateAgentKey(ctx, key)
		if err == nil {
			createdKeyID = keyID
			break
		}
		switch err {
		case storage.ErrEntryExists:
			if existing, lookupErr := s.st.GetAgentKeyByFingerprint(ctx, fingerprint); lookupErr == nil && existing != nil {
				return nil, status.Error(codes.AlreadyExists, "agent key already exists")
			}
			continue
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.Internal, "failed to attach agent key to account")
		case storage.ErrInvalidInput:
			return nil, status.Error(codes.InvalidArgument, "invalid agent key")
		default:
			return nil, status.Error(codes.Internal, "failed to create agent key")
		}
	}
	if createdKeyID == "" {
		return nil, status.Error(codes.Aborted, "failed to create agent key")
	}

	user, err := s.st.GetUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load created user")
	}
	if _, _, err := s.ensureUserHasClaimableAccount(ctx, user); err != nil {
		return nil, status.Error(codes.Internal, "failed to create claimable account")
	}

	if _, err := homeslice.EnsureUserHomeSlice(ctx, s.st, username); err != nil {
		return nil, status.Error(codes.Internal, "failed to provision home slice")
	}
	user, err = s.st.GetUser(ctx, username)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	session, err := s.createRefreshableSession(ctx, username, createdKeyID)
	if err != nil {
		if stErr, ok := status.FromError(err); ok {
			return nil, stErr.Err()
		}
		return nil, status.Error(codes.Internal, "failed to create session")
	}
	cleanupUser = false
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
	session, err = s.rotateSessionTokens(ctx, session)
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

func (s *accountServiceServer) ListAuthMethods(ctx context.Context, req *accountv1.ListAuthMethodsRequest) (*accountv1.ListAuthMethodsResponse, error) {
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
	return &accountv1.ListAuthMethodsResponse{Methods: authMethodsForUser(user)}, nil
}

func (s *accountServiceServer) LinkAuthMethod(ctx context.Context, req *accountv1.LinkAuthMethodRequest) (*accountv1.AuthMethod, error) {
	if _, err := s.resolveIdentity(ctx); err != nil {
		return nil, err
	}

	methodType := req.GetType()
	provider := strings.ToLower(strings.TrimSpace(req.GetProvider()))

	switch methodType {
	case accountv1.AuthMethodType_AUTH_METHOD_TYPE_OAUTH:
		if provider == "clerk" {
			return nil, status.Error(codes.Unimplemented, "Clerk auth method linking is handled by sign-in")
		}
		return nil, status.Error(codes.Unimplemented, "auth method linking is not supported for this provider")
	default:
		return nil, status.Error(codes.Unimplemented, "auth method linking is not supported for this method type")
	}
}

func (s *accountServiceServer) DeleteAuthMethod(ctx context.Context, req *accountv1.DeleteAuthMethodRequest) (*emptypb.Empty, error) {
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

	switch strings.TrimSpace(req.GetMethodId()) {
	case "password":
		if strings.TrimSpace(user.PasswordHash) == "" {
			return nil, status.Error(codes.NotFound, "password auth method not found")
		}
		if strings.TrimSpace(user.ClerkUserID) == "" {
			return nil, status.Error(codes.FailedPrecondition, "cannot remove the only human sign-in method")
		}
		user.PasswordHash = ""
		if user.AuthSource == "" {
			user.AuthSource = "clerk"
		}
	case "oauth:clerk":
		if strings.TrimSpace(user.ClerkUserID) == "" {
			return nil, status.Error(codes.NotFound, "Clerk auth method not found")
		}
		if strings.TrimSpace(user.PasswordHash) == "" {
			return nil, status.Error(codes.FailedPrecondition, "cannot remove the only human sign-in method")
		}
		user.ClerkUserID = ""
		if user.AuthSource == "clerk" {
			user.AuthSource = "local"
		}
	default:
		return nil, status.Error(codes.NotFound, "auth method not found")
	}

	user.UpdatedAt = time.Now()
	if err := s.st.UpdateUser(ctx, user); err != nil {
		switch err {
		case storage.ErrEntryNotFound:
			return nil, status.Error(codes.NotFound, "user not found")
		default:
			return nil, status.Error(codes.Internal, "failed to remove auth method")
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

func (s *accountServiceServer) GetAuthContext(ctx context.Context, req *accountv1.GetAuthContextRequest) (*accountv1.GetAuthContextResponse, error) {
	identity, err := authresolver.OptionalGRPCIdentity(ctx, s.st)
	if err != nil {
		return nil, err
	}
	if identity == nil || strings.TrimSpace(identity.Username) == "" {
		return &accountv1.GetAuthContextResponse{}, nil
	}

	resp := &accountv1.GetAuthContextResponse{
		Authenticated: true,
		Username:      identity.Username,
		SessionId:     identity.SessionID,
		AuthSource:    "legacy_user",
	}

	if identity.SessionID != "" {
		session, err := s.st.GetAuthSession(ctx, identity.SessionID)
		if err != nil {
			if err == storage.ErrEntryNotFound {
				return nil, status.Error(codes.Unauthenticated, "session not found")
			}
			return nil, status.Error(codes.Internal, "failed to load auth session")
		}
		resp.AuthSource = "local_session"
		resp.AgentKeyId = session.AgentKeyID
		if session.AgentKeyID != "" {
			resp.AuthSource = "agent_key"
		}
		if session.AccessTokenExpiresAt != nil {
			resp.AccessTokenExpiresAt = session.AccessTokenExpiresAt.Format(timeRFC3339)
		}
		if session.RefreshTokenExpiresAt != nil {
			resp.RefreshTokenExpiresAt = session.RefreshTokenExpiresAt.Format(timeRFC3339)
		}
	}

	user, err := s.st.GetUser(ctx, identity.Username)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return resp, nil
		}
		return nil, status.Error(codes.Internal, "failed to load user")
	}
	resp.AccountId = user.AccountID
	resp.ClerkUserId = user.ClerkUserID
	resp.ClerkLinked = strings.TrimSpace(user.ClerkUserID) != ""
	return resp, nil
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
	if _, err := s.st.RevokeAuthSessionsByAgentKey(ctx, identity.username, keyID); err != nil {
		return nil, status.Error(codes.Internal, "failed to revoke sessions for agent key")
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

func (s *accountServiceServer) CreateAccountClaimToken(ctx context.Context, req *accountv1.CreateAccountClaimTokenRequest) (*accountv1.CreateAccountClaimTokenResponse, error) {
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
	account, _, err := s.ensureUserHasClaimableAccount(ctx, user)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to resolve account")
	}
	if account.OwnerMode != models.AccountOwnerModeAgentOnly {
		return nil, status.Error(codes.FailedPrecondition, "account is already linked to a human identity")
	}
	claimToken, err := randomToken("claim_", 18)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to create account claim token")
	}
	account.ClaimState = models.AccountClaimStateUnclaimed
	account.ClaimTokenHash = hashClaimToken(claimToken)
	if err := s.st.UpdateAccount(ctx, account); err != nil {
		return nil, status.Error(codes.Internal, "failed to store account claim token")
	}
	claimURL := publicWebBaseURL() + "/auth/claim-account?token=" + url.QueryEscape(claimToken) + "&callbackUrl=" + url.QueryEscape("/browser")
	return &accountv1.CreateAccountClaimTokenResponse{
		AccountId:  account.AccountID,
		ClaimToken: claimToken,
		ClaimUrl:   claimURL,
	}, nil
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

func (s *accountServiceServer) EnsureClerkLocalIdentity(ctx context.Context, req *accountv1.EnsureClerkLocalIdentityRequest) (*accountv1.EnsureClerkLocalIdentityResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	claims, err := verifySignedClerkClaims(req.GetSignedClaims(), time.Now())
	if err != nil {
		return nil, err
	}
	result, err := s.ensureClerkLocalIdentity(ctx, claims, req)
	if err != nil {
		return nil, err
	}
	if req.GetIssueLocalSession() {
		session, err := s.createRefreshableSession(ctx, result.user.Username, "")
		if err != nil {
			if stErr, ok := status.FromError(err); ok {
				return nil, stErr.Err()
			}
			return nil, status.Error(codes.Internal, "failed to create local session")
		}
		result.localSession = session
	}
	resp := ensureClerkLocalIdentityResultToProto(result)
	if result.localSession != nil {
		localAuth, err := s.buildAuthResponse(ctx, result.user, result.localSession)
		if err != nil {
			return nil, err
		}
		resp.LocalAuth = localAuth
	}
	return resp, nil
}

func (s *accountServiceServer) HandleClerkWebhook(ctx context.Context, req *httpbody.HttpBody) (*accountv1.HandleClerkWebhookResponse, error) {
	if req == nil || len(req.GetData()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "webhook payload is required")
	}
	payload := req.GetData()
	idHeader := clerkWebhookHeaderFromContext(ctx, "svix-id", "webhook-id")
	timestampHeader := clerkWebhookHeaderFromContext(ctx, "svix-timestamp", "webhook-timestamp")
	signatureHeader := clerkWebhookHeaderFromContext(ctx, "svix-signature", "webhook-signature")
	if err := verifyClerkWebhookSignature(payload, idHeader, timestampHeader, signatureHeader, os.Getenv("CLERK_WEBHOOK_SECRET"), time.Now()); err != nil {
		return nil, err
	}

	var event clerkWebhookEnvelope
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid Clerk webhook payload")
	}
	eventName := strings.TrimSpace(event.Type)
	clerkUserID := strings.TrimSpace(event.Data.ID)
	resp := &accountv1.HandleClerkWebhookResponse{
		Event:       eventName,
		ClerkUserId: clerkUserID,
		Action:      "ignored",
	}
	if clerkUserID == "" {
		return resp, nil
	}

	user, err := s.st.GetUserByClerkUserID(ctx, clerkUserID)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return resp, nil
		}
		return nil, status.Error(codes.Internal, "failed to load Clerk-linked user")
	}
	resp.Username = user.Username

	switch eventName {
	case "user.updated":
		updated := false
		primaryEmailID := strings.TrimSpace(event.Data.PrimaryEmailAddressID)
		primaryEmail := ""
		for _, email := range event.Data.EmailAddresses {
			if primaryEmailID != "" && strings.TrimSpace(email.ID) != primaryEmailID {
				continue
			}
			primaryEmail = normalizeEmail(email.EmailAddress)
			break
		}
		if primaryEmail == "" && len(event.Data.EmailAddresses) > 0 {
			primaryEmail = normalizeEmail(event.Data.EmailAddresses[0].EmailAddress)
		}
		if primaryEmail != "" && primaryEmail != user.PrimaryEmail {
			user.PrimaryEmail = primaryEmail
			updated = true
		}
		if name := strings.TrimSpace(strings.Join([]string{event.Data.FirstName, event.Data.LastName}, " ")); name != "" && name != user.Name {
			user.Name = name
			updated = true
		}
		if !updated {
			resp.Action = "noop"
			return resp, nil
		}
		user.UpdatedAt = time.Now()
		if err := s.st.UpdateUser(ctx, user); err != nil {
			return nil, status.Error(codes.Internal, "failed to update Clerk-linked user")
		}
		resp.Action = "updated_user"
	case "user.deleted":
		revoked, err := s.revokeAuthSessionsForUser(ctx, user.Username)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to revoke sessions")
		}
		user.ClerkUserID = ""
		if user.AuthSource == "clerk" {
			user.AuthSource = "local"
		}
		user.UpdatedAt = time.Now()
		if err := s.st.UpdateUser(ctx, user); err != nil {
			return nil, status.Error(codes.Internal, "failed to unlink Clerk user")
		}
		resp.Action = "unlinked_user"
		resp.RevokedSessions = int32(revoked)
	}
	return resp, nil
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
