CREATE TABLE IF NOT EXISTS agent_keys (
  key_id TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  algorithm TEXT NOT NULL,
  public_key BYTEA NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_used_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_keys_username
  ON agent_keys (username);

CREATE INDEX IF NOT EXISTS idx_agent_keys_username_active
  ON agent_keys (username)
  WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS agent_key_challenges (
  challenge_id TEXT PRIMARY KEY,
  agent_key_id TEXT NOT NULL REFERENCES agent_keys (key_id) ON DELETE CASCADE,
  username TEXT NOT NULL,
  challenge BYTEA NOT NULL,
  device_info TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at TIMESTAMPTZ NOT NULL,
  used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_agent_key_challenges_key
  ON agent_key_challenges (agent_key_id);

CREATE INDEX IF NOT EXISTS idx_agent_key_challenges_expires_at
  ON agent_key_challenges (expires_at);
