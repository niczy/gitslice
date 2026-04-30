ALTER TABLE users
  ADD COLUMN IF NOT EXISTS clerk_user_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_clerk_user_id_unique
  ON users (clerk_user_id)
  WHERE clerk_user_id <> '';
