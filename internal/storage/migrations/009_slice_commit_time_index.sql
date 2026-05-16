CREATE INDEX IF NOT EXISTS idx_slice_commits_slice_time_hash
    ON slice_commits (slice_id, committed_at DESC, commit_hash DESC);
