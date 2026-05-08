-- Normalize stored IDs to the current taxonomy:
--   root slice: root
--   home slices: home_<username>
--   generated slices: sl_<opaque>
--   changesets: chg_<sequence-or-name>
--   synthetic commits: cmt_<opaque> / cmt_init_<slice>
--   changeset versions: chgver_<opaque>
--   changeset snapshots: chgsnap_<changeset>_v<version>
--   file changes: fc_<opaque>

ALTER TABLE IF EXISTS slice_metadata DROP CONSTRAINT IF EXISTS slice_metadata_slice_id_fkey;
ALTER TABLE IF EXISTS file_slice_index DROP CONSTRAINT IF EXISTS file_slice_index_slice_id_fkey;
ALTER TABLE IF EXISTS slice_locks DROP CONSTRAINT IF EXISTS slice_locks_slice_id_fkey;
ALTER TABLE IF EXISTS file_locks DROP CONSTRAINT IF EXISTS file_locks_owner_slice_id_fkey;
ALTER TABLE IF EXISTS changesets DROP CONSTRAINT IF EXISTS changesets_slice_id_fkey;
ALTER TABLE IF EXISTS slice_commits DROP CONSTRAINT IF EXISTS slice_commits_slice_id_fkey;
ALTER TABLE IF EXISTS file_manifests DROP CONSTRAINT IF EXISTS file_manifests_slice_id_fkey;
ALTER TABLE IF EXISTS repo_bindings DROP CONSTRAINT IF EXISTS repo_bindings_slice_id_fkey;
ALTER TABLE IF EXISTS changeset_snapshots DROP CONSTRAINT IF EXISTS changeset_snapshots_changeset_id_fkey;

DO $$
BEGIN
  IF to_regclass('global_state') IS NOT NULL THEN
    IF EXISTS (
      SELECT 1
      FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = 'global_state'
        AND column_name = 'root_slice_id'
    ) AND NOT EXISTS (
      SELECT 1
      FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = 'global_state'
        AND column_name = 'root_id'
    ) THEN
      ALTER TABLE global_state RENAME COLUMN root_slice_id TO root_id;
    ELSIF NOT EXISTS (
      SELECT 1
      FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = 'global_state'
        AND column_name = 'root_id'
    ) THEN
      ALTER TABLE global_state ADD COLUMN root_id TEXT DEFAULT '';
    ELSIF EXISTS (
      SELECT 1
      FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = 'global_state'
        AND column_name = 'root_slice_id'
    ) THEN
      UPDATE global_state
      SET root_id = COALESCE(NULLIF(root_id, ''), root_slice_id);
      ALTER TABLE global_state DROP COLUMN root_slice_id;
    END IF;
  END IF;
END $$;

CREATE TEMP TABLE id_taxonomy_slice_map AS
WITH candidates AS (
  SELECT
    id AS old_id,
    CASE
      WHEN id = 'root_slice' THEN 'root'
      WHEN id LIKE 'home.%' THEN 'home_' || substring(id FROM 6)
      WHEN id LIKE 'sl-%' THEN 'sl_' || replace(substring(id FROM 4), '-', '')
      ELSE id
    END AS candidate_id
  FROM slices
)
SELECT
  old_id,
  CASE
    WHEN candidate_id <> old_id
      AND EXISTS (SELECT 1 FROM slices s WHERE s.id = candidate_id AND s.id <> old_id)
    THEN candidate_id || '_' || substr(md5(old_id), 1, 8)
    ELSE candidate_id
  END AS new_id
FROM candidates
WHERE old_id <> candidate_id;

UPDATE slice_metadata t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE file_slice_index t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE slice_locks t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE file_locks t SET owner_slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.owner_slice_id = m.old_id;
UPDATE changesets t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE slice_commits t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE directory_entries t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE file_manifests t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE commit_snapshots t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE file_changes t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE repo_bindings t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE agent_sessions t SET slice_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.slice_id = m.old_id;
UPDATE slices t SET parent_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.parent_id = m.old_id;
UPDATE global_state t SET root_id = m.new_id FROM id_taxonomy_slice_map m WHERE t.root_id = m.old_id;

UPDATE directory_entries t
SET id = m.new_id || substring(t.id FROM char_length(m.old_id) + 1)
FROM id_taxonomy_slice_map m
WHERE t.id = m.old_id OR t.id LIKE m.old_id || ':%';

UPDATE directory_entries t
SET parent_id = m.new_id || substring(t.parent_id FROM char_length(m.old_id) + 1)
FROM id_taxonomy_slice_map m
WHERE t.parent_id = m.old_id OR t.parent_id LIKE m.old_id || ':%';

UPDATE repo_bindings t
SET binding_id = m.new_id || substring(t.binding_id FROM char_length(m.old_id) + 1)
FROM id_taxonomy_slice_map m
WHERE t.binding_id = m.old_id OR t.binding_id LIKE m.old_id || ':%';

UPDATE slices t SET id = m.new_id FROM id_taxonomy_slice_map m WHERE t.id = m.old_id;

CREATE TEMP TABLE id_taxonomy_changeset_map AS
WITH candidates AS (
  SELECT
    id AS old_id,
    CASE
      WHEN id ~ '^cs-global-[0-9]+$' THEN 'chg_' || substring(id FROM 11)
      WHEN id ~ '^cs-[0-9]+$' THEN 'chg_' || substring(id FROM 4)
      WHEN id LIKE 'cs-%' THEN 'chg_' || regexp_replace(regexp_replace(substring(id FROM 4), '[^A-Za-z0-9]+', '_', 'g'), '(^_+|_+$)', '', 'g')
      ELSE id
    END AS candidate_id
  FROM changesets
)
SELECT
  old_id,
  CASE
    WHEN candidate_id <> old_id
      AND EXISTS (SELECT 1 FROM changesets c WHERE c.id = candidate_id AND c.id <> old_id)
    THEN candidate_id || '_' || substr(md5(old_id), 1, 8)
    ELSE candidate_id
  END AS new_id
FROM candidates
WHERE old_id <> candidate_id;

UPDATE changeset_snapshots t SET changeset_id = m.new_id FROM id_taxonomy_changeset_map m WHERE t.changeset_id = m.old_id;
UPDATE changesets t SET id = m.new_id FROM id_taxonomy_changeset_map m WHERE t.id = m.old_id;

CREATE TEMP TABLE id_taxonomy_commit_map AS
WITH ids(old_id) AS (
  SELECT head_commit_hash FROM slice_metadata WHERE head_commit_hash <> ''
  UNION SELECT global_commit_hash FROM global_state WHERE global_commit_hash <> ''
  UNION SELECT commit_hash FROM slice_commits WHERE commit_hash <> ''
  UNION SELECT parent_hash FROM slice_commits WHERE parent_hash <> ''
  UNION SELECT commit_hash FROM commit_snapshots WHERE commit_hash <> ''
  UNION SELECT commit_hash FROM file_changes WHERE commit_hash <> ''
),
candidates AS (
  SELECT
    old_id,
    CASE
      WHEN old_id IN ('global-init', 'root-initial', 'init-root_slice', 'init-root') THEN 'cmt_init_root'
      WHEN old_id LIKE 'init-%' THEN 'cmt_init_' || regexp_replace(regexp_replace(substring(old_id FROM 6), '[^A-Za-z0-9]+', '_', 'g'), '(^_+|_+$)', '', 'g')
      WHEN old_id ~ '^(commit|global|fs|git|merged|home-backfill|root-commit)-' THEN 'cmt_' || md5(old_id)
      ELSE old_id
    END AS candidate_id
  FROM ids
)
SELECT old_id, candidate_id AS new_id
FROM candidates
WHERE old_id <> candidate_id;

UPDATE slice_metadata t SET head_commit_hash = m.new_id FROM id_taxonomy_commit_map m WHERE t.head_commit_hash = m.old_id;
UPDATE global_state t SET global_commit_hash = m.new_id FROM id_taxonomy_commit_map m WHERE t.global_commit_hash = m.old_id;
UPDATE slice_commits t SET parent_hash = m.new_id FROM id_taxonomy_commit_map m WHERE t.parent_hash = m.old_id;
UPDATE slice_commits t SET commit_hash = m.new_id FROM id_taxonomy_commit_map m WHERE t.commit_hash = m.old_id;
UPDATE commit_snapshots t SET commit_hash = m.new_id FROM id_taxonomy_commit_map m WHERE t.commit_hash = m.old_id;
UPDATE file_changes t SET commit_hash = m.new_id FROM id_taxonomy_commit_map m WHERE t.commit_hash = m.old_id;

UPDATE changesets
SET hash = 'chgver_' || md5(hash)
WHERE hash <> '' AND hash !~ '^chgver_';

UPDATE changeset_snapshots
SET hash = 'chgver_' || md5(hash)
WHERE hash <> '' AND hash !~ '^chgver_';

UPDATE changeset_snapshots
SET id = 'chgsnap_' ||
  regexp_replace(regexp_replace(changeset_id, '[^A-Za-z0-9]+', '_', 'g'), '(^_+|_+$)', '', 'g') ||
  '_v' || version::text
WHERE id !~ '^chgsnap_';

UPDATE file_changes
SET id = 'fc_' || md5(commit_hash || '|' || path || '|' || slice_id)
WHERE id !~ '^fc_';

SELECT setval(
  'changeset_id_seq',
  GREATEST(
    COALESCE((SELECT MAX((regexp_match(id, '^chg_([0-9]+)$'))[1]::bigint) FROM changesets WHERE id ~ '^chg_[0-9]+$'), 0),
    1
  ),
  true
);

ALTER TABLE IF EXISTS slice_metadata
  ADD CONSTRAINT slice_metadata_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS file_slice_index
  ADD CONSTRAINT file_slice_index_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS slice_locks
  ADD CONSTRAINT slice_locks_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS file_locks
  ADD CONSTRAINT file_locks_owner_slice_id_fkey FOREIGN KEY (owner_slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS changesets
  ADD CONSTRAINT changesets_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS slice_commits
  ADD CONSTRAINT slice_commits_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS file_manifests
  ADD CONSTRAINT file_manifests_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS repo_bindings
  ADD CONSTRAINT repo_bindings_slice_id_fkey FOREIGN KEY (slice_id) REFERENCES slices(id) ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE IF EXISTS changeset_snapshots
  ADD CONSTRAINT changeset_snapshots_changeset_id_fkey FOREIGN KEY (changeset_id) REFERENCES changesets(id) ON UPDATE CASCADE ON DELETE CASCADE;
