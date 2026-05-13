ALTER TABLE agent_sessions
  ADD COLUMN IF NOT EXISTS runner_id text DEFAULT ''::text NOT NULL;

ALTER TABLE agent_sessions
  ALTER COLUMN provider SET DEFAULT 'local'::text;

ALTER TABLE environments
  ALTER COLUMN provider SET DEFAULT 'local'::text;

CREATE TABLE IF NOT EXISTS agent_runners (
    runner_id text NOT NULL,
    user_id text NOT NULL,
    provider text DEFAULT 'local'::text NOT NULL,
    agent_type text DEFAULT 'codex'::text NOT NULL,
    status text DEFAULT 'online'::text NOT NULL,
    host_name text DEFAULT ''::text NOT NULL,
    pid integer DEFAULT 0 NOT NULL,
    workspace_root text DEFAULT ''::text NOT NULL,
    version text DEFAULT ''::text NOT NULL,
    capabilities_json jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_runners_pkey PRIMARY KEY (runner_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_sessions_runner_id
  ON agent_sessions (runner_id)
  WHERE runner_id <> ''::text;

CREATE INDEX IF NOT EXISTS idx_agent_runners_user_heartbeat
  ON agent_runners (user_id, last_heartbeat_at DESC);
