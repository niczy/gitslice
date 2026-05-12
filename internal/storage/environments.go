package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

var environmentNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

const defaultEnvironmentAgentType = "codex"

var validEnvironmentAgentTypes = map[string]struct{}{
	"codex":  {},
	"claude": {},
}

var validEnvironmentProviders = map[string]struct{}{
	"e2b":                   {},
	"cloudflare_containers": {},
	"local":                 {},
}

func ValidateEnvironmentProviderConfig(provider string, values map[string]string) error {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "e2b"
	}

	runtimeWSPath := strings.TrimSpace(values["runtime_ws_path"])
	if runtimeWSPath != "" && !strings.HasPrefix(runtimeWSPath, "/") {
		return fmt.Errorf("providerConfig.runtime_ws_path must start with '/'")
	}

	if provider != "cloudflare_containers" {
		return nil
	}

	workerBaseURL := strings.TrimSpace(values["worker_base_url"])
	if workerBaseURL == "" {
		return fmt.Errorf("providerConfig.worker_base_url is required for cloudflare_containers")
	}
	parsedURL, err := url.Parse(workerBaseURL)
	if err != nil || parsedURL == nil || strings.TrimSpace(parsedURL.Host) == "" {
		return fmt.Errorf("providerConfig.worker_base_url must be a valid absolute URL")
	}
	switch strings.ToLower(strings.TrimSpace(parsedURL.Scheme)) {
	case "http", "https":
	default:
		return fmt.Errorf("providerConfig.worker_base_url must use http or https")
	}

	if strings.TrimSpace(values["container_class"]) == "" {
		return fmt.Errorf("providerConfig.container_class is required for cloudflare_containers")
	}
	if strings.TrimSpace(values["instance_type"]) == "" {
		return fmt.Errorf("providerConfig.instance_type is required for cloudflare_containers")
	}

	runtimeStreamPath := strings.TrimSpace(values["runtime_stream_path"])
	if runtimeStreamPath != "" && !strings.HasPrefix(runtimeStreamPath, "/") {
		return fmt.Errorf("providerConfig.runtime_stream_path must start with '/'")
	}

	startupTimeoutSec := strings.TrimSpace(values["startup_timeout_sec"])
	if startupTimeoutSec != "" {
		parsedTimeout, parseErr := strconv.Atoi(startupTimeoutSec)
		if parseErr != nil || parsedTimeout <= 0 {
			return fmt.Errorf("providerConfig.startup_timeout_sec must be a positive integer")
		}
	}

	return nil
}

func normalizeEnvironmentAgentTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{"codex", "claude"}, nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			continue
		}
		if _, ok := validEnvironmentAgentTypes[value]; !ok {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, ErrInvalidInput
	}
	return out, nil
}

func environmentAgentTypeAllowed(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

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
	if _, ok := validEnvironmentProviders[provider]; !ok {
		return nil, ErrInvalidInput
	}
	providerID := strings.TrimSpace(env.ProviderID)
	if providerID == "" && provider != "local" {
		return nil, ErrInvalidInput
	}
	providerConfig, err := normalizeEnvironmentProviderConfig(env.ProviderConfig)
	if err != nil {
		return nil, err
	}
	if err := ValidateEnvironmentProviderConfig(provider, providerConfig); err != nil {
		return nil, ErrInvalidInput
	}
	defaultAgentType := strings.ToLower(strings.TrimSpace(env.DefaultAgentType))
	if defaultAgentType == "" {
		defaultAgentType = defaultEnvironmentAgentType
	}
	if _, ok := validEnvironmentAgentTypes[defaultAgentType]; !ok {
		return nil, ErrInvalidInput
	}
	allowedAgentTypes, err := normalizeEnvironmentAgentTypes(env.AllowedAgentTypes)
	if err != nil {
		return nil, err
	}
	if !environmentAgentTypeAllowed(allowedAgentTypes, defaultAgentType) {
		return nil, ErrInvalidInput
	}

	now := time.Now()
	copy := *env
	copy.Name = name
	copy.DisplayName = strings.TrimSpace(env.DisplayName)
	copy.Provider = provider
	copy.ProviderID = providerID
	copy.ProviderConfig = providerConfig
	copy.Region = strings.TrimSpace(env.Region)
	copy.DefaultAgentType = defaultAgentType
	copy.AllowedAgentTypes = append([]string(nil), allowedAgentTypes...)
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
	if _, ok := validEnvironmentProviders[provider]; !ok {
		return nil, ErrInvalidInput
	}
	providerID := strings.TrimSpace(env.ProviderID)
	if providerID == "" && provider != "local" {
		return nil, ErrInvalidInput
	}
	providerConfig, err := normalizeEnvironmentProviderConfig(env.ProviderConfig)
	if err != nil {
		return nil, err
	}
	if err := ValidateEnvironmentProviderConfig(provider, providerConfig); err != nil {
		return nil, ErrInvalidInput
	}
	defaultAgentType := strings.ToLower(strings.TrimSpace(env.DefaultAgentType))
	if defaultAgentType == "" {
		defaultAgentType = defaultEnvironmentAgentType
	}
	if _, ok := validEnvironmentAgentTypes[defaultAgentType]; !ok {
		return nil, ErrInvalidInput
	}
	allowedAgentTypes, err := normalizeEnvironmentAgentTypes(env.AllowedAgentTypes)
	if err != nil {
		return nil, err
	}
	if !environmentAgentTypeAllowed(allowedAgentTypes, defaultAgentType) {
		return nil, ErrInvalidInput
	}
	copy := *env
	copy.Name = name
	copy.DisplayName = strings.TrimSpace(env.DisplayName)
	copy.Provider = provider
	copy.ProviderID = providerID
	copy.ProviderConfig = providerConfig
	copy.Region = strings.TrimSpace(env.Region)
	copy.DefaultAgentType = defaultAgentType
	copy.AllowedAgentTypes = append([]string(nil), allowedAgentTypes...)
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
	if env.AllowedAgentTypes != nil {
		copy.AllowedAgentTypes = append([]string(nil), env.AllowedAgentTypes...)
	}
	if env.ProviderConfig != nil {
		copy.ProviderConfig = make(map[string]string, len(env.ProviderConfig))
		for key, value := range env.ProviderConfig {
			copy.ProviderConfig[key] = value
		}
	}
	return &copy
}

func normalizeEnvironmentProviderConfig(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for rawKey, rawValue := range values {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return nil, ErrInvalidInput
		}
		out[key] = strings.TrimSpace(rawValue)
	}
	return out, nil
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
		INSERT INTO environments (
			name, display_name, provider, provider_id, provider_config_json, region, default_agent_type, allowed_agent_types_json,
			created_by, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8::jsonb, $9, $10, $11)
	`, normalized.Name, normalized.DisplayName, normalized.Provider, normalized.ProviderID,
		mustMarshalProviderConfigJSON(normalized.ProviderConfig), normalized.Region,
		normalized.DefaultAgentType, mustMarshalAgentTypesJSON(normalized.AllowedAgentTypes),
		normalized.CreatedBy, normalized.CreatedAt, normalized.UpdatedAt)
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
	var providerConfigJSON []byte
	var allowedAgentTypesJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT name, display_name, provider, provider_id, COALESCE(provider_config_json, '{}'::jsonb), region, COALESCE(default_agent_type, 'codex'),
		       COALESCE(allowed_agent_types_json, '["codex","claude"]'::jsonb),
		       created_by, created_at, updated_at
		FROM environments WHERE name = $1
	`, name).Scan(
		&env.Name, &env.DisplayName, &env.Provider, &env.ProviderID, &providerConfigJSON, &env.Region,
		&env.DefaultAgentType, &allowedAgentTypesJSON,
		&env.CreatedBy, &env.CreatedAt, &env.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(allowedAgentTypesJSON, &env.AllowedAgentTypes); err != nil || len(env.AllowedAgentTypes) == 0 {
		env.AllowedAgentTypes = []string{"codex", "claude"}
	}
	if err := json.Unmarshal(providerConfigJSON, &env.ProviderConfig); err != nil {
		env.ProviderConfig = nil
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
		SELECT name, display_name, provider, provider_id, COALESCE(provider_config_json, '{}'::jsonb), region, COALESCE(default_agent_type, 'codex'),
		       COALESCE(allowed_agent_types_json, '["codex","claude"]'::jsonb),
		       created_by, created_at, updated_at
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
		var providerConfigJSON []byte
		var allowedAgentTypesJSON []byte
		if err := rows.Scan(
			&env.Name, &env.DisplayName, &env.Provider, &env.ProviderID, &providerConfigJSON, &env.Region,
			&env.DefaultAgentType, &allowedAgentTypesJSON,
			&env.CreatedBy, &env.CreatedAt, &env.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(allowedAgentTypesJSON, &env.AllowedAgentTypes); err != nil || len(env.AllowedAgentTypes) == 0 {
			env.AllowedAgentTypes = []string{"codex", "claude"}
		}
		if err := json.Unmarshal(providerConfigJSON, &env.ProviderConfig); err != nil {
			env.ProviderConfig = nil
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
		SET display_name = $1, provider = $2, provider_id = $3, provider_config_json = $4::jsonb, region = $5,
		    default_agent_type = $6, allowed_agent_types_json = $7::jsonb,
		    created_by = $8, updated_at = $9
		WHERE name = $10
	`, normalized.DisplayName, normalized.Provider, normalized.ProviderID,
		mustMarshalProviderConfigJSON(normalized.ProviderConfig), normalized.Region,
		normalized.DefaultAgentType, mustMarshalAgentTypesJSON(normalized.AllowedAgentTypes),
		normalized.CreatedBy, normalized.UpdatedAt, normalized.Name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func mustMarshalAgentTypesJSON(values []string) string {
	encoded, err := json.Marshal(values)
	if err != nil {
		return `["codex","claude"]`
	}
	return string(encoded)
}

func mustMarshalProviderConfigJSON(values map[string]string) string {
	if len(values) == 0 {
		return `{}`
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return `{}`
	}
	return string(encoded)
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
