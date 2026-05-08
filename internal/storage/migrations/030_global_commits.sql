-- Keep the global commit timeline append-only. The single global_state row
-- stores the current head; history is read from this table.
CREATE TABLE IF NOT EXISTS global_commits (
    seq BIGSERIAL PRIMARY KEY,
    commit_hash TEXT NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    merged_slice_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_global_commits_commit_hash ON global_commits(commit_hash);
CREATE INDEX IF NOT EXISTS idx_global_commits_seq_desc ON global_commits(seq DESC);

INSERT INTO global_commits (commit_hash, committed_at, merged_slice_ids)
SELECT
    elem->>'commit_hash',
    COALESCE(NULLIF(elem->>'timestamp', '')::timestamptz, global_state.updated_at),
    COALESCE(elem->'merged_slice_ids', '[]'::jsonb)
FROM global_state,
     jsonb_array_elements(COALESCE(state_json->'history', '[]'::jsonb)) WITH ORDINALITY AS history(elem, ord)
WHERE elem->>'commit_hash' IS NOT NULL
  AND elem->>'commit_hash' <> ''
ORDER BY history.ord DESC
ON CONFLICT (commit_hash) DO NOTHING;
