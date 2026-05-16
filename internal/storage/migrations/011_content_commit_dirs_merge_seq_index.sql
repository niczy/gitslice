CREATE INDEX IF NOT EXISTS idx_content_commit_dirs_seq_lookup
    ON content_commit_dirs (home_id, dir_path, merge_seq, committed_at DESC, commit_hash DESC);
