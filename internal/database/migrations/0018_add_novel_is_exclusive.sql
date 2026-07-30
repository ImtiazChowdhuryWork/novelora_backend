-- Real flag backing the "Exclusive" section family, replacing the old
-- `sort=new` stand-in (which couldn't actually distinguish "exclusive"
-- from any other new novel). Same admin-editable tier as is_recommended.
ALTER TABLE novels ADD COLUMN is_exclusive boolean NOT NULL DEFAULT false;

CREATE INDEX novels_exclusive_index ON novels (is_exclusive) WHERE deleted_at IS NULL;
