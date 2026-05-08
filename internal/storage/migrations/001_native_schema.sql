-- Phase 1: Native Schema for PostgresNativeStorage
-- This migration creates the relational tables that replace the snapshot-based
-- storage_state blob, making PostgreSQL the source of truth for metadata.

-- ============ Core entities ============

CREATE TABLE IF NOT EXISTS slices (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    slug TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    parent_id TEXT,
    is_root BOOLEAN NOT NULL DEFAULT FALSE,
    files JSONB NOT NULL DEFAULT '[]',
    folder_mounts JSONB NOT NULL DEFAULT '[]',
    owners JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_slices_slug ON slices(slug);

CREATE TABLE IF NOT EXISTS slice_metadata (
    slice_id TEXT PRIMARY KEY REFERENCES slices(id),
    head_commit_hash TEXT NOT NULL DEFAULT '',
    modified_files JSONB NOT NULL DEFAULT '[]',
    last_modified TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    modified_files_count INT NOT NULL DEFAULT 0
);

-- ============ File indexing ============

CREATE TABLE IF NOT EXISTS file_slice_index (
    file_id TEXT NOT NULL,
    slice_id TEXT NOT NULL REFERENCES slices(id),
    PRIMARY KEY (file_id, slice_id)
);

CREATE INDEX IF NOT EXISTS idx_file_slice_index_slice ON file_slice_index(slice_id);

-- ============ Locking ============

CREATE TABLE IF NOT EXISTS slice_locks (
    slice_id TEXT PRIMARY KEY REFERENCES slices(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS file_locks (
    file_id TEXT PRIMARY KEY,
    owner_slice_id TEXT NOT NULL REFERENCES slices(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============ Changesets ============

CREATE TABLE IF NOT EXISTS changesets (
    id TEXT PRIMARY KEY,
    hash TEXT NOT NULL DEFAULT '',
    slice_id TEXT NOT NULL REFERENCES slices(id),
    base_commit_hash TEXT NOT NULL DEFAULT '',
    modified_files JSONB NOT NULL DEFAULT '[]',
    status INT NOT NULL DEFAULT 0,
    author TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    merged_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_changesets_slice ON changesets(slice_id);
CREATE INDEX IF NOT EXISTS idx_changesets_slice_status ON changesets(slice_id, status);

-- ============ Commit history ============

CREATE TABLE IF NOT EXISTS slice_commits (
    slice_id TEXT NOT NULL REFERENCES slices(id),
    seq BIGSERIAL,
    commit_hash TEXT NOT NULL,
    parent_hash TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (slice_id, seq)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_slice_commits_hash ON slice_commits(slice_id, commit_hash);
CREATE INDEX IF NOT EXISTS idx_slice_commits_slice_seq_desc ON slice_commits(slice_id, seq DESC);

-- ============ Directory entries ============

CREATE TABLE IF NOT EXISTS directory_entries (
    id TEXT PRIMARY KEY,
    slice_id TEXT NOT NULL,
    path TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'file',
    parent_id TEXT NOT NULL DEFAULT '',
    content BYTEA,
    size BIGINT NOT NULL DEFAULT 0,
    is_executable BOOLEAN NOT NULL DEFAULT FALSE,
    symlink_target TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_directory_entries_slice_path ON directory_entries(slice_id, path);
CREATE INDEX IF NOT EXISTS idx_directory_entries_parent ON directory_entries(parent_id);

-- ============ File content metadata ============
-- Actual blob bytes live in the object store; this table tracks metadata.

CREATE TABLE IF NOT EXISTS file_contents (
    file_id TEXT PRIMARY KEY,
    path TEXT NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    hash TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============ Commit snapshots ============

CREATE TABLE IF NOT EXISTS commit_snapshots (
    commit_hash TEXT PRIMARY KEY,
    slice_id TEXT NOT NULL,
    files JSONB NOT NULL DEFAULT '{}',
    committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============ Versioned content metadata ============
-- Maps content hashes to metadata. Actual bytes in object store.

CREATE TABLE IF NOT EXISTS versioned_content (
    content_hash TEXT PRIMARY KEY,
    file_id TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============ Global state ============

CREATE TABLE IF NOT EXISTS global_state (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    root_id TEXT DEFAULT '',
    global_commit_hash TEXT NOT NULL DEFAULT 'cmt_init_root',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    state_json JSONB NOT NULL DEFAULT '{}'
);

-- ============ File change history ============

CREATE TABLE IF NOT EXISTS file_changes (
    id TEXT PRIMARY KEY,
    slice_id TEXT NOT NULL,
    commit_hash TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    old_path TEXT NOT NULL DEFAULT '',
    change_type TEXT NOT NULL DEFAULT '',
    old_hash TEXT NOT NULL DEFAULT '',
    new_hash TEXT NOT NULL DEFAULT '',
    lines_added INT NOT NULL DEFAULT 0,
    lines_deleted INT NOT NULL DEFAULT 0,
    author TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_file_changes_slice_path_time ON file_changes(slice_id, path, committed_at DESC);
CREATE INDEX IF NOT EXISTS idx_file_changes_slice_time ON file_changes(slice_id, committed_at DESC);
CREATE INDEX IF NOT EXISTS idx_file_changes_commit ON file_changes(commit_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_file_changes_slice_commit_path ON file_changes(slice_id, commit_hash, path);

-- ============ Accounts ============

CREATE TABLE IF NOT EXISTS users (
    username TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS organizations (
    slug TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS organization_members (
    org_slug TEXT NOT NULL REFERENCES organizations(slug),
    username TEXT NOT NULL REFERENCES users(username),
    role TEXT NOT NULL DEFAULT 'member',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_slug, username)
);

CREATE INDEX IF NOT EXISTS idx_org_members_username ON organization_members(username);
