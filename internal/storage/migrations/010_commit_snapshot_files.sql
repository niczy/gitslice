CREATE TABLE IF NOT EXISTS commit_snapshot_files (
    commit_hash text NOT NULL,
    path text NOT NULL,
    content_hash text NOT NULL,
    PRIMARY KEY (commit_hash, path),
    CONSTRAINT commit_snapshot_files_commit_hash_fkey
        FOREIGN KEY (commit_hash) REFERENCES commit_snapshots(commit_hash) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_commit_snapshot_files_commit_path_pattern
    ON commit_snapshot_files (commit_hash, path text_pattern_ops);

INSERT INTO commit_snapshot_files (commit_hash, path, content_hash)
SELECT cs.commit_hash, file_entry.key, file_entry.value
FROM commit_snapshots cs
CROSS JOIN LATERAL jsonb_each_text(cs.files) AS file_entry(key, value)
ON CONFLICT (commit_hash, path) DO UPDATE SET content_hash = EXCLUDED.content_hash;
