-- Path-scoped CI primitives. This migration only creates durable state; the
-- planner, scheduler, runner, and merge gates are introduced incrementally.

CREATE TABLE IF NOT EXISTS ci_runners (
    id TEXT PRIMARY KEY,
    home_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    pool TEXT NOT NULL DEFAULT '',
    labels JSONB NOT NULL DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'offline',
    token_hash TEXT NOT NULL DEFAULT '',
    version TEXT NOT NULL DEFAULT '',
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    disabled_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ci_runners_home_pool_status ON ci_runners(home_id, pool, status);
CREATE INDEX IF NOT EXISTS idx_ci_runners_last_seen ON ci_runners(last_seen_at DESC);

CREATE TABLE IF NOT EXISTS ci_runs (
    id TEXT PRIMARY KEY,
    home_id TEXT NOT NULL DEFAULT '',
    slice_id TEXT NOT NULL DEFAULT '',
    changeset_id TEXT NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
    changeset_version_id TEXT NOT NULL DEFAULT '',
    base_commit_hash TEXT NOT NULL DEFAULT '',
    candidate_tree_hash TEXT NOT NULL DEFAULT '',
    platform_config_hash TEXT NOT NULL DEFAULT '',
    plan_hash TEXT NOT NULL DEFAULT '',
    attempt INT NOT NULL DEFAULT 1,
    trigger_event TEXT NOT NULL DEFAULT '',
    triggered_by_user_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (changeset_id, changeset_version_id, plan_hash, trigger_event, attempt)
);

CREATE INDEX IF NOT EXISTS idx_ci_runs_changeset_version ON ci_runs(changeset_id, changeset_version_id);
CREATE INDEX IF NOT EXISTS idx_ci_runs_home_status ON ci_runs(home_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ci_runs_status_created ON ci_runs(status, created_at);

CREATE TABLE IF NOT EXISTS ci_run_manifests (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES ci_runs(id) ON DELETE CASCADE,
    manifest_path TEXT NOT NULL DEFAULT '',
    manifest_dir TEXT NOT NULL DEFAULT '',
    manifest_hash TEXT NOT NULL DEFAULT '',
    matched_paths JSONB NOT NULL DEFAULT '[]',
    parse_status TEXT NOT NULL DEFAULT 'ok',
    parse_error TEXT NOT NULL DEFAULT '',
    UNIQUE (run_id, manifest_path)
);

CREATE INDEX IF NOT EXISTS idx_ci_run_manifests_run ON ci_run_manifests(run_id);

CREATE TABLE IF NOT EXISTS ci_jobs (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES ci_runs(id) ON DELETE CASCADE,
    manifest_run_id TEXT REFERENCES ci_run_manifests(id) ON DELETE CASCADE,
    manifest_path TEXT NOT NULL DEFAULT '',
    job_key TEXT NOT NULL DEFAULT '',
    check_name TEXT NOT NULL DEFAULT '',
    required BOOLEAN NOT NULL DEFAULT FALSE,
    runner_pool TEXT NOT NULL DEFAULT '',
    image TEXT NOT NULL DEFAULT '',
    working_directory TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    runner_id TEXT REFERENCES ci_runners(id) ON DELETE SET NULL,
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    exit_code INT NOT NULL DEFAULT 0,
    infra_failure BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (run_id, manifest_path, job_key)
);

CREATE INDEX IF NOT EXISTS idx_ci_jobs_run_status ON ci_jobs(run_id, status);
CREATE INDEX IF NOT EXISTS idx_ci_jobs_pool_status ON ci_jobs(runner_pool, status, id);
CREATE INDEX IF NOT EXISTS idx_ci_jobs_runner ON ci_jobs(runner_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_ci_jobs_lease_expiry ON ci_jobs(status, lease_expires_at) WHERE lease_expires_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS ci_job_dependencies (
    run_id TEXT NOT NULL REFERENCES ci_runs(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL REFERENCES ci_jobs(id) ON DELETE CASCADE,
    depends_on_job_id TEXT NOT NULL REFERENCES ci_jobs(id) ON DELETE CASCADE,
    PRIMARY KEY (job_id, depends_on_job_id)
);

CREATE INDEX IF NOT EXISTS idx_ci_job_dependencies_run ON ci_job_dependencies(run_id);

CREATE TABLE IF NOT EXISTS ci_steps (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES ci_jobs(id) ON DELETE CASCADE,
    step_index INT NOT NULL,
    command TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued',
    exit_code INT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (job_id, step_index)
);

CREATE INDEX IF NOT EXISTS idx_ci_steps_job ON ci_steps(job_id, step_index);

CREATE TABLE IF NOT EXISTS ci_log_chunks (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES ci_jobs(id) ON DELETE CASCADE,
    chunk_index BIGINT NOT NULL,
    stream TEXT NOT NULL DEFAULT 'stdout',
    object_key TEXT NOT NULL DEFAULT '',
    payload BYTEA,
    byte_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (job_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_ci_log_chunks_job_chunk ON ci_log_chunks(job_id, chunk_index);

CREATE TABLE IF NOT EXISTS ci_checks (
    changeset_id TEXT NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
    changeset_version_id TEXT NOT NULL DEFAULT '',
    plan_hash TEXT NOT NULL DEFAULT '',
    manifest_path TEXT NOT NULL DEFAULT '',
    job_key TEXT NOT NULL DEFAULT '',
    check_name TEXT NOT NULL DEFAULT '',
    required BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'queued',
    run_id TEXT NOT NULL REFERENCES ci_runs(id) ON DELETE CASCADE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (changeset_id, changeset_version_id, plan_hash, manifest_path, job_key)
);

CREATE INDEX IF NOT EXISTS idx_ci_checks_changeset_version ON ci_checks(changeset_id, changeset_version_id, plan_hash);
CREATE INDEX IF NOT EXISTS idx_ci_checks_run ON ci_checks(run_id);

CREATE TABLE IF NOT EXISTS ci_manifest_index (
    home_id TEXT NOT NULL DEFAULT '',
    home_commit_hash TEXT NOT NULL DEFAULT '',
    manifest_path TEXT NOT NULL DEFAULT '',
    manifest_dir TEXT NOT NULL DEFAULT '',
    manifest_hash TEXT NOT NULL DEFAULT '',
    watch_globs JSONB NOT NULL DEFAULT '[]',
    ignore_globs JSONB NOT NULL DEFAULT '[]',
    applies_to_globs JSONB NOT NULL DEFAULT '[]',
    parse_status TEXT NOT NULL DEFAULT 'ok',
    parse_error TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (home_id, home_commit_hash, manifest_path)
);

CREATE INDEX IF NOT EXISTS idx_ci_manifest_index_home_commit ON ci_manifest_index(home_id, home_commit_hash);
