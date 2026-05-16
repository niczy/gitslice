ALTER TABLE home_path_heads
    ADD COLUMN IF NOT EXISTS entry_type text DEFAULT 'file'::text NOT NULL;

CREATE TABLE IF NOT EXISTS path_head_children (
    home_id text NOT NULL,
    dir_path text NOT NULL,
    child_name text NOT NULL,
    child_path text NOT NULL,
    entry_type text DEFAULT 'file'::text NOT NULL,
    content_hash text DEFAULT ''::text NOT NULL,
    manifest_hash text DEFAULT ''::text NOT NULL,
    source_slice_id text DEFAULT ''::text NOT NULL,
    source_commit_hash text DEFAULT ''::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (home_id, dir_path, child_name)
);

CREATE INDEX IF NOT EXISTS idx_path_head_children_dir
    ON path_head_children (home_id, dir_path, child_name);

WITH active_heads AS (
    SELECT
        home_id,
        path,
        COALESCE(NULLIF(entry_type, ''), 'file') AS entry_type,
        content_hash,
        manifest_hash,
        source_slice_id,
        source_commit_hash,
        updated_at,
        string_to_array(path, '/') AS parts
    FROM home_path_heads
    WHERE path <> '' AND deleted = false
),
children AS (
    SELECT DISTINCT ON (home_id, dir_path, child_name)
        home_id,
        CASE WHEN idx <= 1 THEN '' ELSE COALESCE(array_to_string(parts[1:(idx - 1)], '/'), '') END AS dir_path,
        parts[idx] AS child_name,
        array_to_string(parts[1:idx], '/') AS child_path,
        CASE
            WHEN idx = array_length(parts, 1) THEN entry_type
            ELSE 'directory'
        END AS entry_type,
        CASE WHEN idx = array_length(parts, 1) THEN content_hash ELSE '' END AS content_hash,
        CASE WHEN idx = array_length(parts, 1) THEN manifest_hash ELSE '' END AS manifest_hash,
        CASE WHEN idx = array_length(parts, 1) THEN source_slice_id ELSE '' END AS source_slice_id,
        CASE WHEN idx = array_length(parts, 1) THEN source_commit_hash ELSE '' END AS source_commit_hash,
        updated_at
    FROM active_heads
    CROSS JOIN LATERAL generate_series(1, array_length(parts, 1)) AS g(idx)
    ORDER BY home_id, dir_path, child_name, updated_at DESC
)
INSERT INTO path_head_children (
    home_id, dir_path, child_name, child_path, entry_type,
    content_hash, manifest_hash, source_slice_id, source_commit_hash, updated_at
)
SELECT
    home_id, dir_path, child_name, child_path, entry_type,
    content_hash, manifest_hash, source_slice_id, source_commit_hash, updated_at
FROM children
ON CONFLICT (home_id, dir_path, child_name) DO UPDATE SET
    child_path = EXCLUDED.child_path,
    entry_type = EXCLUDED.entry_type,
    content_hash = EXCLUDED.content_hash,
    manifest_hash = EXCLUDED.manifest_hash,
    source_slice_id = EXCLUDED.source_slice_id,
    source_commit_hash = EXCLUDED.source_commit_hash,
    updated_at = EXCLUDED.updated_at;
