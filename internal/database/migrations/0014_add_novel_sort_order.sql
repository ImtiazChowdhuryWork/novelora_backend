ALTER TABLE novels ADD COLUMN sort_order integer NOT NULL DEFAULT 0;

WITH ordered AS (
    SELECT id, row_number() OVER (ORDER BY created_at) AS rn FROM novels
)
UPDATE novels SET sort_order = ordered.rn
FROM ordered WHERE novels.id = ordered.id;

CREATE INDEX novels_sort_order_index ON novels (sort_order) WHERE deleted_at IS NULL;
