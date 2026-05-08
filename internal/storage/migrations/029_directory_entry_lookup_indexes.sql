CREATE INDEX IF NOT EXISTS idx_directory_entries_slice_parent
ON directory_entries(slice_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_directory_entries_slice_path_pattern
ON directory_entries(slice_id, path text_pattern_ops);
