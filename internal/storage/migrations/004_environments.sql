CREATE TABLE IF NOT EXISTS environments (
    name TEXT PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT 'e2b',
    provider_id TEXT NOT NULL DEFAULT '',
    region TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_environments_provider
    ON environments (provider);
