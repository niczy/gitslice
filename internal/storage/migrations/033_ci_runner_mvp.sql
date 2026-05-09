-- Runner MVP additions: one-time registration tokens, executor metadata, and
-- shell runtime fields for queued jobs.

ALTER TABLE ci_runners
    ADD COLUMN IF NOT EXISTS executor TEXT NOT NULL DEFAULT '';

ALTER TABLE ci_jobs
    ADD COLUMN IF NOT EXISTS shell TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS timeout_seconds INT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS ci_runner_registration_tokens (
    token_hash TEXT PRIMARY KEY,
    home_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    pool TEXT NOT NULL DEFAULT '',
    labels JSONB NOT NULL DEFAULT '[]',
    expires_at TIMESTAMPTZ NOT NULL,
    created_by_user_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ci_runner_registration_tokens_home
    ON ci_runner_registration_tokens(home_id, expires_at DESC);

