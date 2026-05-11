ALTER TABLE agent_session_events
    ADD COLUMN IF NOT EXISTS message_role TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_session_events
    ADD COLUMN IF NOT EXISTS changeset_id TEXT NOT NULL DEFAULT '';

ALTER TABLE agent_session_events
    ADD COLUMN IF NOT EXISTS commit_hash TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_agent_session_events_changeset_id
    ON agent_session_events (changeset_id)
    WHERE changeset_id <> '';

CREATE INDEX IF NOT EXISTS idx_agent_session_events_commit_hash
    ON agent_session_events (commit_hash)
    WHERE commit_hash <> '';

CREATE INDEX IF NOT EXISTS idx_agent_session_events_message_role
    ON agent_session_events (message_role)
    WHERE message_role <> '';
