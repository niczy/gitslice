-- Keep accepted merge writes lean. The merge hot path needs only:
--   - merge_events primary key (shard_id, merge_seq) for ordered projection
--   - merge_events unique changeset_id for idempotence/lookups
--   - home_path_heads primary key (home_id, path) for conflict CAS/listing
-- Extra event/home-head indexes multiply write amplification on every merge.
ALTER TABLE IF EXISTS merge_events
    DROP CONSTRAINT IF EXISTS merge_events_event_id_key;

DROP INDEX IF EXISTS idx_merge_events_home_seq;
DROP INDEX IF EXISTS idx_merge_events_created_at;
DROP INDEX IF EXISTS idx_home_path_heads_home_version;
DROP INDEX IF EXISTS idx_home_path_heads_home_merge_seq;
