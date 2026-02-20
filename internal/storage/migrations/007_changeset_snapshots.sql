CREATE TABLE IF NOT EXISTS changeset_snapshots (
    id TEXT PRIMARY KEY,
    changeset_id TEXT NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
    version INT NOT NULL,
    hash TEXT NOT NULL DEFAULT '',
    base_commit_hash TEXT NOT NULL DEFAULT '',
    modified_files JSONB NOT NULL DEFAULT '[]',
    author TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (changeset_id, version)
);

CREATE INDEX IF NOT EXISTS idx_changeset_snapshots_changeset ON changeset_snapshots(changeset_id, version DESC);
