-- Keep merge sequence allocation O(1) per shard. The previous allocator used
-- MAX(merge_seq) under an advisory lock, which gets expensive in the merge hot
-- path as the event table grows.
CREATE TABLE IF NOT EXISTS merge_event_shard_sequences (
    shard_id INTEGER PRIMARY KEY,
    next_seq BIGINT NOT NULL DEFAULT 1,
    CHECK (next_seq >= 1)
);

INSERT INTO merge_event_shard_sequences (shard_id, next_seq)
SELECT shard_id, COALESCE(MAX(merge_seq), 0) + 1
FROM merge_events
GROUP BY shard_id
ON CONFLICT (shard_id) DO UPDATE
SET next_seq = GREATEST(merge_event_shard_sequences.next_seq, EXCLUDED.next_seq);
