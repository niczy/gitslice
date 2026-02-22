ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS agent_type TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS runtime_provider TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS runtime_session_id TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS runtime_status TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_sessions
    ADD COLUMN IF NOT EXISTS runtime_error_code TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_agent_sessions_runtime_session_id
    ON agent_sessions (runtime_session_id)
    WHERE runtime_session_id <> '';
