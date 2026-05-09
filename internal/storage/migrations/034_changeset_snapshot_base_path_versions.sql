ALTER TABLE changeset_snapshots
    ADD COLUMN IF NOT EXISTS base_path_versions JSONB;
