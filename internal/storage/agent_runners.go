package storage

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/niczy/gitslice/internal/models"
)

func normalizeAgentRunner(runner *models.AgentRunner, existing *models.AgentRunner) (*models.AgentRunner, error) {
	if runner == nil {
		return nil, ErrInvalidInput
	}
	runnerID := strings.TrimSpace(runner.RunnerID)
	userID := strings.TrimSpace(runner.UserID)
	if runnerID == "" || userID == "" {
		return nil, ErrInvalidInput
	}
	provider := strings.TrimSpace(runner.Provider)
	if provider == "" {
		provider = "local"
	}
	if provider != "local" {
		return nil, ErrInvalidInput
	}
	agentType := strings.ToLower(strings.TrimSpace(runner.AgentType))
	if agentType == "" {
		agentType = "codex"
	}
	status := models.AgentRunnerStatus(strings.TrimSpace(string(runner.Status)))
	if status == "" {
		status = models.AgentRunnerStatusOnline
	}
	switch status {
	case models.AgentRunnerStatusOnline, models.AgentRunnerStatusOffline:
	default:
		return nil, ErrInvalidInput
	}

	now := time.Now().UTC()
	copy := *runner
	copy.RunnerID = runnerID
	copy.UserID = userID
	copy.Provider = provider
	copy.AgentType = agentType
	copy.Status = status
	copy.HostName = strings.TrimSpace(runner.HostName)
	copy.WorkspaceRoot = strings.TrimSpace(runner.WorkspaceRoot)
	copy.Version = strings.TrimSpace(runner.Version)
	if len(runner.Capabilities) > 0 {
		copy.Capabilities = append([]byte(nil), runner.Capabilities...)
	} else {
		copy.Capabilities = json.RawMessage(`{}`)
	}
	if existing != nil && !existing.CreatedAt.IsZero() {
		copy.CreatedAt = existing.CreatedAt
	} else if copy.CreatedAt.IsZero() {
		copy.CreatedAt = now
	}
	if copy.UpdatedAt.IsZero() {
		copy.UpdatedAt = now
	}
	if copy.LastHeartbeatAt.IsZero() {
		copy.LastHeartbeatAt = copy.UpdatedAt
	}
	return &copy, nil
}

func cloneAgentRunner(in *models.AgentRunner) *models.AgentRunner {
	if in == nil {
		return nil
	}
	out := *in
	if in.Capabilities != nil {
		out.Capabilities = append([]byte(nil), in.Capabilities...)
	}
	return &out
}

func (s *InMemoryStorage) UpsertAgentRunner(ctx context.Context, runner *models.AgentRunner) error {
	_ = ctx
	if runner == nil {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	normalized, err := normalizeAgentRunner(runner, s.agentRunners[strings.TrimSpace(runner.RunnerID)])
	if err != nil {
		return err
	}
	s.agentRunners[normalized.RunnerID] = normalized
	return nil
}

func (s *InMemoryStorage) GetAgentRunner(ctx context.Context, runnerID string) (*models.AgentRunner, error) {
	_ = ctx
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil, ErrInvalidInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	runner, ok := s.agentRunners[runnerID]
	if !ok || runner == nil {
		return nil, ErrEntryNotFound
	}
	return cloneAgentRunner(runner), nil
}

func (s *InMemoryStorage) ListAgentRunnersByUser(ctx context.Context, username string, limit int) ([]*models.AgentRunner, error) {
	_ = ctx
	username = strings.TrimSpace(username)
	if username == "" {
		return []*models.AgentRunner{}, nil
	}
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*models.AgentRunner, 0)
	for _, runner := range s.agentRunners {
		if runner == nil || runner.UserID != username {
			continue
		}
		out = append(out, cloneAgentRunner(runner))
	}
	sortAgentRunners(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStorage) UpdateAgentRunner(ctx context.Context, runner *models.AgentRunner) error {
	_ = ctx
	if runner == nil || strings.TrimSpace(runner.RunnerID) == "" {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.agentRunners[strings.TrimSpace(runner.RunnerID)]
	if !ok || existing == nil {
		return ErrEntryNotFound
	}
	normalized, err := normalizeAgentRunner(runner, existing)
	if err != nil {
		return err
	}
	s.agentRunners[normalized.RunnerID] = normalized
	return nil
}

func (s *PostgresNativeStorage) UpsertAgentRunner(ctx context.Context, runner *models.AgentRunner) error {
	ctx = ensureCtx(ctx)
	if runner == nil {
		return ErrInvalidInput
	}
	var existing *models.AgentRunner
	if current, err := s.GetAgentRunner(ctx, strings.TrimSpace(runner.RunnerID)); err == nil {
		existing = current
	} else if err != ErrEntryNotFound {
		return err
	}
	normalized, err := normalizeAgentRunner(runner, existing)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO agent_runners (
			runner_id, user_id, provider, agent_type, status, host_name, pid, workspace_root, version,
			capabilities_json, last_heartbeat_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10::jsonb, $11, $12, $13
		)
		ON CONFLICT (runner_id) DO UPDATE
		SET user_id = EXCLUDED.user_id,
		    provider = EXCLUDED.provider,
		    agent_type = EXCLUDED.agent_type,
		    status = EXCLUDED.status,
		    host_name = EXCLUDED.host_name,
		    pid = EXCLUDED.pid,
		    workspace_root = EXCLUDED.workspace_root,
		    version = EXCLUDED.version,
		    capabilities_json = EXCLUDED.capabilities_json,
		    last_heartbeat_at = EXCLUDED.last_heartbeat_at,
		    updated_at = EXCLUDED.updated_at
	`, normalized.RunnerID, normalized.UserID, normalized.Provider, normalized.AgentType, string(normalized.Status),
		normalized.HostName, normalized.PID, normalized.WorkspaceRoot, normalized.Version, string(normalized.Capabilities),
		normalized.LastHeartbeatAt, normalized.CreatedAt, normalized.UpdatedAt)
	return err
}

func (s *PostgresNativeStorage) GetAgentRunner(ctx context.Context, runnerID string) (*models.AgentRunner, error) {
	ctx = ensureCtx(ctx)
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return nil, ErrInvalidInput
	}
	runner, err := s.scanAgentRunner(ctx, `WHERE runner_id = $1`, runnerID)
	if err != nil {
		return nil, err
	}
	return runner, nil
}

func (s *PostgresNativeStorage) ListAgentRunnersByUser(ctx context.Context, username string, limit int) ([]*models.AgentRunner, error) {
	ctx = ensureCtx(ctx)
	username = strings.TrimSpace(username)
	if username == "" {
		return []*models.AgentRunner{}, nil
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT runner_id, user_id, provider, agent_type, status, host_name, pid, workspace_root, version,
		       COALESCE(capabilities_json, '{}'::jsonb), last_heartbeat_at, created_at, updated_at
		FROM agent_runners
		WHERE user_id = $1
		ORDER BY last_heartbeat_at DESC
		LIMIT $2
	`, username, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*models.AgentRunner, 0, limit)
	for rows.Next() {
		runner, err := scanAgentRunnerRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, runner)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) UpdateAgentRunner(ctx context.Context, runner *models.AgentRunner) error {
	ctx = ensureCtx(ctx)
	if runner == nil || strings.TrimSpace(runner.RunnerID) == "" {
		return ErrInvalidInput
	}
	existing, err := s.GetAgentRunner(ctx, strings.TrimSpace(runner.RunnerID))
	if err != nil {
		return err
	}
	normalized, err := normalizeAgentRunner(runner, existing)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_runners
		SET user_id = $1,
		    provider = $2,
		    agent_type = $3,
		    status = $4,
		    host_name = $5,
		    pid = $6,
		    workspace_root = $7,
		    version = $8,
		    capabilities_json = $9::jsonb,
		    last_heartbeat_at = $10,
		    updated_at = $11
		WHERE runner_id = $12
	`, normalized.UserID, normalized.Provider, normalized.AgentType, string(normalized.Status), normalized.HostName,
		normalized.PID, normalized.WorkspaceRoot, normalized.Version, string(normalized.Capabilities),
		normalized.LastHeartbeatAt, normalized.UpdatedAt, normalized.RunnerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

type agentRunnerRow interface {
	Scan(dest ...any) error
}

func (s *PostgresNativeStorage) scanAgentRunner(ctx context.Context, where string, args ...any) (*models.AgentRunner, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT runner_id, user_id, provider, agent_type, status, host_name, pid, workspace_root, version,
		       COALESCE(capabilities_json, '{}'::jsonb), last_heartbeat_at, created_at, updated_at
		FROM agent_runners
		`+where, args...)
	runner, err := scanAgentRunnerRow(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return runner, nil
}

func scanAgentRunnerRow(row agentRunnerRow) (*models.AgentRunner, error) {
	var runner models.AgentRunner
	var status string
	if err := row.Scan(
		&runner.RunnerID, &runner.UserID, &runner.Provider, &runner.AgentType, &status, &runner.HostName,
		&runner.PID, &runner.WorkspaceRoot, &runner.Version, &runner.Capabilities,
		&runner.LastHeartbeatAt, &runner.CreatedAt, &runner.UpdatedAt,
	); err != nil {
		return nil, err
	}
	runner.Status = models.AgentRunnerStatus(status)
	return &runner, nil
}

func sortAgentRunners(runners []*models.AgentRunner) {
	sort.SliceStable(runners, func(i, j int) bool {
		return runners[i].LastHeartbeatAt.After(runners[j].LastHeartbeatAt)
	})
}
