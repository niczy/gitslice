CREATE TABLE IF NOT EXISTS repo_bindings (
    binding_id TEXT PRIMARY KEY,
    owner_username TEXT NOT NULL REFERENCES users(username),
    slice_id TEXT NOT NULL REFERENCES slices(id),
    root_path TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'github',
    repo_url TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    push_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    last_imported_commit TEXT NOT NULL DEFAULT '',
    last_pushed_commit TEXT NOT NULL DEFAULT '',
    last_seen_remote_commit TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (slice_id, root_path)
);

CREATE INDEX IF NOT EXISTS idx_repo_bindings_owner_username ON repo_bindings(owner_username);
