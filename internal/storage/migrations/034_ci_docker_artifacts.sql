-- Docker executor metadata and runner-uploaded artifacts.

ALTER TABLE ci_jobs
    ADD COLUMN IF NOT EXISTS env JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS cache_paths JSONB NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS artifacts JSONB NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS ci_artifacts (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES ci_jobs(id) ON DELETE CASCADE,
    run_id TEXT NOT NULL REFERENCES ci_runs(id) ON DELETE CASCADE,
    path TEXT NOT NULL DEFAULT '',
    object_key TEXT NOT NULL DEFAULT '',
    payload BYTEA,
    byte_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ci_artifacts_job_created ON ci_artifacts(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ci_artifacts_run_created ON ci_artifacts(run_id, created_at);
