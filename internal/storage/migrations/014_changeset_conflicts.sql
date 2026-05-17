CREATE TABLE IF NOT EXISTS changeset_conflicts (
  id text PRIMARY KEY,
  changeset_id text NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
  slice_id text DEFAULT '' NOT NULL,
  path text NOT NULL,
  type text DEFAULT 'stale_base' NOT NULL,
  message text DEFAULT '' NOT NULL,
  base_version bigint DEFAULT 0 NOT NULL,
  current_version bigint DEFAULT 0 NOT NULL,
  base_hash text DEFAULT '' NOT NULL,
  ours_hash text DEFAULT '' NOT NULL,
  theirs_hash text DEFAULT '' NOT NULL,
  patch text DEFAULT '' NOT NULL,
  resolved boolean DEFAULT false NOT NULL,
  created_at timestamptz DEFAULT now() NOT NULL,
  updated_at timestamptz DEFAULT now() NOT NULL,
  resolved_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_changeset_conflicts_changeset_path
  ON changeset_conflicts(changeset_id, path);
