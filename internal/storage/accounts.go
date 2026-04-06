package storage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/models"
)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateEmail(email string) bool {
	email = normalizeEmail(email)
	if email == "" {
		return false
	}
	at := strings.Index(email, "@")
	if at <= 0 || at >= len(email)-1 {
		return false
	}
	return true
}

func rootPathForSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	if strings.HasPrefix(slug, "/") {
		return slug
	}
	return "/" + slug
}

func copyUser(user *models.User) *models.User {
	if user == nil {
		return nil
	}
	out := *user
	return &out
}

func copyAccount(account *models.Account) *models.Account {
	if account == nil {
		return nil
	}
	out := *account
	return &out
}

func copyAuthSession(session *models.AuthSession) *models.AuthSession {
	if session == nil {
		return nil
	}
	out := *session
	if session.AccessTokenExpiresAt != nil {
		expiresAt := *session.AccessTokenExpiresAt
		out.AccessTokenExpiresAt = &expiresAt
	}
	if session.RefreshTokenExpiresAt != nil {
		expiresAt := *session.RefreshTokenExpiresAt
		out.RefreshTokenExpiresAt = &expiresAt
	}
	if session.RevokedAt != nil {
		revoked := *session.RevokedAt
		out.RevokedAt = &revoked
	}
	return &out
}

func copyAgentKey(key *models.AgentKey) *models.AgentKey {
	if key == nil {
		return nil
	}
	out := *key
	if key.PublicKey != nil {
		out.PublicKey = append([]byte(nil), key.PublicKey...)
	}
	if key.LastUsedAt != nil {
		lastUsedAt := *key.LastUsedAt
		out.LastUsedAt = &lastUsedAt
	}
	if key.RevokedAt != nil {
		revokedAt := *key.RevokedAt
		out.RevokedAt = &revokedAt
	}
	return &out
}

func copyAgentKeyChallenge(challenge *models.AgentKeyChallenge) *models.AgentKeyChallenge {
	if challenge == nil {
		return nil
	}
	out := *challenge
	if challenge.Challenge != nil {
		out.Challenge = append([]byte(nil), challenge.Challenge...)
	}
	if challenge.UsedAt != nil {
		usedAt := *challenge.UsedAt
		out.UsedAt = &usedAt
	}
	return &out
}

func copyDeviceAuthorization(authorization *models.DeviceAuthorization) *models.DeviceAuthorization {
	if authorization == nil {
		return nil
	}
	out := *authorization
	if authorization.ApprovedAt != nil {
		approvedAt := *authorization.ApprovedAt
		out.ApprovedAt = &approvedAt
	}
	if authorization.DeniedAt != nil {
		deniedAt := *authorization.DeniedAt
		out.DeniedAt = &deniedAt
	}
	return &out
}

func normalizeOrganizationRole(role models.OrganizationRole) models.OrganizationRole {
	return models.OrganizationRole(strings.ToLower(strings.TrimSpace(string(role))))
}

func validOrganizationRole(role models.OrganizationRole) bool {
	switch normalizeOrganizationRole(role) {
	case models.OrganizationRoleOwner, models.OrganizationRoleAdmin, models.OrganizationRoleMember:
		return true
	default:
		return false
	}
}

func normalizeOrganizationInviteStatus(status models.OrganizationInviteStatus) models.OrganizationInviteStatus {
	return models.OrganizationInviteStatus(strings.ToLower(strings.TrimSpace(string(status))))
}

func validOrganizationInviteStatus(status models.OrganizationInviteStatus) bool {
	switch normalizeOrganizationInviteStatus(status) {
	case models.OrganizationInvitePending, models.OrganizationInviteAccepted, models.OrganizationInviteDeclined:
		return true
	default:
		return false
	}
}

func copyOrganizationMember(member *models.OrganizationMember) *models.OrganizationMember {
	if member == nil {
		return nil
	}
	out := *member
	return &out
}

func copyOrganizationInvite(invite *models.OrganizationInvite) *models.OrganizationInvite {
	if invite == nil {
		return nil
	}
	out := *invite
	return &out
}

func copyTeam(team *models.Team) *models.Team {
	if team == nil {
		return nil
	}
	out := *team
	return &out
}

func copyTeamMember(member *models.TeamMember) *models.TeamMember {
	if member == nil {
		return nil
	}
	out := *member
	return &out
}

func normalizeAccountOwnerMode(mode models.AccountOwnerMode) models.AccountOwnerMode {
	return models.AccountOwnerMode(strings.ToLower(strings.TrimSpace(string(mode))))
}

func validAccountOwnerMode(mode models.AccountOwnerMode) bool {
	switch normalizeAccountOwnerMode(mode) {
	case models.AccountOwnerModeAgentOnly, models.AccountOwnerModeHumanAttached, models.AccountOwnerModeOrgManaged:
		return true
	default:
		return false
	}
}

func normalizeAccountClaimState(state models.AccountClaimState) models.AccountClaimState {
	return models.AccountClaimState(strings.ToLower(strings.TrimSpace(string(state))))
}

func validAccountClaimState(state models.AccountClaimState) bool {
	switch normalizeAccountClaimState(state) {
	case models.AccountClaimStateUnclaimed, models.AccountClaimStateClaimed:
		return true
	default:
		return false
	}
}

func (s *InMemoryStorage) CreateAccount(ctx context.Context, account *models.Account) error {
	_ = ctx
	if account == nil {
		return ErrInvalidInput
	}
	accountID := strings.TrimSpace(account.AccountID)
	ownerMode := normalizeAccountOwnerMode(account.OwnerMode)
	claimState := normalizeAccountClaimState(account.ClaimState)
	claimTokenHash := strings.TrimSpace(account.ClaimTokenHash)
	if accountID == "" || !validAccountOwnerMode(ownerMode) || !validAccountClaimState(claimState) {
		return ErrInvalidInput
	}

	now := time.Now()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	account.AccountID = accountID
	account.OwnerMode = ownerMode
	account.ClaimState = claimState
	account.ClaimTokenHash = claimTokenHash

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.accounts[accountID]; exists {
		return ErrEntryExists
	}
	if claimTokenHash != "" {
		if existingID, exists := s.accountByClaimTokenHash[claimTokenHash]; exists && existingID != accountID {
			return ErrEntryExists
		}
		s.accountByClaimTokenHash[claimTokenHash] = accountID
	}
	s.accounts[accountID] = copyAccount(account)
	return nil
}

func (s *InMemoryStorage) GetAccount(ctx context.Context, accountID string) (*models.Account, error) {
	_ = ctx
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	account, ok := s.accounts[accountID]
	if !ok || account == nil {
		return nil, ErrEntryNotFound
	}
	return copyAccount(account), nil
}

func (s *InMemoryStorage) GetAccountByClaimTokenHash(ctx context.Context, claimTokenHash string) (*models.Account, error) {
	_ = ctx
	claimTokenHash = strings.TrimSpace(claimTokenHash)
	if claimTokenHash == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	accountID, ok := s.accountByClaimTokenHash[claimTokenHash]
	if !ok {
		return nil, ErrEntryNotFound
	}
	account, ok := s.accounts[accountID]
	if !ok || account == nil {
		return nil, ErrEntryNotFound
	}
	return copyAccount(account), nil
}

func (s *InMemoryStorage) UpdateAccount(ctx context.Context, account *models.Account) error {
	_ = ctx
	if account == nil {
		return ErrInvalidInput
	}
	accountID := strings.TrimSpace(account.AccountID)
	ownerMode := normalizeAccountOwnerMode(account.OwnerMode)
	claimState := normalizeAccountClaimState(account.ClaimState)
	claimTokenHash := strings.TrimSpace(account.ClaimTokenHash)
	if accountID == "" || !validAccountOwnerMode(ownerMode) || !validAccountClaimState(claimState) {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.accounts[accountID]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}
	if claimTokenHash != "" {
		if existingID, exists := s.accountByClaimTokenHash[claimTokenHash]; exists && existingID != accountID {
			return ErrEntryExists
		}
	}
	if existing.ClaimTokenHash != "" {
		delete(s.accountByClaimTokenHash, existing.ClaimTokenHash)
	}
	updated := copyAccount(existing)
	updated.OwnerMode = ownerMode
	updated.ClaimState = claimState
	updated.ClaimTokenHash = claimTokenHash
	updated.UpdatedAt = time.Now()
	if claimTokenHash != "" {
		s.accountByClaimTokenHash[claimTokenHash] = accountID
	}
	s.accounts[accountID] = updated
	return nil
}

func (s *InMemoryStorage) EnsureUser(ctx context.Context, username string) (*models.User, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.users[username]; ok && existing != nil {
		if existing.RootPath == "" {
			existing.RootPath = rootPathForSlug(existing.Username)
		}
		return copyUser(existing), nil
	}
	if _, exists := s.orgs[username]; exists {
		return nil, ErrEntryExists
	}

	u := &models.User{
		Username:  username,
		RootPath:  rootPathForSlug(username),
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.users[username] = u
	return copyUser(u), nil
}

func (s *InMemoryStorage) GetUser(ctx context.Context, username string) (*models.User, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[username]
	if !ok || u == nil {
		return nil, ErrEntryNotFound
	}
	if u.RootPath == "" {
		u.RootPath = rootPathForSlug(u.Username)
	}
	return copyUser(u), nil
}

func (s *InMemoryStorage) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	_ = ctx
	email = normalizeEmail(email)
	if !validateEmail(email) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	username, ok := s.userByEmail[email]
	if !ok {
		return nil, ErrEntryNotFound
	}
	u, ok := s.users[username]
	if !ok || u == nil {
		return nil, ErrEntryNotFound
	}
	return copyUser(u), nil
}

func (s *InMemoryStorage) ListUsers(ctx context.Context, limit, offset int) ([]*models.User, error) {
	_ = ctx
	if offset < 0 {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	usernames := make([]string, 0, len(s.users))
	for username := range s.users {
		usernames = append(usernames, username)
	}
	sort.Strings(usernames)

	if offset > len(usernames) {
		offset = len(usernames)
	}
	end := len(usernames)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}

	users := make([]*models.User, 0, end-offset)
	for _, username := range usernames[offset:end] {
		user := s.users[username]
		if user == nil {
			continue
		}
		if user.RootPath == "" {
			user.RootPath = rootPathForSlug(user.Username)
		}
		users = append(users, copyUser(user))
	}
	return users, nil
}

func (s *InMemoryStorage) CreateUser(ctx context.Context, user *models.User) error {
	_ = ctx
	if user == nil {
		return ErrInvalidInput
	}
	username := strings.TrimSpace(user.Username)
	if !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}
	email := normalizeEmail(user.PrimaryEmail)
	if email != "" && !validateEmail(email) {
		return ErrInvalidInput
	}
	accountID := strings.TrimSpace(user.AccountID)
	authSource := strings.TrimSpace(user.AuthSource)
	workOSUserID := strings.TrimSpace(user.WorkOSUserID)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[username]; ok {
		return ErrEntryExists
	}
	if _, ok := s.orgs[username]; ok {
		return ErrEntryExists
	}
	if email != "" {
		if _, ok := s.userByEmail[email]; ok {
			return ErrEntryExists
		}
	}
	if accountID != "" {
		if _, ok := s.accounts[accountID]; !ok {
			return ErrEntryNotFound
		}
	}
	if workOSUserID != "" {
		for _, existing := range s.users {
			if existing != nil && strings.TrimSpace(existing.WorkOSUserID) == workOSUserID {
				return ErrEntryExists
			}
		}
	}

	newUser := *user
	newUser.Username = username
	newUser.AccountID = accountID
	newUser.PrimaryEmail = email
	newUser.AuthSource = authSource
	newUser.WorkOSUserID = workOSUserID
	newUser.RootPath = rootPathForSlug(username)
	if newUser.CreatedAt.IsZero() {
		newUser.CreatedAt = now
	}
	newUser.UpdatedAt = now
	s.users[username] = &newUser
	if email != "" {
		s.userByEmail[email] = username
	}
	return nil
}

func (s *InMemoryStorage) UpdateUser(ctx context.Context, user *models.User) error {
	_ = ctx
	if user == nil {
		return ErrInvalidInput
	}
	username := strings.TrimSpace(user.Username)
	if !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}
	email := normalizeEmail(user.PrimaryEmail)
	if email != "" && !validateEmail(email) {
		return ErrInvalidInput
	}
	accountID := strings.TrimSpace(user.AccountID)
	authSource := strings.TrimSpace(user.AuthSource)
	workOSUserID := strings.TrimSpace(user.WorkOSUserID)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.users[username]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}

	if email != "" {
		if owner, ok := s.userByEmail[email]; ok && owner != username {
			return ErrEntryExists
		}
	}
	if accountID != "" {
		if _, ok := s.accounts[accountID]; !ok {
			return ErrEntryNotFound
		}
	}
	if workOSUserID != "" {
		for existingUsername, existing := range s.users {
			if existingUsername == username || existing == nil {
				continue
			}
			if strings.TrimSpace(existing.WorkOSUserID) == workOSUserID {
				return ErrEntryExists
			}
		}
	}

	if oldEmail := normalizeEmail(existing.PrimaryEmail); oldEmail != "" && oldEmail != email {
		delete(s.userByEmail, oldEmail)
	}

	updated := *user
	updated.Username = username
	updated.AccountID = accountID
	updated.PrimaryEmail = email
	updated.AuthSource = authSource
	updated.WorkOSUserID = workOSUserID
	updated.RootPath = rootPathForSlug(username)
	if updated.CreatedAt.IsZero() {
		updated.CreatedAt = existing.CreatedAt
	}
	updated.UpdatedAt = now
	s.users[username] = &updated
	if email != "" {
		s.userByEmail[email] = username
	}
	return nil
}

func (s *InMemoryStorage) DeleteUser(ctx context.Context, username string) error {
	_ = ctx
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[username]
	if !ok || user == nil {
		return ErrEntryNotFound
	}
	if email := normalizeEmail(user.PrimaryEmail); email != "" {
		delete(s.userByEmail, email)
	}
	delete(s.users, username)

	if sessionIDs := s.authSessionsByUser[username]; len(sessionIDs) > 0 {
		for sessionID := range sessionIDs {
			if session, ok := s.authSessions[sessionID]; ok && session != nil {
				delete(s.authSessionByToken, session.Token)
			}
			delete(s.authSessions, sessionID)
		}
	}
	delete(s.authSessionsByUser, username)

	if orgs := s.userOrgs[username]; len(orgs) > 0 {
		for slug := range orgs {
			if members, ok := s.orgMembers[slug]; ok && members != nil {
				delete(members, username)
			}
		}
	}
	delete(s.userOrgs, username)

	for orgSlug, invites := range s.orgInvites {
		if len(invites) == 0 {
			continue
		}
		for inviteID, invite := range invites {
			if invite != nil && invite.CreatedBy == username {
				delete(invites, inviteID)
			}
		}
		if len(invites) == 0 {
			delete(s.orgInvites, orgSlug)
		}
	}

	for teamID, members := range s.teamMembers {
		if len(members) == 0 {
			continue
		}
		delete(members, username)
		if len(members) == 0 {
			delete(s.teamMembers, teamID)
		}
	}
	return nil
}

func (s *InMemoryStorage) CreateAuthSession(ctx context.Context, session *models.AuthSession) error {
	_ = ctx
	if session == nil {
		return ErrInvalidInput
	}
	sessionID := strings.TrimSpace(session.SessionID)
	username := strings.TrimSpace(session.Username)
	token := strings.TrimSpace(session.Token)
	if sessionID == "" || token == "" || !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}
	refreshToken := strings.TrimSpace(session.RefreshToken)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[username]; !ok {
		return ErrEntryNotFound
	}
	if _, ok := s.authSessions[sessionID]; ok {
		return ErrEntryExists
	}
	if _, ok := s.authSessionByToken[token]; ok {
		return ErrEntryExists
	}
	if refreshToken != "" {
		if _, ok := s.authSessionByRefreshToken[refreshToken]; ok {
			return ErrEntryExists
		}
	}
	if agentKeyID := strings.TrimSpace(session.AgentKeyID); agentKeyID != "" {
		key, ok := s.agentKeys[agentKeyID]
		if !ok || key == nil || key.Username != username {
			return ErrEntryNotFound
		}
	}

	newSession := *session
	newSession.SessionID = sessionID
	newSession.Username = username
	newSession.Token = token
	newSession.RefreshToken = refreshToken
	newSession.AgentKeyID = strings.TrimSpace(session.AgentKeyID)
	if newSession.CreatedAt.IsZero() {
		newSession.CreatedAt = now
	}
	if newSession.LastSeenAt.IsZero() {
		newSession.LastSeenAt = newSession.CreatedAt
	}
	newSession.RevokedAt = nil

	s.authSessions[sessionID] = &newSession
	s.authSessionByToken[token] = sessionID
	if refreshToken != "" {
		s.authSessionByRefreshToken[refreshToken] = sessionID
	}
	if s.authSessionsByUser[username] == nil {
		s.authSessionsByUser[username] = make(map[string]bool)
	}
	s.authSessionsByUser[username][sessionID] = true
	return nil
}

func (s *InMemoryStorage) GetAuthSession(ctx context.Context, sessionID string) (*models.AuthSession, error) {
	_ = ctx
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.authSessions[sessionID]
	if !ok || session == nil || session.RevokedAt != nil {
		return nil, ErrEntryNotFound
	}
	return copyAuthSession(session), nil
}

func (s *InMemoryStorage) GetAuthSessionByToken(ctx context.Context, token string) (*models.AuthSession, error) {
	_ = ctx
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	sessionID, ok := s.authSessionByToken[token]
	if !ok {
		return nil, ErrEntryNotFound
	}
	session, ok := s.authSessions[sessionID]
	if !ok || session == nil || session.RevokedAt != nil {
		return nil, ErrEntryNotFound
	}
	if session.AccessTokenExpiresAt != nil && !session.AccessTokenExpiresAt.After(time.Now()) {
		return nil, ErrEntryNotFound
	}
	return copyAuthSession(session), nil
}

func (s *InMemoryStorage) GetAuthSessionByRefreshToken(ctx context.Context, refreshToken string) (*models.AuthSession, error) {
	_ = ctx
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	sessionID, ok := s.authSessionByRefreshToken[refreshToken]
	if !ok {
		return nil, ErrEntryNotFound
	}
	session, ok := s.authSessions[sessionID]
	if !ok || session == nil || session.RevokedAt != nil {
		return nil, ErrEntryNotFound
	}
	if session.RefreshToken != refreshToken {
		return nil, ErrEntryNotFound
	}
	if session.RefreshTokenExpiresAt != nil && !session.RefreshTokenExpiresAt.After(time.Now()) {
		return nil, ErrEntryNotFound
	}
	return copyAuthSession(session), nil
}

func (s *InMemoryStorage) ListAuthSessionsByUser(ctx context.Context, username string) ([]*models.AuthSession, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	sessionIDs := s.authSessionsByUser[username]
	if len(sessionIDs) == 0 {
		return []*models.AuthSession{}, nil
	}

	out := make([]*models.AuthSession, 0, len(sessionIDs))
	for sessionID := range sessionIDs {
		session, ok := s.authSessions[sessionID]
		if !ok || session == nil || session.RevokedAt != nil {
			continue
		}
		out = append(out, copyAuthSession(session))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *InMemoryStorage) UpdateAuthSessionTokens(ctx context.Context, sessionID, accessToken string, accessTokenExpiresAt *time.Time, refreshToken string, refreshTokenExpiresAt *time.Time) error {
	_ = ctx
	sessionID = strings.TrimSpace(sessionID)
	accessToken = strings.TrimSpace(accessToken)
	refreshToken = strings.TrimSpace(refreshToken)
	if sessionID == "" || accessToken == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.authSessions[sessionID]
	if !ok || session == nil || session.RevokedAt != nil {
		return ErrEntryNotFound
	}
	if existingSessionID, ok := s.authSessionByToken[accessToken]; ok && existingSessionID != sessionID {
		return ErrEntryExists
	}
	if refreshToken != "" {
		if existingSessionID, ok := s.authSessionByRefreshToken[refreshToken]; ok && existingSessionID != sessionID {
			return ErrEntryExists
		}
	}

	if session.Token != "" {
		delete(s.authSessionByToken, session.Token)
	}
	if session.RefreshToken != "" {
		delete(s.authSessionByRefreshToken, session.RefreshToken)
	}
	session.Token = accessToken
	session.AccessTokenExpiresAt = nil
	if accessTokenExpiresAt != nil {
		expiresAtCopy := *accessTokenExpiresAt
		session.AccessTokenExpiresAt = &expiresAtCopy
	}
	session.RefreshToken = refreshToken
	session.RefreshTokenExpiresAt = nil
	if refreshTokenExpiresAt != nil {
		expiresAtCopy := *refreshTokenExpiresAt
		session.RefreshTokenExpiresAt = &expiresAtCopy
	}
	s.authSessionByToken[accessToken] = sessionID
	if refreshToken != "" {
		s.authSessionByRefreshToken[refreshToken] = sessionID
	}
	return nil
}

func (s *InMemoryStorage) TouchAuthSession(ctx context.Context, sessionID string, at time.Time) error {
	_ = ctx
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrInvalidInput
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.authSessions[sessionID]
	if !ok || session == nil || session.RevokedAt != nil {
		return ErrEntryNotFound
	}
	session.LastSeenAt = at
	return nil
}

func (s *InMemoryStorage) RevokeAuthSession(ctx context.Context, username, sessionID string) error {
	_ = ctx
	username = strings.TrimSpace(username)
	sessionID = strings.TrimSpace(sessionID)
	if !auth.ValidateUsername(username) || sessionID == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.authSessions[sessionID]
	if !ok || session == nil || session.Username != username {
		return ErrEntryNotFound
	}
	if session.RevokedAt != nil {
		return ErrEntryNotFound
	}
	revokedAt := now
	session.RevokedAt = &revokedAt
	delete(s.authSessionByToken, session.Token)
	if session.RefreshToken != "" {
		delete(s.authSessionByRefreshToken, session.RefreshToken)
	}
	return nil
}

func (s *InMemoryStorage) RevokeAuthSessionByToken(ctx context.Context, token string) error {
	_ = ctx
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID, ok := s.authSessionByToken[token]
	if !ok {
		return ErrEntryNotFound
	}
	session, ok := s.authSessions[sessionID]
	if !ok || session == nil || session.RevokedAt != nil {
		return ErrEntryNotFound
	}
	revokedAt := now
	session.RevokedAt = &revokedAt
	delete(s.authSessionByToken, token)
	if session.RefreshToken != "" {
		delete(s.authSessionByRefreshToken, session.RefreshToken)
	}
	return nil
}

func (s *InMemoryStorage) RevokeAuthSessionsByAgentKey(ctx context.Context, username, agentKeyID string) (int, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	agentKeyID = strings.TrimSpace(agentKeyID)
	if !auth.ValidateUsername(username) || agentKeyID == "" {
		return 0, ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionIDs := s.authSessionsByUser[username]
	if len(sessionIDs) == 0 {
		return 0, nil
	}

	revoked := 0
	for sessionID := range sessionIDs {
		session, ok := s.authSessions[sessionID]
		if !ok || session == nil || session.RevokedAt != nil || session.AgentKeyID != agentKeyID {
			continue
		}
		revokedAt := now
		session.RevokedAt = &revokedAt
		delete(s.authSessionByToken, session.Token)
		if session.RefreshToken != "" {
			delete(s.authSessionByRefreshToken, session.RefreshToken)
		}
		revoked += 1
	}

	return revoked, nil
}

func (s *InMemoryStorage) CreateAgentKey(ctx context.Context, key *models.AgentKey) error {
	_ = ctx
	if key == nil {
		return ErrInvalidInput
	}
	keyID := strings.TrimSpace(key.KeyID)
	username := strings.TrimSpace(key.Username)
	fingerprint := strings.TrimSpace(key.Fingerprint)
	algorithm := strings.TrimSpace(key.Algorithm)
	if keyID == "" || !auth.ValidateUsername(username) || fingerprint == "" || algorithm == "" || len(key.PublicKey) == 0 {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[username]; !ok {
		return ErrEntryNotFound
	}
	if _, ok := s.agentKeys[keyID]; ok {
		return ErrEntryExists
	}
	if _, ok := s.agentKeyByFingerprint[fingerprint]; ok {
		return ErrEntryExists
	}

	record := *key
	record.KeyID = keyID
	record.Username = username
	record.Algorithm = algorithm
	record.Fingerprint = fingerprint
	record.Name = strings.TrimSpace(record.Name)
	record.PublicKey = append([]byte(nil), key.PublicKey...)
	if record.State == "" {
		record.State = models.AgentKeyStateActive
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	record.RevokedAt = nil

	s.agentKeys[keyID] = &record
	s.agentKeyByFingerprint[fingerprint] = keyID
	if s.agentKeysByUser[username] == nil {
		s.agentKeysByUser[username] = make(map[string]bool)
	}
	s.agentKeysByUser[username][keyID] = true
	return nil
}

func (s *InMemoryStorage) GetAgentKey(ctx context.Context, keyID string) (*models.AgentKey, error) {
	_ = ctx
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := s.agentKeys[keyID]
	if !ok || key == nil {
		return nil, ErrEntryNotFound
	}
	return copyAgentKey(key), nil
}

func (s *InMemoryStorage) GetAgentKeyByFingerprint(ctx context.Context, fingerprint string) (*models.AgentKey, error) {
	_ = ctx
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	keyID, ok := s.agentKeyByFingerprint[fingerprint]
	if !ok {
		return nil, ErrEntryNotFound
	}
	key, ok := s.agentKeys[keyID]
	if !ok || key == nil {
		return nil, ErrEntryNotFound
	}
	return copyAgentKey(key), nil
}

func (s *InMemoryStorage) ListAgentKeysByUser(ctx context.Context, username string) ([]*models.AgentKey, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	keyIDs := s.agentKeysByUser[username]
	if len(keyIDs) == 0 {
		return []*models.AgentKey{}, nil
	}

	out := make([]*models.AgentKey, 0, len(keyIDs))
	for keyID := range keyIDs {
		key, ok := s.agentKeys[keyID]
		if !ok || key == nil {
			continue
		}
		out = append(out, copyAgentKey(key))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *InMemoryStorage) TouchAgentKey(ctx context.Context, keyID string, at time.Time) error {
	_ = ctx
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return ErrInvalidInput
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.agentKeys[keyID]
	if !ok || key == nil {
		return ErrEntryNotFound
	}
	lastUsedAt := at
	key.LastUsedAt = &lastUsedAt
	key.UpdatedAt = at
	return nil
}

func (s *InMemoryStorage) RevokeAgentKey(ctx context.Context, username, keyID string, revokedAt time.Time) error {
	_ = ctx
	username = strings.TrimSpace(username)
	keyID = strings.TrimSpace(keyID)
	if !auth.ValidateUsername(username) || keyID == "" {
		return ErrInvalidInput
	}
	if revokedAt.IsZero() {
		revokedAt = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.agentKeys[keyID]
	if !ok || key == nil || key.Username != username {
		return ErrEntryNotFound
	}
	key.State = models.AgentKeyStateRevoked
	revokedAtCopy := revokedAt
	key.RevokedAt = &revokedAtCopy
	key.UpdatedAt = revokedAt
	return nil
}

func (s *InMemoryStorage) CreateAgentKeyChallenge(ctx context.Context, challenge *models.AgentKeyChallenge) error {
	_ = ctx
	if challenge == nil {
		return ErrInvalidInput
	}
	challengeID := strings.TrimSpace(challenge.ChallengeID)
	agentKeyID := strings.TrimSpace(challenge.AgentKeyID)
	username := strings.TrimSpace(challenge.Username)
	if challengeID == "" || agentKeyID == "" || !auth.ValidateUsername(username) || len(challenge.Challenge) == 0 {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.agentKeyChallenges[challengeID]; ok {
		return ErrEntryExists
	}
	key, ok := s.agentKeys[agentKeyID]
	if !ok || key == nil {
		return ErrEntryNotFound
	}
	if key.Username != username {
		return ErrInvalidInput
	}

	record := *challenge
	record.ChallengeID = challengeID
	record.AgentKeyID = agentKeyID
	record.Username = username
	record.Challenge = append([]byte(nil), challenge.Challenge...)
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = now.Add(time.Minute)
	}
	record.UsedAt = nil
	s.agentKeyChallenges[challengeID] = &record
	return nil
}

func (s *InMemoryStorage) GetAgentKeyChallenge(ctx context.Context, challengeID string) (*models.AgentKeyChallenge, error) {
	_ = ctx
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	challenge, ok := s.agentKeyChallenges[challengeID]
	if !ok || challenge == nil {
		return nil, ErrEntryNotFound
	}
	return copyAgentKeyChallenge(challenge), nil
}

func (s *InMemoryStorage) MarkAgentKeyChallengeUsed(ctx context.Context, challengeID string, usedAt time.Time) error {
	_ = ctx
	challengeID = strings.TrimSpace(challengeID)
	if challengeID == "" {
		return ErrInvalidInput
	}
	if usedAt.IsZero() {
		usedAt = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	challenge, ok := s.agentKeyChallenges[challengeID]
	if !ok || challenge == nil {
		return ErrEntryNotFound
	}
	if challenge.UsedAt != nil {
		return ErrEntryExists
	}
	usedAtCopy := usedAt
	challenge.UsedAt = &usedAtCopy
	return nil
}

func (s *InMemoryStorage) CreateDeviceAuthorization(ctx context.Context, authorization *models.DeviceAuthorization) error {
	_ = ctx
	if authorization == nil {
		return ErrInvalidInput
	}
	deviceCode := strings.TrimSpace(authorization.DeviceCode)
	userCode := strings.TrimSpace(authorization.UserCode)
	if deviceCode == "" || userCode == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.deviceAuthorizationsByDeviceCode[deviceCode]; ok {
		return ErrEntryExists
	}
	if _, ok := s.deviceAuthorizationByUserCode[userCode]; ok {
		return ErrEntryExists
	}

	record := *authorization
	record.DeviceCode = deviceCode
	record.UserCode = userCode
	if record.Status == "" {
		record.Status = models.DeviceAuthorizationPending
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.ExpiresAt.IsZero() {
		record.ExpiresAt = now.Add(10 * time.Minute)
	}
	s.deviceAuthorizationsByDeviceCode[deviceCode] = &record
	s.deviceAuthorizationByUserCode[userCode] = deviceCode
	return nil
}

func (s *InMemoryStorage) GetDeviceAuthorizationByDeviceCode(ctx context.Context, deviceCode string) (*models.DeviceAuthorization, error) {
	_ = ctx
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.deviceAuthorizationsByDeviceCode[deviceCode]
	if !ok || record == nil {
		return nil, ErrEntryNotFound
	}
	return copyDeviceAuthorization(record), nil
}

func (s *InMemoryStorage) GetDeviceAuthorizationByUserCode(ctx context.Context, userCode string) (*models.DeviceAuthorization, error) {
	_ = ctx
	userCode = strings.TrimSpace(userCode)
	if userCode == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	deviceCode, ok := s.deviceAuthorizationByUserCode[userCode]
	if !ok {
		return nil, ErrEntryNotFound
	}
	record, ok := s.deviceAuthorizationsByDeviceCode[deviceCode]
	if !ok || record == nil {
		return nil, ErrEntryNotFound
	}
	return copyDeviceAuthorization(record), nil
}

func (s *InMemoryStorage) UpdateDeviceAuthorization(ctx context.Context, authorization *models.DeviceAuthorization) error {
	_ = ctx
	if authorization == nil {
		return ErrInvalidInput
	}
	deviceCode := strings.TrimSpace(authorization.DeviceCode)
	userCode := strings.TrimSpace(authorization.UserCode)
	if deviceCode == "" || userCode == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.deviceAuthorizationsByDeviceCode[deviceCode]
	if !ok || record == nil {
		return ErrEntryNotFound
	}
	if record.UserCode != userCode {
		return ErrInvalidInput
	}
	updated := *authorization
	updated.DeviceCode = deviceCode
	updated.UserCode = userCode
	s.deviceAuthorizationsByDeviceCode[deviceCode] = &updated
	s.deviceAuthorizationByUserCode[userCode] = deviceCode
	return nil
}

func (s *InMemoryStorage) CreateOrganization(ctx context.Context, org *models.Organization) error {
	_ = ctx
	if org == nil {
		return ErrInvalidInput
	}
	slug := strings.TrimSpace(org.Slug)
	name := strings.TrimSpace(org.Name)
	createdBy := strings.TrimSpace(org.CreatedBy)
	if slug == "" || name == "" || createdBy == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(slug) || !auth.ValidateUsername(createdBy) {
		return ErrInvalidInput
	}
	workOSOrganizationID := strings.TrimSpace(org.WorkOSOrganizationID)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[slug]; ok {
		return ErrEntryExists
	}
	if _, ok := s.users[slug]; ok {
		return ErrEntryExists
	}
	if workOSOrganizationID != "" {
		for _, existing := range s.orgs {
			if existing != nil && strings.TrimSpace(existing.WorkOSOrganizationID) == workOSOrganizationID {
				return ErrEntryExists
			}
		}
	}

	newOrg := *org
	newOrg.Slug = slug
	newOrg.Name = name
	newOrg.CreatedBy = createdBy
	newOrg.WorkOSOrganizationID = workOSOrganizationID
	newOrg.RootPath = rootPathForSlug(newOrg.Slug)
	if newOrg.CreatedAt.IsZero() {
		newOrg.CreatedAt = now
	}
	newOrg.UpdatedAt = now
	s.orgs[newOrg.Slug] = &newOrg

	return nil
}

func (s *InMemoryStorage) GetOrganization(ctx context.Context, orgSlug string) (*models.Organization, error) {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	if orgSlug == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	org, ok := s.orgs[orgSlug]
	if !ok || org == nil {
		return nil, ErrEntryNotFound
	}
	if org.RootPath == "" {
		org.RootPath = rootPathForSlug(org.Slug)
	}
	copy := *org
	return &copy, nil
}

func (s *InMemoryStorage) UpdateOrganization(ctx context.Context, org *models.Organization) error {
	_ = ctx
	if org == nil {
		return ErrInvalidInput
	}
	slug := strings.TrimSpace(org.Slug)
	if slug == "" || strings.TrimSpace(org.Name) == "" {
		return ErrInvalidInput
	}
	workOSOrganizationID := strings.TrimSpace(org.WorkOSOrganizationID)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.orgs[slug]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}
	if workOSOrganizationID != "" {
		for existingSlug, candidate := range s.orgs {
			if existingSlug == slug || candidate == nil {
				continue
			}
			if strings.TrimSpace(candidate.WorkOSOrganizationID) == workOSOrganizationID {
				return ErrEntryExists
			}
		}
	}

	updated := *org
	updated.Slug = slug
	updated.WorkOSOrganizationID = workOSOrganizationID
	updated.RootPath = rootPathForSlug(slug)
	updated.CreatedBy = existing.CreatedBy
	if updated.CreatedAt.IsZero() {
		updated.CreatedAt = existing.CreatedAt
	}
	updated.UpdatedAt = now
	s.orgs[slug] = &updated
	return nil
}

func (s *InMemoryStorage) DeleteOrganization(ctx context.Context, orgSlug string) error {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	if orgSlug == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[orgSlug]; !ok {
		return ErrEntryNotFound
	}
	delete(s.orgs, orgSlug)

	members := s.orgMembers[orgSlug]
	for username := range members {
		if orgs := s.userOrgs[username]; orgs != nil {
			delete(orgs, orgSlug)
			if len(orgs) == 0 {
				delete(s.userOrgs, username)
			}
		}
	}
	delete(s.orgMembers, orgSlug)
	delete(s.orgInvites, orgSlug)

	for teamID := range s.teamsByOrg[orgSlug] {
		delete(s.teams, teamID)
		delete(s.teamMembers, teamID)
	}
	delete(s.teamsByOrg, orgSlug)
	return nil
}

func (s *InMemoryStorage) AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	_ = ctx
	if member == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(member.OrgSlug)
	username := strings.TrimSpace(member.Username)
	role := normalizeOrganizationRole(member.Role)
	if orgSlug == "" || username == "" || role == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) || !validOrganizationRole(role) {
		return ErrInvalidInput
	}
	workOSMembershipID := strings.TrimSpace(member.WorkOSMembershipID)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[orgSlug]; !ok {
		return ErrEntryNotFound
	}
	if _, ok := s.users[username]; !ok {
		return ErrEntryNotFound
	}
	if s.orgMembers[orgSlug] == nil {
		s.orgMembers[orgSlug] = make(map[string]*models.OrganizationMember)
	}
	if _, ok := s.orgMembers[orgSlug][username]; ok {
		return ErrEntryExists
	}
	if workOSMembershipID != "" {
		for _, members := range s.orgMembers {
			for _, existing := range members {
				if existing != nil && strings.TrimSpace(existing.WorkOSMembershipID) == workOSMembershipID {
					return ErrEntryExists
				}
			}
		}
	}

	newMember := *member
	newMember.OrgSlug = orgSlug
	newMember.Username = username
	newMember.Role = role
	newMember.WorkOSMembershipID = workOSMembershipID
	if newMember.CreatedAt.IsZero() {
		newMember.CreatedAt = now
	}
	newMember.UpdatedAt = now
	s.orgMembers[orgSlug][username] = &newMember

	if s.userOrgs[username] == nil {
		s.userOrgs[username] = make(map[string]bool)
	}
	s.userOrgs[username][orgSlug] = true

	return nil
}

func (s *InMemoryStorage) GetOrganizationMember(ctx context.Context, orgSlug, username string) (*models.OrganizationMember, error) {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	members := s.orgMembers[orgSlug]
	member, ok := members[username]
	if !ok || member == nil {
		return nil, ErrEntryNotFound
	}
	return copyOrganizationMember(member), nil
}

func (s *InMemoryStorage) ListOrganizationMembers(ctx context.Context, orgSlug string) ([]*models.OrganizationMember, error) {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	if !auth.ValidateUsername(orgSlug) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.orgs[orgSlug]; !ok {
		return nil, ErrEntryNotFound
	}

	memberMap := s.orgMembers[orgSlug]
	if len(memberMap) == 0 {
		return []*models.OrganizationMember{}, nil
	}

	out := make([]*models.OrganizationMember, 0, len(memberMap))
	for _, member := range memberMap {
		if member == nil {
			continue
		}
		out = append(out, copyOrganizationMember(member))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}

func (s *InMemoryStorage) UpdateOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	_ = ctx
	if member == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(member.OrgSlug)
	username := strings.TrimSpace(member.Username)
	role := normalizeOrganizationRole(member.Role)
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) || !validOrganizationRole(role) {
		return ErrInvalidInput
	}
	workOSMembershipID := strings.TrimSpace(member.WorkOSMembershipID)

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	existingMap := s.orgMembers[orgSlug]
	existing, ok := existingMap[username]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}
	if workOSMembershipID != "" {
		for existingOrgSlug, members := range s.orgMembers {
			for existingUsername, candidate := range members {
				if existingOrgSlug == orgSlug && existingUsername == username {
					continue
				}
				if candidate != nil && strings.TrimSpace(candidate.WorkOSMembershipID) == workOSMembershipID {
					return ErrEntryExists
				}
			}
		}
	}

	updated := *member
	updated.OrgSlug = orgSlug
	updated.Username = username
	updated.Role = role
	updated.WorkOSMembershipID = workOSMembershipID
	if updated.CreatedAt.IsZero() {
		updated.CreatedAt = existing.CreatedAt
	}
	updated.UpdatedAt = now
	s.orgMembers[orgSlug][username] = &updated
	return nil
}

func (s *InMemoryStorage) RemoveOrganizationMember(ctx context.Context, orgSlug, username string) error {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(orgSlug) || !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	members := s.orgMembers[orgSlug]
	if _, ok := members[username]; !ok {
		return ErrEntryNotFound
	}
	delete(members, username)
	if len(members) == 0 {
		delete(s.orgMembers, orgSlug)
	}

	if orgs := s.userOrgs[username]; orgs != nil {
		delete(orgs, orgSlug)
		if len(orgs) == 0 {
			delete(s.userOrgs, username)
		}
	}
	for teamID := range s.teamsByOrg[orgSlug] {
		if members := s.teamMembers[teamID]; members != nil {
			delete(members, username)
			if len(members) == 0 {
				delete(s.teamMembers, teamID)
			}
		}
	}
	return nil
}

func (s *InMemoryStorage) CreateOrganizationInvite(ctx context.Context, invite *models.OrganizationInvite) error {
	_ = ctx
	if invite == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(invite.OrgSlug)
	inviteID := strings.TrimSpace(invite.InviteID)
	targetEmail := normalizeEmail(invite.TargetEmail)
	createdBy := strings.TrimSpace(invite.CreatedBy)
	role := normalizeOrganizationRole(invite.Role)
	status := normalizeOrganizationInviteStatus(invite.Status)

	if !auth.ValidateUsername(orgSlug) || inviteID == "" || !validateEmail(targetEmail) || !auth.ValidateUsername(createdBy) || !validOrganizationRole(role) {
		return ErrInvalidInput
	}
	if status == "" {
		status = models.OrganizationInvitePending
	}
	if !validOrganizationInviteStatus(status) {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[orgSlug]; !ok {
		return ErrEntryNotFound
	}
	if _, ok := s.users[createdBy]; !ok {
		return ErrEntryNotFound
	}
	if s.orgInvites[orgSlug] == nil {
		s.orgInvites[orgSlug] = make(map[string]*models.OrganizationInvite)
	}
	if _, ok := s.orgInvites[orgSlug][inviteID]; ok {
		return ErrEntryExists
	}
	for _, existing := range s.orgInvites[orgSlug] {
		if existing == nil {
			continue
		}
		if normalizeEmail(existing.TargetEmail) == targetEmail && normalizeOrganizationInviteStatus(existing.Status) == models.OrganizationInvitePending {
			return ErrEntryExists
		}
	}

	newInvite := *invite
	newInvite.InviteID = inviteID
	newInvite.OrgSlug = orgSlug
	newInvite.TargetEmail = targetEmail
	newInvite.CreatedBy = createdBy
	newInvite.Role = role
	newInvite.Status = status
	if newInvite.CreatedAt.IsZero() {
		newInvite.CreatedAt = now
	}
	newInvite.UpdatedAt = now
	s.orgInvites[orgSlug][inviteID] = &newInvite
	return nil
}

func (s *InMemoryStorage) GetOrganizationInvite(ctx context.Context, orgSlug, inviteID string) (*models.OrganizationInvite, error) {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	inviteID = strings.TrimSpace(inviteID)
	if !auth.ValidateUsername(orgSlug) || inviteID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	invites := s.orgInvites[orgSlug]
	invite, ok := invites[inviteID]
	if !ok || invite == nil {
		return nil, ErrEntryNotFound
	}
	return copyOrganizationInvite(invite), nil
}

func (s *InMemoryStorage) UpdateOrganizationInvite(ctx context.Context, invite *models.OrganizationInvite) error {
	_ = ctx
	if invite == nil {
		return ErrInvalidInput
	}
	orgSlug := strings.TrimSpace(invite.OrgSlug)
	inviteID := strings.TrimSpace(invite.InviteID)
	targetEmail := normalizeEmail(invite.TargetEmail)
	role := normalizeOrganizationRole(invite.Role)
	status := normalizeOrganizationInviteStatus(invite.Status)
	createdBy := strings.TrimSpace(invite.CreatedBy)

	if !auth.ValidateUsername(orgSlug) || inviteID == "" || !validateEmail(targetEmail) || !auth.ValidateUsername(createdBy) || !validOrganizationRole(role) || !validOrganizationInviteStatus(status) {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	invites := s.orgInvites[orgSlug]
	existing, ok := invites[inviteID]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}

	if status == models.OrganizationInvitePending {
		for existingID, existingInvite := range invites {
			if existingID == inviteID || existingInvite == nil {
				continue
			}
			if normalizeEmail(existingInvite.TargetEmail) == targetEmail && normalizeOrganizationInviteStatus(existingInvite.Status) == models.OrganizationInvitePending {
				return ErrEntryExists
			}
		}
	}

	updated := *invite
	updated.OrgSlug = orgSlug
	updated.InviteID = inviteID
	updated.TargetEmail = targetEmail
	updated.CreatedBy = createdBy
	updated.Role = role
	updated.Status = status
	if updated.CreatedAt.IsZero() {
		updated.CreatedAt = existing.CreatedAt
	}
	updated.UpdatedAt = now
	invites[inviteID] = &updated
	return nil
}

func (s *InMemoryStorage) CreateTeam(ctx context.Context, team *models.Team) error {
	_ = ctx
	if team == nil {
		return ErrInvalidInput
	}
	teamID := strings.TrimSpace(team.TeamID)
	orgSlug := strings.TrimSpace(team.OrgSlug)
	name := strings.TrimSpace(team.Name)
	createdBy := strings.TrimSpace(team.CreatedBy)
	if teamID == "" || !auth.ValidateUsername(orgSlug) || name == "" || !auth.ValidateUsername(createdBy) {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[orgSlug]; !ok {
		return ErrEntryNotFound
	}
	if _, ok := s.teams[teamID]; ok {
		return ErrEntryExists
	}
	for existingTeamID := range s.teamsByOrg[orgSlug] {
		existing := s.teams[existingTeamID]
		if existing != nil && strings.EqualFold(existing.Name, name) {
			return ErrEntryExists
		}
	}

	newTeam := *team
	newTeam.TeamID = teamID
	newTeam.OrgSlug = orgSlug
	newTeam.Name = name
	newTeam.CreatedBy = createdBy
	if newTeam.CreatedAt.IsZero() {
		newTeam.CreatedAt = now
	}
	newTeam.UpdatedAt = now
	s.teams[teamID] = &newTeam

	if s.teamsByOrg[orgSlug] == nil {
		s.teamsByOrg[orgSlug] = make(map[string]bool)
	}
	s.teamsByOrg[orgSlug][teamID] = true
	return nil
}

func (s *InMemoryStorage) GetTeam(ctx context.Context, teamID string) (*models.Team, error) {
	_ = ctx
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	team, ok := s.teams[teamID]
	if !ok || team == nil {
		return nil, ErrEntryNotFound
	}
	return copyTeam(team), nil
}

func (s *InMemoryStorage) ListTeams(ctx context.Context, orgSlug string) ([]*models.Team, error) {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	if !auth.ValidateUsername(orgSlug) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.orgs[orgSlug]; !ok {
		return nil, ErrEntryNotFound
	}
	teamSet := s.teamsByOrg[orgSlug]
	if len(teamSet) == 0 {
		return []*models.Team{}, nil
	}

	out := make([]*models.Team, 0, len(teamSet))
	for teamID := range teamSet {
		if team, ok := s.teams[teamID]; ok && team != nil {
			out = append(out, copyTeam(team))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TeamID < out[j].TeamID })
	return out, nil
}

func (s *InMemoryStorage) UpdateTeam(ctx context.Context, team *models.Team) error {
	_ = ctx
	if team == nil {
		return ErrInvalidInput
	}
	teamID := strings.TrimSpace(team.TeamID)
	name := strings.TrimSpace(team.Name)
	if teamID == "" || name == "" {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.teams[teamID]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}
	for existingTeamID := range s.teamsByOrg[existing.OrgSlug] {
		if existingTeamID == teamID {
			continue
		}
		other := s.teams[existingTeamID]
		if other != nil && strings.EqualFold(other.Name, name) {
			return ErrEntryExists
		}
	}

	updated := *team
	updated.TeamID = teamID
	updated.OrgSlug = existing.OrgSlug
	updated.CreatedBy = existing.CreatedBy
	updated.Name = name
	if updated.CreatedAt.IsZero() {
		updated.CreatedAt = existing.CreatedAt
	}
	updated.UpdatedAt = now
	s.teams[teamID] = &updated
	return nil
}

func (s *InMemoryStorage) DeleteTeam(ctx context.Context, orgSlug, teamID string) error {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	teamID = strings.TrimSpace(teamID)
	if !auth.ValidateUsername(orgSlug) || teamID == "" {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	team, ok := s.teams[teamID]
	if !ok || team == nil || team.OrgSlug != orgSlug {
		return ErrEntryNotFound
	}
	delete(s.teams, teamID)
	if teamSet := s.teamsByOrg[orgSlug]; teamSet != nil {
		delete(teamSet, teamID)
		if len(teamSet) == 0 {
			delete(s.teamsByOrg, orgSlug)
		}
	}
	delete(s.teamMembers, teamID)
	return nil
}

func (s *InMemoryStorage) AddTeamMember(ctx context.Context, member *models.TeamMember) error {
	_ = ctx
	if member == nil {
		return ErrInvalidInput
	}
	teamID := strings.TrimSpace(member.TeamID)
	username := strings.TrimSpace(member.Username)
	if teamID == "" || !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	team, ok := s.teams[teamID]
	if !ok || team == nil {
		return ErrEntryNotFound
	}
	if _, ok := s.orgMembers[team.OrgSlug][username]; !ok {
		return ErrEntryNotFound
	}
	if s.teamMembers[teamID] == nil {
		s.teamMembers[teamID] = make(map[string]*models.TeamMember)
	}
	if _, ok := s.teamMembers[teamID][username]; ok {
		return ErrEntryExists
	}

	newMember := *member
	newMember.TeamID = teamID
	newMember.Username = username
	if newMember.AddedAt.IsZero() {
		newMember.AddedAt = now
	}
	s.teamMembers[teamID][username] = &newMember
	return nil
}

func (s *InMemoryStorage) DeleteTeamMember(ctx context.Context, orgSlug, teamID, username string) error {
	_ = ctx
	orgSlug = strings.TrimSpace(orgSlug)
	teamID = strings.TrimSpace(teamID)
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(orgSlug) || teamID == "" || !auth.ValidateUsername(username) {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	team, ok := s.teams[teamID]
	if !ok || team == nil || team.OrgSlug != orgSlug {
		return ErrEntryNotFound
	}
	members := s.teamMembers[teamID]
	if _, ok := members[username]; !ok {
		return ErrEntryNotFound
	}
	delete(members, username)
	if len(members) == 0 {
		delete(s.teamMembers, teamID)
	}
	return nil
}

func (s *InMemoryStorage) ListOrganizationsForUser(ctx context.Context, username string) ([]*models.Organization, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	if !auth.ValidateUsername(username) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	orgSet := s.userOrgs[username]
	if len(orgSet) == 0 {
		return []*models.Organization{}, nil
	}

	out := make([]*models.Organization, 0, len(orgSet))
	for slug := range orgSet {
		if org, ok := s.orgs[slug]; ok && org != nil {
			if org.RootPath == "" {
				org.RootPath = rootPathForSlug(org.Slug)
			}
			copy := *org
			out = append(out, &copy)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}
