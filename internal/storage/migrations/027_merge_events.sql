-- Durable merge event store (split from 001_native_schema.sql for existing databases that
-- already applied the earlier version of 001 without these tables).

CREATE TABLE IF NOT EXISTS merge_events (
    home_id TEXT NOT NULL,
    shard_id INTEGER NOT NULL,
    merge_seq BIGINT NOT NULL,
    event_id TEXT NOT NULL,
    changeset_id TEXT NOT NULL,
    source_slice_id TEXT NOT NULL,
    source_commit_hash TEXT NOT NULL,
    author TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    touched_paths JSONB NOT NULL DEFAULT '[]'::jsonb,
    path_updates JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (shard_id, merge_seq),
    UNIQUE (changeset_id)
);

CREATE TABLE IF NOT EXISTS projection_offsets (
    projection_name TEXT NOT NULL,
    shard_id INTEGER NOT NULL,
    merge_seq BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (projection_name, shard_id)
);

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

CREATE TABLE IF NOT EXISTS merge_event_shard_sequences (
    shard_id INTEGER PRIMARY KEY,
    next_seq BIGINT NOT NULL DEFAULT 1
);
