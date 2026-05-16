ALTER TABLE changeset_snapshots
  ADD COLUMN IF NOT EXISTS rename_sources jsonb;
