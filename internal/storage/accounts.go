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

func copyAuthSession(session *models.AuthSession) *models.AuthSession {
	if session == nil {
		return nil
	}
	out := *session
	if session.RevokedAt != nil {
		revoked := *session.RevokedAt
		out.RevokedAt = &revoked
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

	newUser := *user
	newUser.Username = username
	newUser.PrimaryEmail = email
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

	if oldEmail := normalizeEmail(existing.PrimaryEmail); oldEmail != "" && oldEmail != email {
		delete(s.userByEmail, oldEmail)
	}

	updated := *user
	updated.Username = username
	updated.PrimaryEmail = email
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

	newSession := *session
	newSession.SessionID = sessionID
	newSession.Username = username
	newSession.Token = token
	if newSession.CreatedAt.IsZero() {
		newSession.CreatedAt = now
	}
	if newSession.LastSeenAt.IsZero() {
		newSession.LastSeenAt = newSession.CreatedAt
	}
	newSession.RevokedAt = nil

	s.authSessions[sessionID] = &newSession
	s.authSessionByToken[token] = sessionID
	if s.authSessionsByUser[username] == nil {
		s.authSessionsByUser[username] = make(map[string]bool)
	}
	s.authSessionsByUser[username][sessionID] = true
	return nil
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

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[slug]; ok {
		return ErrEntryExists
	}
	if _, ok := s.users[slug]; ok {
		return ErrEntryExists
	}

	newOrg := *org
	newOrg.Slug = slug
	newOrg.Name = name
	newOrg.CreatedBy = createdBy
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

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.orgs[slug]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}

	updated := *org
	updated.Slug = slug
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

	newMember := *member
	newMember.OrgSlug = orgSlug
	newMember.Username = username
	newMember.Role = role
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

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	existingMap := s.orgMembers[orgSlug]
	existing, ok := existingMap[username]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}

	updated := *member
	updated.OrgSlug = orgSlug
	updated.Username = username
	updated.Role = role
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
