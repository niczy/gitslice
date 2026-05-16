CREATE TABLE IF NOT EXISTS content_commit_dirs (
    home_id text NOT NULL,
    dir_path text NOT NULL,
    commit_hash text NOT NULL,
    source_slice_id text DEFAULT ''::text NOT NULL,
    parent_hash text DEFAULT ''::text NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    author text DEFAULT ''::text NOT NULL,
    committed_at timestamp with time zone DEFAULT now() NOT NULL,
    merge_seq bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (home_id, dir_path, commit_hash)
);

CREATE INDEX IF NOT EXISTS idx_content_commit_dirs_lookup
    ON content_commit_dirs (home_id, dir_path, committed_at DESC, commit_hash DESC);

CREATE INDEX IF NOT EXISTS idx_content_commit_dirs_commit
    ON content_commit_dirs (commit_hash);

CREATE INDEX IF NOT EXISTS idx_content_commit_dirs_source
    ON content_commit_dirs (source_slice_id, commit_hash);

WITH raw_paths AS (
    SELECT
        me.home_id,
        me.source_slice_id,
        me.source_commit_hash AS commit_hash,
        COALESCE(update_value ->> 'parent_commit_hash', '') AS parent_hash,
        me.message,
        me.author,
        me.created_at AS committed_at,
        me.merge_seq,
        trim(both '/' FROM COALESCE(update_value ->> 'path', '')) AS path
    FROM merge_events me
    CROSS JOIN LATERAL jsonb_array_elements(me.path_updates) AS updates(update_value)
    UNION ALL
    SELECT
        me.home_id,
        me.source_slice_id,
        me.source_commit_hash AS commit_hash,
        '' AS parent_hash,
        me.message,
        me.author,
        me.created_at AS committed_at,
        me.merge_seq,
        trim(both '/' FROM touched_path) AS path
    FROM merge_events me
    CROSS JOIN LATERAL jsonb_array_elements_text(me.touched_paths) AS touched(touched_path)
    WHERE jsonb_array_length(me.path_updates) = 0
),
dir_rows AS (
    SELECT DISTINCT
        home_id,
        array_to_string((parts.path_parts)[1:g.idx], '/') AS dir_path,
        commit_hash,
        source_slice_id,
        parent_hash,
        message,
        author,
        committed_at,
        merge_seq
    FROM raw_paths
    CROSS JOIN LATERAL (SELECT string_to_array(path, '/') AS path_parts) AS parts
    CROSS JOIN LATERAL generate_series(1, array_length(parts.path_parts, 1)) AS g(idx)
    WHERE path <> ''
)
INSERT INTO content_commit_dirs (
    home_id, dir_path, commit_hash, source_slice_id, parent_hash,
    message, author, committed_at, merge_seq
)
SELECT
    home_id, dir_path, commit_hash, source_slice_id, parent_hash,
    message, author, committed_at, merge_seq
FROM dir_rows
WHERE dir_path <> ''
ON CONFLICT (home_id, dir_path, commit_hash) DO UPDATE SET
    source_slice_id = EXCLUDED.source_slice_id,
    parent_hash = EXCLUDED.parent_hash,
    message = EXCLUDED.message,
    author = EXCLUDED.author,
    committed_at = EXCLUDED.committed_at,
    merge_seq = EXCLUDED.merge_seq;
