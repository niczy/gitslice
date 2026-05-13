CREATE TABLE IF NOT EXISTS agent_session_changesets (
  session_id text NOT NULL,
  changeset_id text NOT NULL,
  snapshot_id text NOT NULL,
  snapshot_version integer DEFAULT 0 NOT NULL,
  snapshot_hash text DEFAULT '' NOT NULL,
  base_commit_hash text DEFAULT '' NOT NULL,
  exported_from_seq bigint DEFAULT 0 NOT NULL,
  runner_id text DEFAULT '' NOT NULL,
  source text DEFAULT 'local_export' NOT NULL,
  exported_at timestamp with time zone DEFAULT now() NOT NULL,
  PRIMARY KEY (session_id, changeset_id, snapshot_id)
);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'agent_session_changesets_session_id_fkey'
  ) THEN
    ALTER TABLE ONLY agent_session_changesets
      ADD CONSTRAINT agent_session_changesets_session_id_fkey
      FOREIGN KEY (session_id) REFERENCES agent_sessions(session_id) ON DELETE CASCADE;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'agent_session_changesets_changeset_id_fkey'
  ) THEN
    ALTER TABLE ONLY agent_session_changesets
      ADD CONSTRAINT agent_session_changesets_changeset_id_fkey
      FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON DELETE CASCADE;
  END IF;
END $$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'agent_session_changesets_snapshot_id_fkey'
  ) THEN
    ALTER TABLE ONLY agent_session_changesets
      ADD CONSTRAINT agent_session_changesets_snapshot_id_fkey
      FOREIGN KEY (snapshot_id) REFERENCES changeset_snapshots(id) ON DELETE CASCADE;
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_agent_session_changesets_session_exported
  ON agent_session_changesets (session_id, exported_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_session_changesets_changeset_exported
  ON agent_session_changesets (changeset_id, exported_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_session_changesets_snapshot
  ON agent_session_changesets (snapshot_id);
