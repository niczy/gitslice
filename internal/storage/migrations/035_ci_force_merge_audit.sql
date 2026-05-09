-- Record CI force-merge bypasses on the accepted merge event.

ALTER TABLE merge_events
    ADD COLUMN IF NOT EXISTS forced BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS force_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS forced_by TEXT NOT NULL DEFAULT '';

