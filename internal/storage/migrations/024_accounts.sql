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
