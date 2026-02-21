CREATE TABLE IF NOT EXISTS organization_invites (
  invite_id TEXT PRIMARY KEY,
  org_slug TEXT NOT NULL REFERENCES organizations(slug) ON DELETE CASCADE,
  target_email TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'member',
  status TEXT NOT NULL DEFAULT 'pending',
  created_by TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_organization_invites_org
  ON organization_invites(org_slug);

CREATE UNIQUE INDEX IF NOT EXISTS idx_organization_invites_pending_email_unique
  ON organization_invites(org_slug, lower(target_email))
  WHERE status = 'pending';
