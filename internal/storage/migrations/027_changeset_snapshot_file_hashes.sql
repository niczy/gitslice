ALTER TABLE changeset_snapshots
    ADD COLUMN IF NOT EXISTS file_hashes JSONB;
