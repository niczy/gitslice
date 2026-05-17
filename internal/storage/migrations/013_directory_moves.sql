ALTER TABLE changeset_snapshots
  ADD COLUMN IF NOT EXISTS directory_moves jsonb;

CREATE TABLE IF NOT EXISTS directory_moves (
  move_id text PRIMARY KEY,
  home_id text NOT NULL,
  source_slice_id text DEFAULT '' NOT NULL,
  source_commit_hash text DEFAULT '' NOT NULL,
  old_prefix text NOT NULL,
  new_prefix text NOT NULL,
  base_subtree_version bigint DEFAULT 0 NOT NULL,
  base_subtree_digest text DEFAULT '' NOT NULL,
  new_subtree_version bigint DEFAULT 0 NOT NULL,
  merge_seq bigint DEFAULT 0 NOT NULL,
  created_at timestamptz DEFAULT now() NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_directory_moves_home_merge_seq
  ON directory_moves(home_id, merge_seq);
