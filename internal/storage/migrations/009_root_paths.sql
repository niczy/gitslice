ALTER TABLE users
  ADD COLUMN IF NOT EXISTS root_path TEXT NOT NULL DEFAULT '';

UPDATE users
SET root_path = '/' || username
WHERE root_path = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_root_path_unique
  ON users(root_path);

ALTER TABLE organizations
  ADD COLUMN IF NOT EXISTS root_path TEXT NOT NULL DEFAULT '';

UPDATE organizations
SET root_path = '/' || slug
WHERE root_path = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_organizations_root_path_unique
  ON organizations(root_path);
