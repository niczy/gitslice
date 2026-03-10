ALTER TABLE auth_sessions
  ADD COLUMN IF NOT EXISTS refresh_token TEXT;

ALTER TABLE auth_sessions
  ADD COLUMN IF NOT EXISTS access_token_expires_at TIMESTAMPTZ;

ALTER TABLE auth_sessions
  ADD COLUMN IF NOT EXISTS refresh_token_expires_at TIMESTAMPTZ;

CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_sessions_refresh_token_unique
  ON auth_sessions (refresh_token)
  WHERE refresh_token IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_auth_sessions_refresh_token_active
  ON auth_sessions (refresh_token)
  WHERE refresh_token IS NOT NULL AND revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS device_authorizations (
  device_code TEXT PRIMARY KEY,
  user_code TEXT NOT NULL UNIQUE,
  username TEXT,
  session_id TEXT,
  device_info TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at TIMESTAMPTZ NOT NULL,
  approved_at TIMESTAMPTZ,
  denied_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_device_authorizations_user_code
  ON device_authorizations (user_code);

CREATE INDEX IF NOT EXISTS idx_device_authorizations_expires_at
  ON device_authorizations (expires_at);
