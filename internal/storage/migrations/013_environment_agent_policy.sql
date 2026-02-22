ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS default_agent_type TEXT NOT NULL DEFAULT 'codex';

ALTER TABLE environments
    ADD COLUMN IF NOT EXISTS allowed_agent_types_json JSONB NOT NULL DEFAULT '["codex","claude"]'::jsonb;
