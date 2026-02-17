package storage

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

var environmentNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func normalizeEnvironmentForCreate(env *models.Environment) (*models.Environment, error) {
	if env == nil {
		return nil, ErrInvalidInput
	}
	name := strings.TrimSpace(env.Name)
	if !environmentNameRE.MatchString(name) {
		return nil, ErrInvalidInput
	}
	provider := strings.TrimSpace(env.Provider)
	if provider == "" {
		provider = "e2b"
	}
	providerID := strings.TrimSpace(env.ProviderID)
	if providerID == "" {
		return nil, ErrInvalidInput
	}

	now := time.Now()
	copy := *env
	copy.Name = name
	copy.DisplayName = strings.TrimSpace(env.DisplayName)
	copy.Provider = provider
	copy.ProviderID = providerID
	copy.Region = strings.TrimSpace(env.Region)
	copy.CreatedBy = strings.TrimSpace(env.CreatedBy)
	if copy.CreatedAt.IsZero() {
		copy.CreatedAt = now
	}
	copy.UpdatedAt = now
	return &copy, nil
}

func normalizeEnvironmentForUpdate(env *models.Environment) (*models.Environment, error) {
	if env == nil {
		return nil, ErrInvalidInput
	}
	name := strings.TrimSpace(env.Name)
	if !environmentNameRE.MatchString(name) {
		return nil, ErrInvalidInput
	}
	provider := strings.TrimSpace(env.Provider)
	if provider == "" {
		provider = "e2b"
	}
	providerID := strings.TrimSpace(env.ProviderID)
	if providerID == "" {
		return nil, ErrInvalidInput
	}
	copy := *env
	copy.Name = name
	copy.DisplayName = strings.TrimSpace(env.DisplayName)
	copy.Provider = provider
	copy.ProviderID = providerID
	copy.Region = strings.TrimSpace(env.Region)
	copy.CreatedBy = strings.TrimSpace(env.CreatedBy)
	if copy.CreatedAt.IsZero() {
		copy.CreatedAt = time.Now()
	}
	copy.UpdatedAt = time.Now()
	return &copy, nil
}

func copyEnvironment(env *models.Environment) *models.Environment {
	if env == nil {
		return nil
	}
	copy := *env
	return &copy
}

func (s *InMemoryStorage) CreateEnvironment(ctx context.Context, env *models.Environment) error {
	_ = ctx
	normalized, err := normalizeEnvironmentForCreate(env)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.environments[normalized.Name]; exists {
		return ErrEntryExists
	}
	s.environments[normalized.Name] = normalized
	return nil
}

func (s *InMemoryStorage) GetEnvironment(ctx context.Context, name string) (*models.Environment, error) {
	_ = ctx
	name = strings.TrimSpace(name)
	if !environmentNameRE.MatchString(name) {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	env, ok := s.environments[name]
	if !ok || env == nil {
		return nil, ErrEntryNotFound
	}
	return copyEnvironment(env), nil
}

func (s *InMemoryStorage) ListEnvironments(ctx context.Context, limit, offset int) ([]*models.Environment, error) {
	_ = ctx
	if offset < 0 {
		return nil, ErrInvalidInput
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make([]*models.Environment, 0, len(s.environments))
	for _, env := range s.environments {
		all = append(all, copyEnvironment(env))
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	if offset >= len(all) {
		return []*models.Environment{}, nil
	}
	if limit <= 0 || offset+limit > len(all) {
		limit = len(all) - offset
	}
	return all[offset : offset+limit], nil
}

func (s *InMemoryStorage) UpdateEnvironment(ctx context.Context, env *models.Environment) error {
	_ = ctx
	normalized, err := normalizeEnvironmentForUpdate(env)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.environments[normalized.Name]
	if !exists || existing == nil {
		return ErrEntryNotFound
	}
	normalized.CreatedAt = existing.CreatedAt
	if strings.TrimSpace(normalized.CreatedBy) == "" {
		normalized.CreatedBy = existing.CreatedBy
	}
	s.environments[normalized.Name] = normalized
	return nil
}

func (s *InMemoryStorage) DeleteEnvironment(ctx context.Context, name string) error {
	_ = ctx
	name = strings.TrimSpace(name)
	if !environmentNameRE.MatchString(name) {
		return ErrInvalidInput
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.environments[name]; !exists {
		return ErrEntryNotFound
	}
	delete(s.environments, name)
	return nil
}

func (s *PostgresNativeStorage) CreateEnvironment(ctx context.Context, env *models.Environment) error {
	ctx = ensureCtx(ctx)
	normalized, err := normalizeEnvironmentForCreate(env)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO environments (name, display_name, provider, provider_id, region, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, normalized.Name, normalized.DisplayName, normalized.Provider, normalized.ProviderID, normalized.Region, normalized.CreatedBy, normalized.CreatedAt, normalized.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return ErrEntryExists
		}
		return err
	}
	return nil
}

func (s *PostgresNativeStorage) GetEnvironment(ctx context.Context, name string) (*models.Environment, error) {
	ctx = ensureCtx(ctx)
	name = strings.TrimSpace(name)
	if !environmentNameRE.MatchString(name) {
		return nil, ErrInvalidInput
	}

	var env models.Environment
	err := s.pool.QueryRow(ctx, `
		SELECT name, display_name, provider, provider_id, region, created_by, created_at, updated_at
		FROM environments WHERE name = $1
	`, name).Scan(&env.Name, &env.DisplayName, &env.Provider, &env.ProviderID, &env.Region, &env.CreatedBy, &env.CreatedAt, &env.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return &env, nil
}

func (s *PostgresNativeStorage) ListEnvironments(ctx context.Context, limit, offset int) ([]*models.Environment, error) {
	ctx = ensureCtx(ctx)
	if limit <= 0 {
		limit = int(^uint(0) >> 1)
	}
	if offset < 0 {
		return nil, ErrInvalidInput
	}

	rows, err := s.pool.Query(ctx, `
		SELECT name, display_name, provider, provider_id, region, created_by, created_at, updated_at
		FROM environments
		ORDER BY name
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*models.Environment{}
	for rows.Next() {
		var env models.Environment
		if err := rows.Scan(&env.Name, &env.DisplayName, &env.Provider, &env.ProviderID, &env.Region, &env.CreatedBy, &env.CreatedAt, &env.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &env)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) UpdateEnvironment(ctx context.Context, env *models.Environment) error {
	ctx = ensureCtx(ctx)
	normalized, err := normalizeEnvironmentForUpdate(env)
	if err != nil {
		return err
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE environments
		SET display_name = $1, provider = $2, provider_id = $3, region = $4, created_by = $5, updated_at = $6
		WHERE name = $7
	`, normalized.DisplayName, normalized.Provider, normalized.ProviderID, normalized.Region, normalized.CreatedBy, normalized.UpdatedAt, normalized.Name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) DeleteEnvironment(ctx context.Context, name string) error {
	ctx = ensureCtx(ctx)
	name = strings.TrimSpace(name)
	if !environmentNameRE.MatchString(name) {
		return ErrInvalidInput
	}

	tag, err := s.pool.Exec(ctx, `DELETE FROM environments WHERE name = $1`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}
