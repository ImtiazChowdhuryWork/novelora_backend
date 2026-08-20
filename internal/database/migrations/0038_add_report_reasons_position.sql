-- Drag-reorder support for the admin's "Report Types" list.
ALTER TABLE report_reasons ADD COLUMN position integer NOT NULL DEFAULT 0;

-- Backfill existing rows with their current creation order so the
-- list doesn't visibly reshuffle the moment this ships.
WITH ordered AS (
    SELECT id, row_number() OVER (ORDER BY created_at) - 1 AS position
    FROM report_reasons
)
UPDATE report_reasons
SET position = ordered.position
FROM ordered
WHERE report_reasons.id = ordered.id;
