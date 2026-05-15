CREATE INDEX IF NOT EXISTS idx_merge_events_source_commit_hash
  ON merge_events (source_commit_hash, created_at DESC);
