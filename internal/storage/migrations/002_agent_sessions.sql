-- Agent sessions control-plane persistence.

CREATE TABLE IF NOT EXISTS agent_sessions (
    session_id TEXT PRIMARY KEY,
    slice_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    state TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'e2b',
    e2b_template_id TEXT NOT NULL,
    e2b_sandbox_id TEXT,
    e2b_region TEXT,
    idle_timeout_sec INT NOT NULL,
    ttl_sec INT NOT NULL,
    runtime_endpoint TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    last_activity_at TIMESTAMPTZ,
    stopped_at TIMESTAMPTZ,
    failure_code TEXT,
    failure_message TEXT
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_user_created
    ON agent_sessions (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_slice_created
    ON agent_sessions (slice_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_state_updated
    ON agent_sessions (state, updated_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_sessions_e2b_sandbox
    ON agent_sessions (e2b_sandbox_id)
    WHERE e2b_sandbox_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_sessions_active_per_slice
    ON agent_sessions (slice_id)
    WHERE state IN ('creating', 'starting', 'running', 'idle', 'stopping');

CREATE TABLE IF NOT EXISTS agent_session_events (
    session_id TEXT NOT NULL,
    seq BIGINT NOT NULL,
    ts TIMESTAMPTZ NOT NULL,
    stream TEXT NOT NULL,
    type TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    PRIMARY KEY (session_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_agent_session_events_ts
    ON agent_session_events (session_id, ts DESC);

CREATE TABLE IF NOT EXISTS agent_session_audit (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    actor_user_id TEXT,
    action TEXT NOT NULL,
    metadata_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_session_audit_session_created
    ON agent_session_audit (session_id, created_at DESC);
