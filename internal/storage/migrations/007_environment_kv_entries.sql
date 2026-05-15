CREATE TABLE IF NOT EXISTS environment_kv_entries (
    id text NOT NULL,
    home_id text NOT NULL,
    slice_id text DEFAULT ''::text NOT NULL,
    slice_slug text DEFAULT ''::text NOT NULL,
    profile text DEFAULT 'default'::text NOT NULL,
    key text NOT NULL,
    class text NOT NULL CHECK (class IN ('secret', 'value')),
    encrypted_value bytea NOT NULL,
    value_hash text DEFAULT ''::text NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    created_by text DEFAULT ''::text NOT NULL,
    updated_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone
);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'environment_kv_entries_pkey'
  ) THEN
    ALTER TABLE ONLY environment_kv_entries
      ADD CONSTRAINT environment_kv_entries_pkey PRIMARY KEY (id);
  END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_environment_kv_entries_active_key
  ON environment_kv_entries (home_id, slice_id, profile, key, class)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_environment_kv_entries_scope
  ON environment_kv_entries (home_id, slice_id, profile)
  WHERE deleted_at IS NULL;
