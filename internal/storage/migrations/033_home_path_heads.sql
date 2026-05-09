-- Home-scoped path heads are the future conflict authority for merge acceptance.
-- This migration only adds schema; merge behavior still uses the existing path.
CREATE TABLE IF NOT EXISTS home_path_heads (
    home_id TEXT NOT NULL,
    path TEXT NOT NULL,
    path_version BIGINT NOT NULL DEFAULT 1,
    content_hash TEXT NOT NULL DEFAULT '',
    manifest_hash TEXT NOT NULL DEFAULT '',
    source_slice_id TEXT NOT NULL DEFAULT '',
    source_commit_hash TEXT NOT NULL DEFAULT '',
    last_merge_seq BIGINT NOT NULL DEFAULT 0,
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (home_id, path),
    CHECK (path_version >= 0),
    CHECK (last_merge_seq >= 0)
);
