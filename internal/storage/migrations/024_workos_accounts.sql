CREATE TABLE IF NOT EXISTS accounts (
  account_id TEXT PRIMARY KEY,
  owner_mode TEXT NOT NULL DEFAULT 'agent_only',
  claim_state TEXT NOT NULL DEFAULT 'unclaimed',
  claim_token_hash TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_claim_token_hash_unique
  ON accounts (claim_token_hash)
  WHERE claim_token_hash <> '';

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS account_id TEXT REFERENCES accounts(account_id) ON DELETE SET NULL;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS auth_source TEXT NOT NULL DEFAULT '';

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS workos_user_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_workos_user_id_unique
  ON users (workos_user_id)
  WHERE workos_user_id <> '';

ALTER TABLE organizations
  ADD COLUMN IF NOT EXISTS workos_organization_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_organizations_workos_organization_id_unique
  ON organizations (workos_organization_id)
  WHERE workos_organization_id <> '';

ALTER TABLE organization_members
  ADD COLUMN IF NOT EXISTS workos_membership_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_organization_members_workos_membership_id_unique
  ON organization_members (workos_membership_id)
  WHERE workos_membership_id <> '';
