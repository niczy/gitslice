ALTER TABLE auth_sessions
  ADD COLUMN IF NOT EXISTS agent_key_id TEXT REFERENCES agent_keys(key_id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_auth_sessions_agent_key_id
  ON auth_sessions (agent_key_id)
  WHERE agent_key_id IS NOT NULL;
