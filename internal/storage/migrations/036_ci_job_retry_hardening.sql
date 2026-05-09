-- CI job retry metadata for runner lease recovery.

ALTER TABLE ci_jobs
    ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS max_attempts INT NOT NULL DEFAULT 2;

CREATE INDEX IF NOT EXISTS idx_ci_jobs_running_lease_expiry
    ON ci_jobs(lease_expires_at)
    WHERE status = 'running' AND lease_expires_at IS NOT NULL;
