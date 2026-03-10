CREATE TABLE IF NOT EXISTS file_manifests (
    slice_id TEXT NOT NULL REFERENCES slices(id),
    path TEXT NOT NULL,
    hash TEXT NOT NULL DEFAULT '',
    total_size BIGINT NOT NULL DEFAULT 0,
    block_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (slice_id, path)
);
