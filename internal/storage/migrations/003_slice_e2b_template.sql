-- Add per-slice E2B template configuration.
ALTER TABLE slices ADD COLUMN IF NOT EXISTS e2b_template_id TEXT NOT NULL DEFAULT '';
