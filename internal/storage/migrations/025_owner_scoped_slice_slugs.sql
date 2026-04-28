UPDATE slices
SET slug = substring(slug FROM char_length(created_by) + 2)
WHERE created_by <> ''
  AND slug LIKE created_by || '/%'
  AND NOT EXISTS (
    SELECT 1
    FROM slices AS other
    WHERE other.id <> slices.id
      AND other.created_by = slices.created_by
      AND other.slug = substring(slices.slug FROM char_length(slices.created_by) + 2)
  );

UPDATE slices
SET slug = substring(slug FROM char_length(created_by) + 2) || '-' || md5(id)
WHERE created_by <> ''
  AND slug LIKE created_by || '/%';

DROP INDEX IF EXISTS idx_slices_slug;
CREATE UNIQUE INDEX IF NOT EXISTS idx_slices_owner_slug ON slices(created_by, slug);
CREATE INDEX IF NOT EXISTS idx_slices_slug ON slices(slug);
