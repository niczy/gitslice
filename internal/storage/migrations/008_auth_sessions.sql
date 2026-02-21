ALTER TABLE users
  ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS primary_email TEXT NOT NULL DEFAULT '';

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_primary_email_unique
  ON users (lower(primary_email))
  WHERE primary_email <> '';

CREATE TABLE IF NOT EXISTS auth_sessions (
  session_id TEXT PRIMARY KEY,
  username TEXT NOT NULL REFERENCES users(username),
  token TEXT NOT NULL UNIQUE,
  device_info TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  revoked_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_username
  ON auth_sessions(username);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_token_active
  ON auth_sessions(token)
  WHERE revoked_at IS NULL;
