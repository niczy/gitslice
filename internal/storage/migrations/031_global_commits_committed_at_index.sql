-- Global history is read newest-first by commit timestamp. The sequence remains
-- a deterministic tie-breaker for commits with the same timestamp.
CREATE INDEX IF NOT EXISTS idx_global_commits_committed_at_desc ON global_commits(committed_at DESC, seq DESC);
