package storage

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresNativeStorage) CreateCIPlan(ctx context.Context, plan *CIPlan) error {
	ctx = ensureCtx(ctx)
	if plan == nil || plan.Run == nil || strings.TrimSpace(plan.Run.ID) == "" {
		return ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	run := plan.Run
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ci_runs (
			id, home_id, slice_id, changeset_id, changeset_version_id, base_commit_hash,
			candidate_tree_hash, platform_config_hash, plan_hash, attempt, trigger_event,
			triggered_by_user_id, status, created_at, started_at, finished_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, run.ID, run.HomeID, run.SliceID, run.ChangesetID, run.ChangesetVersionID, run.BaseCommitHash,
		run.CandidateTreeHash, run.PlatformConfigHash, run.PlanHash, run.Attempt, run.TriggerEvent,
		run.TriggeredByUserID, run.Status, run.CreatedAt, run.StartedAt, run.FinishedAt); err != nil {
		return err
	}

	for _, manifest := range plan.Manifests {
		if manifest == nil || strings.TrimSpace(manifest.ID) == "" || strings.TrimSpace(manifest.RunID) != run.ID {
			return ErrInvalidInput
		}
		matchedJSON, _ := json.Marshal(manifest.MatchedPaths)
		if _, err := tx.Exec(ctx, `
			INSERT INTO ci_run_manifests (
				id, run_id, manifest_path, manifest_dir, manifest_hash, matched_paths, parse_status, parse_error
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, manifest.ID, manifest.RunID, manifest.ManifestPath, manifest.ManifestDir, manifest.ManifestHash,
			matchedJSON, manifest.ParseStatus, manifest.ParseError); err != nil {
			return err
		}
	}

	for _, job := range plan.Jobs {
		if job == nil || strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.RunID) != run.ID {
			return ErrInvalidInput
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ci_jobs (
				id, run_id, manifest_run_id, manifest_path, job_key, check_name, required,
				runner_pool, image, shell, working_directory, timeout_seconds, status, runner_id, lease_id,
				lease_expires_at, exit_code, infra_failure, started_at, finished_at
			) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12,
				NULLIF($13, ''), $14, $15, $16, $17, $18, $19, $20)
		`, job.ID, job.RunID, job.ManifestRunID, job.ManifestPath, job.JobKey, job.CheckName, job.Required,
			job.RunnerPool, job.Image, job.Shell, job.WorkingDirectory, job.TimeoutSeconds, job.Status, job.RunnerID, job.LeaseID,
			job.LeaseExpiresAt, job.ExitCode, job.InfraFailure, job.StartedAt, job.FinishedAt); err != nil {
			return err
		}
	}

	for _, job := range plan.Jobs {
		for _, dependencyID := range job.DependsOnJobIDs {
			if strings.TrimSpace(dependencyID) == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO ci_job_dependencies (run_id, job_id, depends_on_job_id)
				VALUES ($1, $2, $3)
				ON CONFLICT (job_id, depends_on_job_id) DO NOTHING
			`, job.RunID, job.ID, dependencyID); err != nil {
				return err
			}
		}
	}

	for _, step := range plan.Steps {
		if step == nil || strings.TrimSpace(step.ID) == "" || strings.TrimSpace(step.JobID) == "" {
			return ErrInvalidInput
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ci_steps (id, job_id, step_index, command, status, exit_code, started_at, finished_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, step.ID, step.JobID, step.StepIndex, step.Command, step.Status, step.ExitCode, step.StartedAt, step.FinishedAt); err != nil {
			return err
		}
	}

	for _, check := range plan.Checks {
		if check == nil {
			return ErrInvalidInput
		}
		if check.UpdatedAt.IsZero() {
			check.UpdatedAt = time.Now()
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ci_checks (
				changeset_id, changeset_version_id, plan_hash, manifest_path, job_key,
				check_name, required, status, run_id, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (changeset_id, changeset_version_id, plan_hash, manifest_path, job_key)
			DO UPDATE SET check_name = EXCLUDED.check_name,
				required = EXCLUDED.required,
				status = EXCLUDED.status,
				run_id = EXCLUDED.run_id,
				updated_at = EXCLUDED.updated_at
		`, check.ChangesetID, check.ChangesetVersionID, check.PlanHash, check.ManifestPath, check.JobKey,
			check.CheckName, check.Required, check.Status, check.RunID, check.UpdatedAt); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *PostgresNativeStorage) GetCIRun(ctx context.Context, runID string) (*CIRun, error) {
	ctx = ensureCtx(ctx)
	run, err := scanCIRun(s.pool.QueryRow(ctx, `
		SELECT id, home_id, slice_id, changeset_id, changeset_version_id, base_commit_hash,
		       candidate_tree_hash, platform_config_hash, plan_hash, attempt, trigger_event,
		       triggered_by_user_id, status, created_at, started_at, finished_at
		FROM ci_runs WHERE id = $1
	`, strings.TrimSpace(runID)))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return run, nil
}

func (s *PostgresNativeStorage) ListCIRuns(ctx context.Context, filter CIRunListFilter) ([]*CIRun, error) {
	ctx = ensureCtx(ctx)
	limit := normalizeCIListLimit(filter.Limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id, home_id, slice_id, changeset_id, changeset_version_id, base_commit_hash,
		       candidate_tree_hash, platform_config_hash, plan_hash, attempt, trigger_event,
		       triggered_by_user_id, status, created_at, started_at, finished_at
		FROM ci_runs
		WHERE ($1 = '' OR home_id = $1)
		  AND ($2 = '' OR changeset_id = $2)
		  AND ($3 = '' OR changeset_version_id = $3)
		  AND ($4 = '' OR plan_hash = $4)
		  AND ($5 = '' OR status = $5)
		ORDER BY created_at DESC
		LIMIT $6
	`, filter.HomeID, filter.ChangesetID, filter.ChangesetVersionID, filter.PlanHash, filter.Status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*CIRun, 0)
	for rows.Next() {
		run, err := scanCIRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) UpdateCIRunStatus(ctx context.Context, runID string, status string, finishedAt *time.Time) error {
	ctx = ensureCtx(ctx)
	tag, err := s.pool.Exec(ctx, `
		UPDATE ci_runs SET status = $1, finished_at = $2 WHERE id = $3
	`, strings.TrimSpace(status), finishedAt, strings.TrimSpace(runID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) ListCIRunManifests(ctx context.Context, runID string) ([]*CIRunManifest, error) {
	ctx = ensureCtx(ctx)
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, manifest_path, manifest_dir, manifest_hash, matched_paths, parse_status, parse_error
		FROM ci_run_manifests
		WHERE run_id = $1
		ORDER BY manifest_path
	`, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*CIRunManifest, 0)
	for rows.Next() {
		var manifest CIRunManifest
		var matchedJSON []byte
		if err := rows.Scan(&manifest.ID, &manifest.RunID, &manifest.ManifestPath, &manifest.ManifestDir,
			&manifest.ManifestHash, &matchedJSON, &manifest.ParseStatus, &manifest.ParseError); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(matchedJSON, &manifest.MatchedPaths)
		out = append(out, &manifest)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) ListCIJobs(ctx context.Context, filter CIJobListFilter) ([]*CIJob, error) {
	ctx = ensureCtx(ctx)
	limit := normalizeCIListLimit(filter.Limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id, run_id, COALESCE(manifest_run_id, ''), manifest_path, job_key, check_name,
		       required, runner_pool, image, shell, working_directory, timeout_seconds, status, COALESCE(runner_id, ''),
		       lease_id, lease_expires_at, exit_code, infra_failure, started_at, finished_at
		FROM ci_jobs
		WHERE ($1 = '' OR run_id = $1)
		  AND ($2 = '' OR COALESCE(runner_id, '') = $2)
		  AND ($3 = '' OR runner_pool = $3)
		  AND ($4 = '' OR status = $4)
		ORDER BY run_id, manifest_path, job_key
		LIMIT $5
	`, filter.RunID, filter.RunnerID, filter.Pool, filter.Status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*CIJob, 0)
	for rows.Next() {
		job, err := scanCIJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, job := range out {
		deps, err := s.listCIJobDependencies(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		job.DependsOnJobIDs = deps
	}
	return out, nil
}

func (s *PostgresNativeStorage) GetCIJob(ctx context.Context, jobID string) (*CIJob, error) {
	ctx = ensureCtx(ctx)
	job, err := scanCIJob(s.pool.QueryRow(ctx, `
		SELECT id, run_id, COALESCE(manifest_run_id, ''), manifest_path, job_key, check_name,
		       required, runner_pool, image, shell, working_directory, timeout_seconds, status,
		       COALESCE(runner_id, ''), lease_id, lease_expires_at, exit_code, infra_failure, started_at, finished_at
		FROM ci_jobs
		WHERE id = $1
	`, strings.TrimSpace(jobID)))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	deps, err := s.listCIJobDependencies(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	job.DependsOnJobIDs = deps
	return job, nil
}

func (s *PostgresNativeStorage) listCIJobDependencies(ctx context.Context, jobID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT depends_on_job_id
		FROM ci_job_dependencies
		WHERE job_id = $1
		ORDER BY depends_on_job_id
	`, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) ListCISteps(ctx context.Context, jobID string) ([]*CIStep, error) {
	ctx = ensureCtx(ctx)
	rows, err := s.pool.Query(ctx, `
		SELECT id, job_id, step_index, command, status, exit_code, started_at, finished_at
		FROM ci_steps
		WHERE job_id = $1
		ORDER BY step_index
	`, strings.TrimSpace(jobID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*CIStep, 0)
	for rows.Next() {
		var step CIStep
		if err := rows.Scan(&step.ID, &step.JobID, &step.StepIndex, &step.Command, &step.Status, &step.ExitCode, &step.StartedAt, &step.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, &step)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) ClaimCIJob(ctx context.Context, jobID string, runnerID string, leaseID string, leaseExpiresAt time.Time, startedAt time.Time) (*CIJob, error) {
	ctx = ensureCtx(ctx)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	job, err := scanCIJob(tx.QueryRow(ctx, `
		UPDATE ci_jobs
		SET status = 'running',
		    runner_id = $2,
		    lease_id = $3,
		    lease_expires_at = $4,
		    started_at = $5
		WHERE id = $1 AND status = 'queued'
		RETURNING id, run_id, COALESCE(manifest_run_id, ''), manifest_path, job_key, check_name,
		          required, runner_pool, image, shell, working_directory, timeout_seconds, status,
		          COALESCE(runner_id, ''), lease_id, lease_expires_at, exit_code, infra_failure, started_at, finished_at
	`, strings.TrimSpace(jobID), strings.TrimSpace(runnerID), strings.TrimSpace(leaseID), leaseExpiresAt, startedAt))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ci_runners SET status = 'busy', last_seen_at = $2 WHERE id = $1
	`, strings.TrimSpace(runnerID), startedAt); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE ci_runs
		SET status = 'running',
		    started_at = COALESCE(started_at, $2)
		WHERE id = $1 AND status IN ('queued', 'planning')
	`, job.RunID, startedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	deps, err := s.listCIJobDependencies(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	job.DependsOnJobIDs = deps
	return job, nil
}

func (s *PostgresNativeStorage) UpdateCIStepStatus(ctx context.Context, jobID string, stepIndex int, status string, exitCode int, startedAt *time.Time, finishedAt *time.Time) error {
	ctx = ensureCtx(ctx)
	tag, err := s.pool.Exec(ctx, `
		UPDATE ci_steps
		SET status = $3, exit_code = $4, started_at = $5, finished_at = $6
		WHERE job_id = $1 AND step_index = $2
	`, strings.TrimSpace(jobID), stepIndex, strings.TrimSpace(status), exitCode, startedAt, finishedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) AppendCILogChunk(ctx context.Context, chunk *CILogChunk) error {
	ctx = ensureCtx(ctx)
	if chunk == nil || strings.TrimSpace(chunk.JobID) == "" {
		return ErrInvalidInput
	}
	if strings.TrimSpace(chunk.ID) == "" {
		chunk.ID = strings.TrimSpace(chunk.JobID) + ":" + strconv.FormatInt(chunk.ChunkIndex, 10)
	}
	if chunk.CreatedAt.IsZero() {
		chunk.CreatedAt = time.Now()
	}
	if chunk.ByteCount == 0 && len(chunk.Payload) > 0 {
		chunk.ByteCount = int64(len(chunk.Payload))
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ci_log_chunks (id, job_id, chunk_index, stream, object_key, payload, byte_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (job_id, chunk_index)
		DO UPDATE SET stream = EXCLUDED.stream,
			object_key = EXCLUDED.object_key,
			payload = EXCLUDED.payload,
			byte_count = EXCLUDED.byte_count,
			created_at = EXCLUDED.created_at
	`, chunk.ID, strings.TrimSpace(chunk.JobID), chunk.ChunkIndex, chunk.Stream, chunk.ObjectKey, chunk.Payload, chunk.ByteCount, chunk.CreatedAt)
	return err
}

func (s *PostgresNativeStorage) CompleteCIJob(ctx context.Context, jobID string, leaseID string, status string, exitCode int, infraFailure bool, finishedAt time.Time) (*CIJob, error) {
	ctx = ensureCtx(ctx)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	job, err := scanCIJob(tx.QueryRow(ctx, `
		UPDATE ci_jobs
		SET status = $3,
		    exit_code = $4,
		    infra_failure = $5,
		    finished_at = $6,
		    lease_expires_at = NULL
		WHERE id = $1 AND ($2 = '' OR lease_id = $2)
		RETURNING id, run_id, COALESCE(manifest_run_id, ''), manifest_path, job_key, check_name,
		          required, runner_pool, image, shell, working_directory, timeout_seconds, status,
		          COALESCE(runner_id, ''), lease_id, lease_expires_at, exit_code, infra_failure, started_at, finished_at
	`, strings.TrimSpace(jobID), strings.TrimSpace(leaseID), strings.TrimSpace(status), exitCode, infraFailure, finishedAt))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	if strings.TrimSpace(job.RunnerID) != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE ci_runners
			SET status = 'idle', last_seen_at = $2
			WHERE id = $1 AND status NOT IN ('disabled', 'revoked')
		`, job.RunnerID, finishedAt); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	deps, err := s.listCIJobDependencies(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	job.DependsOnJobIDs = deps
	return job, nil
}

func (s *PostgresNativeStorage) UpsertCICheck(ctx context.Context, check *CICheck) error {
	ctx = ensureCtx(ctx)
	if check == nil {
		return ErrInvalidInput
	}
	if check.UpdatedAt.IsZero() {
		check.UpdatedAt = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ci_checks (
			changeset_id, changeset_version_id, plan_hash, manifest_path, job_key,
			check_name, required, status, run_id, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (changeset_id, changeset_version_id, plan_hash, manifest_path, job_key)
		DO UPDATE SET check_name = EXCLUDED.check_name,
			required = EXCLUDED.required,
			status = EXCLUDED.status,
			run_id = EXCLUDED.run_id,
			updated_at = EXCLUDED.updated_at
	`, check.ChangesetID, check.ChangesetVersionID, check.PlanHash, check.ManifestPath, check.JobKey,
		check.CheckName, check.Required, check.Status, check.RunID, check.UpdatedAt)
	return err
}

func (s *PostgresNativeStorage) ListCIChecks(ctx context.Context, changesetID string, changesetVersionID string, planHash string) ([]*CICheck, error) {
	ctx = ensureCtx(ctx)
	rows, err := s.pool.Query(ctx, `
		SELECT changeset_id, changeset_version_id, plan_hash, manifest_path, job_key,
		       check_name, required, status, run_id, updated_at
		FROM ci_checks
		WHERE ($1 = '' OR changeset_id = $1)
		  AND ($2 = '' OR changeset_version_id = $2)
		  AND ($3 = '' OR plan_hash = $3)
		ORDER BY updated_at DESC, manifest_path, job_key
	`, strings.TrimSpace(changesetID), strings.TrimSpace(changesetVersionID), strings.TrimSpace(planHash))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*CICheck, 0)
	for rows.Next() {
		var check CICheck
		if err := rows.Scan(&check.ChangesetID, &check.ChangesetVersionID, &check.PlanHash, &check.ManifestPath,
			&check.JobKey, &check.CheckName, &check.Required, &check.Status, &check.RunID, &check.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &check)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) ListCILogChunks(ctx context.Context, filter CILogChunkListFilter) ([]*CILogChunk, error) {
	ctx = ensureCtx(ctx)
	limit := normalizeCIListLimit(filter.Limit)
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.job_id, j.run_id, c.chunk_index, c.stream, c.object_key,
		       COALESCE(c.payload, ''::bytea), c.byte_count, c.created_at
		FROM ci_log_chunks c
		JOIN ci_jobs j ON j.id = c.job_id
		WHERE ($1 = '' OR j.run_id = $1)
		  AND ($2 = '' OR c.job_id = $2)
		  AND c.chunk_index >= $3
		ORDER BY c.created_at, c.job_id, c.chunk_index
		LIMIT $4
	`, filter.RunID, filter.JobID, filter.SinceChunk, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*CILogChunk, 0)
	for rows.Next() {
		var chunk CILogChunk
		if err := rows.Scan(&chunk.ID, &chunk.JobID, &chunk.RunID, &chunk.ChunkIndex, &chunk.Stream,
			&chunk.ObjectKey, &chunk.Payload, &chunk.ByteCount, &chunk.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &chunk)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) CreateCIRunnerRegistrationToken(ctx context.Context, token *CIRunnerRegistrationToken) error {
	ctx = ensureCtx(ctx)
	if token == nil || strings.TrimSpace(token.TokenHash) == "" {
		return ErrInvalidInput
	}
	if token.CreatedAt.IsZero() {
		token.CreatedAt = time.Now()
	}
	labelsJSON, _ := json.Marshal(token.Labels)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ci_runner_registration_tokens (
			token_hash, home_id, name, pool, labels, expires_at, created_by_user_id, created_at, used_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, token.TokenHash, token.HomeID, token.Name, token.Pool, labelsJSON, token.ExpiresAt,
		token.CreatedByUserID, token.CreatedAt, token.UsedAt)
	return err
}

func (s *PostgresNativeStorage) ConsumeCIRunnerRegistrationToken(ctx context.Context, tokenHash string, usedAt time.Time) (*CIRunnerRegistrationToken, error) {
	ctx = ensureCtx(ctx)
	token, err := scanCIRunnerRegistrationToken(s.pool.QueryRow(ctx, `
		UPDATE ci_runner_registration_tokens
		SET used_at = $2
		WHERE token_hash = $1
		  AND used_at IS NULL
		  AND expires_at > $2
		RETURNING token_hash, home_id, name, pool, labels, expires_at, created_by_user_id, created_at, used_at
	`, strings.TrimSpace(tokenHash), usedAt))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return token, nil
}

func (s *PostgresNativeStorage) CreateCIRunner(ctx context.Context, runner *CIRunner) error {
	ctx = ensureCtx(ctx)
	if runner == nil || strings.TrimSpace(runner.ID) == "" {
		return ErrInvalidInput
	}
	if runner.CreatedAt.IsZero() {
		runner.CreatedAt = time.Now()
	}
	labelsJSON, _ := json.Marshal(runner.Labels)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ci_runners (id, home_id, name, pool, labels, executor, status, token_hash, version, last_seen_at, created_at, disabled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, runner.ID, runner.HomeID, runner.Name, runner.Pool, labelsJSON, runner.Executor, runner.Status, runner.TokenHash,
		runner.Version, runner.LastSeenAt, runner.CreatedAt, runner.DisabledAt)
	return err
}

func (s *PostgresNativeStorage) GetCIRunner(ctx context.Context, runnerID string) (*CIRunner, error) {
	ctx = ensureCtx(ctx)
	runner, err := scanCIRunner(s.pool.QueryRow(ctx, `
		SELECT id, home_id, name, pool, labels, executor, status, token_hash, version, last_seen_at, created_at, disabled_at
		FROM ci_runners WHERE id = $1
	`, strings.TrimSpace(runnerID)))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return runner, nil
}

func (s *PostgresNativeStorage) GetCIRunnerByTokenHash(ctx context.Context, tokenHash string) (*CIRunner, error) {
	ctx = ensureCtx(ctx)
	runner, err := scanCIRunner(s.pool.QueryRow(ctx, `
		SELECT id, home_id, name, pool, labels, executor, status, token_hash, version, last_seen_at, created_at, disabled_at
		FROM ci_runners WHERE token_hash = $1
	`, strings.TrimSpace(tokenHash)))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrEntryNotFound
		}
		return nil, err
	}
	return runner, nil
}

func (s *PostgresNativeStorage) ListCIRunners(ctx context.Context, filter CIRunnerListFilter) ([]*CIRunner, error) {
	ctx = ensureCtx(ctx)
	limit := normalizeCIListLimit(filter.Limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id, home_id, name, pool, labels, executor, status, token_hash, version, last_seen_at, created_at, disabled_at
		FROM ci_runners
		WHERE ($1 = '' OR home_id = $1)
		  AND ($2 = '' OR pool = $2)
		  AND ($3 = '' OR status = $3)
		ORDER BY created_at DESC
		LIMIT $4
	`, filter.HomeID, filter.Pool, filter.Status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*CIRunner, 0)
	for rows.Next() {
		runner, err := scanCIRunner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, runner)
	}
	return out, rows.Err()
}

func (s *PostgresNativeStorage) UpdateCIRunnerStatus(ctx context.Context, runnerID string, status string, lastSeenAt *time.Time) error {
	ctx = ensureCtx(ctx)
	tag, err := s.pool.Exec(ctx, `
		UPDATE ci_runners SET status = $1, last_seen_at = $2 WHERE id = $3
	`, strings.TrimSpace(status), lastSeenAt, strings.TrimSpace(runnerID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func (s *PostgresNativeStorage) RevokeCIRunner(ctx context.Context, runnerID string, revokedAt time.Time) error {
	ctx = ensureCtx(ctx)
	tag, err := s.pool.Exec(ctx, `
		UPDATE ci_runners SET status = 'revoked', token_hash = '', disabled_at = $1 WHERE id = $2
	`, revokedAt, strings.TrimSpace(runnerID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEntryNotFound
	}
	return nil
}

func scanCIRun(row pgx.Row) (*CIRun, error) {
	var run CIRun
	if err := row.Scan(
		&run.ID,
		&run.HomeID,
		&run.SliceID,
		&run.ChangesetID,
		&run.ChangesetVersionID,
		&run.BaseCommitHash,
		&run.CandidateTreeHash,
		&run.PlatformConfigHash,
		&run.PlanHash,
		&run.Attempt,
		&run.TriggerEvent,
		&run.TriggeredByUserID,
		&run.Status,
		&run.CreatedAt,
		&run.StartedAt,
		&run.FinishedAt,
	); err != nil {
		return nil, err
	}
	return &run, nil
}

func scanCIJob(row pgx.Row) (*CIJob, error) {
	var job CIJob
	if err := row.Scan(
		&job.ID,
		&job.RunID,
		&job.ManifestRunID,
		&job.ManifestPath,
		&job.JobKey,
		&job.CheckName,
		&job.Required,
		&job.RunnerPool,
		&job.Image,
		&job.Shell,
		&job.WorkingDirectory,
		&job.TimeoutSeconds,
		&job.Status,
		&job.RunnerID,
		&job.LeaseID,
		&job.LeaseExpiresAt,
		&job.ExitCode,
		&job.InfraFailure,
		&job.StartedAt,
		&job.FinishedAt,
	); err != nil {
		return nil, err
	}
	return &job, nil
}

func scanCIRunnerRegistrationToken(row pgx.Row) (*CIRunnerRegistrationToken, error) {
	var token CIRunnerRegistrationToken
	var labelsJSON []byte
	if err := row.Scan(
		&token.TokenHash,
		&token.HomeID,
		&token.Name,
		&token.Pool,
		&labelsJSON,
		&token.ExpiresAt,
		&token.CreatedByUserID,
		&token.CreatedAt,
		&token.UsedAt,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(labelsJSON, &token.Labels)
	return &token, nil
}

func scanCIRunner(row pgx.Row) (*CIRunner, error) {
	var runner CIRunner
	var labelsJSON []byte
	if err := row.Scan(
		&runner.ID,
		&runner.HomeID,
		&runner.Name,
		&runner.Pool,
		&labelsJSON,
		&runner.Executor,
		&runner.Status,
		&runner.TokenHash,
		&runner.Version,
		&runner.LastSeenAt,
		&runner.CreatedAt,
		&runner.DisabledAt,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(labelsJSON, &runner.Labels)
	return &runner, nil
}
