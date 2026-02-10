package storage

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/models"
)

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
		copy := *existing
		return &copy, nil
	}

	u := &models.User{
		Username:  username,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.users[username] = u
	copy := *u
	return &copy, nil
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
	copy := *u
	return &copy, nil
}

func (s *InMemoryStorage) CreateOrganization(ctx context.Context, org *models.Organization) error {
	_ = ctx
	if org == nil {
		return ErrInvalidInput
	}
	if org.Slug == "" || org.Name == "" || org.CreatedBy == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(org.CreatedBy) {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[org.Slug]; ok {
		return ErrEntryExists
	}

	newOrg := *org
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
	copy := *org
	return &copy, nil
}

func (s *InMemoryStorage) AddOrganizationMember(ctx context.Context, member *models.OrganizationMember) error {
	_ = ctx
	if member == nil {
		return ErrInvalidInput
	}
	if member.OrgSlug == "" || member.Username == "" || member.Role == "" {
		return ErrInvalidInput
	}
	if !auth.ValidateUsername(member.Username) {
		return ErrInvalidInput
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.orgs[member.OrgSlug]; !ok {
		return ErrEntryNotFound
	}
	if s.orgMembers[member.OrgSlug] == nil {
		s.orgMembers[member.OrgSlug] = make(map[string]*models.OrganizationMember)
	}
	if _, ok := s.orgMembers[member.OrgSlug][member.Username]; ok {
		return ErrEntryExists
	}

	newMember := *member
	if newMember.CreatedAt.IsZero() {
		newMember.CreatedAt = now
	}
	newMember.UpdatedAt = now
	s.orgMembers[member.OrgSlug][member.Username] = &newMember

	if s.userOrgs[member.Username] == nil {
		s.userOrgs[member.Username] = make(map[string]bool)
	}
	s.userOrgs[member.Username][member.OrgSlug] = true

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
			copy := *org
			out = append(out, &copy)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}
