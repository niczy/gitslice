-- Accepted merge events are the durable, replayable source for async
-- projections. This PR only adds schema and storage accessors; production merge
-- writes are switched in a later PR.
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
    UNIQUE (event_id),
    UNIQUE (changeset_id)
);

CREATE INDEX IF NOT EXISTS idx_merge_events_home_seq ON merge_events(home_id, shard_id, merge_seq);
CREATE INDEX IF NOT EXISTS idx_merge_events_created_at ON merge_events(created_at);

CREATE TABLE IF NOT EXISTS projection_offsets (
    projection_name TEXT NOT NULL,
    shard_id INTEGER NOT NULL,
    merge_seq BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (projection_name, shard_id)
);
