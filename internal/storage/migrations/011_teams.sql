CREATE TABLE IF NOT EXISTS teams (
  team_id TEXT PRIMARY KEY,
  org_slug TEXT NOT NULL REFERENCES organizations(slug) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_by TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_teams_org_name_unique
  ON teams(org_slug, lower(name));

CREATE INDEX IF NOT EXISTS idx_teams_org
  ON teams(org_slug);

CREATE TABLE IF NOT EXISTS team_members (
  team_id TEXT NOT NULL REFERENCES teams(team_id) ON DELETE CASCADE,
  username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (team_id, username)
);

CREATE INDEX IF NOT EXISTS idx_team_members_username
  ON team_members(username);
