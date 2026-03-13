ALTER TABLE slices
  ADD COLUMN IF NOT EXISTS slug TEXT NOT NULL DEFAULT '';

UPDATE slices
SET slug = CASE
  WHEN slug <> '' THEN slug
  WHEN is_root THEN 'root'
  WHEN COALESCE(created_by, '') <> '' THEN created_by || '/' || id
  ELSE id
END
WHERE slug = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_slices_slug ON slices(slug);
