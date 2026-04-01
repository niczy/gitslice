ALTER TABLE slices
  ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private';

UPDATE slices
SET visibility = 'private'
WHERE COALESCE(NULLIF(BTRIM(visibility), ''), 'private') <> visibility;

CREATE TABLE IF NOT EXISTS path_visibility (
  path TEXT PRIMARY KEY,
  entry_type TEXT NOT NULL DEFAULT 'file',
  visibility TEXT NOT NULL DEFAULT 'private',
  updated_by TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_path_visibility_path_prefix ON path_visibility(path);
